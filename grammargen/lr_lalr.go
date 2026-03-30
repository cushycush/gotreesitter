package grammargen

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"time"
)

// DeRemer/Pennello LALR(1) lookahead computation.
//
// Instead of building full LR(1) item sets with lookaheads (which is O(n²) for
// large grammars due to iterative merging), this builds:
//   1. An LR(0) automaton (cores only, no lookaheads) — very fast
//   2. Lookaheads for reduce items via READS/INCLUDES/LOOKBACK relations
//      resolved with Tarjan's SCC digraph — near-linear time
//
// References:
//   - DeRemer, Pennello: "Efficient Computation of LALR(1) Look-Ahead Sets" (1982)
//   - Grune, Jacobs: "Parsing Techniques: A Practical Guide", §9.7

// ntTransition identifies a nonterminal transition (p, A) in the LR(0) automaton,
// meaning: in state p, reading nonterminal A, go to some state q.
type ntTransition struct {
	state   int // source state p
	nonterm int // nonterminal symbol A
	target  int // target state q = GOTO(p, A)
}

// buildItemSetsLALR constructs LALR(1) item sets using the DeRemer/Pennello algorithm.
// Returns the item sets with lookaheads attached only to reduce items.
func (ctx *lrContext) buildItemSetsLALR() []lrItemSet {
	if ctx.bgCtx == nil {
		ctx.bgCtx = context.Background()
	}
	debugLALR := os.Getenv("GOT_DEBUG_LALR") == "1"

	// Phase 1: Build LR(0) automaton.
	t0 := time.Now()
	ctx.buildLR0()
	if debugLALR {
		debugLALRProgress("buildLR0",
			"dur=%v states=%d productions=%d transitions=%d",
			time.Since(t0), len(ctx.lalrLR0ItemSets), len(ctx.ng.Productions), countTransitionEdges(ctx.transitions))
	}
	if ctx.lalrLR0StateBudgetExceeded || ctx.lalrLR0CoreBudgetExceeded {
		return ctx.itemSets
	}
	ctx.maybeGCForLargeLALR()

	// Phase 2: Compute LALR(1) lookaheads via DeRemer/Pennello.
	t1 := time.Now()
	ctx.computeLALRLookaheads()
	if debugLALR {
		debugLALRProgress("computeLALRLookaheads", "dur=%v", time.Since(t1))
	}

	return ctx.itemSets
}

