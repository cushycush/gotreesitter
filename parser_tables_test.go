package gotreesitter

import "testing"

func TestLookupActionIndexSmallUsesDenseTokenRows(t *testing.T) {
	lang := &Language{
		TokenCount:         16,
		LargeStateCount:    1,
		SmallParseTableMap: []uint32{0},
		// groupCount=2
		// action 11 for token symbols 1..9
		// action 17 for nonterminal symbol 20
		SmallParseTable: []uint16{
			2,
			11, 9, 1, 2, 3, 4, 5, 6, 7, 8, 9,
			17, 1, 20,
		},
	}

	p := &Parser{
		language:         lang,
		smallBase:        int(lang.LargeStateCount),
		smallLookup:      buildSmallLookup(lang),
		smallTokenLookup: buildSmallTokenLookup(lang),
	}

	if got, want := p.lookupActionIndexSmall(1, 1), uint16(11); got != want {
		t.Fatalf("lookupActionIndexSmall token 1 = %d, want %d", got, want)
	}
	if got, want := p.lookupActionIndexSmall(1, 9), uint16(11); got != want {
		t.Fatalf("lookupActionIndexSmall token 9 = %d, want %d", got, want)
	}
	if got := p.lookupActionIndexSmall(1, 10); got != 0 {
		t.Fatalf("lookupActionIndexSmall missing token = %d, want 0", got)
	}
	if got, want := p.lookupActionIndexSmall(1, 20), uint16(17); got != want {
		t.Fatalf("lookupActionIndexSmall nonterminal = %d, want %d", got, want)
	}
	if len(p.smallTokenLookup) != 1 || len(p.smallTokenLookup[0]) != int(lang.TokenCount) {
		t.Fatalf("smallTokenLookup row missing or wrong size: %+v", p.smallTokenLookup)
	}
}
