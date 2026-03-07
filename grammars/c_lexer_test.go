package grammars

import (
	"bytes"
	"testing"

	"github.com/odvcencio/gotreesitter"
)

func TestNewCTokenSourceReturnsErrorOnMissingSymbols(t *testing.T) {
	lang := &gotreesitter.Language{
		TokenCount:  1,
		SymbolNames: []string{"end"},
	}
	if _, err := NewCTokenSource([]byte("int main(void) { return 0; }\n"), lang); err == nil {
		t.Fatal("expected error for language missing c token symbols")
	}
}

func TestNewCTokenSourceOrEOFFallsBack(t *testing.T) {
	lang := &gotreesitter.Language{
		TokenCount:  1,
		SymbolNames: []string{"end"},
	}
	ts := NewCTokenSourceOrEOF([]byte("int main(void) { return 0; }\n"), lang)
	tok := ts.Next()
	if tok.Symbol != 0 {
		t.Fatalf("fallback token symbol = %d, want EOF (0)", tok.Symbol)
	}
}

func TestCTokenSourceSkipToByte(t *testing.T) {
	lang := CLanguage()
	src := []byte("int main(void) {\n  int x = 1;\n  return x;\n}\n")
	target := bytes.Index(src, []byte("return"))
	if target < 0 {
		t.Fatal("missing target marker")
	}

	ts, err := NewCTokenSource(src, lang)
	if err != nil {
		t.Fatalf("NewCTokenSource failed: %v", err)
	}

	tok := ts.SkipToByte(uint32(target))
	if tok.Symbol == 0 {
		t.Fatal("SkipToByte unexpectedly returned EOF")
	}
	if int(tok.StartByte) < target {
		t.Fatalf("token starts before target offset: got %d, target %d", tok.StartByte, target)
	}
	if tok.Text != "return" {
		t.Fatalf("expected token text %q, got %q", "return", tok.Text)
	}
}

func TestParseCPreprocessorDefines(t *testing.T) {
	lang := CLanguage()
	parser := gotreesitter.NewParser(lang)
	src := []byte("#define FOO 42\n#define BAR 100\n")
	ts, err := NewCTokenSource(src, lang)
	if err != nil {
		t.Fatalf("NewCTokenSource failed: %v", err)
	}

	tree, err := parser.ParseWithTokenSource(src, ts)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("nil root")
	}
	if root.HasError() {
		t.Fatalf("parse has errors; root type = %s", root.Type(lang))
	}

	found := 0
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Type(lang) == "preproc_def" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("expected 2 preproc_def nodes, got %d", found)
	}
}

func TestParseCMixedWithPreprocessor(t *testing.T) {
	lang := CLanguage()
	parser := gotreesitter.NewParser(lang)
	src := []byte("#define MAX 255\nint main(void) { return 0; }\n")
	ts, err := NewCTokenSource(src, lang)
	if err != nil {
		t.Fatalf("NewCTokenSource failed: %v", err)
	}

	tree, err := parser.ParseWithTokenSource(src, ts)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root.HasError() {
		t.Fatalf("parse has errors")
	}

	types := make([]string, root.ChildCount())
	for i := 0; i < root.ChildCount(); i++ {
		types[i] = root.Child(i).Type(lang)
	}
	if len(types) < 2 {
		t.Fatalf("expected at least 2 top-level nodes, got %v", types)
	}
}

func TestParseCPreprocessorIncludesWithSystemHeaders(t *testing.T) {
	lang := CLanguage()
	parser := gotreesitter.NewParser(lang)
	src := []byte("#include \"runtime/parser.h\"\n#include <assert.h>\n#include <stdio.h>\n")
	ts, err := NewCTokenSource(src, lang)
	if err != nil {
		t.Fatalf("NewCTokenSource failed: %v", err)
	}

	tree, err := parser.ParseWithTokenSource(src, ts)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("nil root")
	}
	if root.HasError() {
		t.Fatalf("include parse has errors; root type = %s", root.Type(lang))
	}

	includeCount := 0
	systemHeaderCount := 0
	gotreesitter.Walk(root, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		switch node.Type(lang) {
		case "preproc_include":
			includeCount++
		case "system_lib_string":
			systemHeaderCount++
		}
		return gotreesitter.WalkContinue
	})
	if got, want := includeCount, 3; got != want {
		t.Fatalf("preproc_include count = %d, want %d", got, want)
	}
	if got, want := systemHeaderCount, 2; got != want {
		t.Fatalf("system_lib_string count = %d, want %d", got, want)
	}
}

