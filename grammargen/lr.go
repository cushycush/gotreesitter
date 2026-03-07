package grammargen

import (
	"fmt"
	"strings"
)

// coreEntry is a core item (prodIdx, dot) with a bitset of lookahead terminals.
// This avoids expanding N lookaheads into N individual lrItems during closure.
type coreEntry struct {
	prodIdx    int
	dot        int
	lookaheads bitset
}

// lrItemSet is a set of LR(1) items stored in core-based representation.
type lrItemSet struct {
	// cores is the core-based representation: one entry per (prodIdx, dot).
	cores []coreEntry
	// coreIndex maps (prodIdx, dot) → index in cores for fast lookup.
	coreIndex map[coreItem]int
	// coreHash is a hash of the core items only (without lookaheads).
	coreHash uint64
	// fullHash is a hash of core items + all lookaheads.
	fullHash uint64
	// reduceLAHash is a hash of only the reduce-item lookaheads (for extended merging).
	reduceLAHash uint64
}

// lrAction is a parse table action.
type lrAction struct {
	kind    lrActionKind
	state   int   // shift target / goto target
	prodIdx int   // reduce production index
	prec    int   // for shift: precedence of the item's production
	assoc   Assoc // for shift: associativity of the item's production
	lhsSym  int   // LHS nonterminal of the production (for conflict detection)
	lhsSyms []int // additional LHS symbols (when shifts from multiple rules merge)
	isExtra bool  // true if this action comes from a nonterminal extra production
}

type lrActionKind int

const (
	lrShift lrActionKind = iota
	lrReduce
	lrAccept
)

// LRTables holds the generated parse tables.
type LRTables struct {
	// ActionTable[state][symbol] = list of actions (multiple = conflict/GLR)
	ActionTable map[int]map[int][]lrAction
	GotoTable   map[int]map[int]int // [state][nonterminal] → target state
	StateCount  int
}

// buildLRTables constructs LR(1) parse tables from a normalized grammar.
func buildLRTables(ng *NormalizedGrammar) (*LRTables, error) {
	ctx := &lrContext{
		ng:         ng,
		firstSets:  make([]bitset, len(ng.Symbols)),
		nullables:  make([]bool, len(ng.Symbols)),
		prodsByLHS: make(map[int][]int),
		betaCache:  make(map[uint32]*betaResult),
	}

	tokenCount := ng.TokenCount()
	ctx.tokenCount = tokenCount

	// Build production-by-LHS index for fast closure lookups.
	for i := range ng.Productions {
		lhs := ng.Productions[i].LHS
		ctx.prodsByLHS[lhs] = append(ctx.prodsByLHS[lhs], i)
	}

	// Identify nonterminal extra productions and all terminals for injection.
	for i := range ng.Productions {
		if ng.Productions[i].IsExtra {
			ctx.extraProdIndices = append(ctx.extraProdIndices, i)
		}
	}
	if len(ctx.extraProdIndices) > 0 {
		ctx.allTerminals = newBitset(tokenCount)
		for i := 0; i < tokenCount; i++ {
			ctx.allTerminals.add(i)
		}
	}

	// Pre-allocate dot-0 index for fast closure lookups.
	ctx.dot0Index = make([]int, len(ng.Productions))
	for i := range ctx.dot0Index {
		ctx.dot0Index[i] = -1
	}

	// Compute FIRST and nullable sets.
	ctx.computeFirstSets()

	// Build item sets. Use DeRemer/Pennello LALR for large grammars (>400 productions)
	// which would otherwise be slow with the iterative LR(1) construction.
	var itemSets []lrItemSet
	if len(ng.Productions) > 400 {
		itemSets = ctx.buildItemSetsLALR()
	} else {
		itemSets = ctx.buildItemSets()
	}

	// Build action and goto tables.
	tables := &LRTables{
		ActionTable: make(map[int]map[int][]lrAction),
		GotoTable:   make(map[int]map[int]int),
		StateCount:  len(itemSets),
	}

	for stateIdx, itemSet := range itemSets {
		tables.ActionTable[stateIdx] = make(map[int][]lrAction)
		tables.GotoTable[stateIdx] = make(map[int]int)

		// Use pre-computed transitions instead of recomputing gotoState.
		trans := ctx.transitions[stateIdx]

		for _, ce := range itemSet.cores {
			prod := &ng.Productions[ce.prodIdx]

			if ce.dot < len(prod.RHS) {
				// Dot not at end → shift or goto
				nextSym := prod.RHS[ce.dot]
				targetState, ok := trans[nextSym]
				if !ok {
					continue
				}

				if nextSym < tokenCount {
					// Terminal → shift action
					tables.addAction(stateIdx, nextSym, lrAction{
						kind:    lrShift,
						state:   targetState,
						prec:    prod.Prec,
						assoc:   prod.Assoc,
						lhsSym:  prod.LHS,
						isExtra: prod.IsExtra,
					})
				} else {
					// Nonterminal → goto
					tables.GotoTable[stateIdx][nextSym] = targetState
				}
			} else {
				// Dot at end → reduce or accept
				if ce.prodIdx == ng.AugmentProdID {
					// Augmented start production → accept
					tables.addAction(stateIdx, 0, lrAction{kind: lrAccept})
				} else {
					// Regular reduce — one action per lookahead terminal.
					ce.lookaheads.forEach(func(la int) {
						tables.addAction(stateIdx, la, lrAction{
							kind:    lrReduce,
							prodIdx: ce.prodIdx,
							lhsSym:  prod.LHS,
							isExtra: prod.IsExtra,
						})
					})
				}
			}
		}
	}

	return tables, nil
}

