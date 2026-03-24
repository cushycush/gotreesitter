package grammargen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/odvcencio/gotreesitter"
)

func TestTypeScriptTernaryAfterLogicalOrParity(t *testing.T) {
	if raceEnabled {
		t.Skip("skip heavyweight TypeScript parity generation under -race; non-race coverage keeps the generated-vs-reference check")
	}
	var grammarSpec importParityGrammar
	for _, g := range importParityGrammars {
		if g.name == "typescript" {
			grammarSpec = g
			break
		}
	}
	if grammarSpec.name == "" {
		t.Fatal("typescript import parity grammar not found")
	}
	if grammarSpec.jsonPath != "" {
		if _, err := os.Stat(grammarSpec.jsonPath); err != nil && strings.HasPrefix(grammarSpec.jsonPath, "/tmp/grammar_parity/") {
			relSeedPath := filepath.Join(".parity_seed", strings.TrimPrefix(grammarSpec.jsonPath, "/tmp/grammar_parity/"))
			switch {
			case fileExists(relSeedPath):
				grammarSpec.jsonPath = relSeedPath
			case fileExists(filepath.Join("..", relSeedPath)):
				grammarSpec.jsonPath = filepath.Join("..", relSeedPath)
			}
		}
	}

	gram, err := importParityGrammarSource(grammarSpec)
	if err != nil {
		t.Fatalf("import typescript grammar: %v", err)
	}

	timeout := grammarSpec.genTimeout
	if timeout == 0 {
		timeout = 180 * time.Second
	}
	genLang, err := generateWithTimeout(gram, timeout)
	if err != nil {
		t.Fatalf("generate typescript language: %v", err)
	}
	refLang := grammarSpec.blobFunc()
	adaptExternalScanner(refLang, genLang)

	src := []byte("namespace ts {\n    namespace Parser {\n        function getLanguageVariant(scriptKind: ScriptKind) {\n            return scriptKind === ScriptKind.TSX || scriptKind === ScriptKind.JSX || scriptKind === ScriptKind.JS || scriptKind === ScriptKind.JSON ? LanguageVariant.JSX : LanguageVariant.Standard;\n        }\n    }\n}\n")

	genTree, err := gotreesitter.NewParser(genLang).Parse(src)
	if err != nil {
		t.Fatalf("generated parse: %v", err)
	}
	refTree, err := gotreesitter.NewParser(refLang).Parse(src)
	if err != nil {
		t.Fatalf("reference parse: %v", err)
	}

	genRoot := genTree.RootNode()
	refRoot := refTree.RootNode()
	genSExpr := safeSExpr(genRoot, genLang, 128)
	refSExpr := safeSExpr(refRoot, refLang, 128)

	if genRoot.HasError() != refRoot.HasError() {
		t.Fatalf("error mismatch: gen=%v ref=%v\nGEN: %s\nREF: %s", genRoot.HasError(), refRoot.HasError(), genSExpr, refSExpr)
	}
	if genSExpr != refSExpr {
		divs := compareTreesDeep(genRoot, genLang, refRoot, refLang, "root", 8)
		t.Fatalf("sexpr mismatch\nGEN: %s\nREF: %s\nDIVS: %v", genSExpr, refSExpr, divs)
	}
}