// buildLR0 constructs the LR(0) automaton: item sets with cores only, no lookaheads.
// This is much faster than the full LR(1) construction because there's no lookahead
// propagation, merging, or worklist re-processing.
func (ctx *lrContext) buildLR0() {
	ctx.transitions = make(map[int]map[int]int)
	ctx.itemSets = nil
	ctx.lalrLR0ItemSets = nil
	ctx.ensureProvenance()
	ng := ctx.ng
	tokenCount := ctx.tokenCount
	debugLALR := os.Getenv("GOT_DEBUG_LALR") == "1"
	ctx.ensureLR0SymbolSeenCapacity(len(ng.Symbols))

	// Hash map for state dedup: coreHash → chain of state indices.
	coreMap := make(map[uint64]*stateHashEntry)

	// Build initial state: closure of [S' → .S]
	initialSet := ctx.lr0Closure([]coreItem{{prodIdx: ng.AugmentProdID, dot: 0}})
	ctx.lalrLR0ItemSets = []lr0ItemSet{initialSet}
	addToHashMap(coreMap, initialSet.coreHash, 0)
	ctx.recordFreshState(0)
	totalCoreEntries := len(initialSet.cores)
	ctx.lalrLR0CoreEntries = totalCoreEntries
	if debugLALR {
		debugLALRProgress("buildLR0_initial",
			"states=%d initial_cores=%d productions=%d",
			len(ctx.lalrLR0ItemSets), len(initialSet.cores), len(ng.Productions))
	}
	if ctx.lalrLR0StateBudget > 0 && len(ctx.lalrLR0ItemSets) > ctx.lalrLR0StateBudget {
		ctx.lalrLR0StateBudgetExceeded = true
		return
	}
	if ctx.lalrLR0CoreBudget > 0 && totalCoreEntries > ctx.lalrLR0CoreBudget {
		ctx.lalrLR0CoreBudgetExceeded = true
		return
	}

	// BFS through states.
	for stateIdx := 0; stateIdx < len(ctx.lalrLR0ItemSets); stateIdx++ {
		// Check for cancellation periodically (every 64 iterations).
		if ctx.bgCtx != nil && stateIdx&63 == 0 {
			select {
			case <-ctx.bgCtx.Done():
				return
			default:
			}
		}
		itemSet := &ctx.lalrLR0ItemSets[stateIdx]
		if debugLALR && stateIdx > 0 && stateIdx%128 == 0 {
			debugLALRProgress("buildLR0_progress",
				"state=%d states=%d total_core_entries=%d transitions=%d current_cores=%d",
				stateIdx, len(ctx.lalrLR0ItemSets), totalCoreEntries, countTransitionEdges(ctx.transitions), len(itemSet.cores))
		}

		// Collect all symbols after the dot.
		symbolSeenEpoch := ctx.nextLR0SymbolSeenEpoch()
		syms := ctx.gotoSymbolsScratch[:0]
		for _, ce := range itemSet.cores {
			prodIdx := int(ce.prodIdx())
			dot := int(ce.dot())
			prod := &ng.Productions[prodIdx]
			if dot < len(prod.RHS) {
				sym := prod.RHS[dot]
				if ctx.lr0SymbolSeenGen[sym] != symbolSeenEpoch {
					ctx.lr0SymbolSeenGen[sym] = symbolSeenEpoch
					syms = append(syms, sym)
				}
			}
		}

		for _, sym := range syms {
			// Compute GOTO(state, sym): advance dot past sym, then close.
			kernel := ctx.lr0KernelScratch[:0]
			for _, ce := range itemSet.cores {
				prodIdx := int(ce.prodIdx())
				dot := int(ce.dot())
				prod := &ng.Productions[prodIdx]
				if dot < len(prod.RHS) && prod.RHS[dot] == sym {
					kernel = append(kernel, coreItem{prodIdx: prodIdx, dot: dot + 1})
				}
			}
			if len(kernel) == 0 {
				continue
			}

			closedSet := ctx.lr0Closure(kernel)
			ctx.lr0KernelScratch = kernel[:0]
			closedSet.annotationArgTag = ctx.templateContextTagForLR0Transition(stateIdx, sym, &closedSet)
			closedSet.annotationArgTag |= ctx.repeatWrapperSourceTagForLR0Transition(stateIdx, sym, &closedSet)
			closedSet.annotationArgTag |= ctx.conditionalTypeContextTagForLR0Transition(stateIdx, sym, &closedSet)

			// Find existing state with same core, or create new.
			targetIdx := -1
			for entry := coreMap[closedSet.coreHash]; entry != nil; entry = entry.next {
				if sameAnnotationArgTagLR0(&ctx.lalrLR0ItemSets[entry.stateIdx], &closedSet) &&
					sameSortedLR0CoreEntries(ctx.lalrLR0ItemSets[entry.stateIdx].cores, closedSet.cores) {
					targetIdx = entry.stateIdx
					ctx.recordMergedState(targetIdx, mergeOrigin{
						kernelHash:  closedSet.coreHash,
						sourceState: stateIdx,
					})
					break
				}
			}
			if targetIdx < 0 {
				targetIdx = len(ctx.lalrLR0ItemSets)
				ctx.lalrLR0ItemSets = append(ctx.lalrLR0ItemSets, closedSet)
				totalCoreEntries += len(closedSet.cores)
				ctx.lalrLR0CoreEntries = totalCoreEntries
				addToHashMap(coreMap, closedSet.coreHash, targetIdx)
				ctx.recordFreshState(targetIdx)
				if ctx.lalrLR0StateBudget > 0 && len(ctx.lalrLR0ItemSets) > ctx.lalrLR0StateBudget {
					ctx.lalrLR0StateBudgetExceeded = true
					if debugLALR {
						debugLALRProgress("buildLR0_budget_exceeded",
							"kind=states states=%d state_budget=%d core_entries=%d",
							len(ctx.lalrLR0ItemSets), ctx.lalrLR0StateBudget, totalCoreEntries)
					}
					return
				}
				if ctx.lalrLR0CoreBudget > 0 && totalCoreEntries > ctx.lalrLR0CoreBudget {
					ctx.lalrLR0CoreBudgetExceeded = true
					if debugLALR {
						debugLALRProgress("buildLR0_budget_exceeded",
							"kind=cores core_entries=%d core_budget=%d states=%d",
							totalCoreEntries, ctx.lalrLR0CoreBudget, len(ctx.lalrLR0ItemSets))
					}
					return
				}
			}

			// Record transition.
			if ctx.transitions[stateIdx] == nil {
				ctx.transitions[stateIdx] = make(map[int]int, len(syms))
			}
			ctx.transitions[stateIdx][sym] = targetIdx

			// After appending to itemSets, re-read pointer in case of slice realloc.
			itemSet = &ctx.lalrLR0ItemSets[stateIdx]
		}
		ctx.gotoSymbolsScratch = syms[:0]

		_ = tokenCount // used implicitly via lr0Closure
	}
}

