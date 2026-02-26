package grammars

import (
	"fmt"

	"github.com/odvcencio/gotreesitter"
)

// YAMLTokenSource is a custom lexer for tree-sitter-yaml.
//
// YAML is uniquely challenging: the grammar has 113 external tokens and only
// 2 DFA lex states, meaning virtually all tokenization must happen here.
// The grammar duplicates symbol names across contexts (e.g., 5 variants of
// "integer_scalar" for block-root, block-nested, flow-root, flow-nested,
// and single-flow-collection contexts). This lexer focuses on the block
// mapping context (the most common YAML pattern) and emits the first
// (block-root) variant of each token.
type YAMLTokenSource struct {
	src  []byte
	lang *gotreesitter.Language
	cur  sourceCursor

	done    bool
	pending []gotreesitter.Token

	// Special tokens
	eofSymbol gotreesitter.Symbol // sym 0 "end" - parser EOF
	yamlEOF   gotreesitter.Symbol // sym 1 "_eof" - YAML document EOF
	blSym     gotreesitter.Symbol // sym 111 "_bl" - newline/blank line

	// Document markers
	docStartSym gotreesitter.Symbol // sym 9 "---"
	docEndSym   gotreesitter.Symbol // sym 10 "..."

	// Block indicators (first variants for block context)
	dashSym  gotreesitter.Symbol // sym 11 "-" (block sequence)
	colonSym gotreesitter.Symbol // sym 20 ":" (block implicit key colon)
	qmarkSym gotreesitter.Symbol // sym 14 "?" (block explicit key)
	pipeSym  gotreesitter.Symbol // sym 21 "|" (literal block scalar)
	gtSym    gotreesitter.Symbol // sym 23 ">" (folded block scalar)

	// Flow collection indicators (first variants)
	lbrackSym gotreesitter.Symbol // sym 26 "["
	rbrackSym gotreesitter.Symbol // sym 29 "]"
	lbraceSym gotreesitter.Symbol // sym 32 "{"
	rbraceSym gotreesitter.Symbol // sym 35 "}"
	commaSym  gotreesitter.Symbol // sym 38 ","

	// Quoted strings
	dquoteSym      gotreesitter.Symbol // sym 46 '"' (open)
	dquoteCloseSym gotreesitter.Symbol // sym 55 '"' (close)
	dqtContentSym  gotreesitter.Symbol // sym 49 "_r_dqt_str_ctn"
	dqtEscapeSym   gotreesitter.Symbol // sym 51 "escape_sequence"
	squoteSym      gotreesitter.Symbol // sym 57 "'" (open)
	squoteCloseSym gotreesitter.Symbol // sym 64 "'" (close)
	sqtContentSym  gotreesitter.Symbol // sym 60 "_r_sqt_str_ctn"
	sqtEscapeSym   gotreesitter.Symbol // sym 62 "escape_sequence" ('' in single)

	// Scalar types (first/block-root variants)
	nullSym      gotreesitter.Symbol // sym 66 "null_scalar"
	boolSym      gotreesitter.Symbol // sym 71 "boolean_scalar"
	intSym       gotreesitter.Symbol // sym 76 "integer_scalar"
	floatSym     gotreesitter.Symbol // sym 81 "float_scalar"
	timestampSym gotreesitter.Symbol // sym 86 "timestamp_scalar"
	stringSym    gotreesitter.Symbol // sym 91 "string_scalar"

	// Other
	tagSym       gotreesitter.Symbol // sym 100 "tag"
	ampSym       gotreesitter.Symbol // sym 103 "&"
	anchorSym    gotreesitter.Symbol // sym 106 "anchor_name"
	starSym      gotreesitter.Symbol // sym 107 "*"
	aliasSym   gotreesitter.Symbol // sym 110 "alias_name"
	commentSym gotreesitter.Symbol // sym 112 "comment"

	emittedYAMLEOF bool

	// Incremental position tracking for pointAtOffset.
	lastPtOffset int
	lastPtRow    uint32
	lastPtCol    uint32
}

