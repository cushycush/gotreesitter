package grammargen

import (
	"context"
	"fmt"
	"math/bits"
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
	state   uint32 // source state p
	nonterm uint32 // nonterminal symbol A
	target  uint32 // target state q = GOTO(p, A)
}

type ntTransitionIndex struct {
	nonterm uint32
	idx     uint32
}

type ntTransitionIndexRow []ntTransitionIndex

func ntTransitionIndexLookup(rows []ntTransitionIndexRow, state, sym int) (int, bool) {
	if state < 0 || state >= len(rows) {
		return 0, false
	}
	row := rows[state]
	if len(row) == 0 {
		return 0, false
	}
	want := uint32(sym)
	idx := sort.Search(len(row), func(i int) bool {
		return row[i].nonterm >= want
	})
	if idx < len(row) && row[idx].nonterm == want {
		return int(row[idx].idx), true
	}
	return 0, false
}

type adjacencyRows struct {
	offsets []uint32
	edges   []uint32
}

type packedAdjacencyRows struct {
	offsets     []uint32
	words       []uint64
	bitsPerEdge uint8
	mask        uint64
}

func newAdjacencyRows(counts []uint32) adjacencyRows {
	offsets := make([]uint32, len(counts)+1)
	total := uint32(0)
	for i, count := range counts {
		offsets[i] = total
		total += count
	}
	offsets[len(counts)] = total
	return adjacencyRows{
		offsets: offsets,
		edges:   make([]uint32, int(total)),
	}
}

func newPackedAdjacencyRows(counts []uint32) packedAdjacencyRows {
	return newPackedAdjacencyRowsForMaxValue(counts, len(counts)-1)
}

func newPackedAdjacencyRowsForMaxValue(counts []uint32, maxValue int) packedAdjacencyRows {
	offsets := make([]uint32, len(counts)+1)
	total := uint32(0)
	for i, count := range counts {
		offsets[i] = total
		total += count
	}
	offsets[len(counts)] = total
	bitsPerEdge := bits.Len(uint(maxValue))
	if bitsPerEdge == 0 {
		bitsPerEdge = 1
	}
	totalBits := uint64(total) * uint64(bitsPerEdge)
	return packedAdjacencyRows{
		offsets:     offsets,
		words:       make([]uint64, int((totalBits+63)/64)),
		bitsPerEdge: uint8(bitsPerEdge),
		mask:        (uint64(1) << bitsPerEdge) - 1,
	}
}

func (rows adjacencyRows) row(i int) []uint32 {
	if i < 0 || i+1 >= len(rows.offsets) {
		return nil
	}
	start := rows.offsets[i]
	end := rows.offsets[i+1]
	return rows.edges[start:end]
}

func (rows packedAdjacencyRows) set(idx, value uint32) {
	if uint64(value) > rows.mask {
		panic(fmt.Sprintf("packed adjacency value out of range: %d", value))
	}
	bitOffset := uint64(idx) * uint64(rows.bitsPerEdge)
	wordIdx := bitOffset / 64
	shift := bitOffset % 64
	raw := uint64(value) & rows.mask
	rows.words[wordIdx] |= raw << shift
	if shift+uint64(rows.bitsPerEdge) > 64 {
		rows.words[wordIdx+1] |= raw >> (64 - shift)
	}
}

func (rows packedAdjacencyRows) edgeAt(idx uint32) int {
	bitOffset := uint64(idx) * uint64(rows.bitsPerEdge)
	wordIdx := bitOffset / 64
	shift := bitOffset % 64
	raw := rows.words[wordIdx] >> shift
	if shift+uint64(rows.bitsPerEdge) > 64 {
		raw |= rows.words[wordIdx+1] << (64 - shift)
	}
	return int(raw & rows.mask)
}

func (rows packedAdjacencyRows) forEachRange(start, end uint32, fn func(int)) {
	if start >= end {
		return
	}
	step := uint64(rows.bitsPerEdge)
	wordIdx := (uint64(start) * step) / 64
	shift := (uint64(start) * step) % 64
	for idx := start; idx < end; idx++ {
		raw := rows.words[wordIdx] >> shift
		if shift+step > 64 {
			raw |= rows.words[wordIdx+1] << (64 - shift)
		}
		fn(int(raw & rows.mask))
		shift += step
		wordIdx += shift / 64
		shift %= 64
	}
}

