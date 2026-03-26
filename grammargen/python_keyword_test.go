package grammargen

import (
	"os"
	"testing"
)

func TestPythonKeywordIdentificationIncludesSoftKeywords(t *testing.T) {
	gram := loadPythonGrammarJSONForTest(t)

	ng, err := Normalize(gram)
	if err != nil {
		t.Fatalf("normalize Python grammar: %v", err)
	}

	want := map[string]bool{
		"match":  false,
		"case":   false,
		"except": false,
	}
	for _, symID := range ng.KeywordSymbols {
		if symID >= 0 && symID < len(ng.Symbols) {
			if _, ok := want[ng.Symbols[symID].Name]; ok {
				want[ng.Symbols[symID].Name] = true
			}
		}
	}

	for name, found := range want {
		if !found {
			t.Fatalf("keyword %q missing from normalized keyword set", name)
		}
	}
}

func TestPythonReservedWordsSurviveNormalization(t *testing.T) {
	gram := loadPythonGrammarJSONForTest(t)
	if len(gram.ReservedWordSets) == 0 {
		t.Fatal("imported grammar has no reserved word sets")
	}

	ng, err := Normalize(gram)
	if err != nil {
		t.Fatalf("normalize Python grammar: %v", err)
	}
	if len(ng.ReservedWordSets) == 0 {
		t.Fatal("normalized grammar dropped reserved word sets")
	}
	if len(ng.ReservedWordSets[0]) == 0 {
		t.Fatal("normalized global reserved word set is empty")
	}

	want := map[string]bool{
		"if":     false,
		"except": false,
		"await":  false,
	}
	for _, symID := range ng.ReservedWordSets[0] {
		if symID >= 0 && symID < len(ng.Symbols) {
			if _, ok := want[ng.Symbols[symID].Name]; ok {
				want[ng.Symbols[symID].Name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("reserved word %q missing from normalized global set", name)
		}
	}
}

func TestPythonGenerateLanguageEmitsReservedWords(t *testing.T) {
	gram := loadPythonGrammarJSONForTest(t)

	lang, err := GenerateLanguage(gram)
	if err != nil {
		t.Fatalf("GenerateLanguage failed: %v", err)
	}
	if lang.LanguageVersion < 15 {
		t.Fatalf("LanguageVersion = %d, want >= 15", lang.LanguageVersion)
	}
	if lang.MaxReservedWordSetSize == 0 || len(lang.ReservedWords) == 0 {
		t.Fatalf("reserved words missing from generated language: stride=%d len=%d", lang.MaxReservedWordSetSize, len(lang.ReservedWords))
	}

	nonZeroSetIDs := 0
	for _, mode := range lang.LexModes {
		if mode.ReservedWordSetID > 0 {
			nonZeroSetIDs++
		}
	}
	if nonZeroSetIDs == 0 {
		t.Fatal("generated language has no lex modes with reserved word sets")
	}
}

func loadPythonGrammarJSONForTest(t *testing.T) *Grammar {
	t.Helper()

	jsonPath := "/tmp/python-locked-26855ea/src/grammar.json"
	if _, err := os.Stat(jsonPath); err != nil {
		jsonPath = "/tmp/grammar_parity/python/src/grammar.json"
	}
	source, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Skipf("Python grammar.json not available: %v", err)
	}

	gram, err := ImportGrammarJSON(source)
	if err != nil {
		t.Fatalf("import Python grammar.json: %v", err)
	}
	return gram
}
