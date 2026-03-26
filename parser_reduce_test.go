package gotreesitter

import "testing"

func TestBuildSingleTokenWrapperSymbols(t *testing.T) {
	lang := &Language{
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF"},
			{Name: "single_wrapper", Visible: true, Named: true},
			{Name: "multi_wrapper", Visible: true, Named: true},
			{Name: "statement", Visible: true, Named: true},
		},
		ParseActions: []ParseActionEntry{
			{Actions: []ParseAction{{Type: ParseActionReduce, Symbol: 1, ChildCount: 1, ProductionID: 10}}},
			{Actions: []ParseAction{{Type: ParseActionReduce, Symbol: 2, ChildCount: 1, ProductionID: 11}}},
			{Actions: []ParseAction{{Type: ParseActionReduce, Symbol: 2, ChildCount: 1, ProductionID: 12}}},
			{Actions: []ParseAction{{Type: ParseActionReduce, Symbol: 3, ChildCount: 2, ProductionID: 13}}},
			{Actions: []ParseAction{{Type: ParseActionShift, State: 1}}},
		},
	}

	got := buildSingleTokenWrapperSymbols(lang)
	if !got[1] {
		t.Fatal("expected single_wrapper to be marked as a single-token wrapper")
	}
	if got[2] {
		t.Fatal("did not expect multi_wrapper to be marked as a single-token wrapper")
	}
	if got[3] {
		t.Fatal("did not expect statement to be marked as a single-token wrapper")
	}
}

func TestCanCollapseNamedLeafWrapperSingleAnonymousToken(t *testing.T) {
	p := &Parser{
		language: &Language{
			SymbolMetadata: []SymbolMetadata{
				{Name: "EOF"},
				{Name: "optional_chain", Visible: true, Named: true},
				{Name: "?.", Visible: true, Named: false},
				{Name: "identifier", Visible: true, Named: true},
				{Name: "_hidden", Visible: false, Named: false},
			},
		},
	}

	if !p.canCollapseNamedLeafWrapper(1, 2) {
		t.Fatal("expected visible named wrapper over visible anonymous token to collapse")
	}
	if p.canCollapseNamedLeafWrapper(1, 3) {
		t.Fatal("did not expect visible named wrapper over visible named child to collapse")
	}
	if p.canCollapseNamedLeafWrapper(1, 4) {
		t.Fatal("did not expect visible named wrapper over invisible child to collapse")
	}
}

func TestCollapsibleUnarySelfReductionAliasesSingleAnonymousLeaf(t *testing.T) {
	lang := &Language{
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF"},
			{Name: "optional_chain", Visible: true, Named: true},
			{Name: "?.", Visible: true, Named: false},
		},
	}
	p := &Parser{
		language:                 lang,
		singleTokenWrapperSymbol: []bool{false, true, false},
	}
	arena := newNodeArena(arenaClassFull)
	child := newLeafNodeInArena(arena, 2, false, 1, 3, Point{Column: 1}, Point{Column: 3})
	entries := []stackEntry{{node: child}}
	act := ParseAction{Symbol: 1, ChildCount: 1}

	got := p.collapsibleUnarySelfReduction(act, Token{}, arena, entries, 0, 1, []*Node{child}, nil)
	if got == nil {
		t.Fatal("expected unary single-token reduction to collapse to named leaf")
	}
	if got.Symbol() != 1 {
		t.Fatalf("collapsed symbol = %d, want %d", got.Symbol(), 1)
	}
	if !got.IsNamed() {
		t.Fatal("collapsed node should be named")
	}
	if got.ChildCount() != 0 {
		t.Fatalf("collapsed child count = %d, want 0", got.ChildCount())
	}
	if got.StartByte() != 1 || got.EndByte() != 3 {
		t.Fatalf("collapsed span = [%d,%d), want [1,3)", got.StartByte(), got.EndByte())
	}
}
