package grammargen

import "testing"

func TestNamedStringChoiceTokenBecomesKeyword(t *testing.T) {
	g := NewGrammar("named_string_choice_keyword")
	g.Define("source_file", Sym("predefined_type"))
	g.Define("predefined_type", Token(Choice(
		Str("int"),
		Str("string"),
		Str("nint"),
	)))
	g.Define("identifier", Pat(`[A-Za-z_][A-Za-z0-9_]*`))
	g.SetWord("identifier")

	ng, err := Normalize(g)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	predefinedTypeSym := -1
	for i, sym := range ng.Symbols {
		if sym.Name == "predefined_type" {
			predefinedTypeSym = i
			break
		}
	}
	if predefinedTypeSym < 0 {
		t.Fatal("predefined_type symbol not found")
	}

	foundKeyword := false
	for _, symID := range ng.KeywordSymbols {
		if symID == predefinedTypeSym {
			foundKeyword = true
			break
		}
	}
	if !foundKeyword {
		t.Fatalf("predefined_type sym %d missing from keyword set", predefinedTypeSym)
	}

	for _, term := range ng.Terminals {
		if term.SymbolID == predefinedTypeSym {
			t.Fatalf("predefined_type sym %d still present in main terminals", predefinedTypeSym)
		}
	}

	foundEntry := false
	for _, entry := range ng.KeywordEntries {
		if entry.SymbolID == predefinedTypeSym {
			foundEntry = true
			break
		}
	}
	if !foundEntry {
		t.Fatalf("predefined_type sym %d missing from keyword entries", predefinedTypeSym)
	}
}