func (rows packedAdjacencyRows) forEachRow(i int, fn func(int)) {
	if i < 0 || i+1 >= len(rows.offsets) {
		return
	}
	rows.forEachRange(rows.offsets[i], rows.offsets[i+1], fn)
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
	ctx.prepareLALRStatesForLookaheads()

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
	ctx.transitions = nil
	ctx.itemSets = nil
	ctx.lalrLR0ItemSets = nil
	ctx.ensureProvenance()
	ng := ctx.ng
	tokenCount := ctx.tokenCount
	debugLALR := os.Getenv("GOT_DEBUG_LALR") == "1"
	ctx.ensureLR0SymbolSeenCapacity(len(ng.Symbols))
	ctx.ensureLR0SymbolBucketCapacity(len(ng.Symbols))
	ctx.configureRetainedLR0Packing()
	contextTagsEnabled := os.Getenv("GOT_LR_DISABLE_CONTEXT_TAGS") != "1" && len(ng.Productions) >= 2000
	if contextTagsEnabled {
		ctx.ensureRepeatWrapperLHS()
		ctx.ensureLR0RepeatSourceCapacity(len(ng.Symbols))
	}

	// Hash map for state dedup: coreHash → chain of state indices.
	coreMap := make(map[uint64]*stateHashEntry)

	// Build initial state: closure of [S' → .S]
	initialSet := ctx.retainLR0ItemSet(ctx.lr0Closure([]coreItem{{prodIdx: ng.AugmentProdID, dot: 0}}))
	ctx.lalrLR0ItemSets = []lr0ItemSet{initialSet}
	addToHashMap(coreMap, initialSet.coreHash, 0)
	ctx.recordFreshState(0)
	totalCoreEntries := initialSet.coreLen()
	ctx.lalrLR0CoreEntries = totalCoreEntries
	if debugLALR {
		debugLALRProgress("buildLR0_initial",
			"states=%d initial_cores=%d productions=%d",
			len(ctx.lalrLR0ItemSets), initialSet.coreLen(), len(ng.Productions))
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
				stateIdx, len(ctx.lalrLR0ItemSets), totalCoreEntries, countTransitionEdges(ctx.transitions), itemSet.coreLen())
		}

		// Collect all symbols after the dot.
		symbolSeenEpoch := ctx.nextLR0SymbolSeenEpoch()
		repeatRecursiveEpoch := uint32(0)
		if contextTagsEnabled {
			repeatRecursiveEpoch = ctx.nextLR0RepeatSourceEpoch()
		}
		syms := ctx.gotoSymbolsScratch[:0]
		bucketCounts := ctx.lr0SymbolBucketCount
		bucketOffsets := ctx.lr0SymbolBucketOffset
		targetRepeatWrapperLHSBySym := ctx.lr0TargetRepeatWrapper
		sourceTemplateCarrier := false
		sourceConditionalTypeEntry := false
		itemSet.forEachCore(func(ce lr0CoreEntry) {
			prodIdx := int(ce.prodIdx())
			dot := int(ce.dot())
			prod := &ng.Productions[prodIdx]
			if dot < len(prod.RHS) {
				sym := prod.RHS[dot]
				bucketIdx := 0
				if ctx.lr0SymbolSeenGen[sym] != symbolSeenEpoch {
					ctx.lr0SymbolSeenGen[sym] = symbolSeenEpoch
					bucketIdx = len(syms)
					ctx.lr0SymbolBucketIdx[sym] = bucketIdx
					syms = append(syms, sym)
					bucketCounts[bucketIdx] = 1
					targetRepeatWrapperLHSBySym[bucketIdx] = -1
				} else {
					bucketIdx = ctx.lr0SymbolBucketIdx[sym]
					bucketCounts[bucketIdx]++
				}
				nextDot := dot + 1
				if contextTagsEnabled &&
					targetRepeatWrapperLHSBySym[bucketIdx] < 0 &&
					sym >= tokenCount &&
					nextDot == len(prod.RHS) &&
					len(prod.RHS) == 1 &&
					prod.LHS >= 0 &&
					prod.LHS < len(ctx.repeatWrapperLHS) &&
					ctx.repeatWrapperLHS[prod.LHS] {
					targetRepeatWrapperLHSBySym[bucketIdx] = prod.LHS
				}
			}
			if !contextTagsEnabled {
				return
			}
			lhs := prod.LHS
			if !sourceTemplateCarrier {
				switch lhs {
				case ctx.bracedTemplateBodySym, ctx.bracedTemplateBody1Sym, ctx.bracedTemplateBody2Sym:
					sourceTemplateCarrier = true
				default:
					if lhs >= 0 && lhs < len(ctx.templateDefinitionCarrierLHS) && ctx.templateDefinitionCarrierLHS[lhs] {
						sourceTemplateCarrier = true
					}
				}
			}
			if !sourceConditionalTypeEntry &&
				lhs == ctx.conditionalTypeSym &&
				len(prod.RHS) >= 4 &&
				prod.RHS[1] == ctx.conditionalTypeExtendsSym &&
				prod.RHS[3] == ctx.conditionalTypePlainQmarkSym &&
				dot == 1 {
				sourceConditionalTypeEntry = true
			}
			if lhs < 0 || lhs >= len(ctx.repeatWrapperLHS) || !ctx.repeatWrapperLHS[lhs] || dot != len(prod.RHS) {
				return
			}
			for _, rhsSym := range prod.RHS {
				if rhsSym == lhs {
					ctx.lr0RepeatSourceGen[lhs] = repeatRecursiveEpoch
					break
				}
			}
		})

		totalKernelItems := 0
		for idx := range syms {
			bucketOffsets[idx] = totalKernelItems
			totalKernelItems += bucketCounts[idx]
			bucketCounts[idx] = bucketOffsets[idx]
		}
		if totalKernelItems > cap(ctx.lr0KernelScratch) {
			ctx.lr0KernelScratch = make([]coreItem, totalKernelItems)
		}
		kernelScratch := ctx.lr0KernelScratch[:totalKernelItems]
		if totalKernelItems > 0 {
			itemSet.forEachCore(func(ce lr0CoreEntry) {
				prodIdx := int(ce.prodIdx())
				dot := int(ce.dot())
				prod := &ng.Productions[prodIdx]
				if dot >= len(prod.RHS) {
					return
				}
				sym := prod.RHS[dot]
				bucketIdx := ctx.lr0SymbolBucketIdx[sym]
				writePos := bucketCounts[bucketIdx]
				kernelScratch[writePos] = coreItem{prodIdx: prodIdx, dot: dot + 1}
				bucketCounts[bucketIdx] = writePos + 1
			})
		}

		for idx, sym := range syms {
			// Compute GOTO(state, sym): advance dot past sym, then close.
			kernel := kernelScratch[bucketOffsets[idx]:bucketCounts[idx]]
			targetRepeatWrapperLHS := targetRepeatWrapperLHSBySym[idx]
			if len(kernel) == 0 {
				continue
			}

			closedSet := ctx.lr0Closure(kernel)
			if contextTagsEnabled {
				targetTemplateCarrier := false
				targetConditionalCarrier := false
				for _, ce := range closedSet.cores {
					lhs := ng.Productions[int(ce.prodIdx())].LHS
					if !targetTemplateCarrier {
						switch lhs {
						case ctx.bracedTemplateBodySym, ctx.bracedTemplateBody1Sym, ctx.bracedTemplateBody2Sym:
							targetTemplateCarrier = true
						default:
							if lhs >= 0 && lhs < len(ctx.templateDefinitionCarrierLHS) && ctx.templateDefinitionCarrierLHS[lhs] {
								targetTemplateCarrier = true
							}
						}
					}
					if !targetConditionalCarrier &&
						lhs >= 0 &&
						lhs < len(ctx.conditionalTypeCarrierLHS) &&
						ctx.conditionalTypeCarrierLHS[lhs] {
						targetConditionalCarrier = true
					}
					if targetTemplateCarrier && targetConditionalCarrier {
						break
					}
				}
				srcTemplateTag := itemSet.annotationArgTag & templateContextTagMask
				if srcTemplateTag != 0 && targetRepeatWrapperLHS >= 0 {
					closedSet.annotationArgTag = srcTemplateTag
				} else if sourceTemplateCarrier || targetTemplateCarrier {
					if ctx.annotationAtSym >= 0 && sym == ctx.annotationAtSym && targetTemplateCarrier {
						if srcTemplateTag != 0 && srcTemplateTag != templateContextPendingTag {
							closedSet.annotationArgTag = srcTemplateTag
						} else {
							closedSet.annotationArgTag = templateContextPendingTag
						}
					} else if sym >= 0 && sym < len(ctx.definitionBoundaryTagBySym) {
						if tag := ctx.definitionBoundaryTagBySym[sym]; tag != 0 && (sourceTemplateCarrier || srcTemplateTag != 0 || targetTemplateCarrier) {
							closedSet.annotationArgTag = tag
						}
					} else if srcTemplateTag != 0 && targetTemplateCarrier {
						closedSet.annotationArgTag = srcTemplateTag
					}
				}
				if targetRepeatWrapperLHS >= 0 && ctx.lr0RepeatSourceGen[targetRepeatWrapperLHS] == repeatRecursiveEpoch {
					closedSet.annotationArgTag |= 1 << 24
				}
				if targetConditionalCarrier &&
					(itemSet.annotationArgTag&conditionalTypeContextTag != 0 ||
						(sym == ctx.conditionalTypeExtendsSym && sym != ctx.conditionalTypePlainQmarkSym && sourceConditionalTypeEntry)) {
					closedSet.annotationArgTag |= conditionalTypeContextTag
				}
			}

			// Find existing state with same core, or create new.
			targetIdx := -1
			for entry := coreMap[closedSet.coreHash]; entry != nil; entry = entry.next {
				if sameAnnotationArgTagLR0(&ctx.lalrLR0ItemSets[entry.stateIdx], &closedSet) &&
					sameSortedLR0CoreEntriesSet(&ctx.lalrLR0ItemSets[entry.stateIdx], closedSet.cores) {
					targetIdx = entry.stateIdx
					ctx.recordMergedState(targetIdx, mergeOrigin{
						kernelHash:  closedSet.coreHash,
						sourceState: stateIdx,
					})
					break
				}
			}
			if targetIdx < 0 {
				closedSet = ctx.retainLR0ItemSet(closedSet)
				targetIdx = len(ctx.lalrLR0ItemSets)
				ctx.lalrLR0ItemSets = append(ctx.lalrLR0ItemSets, closedSet)
				totalCoreEntries += closedSet.coreLen()
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
			} else {
				ctx.lr0ClosureScratch = closedSet.cores[:0]
			}

			// Record transition.
			ctx.addTransition(stateIdx, sym, targetIdx)

			// After appending to itemSets, re-read pointer in case of slice realloc.
			itemSet = &ctx.lalrLR0ItemSets[stateIdx]
		}
		ctx.sortStateTransitions(stateIdx)
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

	cores := ctx.lr0ClosureScratch[:0]
	if cap(cores) < len(kernel)*2 {
		cores = make([]lr0CoreEntry, 0, len(kernel)*2)
	}

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

