package gotreesitter

import "testing"

func TestArenaMemoryBudgetExhaustedByNodeSlabGrowth(t *testing.T) {
	arena := newNodeArena(arenaClassIncremental)
	initial := arena.allocatedBytes
	arena.setBudget(initial + 1)

	for i := 0; i < len(arena.nodes); i++ {
		_ = arena.allocNode()
	}
	if arena.budgetExhausted() {
		t.Fatal("budget exhausted before overflow node slab allocation")
	}

	_ = arena.allocNode()
	if !arena.budgetExhausted() {
		t.Fatal("budget not exhausted after overflow node slab allocation")
	}
}

func TestArenaMemoryBudgetExhaustedByChildSlabGrowth(t *testing.T) {
	arena := newNodeArena(arenaClassFull)
	initial := arena.allocatedBytes
	base := defaultChildSliceCap(arena.class)
	arena.setBudget(initial + 1)

	_ = arena.allocNodeSlice(base)
	if arena.budgetExhausted() {
		t.Fatal("budget exhausted before child slab overflow")
	}

	_ = arena.allocNodeSlice(base)
	if !arena.budgetExhausted() {
		t.Fatal("budget not exhausted after child slab overflow")
	}
}

func TestArenaMemoryBudgetExhaustedByFieldSlabGrowth(t *testing.T) {
	arena := newNodeArena(arenaClassFull)
	initial := arena.allocatedBytes
	base := defaultFieldSliceCap(arena.class)
	arena.setBudget(initial + 1)

	_ = arena.allocFieldIDSlice(base)
	if arena.budgetExhausted() {
		t.Fatal("budget exhausted before field slab overflow")
	}

	_ = arena.allocFieldIDSlice(base)
	if !arena.budgetExhausted() {
		t.Fatal("budget not exhausted after field slab overflow")
	}
}

func TestArenaMemoryBudgetExhaustedByFieldSourceSlabGrowth(t *testing.T) {
	arena := newNodeArena(arenaClassFull)
	initial := arena.allocatedBytes
	base := defaultFieldSliceCap(arena.class)
	arena.setBudget(initial + 1)

	_ = arena.allocFieldSourceSlice(base)
	if arena.budgetExhausted() {
		t.Fatal("budget exhausted before field-source slab overflow")
	}

	_ = arena.allocFieldSourceSlice(base)
	if !arena.budgetExhausted() {
		t.Fatal("budget not exhausted after field-source slab overflow")
	}
}

func TestArenaResetRecomputesAllocatedBytes(t *testing.T) {
	arena := newNodeArena(arenaClassFull)
	baseChild := defaultChildSliceCap(arena.class)
	baseField := defaultFieldSliceCap(arena.class)

	for i := 0; i < len(arena.nodes)+1; i++ {
		_ = arena.allocNode()
	}
	_ = arena.allocNodeSlice(baseChild)
	_ = arena.allocNodeSlice(baseChild)
	_ = arena.allocFieldIDSlice(baseField)
	_ = arena.allocFieldIDSlice(baseField)
	_ = arena.allocFieldSourceSlice(baseField)
	_ = arena.allocFieldSourceSlice(baseField)

	if arena.allocatedBytes <= 0 {
		t.Fatal("allocatedBytes should be positive before reset")
	}

	arena.reset()

	var want int64
	want += nodeBytesForCap(len(arena.nodes))
	for _, slab := range arena.nodeSlabs {
		want += nodeBytesForCap(len(slab.data))
	}
	for _, slab := range arena.childSlabs {
		want += childSliceBytesForCap(len(slab.data))
	}
	for _, slab := range arena.fieldSlabs {
		want += fieldSliceBytesForCap(len(slab.data))
	}
	for _, slab := range arena.fieldSourceSlabs {
		want += fieldSourceSliceBytesForCap(len(slab.data))
	}
	if arena.allocatedBytes != want {
		t.Fatalf("allocatedBytes after reset = %d, want %d", arena.allocatedBytes, want)
	}
}
