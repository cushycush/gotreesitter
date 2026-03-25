package grammargen

import (
	"os"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func TestCCompoundAssignmentParity(t *testing.T) {
	source, err := os.ReadFile("/tmp/grammar_parity/c/src/grammar.json")
	if err != nil {
		t.Skipf("read c grammar.json: %v", err)
	}
	gram, err := ImportGrammarJSON(source)
	if err != nil {
		t.Fatalf("import c grammar: %v", err)
	}
	genLang, err := GenerateLanguage(gram)
	if err != nil {
		t.Fatalf("generate c language: %v", err)
	}
	refLang := grammars.CLanguage()
	if refLang.ExternalScanner != nil && len(genLang.ExternalSymbols) > 0 {
		if scanner, ok := gotreesitter.AdaptExternalScannerByExternalOrder(refLang, genLang); ok {
			genLang.ExternalScanner = scanner
		}
	}

	genParser := gotreesitter.NewParser(genLang)
	refParser := gotreesitter.NewParser(refLang)
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "plain",
			src:  "static void f(void) { x |= y; }\n",
		},
		{
			name: "pointer",
			src:  "static void f(int *p) { *p |= y; }\n",
		},
		{
			name: "field",
			src:  "static void f(struct chunk *c) { c->csize |= C_INUSE; }\n",
		},
		{
			name: "macro_lhs",
			src:  "static void f(struct chunk *c) { NEXT_CHUNK(c)->psize |= C_INUSE; }\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			genTree, err := genParser.Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("gen parse: %v", err)
			}
			defer genTree.Release()
			refTree, err := refParser.Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("ref parse: %v", err)
			}
			defer refTree.Release()

			genRoot := genTree.RootNode()
			refRoot := refTree.RootNode()
			if genRoot == nil || refRoot == nil {
				t.Fatal("nil root")
			}
			if genRoot.HasError() {
				t.Fatalf("generated tree has ERROR: %s", genRoot.SExpr(genLang))
			}
			if refRoot.HasError() {
				t.Fatalf("reference tree has ERROR: %s", refRoot.SExpr(refLang))
			}

			genSexp := normalizeSexp(genRoot.SExpr(genLang))
			refSexp := normalizeSexp(refRoot.SExpr(refLang))
			if genSexp != refSexp {
				t.Fatalf("parity mismatch:\n  gen: %s\n  ref: %s", genRoot.SExpr(genLang), refRoot.SExpr(refLang))
			}
		})
	}
}