// NewYAMLTokenSource creates a token source for YAML source text.
func NewYAMLTokenSource(src []byte, lang *gotreesitter.Language) (*YAMLTokenSource, error) {
	if lang == nil {
		return nil, fmt.Errorf("yaml lexer: language is nil")
	}

	ts := &YAMLTokenSource{
		src:  src,
		lang: lang,
		cur:  newSourceCursor(src),
	}

	ts.eofSymbol, _ = lang.SymbolByName("end")

	// Look up symbol IDs by name. For duplicate names, use the first token variant.
	lookup := newTokenLookup(lang, "yaml")

	ts.yamlEOF = lookup.require("_eof")
	ts.blSym = lookup.require("_bl")
	ts.commentSym = lookup.optional("comment")

	// Document markers
	ts.docStartSym = lookup.optional("---")
	ts.docEndSym = lookup.optional("...")

	// Look up the colon symbol - we need the specific variant that works in
	// block mapping context. The grammar has ":" at syms 17-20, 42-45.
	// From parse table tracing, state 1030 (after string_scalar[91]) expects
	// ":" at sym 20. We get all colon symbols and use the correct one.
	colonSyms := lang.TokenSymbolsByName(":")
	if len(colonSyms) >= 4 {
		ts.colonSym = colonSyms[3] // sym 20 - block mapping implicit key colon
	} else if len(colonSyms) > 0 {
		ts.colonSym = colonSyms[0]
	}

	// Dash for block sequence
	dashSyms := lang.TokenSymbolsByName("-")
	if len(dashSyms) > 0 {
		ts.dashSym = dashSyms[0] // sym 11
	}

	// Question mark
	qmarkSyms := lang.TokenSymbolsByName("\\?")
	if len(qmarkSyms) > 0 {
		ts.qmarkSym = qmarkSyms[0] // sym 14
	}

	ts.pipeSym = lookup.optional("|")
	ts.gtSym = lookup.optional(">")

	// Flow collection indicators - use first variants
	lbrackSyms := lang.TokenSymbolsByName("[")
	if len(lbrackSyms) > 0 {
		ts.lbrackSym = lbrackSyms[0]
	}
	rbrackSyms := lang.TokenSymbolsByName("]")
	if len(rbrackSyms) > 0 {
		ts.rbrackSym = rbrackSyms[0]
	}
	lbraceSyms := lang.TokenSymbolsByName("{")
	if len(lbraceSyms) > 0 {
		ts.lbraceSym = lbraceSyms[0]
	}
	rbraceSyms := lang.TokenSymbolsByName("}")
	if len(rbraceSyms) > 0 {
		ts.rbraceSym = rbraceSyms[0]
	}
	commaSyms := lang.TokenSymbolsByName(",")
	if len(commaSyms) > 0 {
		ts.commaSym = commaSyms[0]
	}

	// Quoted strings
	dquoteSyms := lang.TokenSymbolsByName("\"")
	if len(dquoteSyms) > 0 {
		ts.dquoteSym = dquoteSyms[0] // sym 46 (open)
	}
	if len(dquoteSyms) >= 4 {
		ts.dquoteCloseSym = dquoteSyms[3] // sym 55 (close)
	} else if len(dquoteSyms) > 0 {
		ts.dquoteCloseSym = dquoteSyms[len(dquoteSyms)-1]
	}
	ts.dqtContentSym = lookup.optional("_r_dqt_str_ctn")
	escSyms := lang.TokenSymbolsByName("escape_sequence")
	if len(escSyms) > 0 {
		ts.dqtEscapeSym = escSyms[0]
	}

	squoteSyms := lang.TokenSymbolsByName("'")
	if len(squoteSyms) > 0 {
		ts.squoteSym = squoteSyms[0] // sym 57 (open)
	}
	if len(squoteSyms) >= 3 {
		ts.squoteCloseSym = squoteSyms[2] // sym 64 (close)
	} else if len(squoteSyms) > 0 {
		ts.squoteCloseSym = squoteSyms[len(squoteSyms)-1]
	}
	ts.sqtContentSym = lookup.optional("_r_sqt_str_ctn")
	if len(escSyms) >= 5 {
		ts.sqtEscapeSym = escSyms[4] // escape_sequence for single-quoted
	}

	// Scalar types - first variant of each
	nullSyms := lang.TokenSymbolsByName("null_scalar")
	if len(nullSyms) > 0 {
		ts.nullSym = nullSyms[0]
	}
	boolSyms := lang.TokenSymbolsByName("boolean_scalar")
	if len(boolSyms) > 0 {
		ts.boolSym = boolSyms[0]
	}
	intSyms := lang.TokenSymbolsByName("integer_scalar")
	if len(intSyms) > 0 {
		ts.intSym = intSyms[0]
	}
	floatSyms := lang.TokenSymbolsByName("float_scalar")
	if len(floatSyms) > 0 {
		ts.floatSym = floatSyms[0]
	}
	tsSyms := lang.TokenSymbolsByName("timestamp_scalar")
	if len(tsSyms) > 0 {
		ts.timestampSym = tsSyms[0]
	}
	strSyms := lang.TokenSymbolsByName("string_scalar")
	if len(strSyms) > 0 {
		ts.stringSym = strSyms[0]
	}

	// Tags, anchors, aliases
	tagSyms := lang.TokenSymbolsByName("tag")
	if len(tagSyms) > 0 {
		ts.tagSym = tagSyms[0]
	}
	ampSyms := lang.TokenSymbolsByName("&")
	if len(ampSyms) > 0 {
		ts.ampSym = ampSyms[0]
	}
	ts.anchorSym = lookup.optional("anchor_name")
	starSyms := lang.TokenSymbolsByName("*")
	if len(starSyms) > 0 {
		ts.starSym = starSyms[0]
	}
	ts.aliasSym = lookup.optional("alias_name")

	// Validate minimum required symbols
	if ts.yamlEOF == 0 {
		return nil, fmt.Errorf("yaml lexer: _eof symbol not found")
	}
	if ts.blSym == 0 {
		return nil, fmt.Errorf("yaml lexer: _bl symbol not found")
	}
	if ts.colonSym == 0 {
		return nil, fmt.Errorf("yaml lexer: colon symbol not found")
	}
	if ts.stringSym == 0 {
		return nil, fmt.Errorf("yaml lexer: string_scalar symbol not found")
	}

	return ts, nil
}

