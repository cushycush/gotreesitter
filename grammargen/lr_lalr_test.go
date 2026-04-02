package grammargen

import (
	"testing"
	"unsafe"
)

func cloneBitsetSlice(src []bitset) []bitset {
	dst := make([]bitset, len(src))
	for i := range src {
		dst[i] = src[i].clone()
	}
	return dst
}

func solveDigraphNaive(src []bitset, rel [][]uint32) []bitset {
	result := cloneBitsetSlice(src)
	changed := true
	for changed {
		changed = false
		for i := range result {
			for _, y := range rel[i] {
				if result[i].unionWith(&result[y]) {
					changed = true
				}
			}
		}
	}
	return result
}

func adjacencyRowsFromSlices(rel [][]uint32) adjacencyRows {
	counts := make([]uint32, len(rel))
	for i := range rel {
		counts[i] = uint32(len(rel[i]))
	}
	rows := newAdjacencyRows(counts)
	writeOffsets := append([]uint32(nil), rows.offsets[:len(rel)]...)
	for i := range rel {
		for _, edge := range rel[i] {
			writeIdx := writeOffsets[i]
			rows.edges[writeIdx] = edge
			writeOffsets[i] = writeIdx + 1
		}
	}
	return rows
}

func packedAdjacencyRowsFromSlices(rel [][]uint32, maxValue int) packedAdjacencyRows {
	counts := make([]uint32, len(rel))
	for i := range rel {
		counts[i] = uint32(len(rel[i]))
	}
	rows := newPackedAdjacencyRowsForMaxValue(counts, maxValue)
	writeOffsets := append([]uint32(nil), rows.offsets[:len(rel)]...)
	for i := range rel {
		for _, edge := range rel[i] {
			writeIdx := writeOffsets[i]
			rows.set(writeIdx, edge)
			writeOffsets[i] = writeIdx + 1
		}
	}
	return rows
}

func TestDigraphInPlaceMatchesNaiveClosure(t *testing.T) {
	values := make([]bitset, 4)
	for i := range values {
		values[i] = newBitset(8)
		values[i].add(i)
	}
	rel := [][]uint32{
		{1},
		{0, 2},
		{3},
		nil,
	}

	want := solveDigraphNaive(values, rel)
	got := digraphInPlace(len(values), cloneBitsetSlice(values), adjacencyRowsFromSlices(rel))
	if len(got) != len(want) {
		t.Fatalf("got %d result sets, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].equal(&want[i]) {
			t.Fatalf("set %d mismatch: got=%v want=%v", i, got[i].words, want[i].words)
		}
	}

	// The returned slice feeds the FOLLOW pass, so SCC peers must not alias the
	// same backing words after the digraph walk finishes.
	got[0].add(7)
	if got[1].contains(7) {
		t.Fatal("SCC result bitsets alias backing storage")
	}
}

func TestPackedAdjacencyRowsForEachRowMatchesEdgeAt(t *testing.T) {
	rel := [][]uint32{
		{7, 8, 15},
		nil,
		{1, 9, 15, 2},
	}
	rows := packedAdjacencyRowsFromSlices(rel, 15)
	for rowIdx := range rel {
		var got []int
		rows.forEachRow(rowIdx, func(v int) {
			got = append(got, v)
		})
		if len(got) != len(rel[rowIdx]) {
			t.Fatalf("row %d len=%d, want %d", rowIdx, len(got), len(rel[rowIdx]))
		}
		for j, want := range rel[rowIdx] {
			if got[j] != int(want) {
				t.Fatalf("row %d entry %d = %d, want %d", rowIdx, j, got[j], want)
			}
		}
	}
}

