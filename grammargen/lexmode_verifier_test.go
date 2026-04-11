package grammargen

import (
	"os"
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"
)

// TestLexModeVerifierFortran runs VerifyLexModeCompleteness on the Fortran
// grammar and prints a summary. Run with GOT_DEBUG_LEXMODE=1 to see full
// output. This is both a regression check (mismatches > 0 flags bugs) and
// a diagnostic for the current state.
func TestLexModeVerifierFortran(t *testing.T) {
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
	grammars.AdaptScannerForLanguage("fortran", lang)

	t.Logf("lang.LexStates count: %d", len(lang.LexStates))
	t.Logf("lang.LexModes count: %d", len(lang.LexModes))

	mismatches := VerifyLexModeCompleteness(lang)
	t.Logf("fortran: %d states with mismatched lex modes", len(mismatches))

	// Distribution by severity.
	buckets := map[int]int{}
	for _, m := range mismatches {
		b := len(m.MissingSyms)
		if b > 100 {
			b = 100 // cap at 100 for bucket display
		}
		buckets[b]++
	}
	t.Logf("  minor (1-5 missing): %d", buckets[1]+buckets[2]+buckets[3]+buckets[4]+buckets[5])
	moderate := 0
	for i := 6; i <= 50; i++ {
		moderate += buckets[i]
	}
	severe := 0
	for i := 51; i <= 100; i++ {
		severe += buckets[i]
	}
	t.Logf("  moderate (6-50 missing): %d", moderate)
	t.Logf("  severe (51-100+ missing): %d", severe)

	if len(mismatches) > 0 {
		// Print worst offender for context.
		worst := mismatches[0]
		for _, m := range mismatches {
			if len(m.MissingSyms) > len(worst.MissingSyms) {
				worst = m
			}
		}
		sample := worst.MissingNames
		if len(sample) > 10 {
			sample = append(sample[:10:10], "...")
		}
		t.Logf("worst: state=%d lexState=%d missing=%d shifts=%d first=%v",
			worst.State, worst.LexState, len(worst.MissingSyms), worst.TotalShifts, sample)
	}
}