// lr0Closure computes the LR(0) closure of a set of kernel items.
// No lookaheads are involved — just expands nonterminals to their productions.
func (ctx *lrContext) lr0Closure(kernel []coreItem) lr0ItemSet {
	ng := ctx.ng
	tokenCount := ctx.tokenCount

	for _, prodIdx := range ctx.dot0Dirty {
		ctx.dot0Index[prodIdx] = -1
	}
	ctx.dot0Dirty = ctx.dot0Dirty[:0]

	cores := make([]lr0CoreEntry, 0, len(kernel)*2)

	// Add kernel items.
	for _, ki := range kernel {
		idx := len(cores)
		cores = append(cores, packLR0CoreEntry(ki.prodIdx, ki.dot))
		if ki.dot == 0 {
			ctx.dot0Index[ki.prodIdx] = idx
			ctx.dot0Dirty = append(ctx.dot0Dirty, ki.prodIdx)
		}
	}

	// Expand: for each item [A → α.Bβ], add [B → .γ] for all B-productions.
	// Use a worklist but only process each core item once (no re-processing needed
	// since LR(0) closure doesn't change — there are no lookaheads to propagate).
	for i := 0; i < len(cores); i++ {
		ce := &cores[i]
		prodIdx := int(ce.prodIdx())
		dot := int(ce.dot())
		prod := &ng.Productions[prodIdx]
		if dot >= len(prod.RHS) {
			continue
		}

		nextSym := prod.RHS[dot]
		if nextSym < tokenCount {
			continue
		}

		for _, prodIdx := range ctx.prodsByLHS[nextSym] {
			if ctx.dot0Index[prodIdx] >= 0 {
				continue
			}
			idx := len(cores)
			ctx.dot0Index[prodIdx] = idx
			ctx.dot0Dirty = append(ctx.dot0Dirty, prodIdx)
			cores = append(cores, packLR0CoreEntry(prodIdx, 0))
		}
	}

	if len(cores) > 1 {
		sort.Slice(cores, func(i, j int) bool {
			if cores[i].prodIdx() != cores[j].prodIdx() {
				return cores[i].prodIdx() < cores[j].prodIdx()
			}
			return cores[i].dot() < cores[j].dot()
		})
	}
	// LR0 states are retained until lookahead computation finishes. For large
	// grammars, append growth leaves substantial spare capacity on these slices,
	// so copy them down to exact size before storing them on the context.
	if cap(cores) != len(cores) {
		tight := make([]lr0CoreEntry, len(cores))
		copy(tight, cores)
		cores = tight
	}

	set := lr0ItemSet{
		cores: cores,
	}
	// Compute only coreHash (fullHash and completionLAHash will be set after lookaheads).
	var ch uint64
	for _, c := range cores {
		ch += mixCoreItem(int(c.prodIdx()), int(c.dot()))
	}
	set.coreHash = ch

	return set
}

func packCoreItemKey(prodIdx, dot int) uint64 {
	return uint64(uint32(prodIdx))<<32 | uint64(uint32(dot))
}

