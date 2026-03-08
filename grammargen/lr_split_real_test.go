// grammargen/lr_split_real_test.go
package grammargen

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSplitOracleRealGrammars(t *testing.T) {
	root := os.Getenv("GTS_GRAMMARGEN_REAL_CORPUS_ROOT")
	if root == "" {
		root = "/tmp/grammar_parity"
	}

	// Grammars known to have merge pathology (>400 productions, LALR path).
	targets := []string{
		"javascript", "python", "php", "scala", "c",
		"elixir", "ocaml", "sql", "haskell", "yaml",
	}

	for _, lang := range targets {
		t.Run(lang, func(t *testing.T) {
			grammarDir := filepath.Join(root, lang)
			jsPath := filepath.Join(grammarDir, "src", "grammar.json")
			if _, err := os.Stat(jsPath); err != nil {
				// Try alternate paths.
				alts := []string{
					filepath.Join(grammarDir, "grammar.js"),
					filepath.Join(grammarDir, "grammars", lang, "src", "grammar.json"),
				}
				found := false
				for _, alt := range alts {
					if _, err := os.Stat(alt); err == nil {
						jsPath = alt
						found = true
						break
					}
				}
				if !found {
					t.Skipf("grammar not available at %s", grammarDir)
				}
			}

			data, err := os.ReadFile(jsPath)
			if err != nil {
				t.Skipf("read failed: %v", err)
			}

			g, err := ImportGrammarJSON(data)
			if err != nil {
				t.Skipf("import failed: %v", err)
			}

			report, err := GenerateWithReport(g)
			if err != nil {
				t.Skipf("generate failed: %v", err)
			}

			// Log summary.
			totalConflicts := len(report.Conflicts)
			glrConflicts := 0
			mergedConflicts := 0
			for _, c := range report.Conflicts {
				if c.Resolution == "GLR (multiple actions kept)" {
					glrConflicts++
				}
				if c.IsMergedState {
					mergedConflicts++
				}
			}

			t.Logf("SPLIT ORACLE: %s", lang)
			t.Logf("  states=%d, conflicts=%d, glr=%d, merged=%d",
				report.StateCount, totalConflicts, glrConflicts, mergedConflicts)
			t.Logf("  split_candidates=%d", len(report.SplitCandidates))

			for i, c := range report.SplitCandidates {
				if i >= 20 {
					t.Logf("  ... and %d more", len(report.SplitCandidates)-20)
					break
				}
				t.Logf("  candidate[%d]: state=%d merges=%d kind=%v sym=%d reason=%s",
					i, c.stateIdx, c.mergeCount, c.conflictKind, c.lookaheadSym, c.reason)
			}

			// Write summary to a temp file for collection.
			summaryPath := fmt.Sprintf("/tmp/split_oracle_%s.txt", lang)
			f, err := os.Create(summaryPath)
			if err == nil {
				fmt.Fprintf(f, "lang=%s states=%d conflicts=%d glr=%d merged=%d candidates=%d\n",
					lang, report.StateCount, totalConflicts, glrConflicts, mergedConflicts,
					len(report.SplitCandidates))
				for _, c := range report.SplitCandidates {
					fmt.Fprintf(f, "  state=%d merges=%d kind=%v sym=%d\n",
						c.stateIdx, c.mergeCount, c.conflictKind, c.lookaheadSym)
				}
				f.Close()
			}
		})
	}
}
