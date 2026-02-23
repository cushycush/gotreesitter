# gotreesitter

Pure-Go [tree-sitter](https://tree-sitter.github.io/) runtime — no CGo, no C toolchain, WASM-ready.

```sh
go get github.com/odvcencio/gotreesitter
```

Implements the same parse-table format tree-sitter uses, so existing grammars work without recompilation. Outperforms the CGo binding on every workload — incremental edits (the dominant operation in editors and language servers) are **90x faster** than the C implementation.

## Why Not CGo?

Every existing Go tree-sitter binding requires CGo. That means:

- Cross-compilation breaks (`GOOS=wasip1`, `GOARCH=arm64` from Linux, Windows without MSYS2)
- CI pipelines need a C toolchain in every build image
- `go install` fails for end users without `gcc`
- Race detector, fuzzing, and coverage tools work poorly across the CGo boundary

gotreesitter is pure Go. `go get` and build — on any target, any platform.

## Quick Start

```go
import (
    "fmt"

    "github.com/odvcencio/gotreesitter"
    "github.com/odvcencio/gotreesitter/grammars"
)

func main() {
    src := []byte(`package main

func main() {}
`)

    lang := grammars.GoLanguage()
    parser := gotreesitter.NewParser(lang)

    tree := parser.Parse(src)
    fmt.Println(tree.RootNode())

    // After editing source, reparse incrementally:
    //   tree.Edit(edit)
    //   tree2 := parser.ParseIncremental(newSrc, tree)
}
```

### Queries

Tree-sitter's S-expression query language is supported, including predicates and cursor-based streaming. See [Known Limitations](#known-limitations) for current caveats.

```go
q, _ := gotreesitter.NewQuery(`(function_declaration name: (identifier) @fn)`, lang)
cursor := q.Exec(tree.RootNode(), lang, src)

for {
    match, ok := cursor.NextMatch()
    if !ok {
        break
    }
    for _, cap := range match.Captures {
        fmt.Println(cap.Node.Text(src))
    }
}
```

### Incremental Editing

After the initial parse, re-parse only the changed region — unchanged subtrees are reused automatically.

```go
// Initial parse
tree := parser.Parse(src)

// User types "x" at byte offset 42
src = append(src[:42], append([]byte("x"), src[42:]...)...)

tree.Edit(gotreesitter.InputEdit{
    StartByte:   42,
    OldEndByte:  42,
    NewEndByte:  43,
    StartPoint:  gotreesitter.Point{Row: 3, Column: 10},
    OldEndPoint: gotreesitter.Point{Row: 3, Column: 10},
    NewEndPoint: gotreesitter.Point{Row: 3, Column: 11},
})

// Incremental reparse — ~1.38 μs vs 124 μs for the CGo binding (90x faster)
tree2 := parser.ParseIncremental(src, tree)
```

> **Tip:** Use `grammars.DetectLanguage("main.go")` to pick the right grammar by filename — useful for editor integration.

### Syntax Highlighting

```go
hl, _ := gotreesitter.NewHighlighter(lang, highlightQuery)
ranges := hl.Highlight(src)

for _, r := range ranges {
    fmt.Printf("%s: %q\n", r.Capture, src[r.StartByte:r.EndByte])
}
```

> **Note:** Text predicates (`#eq?`, `#match?`, `#any-of?`, `#not-eq?`) require `source []byte` to evaluate. Passing `nil` disables predicate checks.

### Symbol Tagging

Extract definitions and references from source code:

```go
entry := grammars.DetectLanguage("main.go")
lang := entry.Language()

tagger, _ := gotreesitter.NewTagger(lang, entry.TagsQuery)
tags := tagger.Tag(src)

for _, tag := range tags {
    fmt.Printf("%s %s at %d:%d\n", tag.Kind, tag.Name,
        tag.NameRange.StartPoint.Row, tag.NameRange.StartPoint.Column)
}
```

### Parse Quality

Each `LangEntry` exposes a `Quality` field indicating how trustworthy the parse output is:

| Quality | Meaning |
|---|---|
| `full` | Token source or DFA with external scanner — full fidelity |
| `partial` | DFA-partial — missing external scanner, tree may have silent gaps |
| `none` | Cannot parse |

```go
entries := grammars.AllLanguages()
for _, e := range entries {
    fmt.Printf("%s: %s\n", e.Name, e.Quality)
}
```

---

## Benchmarks

Measured against [`go-tree-sitter`](https://github.com/smacker/go-tree-sitter) (the standard CGo binding), parsing a Go source file with 500 function definitions.

```
goos: linux / goarch: amd64 / cpu: Intel(R) Core(TM) Ultra 9 285

# pure-Go parser benchmarks (root module)
go test -run '^$' -bench 'BenchmarkGoParse' -benchmem -count=3

# C baseline benchmarks (cgo_harness module)
cd cgo_harness
go test . -run '^$' -tags treesitter_c_bench -bench 'BenchmarkCTreeSitterGoParse' -benchmem -count=3
```

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkCTreeSitterGoParseFull` | 2,058,000 | 600 | 6 |
| `BenchmarkCTreeSitterGoParseIncrementalSingleByteEdit` | 124,100 | 648 | 7 |
| `BenchmarkCTreeSitterGoParseIncrementalNoEdit` | 121,100 | 600 | 6 |
| `BenchmarkGoParseFull` | 1,330,000 | 10,842 | 2,495 |
| `BenchmarkGoParseIncrementalSingleByteEdit` | 1,381 | 361 | 9 |
| `BenchmarkGoParseIncrementalNoEdit` | 8.63 | 0 | 0 |

**Summary:**

| Workload | gotreesitter | CGo binding | Ratio |
|---|---:|---:|---|
| Full parse | 1,330 μs | 2,058 μs | **~1.5x faster** |
| Incremental (single-byte edit) | 1.38 μs | 124 μs | **~90x faster** |
| Incremental (no-op reparse) | 8.6 ns | 121 μs | **~14,000x faster** |

The incremental hot path reuses subtrees aggressively — a single-byte edit reparses in microseconds while the CGo binding pays full C-runtime and call overhead. The no-edit fast path exits on a single nil-check: zero allocations, single-digit nanoseconds.

---

## Supported Languages

205 grammars ship in the registry. Run `go run ./cmd/parity_report` for live per-language status.

Current summary:
- **204 clean** — parse without errors
- **1 degraded** — `norg` (requires external scanner with 122 tokens, not yet implemented)
- **0 unsupported**

Quality breakdown:
- **116 full** — token source or DFA with complete external scanner
- **89 partial** — DFA-partial (missing external scanner, tree may have silent gaps)

Backend breakdown:
- **92 dfa** — lexer fully generated from grammar tables
- **89 dfa-partial** — generated DFA without external scanner
- **24 token_source** — hand-written or generic pure-Go lexer bridge

12 languages have hand-written Go external scanners: python, elixir, comment, doxygen, foam, nginx, nushell, r, xml, yuck, purescript, typst.

**Full language list (205):**
`ada`, `agda`, `angular`, `apex`, `arduino`, `asm`, `astro`, `authzed`, `awk`, `bash`, `bass`, `beancount`, `bibtex`, `bicep`, `bitbake`, `blade`, `brightscript`, `c`, `c_sharp`, `caddy`, `cairo`, `capnp`, `chatito`, `circom`, `clojure`, `cmake`, `cobol`, `comment`, `commonlisp`, `cooklang`, `corn`, `cpon`, `cpp`, `crystal`, `css`, `csv`, `cuda`, `cue`, `cylc`, `d`, `dart`, `desktop`, `devicetree`, `dhall`, `diff`, `disassembly`, `djot`, `dockerfile`, `dot`, `doxygen`, `dtd`, `earthfile`, `ebnf`, `editorconfig`, `eds`, `eex`, `elisp`, `elixir`, `elm`, `elsa`, `embedded_template`, `enforce`, `erlang`, `facility`, `faust`, `fennel`, `fidl`, `firrtl`, `fish`, `foam`, `forth`, `fortran`, `fsharp`, `gdscript`, `git_config`, `git_rebase`, `gitattributes`, `gitcommit`, `gitignore`, `gleam`, `glsl`, `gn`, `go`, `godot_resource`, `gomod`, `graphql`, `groovy`, `hack`, `hare`, `haskell`, `haxe`, `hcl`, `heex`, `hlsl`, `html`, `http`, `hurl`, `hyprlang`, `ini`, `janet`, `java`, `javascript`, `jinja2`, `jq`, `jsdoc`, `json`, `json5`, `jsonnet`, `julia`, `just`, `kconfig`, `kdl`, `kotlin`, `ledger`, `less`, `linkerscript`, `liquid`, `llvm`, `lua`, `luau`, `make`, `markdown`, `markdown_inline`, `matlab`, `mermaid`, `meson`, `mojo`, `move`, `nginx`, `nickel`, `nim`, `ninja`, `nix`, `norg`, `nushell`, `objc`, `ocaml`, `odin`, `org`, `pascal`, `pem`, `perl`, `php`, `pkl`, `powershell`, `prisma`, `prolog`, `promql`, `properties`, `proto`, `pug`, `puppet`, `purescript`, `python`, `ql`, `r`, `racket`, `regex`, `rego`, `requirements`, `rescript`, `robot`, `ron`, `rst`, `ruby`, `rust`, `scala`, `scheme`, `scss`, `smithy`, `solidity`, `sparql`, `sql`, `squirrel`, `ssh_config`, `starlark`, `svelte`, `swift`, `tablegen`, `tcl`, `teal`, `templ`, `textproto`, `thrift`, `tlaplus`, `tmux`, `todotxt`, `toml`, `tsx`, `turtle`, `twig`, `typescript`, `typst`, `uxntal`, `v`, `verilog`, `vhdl`, `vimdoc`, `vue`, `wgsl`, `wolfram`, `xml`, `yaml`, `yuck`, `zig`

---

## Query API

| Feature | Status |
|---|---|
| Compile + execute (`NewQuery`, `Execute`, `ExecuteNode`) | supported |
| Cursor streaming (`Exec`, `NextMatch`, `NextCapture`) | supported |
| Structural quantifiers (`?`, `*`, `+`) | supported |
| Alternation (`[...]`) | supported |
| Field matching (`name: (identifier)`) | supported |
| `#eq?` / `#not-eq?` | supported |
| `#match?` / `#not-match?` | supported |
| `#any-of?` / `#not-any-of?` | supported |
| `#lua-match?` | supported |
| `#has-ancestor?` / `#not-has-ancestor?` | supported |
| `#not-has-parent?` | supported |
| `#is?` / `#is-not?` | supported |
| `#set!` / `#offset!` directives | parsed and accepted |

---

## Known Limitations

### Query compiler gaps

As of February 23, 2026, all shipped highlight and tags queries compile in this repo (`156/156` non-empty `HighlightQuery` entries, `69/69` non-empty `TagsQuery` entries).

No known query-syntax gaps currently block shipped highlight or tags queries.

### DFA-partial languages

89 languages require an external scanner that has not been ported to Go. These parse successfully using the DFA lexer alone, but tokens that require the external scanner are silently skipped. The tree structure is valid but may have gaps. Check `entry.Quality` to distinguish `full` from `partial`.

---

## Adding a Language

**1.** Add the grammar to `grammars/languages.manifest`.

**2.** Generate bindings:

```sh
go run ./cmd/ts2go -manifest grammars/languages.manifest -outdir ./grammars -package grammars -compact=true
```

This regenerates `grammars/embedded_grammars_gen.go`, `grammars/grammar_blobs/*.bin`, and language register stubs.

**3.** Add smoke samples to `cmd/parity_report/main.go` and `grammars/parse_support_test.go`.

**4.** Verify:

```sh
go run ./cmd/parity_report
go test ./grammars/...
```

---

## Architecture

gotreesitter reimplements the tree-sitter runtime in pure Go:

- **Parser** — table-driven LR(1) with GLR support for ambiguous grammars
- **Incremental reuse** — cursor-based subtree reuse; unchanged regions skip reparsing entirely
- **Arena allocator** — slab-based node allocation with ref counting, minimizing GC pressure
- **DFA lexer** — generated from grammar tables via `ts2go`, with hand-written bridges where needed
- **External scanner VM** — bytecode interpreter for language-specific scanning (Python indentation, etc.)
- **Query engine** — S-expression pattern matching with predicate evaluation and streaming cursors
- **Highlighter** — query-based syntax highlighting with incremental support
- **Tagger** — symbol definition/reference extraction using tags queries

Grammar tables are extracted from upstream tree-sitter `parser.c` files by the `ts2go` tool, serialized into compressed binary blobs, and lazy-loaded on first language use. No C code runs at parse time.

To avoid embedding blobs into the binary, build with `-tags grammar_blobs_external` and set `GOTREESITTER_GRAMMAR_BLOB_DIR` to a directory containing `*.bin` grammar blobs. External blob mode uses `mmap` on Unix by default (`GOTREESITTER_GRAMMAR_BLOB_MMAP=false` to disable).

To ship a smaller embedded binary with a curated language set, build with `-tags grammar_set_core` (core set includes common languages like `c`, `go`, `java`, `javascript`, `python`, `rust`, `typescript`, etc.).

To restrict registered languages at runtime (embedded or external), set:

```sh
GOTREESITTER_GRAMMAR_SET=go,json,python
```

For long-lived processes, grammar cache memory is tunable:

```go
// Keep only the 8 most recently used decoded grammars in cache.
grammars.SetEmbeddedLanguageCacheLimit(8)

// Drop one language blob from cache (e.g. "rust.bin").
grammars.UnloadEmbeddedLanguage("rust.bin")

// Drop all decoded grammars from cache.
grammars.PurgeEmbeddedLanguageCache()
```

You can also set `GOTREESITTER_GRAMMAR_CACHE_LIMIT` at process start to apply a cache cap without code changes.
Set it to `0` only when you explicitly want no retention (each grammar access will decode again).

Idle eviction can be enabled with env vars:

```sh
GOTREESITTER_GRAMMAR_IDLE_TTL=5m
GOTREESITTER_GRAMMAR_IDLE_SWEEP=30s
```

Loader compaction/interning is enabled by default and tunable via:

```sh
GOTREESITTER_GRAMMAR_COMPACT=true
GOTREESITTER_GRAMMAR_STRING_INTERN_LIMIT=200000
GOTREESITTER_GRAMMAR_TRANSITION_INTERN_LIMIT=20000
```

---

## Testing

The test suite includes:

- **Smoke tests** — all 205 grammars parse a sample without crashing or producing ERROR nodes
- **Correctness snapshots** — golden S-expression tests for 20 core languages catch parser and grammar regressions
- **Highlight validation** — end-to-end test that compiled highlight queries produce highlight ranges
- **Query tests** — pattern matching, predicates, cursors, field-based matching
- **Parser tests** — incremental reparsing, error recovery, GLR ambiguity resolution
- **Fuzzing** — `FuzzGoParseDoesNotPanic` for parser robustness

```sh
go test ./... -race -count=1
```

---

## Roadmap

**Current: v0.1.0** — 205 grammars, stable parser, incremental reparsing, query engine, highlighting, tagging.

**Next:**

- Query engine parity hardening — field-negation semantics, metadata directive behavior, and additional edge-case parity with upstream tree-sitter query execution
- More hand-written external scanners for high-value `dfa-partial` languages
- `Parse() (*Tree, error)` — return errors instead of silent nil trees
- Automated parity testing against the C tree-sitter output
- Fuzzing expansion to cover more languages and the query engine

---

## License

[MIT](LICENSE)
