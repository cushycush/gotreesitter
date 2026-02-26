//go:build perf

package gotreesitter

import "sync/atomic"

const (
	perfCountersEnabled = true
	perfMergeHistBins   = maxGLRStacks + 2
	perfForkHistBins    = 8 // 2..8, 9+
)

type perfCountersData struct {
	mergeCalls             atomic.Uint64
	mergeDeadPruned        atomic.Uint64
	mergePerKeyOverflow    atomic.Uint64
	mergeReplacements      atomic.Uint64
	stackEquivalentCalls   atomic.Uint64
	stackEquivalentTrue    atomic.Uint64
	stackCompareCalls      atomic.Uint64
	forkCount              atomic.Uint64
	firstConflictToken     atomic.Uint64
	maxConcurrentStacks    atomic.Uint64
	lexBytes               atomic.Uint64
	lexTokens              atomic.Uint64
	reuseNodesVisited      atomic.Uint64
	reuseNodesPushed       atomic.Uint64
	reuseNodesPopped       atomic.Uint64
	reuseCandidatesChecked atomic.Uint64
	reuseSuccesses         atomic.Uint64
	mergeStacksInHist      [perfMergeHistBins]atomic.Uint64
	mergeAliveHist         [perfMergeHistBins]atomic.Uint64
	forkActionsHist        [perfForkHistBins]atomic.Uint64
}

var perfCounters perfCountersData

type PerfCounters struct {
	MergeCalls             uint64
	MergeDeadPruned        uint64
	MergePerKeyOverflow    uint64
	MergeReplacements      uint64
	StackEquivalentCalls   uint64
	StackEquivalentTrue    uint64
	StackCompareCalls      uint64
	ForkCount              uint64
	FirstConflictToken     uint64
	MaxConcurrentStacks    uint64
	LexBytes               uint64
	LexTokens              uint64
	ReuseNodesVisited      uint64
	ReuseNodesPushed       uint64
	ReuseNodesPopped       uint64
	ReuseCandidatesChecked uint64
	ReuseSuccesses         uint64
	MergeStacksInHist      [perfMergeHistBins]uint64
	MergeAliveHist         [perfMergeHistBins]uint64
	ForkActionsHist        [perfForkHistBins]uint64
}

func ResetPerfCounters() {
	perfCounters.mergeCalls.Store(0)
	perfCounters.mergeDeadPruned.Store(0)
	perfCounters.mergePerKeyOverflow.Store(0)
	perfCounters.mergeReplacements.Store(0)
	perfCounters.stackEquivalentCalls.Store(0)
	perfCounters.stackEquivalentTrue.Store(0)
	perfCounters.stackCompareCalls.Store(0)
	perfCounters.forkCount.Store(0)
	perfCounters.firstConflictToken.Store(0)
	perfCounters.maxConcurrentStacks.Store(0)
	perfCounters.lexBytes.Store(0)
	perfCounters.lexTokens.Store(0)
	perfCounters.reuseNodesVisited.Store(0)
	perfCounters.reuseNodesPushed.Store(0)
	perfCounters.reuseNodesPopped.Store(0)
	perfCounters.reuseCandidatesChecked.Store(0)
	perfCounters.reuseSuccesses.Store(0)
	for i := range perfCounters.mergeStacksInHist {
		perfCounters.mergeStacksInHist[i].Store(0)
	}
	for i := range perfCounters.mergeAliveHist {
		perfCounters.mergeAliveHist[i].Store(0)
	}
	for i := range perfCounters.forkActionsHist {
		perfCounters.forkActionsHist[i].Store(0)
	}
}