// NewYAMLTokenSourceOrEOF returns a YAML token source, or EOF-only fallback
// if setup fails.
func NewYAMLTokenSourceOrEOF(src []byte, lang *gotreesitter.Language) gotreesitter.TokenSource {
	ts, err := NewYAMLTokenSource(src, lang)
	if err != nil {
		return tokenSourceInitError{sourceLen: uint32(len(src))}
	}
	return ts
}

func (ts *YAMLTokenSource) Next() gotreesitter.Token {
	if len(ts.pending) > 0 {
		tok := ts.pending[0]
		ts.pending = ts.pending[1:]
		return tok
	}

	if ts.done {
		return ts.eofToken()
	}

	for {
		// Skip spaces and tabs (not newlines - those are significant in YAML)
		ts.skipSpacesAndTabs()

		if ts.cur.eof() {
			if !ts.emittedYAMLEOF {
				ts.emittedYAMLEOF = true
				pt := ts.cur.point()
				n := uint32(len(ts.src))
				return gotreesitter.Token{
					Symbol:     ts.yamlEOF,
					StartByte:  n,
					EndByte:    n,
					StartPoint: pt,
					EndPoint:   pt,
				}
			}
			ts.done = true
			return ts.eofToken()
		}

		ch := ts.cur.peekByte()

		// Newlines produce _bl tokens
		if ch == '\n' || ch == '\r' {
			return ts.newlineToken()
		}

		// Comments
		if ch == '#' {
			return ts.scanComment()
		}

		// Document markers
		if ch == '-' && ts.cur.matchLiteralAtCurrent("---") && ts.isDocMarkerBoundary(3) {
			return ts.makeLiteralToken(ts.docStartSym, 3)
		}
		if ch == '.' && ts.cur.matchLiteralAtCurrent("...") && ts.isDocMarkerBoundary(3) {
			return ts.makeLiteralToken(ts.docEndSym, 3)
		}

		// Block sequence dash: "-" followed by space/newline/eof
		if ch == '-' && ts.dashSym != 0 {
			if ts.cur.offset+1 >= len(ts.src) || isYAMLWhitespace(ts.src[ts.cur.offset+1]) || ts.src[ts.cur.offset+1] == '\n' {
				return ts.makeLiteralToken(ts.dashSym, 1)
			}
		}

		// Colon: ":" followed by space/newline/eof (block mapping value indicator)
		if ch == ':' && ts.colonSym != 0 {
			if ts.cur.offset+1 >= len(ts.src) || isYAMLWhitespace(ts.src[ts.cur.offset+1]) || ts.src[ts.cur.offset+1] == '\n' || ts.src[ts.cur.offset+1] == '\r' {
				return ts.makeLiteralToken(ts.colonSym, 1)
			}
		}

		// Question mark: "?" followed by space/newline
		if ch == '?' && ts.qmarkSym != 0 {
			if ts.cur.offset+1 >= len(ts.src) || isYAMLWhitespace(ts.src[ts.cur.offset+1]) || ts.src[ts.cur.offset+1] == '\n' {
				return ts.makeLiteralToken(ts.qmarkSym, 1)
			}
		}

		// Block scalar indicators
		if ch == '|' && ts.pipeSym != 0 {
			return ts.makeLiteralToken(ts.pipeSym, 1)
		}
		if ch == '>' && ts.gtSym != 0 {
			return ts.makeLiteralToken(ts.gtSym, 1)
		}

		// Flow collection indicators
		if ch == '[' && ts.lbrackSym != 0 {
			return ts.makeLiteralToken(ts.lbrackSym, 1)
		}
		if ch == ']' && ts.rbrackSym != 0 {
			return ts.makeLiteralToken(ts.rbrackSym, 1)
		}
		if ch == '{' && ts.lbraceSym != 0 {
			return ts.makeLiteralToken(ts.lbraceSym, 1)
		}
		if ch == '}' && ts.rbraceSym != 0 {
			return ts.makeLiteralToken(ts.rbraceSym, 1)
		}
		if ch == ',' && ts.commaSym != 0 {
			return ts.makeLiteralToken(ts.commaSym, 1)
		}

		// Double-quoted strings
		if ch == '"' && ts.dquoteSym != 0 {
			return ts.scanDoubleQuotedString()
		}

		// Single-quoted strings
		if ch == '\'' && ts.squoteSym != 0 {
			return ts.scanSingleQuotedString()
		}

		// Anchor: &name
		if ch == '&' && ts.ampSym != 0 {
			return ts.scanAnchor()
		}

		// Alias: *name
		if ch == '*' && ts.starSym != 0 {
			return ts.scanAlias()
		}

		// Tag: !tag or !!tag
		if ch == '!' && ts.tagSym != 0 {
			return ts.scanTag()
		}

		// Scalars (null, bool, int, float, timestamp, or plain string)
		return ts.scanScalar()
	}
}

