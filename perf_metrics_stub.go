//go:build !perf

package gotreesitter

const perfCountersEnabled = false

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
	MergeStacksInHist      [maxGLRStacks + 2]uint64
	MergeAliveHist         [maxGLRStacks + 2]uint64
	ForkActionsHist        [8]uint64
}

func ResetPerfCounters()                 {}
func PerfCountersSnapshot() PerfCounters { return PerfCounters{} }

func perfRecordMergeCall(int)           {}
func perfRecordMergeAlive(int, int)     {}
func perfRecordMergePerKeyOverflow()    {}
func perfRecordMergeReplacement()       {}
func perfRecordStackEquivalentCall()    {}
func perfRecordStackEquivalentTrue()    {}
func perfRecordStackCompare()           {}
func perfRecordFork(int, uint64)        {}
func perfRecordMaxConcurrentStacks(int) {}
func perfRecordLexed(int, int)          {}
func perfRecordReuseVisited()           {}
func perfRecordReusePushed(int)         {}
func perfRecordReusePopped()            {}
func perfRecordReuseCandidates(int)     {}
func perfRecordReuseSuccess()           {}
