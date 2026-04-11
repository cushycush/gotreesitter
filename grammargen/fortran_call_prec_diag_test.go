package grammargen

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestFortranCallExpressionShiftPrecOnLParen imports the Fortran grammar.json
// directly and walks the LR action table to find states where the shift on `(`
// comes from the call_expression closure chain (call_expression →
// _expression . call_expression_repeat1). It logs the resulting shift prec
// so we can verify the hypothesis from baseline-2026-04-10 that the shift
// prec is stuck at 0 instead of 80.
//
// This test is host-only: it reads from a read-only host path and is
// skipped in CI / Docker where that path doesn't exist.
func TestFortranCallExpressionShiftPrecOnLParen(t *testing.T) {
	const grammarPath = "/home/draco/grammar_parity_ro/fortran/src/grammar.json"
	data, err := os.ReadFile(grammarPath)
	if err != nil {
		t.Skipf("skip: %s not available: %v", grammarPath, err)
	}

	g, err := ImportGrammarJSON(data)
	if err != nil {
		t.Fatalf("ImportGrammarJSON: %v", err)
	}

	ng, err := Normalize(g)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	// Pass trackProvenance=true to DEFER conflict resolution. Otherwise shifts
	// that would lose to reduces get stripped during table build and we can't
	// observe whether the shift had the right precedence to win.
	tables, _, err := buildLRTablesInternal(context.Background(), ng, true)
	if err != nil {
		t.Fatalf("buildLRTablesInternal: %v", err)
	}

	// Locate symbol IDs.
	lparenSym := -1
	callExprSym := -1
	argListSym := -1
	callRepeatSym := -1
	for i, s := range ng.Symbols {
		switch {
		case s.Name == "(" && s.Kind == SymbolTerminal:
			lparenSym = i
		case s.Name == "call_expression":
			callExprSym = i
		case s.Name == "argument_list":
			argListSym = i
		case strings.HasPrefix(s.Name, "call_expression_repeat"):
			callRepeatSym = i
		}
	}
	if lparenSym < 0 || callExprSym < 0 {
		t.Fatalf("missing expected symbols: ( = %d, call_expression = %d, argument_list = %d, call_expression_repeat1 = %d",
			lparenSym, callExprSym, argListSym, callRepeatSym)
	}
	// Collect all symbols named "(" — Fortran has both an immediate-token
	// `(` and a regular-token `(` variant.
	var allLparens []int
	for i, s := range ng.Symbols {
		if s.Name == "(" {
			allLparens = append(allLparens, i)
		}
	}

	// Walk states, find shift actions on `(` and categorise by lhsSym.
	type hit struct {
		state int
		prec  int32
		lhsID int32
		lhsN  string
		lhss  []string
	}
	var hits []hit
	states := make([]int, 0, len(tables.ActionTable))
	for s := range tables.ActionTable {
		states = append(states, s)
	}
	sort.Ints(states)

	// Walk ALL `(` variants.
	totalShiftsOnLParen := 0
	statesWithShiftOnLParen := 0
	lparenSyms := allLparens
	if len(lparenSyms) == 0 {
		lparenSyms = []int{lparenSym}
	}
	for _, state := range states {
		row := tables.ActionTable[state]
		var acts []lrAction
		for _, lp := range lparenSyms {
			acts = append(acts, row[lp]...)
		}
		hasShift := false
		for _, a := range acts {
			if a.kind == lrShift {
				totalShiftsOnLParen++
				hasShift = true
			}
		}
		if hasShift {
			statesWithShiftOnLParen++
		}
		for _, a := range acts {
			if a.kind != lrShift {
				continue
			}
			lhsName := ""
			if int(a.lhsSym) >= 0 && int(a.lhsSym) < len(ng.Symbols) {
				lhsName = ng.Symbols[a.lhsSym].Name
			}
			lhss := make([]string, 0, len(a.lhsSyms))
			for _, l := range a.lhsSyms {
				if int(l) >= 0 && int(l) < len(ng.Symbols) {
					lhss = append(lhss, ng.Symbols[l].Name)
				}
			}
			_ = lhsName
			_ = lhss
			// Keep ALL shifts on `(` — we'll categorize by prec.
			hits = append(hits, hit{
				state: state,
				prec:  a.prec,
				lhsID: a.lhsSym,
				lhsN:  lhsName,
				lhss:  lhss,
			})
		}
	}

	t.Logf("total shift actions on `(` = %d across %d states (total states = %d)",
		totalShiftsOnLParen, statesWithShiftOnLParen, len(states))

	// REGRESSION ASSERTION: with the two-pass closure-chain propagation fix,
	// a non-trivial count of `(` shift actions whose lhsSyms includes
	// call_expression_repeat150 must carry prec=80 (call_expression's
	// explicit precedence). Before the fix this count was ZERO because the
	// kernel item `call_expression → _expression . call_expression_repeat150`
	// was processed before closure items populated lhsSyms, so its prec
	// never reached the shift action.
	//
	// A minority of states (repeat-continuation states where the kernel item
	// `call_expression_repeat150 → call_expression_repeat150 . call_expression_repeat150`
	// is present but the call_expression kernel is already reduced) still
	// carry prec=0. That is a KNOWN follow-up limitation — samples 5 and 26
	// from the real-corpus baseline only need the initial-entry fix.
	upgradedCount := 0
	unupgradedCount := 0
	for _, h := range hits {
		containsCallRepeat := strings.HasPrefix(h.lhsN, "call_expression_repeat")
		for _, l := range h.lhss {
			if strings.HasPrefix(l, "call_expression_repeat") {
				containsCallRepeat = true
				break
			}
		}
		if !containsCallRepeat {
			continue
		}
		if h.prec >= 80 {
			upgradedCount++
		} else {
			unupgradedCount++
		}
	}
	t.Logf("call_expression_repeat150 shifts: upgraded=%d, not-upgraded (known repeat-chain gap)=%d",
		upgradedCount, unupgradedCount)
	if upgradedCount < 50 {
		t.Errorf("expected >= 50 `(` shifts to be upgraded to prec=80 via closure-chain propagation, got %d — the two-pass fix in propagateEntryShiftMetadataForState may have regressed", upgradedCount)
	}
}