func (ts *YAMLTokenSource) SkipToByte(offset uint32) gotreesitter.Token {
	target := int(offset)
	if target < 0 {
		target = 0
	}
	if target > len(ts.src) {
		target = len(ts.src)
	}

	ts.pending = nil
	ts.done = false
	ts.emittedYAMLEOF = false

	if target < ts.cur.offset {
		ts.cur = newSourceCursor(ts.src)
		ts.lastPtOffset = 0
		ts.lastPtRow = 0
		ts.lastPtCol = 0
	}
	for ts.cur.offset < target {
		ts.cur.advanceRune()
	}
	if ts.cur.eof() {
		ts.done = true
		return ts.eofToken()
	}
	return ts.Next()
}

// --- Token scanning methods ---

func (ts *YAMLTokenSource) newlineToken() gotreesitter.Token {
	start := ts.cur.offset
	startPt := ts.cur.point()
	// Consume \r\n or \n
	if ts.cur.peekByte() == '\r' {
		ts.cur.advanceByte()
	}
	if !ts.cur.eof() && ts.cur.peekByte() == '\n' {
		ts.cur.advanceByte()
	}
	return makeToken(ts.blSym, ts.src, start, ts.cur.offset, startPt, ts.cur.point())
}

func (ts *YAMLTokenSource) scanComment() gotreesitter.Token {
	start := ts.cur.offset
	startPt := ts.cur.point()
	for !ts.cur.eof() && ts.cur.peekByte() != '\n' && ts.cur.peekByte() != '\r' {
		ts.cur.advanceRune()
	}
	if ts.commentSym != 0 {
		return makeToken(ts.commentSym, ts.src, start, ts.cur.offset, startPt, ts.cur.point())
	}
	// If no comment symbol, skip the comment
	return ts.Next()
}