func TestLALRLookaheadsAfterEarlyLR0Release(t *testing.T) {
	ng, err := Normalize(JSONGrammar())
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	ctx := makeLRContext(ng)
	ctx.computeFirstSets()
	ctx.buildLR0()
	if ctx.lalrLR0StateBudgetExceeded || ctx.lalrLR0CoreBudgetExceeded {
		t.Fatal("unexpected LR0 budget failure")
	}
	if len(ctx.lalrLR0ItemSets) == 0 {
		t.Fatal("expected LR0 states")
	}

	// Force the early-release path on a small grammar so materialization from
	// packed kernel rows stays regression-tested.
	ctx.lalrLR0CoreEntries = 50_000_000
	ctx.prepareLALRStatesForLookaheads()
	if !ctx.lalrLR0Released {
		t.Fatal("expected LR0 states to be summarized and released")
	}
	if len(ctx.lalrKernelRows.offsets) == 0 {
		t.Fatal("expected packed kernel rows after early LR0 release")
	}

	ctx.computeLALRLookaheads()
	if len(ctx.itemSets) == 0 {
		t.Fatal("expected materialized LALR item sets after early LR0 release")
	}

	augProd := &ng.Productions[ng.AugmentProdID]
	hasAccept := false
	for _, set := range ctx.itemSets {
		for _, ce := range set.cores {
			if int(ce.prodIdx) == ng.AugmentProdID && int(ce.dot) == len(augProd.RHS) && ce.lookaheads.contains(0) {
				hasAccept = true
				break
			}
		}
		if hasAccept {
			break
		}
	}
	if !hasAccept {
		t.Fatal("expected accept lookahead after early LR0 release")
	}
}

func TestBuiltStateCountPrefersRetainedLALRStateInventory(t *testing.T) {
	ctx := &lrContext{
		transitions: make([]lrTransitionRow, 2),
		lalrLR0ItemSets: []lr0ItemSet{
			{},
			{},
			{},
		},
		lalrReduceLookaheads: []map[int]bitset{
			nil,
			nil,
			nil,
		},
	}

	if got := ctx.builtStateCount(); got != 3 {
		t.Fatalf("builtStateCount() = %d, want 3", got)
	}
}

func TestReleaseBuiltItemSetClearsTransientScratch(t *testing.T) {
	la := newBitset(8)
	la.add(3)
	ctx := &lrContext{
		useCompactLALRTableBuild: true,
		lalrReduceLookaheads:     make([]map[int]bitset, 1),
		transientLALRCoreScratch: []coreEntry{{
			prodIdx:    11,
			dot:        2,
			lookaheads: la,
		}},
		transientLALRCoreScratchN: 1,
	}

	scratch := ctx.transientLALRCoreScratch
	ctx.releaseBuiltItemSet(0)

	if len(ctx.transientLALRCoreScratch) != 0 {
		t.Fatalf("len(transientLALRCoreScratch) = %d, want 0", len(ctx.transientLALRCoreScratch))
	}
	if ctx.transientLALRCoreScratchN != 0 {
		t.Fatalf("transientLALRCoreScratchN = %d, want 0", ctx.transientLALRCoreScratchN)
	}
	if scratch[0].prodIdx != 0 || scratch[0].dot != 0 || len(scratch[0].lookaheads.words) != 0 {
		t.Fatalf("scratch entry not cleared: %+v", scratch[0])
	}
}

func TestLRActionPackedFootprint(t *testing.T) {
	if got := unsafe.Sizeof(lrAction{}); got != 48 {
		t.Fatalf("unsafe.Sizeof(lrAction{}) = %d, want 48", got)
	}
}

func TestAddActionInitializesMissingStateRow(t *testing.T) {
	tables := &LRTables{
		ActionTable: make(map[int]map[int][]lrAction),
		GotoTable:   make(map[int]map[int]int),
	}

	tables.addAction(7, 11, lrAction{kind: lrAccept})

	row := tables.ActionTable[7]
	if row == nil {
		t.Fatal("expected addAction to allocate the missing state row")
	}
	if got := row[11]; len(got) != 1 || got[0].kind != lrAccept {
		t.Fatalf("action row = %#v, want one accept action", got)
	}
}

func TestEnsureGotoRowAllocatesMissingStateRow(t *testing.T) {
	tables := &LRTables{
		ActionTable: make(map[int]map[int][]lrAction),
		GotoTable:   make(map[int]map[int]int),
	}

	row := tables.ensureGotoRow(9)
	row[3] = 17

	if tables.GotoTable[9] == nil {
		t.Fatal("expected ensureGotoRow to allocate the missing state row")
	}
	if got := tables.GotoTable[9][3]; got != 17 {
		t.Fatalf("GotoTable[9][3] = %d, want 17", got)
	}
}