func PerfCountersSnapshot() PerfCounters {
	var out PerfCounters
	out.MergeCalls = perfCounters.mergeCalls.Load()
	out.MergeDeadPruned = perfCounters.mergeDeadPruned.Load()
	out.MergePerKeyOverflow = perfCounters.mergePerKeyOverflow.Load()
	out.MergeReplacements = perfCounters.mergeReplacements.Load()
	out.StackEquivalentCalls = perfCounters.stackEquivalentCalls.Load()
	out.StackEquivalentTrue = perfCounters.stackEquivalentTrue.Load()
	out.StackCompareCalls = perfCounters.stackCompareCalls.Load()
	out.ForkCount = perfCounters.forkCount.Load()
	out.FirstConflictToken = perfCounters.firstConflictToken.Load()
	out.MaxConcurrentStacks = perfCounters.maxConcurrentStacks.Load()
	out.LexBytes = perfCounters.lexBytes.Load()
	out.LexTokens = perfCounters.lexTokens.Load()
	out.ReuseNodesVisited = perfCounters.reuseNodesVisited.Load()
	out.ReuseNodesPushed = perfCounters.reuseNodesPushed.Load()
	out.ReuseNodesPopped = perfCounters.reuseNodesPopped.Load()
	out.ReuseCandidatesChecked = perfCounters.reuseCandidatesChecked.Load()
	out.ReuseSuccesses = perfCounters.reuseSuccesses.Load()
	for i := range out.MergeStacksInHist {
		out.MergeStacksInHist[i] = perfCounters.mergeStacksInHist[i].Load()
	}
	for i := range out.MergeAliveHist {
		out.MergeAliveHist[i] = perfCounters.mergeAliveHist[i].Load()
	}
	for i := range out.ForkActionsHist {
		out.ForkActionsHist[i] = perfCounters.forkActionsHist[i].Load()
	}
	return out
}

func perfRecordMergeCall(stacksIn int) {
	perfCounters.mergeCalls.Add(1)
	perfCounters.mergeStacksInHist[perfMergeHistBin(stacksIn)].Add(1)
}

func perfRecordMergeAlive(alive, dead int) {
	if dead > 0 {
		perfCounters.mergeDeadPruned.Add(uint64(dead))
	}
	perfCounters.mergeAliveHist[perfMergeHistBin(alive)].Add(1)
}

func perfRecordMergePerKeyOverflow() {
	perfCounters.mergePerKeyOverflow.Add(1)
}

func perfRecordMergeReplacement() {
	perfCounters.mergeReplacements.Add(1)
}

func perfRecordStackEquivalentCall() {
	perfCounters.stackEquivalentCalls.Add(1)
}

func perfRecordStackEquivalentTrue() {
	perfCounters.stackEquivalentTrue.Add(1)
}

func perfRecordStackCompare() {
	perfCounters.stackCompareCalls.Add(1)
}

func perfRecordFork(actionCount int, tokenPos uint64) {
	perfCounters.forkCount.Add(1)
	perfCounters.forkActionsHist[perfForkHistBin(actionCount)].Add(1)
	if tokenPos == 0 {
		return
	}
	perfCounters.firstConflictToken.CompareAndSwap(0, tokenPos)
}

func perfRecordMaxConcurrentStacks(n int) {
	if n <= 0 {
		return
	}
	target := uint64(n)
	for {
		prev := perfCounters.maxConcurrentStacks.Load()
		if target <= prev {
			return
		}
		if perfCounters.maxConcurrentStacks.CompareAndSwap(prev, target) {
			return
		}
	}
}

func perfRecordLexed(bytes, tokens int) {
	if bytes > 0 {
		perfCounters.lexBytes.Add(uint64(bytes))
	}
	if tokens > 0 {
		perfCounters.lexTokens.Add(uint64(tokens))
	}
}

func perfRecordReuseVisited() {
	perfCounters.reuseNodesVisited.Add(1)
}

func perfRecordReusePushed(n int) {
	if n > 0 {
		perfCounters.reuseNodesPushed.Add(uint64(n))
	}
}

func perfRecordReusePopped() {
	perfCounters.reuseNodesPopped.Add(1)
}

func perfRecordReuseCandidates(n int) {
	if n > 0 {
		perfCounters.reuseCandidatesChecked.Add(uint64(n))
	}
}

func perfRecordReuseSuccess() {
	perfCounters.reuseSuccesses.Add(1)
}

func perfMergeHistBin(n int) int {
	if n < 0 {
		return 0
	}
	if n >= perfMergeHistBins {
		return perfMergeHistBins - 1
	}
	return n
}

func perfForkHistBin(actions int) int {
	if actions <= 2 {
		return 0
	}
	if actions >= 9 {
		return perfForkHistBins - 1
	}
	return actions - 2
}
