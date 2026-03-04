package gotreesitter

import "testing"

func TestExternalLexerTokenDefaultsEndToCurrentPosition(t *testing.T) {
	l := newExternalLexer([]byte("#"), 0, 0, 0)
	l.Advance(false)
	l.SetResultSymbol(Symbol(2))

	tok, ok := l.token()
	if !ok {
		t.Fatal("token() returned !ok")
	}
	if tok.StartByte != 0 || tok.EndByte != 1 {
		t.Fatalf("token span = [%d,%d), want [0,1)", tok.StartByte, tok.EndByte)
	}
	if tok.Text != "#" {
		t.Fatalf("token text = %q, want %q", tok.Text, "#")
	}
}

func TestExternalLexerSkipThenConsumeKeepsCorrectSpan(t *testing.T) {
	l := newExternalLexer([]byte(" \""), 0, 0, 0)
	l.Advance(true)  // skip space
	l.Advance(false) // consume quote
	l.SetResultSymbol(Symbol(7))

	tok, ok := l.token()
	if !ok {
		t.Fatal("token() returned !ok")
	}
	if tok.StartByte != 1 || tok.EndByte != 2 {
		t.Fatalf("token span = [%d,%d), want [1,2)", tok.StartByte, tok.EndByte)
	}
	if tok.Text != `"` {
		t.Fatalf("token text = %q, want %q", tok.Text, `"`)
	}
}

func TestExternalLexerMarkBeforeSkipStillYieldsZeroWidthAtMark(t *testing.T) {
	l := newExternalLexer([]byte(" abc"), 0, 0, 0)
	l.MarkEnd()
	l.Advance(true) // move start beyond marked end
	l.SetResultSymbol(Symbol(9))

	tok, ok := l.token()
	if !ok {
		t.Fatal("token() returned !ok")
	}
	if tok.StartByte != 0 || tok.EndByte != 0 {
		t.Fatalf("token span = [%d,%d), want [0,0)", tok.StartByte, tok.EndByte)
	}
}
