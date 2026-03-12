# L4 Runtime Parity & Semantic Mismatch Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clear all 16 L4 blockers (8 runtime, 8 parity mismatches) to achieve full L3/L4 real-corpus parity across the top-50 languages.

**Architecture:** Three-phase attack: (1) re-probe ambiguous files to separate infrastructure noise from real parser failures, (2) fix confirmed OOM/timeout runtime blockers via arena memory budgeting, (3) fix semantic parity mismatches via targeted parser normalizations.

**Tech Stack:** Go, Docker (8g containers), CGo parity harness, tree-sitter C oracle

---

## Phase 1: Re-probe Ambiguous Runtime Suspects

These 5 files were manually killed during a prior Docker sweep. We don't know if they're parser failures or probe-management artifacts. Re-run each in isolation with higher memory and no manual intervention.

### Task 1: Build isolated re-probe script

**Files:**
- Create: `cgo_harness/docker/run_single_file_probe.sh`

The existing `run_parity_in_docker.sh` runs the full test suite. We need a wrapper that probes a single language+file pair with configurable memory and timeout, captures OOM status, peak RSS, and elapsed time.

- [ ] **Step 1: Write the probe script**

```bash
#!/usr/bin/env bash
set -euo pipefail
# Usage: run_single_file_probe.sh <language> <file_id> [--memory 12g] [--timeout 300]
#
# Runs corpus_parity for a single file inside Docker.
# Outputs: exit_code, oom_killed, peak_rss_kb, elapsed_s, stderr snippet.
```

The script should:
- Accept `--language <lang>` and `--file-id <id>` args
- Accept `--memory <limit>` (default 12g — higher than the 8g that killed prior probes)
- Accept `--timeout <seconds>` (default 300)
- Use the existing Docker image (`gotreesitter/cgo-harness:go1.24-local`)
- Run `corpus_parity` with `--lang <lang>` filtering to the single file
- Capture: exit code, OOM killed status, peak RSS from `/usr/bin/time -v`, wall clock time
- Write a single-line JSON summary to stdout

- [ ] **Step 2: Test the script on a known-passing file**

```bash
bash cgo_harness/docker/run_single_file_probe.sh \
  --language go --file-id small__hello.go --memory 12g --timeout 120
```

Expected: exit 0, oom_killed=false, pass=true.

- [ ] **Step 3: Probe all 5 ambiguous files**

Run each in sequence. Record results.

```bash
for spec in \
  "css large__atom.io.css" \
  "css large__github.com.css" \
  "scss large__github.com.scss" \
  "rust large__ast.rs" \
  "tsx medium__RangeSlider.tsx"; do
  lang="${spec%% *}"
  file="${spec#* }"
  bash cgo_harness/docker/run_single_file_probe.sh \
    --language "$lang" --file-id "$file" --memory 12g --timeout 300
done
```

- [ ] **Step 4: Classify results**

For each file, one of:
- **PASS**: Parity matches. Remove from blocker list. Done.
- **OOM**: Real memory blowup. Move to Phase 2 (runtime fix).
- **TIMEOUT**: Parser runs too long. Move to Phase 2 (investigate GLR explosion).
- **PARITY MISMATCH**: Parser completes but diverges. Move to Phase 3 (semantic fix).
- **C ORACLE ERROR**: C parser also fails. Move to exclusion list. Done.

- [ ] **Step 5: Update the board tracking**

Record the re-probe results in `harness_out/` and update any skip/exclusion lists.

---

## Phase 2: Fix Confirmed OOM Runtime Blockers

### Current understanding

The 3 confirmed OOM files (d, javascript, typescript) and any newly-confirmed from Phase 1 share a pattern: modest input size (10-40KB) causes catastrophic memory growth (8GB+).

**Root cause hypothesis:** The node count limit (`max(300K, sourceLen*52)`) caps Node struct allocations, but does NOT cap:
- `childSlabs` — `[]*Node` slices, each slab is 64K pointers = 512KB per slab on 64-bit
- `fieldSlabs` — `[]FieldID` slices, each slab is 64K × 2 bytes = 128KB per slab
- `fieldSources` — `[]uint8` slices allocated per-node via `make()`
- New node slabs that grow unbounded when `allocNodeSlow()` appends

Each reduce action that builds a parent node allocates children and field slices from these slabs. For grammars with high ambiguity (D generics, JS nested expressions, TS type declarations), the GLR parser forks and reduces prolifically, creating many intermediate nodes that each claim slab space.