func (ts *YAMLTokenSource) scanDoubleQuotedString() gotreesitter.Token {
	// Opening quote
	start := ts.cur.offset
	startPt := ts.cur.point()
	ts.cur.advanceByte() // consume "
	openTok := makeToken(ts.dquoteSym, ts.src, start, ts.cur.offset, startPt, ts.cur.point())

	// Content and escape sequences
	contentStart := ts.cur.offset
	contentPt := ts.cur.point()

	for !ts.cur.eof() {
		ch := ts.cur.peekByte()
		if ch == '\\' {
			// Flush content before escape
			if ts.dqtContentSym != 0 && ts.cur.offset > contentStart {
				ts.pending = append(ts.pending, makeToken(ts.dqtContentSym, ts.src, contentStart, ts.cur.offset, contentPt, ts.cur.point()))
			}
			escStart := ts.cur.offset
			escPt := ts.cur.point()
			ts.cur.advanceByte() // consume backslash
			if !ts.cur.eof() {
				ts.cur.advanceRune() // consume escaped char
			}
			if ts.dqtEscapeSym != 0 {
				ts.pending = append(ts.pending, makeToken(ts.dqtEscapeSym, ts.src, escStart, ts.cur.offset, escPt, ts.cur.point()))
			}
			contentStart = ts.cur.offset
			contentPt = ts.cur.point()
			continue
		}
		if ch == '"' {
			// Flush remaining content
			if ts.dqtContentSym != 0 && ts.cur.offset > contentStart {
				ts.pending = append(ts.pending, makeToken(ts.dqtContentSym, ts.src, contentStart, ts.cur.offset, contentPt, ts.cur.point()))
			}
			// Closing quote
			closeStart := ts.cur.offset
			closePt := ts.cur.point()
			ts.cur.advanceByte()
			ts.pending = append(ts.pending, makeToken(ts.dquoteCloseSym, ts.src, closeStart, ts.cur.offset, closePt, ts.cur.point()))
			return openTok
		}
		ts.cur.advanceRune()
	}

	// Unterminated string
	if ts.dqtContentSym != 0 && ts.cur.offset > contentStart {
		ts.pending = append(ts.pending, makeToken(ts.dqtContentSym, ts.src, contentStart, ts.cur.offset, contentPt, ts.cur.point()))
	}
	return openTok
}

