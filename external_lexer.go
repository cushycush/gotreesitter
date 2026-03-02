package gotreesitter

import "unicode/utf8"

// ExternalLexer is the scanner-facing lexer API used by external scanners.
// It mirrors the essential tree-sitter scanner API: lookahead, advance,
// mark_end, and result_symbol.
type ExternalLexer struct {
	source []byte

	startPos int
	pos      int
	endPos   int

	startPoint Point
	point      Point
	endPoint   Point

	resultSymbol Symbol
	hasResult    bool
}

func newExternalLexer(source []byte, pos int, row, col uint32) *ExternalLexer {
	pt := Point{Row: row, Column: col}
	return &ExternalLexer{
		source:     source,
		startPos:   pos,
		pos:        pos,
		endPos:     pos,
		startPoint: pt,
		point:      pt,
		endPoint:   pt,
	}
}

// Lookahead returns the current rune or 0 at EOF.
func (l *ExternalLexer) Lookahead() rune {
	if l.pos >= len(l.source) {
		return 0
	}
	r, _ := utf8.DecodeRune(l.source[l.pos:])
	return r
}

// Advance consumes one rune. When skip is true, consumed bytes are excluded
// from the token span (scanner whitespace skipping behavior).
func (l *ExternalLexer) Advance(skip bool) {
	if l.pos >= len(l.source) {
		return
	}

	r, size := utf8.DecodeRune(l.source[l.pos:])
	l.pos += size
	if r == '\n' {
		l.point.Row++
		l.point.Column = 0
	} else {
		l.point.Column++
	}

	if skip {
		l.startPos = l.pos
		l.startPoint = l.point
		// Note: endPos/endPoint are NOT updated here.  In C tree-sitter,
		// ts_lexer_advance(skip=true) only moves token_start_position, not
		// token_end_position.  MarkEnd() is the sole way to advance endPos.
		// This matters for scanners (e.g. YAML) that mark the end before
		// skipping whitespace and then return a zero-width token: the parser
		// must re-position at the mark, not past the skipped bytes.
	}
}

// MarkEnd marks the current scanner position as the token end.
func (l *ExternalLexer) MarkEnd() {
	l.endPos = l.pos
	l.endPoint = l.point
}

// SetResultSymbol sets the token symbol to emit when Scan returns true.
func (l *ExternalLexer) SetResultSymbol(sym Symbol) {
	l.resultSymbol = sym
	l.hasResult = true
}

// Column returns the current column (0-based) at the scanner cursor.
func (l *ExternalLexer) Column() uint32 {
	return l.point.Column
}

// GetColumn returns the current column (0-based) at the scanner cursor.
//
// Deprecated: use Column.
func (l *ExternalLexer) GetColumn() uint32 {
	return l.Column()
}

func (l *ExternalLexer) token() (Token, bool) {
	if !l.hasResult {
		return Token{}, false
	}
	// When endPos < startPos the scanner marked a position before skip
	// advanced startPos past it.  This yields a zero-width token at the
	// mark position — the parser will re-position the lexer there so the
	// skipped bytes are re-encountered on the next scan, matching C
	// tree-sitter semantics.
	if l.endPos < l.startPos {
		return Token{
			Symbol:     l.resultSymbol,
			StartByte:  uint32(l.endPos),
			EndByte:    uint32(l.endPos),
			StartPoint: l.endPoint,
			EndPoint:   l.endPoint,
		}, true
	}

	return Token{
		Symbol:     l.resultSymbol,
		Text:       bytesToStringNoCopy(l.source[l.startPos:l.endPos]),
		StartByte:  uint32(l.startPos),
		EndByte:    uint32(l.endPos),
		StartPoint: l.startPoint,
		EndPoint:   l.endPoint,
	}, true
}
