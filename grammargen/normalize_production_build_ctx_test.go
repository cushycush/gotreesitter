package grammargen

import (
	"strings"
	"testing"
)

func TestProductionBuildCtxCreateHelperNonterminalTracksOrigin(t *testing.T) {
	st := newSymbolTable()
	prodIDCounter := 7
	auxRules := make(map[string]*Rule)
	auxOrigin := make(map[string]string)
	ctx := newProductionBuildCtx(st, &prodIDCounter, auxRules, auxOrigin, 0, nil, nil)

	firstRule := Choice(Str("a"), Str("b"))
	secondRule := Sym("stmt")
	firstName := ctx.createHelperNonterminal("statement", "seq_choice", firstRule)
	secondName := ctx.createHelperNonterminal("statement", "seq_choice", secondRule)

	if firstName == secondName {
		t.Fatalf("expected unique helper names, got %q", firstName)
	}
	if got := auxOrigin[firstName]; got != "statement" {
		t.Fatalf("first helper origin = %q, want %q", got, "statement")
	}
	if got := auxOrigin[secondName]; got != "statement" {
		t.Fatalf("second helper origin = %q, want %q", got, "statement")
	}
	if _, ok := st.lookupNonterm(firstName); !ok {
		t.Fatalf("helper %q was not registered as a nonterminal", firstName)
	}
	if _, ok := st.lookupNonterm(secondName); !ok {
		t.Fatalf("helper %q was not registered as a nonterminal", secondName)
	}
	if auxRules[firstName] == firstRule {
		t.Fatalf("helper %q stored original rule pointer; want cloned rule", firstName)
	}
	if auxRules[secondName] == secondRule {
		t.Fatalf("helper %q stored original rule pointer; want cloned rule", secondName)
	}
	if auxRules[firstName] == nil || auxRules[secondName] == nil {
		t.Fatal("helper rule was not stored")
	}
}

