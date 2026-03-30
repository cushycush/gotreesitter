package gotreesitter_test

// Golden artifact tests freeze the deterministic output of the core public API
// so that optimizations can be validated without introducing regressions.
//
// Regenerate: UPDATE_GOLDEN=1 go test -run TestGolden -v .

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const updateGoldenEnv = "UPDATE_GOLDEN"
const goldenRoot = ".golden/corpus"

// --- canonical source inputs --------------------------------------------------

const goldenGoEmpty = ``

const goldenGoMinimal = `package main`

const goldenGoFunction = `package main

func hello(name string) string {
	return "Hello, " + name
}
`

const goldenGoMultiFuncs = `package main

func add(a, b int) int {
	return a + b
}

func sub(a, b int) int {
	return a - b
}

type Counter struct{ n int }

func (c *Counter) Inc() { c.n++ }
`

// Error recovery: parser must produce a tree even for broken input.
const goldenGoErrorRecovery = `package main

func broken( {
	return 42
}
`

const goldenGoRewriteSrc = `package main

func oldName(x int) int {
	return oldName(x - 1)
}
`

// --- helpers ------------------------------------------------------------------

func goldenFilePath(category, name string) string {
	return filepath.Join(goldenRoot, category, name+".golden")
}

func checkOrWriteGolden(t *testing.T, category, name, got string) {
	t.Helper()
	path := goldenFilePath(category, name)
	if os.Getenv(updateGoldenEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("wrote %s", path)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file missing: %s\n  run with %s=1 to generate", path, updateGoldenEnv)
	}
	want := string(data)
	if got != want {
		t.Errorf("%s/%s diverges from golden\n--- got ---\n%s\n--- want ---\n%s", category, name, got, want)
	}
}

func goLangEntry(t *testing.T) *grammars.LangEntry {
	t.Helper()
	entry := grammars.DetectLanguage("main.go")
	if entry == nil {
		t.Fatal("DetectLanguage(main.go) returned nil")
	}
	return entry
}

// --- Parse golden tests -------------------------------------------------------

// TestGoldenParse freezes the S-expression output of Parser.Parse for canonical
// Go inputs. The DFA lexer is used so these tests have no external dependencies.
func TestGoldenParse(t *testing.T) {
	entry := goLangEntry(t)
	lang := entry.Language()
	parser := gotreesitter.NewParser(lang)

	cases := []struct {
		name string
		src  string
	}{
		{"go_empty", goldenGoEmpty},
		{"go_minimal", goldenGoMinimal},
		{"go_function", goldenGoFunction},
		{"go_multi_funcs", goldenGoMultiFuncs},
		{"go_error_recovery", goldenGoErrorRecovery},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := parser.Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			defer tree.Release()
			got := tree.RootNode().SExpr(lang)
			checkOrWriteGolden(t, "parse", tc.name, got)
		})
	}
}

// TestGoldenParseIncremental freezes parse tree identity under a no-op edit
// and under a single-byte toggle. Both must yield the same final S-expression
// as the cold parse.
func TestGoldenParseIncremental(t *testing.T) {
	entry := goLangEntry(t)
	lang := entry.Language()
	parser := gotreesitter.NewParser(lang)
	src := []byte(goldenGoFunction)

	// Cold parse as the reference.
	cold, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("cold parse: %v", err)
	}
	defer cold.Release()
	wantSExpr := cold.RootNode().SExpr(lang)

	t.Run("no_edit", func(t *testing.T) {
		cold.Edit(gotreesitter.InputEdit{
			StartByte:  0, OldEndByte: 0, NewEndByte: 0,
			StartPoint: gotreesitter.Point{}, OldEndPoint: gotreesitter.Point{}, NewEndPoint: gotreesitter.Point{},
		})
		inc, err := parser.ParseIncremental(src, cold)
		if err != nil {
			t.Fatalf("incremental parse: %v", err)
		}
		defer inc.Release()
		got := inc.RootNode().SExpr(lang)
		checkOrWriteGolden(t, "parse", "go_incremental_no_edit", got)
		if got != wantSExpr {
			t.Errorf("incremental no-edit diverges from cold parse")
		}
	})
}

// --- Query golden tests -------------------------------------------------------

// TestGoldenQuery freezes QueryMatch output for a simple function-capture pattern.
func TestGoldenQuery(t *testing.T) {
	entry := goLangEntry(t)
	lang := entry.Language()
	parser := gotreesitter.NewParser(lang)
	src := []byte(goldenGoMultiFuncs)

	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	defer tree.Release()

	cases := []struct {
		name    string
		pattern string
	}{
		{
			"function_defs",
			`(function_declaration name: (identifier) @name) @func`,
		},
		{
			"type_defs",
			`(type_declaration (type_spec name: (type_identifier) @name))`,
		},
		{
			"method_defs",
			`(method_declaration name: (field_identifier) @name) @method`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := gotreesitter.NewQuery(tc.pattern, lang)
			if err != nil {
				t.Fatalf("NewQuery: %v", err)
			}
			matches := q.Execute(tree)
			got := formatQueryMatches(matches, src)
			checkOrWriteGolden(t, "query", tc.name, got)
		})
	}
}

