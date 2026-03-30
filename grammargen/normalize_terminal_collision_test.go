package grammargen

import "testing"

func TestNormalizeSeparatesAnonymousStringAndPatternTerminals(t *testing.T) {
	g := NewGrammar("terminal_collision")
	g.Define("source_file", Seq(
		Choice(
			Str(".*"),
			Pat(`.*`),
		),
		Str(";"),
	))

	ng, err := Normalize(g)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	var stringSymID int = -1
	collisionSymIDs := map[int]struct{}{}
	for _, term := range ng.Terminals {
		if term.Rule == nil {
			continue
		}
		if ng.Symbols[term.SymbolID].Name == ".*" {
			collisionSymIDs[term.SymbolID] = struct{}{}
		}
		if term.Rule.Kind == RuleString && term.Rule.Value == ".*" {
			stringSymID = term.SymbolID
		}
	}

	if stringSymID < 0 {
		t.Fatal("missing anonymous string terminal for \".*\"")
	}
	if ng.Symbols[stringSymID].Name != ".*" {
		t.Fatalf("string terminal display name = %q, want %q", ng.Symbols[stringSymID].Name, ".*")
	}
	if len(collisionSymIDs) != 2 {
		t.Fatalf("expected 2 distinct terminals named %q, got %d", ".*", len(collisionSymIDs))
	}
}
