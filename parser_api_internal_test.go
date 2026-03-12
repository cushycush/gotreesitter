package gotreesitter

import "testing"

func TestRepetitionShiftConflictChoice(t *testing.T) {
	chosen, ok := repetitionShiftConflictChoice([]ParseAction{
		{Type: ParseActionReduce, Symbol: 191, ChildCount: 2},
		{Type: ParseActionShift, State: 1245, Repetition: true},
	})
	if !ok {
		t.Fatal("repetitionShiftConflictChoice = false, want true")
	}
	if chosen.Type != ParseActionShift || chosen.State != 1245 || !chosen.Repetition {
		t.Fatalf("repetitionShiftConflictChoice picked %+v, want repetition shift", chosen)
	}
}

func TestRepetitionShiftConflictChoiceRejectsNonRepetitionShift(t *testing.T) {
	if _, ok := repetitionShiftConflictChoice([]ParseAction{
		{Type: ParseActionReduce, Symbol: 191, ChildCount: 2},
		{Type: ParseActionShift, State: 1245, Repetition: false},
	}); ok {
		t.Fatal("repetitionShiftConflictChoice = true, want false")
	}
}

func TestShouldRetryNodeLimitParse(t *testing.T) {
	tree := &Tree{
		parseRuntime: ParseRuntime{
			StopReason:     ParseStopNodeLimit,
			NodeLimit:      300_000,
			NodesAllocated: 300_001,
		},
	}

	if !shouldRetryNodeLimitParse(tree, 4096) {
		t.Fatal("shouldRetryNodeLimitParse = false, want true")
	}
}

func TestShouldNotRetryNodeLimitParseForLargeSource(t *testing.T) {
	tree := &Tree{
		parseRuntime: ParseRuntime{
			StopReason:     ParseStopNodeLimit,
			NodeLimit:      300_000,
			NodesAllocated: 300_001,
		},
	}

	if shouldRetryNodeLimitParse(tree, fullParseRetryMaxSourceBytes+1) {
		t.Fatal("shouldRetryNodeLimitParse = true, want false")
	}
}

func TestShouldNotRetryMemoryBudgetParse(t *testing.T) {
	tree := &Tree{
		parseRuntime: ParseRuntime{
			StopReason: ParseStopMemoryBudget,
		},
	}

	if shouldRetryNodeLimitParse(tree, 4096) {
		t.Fatal("shouldRetryNodeLimitParse = true, want false for memory budget stop")
	}
}

func TestFullParseRetryNodeLimitOverride(t *testing.T) {
	tree := &Tree{
		parseRuntime: ParseRuntime{
			StopReason:     ParseStopNodeLimit,
			NodeLimit:      300_000,
			NodesAllocated: 300_001,
		},
	}

	got := fullParseRetryNodeLimitOverride(tree, 4096)
	want := 600_000
	if got != want {
		t.Fatalf("fullParseRetryNodeLimitOverride = %d, want %d", got, want)
	}
}

func TestFullParseRetrySecondaryNodeLimitOverride(t *testing.T) {
	tree := &Tree{
		parseRuntime: ParseRuntime{
			StopReason:     ParseStopNodeLimit,
			NodeLimit:      600_000,
			NodesAllocated: 600_001,
		},
	}

	got := fullParseRetrySecondaryNodeLimitOverride(tree, 4096)
	want := 1_800_000
	if got != want {
		t.Fatalf("fullParseRetrySecondaryNodeLimitOverride = %d, want %d", got, want)
	}
}

func TestPreferRetryTreePrefersFurtherAcceptedProgress(t *testing.T) {
	incumbent := &Tree{
		root: &Node{
			endByte:  100,
			hasError: true,
			children: []*Node{{}, {}, {}},
		},
		parseRuntime: ParseRuntime{
			StopReason:      ParseStopNoStacksAlive,
			ExpectedEOFByte: 200,
			Truncated:       true,
		},
	}
	candidate := &Tree{
		root: &Node{
			endByte:  200,
			hasError: true,
			children: []*Node{{}, {}},
		},
		parseRuntime: ParseRuntime{
			StopReason:      ParseStopAccepted,
			ExpectedEOFByte: 200,
		},
	}

	if !preferRetryTree(candidate, incumbent) {
		t.Fatal("preferRetryTree = false, want true for accepted full-length retry")
	}
}

func TestPreferRetryTreePrefersFewerChildrenOnEqualErrorTrees(t *testing.T) {
	incumbent := &Tree{
		root: &Node{
			endByte:  200,
			hasError: true,
			children: make([]*Node, 12),
		},
		parseRuntime: ParseRuntime{
			StopReason:      ParseStopAccepted,
			ExpectedEOFByte: 200,
			NodesAllocated:  1200,
		},
	}
	candidate := &Tree{
		root: &Node{
			endByte:  200,
			hasError: true,
			children: make([]*Node, 4),
		},
		parseRuntime: ParseRuntime{
			StopReason:      ParseStopAccepted,
			ExpectedEOFByte: 200,
			NodesAllocated:  800,
		},
	}

	if !preferRetryTree(candidate, incumbent) {
		t.Fatal("preferRetryTree = false, want true for smaller equal-span error tree")
	}
}

