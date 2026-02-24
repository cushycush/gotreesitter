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