func (ctx *lrContext) retainLR0ItemSet(set lr0ItemSet) lr0ItemSet {
	if len(set.cores) == 0 {
		ctx.lr0ClosureScratch = set.cores[:0]
		set.retainedChunk = -1
		return set
	}
	if ctx.lr0PackedRetentionEnabled {
		set.coreCount = len(set.cores)
		set.packedCoreBits = ctx.lr0PackedCoreBits
		set.packedProdBits = ctx.lr0PackedProdBits
		set.packedCores = ctx.retainPackedLR0Cores(set.cores)
		ctx.lr0ClosureScratch = set.cores[:0]
		set.cores = nil
		set.retainedChunk = -1
		return set
	}
	tight, chunkIdx := ctx.retainLR0Cores(set.cores)
	ctx.lr0ClosureScratch = set.cores[:0]
	set.cores = tight
	set.retainedChunk = chunkIdx
	return set
}

func (ctx *lrContext) lalrStateCount() int {
	if len(ctx.lalrLR0ItemSets) > 0 {
		return len(ctx.lalrLR0ItemSets)
	}
	if len(ctx.lalrDot0Rows.offsets) > 0 {
		return len(ctx.lalrDot0Rows.offsets) - 1
	}
	return 0
}

func (ctx *lrContext) forEachLALRDot0Prod(state int, fn func(int)) {
	if state < 0 {
		return
	}
	if len(ctx.lalrDot0Rows.offsets) > 0 {
		ctx.lalrDot0Rows.forEachRow(state, fn)
		return
	}
	itemSet := &ctx.lalrLR0ItemSets[state]
	itemSet.forEachCore(func(ce lr0CoreEntry) {
		if ce.dot() == 0 {
			fn(int(ce.prodIdx()))
		}
	})
}