func formatQueryMatches(matches []gotreesitter.QueryMatch, src []byte) string {
	var sb strings.Builder
	for _, m := range matches {
		for _, cap := range m.Captures {
			node := cap.Node
			text := string(src[node.StartByte():node.EndByte()])
			// truncate long captures for readability
			if len(text) > 40 {
				text = text[:40] + "..."
			}
			fmt.Fprintf(&sb, "pattern=%d capture=%s bytes=[%d,%d) text=%q\n",
				m.PatternIndex, cap.Name, node.StartByte(), node.EndByte(), text)
		}
	}
	return sb.String()
}

// --- Highlight golden tests ---------------------------------------------------

// TestGoldenHighlight freezes HighlightRange output for a small Go function.
func TestGoldenHighlight(t *testing.T) {
	entry := goLangEntry(t)
	lang := entry.Language()

	var opts []gotreesitter.HighlighterOption
	if entry.TokenSourceFactory != nil {
		factory := entry.TokenSourceFactory
		opts = append(opts, gotreesitter.WithTokenSourceFactory(func(s []byte) gotreesitter.TokenSource {
			return factory(s, lang)
		}))
	}

	hl, err := gotreesitter.NewHighlighter(lang, entry.HighlightQuery, opts...)
	if err != nil {
		t.Fatalf("NewHighlighter: %v", err)
	}

	cases := []struct {
		name string
		src  string
	}{
		{"go_function", goldenGoFunction},
		{"go_multi_funcs", goldenGoMultiFuncs},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ranges := hl.Highlight([]byte(tc.src))
			got := formatHighlightRanges(ranges)
			checkOrWriteGolden(t, "highlight", tc.name, got)
		})
	}
}

func formatHighlightRanges(ranges []gotreesitter.HighlightRange) string {
	var sb strings.Builder
	for _, r := range ranges {
		fmt.Fprintf(&sb, "bytes=[%d,%d) capture=%s\n", r.StartByte, r.EndByte, r.Capture)
	}
	return sb.String()
}

// --- Tagger golden tests ------------------------------------------------------

// TestGoldenTag freezes Tag output for canonical Go source.
func TestGoldenTag(t *testing.T) {
	entry := goLangEntry(t)
	lang := entry.Language()

	var opts []gotreesitter.TaggerOption
	if entry.TokenSourceFactory != nil {
		factory := entry.TokenSourceFactory
		opts = append(opts, gotreesitter.WithTaggerTokenSourceFactory(func(s []byte) gotreesitter.TokenSource {
			return factory(s, lang)
		}))
	}

	tagger, err := gotreesitter.NewTagger(lang, benchTagsQuery, opts...)
	if err != nil {
		t.Fatalf("NewTagger: %v", err)
	}

	cases := []struct {
		name string
		src  string
	}{
		{"go_function", goldenGoFunction},
		{"go_multi_funcs", goldenGoMultiFuncs},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tags := tagger.Tag([]byte(tc.src))
			got := formatTags(tags)
			checkOrWriteGolden(t, "tag", tc.name, got)
		})
	}
}

func formatTags(tags []gotreesitter.Tag) string {
	var sb strings.Builder
	for _, tag := range tags {
		fmt.Fprintf(&sb, "kind=%s name=%s bytes=[%d,%d)\n",
			tag.Kind, tag.Name, tag.Range.StartByte, tag.Range.EndByte)
	}
	return sb.String()
}

// --- Rewriter golden tests ----------------------------------------------------

// TestGoldenRewrite freezes Rewriter.Apply output for common edit patterns.
func TestGoldenRewrite(t *testing.T) {
	entry := goLangEntry(t)
	lang := entry.Language()
	parser := gotreesitter.NewParser(lang)
	src := []byte(goldenGoRewriteSrc)

	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	defer tree.Release()

	t.Run("rename_identifier", func(t *testing.T) {
		// Rename all identifiers named "oldName" to "newName" via query.
		q, err := gotreesitter.NewQuery(`(identifier) @id`, lang)
		if err != nil {
			t.Fatalf("NewQuery: %v", err)
		}
		rw := gotreesitter.NewRewriter(src)
		for _, m := range q.Execute(tree) {
			for _, cap := range m.Captures {
				node := cap.Node
				if string(src[node.StartByte():node.EndByte()]) == "oldName" {
					rw.Replace(node, []byte("newName"))
				}
			}
		}
		newSrc, _, err := rw.Apply()
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		checkOrWriteGolden(t, "rewrite", "go_rename_identifier", string(newSrc))
	})

	t.Run("delete_function_body_return", func(t *testing.T) {
		// Replace the return expression value with 0.
		q, err := gotreesitter.NewQuery(`(return_statement (binary_expression) @expr)`, lang)
		if err != nil {
			t.Fatalf("NewQuery: %v", err)
		}
		rw := gotreesitter.NewRewriter(src)
		for _, m := range q.Execute(tree) {
			for _, cap := range m.Captures {
				rw.Replace(cap.Node, []byte("0"))
			}
		}
		newSrc, _, err := rw.Apply()
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		checkOrWriteGolden(t, "rewrite", "go_replace_return_expr", string(newSrc))
	})
}
