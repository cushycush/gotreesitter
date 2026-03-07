package gotreesitter

import "testing"

func TestParseWithoutDFALexerReturnsError(t *testing.T) {
	lang := &Language{Name: "no_dfa", InitialState: 1}
	parser := NewParser(lang)

	_, err := parser.Parse([]byte("anything"))
	if err == nil {
		t.Fatal("expected error for language without DFA lexer")
	}
}

func TestParseIncrementalWithoutDFALexerReturnsError(t *testing.T) {
	lang := &Language{Name: "no_dfa", InitialState: 1}
	parser := NewParser(lang)
	oldTree := NewTree(nil, []byte("old"), lang)

	_, err := parser.ParseIncremental([]byte("new"), oldTree)
	if err == nil {
		t.Fatal("expected error for language without DFA lexer")
	}
}

func TestParseWithIncompatibleLanguageVersionReturnsError(t *testing.T) {
	lang := buildArithmeticLanguage()
	lang.LanguageVersion = RuntimeLanguageVersion + 1
	parser := NewParser(lang)

	_, err := parser.Parse([]byte("1+2"))
	if err == nil {
		t.Fatal("expected error for incompatible language version")
	}
}

func TestParseWithTokenSourceIncompatibleLanguageVersionReturnsError(t *testing.T) {
	lang := buildArithmeticLanguage()
	lang.LanguageVersion = RuntimeLanguageVersion + 1
	parser := NewParser(lang)
	ts := &dfaTokenSource{
		lexer:             NewLexer(lang.LexStates, []byte("1+2")),
		language:          lang,
		lookupActionIndex: parser.lookupActionIndex,
	}

	_, err := parser.ParseWithTokenSource([]byte("1+2"), ts)
	if err == nil {
		t.Fatal("expected error for incompatible language version")
	}
}

func TestParseIncrementalWithIncompatibleLanguageVersionReturnsError(t *testing.T) {
	lang := buildArithmeticLanguage()
	lang.LanguageVersion = RuntimeLanguageVersion + 1
	parser := NewParser(lang)
	oldTree := NewTree(nil, []byte("1+2"), lang)

	_, err := parser.ParseIncremental([]byte("1+3"), oldTree)
	if err == nil {
		t.Fatal("expected error for incompatible language version")
	}
}

func TestParseWithNilLanguageReturnsError(t *testing.T) {
	parser := &Parser{}

	_, err := parser.Parse([]byte("anything"))
	if err == nil {
		t.Fatal("expected error for nil language")
	}
	if err != ErrNoLanguage {
		t.Errorf("expected ErrNoLanguage, got: %v", err)
	}
}

func TestParseIncrementalWithNilLanguageReturnsError(t *testing.T) {
	parser := &Parser{}
	oldTree := NewTree(nil, []byte("old"), nil)

	_, err := parser.ParseIncremental([]byte("new"), oldTree)
	if err == nil {
		t.Fatal("expected error for nil language")
	}
	if err != ErrNoLanguage {
		t.Errorf("expected ErrNoLanguage, got: %v", err)
	}
}

func TestParseWithTokenSourceNilLanguageReturnsError(t *testing.T) {
	parser := &Parser{}

	_, err := parser.ParseWithTokenSource([]byte("anything"), nil)
	if err == nil {
		t.Fatal("expected error for nil language")
	}
	if err != ErrNoLanguage {
		t.Errorf("expected ErrNoLanguage, got: %v", err)
	}
}

func TestParseIncrementalWithTokenSourceNilLanguageReturnsError(t *testing.T) {
	parser := &Parser{}
	oldTree := NewTree(nil, []byte("old"), nil)

	_, err := parser.ParseIncrementalWithTokenSource([]byte("new"), oldTree, nil)
	if err == nil {
		t.Fatal("expected error for nil language")
	}
	if err != ErrNoLanguage {
		t.Errorf("expected ErrNoLanguage, got: %v", err)
	}
}

func TestAllowRepeatedZeroWidthExternalImplicitEndTag(t *testing.T) {
	lang := &Language{
		SymbolNames:     []string{"end", "_implicit_end_tag", "_other"},
		ExternalSymbols: []Symbol{1, 2},
	}
	d := &dfaTokenSource{language: lang}

	if !d.allowRepeatedZeroWidthExternalSymbol(1) {
		t.Fatal("expected _implicit_end_tag to be repeatable")
	}
	if d.allowRepeatedZeroWidthExternalSymbol(2) {
		t.Fatal("expected non-implicit external symbol to remain guarded")
	}
}

func TestTrackZeroWidthExternalRepeatableSymbolClearsLoopGuard(t *testing.T) {
	lang := &Language{
		SymbolNames:     []string{"end", "_implicit_end_tag"},
		ExternalSymbols: []Symbol{1},
	}
	d := &dfaTokenSource{
		language:     lang,
		lexer:        &Lexer{},
		state:        7,
		extZeroPos:   12,
		extZeroState: 7,
		extZeroTried: []bool{true},
	}

	d.trackZeroWidthExternalToken(Token{Symbol: 1, StartByte: 5, EndByte: 5})

	if got := d.extZeroPos; got != -1 {
		t.Fatalf("extZeroPos = %d, want -1", got)
	}
	if got := len(d.extZeroTried); got != 0 {
		t.Fatalf("len(extZeroTried) = %d, want 0", got)
	}
}