func (t *LRTables) addAction(state, sym int, action lrAction) {
	existing := t.ActionTable[state][sym]
	// Avoid duplicates.
	for i, a := range existing {
		if a.kind == action.kind && a.state == action.state {
			if a.kind == lrShift {
				// For shifts to the same target, keep the higher prec.
				if !a.isExtra && action.isExtra {
					return // existing non-extra wins
				}
				if a.isExtra && !action.isExtra {
					existing[i].isExtra = false
				}
				if action.prec > a.prec {
					existing[i].prec = action.prec
					existing[i].assoc = action.assoc
				}
				// Accumulate all contributing LHS symbols for conflict detection.
				if action.lhsSym != a.lhsSym && action.lhsSym != 0 {
					found := false
					for _, s := range existing[i].lhsSyms {
						if s == action.lhsSym {
							found = true
							break
						}
					}
					if !found {
						existing[i].lhsSyms = append(existing[i].lhsSyms, action.lhsSym)
					}
				}
				return
			}
			if a.prodIdx == action.prodIdx {
				return
			}
		}
	}
	t.ActionTable[state][sym] = append(existing, action)
}

// lrContext holds state during LR table construction.
type lrContext struct {
	ng         *NormalizedGrammar
	tokenCount int
	firstSets  []bitset // symbol → bitset of terminal first symbols
	nullables  []bool   // symbol → can derive ε

	// Production index: LHS symbol → production indices
	prodsByLHS map[int][]int

	// FIRST(β) cache: packed (prodIdx, dot) → first set + nullable flag
	betaCache map[uint32]*betaResult

	// Item set management
	itemSets    []lrItemSet
	transitions map[int]map[int]int

	// Fast dot-0 lookup: prodIdx → cores slice index (-1 = absent).
	// Allocated once, reused across closureToSet calls.
	dot0Index []int
	dot0Dirty []int // indices to reset between calls

	// Nonterminal extra support
	extraProdIndices []int
	allTerminals     bitset // all terminal symbol IDs
}

// addNonterminalExtraChains creates dedicated parse state chains for nonterminal
// extra productions and adds shift actions from every main state.
func addNonterminalExtraChains(tables *LRTables, ng *NormalizedGrammar) {
	tokenCount := ng.TokenCount()
	if len(ng.ExtraSymbols) == 0 {
		return
	}

	var extraProds []int
	for i := range ng.Productions {
		if ng.Productions[i].IsExtra && len(ng.Productions[i].RHS) > 0 {
			extraProds = append(extraProds, i)
		}
	}
	if len(extraProds) == 0 {
		return
	}

	mainStateCount := tables.StateCount

	mainValidTerminals := make(map[int]bool)
	for state := 0; state < mainStateCount; state++ {
		for sym := range tables.ActionTable[state] {
			if sym < tokenCount {
				mainValidTerminals[sym] = true
			}
		}
	}
	for _, e := range ng.ExtraSymbols {
		if e > 0 && e < tokenCount {
			mainValidTerminals[e] = true
		}
	}
	mainValidTerminals[0] = true
	// Include the first terminal of each nonterminal extra production.
	// Without this, consecutive nonterminal extras (e.g., two comments in a
	// row) fail because the chain's reduce state has no action for the start
	// token of the next extra, preventing the reduce from firing.
	for _, prodIdx := range extraProds {
		prod := &ng.Productions[prodIdx]
		if len(prod.RHS) > 0 && prod.RHS[0] < tokenCount {
			mainValidTerminals[prod.RHS[0]] = true
		}
	}

	for _, prodIdx := range extraProds {
		prod := &ng.Productions[prodIdx]
		rhsLen := len(prod.RHS)

		chainStart := tables.StateCount
		for i := 0; i < rhsLen; i++ {
			stateIdx := chainStart + i
			tables.ActionTable[stateIdx] = make(map[int][]lrAction)
			tables.GotoTable[stateIdx] = make(map[int]int)

			if i < rhsLen-1 {
				nextSym := prod.RHS[i+1]
				if nextSym < tokenCount {
					tables.ActionTable[stateIdx][nextSym] = []lrAction{{
						kind:    lrShift,
						state:   stateIdx + 1,
						isExtra: true,
					}}
				}
			} else {
				for t := range mainValidTerminals {
					tables.ActionTable[stateIdx][t] = []lrAction{{
						kind:    lrReduce,
						prodIdx: prodIdx,
						lhsSym:  prod.LHS,
						isExtra: true,
					}}
				}
			}

			for _, extraSym := range ng.ExtraSymbols {
				if extraSym < tokenCount {
					if _, ok := tables.ActionTable[stateIdx][extraSym]; !ok {
						tables.ActionTable[stateIdx][extraSym] = []lrAction{{
							kind:    lrShift,
							state:   stateIdx,
							isExtra: true,
						}}
					}
				}
			}
		}
		tables.StateCount += rhsLen

		firstSym := prod.RHS[0]
		if firstSym >= tokenCount {
			continue
		}
		for state := 0; state < mainStateCount; state++ {
			if _, ok := tables.ActionTable[state][firstSym]; !ok {
				tables.ActionTable[state][firstSym] = []lrAction{{
					kind:    lrShift,
					state:   chainStart,
					isExtra: true,
				}}
			}
		}
	}
}

