package grammars

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/odvcencio/gotreesitter"
)

func TestCorpusDiag(t *testing.T) {
	type testCase struct {
		filePath string
		langName string
	}

	cases := []testCase{
		{"/home/draco/work/gotreesitter/harness_out/corpus_real_205_noscala_bounded/earthfile/small__Earthfile", "earthfile"},
		{"/home/draco/work/gotreesitter/harness_out/corpus_real_205_noscala_bounded/cpp/small__names.cpp", "cpp"},
		{"/home/draco/work/gotreesitter/harness_out/corpus_real_205_noscala_bounded/bash/small__release.sh", "bash"},
		{"/home/draco/work/gotreesitter/harness_out/corpus_real_205_noscala_bounded/jq/small__func-expr.jq", "jq"},
		{"/home/draco/work/gotreesitter/harness_out/corpus_real_205_noscala_bounded/php/small__literals.php", "php"},
		{"/home/draco/work/gotreesitter/harness_out/corpus_real_205_noscala_bounded/cmake/small__escape_sequence.txt", "cmake"},
		{"/home/draco/work/gotreesitter/harness_out/corpus_real_205_noscala_bounded/fennel/small__edge-cases.txt", "fennel"},
		{"/home/draco/work/gotreesitter/harness_out/corpus_real_205_noscala_bounded/caddy/small__named_routes.txt", "caddy"},
	}

	// Build lookup map from AllLanguages
	langByName := make(map[string]LangEntry)
	for _, entry := range AllLanguages() {
		langByName[entry.Name] = entry
	}

	for _, tc := range cases {
		t.Run(tc.langName, func(t *testing.T) {
			entry, ok := langByName[tc.langName]
			if !ok {
				t.Fatalf("language %q not found in AllLanguages()", tc.langName)
			}

			lang := entry.Language()
			if lang == nil {
				t.Fatalf("Language() returned nil for %q", tc.langName)
			}

			if _, err := os.Stat(tc.filePath); err != nil {
				t.Skipf("diag fixture not present: %v", err)
			}

			src, err := os.ReadFile(tc.filePath)
			if err != nil {
				t.Fatalf("failed to read %s: %v", tc.filePath, err)
			}

			// Determine backend via EvaluateParseSupport
			report := EvaluateParseSupport(entry, lang)

			parser := gotreesitter.NewParser(lang)

			var tree *gotreesitter.Tree
			var parseErr error
			switch report.Backend {
			case ParseBackendTokenSource:
				ts := entry.TokenSourceFactory(src, lang)
				tree, parseErr = parser.ParseWithTokenSource(src, ts)
			case ParseBackendDFA, ParseBackendDFAPartial:
				tree, parseErr = parser.Parse(src)
			default:
				// Try DFA as fallback
				tree, parseErr = parser.Parse(src)
			}

			fmt.Printf("\n=== %s (%s, %d bytes) ===\n", tc.langName, tc.filePath, len(src))
			fmt.Printf("  Backend: %s\n", report.Backend)

			if parseErr != nil {
				fmt.Printf("  Parse error: %v\n", parseErr)
			}

			if tree == nil {
				fmt.Printf("  Parse() returned nil tree!\n")
				return
			}

			// ParseRuntime diagnostics
			rt := tree.ParseRuntime()
			fmt.Printf("  ParseRuntime: %s\n", rt.Summary())

			// ParseStopReason
			fmt.Printf("  ParseStopReason: %s\n", tree.ParseStopReason())

			root := tree.RootNode()
			if root == nil {
				fmt.Printf("  RootNode() returned nil!\n")
				return
			}

			fmt.Printf("  Root symbol ID: %d\n", root.Symbol())
			fmt.Printf("  Root type: %q\n", root.Type(lang))
			fmt.Printf("  Root HasError: %v\n", root.HasError())
			fmt.Printf("  Root ChildCount: %d\n", root.ChildCount())
			fmt.Printf("  Root StartByte: %d\n", root.StartByte())
			fmt.Printf("  Root EndByte: %d\n", root.EndByte())

			// Show first 3 child types
			childTypes := make([]string, 0, 3)
			for i := 0; i < root.ChildCount() && i < 3; i++ {
				child := root.Child(i)
				if child != nil {
					childTypes = append(childTypes, fmt.Sprintf("%q (sym=%d, named=%v, err=%v, %d-%d)",
						child.Type(lang), child.Symbol(), child.IsNamed(), child.HasError(),
						child.StartByte(), child.EndByte()))
				}
			}
			fmt.Printf("  First 3 children: [%s]\n", strings.Join(childTypes, ", "))

			// Additional: check if symbol 65535 means error
			if root.Symbol() == 65535 {
				fmt.Printf("  WARNING: Root symbol=65535 indicates error/invalid tree!\n")
				fmt.Printf("  SymbolNames count: %d\n", len(lang.SymbolNames))
				if len(lang.SymbolNames) > 0 {
					fmt.Printf("  SymbolNames[0]: %q\n", lang.SymbolNames[0])
					if len(lang.SymbolNames) > 1 {
						fmt.Printf("  SymbolNames[1]: %q\n", lang.SymbolNames[1])
					}
				}
			}

			// Show source preview (first 100 bytes)
			preview := string(src)
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			fmt.Printf("  Source preview: %q\n", preview)

			tree.Release()
		})
	}
}
