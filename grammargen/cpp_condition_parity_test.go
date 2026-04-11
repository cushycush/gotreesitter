package grammargen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/odvcencio/gotreesitter"
)

func TestCPPWhileAssignmentConditionParity(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "wrapped_function",
			src:  "int f() { while ((a = b)) {} }\n",
		},
		{
			name: "bare_corpus_statement",
			src:  "while ((a = b)) {}\n",
		},
	}

	var grammarSpec importParityGrammar
	for _, g := range importParityGrammars {
		if g.name == "cpp" {
			grammarSpec = g
			break
		}
	}
	if grammarSpec.name == "" {
		t.Fatal("cpp import parity grammar not found")
	}
	if grammarSpec.jsonPath != "" {
		if _, err := os.Stat(grammarSpec.jsonPath); err != nil && strings.HasPrefix(grammarSpec.jsonPath, "/tmp/grammar_parity/") {
			relSeedPath := filepath.Join(".parity_seed", strings.TrimPrefix(grammarSpec.jsonPath, "/tmp/grammar_parity/"))
			switch {
			case fileExists(relSeedPath):
				grammarSpec.jsonPath = relSeedPath
			case fileExists(filepath.Join("..", relSeedPath)):
				grammarSpec.jsonPath = filepath.Join("..", relSeedPath)
			default:
				t.Skipf("cpp grammar.json not available: %v", err)
			}
		}
	}

	gram, err := importParityGrammarSource(grammarSpec)
	if err != nil {
		t.Fatalf("import cpp grammar: %v", err)
	}

	timeout := grammarSpec.genTimeout
	if timeout == 0 {
		timeout = 300 * time.Second
	}
	genLang, err := generateWithTimeout(gram, timeout)
	if err != nil {
		t.Fatalf("generate cpp language: %v", err)
	}
	refLang := grammarSpec.blobFunc()
	adaptExternalScanner(refLang, genLang)

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)

			genTree, err := gotreesitter.NewParser(genLang).Parse(src)
			if err != nil {
				t.Fatalf("generated parse: %v", err)
			}
			defer genTree.Release()
			refTree, err := gotreesitter.NewParser(refLang).Parse(src)
			if err != nil {
				t.Fatalf("reference parse: %v", err)
			}
			defer refTree.Release()

			genRoot := genTree.RootNode()
			refRoot := refTree.RootNode()
			if genRoot == nil || refRoot == nil {
				t.Fatalf("nil roots: gen=%v ref=%v", genRoot, refRoot)
			}
			if genRoot.HasError() || refRoot.HasError() {
				t.Fatalf("unexpected root error\nGEN: %s\nREF: %s", safeSExpr(genRoot, genLang, 64), safeSExpr(refRoot, refLang, 64))
			}

			genCond := findFirstNamedDescendantOfType(genRoot, genLang, "condition_clause")
			refCond := findFirstNamedDescendantOfType(refRoot, refLang, "condition_clause")
			if genCond == nil || refCond == nil {
				t.Fatalf("missing condition clauses\nGEN: %s\nREF: %s", safeSExpr(genRoot, genLang, 64), safeSExpr(refRoot, refLang, 64))
			}

			genSExpr := safeSExpr(genCond, genLang, 64)
			refSExpr := safeSExpr(refCond, refLang, 64)
			if genSExpr != refSExpr {
				t.Fatalf("condition clause mismatch\nGEN: %s\nREF: %s", genSExpr, refSExpr)
			}

			genAssign := findFirstNamedDescendantOfType(genCond, genLang, "assignment_expression")
			refAssign := findFirstNamedDescendantOfType(refCond, refLang, "assignment_expression")
			if genAssign == nil || refAssign == nil {
				t.Fatalf("assignment_expression mismatch\nGEN: %s\nREF: %s", genSExpr, refSExpr)
			}

			if bad := findFirstNamedDescendantOfType(genCond, genLang, "declaration"); bad != nil {
				t.Fatalf("generated condition clause still picked declaration branch: %s", genSExpr)
			}
		})
	}
}