The node count check at `parser.go:1152` catches the node count, but by the time it fires, the child/field slabs may have already consumed gigabytes.

### Task 2: Add arena memory budget tracking

**Files:**
- Modify: `arena.go` — add byte-level budget tracking
- Modify: `parser_limits.go` — add `parseMemoryBudget()` function
- Modify: `parser.go:~1152` — check memory budget alongside node count
- Create: `arena_budget_test.go` — test budget enforcement

- [ ] **Step 1: Write the failing test**

```go
// arena_budget_test.go
func TestArenaMemoryBudgetEnforced(t *testing.T) {
    a := newNodeArena(arenaClassFull)
    a.refs.Store(1)
    // Set a tight budget (e.g. 1MB)
    a.setBudget(1 * 1024 * 1024)

    // Allocate nodes until budget is hit
    var count int
    for {
        if a.budgetExhausted() {
            break
        }
        _ = a.allocNode()
        count++
        if count > 10_000_000 {
            t.Fatal("budget never triggered")
        }
    }
    // Should have stopped well before 10M nodes
    if count > 100_000 {
        t.Errorf("allocated %d nodes before budget; expected fewer", count)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -run TestArenaMemoryBudgetEnforced -v ./...
```

Expected: FAIL — `setBudget` and `budgetExhausted` don't exist yet.

- [ ] **Step 3: Implement arena budget tracking**

Add to `nodeArena` struct in `arena.go`:

```go
// Memory budget fields
budgetBytes   int64  // 0 = unlimited
allocatedBytes int64 // running total of all slab allocations
```

Track allocations in:
- `allocNodeSlow()` — when appending a new node slab, add `len(slab.data) * int(unsafe.Sizeof(Node{}))` to `allocatedBytes`
- `allocNodeSlice()` — when appending a new child slab, add `cap * 8` (pointer size) to `allocatedBytes`
- `allocFieldIDSlice()` — when appending a new field slab, add `cap * 2` (FieldID size) to `allocatedBytes`

```go
func (a *nodeArena) setBudget(bytes int64) {
    a.budgetBytes = bytes
}

func (a *nodeArena) budgetExhausted() bool {
    return a.budgetBytes > 0 && a.allocatedBytes >= a.budgetBytes
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -run TestArenaMemoryBudgetEnforced -v ./...
```

- [ ] **Step 5: Commit**

```bash
buckley commit --yes --minimal-output
```

### Task 3: Wire budget check into parser loop

**Files:**
- Modify: `parser_limits.go` — add `parseMemoryBudget(sourceLen)`
- Modify: `parser.go:~1152` — check `arena.budgetExhausted()` alongside nodeCount
- Modify: `tree.go` — add `ParseStopMemoryBudget` stop reason
- Create: `parser_memory_budget_test.go`

- [ ] **Step 1: Write the failing test**

```go
// parser_memory_budget_test.go
func TestParserStopsOnMemoryBudget(t *testing.T) {
    // Use a language with known high node expansion
    // Parse a moderate input with a very tight budget env var
    t.Setenv("GOT_PARSE_MEMORY_BUDGET_MB", "16")
    ResetParseEnvConfigCacheForTests()

    lang := loadTestLanguage(t, "javascript")
    p := NewParser(lang)
    defer p.Close()

    // Generate a deeply nested JS expression that would normally OOM
    src := generateDeepNesting("(", "1", ")", 5000)
    tree := p.Parse([]byte(src), nil)
    defer tree.Release()

    // Parser should stop gracefully, not OOM
    if tree.ParseStopReason() == ParseStopNone {
        // If it completed normally, budget was sufficient — that's fine
        return
    }
    if tree.ParseStopReason() != ParseStopMemoryBudget {
        t.Errorf("expected ParseStopMemoryBudget, got %s", tree.ParseStopReason())
    }
}
```

- [ ] **Step 2: Add `parseMemoryBudget` to parser_limits.go**

```go
func parseMemoryBudget(sourceLen int) int64 {
    // Default: 512MB per parse. Configurable via GOT_PARSE_MEMORY_BUDGET_MB.
    mb := parseMemoryBudgetMB()
    return int64(mb) * 1024 * 1024
}
```

Add env config to `parser_config.go`:

```go
var (
    parseMemoryBudgetOnce sync.Once
    parseMemoryBudgetVal  int
)

func parseMemoryBudgetMB() int {
    parseMemoryBudgetOnce.Do(func() {
        parseMemoryBudgetVal = 512 // default 512MB
        raw := strings.TrimSpace(os.Getenv("GOT_PARSE_MEMORY_BUDGET_MB"))
        if raw == "" {
            return
        }
        n, err := strconv.Atoi(raw)
        if err == nil && n > 0 {
            parseMemoryBudgetVal = n
        }
    })
    return parseMemoryBudgetVal
}
```

- [ ] **Step 3: Add ParseStopMemoryBudget to tree.go**

```go
ParseStopMemoryBudget ParseStopReason = "memory_budget"
```

- [ ] **Step 4: Wire into parser.go parseInternal**

At `parser.go:~1152`, after the existing `nodeCount > maxNodes` check:

```go
if arena.budgetExhausted() {
    return finalize(stacks, ParseStopMemoryBudget)
}
```

Set the budget when acquiring the arena at parse start:

```go
arena.setBudget(parseMemoryBudget(len(source)))
```

- [ ] **Step 5: Run test**

```bash
go test -run TestParserStopsOnMemoryBudget -v ./...
```

- [ ] **Step 6: Run existing parity tests to verify no regressions**

```bash
go test -run 'TestParityFreshParse|TestParityHasNoErrors' -tags treesitter_c_parity -count=1 -v ./cgo_harness/
```

With default 512MB budget, all existing passing files should remain passing.

- [ ] **Step 7: Commit**

```bash
buckley commit --yes --minimal-output
```

### Task 4: Re-probe confirmed OOM files with budget

**Files:**
- No code changes — uses the script from Task 1

- [ ] **Step 1: Probe the 3 confirmed OOM files with 12g memory and budget enabled**

```bash
for spec in \
  "d large__date.d" \
  "javascript large__text-editor-component.js" \
  "typescript large__parser.ts"; do
  lang="${spec%% *}"
  file="${spec#* }"
  bash cgo_harness/docker/run_single_file_probe.sh \
    --language "$lang" --file-id "$file" --memory 12g --timeout 300
done
```

- [ ] **Step 2: Evaluate results**

With the memory budget, the parser should stop gracefully instead of OOMing. Two possible outcomes:

A. **Parser stops at budget, produces truncated tree.** The parity test will show `stop_reason=memory_budget`. This is acceptable for L4 — the parser handles the file without crashing. The parity comparison should treat memory-budget stops the same as node-limit stops (known limitation, not a correctness bug).

B. **Parser completes within budget.** The OOM was caused by something outside the arena (e.g., the C oracle, Go runtime overhead, CGo bridge). In this case, increase Docker memory and investigate the C-side allocation.

- [ ] **Step 3: Update board classification**

Files that now produce a truncated tree with `memory_budget` stop reason should be reclassified from "runtime blocker" to "budget-limited" (analogous to node-limit files). These are not correctness failures.

---

## Phase 3: Fix Semantic Parity Mismatches

These 8 files parse successfully in both Go and C but produce different trees. Each needs investigation and a targeted fix.

### Task 5: Go — large__proc.go (type mismatch)

**Files:**
- Modify: `parser_result.go` — extend `normalizeGoSourceFileRoot()` or add new normalization
- Modify: `grammars/go_lexer.go` — if lexer issue

- [ ] **Step 1: Run parity dump on the file to get exact divergence**

```bash
# In Docker or with CGo build tags:
cd cgo_harness && go test -tags treesitter_c_parity \
  -run TestParityFreshParse -v \
  -count=1 \
  -env GTS_PARITY_FILTER_LANG=go \
  -env GTS_PARITY_FILTER_FILE=large__proc.go
```

Capture the exact `first_divergence` output: which node path, what Go type vs C type.

- [ ] **Step 2: Analyze the divergence**