func (ts *YAMLTokenSource) scanSingleQuotedString() gotreesitter.Token {
	// Opening quote
	start := ts.cur.offset
	startPt := ts.cur.point()
	ts.cur.advanceByte() // consume '
	openTok := makeToken(ts.squoteSym, ts.src, start, ts.cur.offset, startPt, ts.cur.point())

	contentStart := ts.cur.offset
	contentPt := ts.cur.point()

	for !ts.cur.eof() {
		ch := ts.cur.peekByte()
		if ch == '\'' {
			// Check for escaped single quote ('')
			if ts.cur.offset+1 < len(ts.src) && ts.src[ts.cur.offset+1] == '\'' {
				// Flush content before escape
				if ts.sqtContentSym != 0 && ts.cur.offset > contentStart {
					ts.pending = append(ts.pending, makeToken(ts.sqtContentSym, ts.src, contentStart, ts.cur.offset, contentPt, ts.cur.point()))
				}
				escStart := ts.cur.offset
				escPt := ts.cur.point()
				ts.cur.advanceByte()
				ts.cur.advanceByte()
				if ts.sqtEscapeSym != 0 {
					ts.pending = append(ts.pending, makeToken(ts.sqtEscapeSym, ts.src, escStart, ts.cur.offset, escPt, ts.cur.point()))
				}
				contentStart = ts.cur.offset
				contentPt = ts.cur.point()
				continue
			}
			// Closing quote
			if ts.sqtContentSym != 0 && ts.cur.offset > contentStart {
				ts.pending = append(ts.pending, makeToken(ts.sqtContentSym, ts.src, contentStart, ts.cur.offset, contentPt, ts.cur.point()))
			}
			closeStart := ts.cur.offset
			closePt := ts.cur.point()
			ts.cur.advanceByte()
			ts.pending = append(ts.pending, makeToken(ts.squoteCloseSym, ts.src, closeStart, ts.cur.offset, closePt, ts.cur.point()))
			return openTok
		}
		ts.cur.advanceRune()
	}

	// Unterminated string
	if ts.sqtContentSym != 0 && ts.cur.offset > contentStart {
		ts.pending = append(ts.pending, makeToken(ts.sqtContentSym, ts.src, contentStart, ts.cur.offset, contentPt, ts.cur.point()))
	}
	return openTok
}

func (ts *YAMLTokenSource) scanAnchor() gotreesitter.Token {
	start := ts.cur.offset
	startPt := ts.cur.point()
	ts.cur.advanceByte() // consume &
	ampTok := makeToken(ts.ampSym, ts.src, start, ts.cur.offset, startPt, ts.cur.point())

	if ts.anchorSym != 0 && !ts.cur.eof() && !isYAMLFlowIndicator(ts.cur.peekByte()) {
		nameStart := ts.cur.offset
		namePt := ts.cur.point()
		for !ts.cur.eof() && !isYAMLWhitespace(ts.cur.peekByte()) && !isYAMLNewline(ts.cur.peekByte()) && !isYAMLFlowIndicator(ts.cur.peekByte()) {
			ts.cur.advanceRune()
		}
		if ts.cur.offset > nameStart {
			ts.pending = append(ts.pending, makeToken(ts.anchorSym, ts.src, nameStart, ts.cur.offset, namePt, ts.cur.point()))
		}
	}
	return ampTok
}

func (ts *YAMLTokenSource) scanAlias() gotreesitter.Token {
	start := ts.cur.offset
	startPt := ts.cur.point()
	ts.cur.advanceByte() // consume *
	starTok := makeToken(ts.starSym, ts.src, start, ts.cur.offset, startPt, ts.cur.point())

	if ts.aliasSym != 0 && !ts.cur.eof() && !isYAMLFlowIndicator(ts.cur.peekByte()) {
		nameStart := ts.cur.offset
		namePt := ts.cur.point()
		for !ts.cur.eof() && !isYAMLWhitespace(ts.cur.peekByte()) && !isYAMLNewline(ts.cur.peekByte()) && !isYAMLFlowIndicator(ts.cur.peekByte()) {
			ts.cur.advanceRune()
		}
		if ts.cur.offset > nameStart {
			ts.pending = append(ts.pending, makeToken(ts.aliasSym, ts.src, nameStart, ts.cur.offset, namePt, ts.cur.point()))
		}
	}
	return starTok
}