// computeFirstSets computes FIRST sets for all symbols using bitsets.
func (ctx *lrContext) computeFirstSets() {
	ng := ctx.ng
	tokenCount := ctx.tokenCount

	// Initialize: terminals have FIRST = {self}
	for i, sym := range ng.Symbols {
		ctx.firstSets[i] = newBitset(tokenCount)
		if sym.Kind == SymbolTerminal || sym.Kind == SymbolNamedToken || sym.Kind == SymbolExternal {
			ctx.firstSets[i].add(i)
		}
	}

	// Compute nullables.
	changed := true
	for changed {
		changed = false
		for _, prod := range ng.Productions {
			if ctx.nullables[prod.LHS] {
				continue
			}
			nullable := true
			for _, sym := range prod.RHS {
				if sym < tokenCount || !ctx.nullables[sym] {
					nullable = false
					break
				}
			}
			if nullable {
				ctx.nullables[prod.LHS] = true
				changed = true
			}
		}
	}

	// Iterate until fixed point.
	changed = true
	for changed {
		changed = false
		for _, prod := range ng.Productions {
			for _, sym := range prod.RHS {
				if ctx.firstSets[prod.LHS].unionWith(&ctx.firstSets[sym]) {
					changed = true
				}
				if sym >= tokenCount && ctx.nullables[sym] {
					continue
				}
				break
			}
		}
	}
}

// firstOfSequence computes FIRST(β) for a sequence of symbols.
func (ctx *lrContext) firstOfSequence(syms []int) bitset {
	result := newBitset(ctx.tokenCount)
	for _, sym := range syms {
		result.unionWith(&ctx.firstSets[sym])
		if sym < ctx.tokenCount || !ctx.nullables[sym] {
			return result
		}
	}
	return result
}

// coreItem identifies an LR(0) core (production + dot position).
type coreItem struct {
	prodIdx, dot int
}

// closureToSet computes the closure of kernel items and returns an lrItemSet
// using core-based representation with bitset lookaheads.
func (ctx *lrContext) closureToSet(kernel []coreEntry) lrItemSet {
	ng := ctx.ng
	tokenCount := ctx.tokenCount

	// Reset dot0Index from previous call.
	for _, pi := range ctx.dot0Dirty {
		ctx.dot0Index[pi] = -1
	}
	ctx.dot0Dirty = ctx.dot0Dirty[:0]

	// Build initial core index from kernel.
	coreIdx := make(map[coreItem]int, len(kernel)*2)
	cores := make([]coreEntry, 0, len(kernel)*2)
	for _, ke := range kernel {
		c := coreItem{ke.prodIdx, ke.dot}
		if idx, ok := coreIdx[c]; ok {
			cores[idx].lookaheads.unionWith(&ke.lookaheads)
		} else {
			idx := len(cores)
			coreIdx[c] = idx
			cores = append(cores, coreEntry{
				prodIdx:    ke.prodIdx,
				dot:        ke.dot,
				lookaheads: ke.lookaheads.clone(),
			})
			// Populate dot0Index for kernel items at dot=0.
			if ke.dot == 0 {
				ctx.dot0Index[ke.prodIdx] = idx
				ctx.dot0Dirty = append(ctx.dot0Dirty, ke.prodIdx)
			}
		}
	}

	// Worklist of core indices that need (re-)processing.
	worklist := make([]int, 0, len(cores))
	inWorklist := make([]bool, 0, len(cores)*2)
	for i := range cores {
		worklist = append(worklist, i)
		inWorklist = append(inWorklist, true)
	}

	for len(worklist) > 0 {
		ci := worklist[0]
		worklist = worklist[1:]
		if ci < len(inWorklist) {
			inWorklist[ci] = false
		}

		ce := &cores[ci]
		prod := &ng.Productions[ce.prodIdx]
		if ce.dot >= len(prod.RHS) {
			continue
		}

		nextSym := prod.RHS[ce.dot]
		if nextSym < tokenCount {
			continue
		}

		br := ctx.getBetaFirst(ce.prodIdx, ce.dot)

		for _, prodIdx := range ctx.prodsByLHS[nextSym] {
			// Fast path: dot=0 lookup via flat array.
			tidx := ctx.dot0Index[prodIdx]
			exists := tidx >= 0

			if !exists {
				tidx = len(cores)
				ctx.dot0Index[prodIdx] = tidx
				ctx.dot0Dirty = append(ctx.dot0Dirty, prodIdx)
				coreIdx[coreItem{prodIdx, 0}] = tidx
				cores = append(cores, coreEntry{
					prodIdx:    prodIdx,
					dot:        0,
					lookaheads: newBitset(tokenCount),
				})
				// Grow inWorklist if needed.
				for len(inWorklist) <= tidx {
					inWorklist = append(inWorklist, false)
				}
			}

			addedNew := false
			// FIRST(β) lookaheads.
			if cores[tidx].lookaheads.unionWith(&br.first) {
				addedNew = true
			}
			// If β is nullable, propagate all source lookaheads.
			if br.nullable {
				if cores[tidx].lookaheads.unionWith(&ce.lookaheads) {
					addedNew = true
				}
			}
			// Re-process target if it gained new lookaheads.
			if addedNew && tidx < len(inWorklist) && !inWorklist[tidx] {
				worklist = append(worklist, tidx)
				inWorklist[tidx] = true
			}
			if addedNew && tidx >= len(inWorklist) {
				for len(inWorklist) <= tidx {
					inWorklist = append(inWorklist, false)
				}
				worklist = append(worklist, tidx)
				inWorklist[tidx] = true
			}
		}
	}

	set := lrItemSet{
		cores:     cores,
		coreIndex: coreIdx,
	}
	set.computeHashes(ng.Productions)
	return set
}

