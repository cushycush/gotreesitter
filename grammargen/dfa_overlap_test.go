package grammargen

import (
	"context"
	"testing"

	"github.com/odvcencio/gotreesitter"
)

func TestBuildLexDFAPrefersLongerStringOverSingleCharPattern(t *testing.T) {
	lexStates, modeOffsets, err := buildLexDFA(
		context.Background(),
		[]TerminalPattern{
			{SymbolID: 1, Rule: Pat(`[^\n]`), Priority: 0},
			{SymbolID: 2, Rule: Str("*/"), Priority: 0},
		},
		nil,
		nil,
		[]lexModeSpec{{
			validSymbols: map[int]bool{1: true, 2: true},
		}},
	)
	if err != nil {
		t.Fatalf("buildLexDFA: %v", err)
	}
	if len(modeOffsets) != 1 {
		t.Fatalf("len(modeOffsets) = %d, want 1", len(modeOffsets))
	}

	lexer := gotreesitter.NewLexer(lexStates, []byte("*/"))
	tok := lexer.Next(uint32(modeOffsets[0]))
	if got, want := tok.Symbol, gotreesitter.Symbol(2); got != want {
		t.Fatalf("token symbol = %d, want %d", got, want)
	}
	if got, want := tok.EndByte, uint32(2); got != want {
		t.Fatalf("token end = %d, want %d", got, want)
	}
}

func TestCollectTransitionMovesMatchesLegacyRanges(t *testing.T) {
	n := &nfa{
		states: []nfaState{
			{transitions: []nfaTransition{
				{lo: 'a', hi: 'f', nextState: 1},
				{lo: 'd', hi: 'h', nextState: 2},
			}},
			{transitions: []nfaTransition{
				{lo: 'b', hi: 'e', nextState: 3},
			}},
			{},
			{},
		},
		start: 0,
	}

	states := []int{0, 1}
	legacyRanges := collectTransitionRanges(n, states)
	moves := collectTransitionMoves(n, states)
	if len(moves) != len(legacyRanges) {
		t.Fatalf("len(moves) = %d, want %d", len(moves), len(legacyRanges))
	}

	for i, move := range moves {
		if move.lo != legacyRanges[i].lo || move.hi != legacyRanges[i].hi {
			t.Fatalf("move[%d] range = [%q,%q], want [%q,%q]", i, move.lo, move.hi, legacyRanges[i].lo, legacyRanges[i].hi)
		}
		want := moveTargets(n, states, legacyRanges[i].lo, legacyRanges[i].hi)
		if !sameIntSlice(move.targets, want) {
			t.Fatalf("move[%d] targets = %v, want %v", i, move.targets, want)
		}
	}
}

func TestBuildModeKeyOrderIndependent(t *testing.T) {
	a := map[int]bool{2: true, 7: true, 65: true}
	b := map[int]bool{65: true, 2: true, 7: true}

	if gotA, gotB := buildModeKey(a, false), buildModeKey(b, false); gotA != gotB {
		t.Fatalf("buildModeKey order mismatch: %q != %q", gotA, gotB)
	}
}

func TestBuildModeKeyDistinguishesSkipWhitespace(t *testing.T) {
	syms := map[int]bool{3: true, 9: true}

	if gotSkip, gotNoSkip := buildModeKey(syms, true), buildModeKey(syms, false); gotSkip == gotNoSkip {
		t.Fatal("expected skip flag to affect the mode key")
	}
}