func TestCTokenSourceFunctionLikeMacroTokenSequence(t *testing.T) {
	lang := CLanguage()
	src := []byte("#define LOG(...) fprintf(stderr, __VA_ARGS__)\n")
	ts, err := NewCTokenSource(src, lang)
	if err != nil {
		t.Fatalf("NewCTokenSource failed: %v", err)
	}

	var got []string
	for {
		tok := ts.Next()
		if tok.Symbol == 0 {
			break
		}
		got = append(got, lang.SymbolNames[tok.Symbol])
	}

	want := []string{"#define", "identifier", "(", "...", ")", "preproc_arg", "preproc_include_token2"}
	if len(got) != len(want) {
		t.Fatalf("token count = %d, want %d; got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d = %q, want %q; got=%v", i, got[i], want[i], got)
		}
	}
}

func TestParseCFunctionLikeMacro(t *testing.T) {
	lang := CLanguage()
	parser := gotreesitter.NewParser(lang)
	src := []byte("#define LOG(...) fprintf(stderr, __VA_ARGS__)\n")
	ts, err := NewCTokenSource(src, lang)
	if err != nil {
		t.Fatalf("NewCTokenSource failed: %v", err)
	}

	tree, err := parser.ParseWithTokenSource(src, ts)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("nil root")
	}
	if root.HasError() {
		t.Fatalf("function-like macro parse has errors; root type = %s", root.Type(lang))
	}

	found := false
	gotreesitter.Walk(root, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		if node.Type(lang) == "preproc_function_def" {
			found = true
			return gotreesitter.WalkStop
		}
		return gotreesitter.WalkContinue
	})
	if !found {
		t.Fatalf("expected preproc_function_def in tree, got %s", root.SExpr(lang))
	}
}

func TestParseCMultilineFunctionLikeMacro(t *testing.T) {
	lang := CLanguage()
	parser := gotreesitter.NewParser(lang)
	src := []byte("#define LOG(...) \\\n  fprintf(stderr, __VA_ARGS__)\n")
	ts, err := NewCTokenSource(src, lang)
	if err != nil {
		t.Fatalf("NewCTokenSource failed: %v", err)
	}

	tree, err := parser.ParseWithTokenSource(src, ts)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("nil root")
	}
	if root.HasError() {
		t.Fatalf("multiline function-like macro parse has errors; root type = %s", root.Type(lang))
	}

	found := false
	gotreesitter.Walk(root, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		if node.Type(lang) == "preproc_function_def" {
			found = true
			return gotreesitter.WalkStop
		}
		return gotreesitter.WalkContinue
	})
	if !found {
		t.Fatalf("expected preproc_function_def in tree, got %s", root.SExpr(lang))
	}
}

func TestParseCHeaderGuard(t *testing.T) {
	lang := CLanguage()
	parser := gotreesitter.NewParser(lang)
	src := []byte("#ifndef FOO_H\n#define FOO_H\n\nint x;\n\n#endif\n")
	ts, err := NewCTokenSource(src, lang)
	if err != nil {
		t.Fatalf("NewCTokenSource failed: %v", err)
	}
	tree, err := parser.ParseWithTokenSource(src, ts)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root.HasError() {
		t.Fatalf("header guard parse has errors")
	}
}

func TestParseCDefineWithExpression(t *testing.T) {
	lang := CLanguage()
	parser := gotreesitter.NewParser(lang)
	src := []byte("#define FOO (1 + 2)\n")
	ts, err := NewCTokenSource(src, lang)
	if err != nil {
		t.Fatalf("NewCTokenSource failed: %v", err)
	}
	tree, err := parser.ParseWithTokenSource(src, ts)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root.HasError() {
		t.Fatalf("define-with-expression parse has errors")
	}
}

func TestParseCWithTokenSource(t *testing.T) {
	lang := CLanguage()
	parser := gotreesitter.NewParser(lang)
	src := []byte("int main(void) { return 0; }\n")
	ts, err := NewCTokenSource(src, lang)
	if err != nil {
		t.Fatalf("NewCTokenSource failed: %v", err)
	}

	tree, err := parser.ParseWithTokenSource(src, ts)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if tree == nil || tree.RootNode() == nil {
		t.Fatal("parse returned nil root")
	}
	if tree.RootNode().HasError() {
		t.Fatal("expected c parse without syntax errors")
	}
}