func TestPendingAuxNamesSortsByOriginThenName(t *testing.T) {
	auxRules := map[string]*Rule{
		"_beta_choice2":  Str("b"),
		"_alpha_choice3": Str("a"),
		"_alpha_choice1": Str("c"),
		"_orphan_choice": Str("d"),
	}
	auxOrigin := map[string]string{
		"_beta_choice2":  "beta",
		"_alpha_choice3": "alpha",
		"_alpha_choice1": "alpha",
	}
	ruleOrderIdx := map[string]int{
		"alpha": 0,
		"beta":  1,
	}
	processed := map[string]bool{
		"_alpha_choice3": true,
	}

	got := pendingAuxNames(auxRules, auxOrigin, ruleOrderIdx, processed)
	want := []string{"_alpha_choice1", "_beta_choice2", "_orphan_choice"}
	if len(got) != len(want) {
		t.Fatalf("pendingAuxNames len=%d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pendingAuxNames[%d]=%q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestFlattenRuleWithCtxSeqChoiceHelperLowersWideSeq(t *testing.T) {
	st := newSymbolTable()
	lhs := st.addSymbol("start", SymbolInfo{Name: "start", Kind: SymbolNonterminal})
	st.addSymbol("a", SymbolInfo{Name: "a", Kind: SymbolTerminal})
	st.addSymbol("x", SymbolInfo{Name: "x", Kind: SymbolTerminal})
	st.addSymbol("y", SymbolInfo{Name: "y", Kind: SymbolTerminal})
	st.addSymbol("z", SymbolInfo{Name: "z", Kind: SymbolTerminal})
	st.addSymbol("b", SymbolInfo{Name: "b", Kind: SymbolTerminal})

	prodIDCounter := 0
	auxRules := make(map[string]*Rule)
	auxOrigin := make(map[string]string)
	ctx := newProductionBuildCtx(st, &prodIDCounter, auxRules, auxOrigin, 1, nil, nil)

	prods := flattenRuleWithCtx(ctx, Seq(Str("a"), Choice(Str("x"), Str("y"), Str("z")), Str("b")), lhs, "start")
	if len(prods) != 1 {
		t.Fatalf("flattenRuleWithCtx produced %d productions, want 1", len(prods))
	}
	if len(prods[0].RHS) != 3 {
		t.Fatalf("flattened production rhs len=%d, want 3", len(prods[0].RHS))
	}
	if got := len(auxRules); got != 1 {
		t.Fatalf("auxRules len=%d, want 1", got)
	}
	var helperName string
	for name := range auxRules {
		helperName = name
	}
	if got := auxOrigin[helperName]; got != "start" {
		t.Fatalf("helper origin=%q, want %q", got, "start")
	}
	helperID, ok := st.lookupNonterm(helperName)
	if !ok {
		t.Fatalf("helper %q not registered as nonterminal", helperName)
	}
	if got := prods[0].RHS[1]; got != helperID {
		t.Fatalf("production middle rhs=%d, want helper id %d", got, helperID)
	}
	if auxRules[helperName] == nil || auxRules[helperName].Kind != RuleChoice {
		t.Fatalf("helper rule=%v, want direct choice rule", auxRules[helperName])
	}
}

func TestFlattenRuleWithCtxSeqChoiceHelperPreservesFieldWrapper(t *testing.T) {
	st := newSymbolTable()
	lhs := st.addSymbol("start", SymbolInfo{Name: "start", Kind: SymbolNonterminal})
	st.addSymbol("x", SymbolInfo{Name: "x", Kind: SymbolTerminal})
	st.addSymbol("y", SymbolInfo{Name: "y", Kind: SymbolTerminal})
	st.addSymbol("z", SymbolInfo{Name: "z", Kind: SymbolTerminal})

	prodIDCounter := 0
	auxRules := make(map[string]*Rule)
	auxOrigin := make(map[string]string)
	ctx := newProductionBuildCtx(st, &prodIDCounter, auxRules, auxOrigin, 1, nil, nil)

	prods := flattenRuleWithCtx(ctx, Seq(Field("value", Choice(Str("x"), Str("y"), Str("z")))), lhs, "start")
	if len(prods) != 1 {
		t.Fatalf("flattenRuleWithCtx produced %d productions, want 1", len(prods))
	}
	if len(prods[0].Fields) != 1 {
		t.Fatalf("field count=%d, want 1", len(prods[0].Fields))
	}
	if got := prods[0].Fields[0]; got.ChildIndex != 0 || got.FieldName != "value" {
		t.Fatalf("field assignment=%+v, want child 0 field value", got)
	}
	if len(auxRules) != 1 {
		t.Fatalf("auxRules len=%d, want 1", len(auxRules))
	}
}

func TestNormalizeSeqChoiceHelperThresholdEmitsHelperProductions(t *testing.T) {
	g := &Grammar{
		Name: "test_seq_helper",
		Rules: map[string]*Rule{
			"start": Seq(Str("a"), Choice(Str("x"), Str("y"), Str("z")), Str("b")),
		},
		RuleOrder:                []string{"start"},
		SeqChoiceHelperThreshold: 1,
	}

	ng, err := Normalize(g)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	startID := -1
	helperID := -1
	for i, sym := range ng.Symbols {
		switch {
		case sym.Name == "start":
			startID = i
		case strings.HasPrefix(sym.Name, "_start_seq_choice"):
			helperID = i
		}
	}
	if startID < 0 || helperID < 0 {
		t.Fatalf("expected start/helper symbols, got start=%d helper=%d", startID, helperID)
	}

	startProdCount := 0
	helperProdCount := 0
	for _, p := range ng.Productions {
		switch p.LHS {
		case startID:
			startProdCount++
			if len(p.RHS) != 3 || p.RHS[1] != helperID {
				t.Fatalf("start production=%+v, want helper in middle rhs", p)
			}
		case helperID:
			helperProdCount++
		}
	}
	if startProdCount != 1 {
		t.Fatalf("start production count=%d, want 1", startProdCount)
	}
	if helperProdCount != 3 {
		t.Fatalf("helper production count=%d, want 3", helperProdCount)
	}
}

func TestFlattenRuleWithCtxSeqChoiceHelperSkipsNestedSeqChildChoices(t *testing.T) {
	st := newSymbolTable()
	lhs := st.addSymbol("start", SymbolInfo{Name: "start", Kind: SymbolNonterminal})
	st.addSymbol("x", SymbolInfo{Name: "x", Kind: SymbolTerminal})
	st.addSymbol("y", SymbolInfo{Name: "y", Kind: SymbolTerminal})
	st.addSymbol("z", SymbolInfo{Name: "z", Kind: SymbolTerminal})

	prodIDCounter := 0
	auxRules := make(map[string]*Rule)
	auxOrigin := make(map[string]string)
	ctx := newProductionBuildCtx(st, &prodIDCounter, auxRules, auxOrigin, 1, nil, nil)

	prods := flattenRuleWithCtx(ctx, Seq(Seq(Choice(Str("x"), Str("y")), Str("z"))), lhs, "start")
	if len(prods) != 2 {
		t.Fatalf("flattenRuleWithCtx produced %d productions, want 2 without helper lowering", len(prods))
	}
	if len(auxRules) != 0 {
		t.Fatalf("auxRules len=%d, want 0 for nested seq child", len(auxRules))
	}
}

func TestFlattenRuleWithCtxSeqChoiceHelperExcludeSkipsParent(t *testing.T) {
	st := newSymbolTable()
	lhs := st.addSymbol("start", SymbolInfo{Name: "start", Kind: SymbolNonterminal})
	st.addSymbol("a", SymbolInfo{Name: "a", Kind: SymbolTerminal})
	st.addSymbol("x", SymbolInfo{Name: "x", Kind: SymbolTerminal})
	st.addSymbol("y", SymbolInfo{Name: "y", Kind: SymbolTerminal})
	st.addSymbol("z", SymbolInfo{Name: "z", Kind: SymbolTerminal})
	st.addSymbol("b", SymbolInfo{Name: "b", Kind: SymbolTerminal})

	prodIDCounter := 0
	auxRules := make(map[string]*Rule)
	auxOrigin := make(map[string]string)
	ctx := newProductionBuildCtx(st, &prodIDCounter, auxRules, auxOrigin, 1, []string{"start"}, nil)

	prods := flattenRuleWithCtx(ctx, Seq(Str("a"), Choice(Str("x"), Str("y"), Str("z")), Str("b")), lhs, "start")
	if len(prods) != 3 {
		t.Fatalf("flattenRuleWithCtx produced %d productions, want 3 without helper lowering", len(prods))
	}
	if len(auxRules) != 0 {
		t.Fatalf("auxRules len=%d, want 0 when parent excluded", len(auxRules))
	}
}

func TestFlattenRuleWithCtxSeqChoiceHelperForceLowersBelowThreshold(t *testing.T) {
	st := newSymbolTable()
	lhs := st.addSymbol("select_case_statement", SymbolInfo{Name: "select_case_statement", Kind: SymbolNonterminal})
	st.addSymbol("start", SymbolInfo{Name: "start", Kind: SymbolTerminal})
	st.addSymbol("a", SymbolInfo{Name: "a", Kind: SymbolTerminal})
	st.addSymbol("b", SymbolInfo{Name: "b", Kind: SymbolTerminal})
	st.addSymbol("c", SymbolInfo{Name: "c", Kind: SymbolTerminal})
	st.addSymbol("end", SymbolInfo{Name: "end", Kind: SymbolTerminal})

	prodIDCounter := 0
	auxRules := make(map[string]*Rule)
	auxOrigin := make(map[string]string)
	ctx := newProductionBuildCtx(st, &prodIDCounter, auxRules, auxOrigin, 1024, nil, []string{"select_case_statement"})

	prods := flattenRuleWithCtx(ctx, Seq(Str("start"), Choice(Str("a"), Str("b"), Str("c")), Str("end")), lhs, "select_case_statement")
	if len(prods) != 1 {
		t.Fatalf("flattenRuleWithCtx produced %d productions, want 1 with force lowering", len(prods))
	}
	if len(auxRules) != 1 {
		t.Fatalf("auxRules len=%d, want 1 with force lowering", len(auxRules))
	}
}

func TestNormalizeChoiceLiftForceRestrictsRepeatBodyExtraction(t *testing.T) {
	g := &Grammar{
		Name: "test_choice_lift_force",
		Rules: map[string]*Rule{
			"forced": Seq(Str("["), Repeat(Choice(Str("a"), Str("b"), Str("c"))), Str("]")),
			"other":  Seq(Str("{"), Repeat(Choice(Str("a"), Str("b"), Str("c"))), Str("}")),
		},
		RuleOrder:           []string{"forced", "other"},
		ChoiceLiftThreshold: 2,
		ChoiceLiftForce:     []string{"forced"},
	}

	ng, err := Normalize(g)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	var sawForcedLift bool
	var sawOtherLift bool
	for _, sym := range ng.Symbols {
		switch {
		case strings.HasPrefix(sym.Name, "_forced_choice_lift"):
			sawForcedLift = true
		case strings.HasPrefix(sym.Name, "_other_choice_lift"):
			sawOtherLift = true
		}
	}
	if !sawForcedLift {
		t.Fatal("expected forced rule to get a choice-lift helper")
	}
	if sawOtherLift {
		t.Fatal("expected non-forced rule to skip choice-lift helper")
	}
}
