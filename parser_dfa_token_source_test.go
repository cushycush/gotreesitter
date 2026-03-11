package gotreesitter

import "testing"

type dualChoiceExternalScanner struct{}

func (dualChoiceExternalScanner) Create() any                           { return nil }
func (dualChoiceExternalScanner) Destroy(payload any)                   {}
func (dualChoiceExternalScanner) Serialize(payload any, buf []byte) int { return 0 }
func (dualChoiceExternalScanner) Deserialize(payload any, buf []byte)   {}
func (dualChoiceExternalScanner) Scan(payload any, lexer *ExternalLexer, valid []bool) bool {
	switch {
	case len(valid) > 0 && valid[0]:
		lexer.SetResultSymbol(Symbol(1))
		return true
	case len(valid) > 1 && valid[1]:
		lexer.SetResultSymbol(Symbol(2))
		return true
	default:
		return false
	}
}

func TestNextExternalTokenPrefersCandidateUsableByPrimaryState(t *testing.T) {
	lang := &Language{
		Name:            "bash",
		SymbolNames:     []string{"EOF", "first", "second"},
		ExternalScanner: dualChoiceExternalScanner{},
		ExternalSymbols: []Symbol{1, 2},
		ExternalLexStates: [][]bool{
			{false, false},
			{true, false},
			{false, true},
		},
		LexModes: []LexMode{
			{},
			{ExternalLexState: 1},
			{ExternalLexState: 2},
		},
		ParseActions: []ParseActionEntry{
			{},
			{Actions: []ParseAction{{Type: ParseActionShift, State: 1}}},
		},
	}
	lookup := func(state StateID, sym Symbol) uint16 {
		switch {
		case state == 1 && sym == 1:
			return 1
		case state == 2 && sym == 2:
			return 1
		default:
			return 0
		}
	}

	ts := acquireDFATokenSource(NewLexer(nil, []byte("x")), lang, lookup, nil)
	defer ts.Close()
	ts.SetParserState(2)
	ts.SetGLRStates([]StateID{2, 1})

	scored, ok := ts.nextGLRScoredExternalToken([]StateID{2, 1})
	if !ok {
		t.Fatal("expected scored external token")
	}
	if got, want := scored.Symbol, Symbol(2); got != want {
		t.Fatalf("scored external token symbol = %d, want %d", got, want)
	}

	tok, ok := ts.nextExternalToken()
	if !ok {
		t.Fatal("expected external token")
	}
	if got, want := tok.Symbol, Symbol(2); got != want {
		t.Fatalf("external token symbol = %d, want %d", got, want)
	}
}