Read the dump files. Determine whether this is:
- A Go lexer tokenization error (wrong token type produced)
- A parser error recovery difference (Go wraps in ERROR, C doesn't)
- A normalization gap (both parse correctly but represent structure differently)

- [ ] **Step 3: Write a regression test with the minimal reproducing input**

Extract the smallest code snippet from proc.go that triggers the divergence. Add it to the language-specific regression test file.

- [ ] **Step 4: Fix**

Apply the appropriate fix based on Step 2 analysis.

- [ ] **Step 5: Verify parity and no regressions**

- [ ] **Step 6: Commit**

```bash
buckley commit --yes --minimal-output
```

### Task 6: Haskell — medium__BackendType.hs (range mismatch)

**Files:**
- Modify: `parser_result.go` — extend Haskell normalizations

- [ ] **Step 1: Run parity dump to get exact divergence**

The known info: `quasiquote[1]` span differs by 1 byte at start column (49:28 Go vs 49:27 C).

- [ ] **Step 2: Determine if this is a known range tolerance gap**

Check if the existing ±2 byte range tolerance in the comparison logic should catch this. If it should but doesn't, the comparison logic may need adjustment. If it's a real 1-byte span error, trace the quasiquote scanner/lexer behavior.

- [ ] **Step 3: Fix (normalization or lexer correction)**

- [ ] **Step 4: Verify and commit**

```bash
buckley commit --yes --minimal-output
```

### Task 7: Ruby — 3 files (type and range mismatches)

**Files:**
- Modify: `parser_result.go` — add Ruby normalizations
- Potentially modify: `grammars/ruby_scanner.go` if scanner issue

Ruby has no existing normalizations. Three files fail:
- `large__form_helper.rb` — type (ERROR vs program = complete parse failure)
- `medium__lookup_context.rb` — type (ERROR vs program = complete parse failure)
- `small__version_command.rb` — range (module span includes leading blank lines)

- [ ] **Step 1: Triage all 3 with parity dumps**

- [ ] **Step 2: For the 2 type mismatches (ERROR root), investigate parse failure**

These are complete parse failures in gotreesitter. Check:
- Does Ruby use an external scanner? (Yes — check `zzz_scanner_attachments.go`)
- Is the scanner correctly attached?
- What token does the parser fail on? (Check `ParseRuntime` diagnostics)

- [ ] **Step 3: For the range mismatch, check if it's leading whitespace**

The `module[1]` span starts at `0:0` in Go vs `2:0` in C. This suggests gotreesitter includes leading blank lines in the module span that C doesn't. This may be a span attribution issue in `parser_reduce.go` or fixable with a normalization.

- [ ] **Step 4: Fix each issue, with regression tests**

- [ ] **Step 5: Verify and commit**

```bash
buckley commit --yes --minimal-output
```

### Task 8: Scala — 2 files (type and shape mismatches)

**Files:**
- Modify: `parser_result.go` — extend Scala normalizations

- `medium__PathResolver.scala` — type (ERROR vs compilation_unit = parse failure)
- `small__basics.scala` — shape (6 children Go vs 5 children C)

- [ ] **Step 1: Triage with parity dumps**

- [ ] **Step 2: For PathResolver (ERROR root), investigate parse failure**

Scala has existing normalizations for comments and string interpolation. Check if this is a new failure category.

- [ ] **Step 3: For basics.scala (shape), identify the extra child**

Shape mismatch means child count differs at root. One side has an extra node. Dump both trees and diff to find which child is extra/missing.

- [ ] **Step 4: Fix and verify**

- [ ] **Step 5: Commit**

```bash
buckley commit --yes --minimal-output
```

### Task 9: TSX — large__MaterialsInput.tsx (type mismatch)

**Files:**
- Modify: `parser_result.go` — add TSX normalization if needed
- Potentially modify: TSX scanner or token source

- [ ] **Step 1: Triage with parity dump**

Type mismatch = ERROR root = complete parse failure. TSX uses the JavaScript/TypeScript scanner family.

- [ ] **Step 2: Investigate**

Check if this is:
- A JSX parsing issue (template expressions, fragments)
- A TypeScript type annotation issue
- A scanner issue (template literal, automatic semicollon insertion)

- [ ] **Step 3: Fix with regression test**

- [ ] **Step 4: Verify and commit**

```bash
buckley commit --yes --minimal-output
```

---

## Phase Summary

| Phase | Files | Expected Outcome |
|-------|-------|-----------------|
| 1: Re-probe | 5 ambiguous | Classify as pass/OOM/timeout/mismatch |
| 2: Memory budget | 3+ confirmed OOM | Graceful stop instead of crash |
| 3: Parity fixes | 8 semantic | Correct parse trees matching C oracle |

**Success criteria:** L4 board shows 0 runtime blockers and 0 valid-input parity mismatches. Oracle-error exclusions remain excluded.

**Estimated work:** Phase 1 is infrastructure (half day). Phase 2 is the core engineering (1-2 days). Phase 3 is per-language investigation (2-4 days depending on root causes). Total: ~1 week.