// closureIncremental propagates new lookaheads through an existing item set.
func (ctx *lrContext) closureIncremental(set *lrItemSet, newEntries []coreEntry) {
	ng := ctx.ng
	tokenCount := ctx.tokenCount

	// Merge new entries into existing set and track which cores changed.
	var worklist []int
	inWorklist := make([]bool, len(set.cores)+len(newEntries))

	for _, ne := range newEntries {
		c := coreItem{ne.prodIdx, ne.dot}
		if idx, ok := set.coreIndex[c]; ok {
			if set.cores[idx].lookaheads.unionWith(&ne.lookaheads) {
				if !inWorklist[idx] {
					worklist = append(worklist, idx)
					inWorklist[idx] = true
				}
			}
		} else {
			idx = len(set.cores)
			set.coreIndex[c] = idx
			set.cores = append(set.cores, coreEntry{
				prodIdx:    ne.prodIdx,
				dot:        ne.dot,
				lookaheads: ne.lookaheads.clone(),
			})
			for len(inWorklist) <= idx {
				inWorklist = append(inWorklist, false)
			}
			worklist = append(worklist, idx)
			inWorklist[idx] = true
		}
	}

	for len(worklist) > 0 {
		ci := worklist[0]
		worklist = worklist[1:]
		if ci < len(inWorklist) {
			inWorklist[ci] = false
		}

		ce := &set.cores[ci]
		prod := &ng.Productions[ce.prodIdx]
		if ce.dot >= len(prod.RHS) {
			continue
		}

		nextSym := prod.RHS[ce.dot]
		if nextSym < tokenCount {
			continue
		}

		br := ctx.getBetaFirst(ce.prodIdx, ce.dot)

		for _, prodIdx := range ctx.prodsByLHS[nextSym] {
			target := coreItem{prodIdx, 0}
			tidx, exists := set.coreIndex[target]

			if !exists {
				tidx = len(set.cores)
				set.coreIndex[target] = tidx
				set.cores = append(set.cores, coreEntry{
					prodIdx:    prodIdx,
					dot:        0,
					lookaheads: newBitset(tokenCount),
				})
				for len(inWorklist) <= tidx {
					inWorklist = append(inWorklist, false)
				}
			}

			addedNew := false
			if set.cores[tidx].lookaheads.unionWith(&br.first) {
				addedNew = true
			}
			if br.nullable {
				if set.cores[tidx].lookaheads.unionWith(&ce.lookaheads) {
					addedNew = true
				}
			}
			if addedNew {
				if tidx >= len(inWorklist) {
					for len(inWorklist) <= tidx {
						inWorklist = append(inWorklist, false)
					}
				}
				if !inWorklist[tidx] {
					worklist = append(worklist, tidx)
					inWorklist[tidx] = true
				}
			}
		}
	}

	set.computeHashes(ng.Productions)
}

// betaResult caches the FIRST set and nullability of a production suffix.
type betaResult struct {
	first    bitset
	nullable bool
}

// getBetaFirst returns the cached FIRST(β) for the suffix after the dot in an item.
func (ctx *lrContext) getBetaFirst(prodIdx, dot int) *betaResult {
	bk := uint32(prodIdx)<<16 | uint32(dot)
	if cached, ok := ctx.betaCache[bk]; ok {
		return cached
	}
	prod := &ctx.ng.Productions[prodIdx]
	beta := prod.RHS[dot+1:]
	result := &betaResult{
		first:    ctx.firstOfSequence(beta),
		nullable: true,
	}
	for _, sym := range beta {
		if sym < ctx.tokenCount || !ctx.nullables[sym] {
			result.nullable = false
			break
		}
	}
	ctx.betaCache[bk] = result
	return result
}

