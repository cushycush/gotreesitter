package gotreesitter

import "testing"

func TestParserScratchMemoryBudgetExhaustedByEntrySlabGrowth(t *testing.T) {
	var scratch parserScratch
	scratch.entries.ensureInitialCap(defaultStackEntrySlabCap)
	initial := scratch.allocatedBytes()
	scratch.setBudget(initial + 1)

	_ = scratch.entries.allocWithCap(defaultStackEntrySlabCap, defaultStackEntrySlabCap)
	if scratch.budgetExhausted() {
		t.Fatal("budget exhausted before entry-slab overflow")
	}

	_ = scratch.entries.alloc(1)
	if !scratch.budgetExhausted() {
		t.Fatal("budget not exhausted after entry-slab overflow")
	}
}

func TestParserScratchMemoryBudgetExhaustedByGSSSlabGrowth(t *testing.T) {
	var scratch parserScratch
	_ = scratch.gss.allocNode(stackEntry{state: 1}, nil, 1)
	initial := scratch.allocatedBytes()
	scratch.setBudget(initial + 1)

	for depth := 2; depth <= defaultGSSNodeSlabCap; depth++ {
		_ = scratch.gss.allocNode(stackEntry{state: 1}, nil, depth)
	}
	if scratch.budgetExhausted() {
		t.Fatal("budget exhausted before gss-slab overflow")
	}

	_ = scratch.gss.allocNode(stackEntry{state: 1}, nil, defaultGSSNodeSlabCap+1)
	if !scratch.budgetExhausted() {
		t.Fatal("budget not exhausted after gss-slab overflow")
	}
}

func TestParserScratchResetRecomputesAllocatedBytes(t *testing.T) {
	var scratch parserScratch
	scratch.entries.ensureInitialCap(defaultStackEntrySlabCap)
	_ = scratch.entries.allocWithCap(defaultStackEntrySlabCap, defaultStackEntrySlabCap)
	_ = scratch.entries.alloc(1)
	for depth := 1; depth <= defaultGSSNodeSlabCap+1; depth++ {
		_ = scratch.gss.allocNode(stackEntry{state: 1}, nil, depth)
	}

	if scratch.allocatedBytes() <= 0 {
		t.Fatal("allocatedBytes should be positive before reset")
	}

	scratch.entries.reset()
	scratch.gss.reset()

	want := scratch.entries.allocatedBytes + scratch.gss.allocatedBytes
	if got := scratch.allocatedBytes(); got != want {
		t.Fatalf("allocatedBytes after reset = %d, want %d", got, want)
	}
}
