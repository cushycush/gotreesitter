# cgo_harness

This module contains CGo-only parity and baseline benchmark harnesses used to compare `gotreesitter` against native C tree-sitter parsers.

## Unified Harness Gate

From repo root, run the unified gate runner:

```sh
go run ./cmd/harnessgate -mode all
```

This executes:

- root correctness (`go test ./... -count=1`)
- curated cgo parity suites
- stable perf trio (optionally benchgate-compared to a baseline)

Artifacts are written under `harness_out/`.

Framework details (oracles, corpus tiers, gate policy):

- `cgo_harness/HARNESS_FRAMEWORK.md`

## Run Parity Tests

```sh
go test . -tags treesitter_c_parity \
  -run '^TestParityFreshParse$|^TestParityHasNoErrors$|^TestParityIssue3Repros$|^TestParityGLRCanaryGo$' \
  -count=1 -v

go test . -tags treesitter_c_parity \
  -run '^TestParityCorpusFreshParse$' \
  -count=1 -v
```

## Run Corpus Parity (`dump.v1`)

This command compares `gotreesitter` vs the native C oracle, emits `dump.v1`
artifacts for both runtimes, writes JSONL results, and updates `PARITY.md`.

```sh
go run -tags treesitter_c_parity ./cmd/corpus_parity \
  --lang top10 \
  --corpus ./corpus \
  --out ./parity_out/results.jsonl \
  --artifact-dir ./parity_out/dump_v1 \
  --scoreboard ./PARITY.md
```

Notes:

- `--lang` accepts `top10` (default), a single language (`go`), or a comma-separated list.
- For multiple languages, corpus layout is `--corpus/<language>/**`.
- For a single language (`--lang go`), `--corpus` can point directly at that language directory.

## Run C Baseline Benchmarks

```sh
GOMAXPROCS=1 go test . -run '^$' -tags treesitter_c_bench \
  -bench 'BenchmarkCTreeSitterGoParseFull|BenchmarkCTreeSitterGoParseIncrementalSingleByteEdit|BenchmarkCTreeSitterGoParseIncrementalNoEdit' \
  -benchmem -count=10 -benchtime=750ms
```

These harnesses are intentionally split into a separate module so the root `gotreesitter` module remains pure-Go in dependency metadata.

## Run Pure-C Runtime Matrix (No CGo)

This compares against the tree-sitter C runtime compiled directly with `gcc`/`g++` and does not execute through Go cgo bindings.

```sh
./pure_c/run_matrix.sh
```

The matrix currently runs full-parse benchmarks for:

- `c`
- `go`
- `java`
- `html`
- `lua`
- `toml`
- `yaml`

## Run Pure-C Go Incremental Benchmark (No CGo)

This reproduces full parse, incremental single-byte edit, and incremental
random-edit incremental, and no-edit numbers against the native C runtime:

```sh
./pure_c/run_go_benchmark.sh
```

Optional arguments:

```sh
./pure_c/run_go_benchmark.sh <func_count> <full_iters> <inc_iters>
```

Example:

```sh
./pure_c/run_go_benchmark.sh 500 2000 20000
```

Optional compiler tuning flags:

```sh
CFLAGS_EXTRA="-march=native -flto" ./pure_c/run_go_benchmark.sh
```

## Run Go Head-to-Head Comparison

This runs both:

- `gotreesitter` Go benchmarks
- pure-C runtime benchmark (no cgo)

```sh
./pure_c/run_go_head_to_head.sh
```

## Run Multi-Language Head-to-Head Matrix

This runs:

- pure-C runtime matrix (`c`, `go`, `java`, `html`, `lua`, `toml`, `yaml`)
- matching `gotreesitter` benchmarks
- a combined summary table with per-language speedup ratios

```sh
./pure_c/run_matrix_head_to_head.sh
```

## Run Full Claim Suite (3-way, multi-size, repeated)

This runs repeated benchmarks across:

- `gotreesitter` (pure Go)
- tree-sitter C runtime via cgo bindings
- tree-sitter C runtime compiled directly with GCC (no cgo)

and generates a median-based report.

```sh
./pure_c/run_claim_suite.sh
```

Tunable inputs:

```sh
RUNS=7 SIZES="500 2000 10000" CFLAGS_EXTRA="-march=native -flto" ./pure_c/run_claim_suite.sh
```
