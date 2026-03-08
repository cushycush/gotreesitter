package gotreesitter

import (
	"unicode/utf8"
	"unsafe"
)

// Point is a row/column position in source text.
type Point struct {
	Row    uint32
	Column uint32
}

// Token is a lexed token with position info.
type Token struct {
	Symbol     Symbol
	Text       string
	StartByte  uint32
	EndByte    uint32
	StartPoint Point
	EndPoint   Point
	// NoLookahead marks a synthetic EOF used to force EOF-table reductions
	// without consuming input, matching tree-sitter's lex_state = -1.
	NoLookahead bool

	// ImmediateReject is set when this token was matched as an immediate
	// token after whitespace was consumed. Per tree-sitter semantics it
	// should not have matched. The parser should prefer reduce over shift
	// when this flag is set.
	ImmediateReject bool
}

func bytesToStringNoCopy(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// Lexer tokenizes source text using a table-driven DFA.
type Lexer struct {
	states          []LexState
	source          []byte
	pos             int
	row             uint32
	col             uint32
	immediateTokens []bool // symbol ID → is immediate (nil = no immediate tokens)

	// ImmediateRejected is set to true by Next() when the only DFA match
	// was an immediate token that appeared after whitespace. The caller
	// (e.g. dfaTokenSource) can check this to probe alternative lex modes
	// where a non-immediate token might be valid.
	ImmediateRejected bool
}

// NewLexer creates a new Lexer that will tokenize source using the given
// DFA state table.
func NewLexer(states []LexState, source []byte) *Lexer {
	return &Lexer{
		states: states,
		source: source,
	}
}

// SetImmediateTokens configures which symbol IDs are token.immediate() tokens.
// When the lexer matches one of these after consuming whitespace (skip
// transitions), the match is rejected — immediate tokens must match at the
// original position. This implements tree-sitter's token.immediate() semantics.
func (l *Lexer) SetImmediateTokens(imm []bool) {
	l.immediateTokens = imm
}

// Next lexes the next token starting from the given lex state index.
// It automatically skips tokens from states where Skip=true (whitespace).
// Returns a zero-Symbol token with StartByte==EndByte at EOF.
func (l *Lexer) Next(startState uint16) Token {
	l.ImmediateRejected = false
	origPos := l.pos
	for {
		// EOF check.
		if l.pos >= len(l.source) {
			return Token{
				StartByte:  uint32(l.pos),
				EndByte:    uint32(l.pos),
				StartPoint: Point{Row: l.row, Column: l.col},
				EndPoint:   Point{Row: l.row, Column: l.col},
			}
		}

		tokenStartPos := l.pos
		tokenStartRow := l.row
		tokenStartCol := l.col

		tok, ok := l.scan(startState, tokenStartPos, tokenStartRow, tokenStartCol)
		if ok {
			if tok.Symbol == 0 {
				// Skip token (whitespace). Verify the lexer actually
				// advanced past the skipped content to prevent an
				// infinite loop on zero-width skip matches.
				if l.pos <= tokenStartPos {
					l.skipOneRune()
				}
				continue
			}
			// Reject immediate token matches when whitespace was consumed
			// before them. tree-sitter's token.immediate() means the token
			// must match at the original position with no preceding whitespace.
			if l.immediateTokens != nil && int(tok.StartByte) > origPos &&
				int(tok.Symbol) < len(l.immediateTokens) && l.immediateTokens[tok.Symbol] {
				// Save the accepted state so we can fall back if no
				// non-immediate alternative exists.
				savedPos, savedRow, savedCol := l.pos, l.row, l.col

				// Re-scan from the same post-whitespace position but
				// reject immediate tokens to find a non-immediate alternative.
				l.pos = tokenStartPos
				l.row = tokenStartRow
				l.col = tokenStartCol
				altTok, altOK := l.scanRejectImmediate(startState, tokenStartPos, tokenStartRow, tokenStartCol)
				if altOK && altTok.Symbol != 0 {
					return altTok
				}
				// No non-immediate alternative — use the immediate token
				// anyway. This is technically wrong per tree-sitter semantics
				// but prevents total parse failure.
				l.ImmediateRejected = true
				tok.ImmediateReject = true
				l.pos = savedPos
				l.row = savedRow
				l.col = savedCol
				return tok
			}
			return tok
		}

		// No accepting state was found. Skip one rune as error recovery.
		l.skipOneRune()
	}
}

// catchAllPrioGap is the minimum priority gap between a shorter and longer
// DFA accept that triggers priority-over-length preference. prec(-1) adds
// 1000 to a token's priority number, so 500 safely catches catch-all patterns
// (like [^\r\n]+ with prec(-1)) without affecting normal token ordering.
const catchAllPrioGap = 500

// scanRejectImmediate is like scan but ignores immediate token accepts.
// Used when whitespace was consumed and we need a non-immediate alternative.
func (l *Lexer) scanRejectImmediate(startState uint16, startPos int, startRow, startCol uint32) (Token, bool) {
	curState := int(startState)
	if curState < 0 || curState >= len(l.states) {
		return Token{}, false
	}

	scanPos := startPos
	scanRow := startRow
	scanCol := startCol
	tokenStartPos := startPos
	tokenStartRow := startRow
	tokenStartCol := startCol

	acceptPos := -1
	acceptRow := uint32(0)
	acceptCol := uint32(0)
	acceptStartPos := 0
	acceptStartRow := uint32(0)
	acceptStartCol := uint32(0)
	acceptSymbol := Symbol(0)
	acceptSkip := false
	acceptPrio := int16(32767)

	isImm := func(sym Symbol) bool {
		return l.immediateTokens != nil && int(sym) < len(l.immediateTokens) && l.immediateTokens[sym]
	}

	eofHops := 0
	for {
		if curState < 0 || curState >= len(l.states) {
			break
		}
		st := &l.states[curState]

		if (st.AcceptToken > 0 && !isImm(st.AcceptToken)) || st.Skip {
			prio := st.AcceptPriority
			update := false
			if acceptPos < 0 {
				update = true
			} else if prio <= acceptPrio {
				update = true
			} else if int(prio)-int(acceptPrio) < catchAllPrioGap {
				update = true
			}
			if update {
				acceptPos = scanPos
				acceptRow = scanRow
				acceptCol = scanCol
				acceptStartPos = tokenStartPos
				acceptStartRow = tokenStartRow
				acceptStartCol = tokenStartCol
				acceptSymbol = st.AcceptToken
				acceptSkip = st.Skip
				acceptPrio = prio
			}
		}

		if scanPos >= len(l.source) {
			if st.EOF >= 0 && eofHops <= len(l.states) {
				curState = st.EOF
				eofHops++
				continue
			}
			break
		}
		eofHops = 0

		r, size := utf8.DecodeRune(l.source[scanPos:])
		nextState := -1
		skipTransition := false
		for i := range st.Transitions {
			tr := &st.Transitions[i]
			if r >= tr.Lo && r <= tr.Hi {
				nextState = tr.NextState
				skipTransition = tr.Skip
				break
			}
		}
		skipTransition = skipTransition && nextState >= 0
		if nextState < 0 && st.Default >= 0 {
			nextState = st.Default
			skipTransition = false
		}
		if nextState < 0 {
			break
		}

		scanPos += size
		if r == '\n' {
			scanRow++
			scanCol = 0
		} else {
			scanCol++
		}

		if skipTransition {
			tokenStartPos = scanPos
			tokenStartRow = scanRow
			tokenStartCol = scanCol
			acceptPos = -1
			acceptSymbol = 0
			acceptSkip = false
			acceptPrio = 32767
		}

		curState = nextState
	}

	if acceptPos < 0 {
		return Token{}, false
	}

	l.pos = acceptPos
	l.row = acceptRow
	l.col = acceptCol

	if acceptSkip {
		return Token{
			StartByte:  uint32(acceptStartPos),
			EndByte:    uint32(acceptPos),
			StartPoint: Point{Row: acceptStartRow, Column: acceptStartCol},
			EndPoint:   Point{Row: acceptRow, Column: acceptCol},
		}, true
	}

	return Token{
		Symbol:     acceptSymbol,
		Text:       bytesToStringNoCopy(l.source[acceptStartPos:acceptPos]),
		StartByte:  uint32(acceptStartPos),
		EndByte:    uint32(acceptPos),
		StartPoint: Point{Row: acceptStartRow, Column: acceptStartCol},
		EndPoint:   Point{Row: acceptRow, Column: acceptCol},
	}, true
}

// scan runs the DFA from the given start state and position. It returns
// a token and true if an accepting state was reached, or false if not.
// On a skip (whitespace) match, it returns a zero-Symbol token and true.
func (l *Lexer) scan(startState uint16, startPos int, startRow, startCol uint32) (Token, bool) {
	curState := int(startState)
	if curState < 0 || curState >= len(l.states) {
		return Token{}, false
	}

	scanPos := startPos
	scanRow := startRow
	scanCol := startCol
	tokenStartPos := startPos
	tokenStartRow := startRow
	tokenStartCol := startCol

	// Track the best accepting state. Uses longest-match by default.
	// When AcceptPriority is set (grammargen), a shorter match can win over
	// a longer one if the priority gap exceeds catchAllPrioGap. This prevents
	// catch-all patterns with prec(-1) or prec(-2) (e.g. [^\r\n]+) from
	// overshadowing shorter keyword matches (e.g. "new").
	acceptPos := -1
	acceptRow := uint32(0)
	acceptCol := uint32(0)
	acceptStartPos := 0
	acceptStartRow := uint32(0)
	acceptStartCol := uint32(0)
	acceptSymbol := Symbol(0)
	acceptSkip := false
	acceptPrio := int16(32767) // worst possible priority

	eofHops := 0
	// Walk the DFA in the same style as tree-sitter START_LEXER/ADVANCE/SKIP.
	for {
		if curState < 0 || curState >= len(l.states) {
			break
		}
		st := &l.states[curState]

		if st.AcceptToken > 0 || st.Skip {
			prio := st.AcceptPriority
			// Default: longest-match (update when same or better priority).
			// Exception: if the PREVIOUS accept had much better priority (the
			// gap exceeds catchAllPrioGap), keep the shorter higher-priority
			// match — the longer match is a catch-all pattern.
			update := false
			if acceptPos < 0 {
				update = true
			} else if prio <= acceptPrio {
				// Same or better priority → longest-match wins.
				update = true
			} else if int(prio)-int(acceptPrio) < catchAllPrioGap {
				// Small gap → normal token ordering, longest-match wins.
				update = true
			}
			// else: large gap → keep shorter higher-priority accept.
			if update {
				acceptPos = scanPos
				acceptRow = scanRow
				acceptCol = scanCol
				acceptStartPos = tokenStartPos
				acceptStartRow = tokenStartRow
				acceptStartCol = tokenStartCol
				acceptSymbol = st.AcceptToken
				acceptSkip = st.Skip
				acceptPrio = prio
			}
		}

		if scanPos >= len(l.source) {
			if st.EOF >= 0 && eofHops <= len(l.states) {
				curState = st.EOF
				eofHops++
				continue
			}
			break
		}
		eofHops = 0

		r, size := utf8.DecodeRune(l.source[scanPos:])
		nextState := -1
		skipTransition := false
		for i := range st.Transitions {
			tr := &st.Transitions[i]
			if r >= tr.Lo && r <= tr.Hi {
				nextState = tr.NextState
				skipTransition = tr.Skip
				break
			}
		}
		// Default transitions are treated as non-skipping.
		skipTransition = skipTransition && nextState >= 0
		if nextState < 0 && st.Default >= 0 {
			nextState = st.Default
			skipTransition = false
		}
		if nextState < 0 {
			break
		}

		scanPos += size
		if r == '\n' {
			scanRow++
			scanCol = 0
		} else {
			scanCol++
		}

		if skipTransition {
			// tree-sitter SKIP(state) consumes and resets token start.
			tokenStartPos = scanPos
			tokenStartRow = scanRow
			tokenStartCol = scanCol
			acceptPos = -1
			acceptSymbol = 0
			acceptSkip = false
			acceptPrio = 32767
		}

		curState = nextState
	}

	if acceptPos < 0 {
		return Token{}, false
	}

	// Rewind (or advance) to the accept position.
	l.pos = acceptPos
	l.row = acceptRow
	l.col = acceptCol

	if acceptSkip {
		// Return a zero-Symbol token to signal "skip".
		return Token{
			StartByte:  uint32(acceptStartPos),
			EndByte:    uint32(acceptPos),
			StartPoint: Point{Row: acceptStartRow, Column: acceptStartCol},
			EndPoint:   Point{Row: acceptRow, Column: acceptCol},
		}, true
	}

	return Token{
		Symbol:     acceptSymbol,
		Text:       bytesToStringNoCopy(l.source[acceptStartPos:acceptPos]),
		StartByte:  uint32(acceptStartPos),
		EndByte:    uint32(acceptPos),
		StartPoint: Point{Row: acceptStartRow, Column: acceptStartCol},
		EndPoint:   Point{Row: acceptRow, Column: acceptCol},
	}, true
}

// skipOneRune advances the lexer position by one rune, updating row/column.
func (l *Lexer) skipOneRune() {
	if l.pos >= len(l.source) {
		return
	}
	r, size := utf8.DecodeRune(l.source[l.pos:])
	l.pos += size
	if r == '\n' {
		l.row++
		l.col = 0
	} else {
		l.col++
	}
}