func (ctx *lrContext) hasLALRCompletedProd(state, prodIdx int) bool {
	if state < 0 {
		return false
	}
	if len(ctx.lalrCompletedRows.offsets) > 0 {
		start := ctx.lalrCompletedRows.offsets[state]
		end := ctx.lalrCompletedRows.offsets[state+1]
		lo, hi := start, end
		for lo < hi {
			mid := lo + (hi-lo)/2
			midProd := ctx.lalrCompletedRows.edgeAt(mid)
			if midProd < prodIdx {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo < end && ctx.lalrCompletedRows.edgeAt(lo) == prodIdx
	}
	_, ok := ctx.lalrLR0ItemSets[state].coreLookup(prodIdx, len(ctx.ng.Productions[prodIdx].RHS))
	return ok
}

func isLALRKernelCore(stateIdx, prodIdx int, dot uint8, augmentProdID int) bool {
	return dot > 0 || (stateIdx == 0 && prodIdx == augmentProdID && dot == 0)
}

func (ctx *lrContext) summarizeAndReleaseLALRStates() {
	if ctx == nil || len(ctx.lalrLR0ItemSets) == 0 || ctx.lalrLR0CoreEntries < 50_000_000 {
		return
	}
	stateCount := len(ctx.lalrLR0ItemSets)
	kernelCounts := make([]uint32, stateCount)
	dot0Counts := make([]uint32, stateCount)
	completedCounts := make([]uint32, stateCount)
	maxKernelCoreRaw := uint32(0)
	for stateIdx := range ctx.lalrLR0ItemSets {
		ctx.lalrLR0ItemSets[stateIdx].forEachCore(func(ce lr0CoreEntry) {
			prodIdx := int(ce.prodIdx())
			if isLALRKernelCore(stateIdx, prodIdx, ce.dot(), ctx.ng.AugmentProdID) {
				kernelCounts[stateIdx]++
				if raw := uint32(ce); raw > maxKernelCoreRaw {
					maxKernelCoreRaw = raw
				}
			}
			if ce.dot() == 0 {
				dot0Counts[stateIdx]++
			}
			if int(ce.dot()) == len(ctx.ng.Productions[prodIdx].RHS) {
				completedCounts[stateIdx]++
			}
		})
	}
	ctx.lalrKernelRows = newPackedAdjacencyRowsForMaxValue(kernelCounts, int(maxKernelCoreRaw))
	ctx.lalrDot0Rows = newPackedAdjacencyRowsForMaxValue(dot0Counts, len(ctx.ng.Productions)-1)
	ctx.lalrCompletedRows = newPackedAdjacencyRowsForMaxValue(completedCounts, len(ctx.ng.Productions)-1)
	kernelWriteOffsets := append([]uint32(nil), ctx.lalrKernelRows.offsets[:stateCount]...)
	dot0WriteOffsets := append([]uint32(nil), ctx.lalrDot0Rows.offsets[:stateCount]...)
	completedWriteOffsets := append([]uint32(nil), ctx.lalrCompletedRows.offsets[:stateCount]...)
	chunkRefs := make([]int, len(ctx.lr0RetainedChunks))
	for i := range ctx.lalrLR0ItemSets {
		if chunkIdx := ctx.lalrLR0ItemSets[i].retainedChunk; chunkIdx >= 0 && chunkIdx < len(chunkRefs) {
			chunkRefs[chunkIdx]++
		}
	}
	for stateIdx := range ctx.lalrLR0ItemSets {
		set := &ctx.lalrLR0ItemSets[stateIdx]
		set.forEachCore(func(ce lr0CoreEntry) {
			prodIdx := int(ce.prodIdx())
			if isLALRKernelCore(stateIdx, prodIdx, ce.dot(), ctx.ng.AugmentProdID) {
				writeIdx := kernelWriteOffsets[stateIdx]
				ctx.lalrKernelRows.set(writeIdx, uint32(ce))
				kernelWriteOffsets[stateIdx] = writeIdx + 1
			}
			if ce.dot() == 0 {
				writeIdx := dot0WriteOffsets[stateIdx]
				ctx.lalrDot0Rows.set(writeIdx, uint32(prodIdx))
				dot0WriteOffsets[stateIdx] = writeIdx + 1
			}
			if int(ce.dot()) == len(ctx.ng.Productions[prodIdx].RHS) {
				writeIdx := completedWriteOffsets[stateIdx]
				ctx.lalrCompletedRows.set(writeIdx, uint32(prodIdx))
				completedWriteOffsets[stateIdx] = writeIdx + 1
			}
		})
		oldChunk := set.retainedChunk
		set.cores = nil
		set.packedCores = nil
		set.coreCount = 0
		set.packedCoreBits = 0
		set.packedProdBits = 0
		set.retainedChunk = -1
		if oldChunk >= 0 && oldChunk < len(chunkRefs) {
			chunkRefs[oldChunk]--
			if chunkRefs[oldChunk] == 0 {
				ctx.lr0RetainedChunks[oldChunk] = nil
			}
		}
		if stateIdx > 0 && stateIdx%4096 == 0 {
			ctx.maybeGCForLargeLALR()
		}
	}
	ctx.lr0RetainedChunks = nil
	ctx.lr0RetainedChunkUsed = 0
	ctx.lr0RetainedPackedChunks = nil
	ctx.lr0RetainedPackedChunkUsed = 0
	ctx.lalrLR0Released = true
	ctx.maybeGCForLargeLALR()
}

func (ctx *lrContext) prepareLALRStatesForLookaheads() {
	if ctx == nil {
		return
	}
	ctx.packRetainedLR0ItemSets()
	ctx.summarizeAndReleaseLALRStates()
	ctx.maybeGCForLargeLALR()
}

func packCoreItemKey(prodIdx, dot int) uint64 {
	return uint64(uint32(prodIdx))<<32 | uint64(uint32(dot))
}

type lalrIncludeGraph struct {
	rows      adjacencyRows
	packed    packedAdjacencyRows
	usePacked bool
	edgeCount int
}

// computeLALRLookaheads implements the DeRemer/Pennello algorithm to compute
// LALR(1) lookaheads for all reduce items in the LR(0) automaton.
func (ctx *lrContext) computeLALRLookaheads() {
	ng := ctx.ng
	tokenCount := ctx.tokenCount
	debugLALR := os.Getenv("GOT_DEBUG_LALR") == "1"

	// Step 1: Index all nonterminal transitions.
	ntTargets, ntTransRows := ctx.indexLALRNonterminalTransitions(tokenCount, debugLALR)
	numTrans := len(ntTargets)
	if numTrans == 0 {
		return
	}

	readSets := ctx.computeLALRReadSets(ng, tokenCount, ntTargets, ntTransRows, debugLALR)
	ntTargets = nil
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
	includeGraph, canceled := ctx.buildLALRIncludeGraph(ng, tokenCount, ntTransRows, numTrans, debugLALR)
	if canceled {
		return
	}
	if debugLALR {
		debugLALRProgress("lalr_includes_lookbacks",
			"includes_edges=%d productions=%d",
			includeGraph.edgeCount, len(ng.Productions))
	}
	ctx.maybeGCForLargeLALR()

	// Step 6: Compute Follow sets = Digraph(Read, INCLUDES).
	// Follow(p, A) = Read(p, A) ∪ ∪{ Follow(p', B) | (p,A) includes (p',B) }
	followSets := readSets
	if includeGraph.edgeCount > 0 {
		if includeGraph.usePacked {
			followSets = digraphPackedInPlace(numTrans, readSets, includeGraph.packed)
		} else {
			followSets = digraphInPlace(numTrans, readSets, includeGraph.rows)
		}
	}
	if debugLALR {
		debugLALRProgress("lalr_follow",
			"num_trans=%d includes_edges=%d",
			numTrans, includeGraph.edgeCount)
	}
	if ng.EnableLRSplitting {
		ctx.lalrFollowSets = followSets
		ctx.lalrNTTransitionRows = append(ctx.lalrNTTransitionRows[:0], ntTransRows...)
	} else {
		ctx.lalrFollowSets = nil
		ctx.lalrNTTransitionRows = nil
	}
	ctx.lalrFollowByTransition = nil
	includeGraph.rows = adjacencyRows{}
	includeGraph.packed = packedAdjacencyRows{}
	readSets = nil
	ctx.maybeGCForLargeLALR()

	// Step 7: Compute LA (lookahead) sets for reduce items via LOOKBACK.
	// LA(q, A → ω) = ∪{ Follow(p, A) | (q, A → ω) lookback (p, A) }
	//
	// Run this as a second pass after Follow sets exist so we do not retain the
	// full LOOKBACK relation in memory for large grammars.
	stateCount := ctx.lalrStateCount()
	reduceLookaheads := make([]map[int]bitset, stateCount)
	trackLookaheadContributors := ctx.provenance != nil && ctx.trackLookaheadContributors
	lookbackCount := 0
	for stateIdx := 0; stateIdx < stateCount; stateIdx++ {
		if ctx.bgCtx != nil && stateIdx&63 == 0 {
			select {
			case <-ctx.bgCtx.Done():
				return
			default:
			}
		}
		if stateIdx > 0 && stateIdx%4096 == 0 {
			ctx.maybeGCForLargeLALR()
		}
		ctx.forEachLALRDot0Prod(stateIdx, func(pi int) {
			prod := &ng.Productions[pi]
			lhs := prod.LHS
			rhs := prod.RHS
			srcIdx, ok := ntTransitionIndexLookup(ntTransRows, stateIdx, lhs)
			if !ok {
				return
			}
			curState := stateIdx
			valid := true
			for _, sym := range rhs {
				if next, ok := ctx.transitionTarget(curState, sym); ok {
					curState = next
				} else {
					valid = false
					break
				}
			}
			if !valid {
				return
			}
			if !ctx.hasLALRCompletedProd(curState, pi) {
				return
			}
			laByProd := reduceLookaheads[curState]
			if laByProd == nil {
				laByProd = make(map[int]bitset)
				reduceLookaheads[curState] = laByProd
			}
			if existing, ok := laByProd[pi]; ok {
				existing.unionWith(&followSets[srcIdx])
				laByProd[pi] = existing
			} else {
				laByProd[pi] = ctx.cloneLookaheadBitset(&followSets[srcIdx])
			}
			if trackLookaheadContributors {
				followSets[srcIdx].forEach(func(la int) {
					ctx.recordLookaheadContributor(curState, la, srcIdx)
				})
			}
			lookbackCount++
		})
		if debugLALR && stateIdx > 0 && stateIdx%256 == 0 {
			debugLALRProgress("lalr_lookbacks_progress",
				"state=%d states=%d lookbacks=%d",
				stateIdx, stateCount, lookbackCount)
		}
	}
	if debugLALR {
		debugLALRProgress("lalr_lookbacks", "lookbacks=%d", lookbackCount)
	}
	ntTransRows = nil

	// Step 8: Handle augmented start production: S' → S has lookahead {$end}.
	// The augmented production reduces in the state reached after reading S.
	augProd := &ng.Productions[ng.AugmentProdID]
	if len(augProd.RHS) > 0 {
		// Find the state reached from state 0 via the start symbol.
		if targetState, ok := ctx.transitionTarget(0, augProd.RHS[0]); ok {
			if ctx.hasLALRCompletedProd(targetState, ng.AugmentProdID) {
				laByProd := reduceLookaheads[targetState]
				if laByProd == nil {
					laByProd = make(map[int]bitset)
					reduceLookaheads[targetState] = laByProd
				}
				if existing, ok := laByProd[ng.AugmentProdID]; ok {
					existing.add(0)
					laByProd[ng.AugmentProdID] = existing
				} else {
					la := ctx.allocLookaheadBitset()
					la.add(0)
					laByProd[ng.AugmentProdID] = la
				}
			}
		}
	}
	ctx.lalrDot0Rows = packedAdjacencyRows{}
	ctx.lalrCompletedRows = packedAdjacencyRows{}
	ctx.maybeGCForLargeLALR()

	if !ng.EnableLRSplitting {
		followSets = nil
		ctx.maybeGCForLargeLALR()
	}
	if ctx.useCompactLALRTableBuild {
		ctx.lalrReduceLookaheads = reduceLookaheads
		if debugLALR {
			debugLALRProgress("lalr_compact_ready", "states=%d", len(reduceLookaheads))
		}
		return
	}
	ctx.materializeLALRItemSets(reduceLookaheads)
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

func (ctx *lrContext) builtStateCount() int {
	if ctx == nil {
		return 0
	}
	if len(ctx.itemSets) > 0 {
		return len(ctx.itemSets)
	}
	if len(ctx.lalrReduceLookaheads) > 0 {
		return len(ctx.lalrReduceLookaheads)
	}
	if n := ctx.lalrStateCount(); n > 0 {
		return n
	}
	if len(ctx.transitions) > 0 {
		return len(ctx.transitions)
	}
	return 0
}

func (ctx *lrContext) builtItemSet(stateIdx int) (lrItemSet, bool) {
	if ctx == nil || stateIdx < 0 {
		return lrItemSet{}, false
	}
	if stateIdx < len(ctx.itemSets) {
		return ctx.itemSets[stateIdx], true
	}
	if !ctx.useCompactLALRTableBuild || stateIdx >= ctx.builtStateCount() {
		return lrItemSet{}, false
	}
	return ctx.transientLALRItemSet(stateIdx)
}

func (ctx *lrContext) releaseBuiltItemSet(stateIdx int) {
	if ctx == nil || !ctx.useCompactLALRTableBuild || stateIdx < 0 || stateIdx >= len(ctx.lalrReduceLookaheads) {
		return
	}
	ctx.lalrReduceLookaheads[stateIdx] = nil
	if ctx.transientLALRCoreScratchN > 0 {
		clear(ctx.transientLALRCoreScratch[:ctx.transientLALRCoreScratchN])
		ctx.transientLALRCoreScratch = ctx.transientLALRCoreScratch[:0]
		ctx.transientLALRCoreScratchN = 0
	}
	if stateIdx > 0 && stateIdx%2048 == 0 {
		ctx.maybeGCForLargeLALR()
	}
}

func (ctx *lrContext) transientLALRItemSet(stateIdx int) (lrItemSet, bool) {
	if ctx == nil || stateIdx < 0 || stateIdx >= ctx.lalrStateCount() {
		return lrItemSet{}, false
	}
	if len(ctx.lalrKernelRows.offsets) > 0 {
		kernelScratch := make([]coreItem, 0, 64)
		ctx.lalrKernelRows.forEachRow(stateIdx, func(raw int) {
			ce := lr0CoreEntry(uint32(raw))
			kernelScratch = append(kernelScratch, coreItem{
				prodIdx: int(ce.prodIdx()),
				dot:     int(ce.dot()),
			})
		})
		reconstructed := ctx.lr0Closure(kernelScratch)
		set := ctx.transientLALRItemSetFromCoreIterator(
			stateIdx,
			reconstructed.coreLen(),
			reconstructed.coreAt,
			reconstructed.coreHash,
		)
		ctx.lr0ClosureScratch = reconstructed.cores[:0]
		return set, true
	}
	if stateIdx >= len(ctx.lalrLR0ItemSets) {
		return lrItemSet{}, false
	}
	lr0Set := &ctx.lalrLR0ItemSets[stateIdx]
	return ctx.transientLALRItemSetFromCoreIterator(
		stateIdx,
		lr0Set.coreLen(),
		lr0Set.coreAt,
		lr0Set.coreHash,
	), true
}

func (ctx *lrContext) transientLALRItemSetFromCoreIterator(
	stateIdx int,
	coreLen int,
	coreAt func(int) lr0CoreEntry,
	coreHash uint64,
) lrItemSet {
	var cores []coreEntry
	if cap(ctx.transientLALRCoreScratch) >= coreLen {
		cores = ctx.transientLALRCoreScratch[:coreLen]
	} else {
		cores = make([]coreEntry, coreLen)
		ctx.transientLALRCoreScratch = cores
	}
	ctx.transientLALRCoreScratch = cores
	ctx.transientLALRCoreScratchN = coreLen
	var laByProd map[int]bitset
	if stateIdx < len(ctx.lalrReduceLookaheads) {
		laByProd = ctx.lalrReduceLookaheads[stateIdx]
	}
	annotationArgTag := uint32(0)
	if stateIdx < len(ctx.lalrLR0ItemSets) {
		coreHash = ctx.lalrLR0ItemSets[stateIdx].coreHash
		annotationArgTag = ctx.lalrLR0ItemSets[stateIdx].annotationArgTag
	}
	for ci := range cores {
		ce := coreAt(ci)
		cores[ci] = coreEntry{
			prodIdx: ce.prodIdx(),
			dot:     uint32(ce.dot()),
		}
		if laByProd != nil && int(ce.dot()) == len(ctx.ng.Productions[ce.prodIdx()].RHS) {
			if la, ok := laByProd[int(ce.prodIdx())]; ok {
				cores[ci].lookaheads = la
			}
		}
	}
	set := lrItemSet{
		cores:            cores,
		coreHash:         coreHash,
		fullHash:         coreHash,
		completionLAHash: coreHash,
		boundaryLAHash:   coreHash,
		annotationArgTag: annotationArgTag,
	}
	ctx.pruneConditionalTypeQmarkLookaheadsInSet(&set)
	return set
}

func (ctx *lrContext) indexLALRNonterminalTransitions(tokenCount int, debugLALR bool) ([]uint32, []ntTransitionIndexRow) {
	var ntTargets []uint32
	ntTransRows := make([]ntTransitionIndexRow, len(ctx.transitions))
	for state, trans := range ctx.transitions {
		for _, edge := range trans {
			sym := int(edge.sym)
			if sym < tokenCount {
				continue
			}
			idx := len(ntTargets)
			ntTargets = append(ntTargets, edge.target)
			ntTransRows[state] = append(ntTransRows[state], ntTransitionIndex{
				nonterm: edge.sym,
				idx:     uint32(idx),
			})
		}
	}
	if debugLALR {
		debugLALRProgress("lalr_nt_transitions",
			"num_trans=%d transition_edges=%d",
			len(ntTargets), countTransitionEdges(ctx.transitions))
	}
	return ntTargets, ntTransRows
}

func (ctx *lrContext) computeLALRReadSets(
	ng *NormalizedGrammar,
	tokenCount int,
	ntTargets []uint32,
	ntTransRows []ntTransitionIndexRow,
	debugLALR bool,
) []bitset {
	numTrans := len(ntTargets)

	// DR(p, A) = { t ∈ Terminals | GOTO(p, A) has a shift on t }.
	dr := make([]bitset, numTrans)
	for i, target := range ntTargets {
		dr[i] = newBitset(tokenCount)
		q := int(target)
		for _, edge := range ctx.transitionRow(q) {
			sym := int(edge.sym)
			if sym < tokenCount {
				dr[i].add(sym)
			}
		}
	}

	// Seed $end into DR(0, start_symbol). The accept state (GOTO(0, start_symbol))
	// doesn't have a transition on $end, but $end is conceptually "readable" there
	// since the augmented production S' → S reduces on $end.
	startSym := ng.Productions[ng.AugmentProdID].RHS[0]
	if idx, ok := ntTransitionIndexLookup(ntTransRows, 0, startSym); ok {
		dr[idx].add(0)
	}

	readCounts := make([]uint32, numTrans)
	readEdgeCount := 0
	nullableScratch := make([]int, 0, 16)
	for i, target := range ntTargets {
		q := int(target)
		nullableScratch = ctx.collectNullableTransitionNonterms(q, tokenCount, nullableScratch)
		for _, sym := range nullableScratch {
			if _, ok := ntTransitionIndexLookup(ntTransRows, q, sym); ok {
				readCounts[i]++
				readEdgeCount++
			}
		}
	}

	reads := adjacencyRows{}
	if readEdgeCount > 0 {
		reads = newAdjacencyRows(readCounts)
		readWriteOffsets := append([]uint32(nil), reads.offsets[:numTrans]...)
		for i, target := range ntTargets {
			q := int(target)
			nullableScratch = ctx.collectNullableTransitionNonterms(q, tokenCount, nullableScratch)
			for _, sym := range nullableScratch {
				if j, ok := ntTransitionIndexLookup(ntTransRows, q, sym); ok {
					writeIdx := readWriteOffsets[i]
					reads.edges[writeIdx] = uint32(j)
					readWriteOffsets[i] = writeIdx + 1
				}
			}
		}
	}

	readSets := dr
	if readEdgeCount > 0 {
		readSets = digraphInPlace(numTrans, dr, reads)
	}
	if debugLALR {
		debugLALRProgress("lalr_reads",
			"num_trans=%d read_edges=%d",
			numTrans, readEdgeCount)
	}
	return readSets
}

func (ctx *lrContext) collectNullableTransitionNonterms(state, tokenCount int, scratch []int) []int {
	scratch = scratch[:0]
	for _, edge := range ctx.transitionRow(state) {
		sym := int(edge.sym)
		if sym >= tokenCount && ctx.nullables[sym] {
			scratch = append(scratch, sym)
		}
	}
	sort.Ints(scratch)
	return scratch
}

func (ctx *lrContext) buildLALRIncludeGraph(
	ng *NormalizedGrammar,
	tokenCount int,
	ntTransRows []ntTransitionIndexRow,
	numTrans int,
	debugLALR bool,
) (lalrIncludeGraph, bool) {
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

	includeCounts := make([]uint32, numTrans)
	includeEdgeCount, canceled := ctx.countLALRIncludeEdges(
		ng,
		tokenCount,
		ntTransRows,
		includePositionsByProd,
		includeCounts,
		debugLALR,
		"lalr_includes_progress",
	)
	if canceled {
		return lalrIncludeGraph{}, true
	}

	graph := lalrIncludeGraph{
		usePacked: numTrans <= 0x00FFFFFF,
		edgeCount: includeEdgeCount,
	}
	if includeEdgeCount == 0 {
		return graph, false
	}

	if graph.usePacked {
		graph.packed = newPackedAdjacencyRows(includeCounts)
		includeWriteOffsets := append([]uint32(nil), graph.packed.offsets[:numTrans]...)
		if ctx.fillLALRIncludeEdgesPacked(
			ng,
			tokenCount,
			ntTransRows,
			includePositionsByProd,
			includeWriteOffsets,
			graph.packed,
		) {
			return lalrIncludeGraph{}, true
		}
		return graph, false
	}

	graph.rows = newAdjacencyRows(includeCounts)
	includeWriteOffsets := append([]uint32(nil), graph.rows.offsets[:numTrans]...)
	if ctx.fillLALRIncludeEdges(
		ng,
		tokenCount,
		ntTransRows,
		includePositionsByProd,
		includeWriteOffsets,
		graph.rows.edges,
	) {
		return lalrIncludeGraph{}, true
	}
	return graph, false
}

func (ctx *lrContext) countLALRIncludeEdges(
	ng *NormalizedGrammar,
	tokenCount int,
	ntTransRows []ntTransitionIndexRow,
	includePositionsByProd [][]int,
	counts []uint32,
	debugLALR bool,
	progressStage string,
) (int, bool) {
	return ctx.walkLALRIncludeEdges(
		ng,
		tokenCount,
		ntTransRows,
		includePositionsByProd,
		len(counts),
		debugLALR,
		progressStage,
		func(tgtIdx int, _ uint32) {
			counts[tgtIdx]++
		},
	)
}

func (ctx *lrContext) fillLALRIncludeEdges(
	ng *NormalizedGrammar,
	tokenCount int,
	ntTransRows []ntTransitionIndexRow,
	includePositionsByProd [][]int,
	writeOffsets []uint32,
	edges []uint32,
) bool {
	_, canceled := ctx.walkLALRIncludeEdges(
		ng,
		tokenCount,
		ntTransRows,
		includePositionsByProd,
		len(writeOffsets),
		false,
		"",
		func(tgtIdx int, srcIdx uint32) {
			writeIdx := writeOffsets[tgtIdx]
			edges[writeIdx] = srcIdx
			writeOffsets[tgtIdx] = writeIdx + 1
		},
	)
	return canceled
}

func (ctx *lrContext) fillLALRIncludeEdgesPacked(
	ng *NormalizedGrammar,
	tokenCount int,
	ntTransRows []ntTransitionIndexRow,
	includePositionsByProd [][]int,
	writeOffsets []uint32,
	rows packedAdjacencyRows,
) bool {
	_, canceled := ctx.walkLALRIncludeEdges(
		ng,
		tokenCount,
		ntTransRows,
		includePositionsByProd,
		len(writeOffsets),
		false,
		"",
		func(tgtIdx int, srcIdx uint32) {
			writeIdx := writeOffsets[tgtIdx]
			rows.set(writeIdx, srcIdx)
			writeOffsets[tgtIdx] = writeIdx + 1
		},
	)
	return canceled
}

func (ctx *lrContext) walkLALRIncludeEdges(
	ng *NormalizedGrammar,
	tokenCount int,
	ntTransRows []ntTransitionIndexRow,
	includePositionsByProd [][]int,
	numTrans int,
	debugLALR bool,
	progressStage string,
	visit func(tgtIdx int, srcIdx uint32),
) (int, bool) {
	if numTrans == 0 {
		return 0, false
	}
	includeSeenEpoch := make([]uint32, numTrans)
	includeLastSrcIdx := make([]uint32, numTrans)
	includeStateEpoch := uint32(0)
	includeEdgeCount := 0
	stateCount := ctx.lalrStateCount()
	for stateIdx := 0; stateIdx < stateCount; stateIdx++ {
		if ctx.bgCtx != nil && stateIdx&63 == 0 {
			select {
			case <-ctx.bgCtx.Done():
				return includeEdgeCount, true
			default:
			}
		}
		if stateIdx > 0 && stateIdx%4096 == 0 {
			ctx.maybeGCForLargeLALR()
		}
		includeStateEpoch++
		if includeStateEpoch == 0 {
			for i := range includeSeenEpoch {
				includeSeenEpoch[i] = 0
			}
			includeStateEpoch = 1
		}
		ctx.forEachLALRDot0Prod(stateIdx, func(pi int) {
			prod := &ng.Productions[pi]
			lhs := prod.LHS
			rhs := prod.RHS
			curState := stateIdx
			valid := true
			includePos := includePositionsByProd[pi]
			nextIncludeIdx := 0
			for dot, sym := range rhs {
				if nextIncludeIdx < len(includePos) && includePos[nextIncludeIdx] == dot {
					if srcIdx, ok := ntTransitionIndexLookup(ntTransRows, stateIdx, lhs); ok {
						if tgtIdx, ok := ntTransitionIndexLookup(ntTransRows, curState, sym); ok {
							srcIdx32 := uint32(srcIdx)
							if includeSeenEpoch[tgtIdx] != includeStateEpoch || includeLastSrcIdx[tgtIdx] != srcIdx32 {
								visit(tgtIdx, srcIdx32)
								includeEdgeCount++
								includeSeenEpoch[tgtIdx] = includeStateEpoch
								includeLastSrcIdx[tgtIdx] = srcIdx32
							}
						}
					}
					nextIncludeIdx++
				}
				if next, ok := ctx.transitionTarget(curState, sym); ok {
					curState = next
				} else {
					valid = false
					break
				}
			}
			if !valid {
				return
			}
		})
		if debugLALR && progressStage != "" && stateIdx > 0 && stateIdx%256 == 0 {
			debugLALRProgress(progressStage,
				"state=%d states=%d includes_edges=%d",
				stateIdx, stateCount, includeEdgeCount)
		}
	}
	return includeEdgeCount, false
}

func (ctx *lrContext) materializeLALRItemSets(reduceLookaheads []map[int]bitset) {
	debugLALR := os.Getenv("GOT_DEBUG_LALR") == "1"
	if ctx.lalrLR0Released {
		if len(ctx.lalrKernelRows.offsets) > 0 {
			stateCount := ctx.lalrStateCount()
			ctx.itemSets = make([]lrItemSet, stateCount)
			kernelScratch := make([]coreItem, 0, 64)
			for stateIdx := 0; stateIdx < stateCount; stateIdx++ {
				if ctx.bgCtx != nil && stateIdx&63 == 0 {
					select {
					case <-ctx.bgCtx.Done():
						ctx.itemSets = nil
						return
					default:
					}
				}
				if stateIdx > 0 && stateIdx%4096 == 0 {
					ctx.maybeGCForLargeLALR()
				}
				if debugLALR && stateIdx > 0 && stateIdx%256 == 0 {
					debugLALRProgress("lalr_materialize_progress",
						"state=%d states=%d",
						stateIdx, stateCount)
				}
				kernelScratch = kernelScratch[:0]
				ctx.lalrKernelRows.forEachRow(stateIdx, func(raw int) {
					ce := lr0CoreEntry(uint32(raw))
					kernelScratch = append(kernelScratch, coreItem{
						prodIdx: int(ce.prodIdx()),
						dot:     int(ce.dot()),
					})
				})
				reconstructed := ctx.lr0Closure(kernelScratch)
				cores := make([]coreEntry, reconstructed.coreLen())
				laByProd := reduceLookaheads[stateIdx]
				for ci := range cores {
					ce := reconstructed.coreAt(ci)
					cores[ci] = coreEntry{
						prodIdx: ce.prodIdx(),
						dot:     uint32(ce.dot()),
					}
					if laByProd != nil && int(ce.dot()) == len(ctx.ng.Productions[ce.prodIdx()].RHS) {
						if la, ok := laByProd[int(ce.prodIdx())]; ok {
							cores[ci].lookaheads = la
						}
					}
				}
				coreHash := reconstructed.coreHash
				annotationArgTag := uint32(0)
				if stateIdx < len(ctx.lalrLR0ItemSets) {
					coreHash = ctx.lalrLR0ItemSets[stateIdx].coreHash
					annotationArgTag = ctx.lalrLR0ItemSets[stateIdx].annotationArgTag
				}
				ctx.itemSets[stateIdx] = lrItemSet{
					cores:            cores,
					coreHash:         coreHash,
					fullHash:         coreHash,
					completionLAHash: coreHash,
					boundaryLAHash:   coreHash,
					annotationArgTag: annotationArgTag,
				}
				reduceLookaheads[stateIdx] = nil
				ctx.lr0ClosureScratch = reconstructed.cores[:0]
			}
			ctx.lalrKernelRows = packedAdjacencyRows{}
			ctx.lalrDot0Rows = packedAdjacencyRows{}
			ctx.lalrCompletedRows = packedAdjacencyRows{}
			ctx.lalrLR0Released = false
			ctx.lalrLR0ItemSets = nil
			if debugLALR {
				debugLALRProgress("lalr_materialize", "states=%d", stateCount)
			}
			return
		}
		ctx.lalrDot0Rows = packedAdjacencyRows{}
		ctx.lalrCompletedRows = packedAdjacencyRows{}
		ctx.lalrLR0Released = false
		ctx.buildLR0()
		if ctx.lalrLR0StateBudgetExceeded || ctx.lalrLR0CoreBudgetExceeded || len(ctx.lalrLR0ItemSets) == 0 {
			ctx.itemSets = nil
			return
		}
		ctx.packRetainedLR0ItemSets()
		ctx.maybeGCForLargeLALR()
	}
	ctx.itemSets = make([]lrItemSet, len(ctx.lalrLR0ItemSets))
	for i := range ctx.lalrLR0ItemSets {
		lr0Set := &ctx.lalrLR0ItemSets[i]
		if debugLALR && i > 0 && i%256 == 0 {
			debugLALRProgress("lalr_materialize_progress",
				"state=%d states=%d",
				i, len(ctx.lalrLR0ItemSets))
		}
		cores := make([]coreEntry, lr0Set.coreLen())
		laByProd := reduceLookaheads[i]
		for ci := range cores {
			ce := lr0Set.coreAt(ci)
			cores[ci] = coreEntry{
				prodIdx: ce.prodIdx(),
				dot:     uint32(ce.dot()),
			}
			if laByProd != nil && int(ce.dot()) == len(ctx.ng.Productions[ce.prodIdx()].RHS) {
				if la, ok := laByProd[int(ce.prodIdx())]; ok {
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
		reduceLookaheads[i] = nil
		lr0Set.cores = nil
		lr0Set.packedCores = nil
		lr0Set.coreCount = 0
	}
	ctx.lalrKernelRows = packedAdjacencyRows{}
	ctx.lalrDot0Rows = packedAdjacencyRows{}
	ctx.lalrCompletedRows = packedAdjacencyRows{}
	ctx.lalrLR0Released = false
	ctx.lalrLR0ItemSets = nil
	if debugLALR {
		debugLALRProgress("lalr_materialize", "states=%d", len(ctx.itemSets))
	}
}

func (ctx *lrContext) pruneConditionalTypeQmarkLookaheads() {
	if ctx == nil {
		return
	}
	for i := range ctx.itemSets {
		ctx.pruneConditionalTypeQmarkLookaheadsInSet(&ctx.itemSets[i])
	}
}

func (ctx *lrContext) pruneConditionalTypeQmarkLookaheadsInSet(set *lrItemSet) {
	if ctx == nil || set == nil || ctx.conditionalTypeExternalQmarkSym < 0 || ctx.conditionalTypePlainQmarkSym < 0 {
		return
	}
	if set.annotationArgTag&conditionalTypeContextTag == 0 {
		return
	}
	for ci := range set.cores {
		ce := &set.cores[ci]
		if !ce.lookaheads.contains(ctx.conditionalTypeExternalQmarkSym) || !ce.lookaheads.contains(ctx.conditionalTypePlainQmarkSym) {
			continue
		}
		ce.lookaheads.clear(ctx.conditionalTypeExternalQmarkSym)
	}
}

// digraphInPlace implements Tarjan's SCC-based algorithm for computing F(x) across a
// relation R, given initial values f(x):
//
//	F(x) = f(x) ∪ ∪{ F(y) | x R y }
//
// This is the core algorithm from DeRemer & Pennello (1982). It visits each
// node at most twice (push + pop), making it near-linear.
//
// n: number of nodes
// values: initial values f(0..n-1), each a bitset
// rel: compact adjacency rows for relation R: rel[x] = list of y such that x R y
//
// values is updated in place and returned to avoid cloning every bitset before
// the SCC walk. Callers must not reuse the pre-digraph contents afterwards.
func digraphInPlace(n int, values []bitset, rel adjacencyRows) []bitset {
	result := values

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

		for _, y32 := range rel.row(x) {
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

func digraphPackedInPlace(n int, values []bitset, rel packedAdjacencyRows) []bitset {
	result := values

	const infinity = 0x7FFFFFFF
	depth := make([]int, n)
	stack := make([]int, 0, n)
	d := 0

	var traverse func(x int)
	traverse = func(x int) {
		d++
		depth[x] = d
		stack = append(stack, x)

		rel.forEachRow(x, func(y int) {
			if depth[y] == 0 {
				traverse(y)
			}
			if depth[y] < depth[x] {
				depth[x] = depth[y]
			}
			result[x].unionWith(&result[y])
		})

		if depth[x] == d {
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				depth[top] = infinity
				if top == x {
					break
				}
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

func countTransitionEdges(transitions []lrTransitionRow) int {
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