// mixCoreItem hashes a (prodIdx, dot) pair into a well-distributed uint64.
func mixCoreItem(p, d int) uint64 {
	x := uint64(p)*0x9e3779b97f4a7c15 + uint64(d)*0x517cc1b727220a95
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	return x
}

// computeHashes computes coreHash, fullHash, and reduceLAHash for the item set.
// Uses commutative (additive) hashing so order of cores doesn't matter,
// avoiding the need to sort.
func (set *lrItemSet) computeHashes(prods []Production) {
	var ch, fh, rh uint64
	for _, c := range set.cores {
		m := mixCoreItem(c.prodIdx, c.dot)
		ch += m
		fh += m ^ c.lookaheads.hash()
		if c.dot >= len(prods[c.prodIdx].RHS) {
			rh += c.lookaheads.hash()
		}
	}
	set.coreHash = ch
	set.fullHash = fh
	set.reduceLAHash = ch + rh
}

// sameCores returns true if two item sets have identical core items.
func sameCores(a, b *lrItemSet) bool {
	if len(a.cores) != len(b.cores) {
		return false
	}
	for _, ac := range a.cores {
		c := coreItem{ac.prodIdx, ac.dot}
		if _, ok := b.coreIndex[c]; !ok {
			return false
		}
	}
	return true
}

// sameFullItems returns true if two item sets are identical (cores + lookaheads).
func sameFullItems(a, b *lrItemSet) bool {
	if len(a.cores) != len(b.cores) {
		return false
	}
	for _, ac := range a.cores {
		c := coreItem{ac.prodIdx, ac.dot}
		bidx, ok := b.coreIndex[c]
		if !ok {
			return false
		}
		if !ac.lookaheads.equal(&b.cores[bidx].lookaheads) {
			return false
		}
	}
	return true
}

// sameReduceLookaheads returns true if two item sets have the same lookaheads
// on all reduce items (dot at end).
func sameReduceLookaheads(a, b *lrItemSet, prods []Production) bool {
	for _, ac := range a.cores {
		if ac.dot < len(prods[ac.prodIdx].RHS) {
			continue // not a reduce item
		}
		c := coreItem{ac.prodIdx, ac.dot}
		bidx, ok := b.coreIndex[c]
		if !ok {
			return false
		}
		if !ac.lookaheads.equal(&b.cores[bidx].lookaheads) {
			return false
		}
	}
	// Also check the reverse direction.
	for _, bc := range b.cores {
		if bc.dot < len(prods[bc.prodIdx].RHS) {
			continue
		}
		c := coreItem{bc.prodIdx, bc.dot}
		if _, ok := a.coreIndex[c]; !ok {
			return false
		}
	}
	return true
}

// stateHashEntry is a linked list node for hash-based state lookup.
type stateHashEntry struct {
	stateIdx int
	next     *stateHashEntry
}

// buildItemSets constructs LR(1) item sets with LALR-like merging.
//
// Uses hash-based state deduplication and core-based item representation
// with bitset lookaheads for performance on large grammars.
func (ctx *lrContext) buildItemSets() []lrItemSet {
	ctx.transitions = make(map[int]map[int]int)

	tokenCount := ctx.tokenCount

	// Hash tables for state lookup.
	// fullMap: fullHash → chain of states with that hash (exact LR(1) match)
	fullMap := make(map[uint64]*stateHashEntry)
	// coreMap: coreHash → chain of states (for LALR merge)
	coreMap := make(map[uint64]*stateHashEntry)
	// extMap: reduceLAHash → chain of states (for extended merge)
	extMap := make(map[uint64]*stateHashEntry)

	// For large grammars, use LALR merging from the start to avoid state explosion.
	const maxExtendedStates = 8000
	useExtendedMerging := len(ctx.ng.Productions) <= 800

	// Initial item set: closure of [S' → .S, $end]
	initialLA := newBitset(tokenCount)
	initialLA.add(0) // $end
	initialSet := ctx.closureToSet([]coreEntry{{
		prodIdx:    ctx.ng.AugmentProdID,
		dot:        0,
		lookaheads: initialLA,
	}})
	ctx.itemSets = []lrItemSet{initialSet}
	addToHashMap(fullMap, initialSet.fullHash, 0)
	addToHashMap(coreMap, initialSet.coreHash, 0)
	addToHashMap(extMap, initialSet.reduceLAHash, 0)

	worklist := []int{0}
	inWorklist := map[int]bool{0: true}

	for len(worklist) > 0 {
		stateIdx := worklist[0]
		worklist = worklist[1:]
		inWorklist[stateIdx] = false
		itemSet := &ctx.itemSets[stateIdx]

		// Collect all symbols after the dot.
		symsSeen := make(map[int]bool)
		var syms []int
		for _, ce := range itemSet.cores {
			prod := &ctx.ng.Productions[ce.prodIdx]
			if ce.dot < len(prod.RHS) {
				sym := prod.RHS[ce.dot]
				if !symsSeen[sym] {
					symsSeen[sym] = true
					syms = append(syms, sym)
				}
			}
		}

		for _, sym := range syms {
			// Compute GOTO(itemSet, sym): advance dot past sym.
			var advanced []coreEntry
			for _, ce := range itemSet.cores {
				prod := &ctx.ng.Productions[ce.prodIdx]
				if ce.dot < len(prod.RHS) && prod.RHS[ce.dot] == sym {
					advanced = append(advanced, coreEntry{
						prodIdx:    ce.prodIdx,
						dot:        ce.dot + 1,
						lookaheads: ce.lookaheads, // shared ref, closureToSet will clone
					})
				}
			}
			if len(advanced) == 0 {
				continue
			}

			closedSet := ctx.closureToSet(advanced)

			targetIdx := ctx.findOrCreateState(
				&closedSet,
				fullMap, coreMap, extMap,
				useExtendedMerging && len(ctx.itemSets) < maxExtendedStates,
				&worklist, &inWorklist,
			)

			// Record transition for table construction.
			if ctx.transitions[stateIdx] == nil {
				ctx.transitions[stateIdx] = make(map[int]int)
			}
			ctx.transitions[stateIdx][sym] = targetIdx
		}
	}

	return ctx.itemSets
}

