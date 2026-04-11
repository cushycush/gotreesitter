package grammargen

import (
	"os"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// loadFortranLangLocal imports the Fortran grammar.json from the host-side
// read-only path and attaches the hand-written scanner. Skips the test if
// the grammar path doesn't exist.
func loadFortranLangLocal(t *testing.T) *gotreesitter.Language {
	t.Helper()
	const grammarPath = "/home/draco/grammar_parity_ro/fortran/src/grammar.json"
	data, err := os.ReadFile(grammarPath)
	if err != nil {
		t.Skipf("skip: %s not available", grammarPath)
	}
	g, err := ImportGrammarJSON(data)
	if err != nil {
		t.Fatalf("ImportGrammarJSON: %v", err)
	}
	lang, err := GenerateLanguage(g)
	if err != nil {
		t.Fatalf("GenerateLanguage: %v", err)
	}
	if ok := grammars.AdaptScannerForLanguage("fortran", lang); !ok {
		t.Fatalf("AdaptScannerForLanguage(fortran) failed")
	}
	return lang
}

// TestFortranIntegerKindSuffix is a regression test for the auto-injected
// conflict fix for statement_label vs number_literal R/R. Before the fix,
// parsing `1_SZ1` produced an error because LALR merging collapsed the
// statement-label context with the expression-RHS context and the prec
// resolution picked the wrong production. After auto-injecting the
// conflict, the parser retains both reduces as GLR and forks on the
// conflict, picking the correct path based on follow context.
func TestFortranIntegerKindSuffix(t *testing.T) {
	lang := loadFortranLangLocal(t)

	cases := []struct {
		label string
		src   string
	}{
		{"int-kind-sz1", "program t\n  int_val = 1_SZ1\nend program\n"},
		{"int-kind-num", "program t\n  int_val = 1_4\nend program\n"},
		{"float-kind-sz1", "program t\n  flt_val = 1.0_SZ1\nend program\n"},
		{"boz-hex", "program t\n  b = Z'09AF'\nend program\n"},
	}
	parser := gotreesitter.NewParser(lang)
	for _, c := range cases {
		tree, err := parser.Parse([]byte(c.src))
		if err != nil {
			t.Errorf("%s: parse error: %v", c.label, err)
			continue
		}
		if tree.RootNode().HasError() {
			t.Errorf("%s: parse has error. sexpr=%s", c.label, tree.RootNode().SExpr(lang))
		}
	}
}