// computeLALRLookaheads implements the DeRemer/Pennello algorithm to compute
// LALR(1) lookaheads for all reduce items in the LR(0) automaton.
func (ctx *lrContext) computeLALRLookaheads() {
	ng := ctx.ng
	tokenCount := ctx.tokenCount
	debugLALR := os.Getenv("GOT_DEBUG_LALR") == "1"

	// Step 1: Index all nonterminal transitions.
	var ntTrans []ntTransition
	ntTransIndex := make(map[[2]int]int) // (state, nonterm) → index in ntTrans

	type stateSymPair struct{ state, sym, target int }
	var ntPairs []stateSymPair
	for state, trans := range ctx.transitions {
		for sym, target := range trans {
			if sym >= tokenCount {
				ntPairs = append(ntPairs, stateSymPair{state, sym, target})
			}
		}
	}
	sort.Slice(ntPairs, func(i, j int) bool {
		if ntPairs[i].state != ntPairs[j].state {
			return ntPairs[i].state < ntPairs[j].state
		}
		return ntPairs[i].sym < ntPairs[j].sym
	})
	for _, p := range ntPairs {
		idx := len(ntTrans)
		ntTransIndex[[2]int{p.state, p.sym}] = idx
		ntTrans = append(ntTrans, ntTransition{
			state:   p.state,
			nonterm: p.sym,
			target:  p.target,
		})
	}
	if debugLALR {
		debugLALRProgress("lalr_nt_transitions",
			"num_trans=%d transition_edges=%d",
			len(ntTrans), countTransitionEdges(ctx.transitions))
	}
	if ctx.trackLookaheadContributors {
		ctx.lalrNTTransitions = append(ctx.lalrNTTransitions[:0], ntTrans...)
	}

	numTrans := len(ntTrans)
	if numTrans == 0 {
		return
	}

	// Step 2: Compute DR (Directly-Reads) sets.
	// DR(p, A) = { t ∈ Terminals | GOTO(p, A) has a shift on t }
	// i.e., terminals reachable in one step from the target state of (p, A).
	dr := make([]bitset, numTrans)
	for i, nt := range ntTrans {
		dr[i] = newBitset(tokenCount)
		q := nt.target // target state
		if trans, ok := ctx.transitions[q]; ok {
			for sym := range trans {
				if sym < tokenCount {
					dr[i].add(sym)
				}
			}
		}
	}

	// Seed $end into DR(0, start_symbol). The accept state (GOTO(0, start_symbol))
	// doesn't have a transition on $end, but $end is conceptually "readable" there
	// since the augmented production S' → S reduces on $end.
	startSym := ng.Productions[ng.AugmentProdID].RHS[0]
	if idx, ok := ntTransIndex[[2]int{0, startSym}]; ok {
		dr[idx].add(0) // $end = symbol 0
	}

	// Step 3: Compute READS relation.
	// (p, A) reads (q, C) iff GOTO(p, A) = q and C is nullable.
	// This means: from the target state of (p,A), if we can read a nullable
	// nonterminal C, then whatever C reads also contributes to Read(p,A).
	reads := make([][]uint32, numTrans)
	for i, nt := range ntTrans {
		q := nt.target
		if trans, ok := ctx.transitions[q]; ok {
			var nullableSyms []int
			for sym := range trans {
				if sym >= tokenCount && ctx.nullables[sym] {
					nullableSyms = append(nullableSyms, sym)
				}
			}
			sort.Ints(nullableSyms)
			for _, sym := range nullableSyms {
				if j, ok := ntTransIndex[[2]int{q, sym}]; ok {
					reads[i] = append(reads[i], uint32(j))
				}
			}
		}
	}

	// Step 4: Compute Read sets = Digraph(DR, READS).
	// Read(p, A) = DR(p, A) ∪ ∪{ Read(q, C) | (p,A) reads (q,C) }
	readSets := digraph(numTrans, dr, reads)
	if debugLALR {
		debugLALRProgress("lalr_reads",
			"num_trans=%d read_edges=%d",
			numTrans, countAdjacencyEdges(reads))
	}
	dr = nil
	reads = nil
	ctx.maybeGCForLargeLALR()

	// Step 5: Compute INCLUDES relation.
	// (p, A) includes (p', B) iff B → βAγ is a production, p' --β--> p,
	// and γ is nullable (γ ⇒* ε).
	//
	// For each production B → X₁X₂...Xₙ and each state p' that has this
	// production in its item set [B → .X₁...Xₙ], trace the path
	// p' → p₁ → p₂ → ... → pₙ through the automaton. For each position k
	// where Xₖ is a nonterminal A and Xₖ₊₁...Xₙ is nullable, add:
	// (pₖ₋₁, A=Xₖ) includes (p', B).
	//
	// At the same time, compute LOOKBACK:
	// (q, A → ω) lookback (p, A) iff p --ω--> q
	// i.e., from state p, reading the entire RHS of production "A → ω" leads to q.
	type lookbackEntry struct {
		stateIdx uint32 // state q where reduce happens
		coreIdx  uint32 // completed core entry for A → ω in state q
		ntIdx    uint32 // index into ntTrans for (p, A)
	}
	var lookbacks []lookbackEntry

	includes := make([][]uint32, numTrans)
	includePositionsByProd := make([][]int, len(ng.Productions))
	for pi := range ng.Productions {
		rhs := ng.Productions[pi].RHS
		if len(rhs) == 0 {
			continue
		}
		var positions []int
		suffixNullable := true
		for dot := len(rhs) - 1; dot >= 0; dot-- {
			sym := rhs[dot]
			if sym >= tokenCount && suffixNullable {
				positions = append(positions, dot)
			}
			if sym < tokenCount || !ctx.nullables[sym] {
				suffixNullable = false
			}
		}
		if len(positions) > 1 {
			for i, j := 0, len(positions)-1; i < j; i, j = i+1, j-1 {
				positions[i], positions[j] = positions[j], positions[i]
			}
		}
		includePositionsByProd[pi] = positions
	}

	includeEdgeCount := 0
	for stateIdx := range ctx.lalrLR0ItemSets {
		// Check for cancellation every 64 states.
		if ctx.bgCtx != nil && stateIdx&63 == 0 {
			select {
			case <-ctx.bgCtx.Done():
				return
			default:
			}
		}
		itemSet := &ctx.lalrLR0ItemSets[stateIdx]
		for _, ce := range itemSet.cores {
			if ce.dot() != 0 {
				continue
			}
			pi := int(ce.prodIdx())
			prod := &ng.Productions[pi]
			lhs := prod.LHS
			rhs := prod.RHS
			curState := stateIdx
			valid := true
			includePos := includePositionsByProd[pi]
			nextIncludeIdx := 0
			for dot, sym := range rhs {
				if nextIncludeIdx < len(includePos) && includePos[nextIncludeIdx] == dot {
					srcKey := [2]int{stateIdx, lhs}
					tgtKey := [2]int{curState, sym}
					if srcIdx, ok := ntTransIndex[srcKey]; ok {
						if tgtIdx, ok := ntTransIndex[tgtKey]; ok {
							includes[tgtIdx] = append(includes[tgtIdx], uint32(srcIdx))
							includeEdgeCount++
						}
					}
					nextIncludeIdx++
				}
				if trans, ok := ctx.transitions[curState]; ok {
					if next, ok := trans[sym]; ok {
						curState = next
					} else {
						valid = false
						break
					}
				} else {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			srcIdx, ok := ntTransIndex[[2]int{stateIdx, lhs}]
			if !ok {
				continue
			}
			if coreIdx, ok := ctx.lalrLR0ItemSets[curState].coreLookup(pi, len(rhs)); ok {
				lookbacks = append(lookbacks, lookbackEntry{
					stateIdx: uint32(curState),
					coreIdx:  uint32(coreIdx),
					ntIdx:    uint32(srcIdx),
				})
			}
		}
		if debugLALR && stateIdx > 0 && stateIdx%256 == 0 {
			debugLALRProgress("lalr_includes_progress",
				"state=%d states=%d includes_edges=%d lookbacks=%d",
				stateIdx, len(ctx.lalrLR0ItemSets), includeEdgeCount, len(lookbacks))
		}
	}
	if debugLALR {
		debugLALRProgress("lalr_includes_lookbacks",
			"includes_edges=%d lookbacks=%d productions=%d",
			includeEdgeCount, len(lookbacks), len(ng.Productions))
	}
	includePositionsByProd = nil
	ctx.maybeGCForLargeLALR()

	// Step 6: Compute Follow sets = Digraph(Read, INCLUDES).
	// Follow(p, A) = Read(p, A) ∪ ∪{ Follow(p', B) | (p,A) includes (p',B) }
	followSets := digraph(numTrans, readSets, includes)
	if debugLALR {
		debugLALRProgress("lalr_follow",
			"num_trans=%d includes_edges=%d",
			numTrans, countAdjacencyEdges(includes))
	}
	ctx.lalrFollowByTransition = make(map[[2]int]bitset, numTrans)
	for i, nt := range ntTrans {
		ctx.lalrFollowByTransition[[2]int{nt.state, nt.nonterm}] = followSets[i]
	}
	includes = nil
	readSets = nil
	ctx.maybeGCForLargeLALR()

	// Step 7: Compute LA (lookahead) sets for reduce items via LOOKBACK.
	// LA(q, A → ω) = ∪{ Follow(p, A) | (q, A → ω) lookback (p, A) }
	//
	// Keep reduce lookaheads separate until the end so LR(0) item sets stay in
	// their compact representation throughout phases 1 and 2.
	reduceLookaheads := make([]map[int]bitset, len(ctx.lalrLR0ItemSets))
	for _, lb := range lookbacks {
		laByCore := reduceLookaheads[lb.stateIdx]
		if laByCore == nil {
			laByCore = make(map[int]bitset)
			reduceLookaheads[lb.stateIdx] = laByCore
		}
		coreIdx := int(lb.coreIdx)
		if existing, ok := laByCore[coreIdx]; ok {
			existing.unionWith(&followSets[lb.ntIdx])
			laByCore[coreIdx] = existing
		} else {
			laByCore[coreIdx] = ctx.cloneLookaheadBitset(&followSets[lb.ntIdx])
		}
		followSets[lb.ntIdx].forEach(func(la int) {
			ctx.recordLookaheadContributor(int(lb.stateIdx), la, int(lb.ntIdx))
		})
	}

	// Step 8: Handle augmented start production: S' → S has lookahead {$end}.
	// The augmented production reduces in the state reached after reading S.
	augProd := &ng.Productions[ng.AugmentProdID]
	if len(augProd.RHS) > 0 {
		// Find the state reached from state 0 via the start symbol.
		if trans, ok := ctx.transitions[0]; ok {
			if targetState, ok := trans[augProd.RHS[0]]; ok {
				augSet := &ctx.lalrLR0ItemSets[targetState]
				if idx, ok := augSet.coreLookup(ng.AugmentProdID, len(augProd.RHS)); ok {
					laByCore := reduceLookaheads[targetState]
					if laByCore == nil {
						laByCore = make(map[int]bitset)
						reduceLookaheads[targetState] = laByCore
					}
					if existing, ok := laByCore[idx]; ok {
						existing.add(0)
						laByCore[idx] = existing
					} else {
						la := ctx.allocLookaheadBitset()
						la.add(0)
						laByCore[idx] = la
					}
				}
			}
		}
	}

	ctx.materializeLALRItemSets(reduceLookaheads)
	lookbacks = nil
	followSets = nil
	reduceLookaheads = nil
	ctx.maybeGCForLargeLALR()
	ctx.pruneConditionalTypeQmarkLookaheads()

	// Recompute hashes now that lookaheads are populated.
	for i := range ctx.itemSets {
		ctx.itemSets[i].computeHashes(ng.Productions, &ctx.boundaryLookaheads, false)
	}
	if debugLALR {
		debugLALRProgress("lalr_hash_recompute", "states=%d", len(ctx.itemSets))
	}
}

func (ctx *lrContext) materializeLALRItemSets(reduceLookaheads []map[int]bitset) {
	ctx.itemSets = make([]lrItemSet, len(ctx.lalrLR0ItemSets))
	for i := range ctx.lalrLR0ItemSets {
		lr0Set := &ctx.lalrLR0ItemSets[i]
		cores := make([]coreEntry, len(lr0Set.cores))
		laByCore := reduceLookaheads[i]
		for ci, ce := range lr0Set.cores {
			cores[ci] = coreEntry{
				prodIdx: ce.prodIdx(),
				dot:     uint32(ce.dot()),
			}
			if laByCore != nil {
				if la, ok := laByCore[ci]; ok {
					cores[ci].lookaheads = la
				}
			}
		}
		ctx.itemSets[i] = lrItemSet{
			cores:            cores,
			coreHash:         lr0Set.coreHash,
			fullHash:         lr0Set.coreHash,
			completionLAHash: lr0Set.coreHash,
			boundaryLAHash:   lr0Set.coreHash,
			annotationArgTag: lr0Set.annotationArgTag,
		}
		lr0Set.cores = nil
	}
	ctx.lalrLR0ItemSets = nil
}

func (ctx *lrContext) pruneConditionalTypeQmarkLookaheads() {
	if ctx == nil || ctx.conditionalTypeExternalQmarkSym < 0 || ctx.conditionalTypePlainQmarkSym < 0 {
		return
	}
	for i := range ctx.itemSets {
		set := &ctx.itemSets[i]
		if set.annotationArgTag&conditionalTypeContextTag == 0 {
			continue
		}
		for ci := range set.cores {
			ce := &set.cores[ci]
			if !ce.lookaheads.contains(ctx.conditionalTypeExternalQmarkSym) || !ce.lookaheads.contains(ctx.conditionalTypePlainQmarkSym) {
				continue
			}
			ce.lookaheads.clear(ctx.conditionalTypeExternalQmarkSym)
		}
	}
}

// digraph implements Tarjan's SCC-based algorithm for computing F(x) across a
// relation R, given initial values f(x):
//
//	F(x) = f(x) ∪ ∪{ F(y) | x R y }
//
// This is the core algorithm from DeRemer & Pennello (1982). It visits each
// node at most twice (push + pop), making it near-linear.
//
// n: number of nodes
// f: initial values f(0..n-1), each a bitset
// rel: adjacency list for relation R: rel[x] = list of y such that x R y
// bitcap: capacity for new bitsets
//
// Returns F(0..n-1).
func digraph(n int, f []bitset, rel [][]uint32) []bitset {
	result := make([]bitset, n)
	for i := 0; i < n; i++ {
		result[i] = f[i].clone()
	}

	// Tarjan's SCC stack and state.
	const infinity = 0x7FFFFFFF
	depth := make([]int, n) // 0 = unvisited, >0 = stack depth, infinity = done
	stack := make([]int, 0, n)
	d := 0 // current depth counter

	var traverse func(x int)
	traverse = func(x int) {
		d++
		depth[x] = d
		stack = append(stack, x)

		for _, y32 := range rel[x] {
			y := int(y32)
			if depth[y] == 0 {
				traverse(y)
			}
			// If y is still on the stack (not yet assigned to an SCC),
			// propagate its result into x and update x's depth.
			if depth[y] < depth[x] {
				depth[x] = depth[y]
			}
			result[x].unionWith(&result[y])
		}

		// If x is the root of an SCC, pop the SCC and assign the same result.
		if depth[x] == d {
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				depth[top] = infinity
				if top == x {
					break
				}
				// All nodes in this SCC get the same result.
				result[top] = result[x].clone()
			}
		}
		d--
	}

	for i := 0; i < n; i++ {
		if depth[i] == 0 {
			traverse(i)
		}
	}

	return result
}

func debugLALRProgress(stage, format string, args ...any) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr,
		"[LALR] stage=%s alloc=%.1fMi heap_alloc=%.1fMi heap_inuse=%.1fMi sys=%.1fMi objs=%d gc=%d %s\n",
		stage,
		float64(ms.Alloc)/(1024*1024),
		float64(ms.HeapAlloc)/(1024*1024),
		float64(ms.HeapInuse)/(1024*1024),
		float64(ms.Sys)/(1024*1024),
		ms.HeapObjects,
		ms.NumGC,
		msg,
	)
}

func countAdjacencyEdges(rel [][]uint32) int {
	total := 0
	for _, edges := range rel {
		total += len(edges)
	}
	return total
}

func countTransitionEdges(transitions map[int]map[int]int) int {
	total := 0
	for _, edges := range transitions {
		total += len(edges)
	}
	return total
}

func (ctx *lrContext) maybeGCForLargeLALR() {
	if ctx == nil || ctx.lalrLR0CoreEntries < 50_000_000 {
		return
	}
	debug.FreeOSMemory()
}