func addToHashMap(m map[uint64]*stateHashEntry, hash uint64, idx int) {
	m[hash] = &stateHashEntry{stateIdx: idx, next: m[hash]}
}

// findOrCreateState looks up or creates a state for the given item set.
func (ctx *lrContext) findOrCreateState(
	closedSet *lrItemSet,
	fullMap, coreMap, extMap map[uint64]*stateHashEntry,
	useExtended bool,
	worklist *[]int,
	inWorklist *map[int]bool,
) int {
	// 1. Check exact LR(1) match via fullHash.
	for entry := fullMap[closedSet.fullHash]; entry != nil; entry = entry.next {
		if sameFullItems(&ctx.itemSets[entry.stateIdx], closedSet) {
			return entry.stateIdx
		}
	}

	if useExtended {
		// 2a. Extended merging: find state with same core AND same reduce lookaheads.
		for entry := extMap[closedSet.reduceLAHash]; entry != nil; entry = entry.next {
			existing := &ctx.itemSets[entry.stateIdx]
			if existing.coreHash == closedSet.coreHash &&
				sameCores(existing, closedSet) &&
				sameReduceLookaheads(existing, closedSet, ctx.ng.Productions) {
				// Merge lookaheads into existing state.
				return ctx.mergeInto(entry.stateIdx, closedSet, fullMap, extMap, worklist, inWorklist)
			}
		}
	} else {
		// 2b. LALR fallback: find state with same core.
		for entry := coreMap[closedSet.coreHash]; entry != nil; entry = entry.next {
			existing := &ctx.itemSets[entry.stateIdx]
			if sameCores(existing, closedSet) {
				return ctx.mergeInto(entry.stateIdx, closedSet, fullMap, extMap, worklist, inWorklist)
			}
		}
	}

	// 3. No match — create new state.
	newIdx := len(ctx.itemSets)
	ctx.itemSets = append(ctx.itemSets, *closedSet)
	addToHashMap(fullMap, closedSet.fullHash, newIdx)
	addToHashMap(coreMap, closedSet.coreHash, newIdx)
	addToHashMap(extMap, closedSet.reduceLAHash, newIdx)
	*worklist = append(*worklist, newIdx)
	(*inWorklist)[newIdx] = true
	return newIdx
}

// mergeInto merges lookaheads from closedSet into the existing state at idx.
func (ctx *lrContext) mergeInto(
	idx int,
	closedSet *lrItemSet,
	fullMap, extMap map[uint64]*stateHashEntry,
	worklist *[]int,
	inWorklist *map[int]bool,
) int {
	// Collect new core entries to merge.
	var newEntries []coreEntry
	existing := &ctx.itemSets[idx]
	for _, ce := range closedSet.cores {
		c := coreItem{ce.prodIdx, ce.dot}
		if eidx, ok := existing.coreIndex[c]; ok {
			// Check if any new lookaheads.
			ec := &existing.cores[eidx]
			for wi, w := range ce.lookaheads.words {
				if wi < len(ec.lookaheads.words) {
					if w & ^ec.lookaheads.words[wi] != 0 {
						newEntries = append(newEntries, ce)
						break
					}
				} else if w != 0 {
					newEntries = append(newEntries, ce)
					break
				}
			}
		} else {
			newEntries = append(newEntries, ce)
		}
	}

	if len(newEntries) > 0 {
		ctx.closureIncremental(existing, newEntries)
		// Update hash maps with new hashes.
		addToHashMap(fullMap, existing.fullHash, idx)
		addToHashMap(extMap, existing.reduceLAHash, idx)
		if !(*inWorklist)[idx] {
			*worklist = append(*worklist, idx)
			(*inWorklist)[idx] = true
		}
	}
	return idx
}

