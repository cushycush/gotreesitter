package gotreesitter

import "testing"

type stubTokenSource struct {
	tokens []Token
	i      int
	state  StateID
}

func (s *stubTokenSource) Next() Token {
	if s.i >= len(s.tokens) {
		return Token{}
	}
	t := s.tokens[s.i]
	s.i++
	return t
}

func (s *stubTokenSource) SkipToByte(offset uint32) Token {
	for {
		t := s.Next()
		if t.Symbol == 0 || t.StartByte >= offset {
			return t
		}
	}
}

func (s *stubTokenSource) SkipToByteWithPoint(offset uint32, _ Point) Token {
	return s.SkipToByte(offset)
}

func (s *stubTokenSource) SetParserState(state StateID) {
	s.state = state
}

func TestNormalizeIncludedRanges(t *testing.T) {
	in := []Range{
		{StartByte: 20, EndByte: 30},
		{StartByte: 10, EndByte: 15},
		{StartByte: 15, EndByte: 18},
		{StartByte: 18, EndByte: 18}, // empty, dropped
		{StartByte: 28, EndByte: 35}, // merge with 20-30
	}
	out := normalizeIncludedRanges(in)
	if len(out) != 2 {
		t.Fatalf("normalize len: got %d, want 2", len(out))
	}
	if out[0].StartByte != 10 || out[0].EndByte != 18 {
		t.Fatalf("range0: got %d-%d, want 10-18", out[0].StartByte, out[0].EndByte)
	}
	if out[1].StartByte != 20 || out[1].EndByte != 35 {
		t.Fatalf("range1: got %d-%d, want 20-35", out[1].StartByte, out[1].EndByte)
	}
}

func TestIncludedRangeTokenSourceFiltersTokens(t *testing.T) {
	base := &stubTokenSource{
		tokens: []Token{
			{Symbol: 1, StartByte: 0, EndByte: 5},
			{Symbol: 2, StartByte: 12, EndByte: 15},
			{Symbol: 3, StartByte: 21, EndByte: 22},
			{},
		},
	}
	ts := newIncludedRangeTokenSource(base, []Range{{StartByte: 10, EndByte: 20}}).(*includedRangeTokenSource)

	tok := ts.Next()
	if tok.Symbol != 2 {
		t.Fatalf("first token: got %d, want 2", tok.Symbol)
	}
	tok = ts.Next()
	if tok.Symbol != 0 {
		t.Fatalf("second token should be EOF-like, got %d", tok.Symbol)
	}
}

func TestIncludedRangeTokenSourceDelegatesParserState(t *testing.T) {
	base := &stubTokenSource{
		tokens: []Token{{}},
	}
	ts := newIncludedRangeTokenSource(base, []Range{{StartByte: 0, EndByte: 1}}).(*includedRangeTokenSource)
	ts.SetParserState(42)
	if base.state != 42 {
		t.Fatalf("delegated parser state: got %d, want 42", base.state)
	}
}

func TestParserSetIncludedRangesRoundTrip(t *testing.T) {
	p := NewParser(nil)
	p.SetIncludedRanges([]Range{
		{StartByte: 5, EndByte: 8},
		{StartByte: 1, EndByte: 3},
	})
	got := p.IncludedRanges()
	if len(got) != 2 {
		t.Fatalf("IncludedRanges len: got %d, want 2", len(got))
	}
	if got[0].StartByte != 1 || got[1].StartByte != 5 {
		t.Fatalf("IncludedRanges not sorted: got %v", got)
	}
}
