package grammargen

import "testing"

func TestResolveActionConflictKeepsRecursiveRepeatShiftReduce(t *testing.T) {
	ng := &NormalizedGrammar{
		Symbols: []SymbolInfo{
			{Name: "end", Kind: SymbolTerminal},
			{Name: "item", Kind: SymbolNamedToken},
			{Name: "list_repeat1", Kind: SymbolNonterminal},
		},
		Productions: []Production{
			{LHS: 2, RHS: []int{2, 1}},
		},
	}

	actions := []lrAction{
		{kind: lrReduce, prodIdx: 0, lhsSym: 2},
		{kind: lrShift, state: 17, lhsSym: 1},
	}

	got, err := resolveActionConflict(1, actions, ng)
	if err != nil {
		t.Fatalf("resolveActionConflict: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("resolved len=%d, want 2", len(got))
	}
	if got[0].kind != lrReduce {
		t.Fatalf("resolved[0].kind=%v, want reduce", got[0].kind)
	}
	if got[1].kind != lrShift || int(got[1].state) != 17 || !got[1].repeat {
		t.Fatalf("resolved shift=%+v, want repetition-marked shift", got[1])
	}
}