func (ts *YAMLTokenSource) scanTag() gotreesitter.Token {
	start := ts.cur.offset
	startPt := ts.cur.point()
	// Consume !tag or !!tag or !prefix!suffix
	ts.cur.advanceByte() // consume !
	for !ts.cur.eof() && !isYAMLWhitespace(ts.cur.peekByte()) && !isYAMLNewline(ts.cur.peekByte()) && !isYAMLFlowIndicator(ts.cur.peekByte()) {
		ts.cur.advanceRune()
	}
	return makeToken(ts.tagSym, ts.src, start, ts.cur.offset, startPt, ts.cur.point())
}

func (ts *YAMLTokenSource) scanScalar() gotreesitter.Token {
	start := ts.cur.offset
	startPt := ts.cur.point()

	// Scan a plain scalar: everything until a flow indicator, colon+space,
	// comment (#), newline, or EOF
	for !ts.cur.eof() {
		ch := ts.cur.peekByte()

		// Stop at newline
		if ch == '\n' || ch == '\r' {
			break
		}

		// Stop at comment preceded by space
		if ch == '#' && ts.cur.offset > start && isYAMLWhitespace(ts.src[ts.cur.offset-1]) {
			break
		}

		// Stop at colon followed by space/newline/eof
		if ch == ':' && (ts.cur.offset+1 >= len(ts.src) || isYAMLWhitespace(ts.src[ts.cur.offset+1]) || ts.src[ts.cur.offset+1] == '\n' || ts.src[ts.cur.offset+1] == '\r') {
			break
		}

		// Stop at flow indicators in flow context
		// For now, also stop at these since they delimit plain scalars
		if ch == ',' || ch == '[' || ch == ']' || ch == '{' || ch == '}' {
			break
		}

		ts.cur.advanceRune()
	}

	if ts.cur.offset == start {
		// Nothing consumed - skip one byte
		ts.cur.advanceRune()
		return ts.Next()
	}

	// Trim trailing whitespace
	end := ts.cur.offset
	for end > start && isYAMLWhitespace(ts.src[end-1]) {
		end--
	}

	text := string(ts.src[start:end])

	// Classify the scalar
	sym := ts.classifyScalar(text)
	return makeToken(sym, ts.src, start, end, startPt, ts.pointAtOffset(end))
}

// classifyScalar determines the token symbol for a plain scalar value.
func (ts *YAMLTokenSource) classifyScalar(text string) gotreesitter.Symbol {
	// Null
	if ts.nullSym != 0 {
		switch text {
		case "null", "Null", "NULL", "~":
			return ts.nullSym
		}
	}

	// Boolean
	if ts.boolSym != 0 {
		switch text {
		case "true", "True", "TRUE", "false", "False", "FALSE":
			return ts.boolSym
		}
	}

	// Integer
	if ts.intSym != 0 && isYAMLInteger(text) {
		return ts.intSym
	}

	// Float
	if ts.floatSym != 0 && isYAMLFloat(text) {
		return ts.floatSym
	}

	// Default: string scalar
	return ts.stringSym
}

// --- Helper methods ---

func (ts *YAMLTokenSource) skipSpacesAndTabs() {
	for !ts.cur.eof() {
		ch := ts.cur.peekByte()
		if ch == ' ' || ch == '\t' {
			ts.cur.advanceByte()
		} else {
			break
		}
	}
}

