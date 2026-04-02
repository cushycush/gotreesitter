package grammargen

import "testing"

func TestApplyImportGrammarShapeHintsFortran(t *testing.T) {
	g := NewGrammar("fortran")

	applyImportGrammarShapeHints(g)

	if !g.BinaryRepeatMode {
		t.Fatal("BinaryRepeatMode = false, want true for fortran")
	}
	if g.SeqChoiceHelperThreshold != 1 {
		t.Fatalf("SeqChoiceHelperThreshold = %d, want 1 for fortran", g.SeqChoiceHelperThreshold)
	}
}

func TestApplyImportGrammarShapeHintsJavaScriptFamilyKeepsBinaryRepeatOnly(t *testing.T) {
	for _, name := range []string{"javascript", "typescript", "tsx", "sql"} {
		t.Run(name, func(t *testing.T) {
			g := NewGrammar(name)

			applyImportGrammarShapeHints(g)

			if !g.BinaryRepeatMode {
				t.Fatal("BinaryRepeatMode = false, want true")
			}
			if g.SeqChoiceHelperThreshold != 0 {
				t.Fatalf("SeqChoiceHelperThreshold = %d, want 0", g.SeqChoiceHelperThreshold)
			}
		})
	}
}
