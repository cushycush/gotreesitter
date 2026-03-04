package grammargen

import (
	"fmt"
	"sort"
)

// lrItem is an LR(1) item: [A → α . β, a]
type lrItem struct {
	prodIdx   int    // index into productions
	dot       int    // position of dot in RHS
	lookahead int    // terminal symbol ID
}

// lrItemSet is a set of LR(1) items (one parser state).
type lrItemSet struct {
	items []lrItem
	key   string // canonical key for dedup
	seen  map[closureItemKey]bool // persistent membership set for fast merge
}

// lrAction is a parse table action.
type lrAction struct {
	kind     lrActionKind
	state    int   // shift target / goto target
	prodIdx  int   // reduce production index
	prec     int   // for shift: precedence of the item's production
	assoc    Assoc // for shift: associativity of the item's production
	lhsSym   int   // LHS nonterminal of the production (for conflict detection)
	lhsSyms  []int // additional LHS symbols (when shifts from multiple rules merge)
	isExtra  bool  // true if this action comes from a nonterminal extra production
}

type lrActionKind int

const (
	lrShift  lrActionKind = iota
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
		firstSets:  make(map[int]map[int]bool),
		nullables:  make(map[int]bool),
		prodsByLHS: make(map[int][]int),
		betaCache:  make(map[struct{ prodIdx, dot int }]*betaResult),
	}

	// Build production-by-LHS index for fast closure lookups.
	for i := range ng.Productions {
		lhs := ng.Productions[i].LHS
		ctx.prodsByLHS[lhs] = append(ctx.prodsByLHS[lhs], i)
	}

	// Identify nonterminal extra productions and all terminals for injection.
	tokenCount := ng.TokenCount()
	for i := range ng.Productions {
		if ng.Productions[i].IsExtra {
			ctx.extraProdIndices = append(ctx.extraProdIndices, i)
		}
	}
	if len(ctx.extraProdIndices) > 0 {
		for i := 0; i < tokenCount; i++ {
			ctx.allTerminals = append(ctx.allTerminals, i)
		}
	}

	// Compute FIRST and nullable sets.
	ctx.computeFirstSets()

	// Build LR(1) item sets (canonical collection).
	itemSets := ctx.buildItemSets()

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

		for _, item := range itemSet.items {
			prod := &ng.Productions[item.prodIdx]

			if item.dot < len(prod.RHS) {
				// Dot not at end → shift or goto
				nextSym := prod.RHS[item.dot]
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
				if item.prodIdx == ng.AugmentProdID {
					// Augmented start production → accept
					tables.addAction(stateIdx, 0, lrAction{kind: lrAccept})
				} else {
					// Regular reduce
					tables.addAction(stateIdx, item.lookahead, lrAction{
						kind:    lrReduce,
						prodIdx: item.prodIdx,
						lhsSym:  prod.LHS,
						isExtra: prod.IsExtra,
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
				// This matters when multiple items contribute shifts on
				// the same terminal (e.g. items from different productions).
				// Non-extra shifts take priority over extra shifts.
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
	ng        *NormalizedGrammar
	firstSets map[int]map[int]bool // symbol → set of terminal first symbols
	nullables map[int]bool         // symbol → can derive ε

	// Production index: LHS symbol → production indices
	prodsByLHS map[int][]int

	// FIRST(β) cache: (prodIdx, dot) → first set + nullable flag
	betaCache map[struct{ prodIdx, dot int }]*betaResult

	// Item set management
	itemSets   []lrItemSet
	itemSetMap map[string]int // full LR(1) key → index
	coreMap    map[string]int // core key (prodIdx+dot only) → index

	// Transition cache: transitions[state][symbol] → target state
	// Populated during buildItemSets, used during table construction.
	transitions map[int]map[int]int

	// Nonterminal extra support: production indices for extras that need
	// to be injected into every state's kernel.
	extraProdIndices []int
	allTerminals     []int // all terminal symbol IDs (for extra item lookaheads)
}

// addNonterminalExtraChains creates dedicated parse state chains for nonterminal
// extra productions and adds shift actions from every main state. This handles
// extras like `comment → [;#] [^\r\n]* \r?\n` without modifying the LR kernel.
//
// For each nonterminal extra production with RHS = [t1, t2, ..., tn]:
//   - Creates n new states (chain states) appended to the existing state count
//   - Chain state 0: result of shifting t1, expects t2
//   - Chain state n-1: result of shifting tn, reduces the production
//   - Adds shift(t1 → chain state 0) to every main state that has no action for t1
func addNonterminalExtraChains(tables *LRTables, ng *NormalizedGrammar) {
	tokenCount := ng.TokenCount()
	if len(ng.ExtraSymbols) == 0 {
		return
	}

	// Find nonterminal extra productions.
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

	// Compute the set of terminals that have actions in any main state.
	// The last chain state's reduce actions are restricted to these terminals
	// so its lex mode only produces tokens valid in main states. This prevents
	// the DFA from matching a chain-only terminal (like [^\r\n]*) as the
	// lookahead that gets reused after the extra reduces back to a main state.
	mainValidTerminals := make(map[int]bool)
	for state := 0; state < mainStateCount; state++ {
		for sym := range tables.ActionTable[state] {
			if sym < tokenCount {
				mainValidTerminals[sym] = true
			}
		}
	}
	// Terminal extras are always valid.
	for _, e := range ng.ExtraSymbols {
		if e > 0 && e < tokenCount {
			mainValidTerminals[e] = true
		}
	}
	// EOF is always valid.
	mainValidTerminals[0] = true

	for _, prodIdx := range extraProds {
		prod := &ng.Productions[prodIdx]
		rhsLen := len(prod.RHS)

		// Create chain states for this production.
		// State i (0-indexed) is the state after shifting RHS[i].
		chainStart := tables.StateCount
		for i := 0; i < rhsLen; i++ {
			stateIdx := chainStart + i
			tables.ActionTable[stateIdx] = make(map[int][]lrAction)
			tables.GotoTable[stateIdx] = make(map[int]int)

			if i < rhsLen-1 {
				// Not the last position: shift next terminal → next chain state.
				nextSym := prod.RHS[i+1]
				if nextSym < tokenCount {
					tables.ActionTable[stateIdx][nextSym] = []lrAction{{
						kind:    lrShift,
						state:   stateIdx + 1,
						isExtra: true,
					}}
				}
			} else {
				// Last position: reduce action for main-valid terminals only.
				// This restricts the lex mode so the DFA doesn't produce
				// chain-only terminals as the lookahead token.
				for t := range mainValidTerminals {
					tables.ActionTable[stateIdx][t] = []lrAction{{
						kind:    lrReduce,
						prodIdx: prodIdx,
						lhsSym:  prod.LHS,
						isExtra: true,
					}}
				}
			}

			// Also add terminal extra shift-extra in chain states.
			for _, extraSym := range ng.ExtraSymbols {
				if extraSym < tokenCount {
					if _, ok := tables.ActionTable[stateIdx][extraSym]; !ok {
						tables.ActionTable[stateIdx][extraSym] = []lrAction{{
							kind:    lrShift,
							state:   stateIdx, // stay in same state (consume extra)
							isExtra: true,
						}}
					}
				}
			}
		}
		tables.StateCount += rhsLen

		// Add shift actions from every main state for the first terminal.
		firstSym := prod.RHS[0]
		if firstSym >= tokenCount {
			continue // first symbol is nonterminal — skip (would need closure)
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

// computeFirstSets computes FIRST sets for all symbols.
func (ctx *lrContext) computeFirstSets() {
	ng := ctx.ng
	tokenCount := ng.TokenCount()

	// Initialize: terminals have FIRST = {self}
	for i, sym := range ng.Symbols {
		if sym.Kind == SymbolTerminal || sym.Kind == SymbolNamedToken || sym.Kind == SymbolExternal {
			ctx.firstSets[i] = map[int]bool{i: true}
		} else {
			ctx.firstSets[i] = make(map[int]bool)
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
			lhsFirst := ctx.firstSets[prod.LHS]
			for _, sym := range prod.RHS {
				symFirst := ctx.firstSets[sym]
				for f := range symFirst {
					if !lhsFirst[f] {
						lhsFirst[f] = true
						changed = true
					}
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
func (ctx *lrContext) firstOfSequence(syms []int) map[int]bool {
	result := make(map[int]bool)
	tokenCount := ctx.ng.TokenCount()
	for _, sym := range syms {
		for f := range ctx.firstSets[sym] {
			result[f] = true
		}
		if sym < tokenCount || !ctx.nullables[sym] {
			return result
		}
	}
	return result
}

// closureItemKey is the identity of an LR(1) item.
type closureItemKey struct {
	prodIdx, dot, lookahead int
}

// coreItem identifies an LR(0) core (production + dot position).
type coreItem struct {
	prodIdx, dot int
}

// closureToSet computes the closure of items and returns an lrItemSet with a
// persistent seen map. Uses core-based closure: items sharing the same
// (prodIdx, dot) core are grouped, and lookaheads are propagated as sets.
// This is dramatically faster for grammars with many lookaheads per core.
func (ctx *lrContext) closureToSet(items []lrItem) lrItemSet {
	ng := ctx.ng
	tokenCount := ng.TokenCount()

	// Group input items by core, collecting lookahead sets.
	cores := make(map[coreItem]map[int]bool)
	var coreOrder []coreItem
	for _, item := range items {
		c := coreItem{item.prodIdx, item.dot}
		if cores[c] == nil {
			cores[c] = make(map[int]bool)
			coreOrder = append(coreOrder, c)
		}
		cores[c][item.lookahead] = true
	}

	// Worklist of cores that need (re-)processing. A core needs processing
	// when it gains new lookaheads that might propagate through nullable suffixes.
	inWorklist := make(map[coreItem]bool, len(coreOrder))
	worklist := make([]coreItem, len(coreOrder))
	copy(worklist, coreOrder)
	for _, c := range coreOrder {
		inWorklist[c] = true
	}

	for len(worklist) > 0 {
		c := worklist[0]
		worklist = worklist[1:]
		inWorklist[c] = false

		prod := &ng.Productions[c.prodIdx]
		if c.dot >= len(prod.RHS) {
			continue
		}

		nextSym := prod.RHS[c.dot]
		if nextSym < tokenCount {
			continue
		}

		br := ctx.getBetaFirst(lrItem{prodIdx: c.prodIdx, dot: c.dot})
		las := cores[c]

		for _, prodIdx := range ctx.prodsByLHS[nextSym] {
			target := coreItem{prodIdx, 0}
			targetLas := cores[target]
			isNew := targetLas == nil
			if isNew {
				targetLas = make(map[int]bool)
				cores[target] = targetLas
				coreOrder = append(coreOrder, target)
			}

			addedNew := false
			// FIRST(β) lookaheads — same for all source lookaheads.
			for la := range br.first {
				if !targetLas[la] {
					targetLas[la] = true
					addedNew = true
				}
			}
			// If β is nullable, propagate all source lookaheads.
			if br.nullable {
				for la := range las {
					if !targetLas[la] {
						targetLas[la] = true
						addedNew = true
					}
				}
			}
			// Re-process target if it gained new lookaheads and could propagate.
			if addedNew && !inWorklist[target] {
				worklist = append(worklist, target)
				inWorklist[target] = true
			}
		}
	}

	// Expand core→lookaheadSet into individual items.
	totalItems := 0
	for _, c := range coreOrder {
		totalItems += len(cores[c])
	}
	result := make([]lrItem, 0, totalItems)
	seen := make(map[closureItemKey]bool, totalItems)
	for _, c := range coreOrder {
		for la := range cores[c] {
			result = append(result, lrItem{prodIdx: c.prodIdx, dot: c.dot, lookahead: la})
			seen[closureItemKey{c.prodIdx, c.dot, la}] = true
		}
	}

	return lrItemSet{items: result, seen: seen}
}

// closureIncremental propagates new items through an existing (already-closed)
// item set. Uses core-based processing for efficiency.
func (ctx *lrContext) closureIncremental(set *lrItemSet, newItems []lrItem) {
	ng := ctx.ng
	tokenCount := ng.TokenCount()

	// Group new items by core.
	cores := make(map[coreItem]map[int]bool)
	var worklist []coreItem
	inWorklist := make(map[coreItem]bool)

	for _, item := range newItems {
		c := coreItem{item.prodIdx, item.dot}
		if cores[c] == nil {
			cores[c] = make(map[int]bool)
			worklist = append(worklist, c)
			inWorklist[c] = true
		}
		cores[c][item.lookahead] = true
	}

	for len(worklist) > 0 {
		c := worklist[0]
		worklist = worklist[1:]
		inWorklist[c] = false

		prod := &ng.Productions[c.prodIdx]
		if c.dot >= len(prod.RHS) {
			continue
		}

		nextSym := prod.RHS[c.dot]
		if nextSym < tokenCount {
			continue
		}

		br := ctx.getBetaFirst(lrItem{prodIdx: c.prodIdx, dot: c.dot})
		las := cores[c]

		for _, prodIdx := range ctx.prodsByLHS[nextSym] {
			target := coreItem{prodIdx, 0}
			targetLas := cores[target]
			if targetLas == nil {
				targetLas = make(map[int]bool)
				cores[target] = targetLas
			}

			addedNew := false
			for la := range br.first {
				key := closureItemKey{prodIdx, 0, la}
				if !set.seen[key] {
					set.seen[key] = true
					targetLas[la] = true
					set.items = append(set.items, lrItem{prodIdx: prodIdx, dot: 0, lookahead: la})
					addedNew = true
				}
			}
			if br.nullable {
				for la := range las {
					key := closureItemKey{prodIdx, 0, la}
					if !set.seen[key] {
						set.seen[key] = true
						targetLas[la] = true
						set.items = append(set.items, lrItem{prodIdx: prodIdx, dot: 0, lookahead: la})
						addedNew = true
					}
				}
			}
			if addedNew && !inWorklist[target] {
				worklist = append(worklist, target)
				inWorklist[target] = true
			}
		}
	}
}

// betaResult caches the FIRST set and nullability of a production suffix.
type betaResult struct {
	first    map[int]bool
	nullable bool
}

// getBetaFirst returns the cached FIRST(β) for the suffix after the dot in an item.
func (ctx *lrContext) getBetaFirst(item lrItem) *betaResult {
	bk := struct{ prodIdx, dot int }{item.prodIdx, item.dot}
	if cached, ok := ctx.betaCache[bk]; ok {
		return cached
	}
	ng := ctx.ng
	tokenCount := ng.TokenCount()
	prod := &ng.Productions[item.prodIdx]
	beta := prod.RHS[item.dot+1:]
	result := &betaResult{
		first:    ctx.firstOfSequence(beta),
		nullable: true,
	}
	for _, sym := range beta {
		if sym < tokenCount || !ctx.nullables[sym] {
			result.nullable = false
			break
		}
	}
	ctx.betaCache[bk] = result
	return result
}

// buildItemSets constructs LR(1) item sets with LALR-like merging.
//
// The algorithm starts as LALR(1) — states with the same core (same prodIdx+dot
// pairs) are candidates for merging. However, states are only merged when the
// merge would not change the set of valid terminals for lex mode computation.
// Specifically, two core-identical states are kept separate if they have
// different sets of reduce-lookahead terminals (terminals that trigger a reduce
// action). This prevents lex mode pollution where greedy patterns become valid
// in parser states where they shouldn't be.
//
// This is between LALR(1) and canonical LR(1): it merges more than LR(1) but
// less than LALR(1), specifically refusing merges that would cause lex mode
// conflicts. This matches tree-sitter's behavior of building canonical LR(1)
// and then minimizing with token-conflict awareness.
func (ctx *lrContext) buildItemSets() []lrItemSet {
	ctx.itemSetMap = make(map[string]int)
	ctx.coreMap = make(map[string]int)
	ctx.transitions = make(map[int]map[int]int)
	// mergeMap maps extendedCoreKey → state index (for lex-safe merging).
	mergeMap := make(map[string]int)

	// For large grammars, extended merging can cause state explosion.
	// Use production count as a heuristic: grammars with many productions
	// (like SQL at 190+) should use LALR from the start.
	// Extended merging splits states with different reduce-lookahead terminal
	// sets to prevent lex mode pollution. This creates more states than LALR but
	// gives better lex modes. For very large grammars, fall back to LALR to
	// avoid state explosion. The 8000-state cap provides an additional safety net.
	const maxExtendedStates = 8000
	useExtendedMerging := len(ctx.ng.Productions) <= 800

	// Initial item set: closure of [S' → .S, $end]
	initialSet := ctx.closureToSet([]lrItem{{
		prodIdx:   ctx.ng.AugmentProdID,
		dot:       0,
		lookahead: 0, // $end
	}})
	initialSet.key = itemSetKey(initialSet.items)
	initialCore := coreKey(initialSet.items)
	extKey := extendedMergeKey(initialSet.items, ctx.ng.Productions)
	ctx.itemSets = []lrItemSet{initialSet}
	ctx.itemSetMap[initialSet.key] = 0
	ctx.coreMap[initialCore] = 0
	mergeMap[extKey] = 0

	worklist := []int{0}
	inWorklist := map[int]bool{0: true}

	for len(worklist) > 0 {
		stateIdx := worklist[0]
		worklist = worklist[1:]
		inWorklist[stateIdx] = false
		itemSet := ctx.itemSets[stateIdx]

		// Collect all symbols after the dot.
		symsSeen := make(map[int]bool)
		var syms []int
		for _, item := range itemSet.items {
			prod := &ctx.ng.Productions[item.prodIdx]
			if item.dot < len(prod.RHS) {
				sym := prod.RHS[item.dot]
				if !symsSeen[sym] {
					symsSeen[sym] = true
					syms = append(syms, sym)
				}
			}
		}

		for _, sym := range syms {
			// Compute GOTO(itemSet, sym).
			var advanced []lrItem
			for _, item := range itemSet.items {
				prod := &ctx.ng.Productions[item.prodIdx]
				if item.dot < len(prod.RHS) && prod.RHS[item.dot] == sym {
					advanced = append(advanced, lrItem{
						prodIdx:   item.prodIdx,
						dot:       item.dot + 1,
						lookahead: item.lookahead,
					})
				}
			}
			if len(advanced) == 0 {
				continue
			}

			closedSet := ctx.closureToSet(advanced)
			core := coreKey(closedSet.items)

			targetIdx := ctx.findOrCreateState(closedSet, core, mergeMap,
				useExtendedMerging && len(ctx.itemSets) < maxExtendedStates,
				&worklist, &inWorklist)

			// Record transition for table construction.
			if ctx.transitions[stateIdx] == nil {
				ctx.transitions[stateIdx] = make(map[int]int)
			}
			ctx.transitions[stateIdx][sym] = targetIdx
		}
	}

	return ctx.itemSets
}

// findOrCreateState looks up or creates a state for the given item set.
// When useExtended is true, it uses the extended merge key (core + reduce
// lookahead terminals) to avoid lex mode pollution. When false, it falls back
// to standard LALR core-based merging.
func (ctx *lrContext) findOrCreateState(
	closedSet lrItemSet,
	core string,
	mergeMap map[string]int,
	useExtended bool,
	worklist *[]int,
	inWorklist *map[int]bool,
) int {
	// 1. Check exact LR(1) key — if we've seen this exact item set, reuse it.
	closedSet.key = itemSetKey(closedSet.items)
	if idx, ok := ctx.itemSetMap[closedSet.key]; ok {
		return idx
	}

	if useExtended {
		// 2a. Extended merging: use core + reduce-lookahead key.
		extKey := extendedMergeKey(closedSet.items, ctx.ng.Productions)
		if idx, ok := mergeMap[extKey]; ok {
			// Merge lookaheads into existing state.
			newItems := mergeItemsReturnNew(&ctx.itemSets[idx], closedSet.items)
			if len(newItems) > 0 {
				// Re-close with new items and re-enqueue.
				ctx.closureIncremental(&ctx.itemSets[idx], newItems)
				ctx.itemSets[idx].key = itemSetKey(ctx.itemSets[idx].items)
				ctx.itemSetMap[ctx.itemSets[idx].key] = idx
				if !(*inWorklist)[idx] {
					*worklist = append(*worklist, idx)
					(*inWorklist)[idx] = true
				}
			}
			return idx
		}
		// No extended match — create a new state.
		newIdx := len(ctx.itemSets)
		ctx.itemSets = append(ctx.itemSets, closedSet)
		ctx.itemSetMap[closedSet.key] = newIdx
		ctx.coreMap[core] = newIdx // may overwrite, that's OK
		mergeMap[extKey] = newIdx
		*worklist = append(*worklist, newIdx)
		(*inWorklist)[newIdx] = true
		return newIdx
	}

	// 2b. LALR fallback: merge by core key.
	if idx, ok := ctx.coreMap[core]; ok {
		newItems := mergeItemsReturnNew(&ctx.itemSets[idx], closedSet.items)
		if len(newItems) > 0 {
			ctx.closureIncremental(&ctx.itemSets[idx], newItems)
			ctx.itemSets[idx].key = itemSetKey(ctx.itemSets[idx].items)
			ctx.itemSetMap[ctx.itemSets[idx].key] = idx
			if !(*inWorklist)[idx] {
				*worklist = append(*worklist, idx)
				(*inWorklist)[idx] = true
			}
		}
		return idx
	}

	// 3. No match at all — create new state.
	newIdx := len(ctx.itemSets)
	ctx.itemSets = append(ctx.itemSets, closedSet)
	ctx.itemSetMap[closedSet.key] = newIdx
	ctx.coreMap[core] = newIdx
	extKey := extendedMergeKey(closedSet.items, ctx.ng.Productions)
	mergeMap[extKey] = newIdx
	*worklist = append(*worklist, newIdx)
	(*inWorklist)[newIdx] = true
	return newIdx
}

// extendedMergeKey computes a merge key that includes both the core (prodIdx+dot)
// and the set of terminal symbols in reduce-item lookaheads. States with the same
// core but different reduce-terminal sets are kept separate to prevent lex mode
// pollution (where greedy patterns become valid in states where they shouldn't be).
func extendedMergeKey(items []lrItem, prods []Production) string {
	// Start with the core key.
	core := coreKey(items)

	// Collect the set of all terminal symbols appearing as reduce lookaheads.
	var reduceLookaheads []int
	seen := make(map[int]bool)
	for _, item := range items {
		prod := &prods[item.prodIdx]
		if item.dot >= len(prod.RHS) && !seen[item.lookahead] {
			// This is a reduce item — include its lookahead.
			seen[item.lookahead] = true
			reduceLookaheads = append(reduceLookaheads, item.lookahead)
		}
	}

	if len(reduceLookaheads) == 0 {
		// No reduce items — core-only merging is safe (pure shift states).
		return core
	}

	sort.Ints(reduceLookaheads)
	buf := make([]byte, 0, len(core)+1+len(reduceLookaheads)*2)
	buf = append(buf, core...)
	buf = append(buf, '|')
	for _, la := range reduceLookaheads {
		buf = append(buf, byte(la>>8), byte(la))
	}
	return string(buf)
}

// mergeItemsReturnNew adds items from src into dst using dst's persistent seen
// set, returning only the newly-added items.
func mergeItemsReturnNew(dst *lrItemSet, src []lrItem) []lrItem {
	var newItems []lrItem
	for _, item := range src {
		k := closureItemKey{item.prodIdx, item.dot, item.lookahead}
		if !dst.seen[k] {
			dst.seen[k] = true
			dst.items = append(dst.items, item)
			newItems = append(newItems, item)
		}
	}
	return newItems
}

// coreKey computes a key from only the (prodIdx, dot) pairs, ignoring lookaheads.
// States with the same core key are LALR-mergeable.
func coreKey(items []lrItem) string {
	// Collect unique (prodIdx, dot) pairs.
	type core struct{ prodIdx, dot int }
	seen := make(map[core]bool)
	var cores []core
	for _, item := range items {
		c := core{item.prodIdx, item.dot}
		if !seen[c] {
			seen[c] = true
			cores = append(cores, c)
		}
	}
	sort.Slice(cores, func(i, j int) bool {
		if cores[i].prodIdx != cores[j].prodIdx {
			return cores[i].prodIdx < cores[j].prodIdx
		}
		return cores[i].dot < cores[j].dot
	})

	buf := make([]byte, 0, len(cores)*4)
	for _, c := range cores {
		buf = append(buf,
			byte(c.prodIdx>>8), byte(c.prodIdx),
			byte(c.dot>>8), byte(c.dot),
		)
	}
	return string(buf)
}

// itemSetKey computes a canonical string key for an item set (full LR(1) key).
func itemSetKey(items []lrItem) string {
	// Sort items for canonical form.
	sorted := make([]lrItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].prodIdx != sorted[j].prodIdx {
			return sorted[i].prodIdx < sorted[j].prodIdx
		}
		if sorted[i].dot != sorted[j].dot {
			return sorted[i].dot < sorted[j].dot
		}
		return sorted[i].lookahead < sorted[j].lookahead
	})

	// Build key.
	buf := make([]byte, 0, len(sorted)*12)
	for _, item := range sorted {
		buf = append(buf,
			byte(item.prodIdx>>8), byte(item.prodIdx),
			byte(item.dot>>8), byte(item.dot),
			byte(item.lookahead>>8), byte(item.lookahead),
		)
	}
	return string(buf)
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
	// If we have a mix, keep only the non-extra ones.
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

		// Use precedence to resolve.
		// The shift prec comes from the item's production (attached during
		// table construction), not from a global symbol-to-prec lookup.
		shiftPrec := shift.prec
		reducePrec := prod.Prec

		if reducePrec != 0 || shiftPrec != 0 {
			if reducePrec > shiftPrec {
				return []lrAction{reduce}, nil
			}
			if shiftPrec > reducePrec {
				return []lrAction{shift}, nil
			}
			// Equal precedence: use associativity from the reduce production.
			switch prod.Assoc {
			case AssocLeft:
				return []lrAction{reduce}, nil
			case AssocRight:
				return []lrAction{shift}, nil
			case AssocNone:
				// Non-associative: neither action (error).
				return nil, nil
			}
		}

		// Check declared conflicts for GLR: if the shift and reduce involve
		// different nonterminals from the same conflict group, keep both.
		if shiftMatchesConflictGroup(shift, reduce.lhsSym, ng) {
			return actions, nil // keep both for GLR
		}
		// Also check using just the reduce LHS (backward compat).
		if isDeclaredConflict(reduce.prodIdx, ng) {
			return actions, nil // keep both for GLR
		}
		// Transitive check: trace hidden helper symbols back to ancestor
		// named symbols to find conflict group membership.
		if isTransitiveConflict(shift, reduce, ng) {
			return actions, nil // keep both for GLR
		}

		// Default: prefer shift (like yacc/bison).
		return []lrAction{shift}, nil
	}

	// Reduce/reduce conflict.
	if len(reduces) > 1 {
		// Check if all reduces are part of the same declared conflict group → GLR.
		if allInDeclaredConflict(reduces, ng) {
			return reduces, nil // keep all for GLR
		}

		// Higher precedence wins. At equal precedence, use tie-breaking:
		// 1. Prefer longer RHS (more specific/contextual production)
		// 2. Prefer lower production index (earlier in grammar)
		best := reduces[0]
		bestProd := &ng.Productions[best.prodIdx]
		for _, r := range reduces[1:] {
			rProd := &ng.Productions[r.prodIdx]
			if rProd.Prec > bestProd.Prec {
				best = r
				bestProd = rProd
			} else if rProd.Prec == bestProd.Prec {
				if len(rProd.RHS) > len(bestProd.RHS) {
					best = r
					bestProd = rProd
				} else if len(rProd.RHS) == len(bestProd.RHS) && r.prodIdx < best.prodIdx {
					best = r
					bestProd = rProd
				}
			}
		}
		return []lrAction{best}, nil
	}

	return actions, nil
}

// shiftMatchesConflictGroup checks if a shift action involves a nonterminal
// from the same declared conflict group as the given reduce LHS. It checks
// both the primary lhsSym and any accumulated lhsSyms from merged shifts.
func shiftMatchesConflictGroup(shift lrAction, reduceLHS int, ng *NormalizedGrammar) bool {
	if len(ng.Conflicts) == 0 {
		return false
	}
	// Collect all LHS symbols contributing to this shift.
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

// isDeclaredConflict checks if the production's LHS is part of a declared conflict.
func isDeclaredConflict(prodIdx int, ng *NormalizedGrammar) bool {
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

// isTransitiveConflict checks if a shift/reduce conflict should be kept
// as a GLR fork by tracing hidden helper symbols (from repeat/optional
// sugar) back to their ancestor named symbols and checking if siblings
// in the production chain are in declared conflict groups.
//
// This handles cases like C's typedef ambiguity where:
//   - shift: _empty_declaration → . type_specifier ; (shift primitive_type)
//   - reduce: __declaration_specifiers_repeat30 → ε
//
// The reduce's helper LHS traces up through _declaration_specifiers to
// declaration/function_definition, which has _declarator as a sibling.
// Since [type_specifier, _declarator] is a declared conflict, this should
// be a GLR fork.
func isTransitiveConflict(shift lrAction, reduce lrAction, ng *NormalizedGrammar) bool {
	if len(ng.Conflicts) == 0 {
		return false
	}

	// Build a set of all symbols in any conflict group for fast lookup.
	conflictSyms := make(map[int]bool)
	for _, cg := range ng.Conflicts {
		for _, s := range cg {
			conflictSyms[s] = true
		}
	}

	// Quick check: if neither side directly involves conflict symbols, check
	// if the reduce's LHS is a hidden helper that traces to conflict symbols.
	reduceLHS := ng.Productions[reduce.prodIdx].LHS
	if conflictSyms[reduceLHS] {
		return false // already handled by isDeclaredConflict
	}

	// Build reverse index: symbol → productions that use it on RHS.
	reverseIdx := make(map[int][]int) // symbol → production indices
	for i, prod := range ng.Productions {
		for _, s := range prod.RHS {
			reverseIdx[s] = append(reverseIdx[s], i)
		}
	}

	// BFS from reduceLHS upward through productions. At each level,
	// check if any sibling RHS symbol or the LHS is in a conflict group.
	// Also collect the shift-side conflict symbols for matching.
	allShiftLHS := make(map[int]bool)
	if shift.lhsSym != 0 {
		allShiftLHS[shift.lhsSym] = true
	}
	for _, s := range shift.lhsSyms {
		allShiftLHS[s] = true
	}

	// For the shift side, also check which conflict symbols appear in
	// productions that share an RHS with the shift's LHS.
	shiftConflictSyms := make(map[int]bool)
	for s := range allShiftLHS {
		if conflictSyms[s] {
			shiftConflictSyms[s] = true
		}
		// Check productions that contain s on RHS — their other RHS symbols
		// or LHS might be in conflict groups.
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

	// BFS upward from reduceLHS.
	visited := make(map[int]bool)
	visited[reduceLHS] = true
	queue := []int{reduceLHS}
	maxDepth := 4 // limit depth to avoid explosion

	for depth := 0; depth < maxDepth && len(queue) > 0; depth++ {
		var next []int
		for _, sym := range queue {
			for _, pi := range reverseIdx[sym] {
				prod := &ng.Productions[pi]
				// Check if the LHS or any sibling RHS symbol is in a conflict
				// group that also contains a shift-side conflict symbol.
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

				// Continue BFS upward through hidden/helper symbols.
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

// allInDeclaredConflict checks if all reduce actions have their LHS symbols
// in the same declared conflict group. This enables GLR forking.
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
