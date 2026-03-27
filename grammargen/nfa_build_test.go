package grammargen

import (
	"context"
	"testing"
)

func TestBuildSeqCoalescesAdjacentStrings(t *testing.T) {
	builder := newNFABuilder()
	seqFrag, err := builder.buildFromRule(Seq(Str("a"), Str("b"), Str("cd")))
	if err != nil {
		t.Fatalf("buildFromRule(seq): %v", err)
	}
	seqStateCount := len(builder.states)

	builder = newNFABuilder()
	stringFrag, err := builder.buildFromRule(Str("abcd"))
	if err != nil {
		t.Fatalf("buildFromRule(string): %v", err)
	}
	stringStateCount := len(builder.states)

	if seqStateCount != stringStateCount {
		t.Fatalf("seq state count = %d, want %d", seqStateCount, stringStateCount)
	}
	if seqFrag.end-seqFrag.start != stringFrag.end-stringFrag.start {
		t.Fatalf("seq fragment width = %d, want %d", seqFrag.end-seqFrag.start, stringFrag.end-stringFrag.start)
	}
}

func TestBuildChoiceSharesStringPrefixes(t *testing.T) {
	builder := newNFABuilder()
	frag, err := builder.buildFromRule(Choice(Str("ab"), Str("ac")))
	if err != nil {
		t.Fatalf("buildFromRule(choice): %v", err)
	}
	if got, want := len(builder.states), 5; got != want {
		t.Fatalf("state count = %d, want %d", got, want)
	}
	startTransitions := builder.states[frag.start].transitions
	if len(startTransitions) != 1 || startTransitions[0].lo != 'a' || startTransitions[0].hi != 'a' {
		t.Fatalf("start transitions = %#v, want single 'a' edge", startTransitions)
	}
}

func TestBuildPatternMergesAdjacentCharClassRanges(t *testing.T) {
	builder := newNFABuilder()
	frag, err := builder.buildFromRule(Pat("[ab]"))
	if err != nil {
		t.Fatalf("buildFromRule(pattern): %v", err)
	}
	transitions := builder.states[frag.start].transitions
	if got, want := len(transitions), 1; got != want {
		t.Fatalf("len(transitions) = %d, want %d", got, want)
	}
	if transitions[0].lo != 'a' || transitions[0].hi != 'b' {
		t.Fatalf("transition = [%q,%q], want ['a','b']", transitions[0].lo, transitions[0].hi)
	}
}

func TestBuildCombinedNFASharesSimpleStringPrefixes(t *testing.T) {
	n, err := buildCombinedNFA([]TerminalPattern{
		{SymbolID: 1, Rule: Str("+"), Priority: 0},
		{SymbolID: 2, Rule: Str("++"), Priority: 0},
		{SymbolID: 3, Rule: Str("-"), Priority: 0},
	})
	if err != nil {
		t.Fatalf("buildCombinedNFA: %v", err)
	}
	if got, want := len(n.states), 4; got != want {
		t.Fatalf("state count = %d, want %d", got, want)
	}

	dfa, err := subsetConstruction(context.Background(), n)
	if err != nil {
		t.Fatalf("subsetConstruction: %v", err)
	}
	plusState := -1
	minusState := -1
	for _, tr := range dfa[0].transitions {
		if tr.lo == '+' && tr.hi == '+' {
			plusState = tr.nextState
		}
		if tr.lo == '-' && tr.hi == '-' {
			minusState = tr.nextState
		}
	}
	if plusState < 0 || minusState < 0 {
		t.Fatalf("missing start transitions: plus=%d minus=%d", plusState, minusState)
	}
	if got, want := dfa[plusState].accept, 1; got != want {
		t.Fatalf("first '+' accept = %d, want %d", got, want)
	}
	plusPlusState := -1
	for _, tr := range dfa[plusState].transitions {
		if tr.lo == '+' && tr.hi == '+' {
			plusPlusState = tr.nextState
			break
		}
	}
	if plusPlusState < 0 {
		t.Fatal("missing second '+' transition")
	}
	if got, want := dfa[plusPlusState].accept, 2; got != want {
		t.Fatalf("'++' accept = %d, want %d", got, want)
	}
	if got, want := dfa[minusState].accept, 3; got != want {
		t.Fatalf("'-' accept = %d, want %d", got, want)
	}
}