func (ts *YAMLTokenSource) isDocMarkerBoundary(n int) bool {
	end := ts.cur.offset + n
	if end >= len(ts.src) {
		return true
	}
	ch := ts.src[end]
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

func (ts *YAMLTokenSource) makeLiteralToken(sym gotreesitter.Symbol, n int) gotreesitter.Token {
	start := ts.cur.offset
	startPt := ts.cur.point()
	for i := 0; i < n && !ts.cur.eof(); i++ {
		ts.cur.advanceByte()
	}
	return makeToken(sym, ts.src, start, ts.cur.offset, startPt, ts.cur.point())
}

func (ts *YAMLTokenSource) eofToken() gotreesitter.Token {
	n := uint32(len(ts.src))
	pt := ts.cur.point()
	return gotreesitter.Token{
		Symbol:     ts.eofSymbol,
		StartByte:  n,
		EndByte:    n,
		StartPoint: pt,
		EndPoint:   pt,
	}
}

func (ts *YAMLTokenSource) pointAtOffset(offset int) gotreesitter.Point {
	if offset < ts.lastPtOffset {
		// Backward seek — reset to start.
		ts.lastPtOffset = 0
		ts.lastPtRow = 0
		ts.lastPtCol = 0
	}

	i := ts.lastPtOffset
	row := ts.lastPtRow
	col := ts.lastPtCol
	for i < offset && i < len(ts.src) {
		if ts.src[i] == '\n' {
			row++
			col = 0
		} else {
			col++
		}
		i++
	}
	ts.lastPtOffset = i
	ts.lastPtRow = row
	ts.lastPtCol = col
	return gotreesitter.Point{Row: row, Column: col}
}

// --- Character classification helpers ---

func isYAMLWhitespace(b byte) bool {
	return b == ' ' || b == '\t'
}

func isYAMLNewline(b byte) bool {
	return b == '\n' || b == '\r'
}

func isYAMLFlowIndicator(b byte) bool {
	return b == ',' || b == '[' || b == ']' || b == '{' || b == '}'
}

func isYAMLInteger(s string) bool {
	if len(s) == 0 {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i++
	}
	if i >= len(s) {
		return false
	}

	// Hex: 0x...
	if i+1 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X') {
		i += 2
		if i >= len(s) {
			return false
		}
		for i < len(s) {
			if !isASCIIHex(s[i]) && s[i] != '_' {
				return false
			}
			i++
		}
		return true
	}

	// Octal: 0o...
	if i+1 < len(s) && s[i] == '0' && (s[i+1] == 'o' || s[i+1] == 'O') {
		i += 2
		if i >= len(s) {
			return false
		}
		for i < len(s) {
			if (s[i] < '0' || s[i] > '7') && s[i] != '_' {
				return false
			}
			i++
		}
		return true
	}

	// Decimal
	for i < len(s) {
		if !isASCIIDigit(s[i]) && s[i] != '_' {
			return false
		}
		i++
	}
	return true
}

func isYAMLFloat(s string) bool {
	if len(s) == 0 {
		return false
	}

	// Special float values
	switch s {
	case ".inf", ".Inf", ".INF", "+.inf", "+.Inf", "+.INF",
		"-.inf", "-.Inf", "-.INF", ".nan", ".NaN", ".NAN":
		return true
	}

	i := 0
	if s[0] == '+' || s[0] == '-' {
		i++
	}
	if i >= len(s) {
		return false
	}

	hasDigit := false
	hasDot := false
	hasE := false

	for i < len(s) {
		ch := s[i]
		if isASCIIDigit(ch) || ch == '_' {
			hasDigit = true
			i++
		} else if ch == '.' && !hasDot {
			hasDot = true
			i++
		} else if (ch == 'e' || ch == 'E') && !hasE && hasDigit {
			hasE = true
			i++
			if i < len(s) && (s[i] == '+' || s[i] == '-') {
				i++
			}
		} else {
			return false
		}
	}

	return hasDigit && (hasDot || hasE)
}