// resolveConflicts resolves shift/reduce and reduce/reduce conflicts
// using precedence and associativity.
func resolveConflicts(tables *LRTables, ng *NormalizedGrammar) error {
	for state, actions := range tables.ActionTable {
		for sym, acts := range actions {
			if len(acts) <= 1 {
				continue
			}

			resolved, err := resolveActionConflict(acts, ng)
			if err != nil {
				return fmt.Errorf("state %d, symbol %d: %w", state, sym, err)
			}
			tables.ActionTable[state][sym] = resolved
		}
	}
	return nil
}

// resolveActionConflict resolves a conflict between multiple actions.
func resolveActionConflict(actions []lrAction, ng *NormalizedGrammar) ([]lrAction, error) {
	if len(actions) <= 1 {
		return actions, nil
	}

	// Priority: non-extra actions always win over extra actions.
	hasExtra, hasNonExtra := false, false
	for _, a := range actions {
		if a.isExtra {
			hasExtra = true
		} else {
			hasNonExtra = true
		}
	}
	if hasExtra && hasNonExtra {
		var nonExtra []lrAction
		for _, a := range actions {
			if !a.isExtra {
				nonExtra = append(nonExtra, a)
			}
		}
		if len(nonExtra) <= 1 {
			return nonExtra, nil
		}
		actions = nonExtra
	}

	// Separate shifts and reduces.
	var shifts, reduces []lrAction
	for _, a := range actions {
		switch a.kind {
		case lrShift:
			shifts = append(shifts, a)
		case lrReduce:
			reduces = append(reduces, a)
		case lrAccept:
			return []lrAction{a}, nil
		}
	}

	// Shift/reduce conflict.
	if len(shifts) > 0 && len(reduces) > 0 {
		shift := shifts[0]
		reduce := reduces[0]
		prod := &ng.Productions[reduce.prodIdx]

		shiftPrec := shift.prec
		reducePrec := prod.Prec

		if reducePrec != 0 || shiftPrec != 0 {
			if reducePrec > shiftPrec {
				return []lrAction{reduce}, nil
			}
			if shiftPrec > reducePrec {
				return []lrAction{shift}, nil
			}
			switch prod.Assoc {
			case AssocLeft:
				return []lrAction{reduce}, nil
			case AssocRight:
				return []lrAction{shift}, nil
			case AssocNone:
				return nil, nil
			}
		}

		if shiftMatchesConflictGroup(shift, reduce.lhsSym, ng) {
			return actions, nil
		}
		if reduceLHSInConflictGroup(reduce.prodIdx, ng) {
			return actions, nil
		}
		if isTransitiveConflict(shift, reduce, ng) {
			return actions, nil
		}

		// Targeted eex ambiguity.
		if len(prod.RHS) == 0 && len(ng.Symbols) > prod.LHS {
			reduceName := ng.Symbols[prod.LHS].Name
			if strings.HasPrefix(reduceName, "_partial_expression_repeat") {
				for _, s := range shifts {
					if s.lhsSym > 0 && s.lhsSym < len(ng.Symbols) &&
						strings.HasPrefix(ng.Symbols[s.lhsSym].Name, "_expression_repeat1_") {
						return actions, nil
					}
					for _, lhs := range s.lhsSyms {
						if lhs > 0 && lhs < len(ng.Symbols) &&
							strings.HasPrefix(ng.Symbols[lhs].Name, "_expression_repeat1_") {
							return actions, nil
						}
					}
				}
			}
		}

		// Default: prefer shift.
		return []lrAction{shift}, nil
	}

	// Reduce/reduce conflict.
	// Tree-sitter resolves ALL R/R conflicts by picking the highest-prec
	// production (then lowest prodIdx) unless they're in a declared conflict
	// group (kept as GLR). The previous hasEpsilon guard only resolved
	// epsilon R/R conflicts, leaving non-epsilon R/R as ambiguous table
	// entries which caused type="" parse failures.
	if len(reduces) > 1 {
		return resolveReduceReduceLegacy(reduces, ng)
	}

	return actions, nil
}

func resolveReduceReduceLegacy(reduces []lrAction, ng *NormalizedGrammar) ([]lrAction, error) {
	if allInDeclaredConflict(reduces, ng) {
		return reduces, nil
	}

	best := reduces[0]
	bestProd := &ng.Productions[best.prodIdx]
	for _, r := range reduces[1:] {
		rProd := &ng.Productions[r.prodIdx]
		if rProd.Prec > bestProd.Prec {
			best = r
			bestProd = rProd
		} else if rProd.Prec == bestProd.Prec {
			// Tree-sitter uses dynamic precedence as the next tiebreaker,
			// then falls back to production index (earlier declaration wins).
			if rProd.DynPrec > bestProd.DynPrec {
				best = r
				bestProd = rProd
			} else if rProd.DynPrec == bestProd.DynPrec && r.prodIdx < best.prodIdx {
				best = r
				bestProd = rProd
			}
		}
	}
	return []lrAction{best}, nil
}

