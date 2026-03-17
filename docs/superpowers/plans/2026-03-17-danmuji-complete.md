# Danmuji Complete Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete danmuji — a BDD testing DSL for Go that compiles to standard Go tests backed by testify, testcontainers-go, vegeta, and runtime/pprof.

**Architecture:** `.dmj` files extend Go syntax via grammargen's `ExtendGrammar`. The `danmuji` CLI transpiles them to `_danmuji_test.go` files that `go test` runs natively. No runtime library — the generated code uses only standard Go packages and three opinionated dependencies (testify, testcontainers-go, vegeta). Existing grammar (22 rules) and transpiler (10 handlers) are working with 13/13 tests passing.

**Tech Stack:** grammargen (grammar + parser), testify (assertions), testcontainers-go (infrastructure), vegeta (load testing), runtime/pprof (profiling), os/exec (shell integration)

**Worktree:** `/home/draco/work/gotreesitter-danmuji` on branch `danmuji`

**Existing code:** `grammargen/danmuji_grammar.go` (22 rules), `grammargen/transpile_danmuji.go` (10 handlers), 13/13 tests passing

**Review fixes (v2):** Addresses 3 critical + 9 important issues from code review:
- **CRITICAL:** Remove `benchmark` from `test_category` — use `benchmark_block` exclusively (avoids grammar ambiguity)
- **CRITICAL:** Add `testVar string` field to transpiler — benchmarks use `b`, tests use `t`, propagates through all emit methods
- **CRITICAL:** Cache generated language at package level — `TranspileDanmuji` regenerates on every call (1.5-3s); use `sync.Once`
- **IMPORTANT:** All new block rules use `Sym("block")` body (not raw `Str("{")` / `Str("}")`), consistent with `test_block`
- **IMPORTANT:** `profile_type` uses `blockprofile` and `mutexprofile` (avoids collision with Go's `block` keyword)
- **IMPORTANT:** Add missing `AddConflict` for `spy_declaration`
- **IMPORTANT:** Generic `injectImport` mechanism replaces per-library injection
- **IMPORTANT:** `run_command` simplified to single string (no trailing Repeat)
- **IMPORTANT:** `target_block` body uses structured config rules (not Go block)
- **IMPORTANT:** `emitFake` uses `collectTopLevel` like `emitMock` for package-level emission
- **IMPORTANT:** Task 5 `go mod tidy` runs in correct directory
- **IMPORTANT:** Tasks 1-6 execute sequentially (all modify same files)

---

## What needs to be built

| Feature | Grammar rules | Transpiler handler | Status |
|---------|--------------|-------------------|--------|
| benchmark + measure | ~6 new rules | emitBenchmark | **not started** |
| load + vegeta | ~7 new rules | emitLoad | **not started** |
| profile + pprof | ~5 new rules | emitProfile | **not started** |
| exec blocks | ~3 new rules | emitExec | **not started** |
| fake (test double) | rules exist | emitFake | **grammar only** |
| spy (test double) | rules exist | emitSpy | **grammar only** |
| table-driven tests | rules exist | emitTable | **grammar only** |
| CLI (danmuji build) | n/a | n/a | **not started** |

## File Structure

```
grammargen/
  danmuji_grammar.go          — MODIFY: add ~21 new grammar rules
  danmuji_grammar_test.go     — MODIFY: add grammar parse tests for new rules
  transpile_danmuji.go        — MODIFY: add 7 new emit handlers
  transpile_danmuji_test.go   — MODIFY: add transpiler tests for new features
  testdata/
    user_service.dmj           — existing
    benchmark.dmj              — CREATE: benchmark test fixture
    load.dmj                   — CREATE: load test fixture
    profile.dmj                — CREATE: profile test fixture
    exec.dmj                   — CREATE: exec test fixture
    full_stack.dmj             — CREATE: integration test using all features

cmd/danmuji/
  main.go                      — CREATE: CLI entry point
  main_test.go                 — CREATE: CLI integration test
```

---

## Task 0: Prerequisite Fixes (Critical)

Fix three critical issues in existing code before adding new features.

**Files:**
- Modify: `grammargen/danmuji_grammar.go`
- Modify: `grammargen/transpile_danmuji.go`

- [ ] **Step 1: Remove `benchmark` from `test_category`**

In `danmuji_grammar.go`, change `test_category` to remove `Str("benchmark")`:
```go
g.Define("test_category",
    Choice(
        Str("unit"),
        Str("integration"),
        Str("e2e"),
    ))
```

This prevents ambiguity when `benchmark_block` is added in Task 1.

- [ ] **Step 2: Add `testVar` field to transpiler**

In `transpile_danmuji.go`, add a field to `dmjTranspiler`:
```go
type dmjTranspiler struct {
    src                 []byte
    lang                *gotreesitter.Language
    testVar             string // "t" for tests, "b" for benchmarks
    mockDecls           []string
    collectedMockStarts map[uint32]bool
    usesAssert          bool
    usesRequire         bool
}
```

Initialize it in `TranspileDanmuji`: `testVar: "t"`.

Update ALL emit methods that hardcode `"t"` to use `t.testVar` instead:
- `emitBDDBlock`: `fmt.Fprintf(&b, "%s.Run(...)` → `fmt.Fprintf(&b, "%s.Run(...)` using `t.testVar`
- `emitExpect`: all `assert.X(t, ...)` → `assert.X(%s, ...)` with `t.testVar`
- `emitReject`: same
- `emitVerify`: same
- `emitLifecycleHook`: `t.Cleanup` → `%s.Cleanup`
- `emitNeedsBlock`: all `require.X(t, ...)` → `require.X(%s, ...)`

- [ ] **Step 3: Cache generated language with sync.Once**

In `transpile_danmuji.go`, add package-level cache:
```go
var (
    danmujiLangOnce sync.Once
    danmujiLangCached *gotreesitter.Language
    danmujiLangErr error
)

func getDanmujiLanguage() (*gotreesitter.Language, error) {
    danmujiLangOnce.Do(func() {
        danmujiLangCached, danmujiLangErr = GenerateLanguage(DanmujiGrammar())
    })
    return danmujiLangCached, danmujiLangErr
}
```

Update `TranspileDanmuji` to call `getDanmujiLanguage()` instead of `GenerateLanguage(DanmujiGrammar())`.

- [ ] **Step 4: Add missing `AddConflict` for `spy_declaration`**

In `danmuji_grammar.go`, add:
```go
AddConflict(g, "_statement", "spy_declaration")
```

- [ ] **Step 5: Add generic import injection**

In `transpile_danmuji.go`, replace `injectTestifyImports` with a generic mechanism:
```go
type importSet struct {
    pkgs []string // e.g. "github.com/stretchr/testify/assert"
}

func (t *dmjTranspiler) addImport(pkg string) {
    // track in a map on the transpiler
}

func (t *dmjTranspiler) injectImports(code string) string {
    // inject all collected imports into the import block
}
```

Each emit handler calls `t.addImport("...")` for the packages it needs. After full emission, `injectImports` is called once.

- [ ] **Step 6: Run ALL existing tests**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run 'TestDanmuji|TestTranspile' -v -timeout 180s ./grammargen/`
Expected: All 13 tests PASS (removing `benchmark` from `test_category` should not break existing tests since no test uses `benchmark` as a `test_category` directly)

- [ ] **Step 7: Commit**

```bash
cd /home/draco/work/gotreesitter-danmuji && buckley commit --yes -min -graft
```

---

## Task 1: Benchmark Grammar Rules

Add grammar rules for `benchmark_block`, `measure`, `parallel measure`, `setup`, `report allocs`, and benchmark performance assertions (`ns_per_op`, `allocs_per_op`, `bytes_per_op`). Uses `Sym("block")` for body (consistent with `test_block`).

**Files:**
- Modify: `grammargen/danmuji_grammar.go`
- Modify: `grammargen/danmuji_grammar_test.go`

- [ ] **Step 1: Write the failing grammar test**

```go
func TestDanmujiBenchmark(t *testing.T) {
	input := `package main
benchmark "JSON marshal" {
	setup {
		data := makeLargeStruct()
	}
	measure {
		json.Marshal(data)
	}
	report allocs
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "benchmark_block") {
		t.Error("expected benchmark_block node")
	}
	if !strings.Contains(sexp, "measure_block") {
		t.Error("expected measure_block node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}

func TestDanmujiParallelBenchmark(t *testing.T) {
	input := `package main
benchmark "concurrent reads" {
	setup {
		cache := NewCache()
	}
	parallel measure {
		cache.Get("key")
	}
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "parallel_measure_block") {
		t.Error("expected parallel_measure_block node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run TestDanmujiBenchmark -v -timeout 60s ./grammargen/`
Expected: FAIL — `benchmark_block` and `measure_block` not found in parse tree

- [ ] **Step 3: Add grammar rules**

Add these rules to `danmuji_grammar.go` BEFORE the "Wire into Go" section:

```go
// ---------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------
g.Define("setup_block", Seq(Str("setup"), Sym("block")))

g.Define("measure_block", Seq(Str("measure"), Sym("block")))

g.Define("parallel_measure_block", Seq(
    Str("parallel"), Str("measure"), Sym("block"),
))

g.Define("report_directive", Seq(Str("report"), Str("allocs")))

g.Define("benchmark_block", Seq(
    Optional(Field("tags", Sym("tag_list"))),
    Str("benchmark"),
    Field("name", Sym("_string_literal")),
    Field("body", Sym("block")),
))
```

Wire into top-level:
```go
AppendChoice(g, "_top_level_declaration", Sym("benchmark_block"))
```

Add conflicts:
```go
AddConflict(g, "_statement", "setup_block")
AddConflict(g, "_statement", "measure_block")
AddConflict(g, "_statement", "report_directive")
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run 'TestDanmujiBenchmark|TestDanmujiParallel' -v -timeout 120s ./grammargen/`
Expected: PASS

- [ ] **Step 5: Run ALL existing tests to verify no regressions**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run 'TestDanmuji|TestTranspile' -v -timeout 180s ./grammargen/`
Expected: All 13+ tests PASS

- [ ] **Step 6: Commit**

```bash
cd /home/draco/work/gotreesitter-danmuji && buckley commit --yes -min -graft
```

---

## Task 2: Benchmark Transpiler

Emit `testing.B` code from benchmark_block nodes. Handle `setup`, `measure`, `parallel measure`, `report allocs`, and performance assertions.

**Files:**
- Modify: `grammargen/transpile_danmuji.go`
- Modify: `grammargen/transpile_danmuji_test.go`
- Create: `grammargen/testdata/benchmark.dmj`

- [ ] **Step 1: Write test fixture**

Create `grammargen/testdata/benchmark.dmj`:
```
package bench_test

import "testing"

benchmark "string concat" {
	setup {
		parts := []string{"hello", " ", "world"}
	}
	measure {
		result := ""
		for _, p := range parts {
			result += p
		}
		_ = result
	}
	report allocs
}
```

- [ ] **Step 2: Write the failing transpiler test**

```go
func TestTranspileDanmujiBenchmark(t *testing.T) {
	source, err := os.ReadFile("testdata/benchmark.dmj")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "func BenchmarkStringConcat(b *testing.B)") {
		t.Error("expected BenchmarkStringConcat function")
	}
	if !strings.Contains(goCode, "b.ReportAllocs()") {
		t.Error("expected b.ReportAllocs()")
	}
	if !strings.Contains(goCode, "b.ResetTimer()") {
		t.Error("expected b.ResetTimer()")
	}
	if !strings.Contains(goCode, "for i := 0; i < b.N; i++") {
		t.Error("expected b.N loop")
	}
}

func TestTranspileDanmujiBenchmarkCompile(t *testing.T) {
	source := []byte(`package bench_test

import "testing"

benchmark "addition" {
	measure {
		x := 1 + 2
		_ = x
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "bench_test.go"), []byte(goCode), 0644)

	cmd := exec.Command("go", "test", "-bench=.", "-benchtime=1x", "-v", "./...")
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", string(out))
	if err != nil {
		t.Fatalf("go test failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "BenchmarkAddition") {
		t.Error("expected BenchmarkAddition in output")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run TestTranspileDanmujiBenchmark -v -timeout 120s ./grammargen/`
Expected: FAIL — benchmark_block not handled by transpiler

- [ ] **Step 4: Implement emitBenchmark**

Add to `transpile_danmuji.go`:

1. Add `"benchmark_block"` case to the `emit()` dispatcher.

2. Implement `emitBenchmark(n)`:
   - **Set `t.testVar = "b"`** before processing children, restore after.
   - Extract name from field "name"
   - Walk the body block's children for `setup_block`, `measure_block`, `parallel_measure_block`, `report_directive`, `then_block`, `profile_block` (body is `Sym("block")` so children are in a `statement_list`)
   - Emit `func BenchmarkXxx(b *testing.B) {`
   - Emit setup code (before ResetTimer)
   - If `report_directive` present: emit `b.ReportAllocs()`
   - Emit `b.ResetTimer()`
   - If `parallel_measure_block`: emit `b.RunParallel(func(pb *testing.PB) { for pb.Next() { ...body... } })`
   - If `measure_block`: emit `for i := 0; i < b.N; i++ { ...body... }`
   - If `then_block`s present: emit a `testing.Benchmark()` call that re-runs the benchmark, then assert on `result.NsPerOp()`, `result.AllocsPerOp()`, `result.AllocedBytesPerOp()`. Note: `then` blocks inside benchmark use `t.testVar` which is `"b"`.
   - Emit `}`

3. Handle benchmark-specific assertion keywords in `emitExpect`:
   - `ns_per_op` → `result.NsPerOp()`
   - `allocs_per_op` → `result.AllocsPerOp()`
   - `bytes_per_op` → `result.AllocedBytesPerOp()`

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run TestTranspileDanmujiBenchmark -v -timeout 120s ./grammargen/`
Expected: PASS

- [ ] **Step 6: Run ALL tests**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run 'TestDanmuji|TestTranspile' -v -timeout 180s ./grammargen/`
Expected: All tests PASS

- [ ] **Step 7: Commit**

```bash
cd /home/draco/work/gotreesitter-danmuji && buckley commit --yes -min -graft
```

---

## Task 3: Load Test Grammar + Transpiler (Vegeta)

Add grammar rules for `load` blocks with `rate`, `duration`, `target`, and threshold assertions. Transpile to vegeta library calls.

**Files:**
- Modify: `grammargen/danmuji_grammar.go`
- Modify: `grammargen/danmuji_grammar_test.go`
- Modify: `grammargen/transpile_danmuji.go`
- Modify: `grammargen/transpile_danmuji_test.go`
- Create: `grammargen/testdata/load.dmj`

- [ ] **Step 1: Write grammar test**

```go
func TestDanmujiLoadBlock(t *testing.T) {
	input := `package main
load "checkout" {
	rate 50
	duration 2
	target post "http://localhost:8080/api" {
		body json { }
	}
	then "fast" {
		expect true
	}
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "load_block") {
		t.Error("expected load_block node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run TestDanmujiLoadBlock -v -timeout 60s ./grammargen/`
Expected: FAIL

- [ ] **Step 3: Add grammar rules**

```go
// ---------------------------------------------------------------
// Load testing (vegeta)
// ---------------------------------------------------------------
g.Define("load_config", Choice(
    Seq(Str("rate"), Sym("_expression")),
    Seq(Str("duration"), Sym("_expression")),
    Seq(Str("rampup"), Sym("_expression")),
    Seq(Str("concurrency"), Sym("_expression")),
))

g.Define("http_method", Choice(
    Str("get"), Str("post"), Str("put"), Str("delete"), Str("patch"),
))

g.Define("target_block", Seq(
    Str("target"),
    Field("method", Sym("http_method")),
    Field("url", Sym("_string_literal")),
))

g.Define("load_block", Seq(
    Optional(Field("tags", Sym("tag_list"))),
    Str("load"),
    Field("name", Sym("_string_literal")),
    Field("body", Sym("block")),
))
```

Wire in: `AppendChoice(g, "_top_level_declaration", Sym("load_block"))`

- [ ] **Step 4: Run grammar test**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run TestDanmujiLoadBlock -v -timeout 120s ./grammargen/`
Expected: PASS

- [ ] **Step 5: Write transpiler test**

```go
func TestTranspileDanmujiLoad(t *testing.T) {
	source := []byte(`package load_test

import "testing"

load "api throughput" {
	rate 10
	duration 5
	target get "http://localhost:8080/health" {}
	then "healthy" {
		expect true
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "vegeta.Rate") {
		t.Error("expected vegeta.Rate in output")
	}
	if !strings.Contains(goCode, "vegeta.NewAttacker") {
		t.Error("expected vegeta.NewAttacker in output")
	}
	if !strings.Contains(goCode, "func TestLoadApiThroughput") {
		t.Error("expected TestLoadApiThroughput function")
	}
}
```

- [ ] **Step 6: Implement emitLoad**

Add to `transpile_danmuji.go`:

1. Add `"load_block"` to emit() dispatcher.
2. Implement `emitLoad(n)`:
   - Extract name, rate, duration from children
   - Extract target method+URL
   - Emit:
     ```go
     //go:build e2e
     func TestLoadXxx(t *testing.T) {
         rate := vegeta.Rate{Freq: <rate>, Per: time.Second}
         duration := <duration> * time.Second
         targeter := vegeta.NewStaticTargeter(vegeta.Target{
             Method: "<METHOD>",
             URL:    "<url>",
         })
         attacker := vegeta.NewAttacker()
         var metrics vegeta.Metrics
         for res := range attacker.Attack(targeter, rate, duration, "<name>") {
             metrics.Add(res)
         }
         metrics.Close()
         // then blocks follow as t.Run assertions on metrics
     }
     ```
3. Add vegeta import injection (similar to testify injection).

- [ ] **Step 7: Run tests**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run TestTranspileDanmujiLoad -v -timeout 120s ./grammargen/`
Expected: PASS

- [ ] **Step 8: Run ALL tests**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run 'TestDanmuji|TestTranspile' -v -timeout 180s ./grammargen/`
Expected: All PASS

- [ ] **Step 9: Commit**

```bash
cd /home/draco/work/gotreesitter-danmuji && buckley commit --yes -min -graft
```

---

## Task 4: Profile Grammar + Transpiler (pprof)

Add `profile` directive that captures CPU/mem/goroutine profiles inline with tests and benchmarks. Display results via `go tool pprof -text`.

**Files:**
- Modify: `grammargen/danmuji_grammar.go`
- Modify: `grammargen/danmuji_grammar_test.go`
- Modify: `grammargen/transpile_danmuji.go`
- Modify: `grammargen/transpile_danmuji_test.go`

- [ ] **Step 1: Write grammar test**

```go
func TestDanmujiProfileBlock(t *testing.T) {
	input := `package main
func f() {
	profile goroutines {
		show top 10
	}
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "profile_block") {
		t.Error("expected profile_block node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run TestDanmujiProfileBlock -v -timeout 60s ./grammargen/`

- [ ] **Step 3: Add grammar rules**

```go
// ---------------------------------------------------------------
// Profiling
// ---------------------------------------------------------------
g.Define("profile_type", Choice(
    Str("cpu"), Str("mem"), Str("allocs"),
    Str("goroutines"), Str("blockprofile"), Str("mutexprofile"),
))

g.Define("profile_directive", Choice(
    Seq(Str("show"), Str("top"), Sym("int_literal")),
    Seq(Str("save"), Sym("_string_literal")),
))

g.Define("profile_block", Seq(
    Str("profile"),
    Field("type", Sym("profile_type")),
    Optional(Sym("block")),
))
```

Wire into `_statement`:
```go
AppendChoice(g, "_statement", Sym("profile_block"))
AddConflict(g, "_statement", "profile_block")
```

- [ ] **Step 4: Run grammar test**

Expected: PASS

- [ ] **Step 5: Write transpiler test**

```go
func TestTranspileDanmujiProfile(t *testing.T) {
	source := []byte(`package prof_test

import "testing"

unit "goroutine safety" {
	profile goroutines {}
	given "a pool" {
		x := 1
		_ = x
		then "no leaks" {
			expect true
		}
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "runtime.NumGoroutine") {
		t.Error("expected goroutine tracking")
	}
}
```

- [ ] **Step 6: Implement emitProfile**

Add `"profile_block"` to emit() dispatcher. Implement `emitProfile(n)`:

- `goroutines` → `goroutinesBefore := runtime.NumGoroutine()` at capture point; expose `goroutine_delta` for assertions
- `cpu` → `pprof.StartCPUProfile(f)` / `StopCPUProfile()` + `go tool pprof -text -top -nodecount=N`
- `mem` → `runtime.GC(); pprof.WriteHeapProfile(f)` + `go tool pprof -text`
- `block` → `runtime.SetBlockProfileRate(1)` + `pprof.Lookup("block")`
- `mutex` → `runtime.SetMutexProfileFraction(1)` + `pprof.Lookup("mutex")`
- `show top N` → shell out to `go tool pprof -text -nodecount=N` and `t.Log()` the output
- `save "path"` → write .prof file to path

- [ ] **Step 7: Run tests**

Expected: PASS

- [ ] **Step 8: Commit**

```bash
cd /home/draco/work/gotreesitter-danmuji && buckley commit --yes -min -graft
```

---

## Task 5: Exec Block Grammar + Transpiler

Add `exec` blocks for shell command execution with assertable exit code, stdout, stderr, and duration.

**Files:**
- Modify: `grammargen/danmuji_grammar.go`
- Modify: `grammargen/danmuji_grammar_test.go`
- Modify: `grammargen/transpile_danmuji.go`
- Modify: `grammargen/transpile_danmuji_test.go`

- [ ] **Step 1: Write grammar test**

```go
func TestDanmujiExecBlock(t *testing.T) {
	input := `package main
func f() {
	exec "run migrations" {
		run "echo hello"
		expect exit_code == 0
		expect stdout contains "hello"
	}
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "exec_block") {
		t.Error("expected exec_block node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

- [ ] **Step 3: Add grammar rules**

```go
// ---------------------------------------------------------------
// Exec blocks (shell commands)
// ---------------------------------------------------------------
g.Define("run_command", Seq(
    Str("run"),
    Field("command", Sym("_string_literal")),
))

g.Define("exec_block", Seq(
    Str("exec"),
    Field("name", Sym("_string_literal")),
    Field("body", Sym("block")),
))
```

Wire into `_statement`:
```go
AppendChoice(g, "_statement", Sym("exec_block"), Sym("run_command"))
AddConflict(g, "_statement", "exec_block")
AddConflict(g, "_statement", "run_command")
```

- [ ] **Step 4: Run grammar test**

Expected: PASS

- [ ] **Step 5: Write transpiler test**

```go
func TestTranspileDanmujiExec(t *testing.T) {
	source := []byte(`package exec_test

import "testing"

unit "shell commands" {
	exec "echo test" {
		run "echo hello"
		expect exit_code == 0
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "exec.Command") {
		t.Error("expected exec.Command in output")
	}
}

func TestTranspileDanmujiExecCompile(t *testing.T) {
	source := []byte(`package exec_test

import "testing"

unit "echo" {
	exec "hello" {
		run "echo hello"
		expect exit_code == 0
		expect stdout contains "hello"
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "exec_test.go"), []byte(goCode), 0644)

	getCmd := exec.Command("go", "get", "github.com/stretchr/testify@latest")
	getCmd.Dir = tmpDir
	getCmd.CombinedOutput()
	tidyCmd := exec.Command("go", "mod", "tidy")
	tidyCmd.Dir = tmpDir
	tidyCmd.Run()

	cmd = exec.Command("go", "test", "-v", "./...")
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", string(out))
	if err != nil {
		t.Fatalf("go test failed: %v\n%s", err, out)
	}
}
```

- [ ] **Step 6: Implement emitExec**

Add `"exec_block"` to emit() dispatcher. Implement `emitExec(n)`:

1. Extract name and run commands from children.
2. For each `run_command`, emit:
   ```go
   t.Run("<name>", func(t *testing.T) {
       var stdout, stderr bytes.Buffer
       cmd := exec.Command("sh", "-c", <command>)
       cmd.Stdout = &stdout
       cmd.Stderr = &stderr
       startTime := time.Now()
       err := cmd.Run()
       execDuration := time.Since(startTime)
       exitCode := 0
       if err != nil {
           if exitErr, ok := err.(*exec.ExitError); ok {
               exitCode = exitErr.ExitCode()
           } else {
               exitCode = -1
           }
       }
       _, _ = execDuration, exitCode // suppress unused if no assertions
       // ... expect statements follow using exitCode, stdout.String(), stderr.String()
   })
   ```
3. Handle special assertion identifiers inside exec blocks:
   - `exit_code` → the `exitCode` variable
   - `stdout` → `stdout.String()`
   - `stderr` → `stderr.String()`
   - `duration` → `execDuration`
   - `stdout contains "x"` → `assert.Contains(t, stdout.String(), "x")`
   - `stderr not_contains "ERROR"` → `assert.NotContains(t, stderr.String(), "ERROR")`

4. Add imports: `"bytes"`, `"os/exec"`, `"time"` (inject similar to testify imports).

- [ ] **Step 7: Run tests**

Expected: PASS

- [ ] **Step 8: Run ALL tests**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run 'TestDanmuji|TestTranspile' -v -timeout 180s ./grammargen/`
Expected: All PASS

- [ ] **Step 9: Commit**

```bash
cd /home/draco/work/gotreesitter-danmuji && buckley commit --yes -min -graft
```

---

## Task 6: Fake + Spy + Table Transpiler Handlers

Complete the remaining test double transpilers and table-driven test generation. Grammar rules already exist — only transpiler handlers are missing.

**Files:**
- Modify: `grammargen/transpile_danmuji.go`
- Modify: `grammargen/transpile_danmuji_test.go`

- [ ] **Step 1: Write fake transpiler test**

```go
func TestTranspileDanmujiFake(t *testing.T) {
	source := []byte(`package fake_test

import "testing"

unit "with fake" {
	fake InMemoryStore {
		Get(key string) -> string {
			return "value"
		}
	}
	store := &fakeInMemoryStore{}
	then "returns value" {
		expect store.Get("k") == "value"
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "type fakeInMemoryStore struct") {
		t.Error("expected fakeInMemoryStore struct")
	}
}
```

- [ ] **Step 2: Write table-driven test transpiler test**

```go
func TestTranspileDanmujiTable(t *testing.T) {
	source := []byte(`package table_test

import "testing"

unit "table driven" {
	table cases {
		| 1 | 2 | 3 |
		| 4 | 5 | 9 |
	}
	each row in cases {
		then "adds up" {
			expect true
		}
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "for _, row := range") {
		t.Error("expected table iteration")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Expected: FAIL — fake and table handlers not implemented

- [ ] **Step 4: Implement emitFake**

Add `"fake_declaration"` to emit() dispatcher. Similar to mock but with real method bodies:

```go
func (t *dmjTranspiler) emitFake(n *gotreesitter.Node) string {
    // Extract name
    // Walk block for fake_method nodes
    // For each fake_method: emit method with the user-provided body
    // type fakeXxx struct {}
    // func (f *fakeXxx) Method(params) returnType { ...user body... }
}
```

- [ ] **Step 5: Implement emitTable**

Add `"table_declaration"` and `"each_row_block"` to emit() dispatcher:

- `table_declaration`: parse rows into `[][]interface{}` literal
- `each_row_block`: emit `for _, row := range <tableName> { t.Run(...) { ...body... } }`
- First row is headers (field names), remaining rows are data

- [ ] **Step 6: Run tests**

Expected: PASS

- [ ] **Step 7: Run ALL tests**

Expected: All PASS

- [ ] **Step 8: Commit**

```bash
cd /home/draco/work/gotreesitter-danmuji && buckley commit --yes -min -graft
```

---

## Task 7: Danmuji CLI

Build the `danmuji` command-line tool that transpiles `.dmj` files to `_danmuji_test.go` files.

**Files:**
- Create: `cmd/danmuji/main.go`
- Create: `cmd/danmuji/main_test.go`

- [ ] **Step 1: Write the CLI test**

```go
func TestDanmujiCLIBuild(t *testing.T) {
	// Create a temp dir with a .dmj file
	tmpDir := t.TempDir()
	dmjSource := `package example_test

import "testing"

unit "hello" {
	then "works" {
		expect 1 == 1
	}
}
`
	os.WriteFile(filepath.Join(tmpDir, "example_test.dmj"), []byte(dmjSource), 0644)
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example\n\ngo 1.21\n"), 0644)

	// Build the CLI
	cliBin := filepath.Join(t.TempDir(), "danmuji")
	cmd := exec.Command("go", "build", "-o", cliBin, "./cmd/danmuji")
	cmd.Dir = "/home/draco/work/gotreesitter-danmuji"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	// Run danmuji build
	cmd = exec.Command(cliBin, "build", tmpDir)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("danmuji build: %v\n%s", err, out)
	}

	// Check output file exists
	outputFile := filepath.Join(tmpDir, "example_danmuji_test.go")
	if _, err := os.Stat(outputFile); err != nil {
		t.Fatalf("output file not found: %v", err)
	}

	// Verify it has the generated header
	content, _ := os.ReadFile(outputFile)
	if !strings.Contains(string(content), "Code generated by danmuji") {
		t.Error("expected generated header")
	}
}
```

- [ ] **Step 2: Write the CLI**

```go
// cmd/danmuji/main.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/odvcencio/gotreesitter/grammargen"
)

const generatedHeader = "// Code generated by danmuji — DO NOT EDIT.\n\n"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: danmuji build <path>\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "build":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: danmuji build <path>\n")
			os.Exit(1)
		}
		if err := build(os.Args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\nUsage: danmuji build <path>\n", os.Args[1])
		os.Exit(1)
	}
}

func build(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return buildDir(path)
	}
	return buildFile(path)
}

func buildDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".dmj") {
			continue
		}
		if err := buildFile(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
		count++
	}
	fmt.Printf("danmuji: transpiled %d file(s)\n", count)
	return nil
}

func buildFile(path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	goCode, err := grammargen.TranspileDanmuji(source)
	if err != nil {
		return fmt.Errorf("transpile %s: %w", path, err)
	}

	// Output: foo_test.dmj → foo_danmuji_test.go
	base := strings.TrimSuffix(filepath.Base(path), ".dmj")
	outName := base + "_danmuji_test.go"
	// If base already ends with _test, don't double it
	if strings.HasSuffix(base, "_test") {
		outName = strings.TrimSuffix(base, "_test") + "_danmuji_test.go"
	}
	outPath := filepath.Join(filepath.Dir(path), outName)

	output := generatedHeader + goCode
	if err := os.WriteFile(outPath, []byte(output), 0644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	fmt.Printf("  %s → %s\n", filepath.Base(path), outName)
	return nil
}
```

- [ ] **Step 3: Build the CLI**

Run: `cd /home/draco/work/gotreesitter-danmuji && go build ./cmd/danmuji/`
Expected: binary compiles

- [ ] **Step 4: Run the CLI test**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run TestDanmujiCLI -v -timeout 120s ./cmd/danmuji/`
Expected: PASS

- [ ] **Step 5: Smoke test with existing fixture**

```bash
cd /home/draco/work/gotreesitter-danmuji
./danmuji build grammargen/testdata/user_service.dmj
cat grammargen/testdata/user_service_danmuji_test.go
```

Expected: generated file with `func TestUserServiceCreate`, `t.Run` calls, `assert.Equal`

- [ ] **Step 6: Commit**

```bash
cd /home/draco/work/gotreesitter-danmuji && buckley commit --yes -min -graft
```

---

## Task 8: Full Stack Integration Test

End-to-end test using all features together. Validates that a realistic `.dmj` file transpiles, compiles, and runs.

**Files:**
- Create: `grammargen/testdata/full_stack.dmj`
- Modify: `grammargen/transpile_danmuji_test.go`

- [ ] **Step 1: Create full_stack.dmj test fixture**

```
package fullstack_test

import "testing"

mock UserRepo {
	FindByID(id int) -> error = nil
	Save(name string) -> error = nil
}

unit "UserService" {
	before each {
		repo := &mockUserRepo{}
	}

	given "valid input" {
		when "creating a user" {
			err := repo.Save("alice")

			then "succeeds" {
				expect err == nil
			}
			then "calls save" {
				verify repo.Save called 1 times
			}
		}
	}

	given "multiple operations" {
		when "saving twice" {
			repo.Save("alice")
			repo.Save("bob")

			then "called twice" {
				verify repo.Save called 2 times
			}
		}
	}
}

benchmark "string operations" {
	measure {
		s := "hello" + " " + "world"
		_ = s
	}
	report allocs
}
```

- [ ] **Step 2: Write integration test**

```go
func TestTranspileDanmujiFullStack(t *testing.T) {
	source, err := os.ReadFile("testdata/full_stack.dmj")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	// Structural checks
	if !strings.Contains(goCode, "func TestUserService(t *testing.T)") {
		t.Error("expected TestUserService")
	}
	if !strings.Contains(goCode, "func BenchmarkStringOperations(b *testing.B)") {
		t.Error("expected BenchmarkStringOperations")
	}
	if !strings.Contains(goCode, "type mockUserRepo struct") {
		t.Error("expected mockUserRepo struct")
	}
	if !strings.Contains(goCode, "assert.") || !strings.Contains(goCode, "require.") {
		t.Error("expected testify calls")
	}

	// Compile and run
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "full_test.go"), []byte(goCode), 0644)

	cmd := exec.Command("sh", "-c", "cd "+tmpDir+" && go get github.com/stretchr/testify@latest && go mod tidy && go test -v -bench=. -benchtime=1x ./...")
	out, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", string(out))
	if err != nil {
		t.Fatalf("go test failed: %v\n%s", err, out)
	}

	if !strings.Contains(string(out), "PASS") {
		t.Error("expected PASS")
	}
	if !strings.Contains(string(out), "BenchmarkStringOperations") {
		t.Error("expected benchmark output")
	}
}
```

- [ ] **Step 3: Run integration test**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run TestTranspileDanmujiFullStack -v -timeout 180s ./grammargen/`
Expected: PASS

- [ ] **Step 4: Run ALL tests — final verification**

Run: `cd /home/draco/work/gotreesitter-danmuji && go test -run 'TestDanmuji|TestTranspile' -v -timeout 300s ./grammargen/`
Expected: All tests PASS (should be ~20+ tests)

- [ ] **Step 5: Commit**

```bash
cd /home/draco/work/gotreesitter-danmuji && buckley commit --yes -min -graft
```

---

## Execution Notes

- **All work happens in** `/home/draco/work/gotreesitter-danmuji` on branch `danmuji`.
- **Run tests directly on host** — no Docker needed.
- **Use `tee` or Go programs for file writes** — the `Write` tool has path resolution issues in worktrees. Always `cd /home/draco/work/gotreesitter-danmuji` first.
- **Grammar generation is slow** (~1.5-3s per DanmujiGrammar() call) — after Task 0, the language is cached via `sync.Once`. Test suite also caches via `getDanmujiLang(t)`. All new tests should use this pattern.
- **Testify, testcontainers-go, vegeta are NOT dependencies of gotreesitter** — they're dependencies of the GENERATED code. The transpiler only emits import statements referencing them. The compile-and-run tests need `go get` in the temp dir.
- **Commit with `buckley commit --yes -min -graft`** — never `git commit` directly.
- **Do not commit plan documents.**
- **Tasks 0-6 execute sequentially** — they all modify the same 4 files. Task 7 (CLI) depends on the transpiler being complete. Task 8 depends on everything.
