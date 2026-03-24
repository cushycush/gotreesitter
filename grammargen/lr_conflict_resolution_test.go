package grammargen

import "testing"

func TestRRPickBestUsesSymbolVsNamedPrecedenceOrder(t *testing.T) {
	ng := &NormalizedGrammar{
		Symbols: []SymbolInfo{
			{Name: "declaration", Kind: SymbolNonterminal},
			{Name: "expression", Kind: SymbolNonterminal},
			{Name: "internal_module", Kind: SymbolNonterminal},
		},
		Productions: []Production{
			{LHS: 0, RHS: []int{2}, Prec: 13, HasExplicitPrec: true},
			{LHS: 1, RHS: []int{2}},
		},
		PrecedenceOrder: &precOrderTable{
			symbolPositions:    map[string]int{"expression": 2},
			symbolLevels:       map[string]int{"expression": 0},
			namedPrecPositions: map[int]int{13: 1},
		},
	}

	got := rrPickBest([]lrAction{
		{kind: lrReduce, prodIdx: 0},
		{kind: lrReduce, prodIdx: 1},
	}, ng)
	if len(got) != 1 || got[0].prodIdx != 1 {
		t.Fatalf("rrPickBest picked %+v, want expression reduce prodIdx=1", got)
	}
}

func TestResolveReduceReduceKeepsTypeValueSingleTokenAmbiguity(t *testing.T) {
	ng := &NormalizedGrammar{
		Symbols: []SymbolInfo{
			{Name: ">", Kind: SymbolTerminal},
			{Name: "string", Kind: SymbolNamedToken},
			{Name: "property_identifier", Kind: SymbolNonterminal},
			{Name: "predefined_type", Kind: SymbolNonterminal},
		},
		Productions: []Production{
			{LHS: 2, RHS: []int{1}},
			{LHS: 3, RHS: []int{1}},
		},
	}

	got, err := resolveActionConflict(0, []lrAction{
		{kind: lrReduce, prodIdx: 0},
		{kind: lrReduce, prodIdx: 1},
	}, ng)
	if err != nil {
		t.Fatalf("resolveActionConflict: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("resolved actions = %+v, want both reduces kept", got)
	}
}