func TestGLRStackCullTrigger(t *testing.T) {
	if got := glrStackCullTrigger(8, arenaClassFull, "go"); got != 12 {
		t.Fatalf("glrStackCullTrigger(full, go) = %d, want 12", got)
	}
	if got := glrStackCullTrigger(8, arenaClassFull, "c_sharp"); got != 8 {
		t.Fatalf("glrStackCullTrigger(full, c_sharp) = %d, want 8", got)
	}
	if got := glrStackCullTrigger(8, arenaClassIncremental, "go"); got != 8 {
		t.Fatalf("glrStackCullTrigger(incremental, go) = %d, want 8", got)
	}
	maxInt := int(^uint(0) >> 1)
	if got := glrStackCullTrigger(maxInt, arenaClassFull, "go"); got != maxInt {
		t.Fatalf("glrStackCullTrigger(maxInt) = %d, want %d", got, maxInt)
	}
}

func TestResolveParseMaxStacks(t *testing.T) {
	if got, retry := resolveParseMaxStacks(6, 0, 2); got != 6 || retry {
		t.Fatalf("resolveParseMaxStacks(default) = (%d, %t), want (6, false)", got, retry)
	}
	if got, retry := resolveParseMaxStacks(6, 2, 2); got != 2 || retry {
		t.Fatalf("resolveParseMaxStacks(low override) = (%d, %t), want (2, false)", got, retry)
	}
	if got, retry := resolveParseMaxStacks(6, 32, 2); got != 32 || !retry {
		t.Fatalf("resolveParseMaxStacks(retry widen) = (%d, %t), want (32, true)", got, retry)
	}
	if got, retry := resolveParseMaxStacks(6, 2, 4); got != 4 || retry {
		t.Fatalf("resolveParseMaxStacks(conflict floor) = (%d, %t), want (4, false)", got, retry)
	}
}

func TestEffectiveFullParseInitialMaxStacks(t *testing.T) {
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "bash"}, maxGLRStacks); got != 256 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(bash) = %d, want 256", got)
	}
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "css"}, maxGLRStacks); got != 2 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(css) = %d, want 2", got)
	}
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "scss"}, maxGLRStacks); got != 2 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(scss) = %d, want 2", got)
	}
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "javascript"}, maxGLRStacks); got != 6 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(javascript) = %d, want 6", got)
	}
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "typescript"}, maxGLRStacks); got != 6 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(typescript) = %d, want 6", got)
	}
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "tsx"}, maxGLRStacks); got != 6 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(tsx) = %d, want 6", got)
	}
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "rust"}, maxGLRStacks); got != 2 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(rust) = %d, want 2", got)
	}
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "css"}, 16); got != 16 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(css, explicit override) = %d, want 16", got)
	}
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "javascript"}, 16); got != 16 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(javascript, explicit override) = %d, want 16", got)
	}
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "typescript"}, 16); got != 16 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(typescript, explicit override) = %d, want 16", got)
	}
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "tsx"}, 16); got != 16 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(tsx, explicit override) = %d, want 16", got)
	}
	if got := effectiveFullParseInitialMaxStacks(&Language{Name: "rust"}, 16); got != 16 {
		t.Fatalf("effectiveFullParseInitialMaxStacks(rust, explicit override) = %d, want 16", got)
	}
}

func TestParseMaxMergePerKeyValue(t *testing.T) {
	t.Setenv("GOT_GLR_MAX_MERGE_PER_KEY", "3")
	ResetParseEnvConfigCacheForTests()
	defer ResetParseEnvConfigCacheForTests()

	if got := parseMaxMergePerKeyValue(); got != 3 {
		t.Fatalf("parseMaxMergePerKeyValue() = %d, want 3", got)
	}
}

func TestNoteRepeatedReduceChainSignatureDetectsCycle(t *testing.T) {
	sig := reduceChainSignature{
		state:        2016,
		depth:        171,
		symbol:       216,
		childCount:   1,
		productionID: 42,
	}
	var prev reduceChainSignature
	count := 0
	cycle := false
	for i := 0; i <= maxRepeatedReduceChainSignature; i++ {
		prev, count, cycle = noteRepeatedReduceChainSignature(prev, count, sig)
	}
	if !cycle {
		t.Fatal("noteRepeatedReduceChainSignature did not report a repeated cycle")
	}
	if prev != sig {
		t.Fatalf("noteRepeatedReduceChainSignature signature = %+v, want %+v", prev, sig)
	}
	if count != maxRepeatedReduceChainSignature+1 {
		t.Fatalf("noteRepeatedReduceChainSignature count = %d, want %d", count, maxRepeatedReduceChainSignature+1)
	}
}

func TestNoteRepeatedReduceChainSignatureResetsOnChange(t *testing.T) {
	first := reduceChainSignature{state: 10, depth: 3, symbol: 7, childCount: 1, productionID: 2}
	second := reduceChainSignature{state: 11, depth: 3, symbol: 7, childCount: 1, productionID: 2}

	prev, count, cycle := noteRepeatedReduceChainSignature(reduceChainSignature{}, 0, first)
	if cycle || count != 1 || prev != first {
		t.Fatalf("first signature = (%+v, %d, %t), want (%+v, 1, false)", prev, count, cycle, first)
	}

	prev, count, cycle = noteRepeatedReduceChainSignature(prev, count, second)
	if cycle {
		t.Fatal("changed signature incorrectly reported a cycle")
	}
	if count != 1 || prev != second {
		t.Fatalf("changed signature = (%+v, %d), want (%+v, 1)", prev, count, second)
	}
}
