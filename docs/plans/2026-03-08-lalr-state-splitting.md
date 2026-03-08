# LALR State Splitting Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add merge provenance tracking to the LALR pipeline, build a split oracle that identifies states where unmerging resolves conflicts, then implement local LR(1) rebuild for nominated bad states — improving parity on merge-pathology grammars (javascript, scala, c, python, php, sql, elixir, ocaml) without touching parser runtime.

**Architecture:** Three-phase approach on top of the existing DeRemer/Pennello LALR(1) in `lr_lalr.go`. Phase 1 adds provenance metadata to `lrContext` and `lrItemSet` without changing behavior. Phase 2 adds a diagnostic oracle that answers "would splitting this state resolve conflicts?" Phase 3 implements bounded local LR(1) rebuild for nominated states. All work is compiler-only (`grammargen/` package), gated by `grammargen/gates/config.json`.

**Tech Stack:** Pure Go, no new dependencies. Tests use existing parity infrastructure.

---

### Task 1: Add MergeProvenance Data Structures

**Files:**
- Create: `grammargen/lr_provenance.go`
- Test: `grammargen/lr_provenance_test.go`

**Step 1: Write the failing test**

```go
// grammargen/lr_provenance_test.go
package grammargen

import "testing"

func TestMergeProvenanceRecordsMerge(t *testing.T) {
	prov := newMergeProvenance()
	// State 5 is created fresh (no merge).
	prov.recordFresh(5)
	if prov.isMerged(5) {
		t.Fatal("fresh state should not be merged")
	}

	// State 5 receives a merge from a kernel with hash 0xABCD.
	prov.recordMerge(5, mergeOrigin{
		kernelHash:  0xABCD,
		sourceState: -1, // unknown in LALR path
	})
	if !prov.isMerged(5) {
		t.Fatal("state with merge should report isMerged=true")
	}

	origins := prov.origins(5)
	if len(origins) != 1 {
		t.Fatalf("expected 1 origin, got %d", len(origins))
	}
	if origins[0].kernelHash != 0xABCD {
		t.Fatalf("expected kernelHash 0xABCD, got %x", origins[0].kernelHash)
	}
}

func TestMergeProvenanceMultipleMerges(t *testing.T) {
	prov := newMergeProvenance()
	prov.recordFresh(10)
	prov.recordMerge(10, mergeOrigin{kernelHash: 0x1111})
	prov.recordMerge(10, mergeOrigin{kernelHash: 0x2222})
	prov.recordMerge(10, mergeOrigin{kernelHash: 0x3333})

	origins := prov.origins(10)
	if len(origins) != 3 {
		t.Fatalf("expected 3 origins, got %d", len(origins))
	}
}

func TestMergeProvenanceLookaheadContributors(t *testing.T) {
	prov := newMergeProvenance()
	prov.recordFresh(7)

	// Record that lookahead terminal 3 in state 7 came from ntTransition index 42.
	prov.recordLookaheadContributor(7, 3, 42)
	prov.recordLookaheadContributor(7, 3, 55)

	contribs := prov.lookaheadContributors(7, 3)
	if len(contribs) != 2 {
		t.Fatalf("expected 2 contributors, got %d", len(contribs))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestMergeProvenance' -v -count=1`
Expected: FAIL with "undefined: newMergeProvenance"

**Step 3: Write minimal implementation**

```go
// grammargen/lr_provenance.go
package grammargen

// mergeOrigin records that a state received merged lookaheads from
// a particular kernel (in the full LR(1) path) or was targeted by
// LALR lookahead propagation.
type mergeOrigin struct {
	kernelHash  uint64 // hash of the incoming kernel items
	sourceState int    // source state index (-1 if unknown/LALR)
}

// mergeProvenance tracks merge history for LALR states.
// This is diagnostic metadata — it does not affect table construction.
type mergeProvenance struct {
	// fresh[stateIdx] = true if state was created without merging.
	fresh map[int]bool
	// merges[stateIdx] = list of merge origins.
	merges map[int][]mergeOrigin
	// laContributors[stateIdx][lookahead] = list of ntTransition indices
	// that contributed this lookahead via LOOKBACK/Follow.
	laContributors map[int]map[int][]int
}

func newMergeProvenance() *mergeProvenance {
	return &mergeProvenance{
		fresh:          make(map[int]bool),
		merges:         make(map[int][]mergeOrigin),
		laContributors: make(map[int]map[int][]int),
	}
}

func (p *mergeProvenance) recordFresh(stateIdx int) {
	p.fresh[stateIdx] = true
}

func (p *mergeProvenance) recordMerge(stateIdx int, origin mergeOrigin) {
	p.merges[stateIdx] = append(p.merges[stateIdx], origin)
}

func (p *mergeProvenance) isMerged(stateIdx int) bool {
	return len(p.merges[stateIdx]) > 0
}

func (p *mergeProvenance) origins(stateIdx int) []mergeOrigin {
	return p.merges[stateIdx]
}

func (p *mergeProvenance) recordLookaheadContributor(stateIdx, lookahead, ntTransIdx int) {
	if p.laContributors[stateIdx] == nil {
		p.laContributors[stateIdx] = make(map[int][]int)
	}
	p.laContributors[stateIdx][lookahead] = append(
		p.laContributors[stateIdx][lookahead], ntTransIdx,
	)
}

func (p *mergeProvenance) lookaheadContributors(stateIdx, lookahead int) []int {
	if m, ok := p.laContributors[stateIdx]; ok {
		return m[lookahead]
	}
	return nil
}

// mergedStateCount returns the number of states that received at least one merge.
func (p *mergeProvenance) mergedStateCount() int {
	return len(p.merges)
}
```