func shiftMatchesConflictGroup(shift lrAction, reduceLHS int, ng *NormalizedGrammar) bool {
	if len(ng.Conflicts) == 0 {
		return false
	}
	allShiftLHS := make([]int, 0, 1+len(shift.lhsSyms))
	if shift.lhsSym != 0 {
		allShiftLHS = append(allShiftLHS, shift.lhsSym)
	}
	allShiftLHS = append(allShiftLHS, shift.lhsSyms...)

	for _, cgroup := range ng.Conflicts {
		hasReduce := false
		for _, sym := range cgroup {
			if sym == reduceLHS {
				hasReduce = true
				break
			}
		}
		if !hasReduce {
			continue
		}
		for _, sym := range cgroup {
			for _, shiftLHS := range allShiftLHS {
				if sym == shiftLHS && shiftLHS != reduceLHS {
					return true
				}
			}
		}
	}
	return false
}

// reduceLHSInConflictGroup checks whether the reduce production's LHS symbol
// appears in any declared conflict group. This is used as a fallback in S/R
// conflict resolution: if the reduce's LHS is in a conflict group, the conflict
// is kept as GLR even if the shift's LHS is not in the same group.
func reduceLHSInConflictGroup(prodIdx int, ng *NormalizedGrammar) bool {
	prod := &ng.Productions[prodIdx]
	for _, cgroup := range ng.Conflicts {
		for _, sym := range cgroup {
			if sym == prod.LHS {
				return true
			}
		}
	}
	return false
}

func isTransitiveConflict(shift lrAction, reduce lrAction, ng *NormalizedGrammar) bool {
	if len(ng.Conflicts) == 0 {
		return false
	}

	conflictSyms := make(map[int]bool)
	for _, cg := range ng.Conflicts {
		for _, s := range cg {
			conflictSyms[s] = true
		}
	}

	reduceLHS := ng.Productions[reduce.prodIdx].LHS
	if conflictSyms[reduceLHS] {
		return false
	}

	reverseIdx := make(map[int][]int)
	for i, prod := range ng.Productions {
		for _, s := range prod.RHS {
			reverseIdx[s] = append(reverseIdx[s], i)
		}
	}

	allShiftLHS := make(map[int]bool)
	if shift.lhsSym != 0 {
		allShiftLHS[shift.lhsSym] = true
	}
	for _, s := range shift.lhsSyms {
		allShiftLHS[s] = true
	}

	shiftConflictSyms := make(map[int]bool)
	for s := range allShiftLHS {
		if conflictSyms[s] {
			shiftConflictSyms[s] = true
		}
		for _, pi := range reverseIdx[s] {
			prod := &ng.Productions[pi]
			if conflictSyms[prod.LHS] {
				shiftConflictSyms[prod.LHS] = true
			}
			for _, rs := range prod.RHS {
				if conflictSyms[rs] {
					shiftConflictSyms[rs] = true
				}
			}
		}
	}

	if len(shiftConflictSyms) == 0 {
		return false
	}

	visited := make(map[int]bool)
	visited[reduceLHS] = true
	queue := []int{reduceLHS}
	maxDepth := 4

	for depth := 0; depth < maxDepth && len(queue) > 0; depth++ {
		var next []int
		for _, sym := range queue {
			for _, pi := range reverseIdx[sym] {
				prod := &ng.Productions[pi]
				var foundReduceSide []int
				if conflictSyms[prod.LHS] {
					foundReduceSide = append(foundReduceSide, prod.LHS)
				}
				for _, rs := range prod.RHS {
					if conflictSyms[rs] && rs != sym {
						foundReduceSide = append(foundReduceSide, rs)
					}
				}
				for _, rcs := range foundReduceSide {
					for _, cg := range ng.Conflicts {
						hasReduce, hasShift := false, false
						for _, cs := range cg {
							if cs == rcs {
								hasReduce = true
							}
							if shiftConflictSyms[cs] {
								hasShift = true
							}
						}
						if hasReduce && hasShift {
							return true
						}
					}
				}

				if !visited[prod.LHS] {
					visited[prod.LHS] = true
					next = append(next, prod.LHS)
				}
			}
		}
		queue = next
	}
	return false
}

func allInDeclaredConflict(reduces []lrAction, ng *NormalizedGrammar) bool {
	if len(reduces) < 2 || len(ng.Conflicts) == 0 {
		return false
	}
	for _, cgroup := range ng.Conflicts {
		cgroupSet := make(map[int]bool, len(cgroup))
		for _, sym := range cgroup {
			cgroupSet[sym] = true
		}
		allFound := true
		for _, r := range reduces {
			lhs := ng.Productions[r.prodIdx].LHS
			if !cgroupSet[lhs] {
				allFound = false
				break
			}
		}
		if allFound {
			return true
		}
	}
	return false
}