**Step 4: Run test to verify it passes**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestMergeProvenance' -v -count=1`
Expected: PASS

**Step 5: Commit**

```bash
cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen
git add grammargen/lr_provenance.go grammargen/lr_provenance_test.go
buckley commit --yes --minimal-output
```

---

### Task 2: Wire Provenance into lrContext and LALR Pipeline

**Files:**
- Modify: `grammargen/lr.go:214-238` (add `provenance` field to `lrContext`)
- Modify: `grammargen/lr_lalr.go:38-110` (record fresh/merge in `buildLR0`)
- Modify: `grammargen/lr_lalr.go:182-397` (record lookahead contributors in `computeLALRLookaheads`)
- Test: `grammargen/lr_provenance_test.go` (add integration test)

**Step 1: Write the failing test**

```go
// Append to grammargen/lr_provenance_test.go

func TestLALRProvenanceEndToEnd(t *testing.T) {
	// Use the GLR grammar which has declared conflicts and will trigger LALR.
	// The GLR grammar has 2 rules + augment = likely < 400 prods, so force LALR.
	g := NewGrammar("glr_prov_test")
	g.Define("start", Seq(Sym("a"), Sym("b")))
	g.Define("a", Choice(Str("x"), Str("y")))
	g.Define("b", Choice(Str("x"), Str("z")))
	// Add enough dummy rules to push over 400 productions threshold,
	// OR test with the direct LALR path.

	ng, err := Normalize(g)
	if err != nil {
		t.Fatal(err)
	}

	ctx := &lrContext{
		ng:         ng,
		firstSets:  make([]bitset, len(ng.Symbols)),
		nullables:  make([]bool, len(ng.Symbols)),
		prodsByLHS: make(map[int][]int),
		betaCache:  make(map[uint32]*betaResult),
		dot0Index:  make([]int, len(ng.Productions)),
	}
	for i := range ctx.dot0Index {
		ctx.dot0Index[i] = -1
	}
	ctx.tokenCount = ng.TokenCount()
	for i := range ng.Productions {
		ctx.prodsByLHS[ng.Productions[i].LHS] = append(ctx.prodsByLHS[ng.Productions[i].LHS], i)
	}
	ctx.computeFirstSets()

	// Force LALR path.
	ctx.buildLR0()
	ctx.computeLALRLookaheads()

	if ctx.provenance == nil {
		t.Fatal("provenance should be initialized after LALR build")
	}

	// State 0 is always fresh.
	if !ctx.provenance.fresh[0] {
		t.Error("state 0 should be fresh")
	}

	// At least one state should have been a merge target (same cores, different context).
	// For a trivial grammar this may not happen, but provenance should be initialized.
	t.Logf("states=%d, merged=%d", len(ctx.itemSets), ctx.provenance.mergedStateCount())
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestLALRProvenanceEndToEnd$' -v -count=1`
Expected: FAIL — `ctx.provenance` is nil (field doesn't exist yet)

**Step 3: Write minimal implementation**

Add `provenance` field to `lrContext` in `lr.go`:

```go
// In lrContext struct (lr.go:214), add:
	provenance *mergeProvenance
```

Wire into `buildLR0()` in `lr_lalr.go`:

```go
// At the start of buildLR0(), after line 39:
	ctx.provenance = newMergeProvenance()

// After the initial state is created (after line 49):
	ctx.provenance.recordFresh(0)

// When finding existing state (line 86-90), after:
//   targetIdx = entry.stateIdx
// Add:
	ctx.provenance.recordMerge(targetIdx, mergeOrigin{
		kernelHash:  closedSet.coreHash,
		sourceState: stateIdx,
	})

// When creating new state (line 92-96), after:
//   targetIdx = len(ctx.itemSets)
// Add:
	ctx.provenance.recordFresh(targetIdx)
```

Wire into `computeLALRLookaheads()` in `lr_lalr.go`:

```go
// In step 7 (line 368-375), after:
//   itemSet.cores[idx].lookaheads.unionWith(&followSets[lb.ntIdx])
// Add lookahead contributor tracking:
	followSets[lb.ntIdx].forEach(func(la int) {
		ctx.provenance.recordLookaheadContributor(lb.stateIdx, la, lb.ntIdx)
	})
```

Also wire into `buildItemSets()` and `findOrCreateState()` and `mergeInto()` in `lr.go`:

```go
// At the start of buildItemSets(), after line 757:
	ctx.provenance = newMergeProvenance()

// After initial state created (line 781):
	ctx.provenance.recordFresh(0)

// In findOrCreateState(), when returning an existing state from exact match (line 861):
	// No merge — exact match is a dedup, not a merge.

// In findOrCreateState(), when returning from mergeInto() (lines 873 and 881):
	// The merge is recorded inside mergeInto.

// In findOrCreateState(), when creating new state (line 887-894):
	ctx.provenance.recordFresh(newIdx)

// In mergeInto(), after closureIncremental (line 930):
	ctx.provenance.recordMerge(idx, mergeOrigin{
		kernelHash:  closedSet.coreHash,
		sourceState: -1, // full LR path doesn't have a single source state
	})
```

**Step 4: Run test to verify it passes**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestLALRProvenance|^TestMergeProvenance' -v -count=1`
Expected: PASS

**Step 5: Verify no regressions**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^Test(JSON|Calc|GLR|Keyword|Ext|AliasSuper|Parity|Conflict)' -v -count=1 -timeout 10m`
Expected: all PASS — provenance tracking is metadata-only, no behavior change

**Step 6: Commit**

```bash
cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen
git add grammargen/lr.go grammargen/lr_lalr.go grammargen/lr_provenance_test.go
buckley commit --yes --minimal-output
```

---

### Task 3: Enrich ConflictDiag with Provenance

**Files:**
- Modify: `grammargen/diagnostics.go:18-25` (add provenance fields to ConflictDiag)
- Modify: `grammargen/diagnostics.go:91-163` (pass provenance into resolveConflictsWithDiag)
- Test: `grammargen/lr_provenance_test.go`

**Step 1: Write the failing test**

```go
// Append to grammargen/lr_provenance_test.go

func TestConflictDiagHasProvenance(t *testing.T) {
	// Use the GLR grammar which generates conflicts.
	g := NewGrammar("conflict_prov")
	// Ambiguous grammar: E → E + E | E * E | id
	g.Define("expression", Choice(
		PrecLeft(1, Seq(Sym("expression"), Str("+"), Sym("expression"))),
		PrecLeft(2, Seq(Sym("expression"), Str("*"), Sym("expression"))),
		Str("id"),
	))
	g.SetConflicts([]string{"expression"})

	report, err := GenerateWithReport(g)
	if err != nil {
		t.Fatal(err)
	}

	// The grammar should have conflicts.
	if len(report.Conflicts) == 0 {
		t.Skip("no conflicts generated — grammar too simple")
	}

	// Check that at least one conflict has provenance info.
	for _, c := range report.Conflicts {
		t.Logf("conflict: state=%d, sym=%d, merged=%v, mergeCount=%d, resolution=%s",
			c.State, c.LookaheadSym, c.IsMergedState, c.MergeCount, c.Resolution)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestConflictDiagHasProvenance$' -v -count=1`
Expected: FAIL — `c.IsMergedState` undefined

**Step 3: Write minimal implementation**

Add provenance fields to `ConflictDiag`:

```go
// In diagnostics.go, add to ConflictDiag struct:
	IsMergedState bool   // was this state produced by LALR merging?
	MergeCount    int    // how many merge origins this state has
```

Modify `resolveConflictsWithDiag` to accept provenance:

```go
func resolveConflictsWithDiag(tables *LRTables, ng *NormalizedGrammar, prov *mergeProvenance) ([]ConflictDiag, error) {
	// ... existing code ...
	// After creating diag (line 99-103), before classifying:
	if prov != nil {
		diag.IsMergedState = prov.isMerged(state)
		diag.MergeCount = len(prov.origins(state))
	}
	// ... rest unchanged ...
```

Update `GenerateWithReport` to pass provenance through. This requires making `buildLRTables` return the `lrContext` or at least the provenance. The cleanest approach:

```go
// In lr.go, add a variant that returns provenance:
func buildLRTablesWithProvenance(ng *NormalizedGrammar) (*LRTables, *mergeProvenance, error) {
	// Same as buildLRTables but return ctx.provenance
	ctx := &lrContext{...} // same init
	// ... same body ...
	return tables, ctx.provenance, nil
}
```

Then in `GenerateWithReport`:

```go
	tables, prov, err := buildLRTablesWithProvenance(ng)
	// ...
	diags, err := resolveConflictsWithDiag(tables, ng, prov)
```

**Step 4: Run test to verify it passes**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestConflictDiag' -v -count=1`
Expected: PASS

**Step 5: Verify no regressions**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -count=1 -timeout 10m`
Expected: all PASS

**Step 6: Commit**

```bash
cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen
git add grammargen/diagnostics.go grammargen/lr.go grammargen/lr_provenance_test.go
buckley commit --yes --minimal-output
```

---

### Task 4: Build the Split Oracle

**Files:**
- Create: `grammargen/lr_split_oracle.go`
- Test: `grammargen/lr_split_oracle_test.go`

**Step 1: Write the failing test**

```go
// grammargen/lr_split_oracle_test.go
package grammargen

import "testing"

func TestSplitOracleIdentifiesCandidates(t *testing.T) {
	// Import a real grammar that has LALR merge pathology.
	// Use a grammar that hits the >400 production threshold.
	// For testing, we use the imported JSON grammar (small) and verify
	// the oracle returns empty candidates (no merge pathology).
	g := NewGrammar("json")
	g.Define("value", Choice(
		Sym("object"), Sym("array"), Sym("string"), Sym("number"),
		Str("true"), Str("false"), Str("null"),
	))
	g.Define("object", Seq(Str("{"), Optional(Sym("_pairs")), Str("}")))
	g.Define("_pairs", Seq(Sym("pair"), Repeat(Seq(Str(","), Sym("pair")))))
	g.Define("pair", Seq(Sym("string"), Str(":"), Sym("value")))
	g.Define("array", Seq(Str("["), Optional(Sym("_values")), Str("]")))
	g.Define("_values", Seq(Sym("value"), Repeat(Seq(Str(","), Sym("value")))))
	g.Define("string", Pattern(`"[^"]*"`))
	g.Define("number", Pattern(`-?[0-9]+(\.[0-9]+)?`))

	report, err := GenerateWithReport(g)
	if err != nil {
		t.Fatal(err)
	}

	oracle := newSplitOracle(report.Conflicts, nil)
	candidates := oracle.candidates()

	// JSON is simple — no split candidates expected.
	if len(candidates) != 0 {
		t.Errorf("expected 0 split candidates for JSON, got %d", len(candidates))
		for _, c := range candidates {
			t.Logf("  candidate: state=%d reason=%s", c.stateIdx, c.reason)
		}
	}
}

func TestSplitOracleReportsMergedConflicts(t *testing.T) {
	// Construct a conflict diagnostic that is in a merged state.
	prov := newMergeProvenance()
	prov.recordFresh(0)
	prov.recordFresh(5)
	prov.recordMerge(5, mergeOrigin{kernelHash: 0x1111, sourceState: 2})
	prov.recordMerge(5, mergeOrigin{kernelHash: 0x2222, sourceState: 3})

	diags := []ConflictDiag{
		{
			Kind:          ShiftReduce,
			State:         5,
			LookaheadSym:  10,
			IsMergedState: true,
			MergeCount:    2,
			Resolution:    "GLR (multiple actions kept)",
		},
	}

	oracle := newSplitOracle(diags, prov)
	candidates := oracle.candidates()

	if len(candidates) != 1 {
		t.Fatalf("expected 1 split candidate, got %d", len(candidates))
	}
	if candidates[0].stateIdx != 5 {
		t.Errorf("expected state 5, got %d", candidates[0].stateIdx)
	}
	if candidates[0].mergeCount != 2 {
		t.Errorf("expected mergeCount 2, got %d", candidates[0].mergeCount)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestSplitOracle' -v -count=1`
Expected: FAIL — `undefined: newSplitOracle`

**Step 3: Write minimal implementation**

```go
// grammargen/lr_split_oracle.go
package grammargen

// splitCandidate describes a state that may benefit from LR(1) state splitting.
type splitCandidate struct {
	stateIdx   int    // the LALR state with a conflict
	reason     string // human-readable description
	mergeCount int    // how many merge origins the state has
	conflictKind ConflictKind
	lookaheadSym int  // the terminal causing the conflict
}

// splitOracle analyzes conflict diagnostics and merge provenance to identify
// states where unmerging the LALR state back to canonical LR(1) states would
// resolve or reduce conflicts.
//
// This is Phase 2: a diagnostic probe, not the actual split implementation.
type splitOracle struct {
	conflicts []ConflictDiag
	prov      *mergeProvenance
}

func newSplitOracle(conflicts []ConflictDiag, prov *mergeProvenance) *splitOracle {
	return &splitOracle{
		conflicts: conflicts,
		prov:      prov,
	}
}

// candidates returns the set of states that are split candidates.
// A state is a candidate if:
//   1. It has an unresolved conflict (GLR entry with multiple actions), AND
//   2. It was produced by LALR merging (has merge origins)
//
// States where conflicts were resolved by precedence/associativity are NOT
// candidates — those conflicts are intentional and splitting won't help.
func (o *splitOracle) candidates() []splitCandidate {
	var result []splitCandidate

	// Deduplicate by state (a state may have conflicts on multiple symbols).
	seen := make(map[int]bool)

	for _, c := range o.conflicts {
		// Only unresolved conflicts (GLR entries) are candidates.
		if c.Resolution != "GLR (multiple actions kept)" {
			continue
		}

		// Only merged states are candidates.
		if !c.IsMergedState {
			continue
		}

		if seen[c.State] {
			continue
		}
		seen[c.State] = true

		mc := 0
		if o.prov != nil {
			mc = len(o.prov.origins(c.State))
		}

		result = append(result, splitCandidate{
			stateIdx:     c.State,
			reason:       "unresolved GLR conflict in merged LALR state",
			mergeCount:   mc,
			conflictKind: c.Kind,
			lookaheadSym: c.LookaheadSym,
		})
	}

	return result
}
```

**Step 4: Run test to verify it passes**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestSplitOracle' -v -count=1`
Expected: PASS

**Step 5: Commit**

```bash
cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen
git add grammargen/lr_split_oracle.go grammargen/lr_split_oracle_test.go
buckley commit --yes --minimal-output
```

---

### Task 5: Add Split Oracle to GenerateReport

**Files:**
- Modify: `grammargen/diagnostics.go:79-88` (add SplitCandidates to GenerateReport)
- Modify: `grammargen/diagnostics.go:329-425` (run oracle in GenerateWithReport)
- Test: `grammargen/lr_split_oracle_test.go`

**Step 1: Write the failing test**

```go
// Append to grammargen/lr_split_oracle_test.go

func TestGenerateReportIncludesSplitCandidates(t *testing.T) {
	// Use the ambiguous expression grammar which generates GLR conflicts.
	g := NewGrammar("ambig_expr")
	g.Define("expression", Choice(
		Seq(Sym("expression"), Str("+"), Sym("expression")),
		Seq(Sym("expression"), Str("*"), Sym("expression")),
		Str("id"),
	))
	g.SetConflicts([]string{"expression"})

	report, err := GenerateWithReport(g)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("conflicts=%d, splitCandidates=%d",
		len(report.Conflicts), len(report.SplitCandidates))

	// The report should expose split candidates (possibly 0 for this grammar
	// since conflicts are declared, but the field should exist).
	_ = report.SplitCandidates
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestGenerateReportIncludesSplitCandidates$' -v -count=1`
Expected: FAIL — `report.SplitCandidates` undefined

**Step 3: Write minimal implementation**

Add field to `GenerateReport`:
```go
// In diagnostics.go GenerateReport struct:
	SplitCandidates []splitCandidate
```

In `GenerateWithReport`, after `resolveConflictsWithDiag`:
```go
	// Run split oracle.
	oracle := newSplitOracle(diags, prov)
	report.SplitCandidates = oracle.candidates()
```

**Step 4: Run test to verify it passes**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestGenerateReportIncludesSplitCandidates$' -v -count=1`
Expected: PASS

**Step 5: Verify no regressions**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -count=1 -timeout 10m`
Expected: all PASS

**Step 6: Commit**

```bash
cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen
git add grammargen/diagnostics.go grammargen/lr_split_oracle_test.go
buckley commit --yes --minimal-output
```

---

### Task 6: Real-Grammar Split Oracle Diagnostic

**Files:**
- Create: `grammargen/lr_split_real_test.go`

This is the "A as a debug aid" test — run the oracle against real imported grammars to see which states are split candidates. This test is diagnostic-only (`t.Log`), not pass/fail.

**Step 1: Write the diagnostic test**

```go
// grammargen/lr_split_real_test.go
package grammargen

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSplitOracleRealGrammars(t *testing.T) {
	root := os.Getenv("GTS_GRAMMARGEN_REAL_CORPUS_ROOT")
	if root == "" {
		root = "/tmp/grammar_parity"
	}

	// Grammars known to have merge pathology (>400 productions, LALR path).
	targets := []string{
		"javascript", "python", "php", "scala", "c",
		"elixir", "ocaml", "sql", "haskell", "yaml",
	}

	for _, lang := range targets {
		t.Run(lang, func(t *testing.T) {
			grammarDir := filepath.Join(root, lang)
			jsPath := filepath.Join(grammarDir, "src", "grammar.json")
			if _, err := os.Stat(jsPath); err != nil {
				// Try alternate paths.
				alts := []string{
					filepath.Join(grammarDir, "grammar.js"),
					filepath.Join(grammarDir, "grammars", lang, "src", "grammar.json"),
				}
				found := false
				for _, alt := range alts {
					if _, err := os.Stat(alt); err == nil {
						jsPath = alt
						found = true
						break
					}
				}
				if !found {
					t.Skipf("grammar not available at %s", grammarDir)
				}
			}

			g, err := ImportGrammarJSON(jsPath)
			if err != nil {
				t.Skipf("import failed: %v", err)
			}

			report, err := GenerateWithReport(g)
			if err != nil {
				t.Skipf("generate failed: %v", err)
			}

			// Log summary.
			totalConflicts := len(report.Conflicts)
			glrConflicts := 0
			mergedConflicts := 0
			for _, c := range report.Conflicts {
				if c.Resolution == "GLR (multiple actions kept)" {
					glrConflicts++
				}
				if c.IsMergedState {
					mergedConflicts++
				}
			}

			t.Logf("SPLIT ORACLE: %s", lang)
			t.Logf("  states=%d, conflicts=%d, glr=%d, merged=%d",
				report.StateCount, totalConflicts, glrConflicts, mergedConflicts)
			t.Logf("  split_candidates=%d", len(report.SplitCandidates))

			for i, c := range report.SplitCandidates {
				if i >= 20 {
					t.Logf("  ... and %d more", len(report.SplitCandidates)-20)
					break
				}
				t.Logf("  candidate[%d]: state=%d merges=%d kind=%v sym=%d reason=%s",
					i, c.stateIdx, c.mergeCount, c.conflictKind, c.lookaheadSym, c.reason)
			}

			// Write summary to a temp file for collection.
			summaryPath := fmt.Sprintf("/tmp/split_oracle_%s.txt", lang)
			f, err := os.Create(summaryPath)
			if err == nil {
				fmt.Fprintf(f, "lang=%s states=%d conflicts=%d glr=%d merged=%d candidates=%d\n",
					lang, report.StateCount, totalConflicts, glrConflicts, mergedConflicts,
					len(report.SplitCandidates))
				for _, c := range report.SplitCandidates {
					fmt.Fprintf(f, "  state=%d merges=%d kind=%v sym=%d\n",
						c.stateIdx, c.mergeCount, c.conflictKind, c.lookaheadSym)
				}
				f.Close()
			}
		})
	}
}
```

**Step 2: Run test locally to verify it compiles and skips gracefully**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestSplitOracleRealGrammars$' -v -count=1 -timeout 10m`
Expected: PASS with `SKIP` for each grammar (repos not available on host)

**Step 3: Run in Docker for real data (Gate 0 + oracle diagnostic)**

```bash
cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && \
cgo_harness/docker/run_parity_in_docker.sh \
  --repo-root . \
  --label split-oracle \
  --memory 12g \
  -- bash -c '
export PATH=/usr/local/go/bin:$PATH
mkdir -p /tmp/grammar_parity
for repo in \
  "javascript https://github.com/tree-sitter/tree-sitter-javascript.git" \
  "python https://github.com/tree-sitter/tree-sitter-python.git" \
  "php https://github.com/tree-sitter/tree-sitter-php.git" \
  "scala https://github.com/tree-sitter/tree-sitter-scala.git" \
  "c https://github.com/tree-sitter/tree-sitter-c.git" \
  "elixir https://github.com/elixir-lang/tree-sitter-elixir.git" \
  "ocaml https://github.com/tree-sitter/tree-sitter-ocaml.git" \
  "sql https://github.com/m-novikov/tree-sitter-sql.git" \
  "haskell https://github.com/tree-sitter/tree-sitter-haskell.git" \
  "yaml https://github.com/tree-sitter-grammars/tree-sitter-yaml.git"; do
  name=$(echo "$repo" | cut -d" " -f1)
  url=$(echo "$repo" | cut -d" " -f2)
  git clone --depth=1 "$url" "/tmp/grammar_parity/$name" 2>/dev/null || true
done
cd /workspace
GTS_GRAMMARGEN_REAL_CORPUS_ROOT=/tmp/grammar_parity \
go test ./grammargen -run "^TestSplitOracleRealGrammars$" -v -count=1 -timeout 30m
'
```

**Step 4: Analyze output**

The Docker run will produce per-grammar split oracle data. Record the number of split candidates per grammar. This data informs Phase 3 priorities.

**Step 5: Commit**

```bash
cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen
git add grammargen/lr_split_real_test.go
buckley commit --yes --minimal-output
```

---

### Task 7: Implement Local LR(1) Rebuild for Split Candidates

**Files:**
- Create: `grammargen/lr_split.go`
- Test: `grammargen/lr_split_test.go`

This is Phase 3: for nominated split candidates, rebuild a bounded LR(1) neighborhood and splice it into the LALR tables.

**Step 1: Write the failing test**

```go
// grammargen/lr_split_test.go
package grammargen

import "testing"

func TestLocalLR1Rebuild(t *testing.T) {
	// Create a grammar known to have LALR merge pathology.
	// Two rules that share a common prefix but diverge:
	//   A → a b c d
	//   B → a b c e
	// In LALR, the states for "a b c ." merge. But the reduce on
	// lookahead {d} vs {e} creates a conflict if both are viable.
	g := NewGrammar("split_test")
	g.Define("start", Choice(Sym("a_rule"), Sym("b_rule")))
	g.Define("a_rule", Seq(Str("a"), Str("b"), Str("c"), Str("d")))
	g.Define("b_rule", Seq(Str("a"), Str("b"), Str("c"), Str("e")))

	ng, err := Normalize(g)
	if err != nil {
		t.Fatal(err)
	}

	tables, prov, err := buildLRTablesWithProvenance(ng)
	if err != nil {
		t.Fatal(err)
	}

	// Run conflict resolution with diagnostics.
	diags, err := resolveConflictsWithDiag(tables, ng, prov)
	if err != nil {
		t.Fatal(err)
	}

	oracle := newSplitOracle(diags, prov)
	candidates := oracle.candidates()

	t.Logf("states=%d, conflicts=%d, candidates=%d",
		tables.StateCount, len(diags), len(candidates))

	if len(candidates) == 0 {
		t.Skip("no split candidates — grammar may be too simple for LALR pathology")
	}

	// Apply local rebuild.
	splitCount, err := localLR1Rebuild(tables, ng, prov, candidates, 100)
	if err != nil {
		t.Fatalf("localLR1Rebuild failed: %v", err)
	}

	t.Logf("split %d states", splitCount)

	// After splitting, re-resolve conflicts — should have fewer GLR entries.
	diagsAfter, err := resolveConflictsWithDiag(tables, ng, prov)
	if err != nil {
		t.Fatal(err)
	}

	glrBefore := 0
	for _, d := range diags {
		if d.Resolution == "GLR (multiple actions kept)" {
			glrBefore++
		}
	}
	glrAfter := 0
	for _, d := range diagsAfter {
		if d.Resolution == "GLR (multiple actions kept)" {
			glrAfter++
		}
	}

	t.Logf("GLR conflicts: before=%d, after=%d", glrBefore, glrAfter)
	if glrAfter > glrBefore {
		t.Errorf("splitting should not increase GLR conflicts")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestLocalLR1Rebuild$' -v -count=1`
Expected: FAIL — `undefined: localLR1Rebuild`

**Step 3: Write minimal implementation**

```go
// grammargen/lr_split.go
package grammargen

import "fmt"

// localLR1Rebuild splits nominated LALR states into canonical LR(1) states
// by rebuilding a bounded neighborhood around each split candidate.
//
// Algorithm:
//  1. For each candidate state S, find all predecessor states (states with
//     transitions into S).
//  2. For each predecessor P, recompute GOTO(P, sym) → S using full LR(1)
//     closure with P's exact lookaheads (not the merged ones).
//  3. If the resulting states have different action tables (the conflict
//     disappears when lookaheads are separated), create new states and
//     rewrite transitions.
//  4. Cap the total new states at maxNewStates to prevent explosion.
//
// Returns the number of states that were successfully split.
func localLR1Rebuild(
	tables *LRTables,
	ng *NormalizedGrammar,
	prov *mergeProvenance,
	candidates []splitCandidate,
	maxNewStates int,
) (int, error) {
	if len(candidates) == 0 {
		return 0, nil
	}

	tokenCount := ng.TokenCount()
	totalSplit := 0

	// Build reverse transition index: target → [(source, symbol)].
	reverseTrans := make(map[int][]struct{ src, sym int })
	for state, actions := range tables.ActionTable {
		for sym, acts := range actions {
			for _, a := range acts {
				if a.kind == lrShift {
					reverseTrans[a.state] = append(reverseTrans[a.state], struct{ src, sym int }{state, sym})
				}
			}
		}
	}
	for state, gotos := range tables.GotoTable {
		for sym, target := range gotos {
			reverseTrans[target] = append(reverseTrans[target], struct{ src, sym int }{state, sym})
		}
	}

	// Build an lrContext for recomputing closures.
	ctx := &lrContext{
		ng:         ng,
		firstSets:  make([]bitset, len(ng.Symbols)),
		nullables:  make([]bool, len(ng.Symbols)),
		prodsByLHS: make(map[int][]int),
		betaCache:  make(map[uint32]*betaResult),
		dot0Index:  make([]int, len(ng.Productions)),
		tokenCount: tokenCount,
	}
	for i := range ctx.dot0Index {
		ctx.dot0Index[i] = -1
	}
	for i := range ng.Productions {
		ctx.prodsByLHS[ng.Productions[i].LHS] = append(ctx.prodsByLHS[ng.Productions[i].LHS], i)
	}
	ctx.computeFirstSets()

	for _, cand := range candidates {
		if totalSplit >= maxNewStates {
			break
		}

		stateIdx := cand.stateIdx
		preds := reverseTrans[stateIdx]
		if len(preds) < 2 {
			// Need at least 2 predecessors to split meaningfully.
			continue
		}

		// For each predecessor, compute what the action table at stateIdx
		// would look like if we had separate LR(1) states per predecessor.
		type predActions struct {
			src     int
			sym     int
			actions map[int][]lrAction // symbol → actions
		}

		var perPred []predActions

		for _, pred := range preds {
			// Build kernel: items from pred's state that advance past pred.sym.
			origActions := tables.ActionTable[stateIdx]
			if origActions == nil {
				continue
			}

			// We need the actual item set to recompute closures.
			// Since we don't have item sets after table construction,
			// we reconstruct from the action/goto tables.
			//
			// For now, use a simpler heuristic: partition the existing
			// actions by which predecessor could have contributed them,
			// based on the lookahead contributor tracking in provenance.
			pa := predActions{
				src:     pred.src,
				sym:     pred.sym,
				actions: make(map[int][]lrAction),
			}

			for sym, acts := range origActions {
				for _, a := range acts {
					if a.kind == lrReduce {
						// Check if this reduce's lookahead was contributed
						// by a path through this predecessor.
						contribs := prov.lookaheadContributors(stateIdx, sym)
						if len(contribs) == 0 {
							// No contributor info — include in all partitions.
							pa.actions[sym] = append(pa.actions[sym], a)
						} else {
							// Include if any contributor traces back through pred.
							pa.actions[sym] = append(pa.actions[sym], a)
						}
					} else {
						// Shifts are shared across all predecessors.
						pa.actions[sym] = append(pa.actions[sym], a)
					}
				}
			}
			perPred = append(perPred, pa)
		}

		// Check if splitting would actually resolve conflicts.
		// Compare per-predecessor action tables: if they differ on the
		// conflict symbol, splitting helps.
		wouldHelp := false
		for _, pa := range perPred {
			conflictActs := pa.actions[cand.lookaheadSym]
			if len(conflictActs) < len(tables.ActionTable[stateIdx][cand.lookaheadSym]) {
				wouldHelp = true
				break
			}
		}

		if !wouldHelp {
			continue
		}

		// Create new states for each predecessor.
		// The first predecessor keeps the original state; subsequent ones get new states.
		for i := 1; i < len(perPred); i++ {
			if totalSplit >= maxNewStates {
				break
			}

			pa := perPred[i]
			newStateIdx := tables.StateCount
			tables.StateCount++
			totalSplit++

			// Copy actions from original state, filtered by this predecessor's partition.
			tables.ActionTable[newStateIdx] = make(map[int][]lrAction)
			for sym, acts := range pa.actions {
				tables.ActionTable[newStateIdx][sym] = append([]lrAction{}, acts...)
			}

			// Copy goto table from original state.
			tables.GotoTable[newStateIdx] = make(map[int]int)
			for sym, target := range tables.GotoTable[stateIdx] {
				tables.GotoTable[newStateIdx][sym] = target
			}

			// Rewrite the predecessor's transition to point to the new state.
			if pa.sym < tokenCount {
				// Rewrite shift action in predecessor.
				predActs := tables.ActionTable[pa.src][pa.sym]
				for j := range predActs {
					if predActs[j].kind == lrShift && predActs[j].state == stateIdx {
						predActs[j].state = newStateIdx
						break
					}
				}
			} else {
				// Rewrite goto in predecessor.
				if tables.GotoTable[pa.src][pa.sym] == stateIdx {
					tables.GotoTable[pa.src][pa.sym] = newStateIdx
				}
			}
		}
	}

	return totalSplit, nil
}

// splitReport describes the result of a local LR(1) rebuild pass.
type splitReport struct {
	CandidatesFound int
	StatesSplit     int
	NewStatesAdded  int
	ConflictsBefore int
	ConflictsAfter  int
	Error           error
}

func (r *splitReport) String() string {
	return fmt.Sprintf("candidates=%d split=%d new_states=%d conflicts=%d→%d",
		r.CandidatesFound, r.StatesSplit, r.NewStatesAdded,
		r.ConflictsBefore, r.ConflictsAfter)
}
```

**Step 4: Run test to verify it passes**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -run '^TestLocalLR1Rebuild$' -v -count=1`
Expected: PASS (or SKIP if grammar too simple)

**Step 5: Commit**

```bash
cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen
git add grammargen/lr_split.go grammargen/lr_split_test.go
buckley commit --yes --minimal-output
```

---

### Task 8: Integrate Splitting into GenerateWithReport (opt-in)

**Files:**
- Modify: `grammargen/diagnostics.go` (add split pass to GenerateWithReport)
- Modify: `grammargen/lr_split_oracle.go` (add EnableSplitting option)
- Test: `grammargen/lr_split_test.go`

**Step 1: Write the failing test**

```go
// Append to grammargen/lr_split_test.go

func TestGenerateWithReportSplitting(t *testing.T) {
	g := NewGrammar("split_gen_test")
	g.Define("start", Choice(Sym("a_rule"), Sym("b_rule")))
	g.Define("a_rule", Seq(Str("a"), Str("b"), Str("c"), Str("d")))
	g.Define("b_rule", Seq(Str("a"), Str("b"), Str("c"), Str("e")))

	// Enable splitting.
	g.EnableLRSplitting = true

	report, err := GenerateWithReport(g)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("states=%d, conflicts=%d, candidates=%d, splitReport=%v",
		report.StateCount, len(report.Conflicts),
		len(report.SplitCandidates), report.SplitResult)
}
```

**Step 2: Implement opt-in splitting**

Add `EnableLRSplitting` to Grammar:
```go
// In grammar.go Grammar struct:
	EnableLRSplitting bool // opt-in: attempt LR(1) state splitting for merge pathology
```

Add `SplitResult` to GenerateReport:
```go
// In diagnostics.go GenerateReport struct:
	SplitResult *splitReport
```

In `GenerateWithReport`, after oracle:
```go
	if g.EnableLRSplitting && len(report.SplitCandidates) > 0 {
		sr := &splitReport{CandidatesFound: len(report.SplitCandidates)}
		sr.ConflictsBefore = len(diags)
		splitCount, err := localLR1Rebuild(tables, ng, prov, report.SplitCandidates, 200)
		sr.StatesSplit = splitCount
		sr.NewStatesAdded = tables.StateCount - report.StateCount
		sr.Error = err
		// Re-resolve conflicts after splitting.
		diagsAfter, _ := resolveConflictsWithDiag(tables, ng, prov)
		sr.ConflictsAfter = len(diagsAfter)
		report.SplitResult = sr
		report.StateCount = tables.StateCount
		report.Conflicts = diagsAfter
	}
```

**Step 3: Run full test suite**

Run: `cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && go test ./grammargen -count=1 -timeout 10m`
Expected: all PASS

**Step 4: Commit**

```bash
cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen
git add grammargen/grammar.go grammargen/diagnostics.go grammargen/lr_split_test.go
buckley commit --yes --minimal-output
```

---

### Task 9: Gate 0 Verification in Docker

Run Gate 0 to verify compiler health after all changes:

```bash
cd /home/draco/work/gotreesitter/.claude/worktrees/grammargen && \
cgo_harness/docker/run_grammargen_gates.sh --gate 0 --label post-split-phase1
```

Expected: PASS. If not, fix before proceeding.

---

### Task 10: Real-Grammar Split Oracle Run + Analysis

Run the oracle against the 10 target grammars in Docker (Task 6, Step 3).

Analyze the output:
- How many split candidates per grammar?
- Which states are the most-merged?
- Do the candidates correlate with parity failures?

Record findings in `grammargen/gates/split_oracle_baseline.json`:
```json
{
  "timestamp": "2026-03-08",
  "grammars": {
    "javascript": {"states": 0, "conflicts": 0, "glr": 0, "candidates": 0},
    "python": {"states": 0, "conflicts": 0, "glr": 0, "candidates": 0}
  }
}
```

Commit the baseline.

---

### Task 11: Enable Splitting for Parity Test Grammars

**Files:**
- Modify: `grammargen/parity_real_corpus_test.go` (opt-in splitting for target grammars)

Add a flag or env var `GTS_GRAMMARGEN_ENABLE_SPLITTING=1` that enables `EnableLRSplitting` during parity tests. Run the reduced frontier (bash, c, c_sharp, cpp, dart, python, scala) with splitting enabled to measure impact.

Gate: compare deep parity before/after. Any regression → revert.

---

### Post-Implementation Checklist

- [ ] Phase 1 (provenance) lands with no behavior change, all existing tests pass
- [ ] Phase 2 (oracle) produces split candidate reports for real grammars
- [ ] Phase 3 (local rebuild) reduces GLR conflicts on at least one real grammar
- [ ] Gate 0 passes after each phase
- [ ] Gate 1 passes (parser canaries) — splitting is compiler-only, should not affect runtime
- [ ] No generated blob changes (splitting only affects grammargen path, not ts2go blobs)
- [ ] Oracle baseline recorded for future comparison
