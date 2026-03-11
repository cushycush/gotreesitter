package gotreesitter

import (
	"bytes"
	"testing"
	"time"
)

// buildArithmeticLanguage constructs a hand-built LR grammar for simple
// arithmetic expressions:
//
//	expression -> NUMBER
//	expression -> expression PLUS NUMBER
//
// Symbols:
//
//	0: EOF
//	1: NUMBER (terminal, named)
//	2: PLUS "+" (terminal, anonymous)
//	3: expression (nonterminal, named)
//
// LR States:
//
//	State 0 (start):       NUMBER -> shift 1, expression -> goto 2
//	State 1 (saw NUMBER):  any -> reduce expression->NUMBER (1 child)
//	State 2 (saw expr):    PLUS -> shift 3, EOF -> accept
//	State 3 (saw expr +):  NUMBER -> shift 4
//	State 4 (saw e+N):     any -> reduce expression->expression PLUS NUMBER (3 children)
//
// Lexer DFA:
//
//	State 0: start (dispatches digits, '+', whitespace)
//	State 1: in number (accept Symbol 1)
//	State 2: saw '+' (accept Symbol 2)
//	State 3: whitespace (skip)
func buildArithmeticLanguage() *Language {
	return &Language{
		Name:               "arithmetic",
		SymbolCount:        4,
		TokenCount:         3,
		ExternalTokenCount: 0,
		StateCount:         5,
		LargeStateCount:    0,
		FieldCount:         0,
		ProductionIDCount:  2,

		SymbolNames: []string{"EOF", "NUMBER", "+", "expression"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "NUMBER", Visible: true, Named: true},
			{Name: "+", Visible: true, Named: false},
			{Name: "expression", Visible: true, Named: true},
		},
		FieldNames: []string{""},

		// ParseActions indexed by the action index stored in the parse table.
		//
		// Index 0: no-op / error (empty actions)
		// Index 1: Shift to state 1 (NUMBER in state 0)
		// Index 2: Reduce expression -> NUMBER (1 child, symbol 3, production 0)
		// Index 3: Shift to state 2 (GOTO for expression from state 0)
		// Index 4: Shift to state 3 (PLUS in state 2)
		// Index 5: Accept (EOF in state 2)
		// Index 6: Shift to state 4 (NUMBER in state 3)
		// Index 7: Reduce expression -> expr PLUS NUMBER (3 children, symbol 3, production 1)
		ParseActions: []ParseActionEntry{
			// 0: error / no action
			{Actions: nil},
			// 1: shift to state 1
			{Actions: []ParseAction{{Type: ParseActionShift, State: 1}}},
			// 2: reduce expression -> NUMBER
			{Actions: []ParseAction{{Type: ParseActionReduce, Symbol: 3, ChildCount: 1, ProductionID: 0}}},
			// 3: shift/goto to state 2
			{Actions: []ParseAction{{Type: ParseActionShift, State: 2}}},
			// 4: shift to state 3
			{Actions: []ParseAction{{Type: ParseActionShift, State: 3}}},
			// 5: accept
			{Actions: []ParseAction{{Type: ParseActionAccept}}},
			// 6: shift to state 4
			{Actions: []ParseAction{{Type: ParseActionShift, State: 4}}},
			// 7: reduce expression -> expression PLUS NUMBER
			{Actions: []ParseAction{{Type: ParseActionReduce, Symbol: 3, ChildCount: 3, ProductionID: 1}}},
		},

		// Dense parse table: [state][symbol] -> action index
		// Columns: EOF(0), NUMBER(1), PLUS(2), expression(3)
		ParseTable: [][]uint16{
			// State 0: shift NUMBER->1, goto expression->2
			{0, 1, 0, 3},
			// State 1: reduce on any terminal
			{2, 2, 2, 0},
			// State 2: accept on EOF, shift PLUS->3
			{5, 0, 4, 0},
			// State 3: shift NUMBER->4
			{0, 6, 0, 0},
			// State 4: reduce on any terminal
			{7, 7, 7, 0},
		},

		// All 5 parser states use the same lex DFA start state (0).
		LexModes: []LexMode{
			{LexState: 0},
			{LexState: 0},
			{LexState: 0},
			{LexState: 0},
			{LexState: 0},
		},

		// Lexer DFA for: NUMBER ([0-9]+), PLUS ('+'), whitespace (skip)
		LexStates: []LexState{
			// State 0: start
			{
				AcceptToken: 0,
				Skip:        false,
				Default:     -1,
				EOF:         -1,
				Transitions: []LexTransition{
					{Lo: '0', Hi: '9', NextState: 1},
					{Lo: '+', Hi: '+', NextState: 2},
					{Lo: ' ', Hi: ' ', NextState: 3},
					{Lo: '\t', Hi: '\t', NextState: 3},
					{Lo: '\n', Hi: '\n', NextState: 3},
				},
			},
			// State 1: in number (accept NUMBER = symbol 1)
			{
				AcceptToken: 1,
				Skip:        false,
				Default:     -1,
				EOF:         -1,
				Transitions: []LexTransition{
					{Lo: '0', Hi: '9', NextState: 1},
				},
			},
			// State 2: saw '+' (accept PLUS = symbol 2)
			{
				AcceptToken: 2,
				Skip:        false,
				Default:     -1,
				EOF:         -1,
				Transitions: nil,
			},
			// State 3: whitespace (skip)
			{
				AcceptToken: 0,
				Skip:        true,
				Default:     -1,
				EOF:         -1,
				Transitions: []LexTransition{
					{Lo: ' ', Hi: ' ', NextState: 3},
					{Lo: '\t', Hi: '\t', NextState: 3},
					{Lo: '\n', Hi: '\n', NextState: 3},
				},
			},
		},
	}
}

// buildArithmeticRecoverLanguage is like buildArithmeticLanguage but adds a
// STAR token and a recover action in state 2. This lets tests verify that
// recovery can pop to an ancestor state and apply ParseActionRecover there.
func buildArithmeticRecoverLanguage() *Language {
	return &Language{
		Name:               "arithmetic_recover",
		SymbolCount:        5,
		TokenCount:         4,
		ExternalTokenCount: 0,
		StateCount:         5,
		LargeStateCount:    0,
		FieldCount:         0,
		ProductionIDCount:  2,

		SymbolNames: []string{"EOF", "NUMBER", "+", "*", "expression"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "NUMBER", Visible: true, Named: true},
			{Name: "+", Visible: true, Named: false},
			{Name: "*", Visible: true, Named: false},
			{Name: "expression", Visible: true, Named: true},
		},
		FieldNames: []string{""},

		ParseActions: []ParseActionEntry{
			{Actions: nil}, // 0
			{Actions: []ParseAction{{Type: ParseActionShift, State: 1}}},                                   // 1
			{Actions: []ParseAction{{Type: ParseActionReduce, Symbol: 4, ChildCount: 1, ProductionID: 0}}}, // 2
			{Actions: []ParseAction{{Type: ParseActionShift, State: 2}}},                                   // 3 (goto)
			{Actions: []ParseAction{{Type: ParseActionShift, State: 3}}},                                   // 4
			{Actions: []ParseAction{{Type: ParseActionAccept}}},                                            // 5
			{Actions: []ParseAction{{Type: ParseActionShift, State: 4}}},                                   // 6
			{Actions: []ParseAction{{Type: ParseActionReduce, Symbol: 4, ChildCount: 3, ProductionID: 1}}}, // 7
			{Actions: []ParseAction{{Type: ParseActionRecover, State: 3}}},                                 // 8
		},

		// Columns: EOF(0), NUMBER(1), PLUS(2), STAR(3), expression(4)
		ParseTable: [][]uint16{
			{0, 1, 0, 0, 3}, // state 0
			{2, 2, 2, 2, 0}, // state 1
			{5, 0, 4, 8, 0}, // state 2
			{0, 6, 0, 0, 0}, // state 3
			{7, 7, 7, 7, 0}, // state 4
		},

		LexModes: []LexMode{
			{LexState: 0},
			{LexState: 0},
			{LexState: 0},
			{LexState: 0},
			{LexState: 0},
		},

		LexStates: []LexState{
			{
				AcceptToken: 0,
				Skip:        false,
				Default:     -1,
				EOF:         -1,
				Transitions: []LexTransition{
					{Lo: '0', Hi: '9', NextState: 1},
					{Lo: '+', Hi: '+', NextState: 2},
					{Lo: '*', Hi: '*', NextState: 4},
					{Lo: ' ', Hi: ' ', NextState: 3},
					{Lo: '\t', Hi: '\t', NextState: 3},
					{Lo: '\n', Hi: '\n', NextState: 3},
				},
			},
			{
				AcceptToken: 1,
				Skip:        false,
				Default:     -1,
				EOF:         -1,
				Transitions: []LexTransition{
					{Lo: '0', Hi: '9', NextState: 1},
				},
			},
			{
				AcceptToken: 2,
				Skip:        false,
				Default:     -1,
				EOF:         -1,
				Transitions: nil,
			},
			{
				AcceptToken: 0,
				Skip:        true,
				Default:     -1,
				EOF:         -1,
				Transitions: []LexTransition{
					{Lo: ' ', Hi: ' ', NextState: 3},
					{Lo: '\t', Hi: '\t', NextState: 3},
					{Lo: '\n', Hi: '\n', NextState: 3},
				},
			},
			{
				AcceptToken: 3,
				Skip:        false,
				Default:     -1,
				EOF:         -1,
				Transitions: nil,
			},
		},
	}
}

func buildKeywordStateLanguageDense() *Language {
	return &Language{
		Name:                "keyword_state_dense",
		SymbolCount:         4,
		TokenCount:          3,
		StateCount:          3,
		LargeStateCount:     3,
		KeywordCaptureToken: 1, // identifier
		KeywordLexStates: []LexState{
			{AcceptToken: 0},
			{AcceptToken: 1}, // capture token
			{AcceptToken: 2}, // keyword token
		},
		// columns: EOF(0), IDENT(1), KW_IF(2), stmt(3)
		ParseTable: [][]uint16{
			{0, 3, 4, 0}, // state 0: keyword allowed
			{0, 3, 0, 0}, // state 1: identifier-only
			{0, 0, 4, 0}, // state 2: keyword-only
		},
	}
}

func buildKeywordStateLanguageSmall() *Language {
	return &Language{
		Name:                "keyword_state_small",
		SymbolCount:         4,
		TokenCount:          3,
		StateCount:          2,
		LargeStateCount:     1,
		KeywordCaptureToken: 1, // identifier
		KeywordLexStates: []LexState{
			{AcceptToken: 0},
			{AcceptToken: 2}, // keyword token
		},
		// state 0 dense row: no keyword actions
		ParseTable: [][]uint16{
			{0, 3, 0, 0},
		},
		// state 1 uses small table and allows KW_IF (symbol 2).
		SmallParseTableMap: []uint32{0},
		SmallParseTable: []uint16{
			1, // group count
			4, // section action index
			1, // symbol count
			2, // KW_IF symbol
		},
	}
}

func TestNewParser(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)
	if parser == nil {
		t.Fatal("NewParser returned nil")
	}
	if parser.language != lang {
		t.Error("parser.language does not match the provided language")
	}
}

func TestParserSingleNumber(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	tree := mustParse(t, parser, []byte("42"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}

	// Root should be "expression".
	if root.Symbol() != 3 {
		t.Errorf("root symbol = %d, want 3 (expression)", root.Symbol())
	}
	if root.Type(lang) != "expression" {
		t.Errorf("root type = %q, want %q", root.Type(lang), "expression")
	}
	if !root.IsNamed() {
		t.Error("root IsNamed = false, want true")
	}

	// expression -> NUMBER: 1 child.
	if root.ChildCount() != 1 {
		t.Fatalf("root child count = %d, want 1", root.ChildCount())
	}

	child := root.Child(0)
	if child.Symbol() != 1 {
		t.Errorf("child symbol = %d, want 1 (NUMBER)", child.Symbol())
	}
	if child.Type(lang) != "NUMBER" {
		t.Errorf("child type = %q, want %q", child.Type(lang), "NUMBER")
	}
	if !child.IsNamed() {
		t.Error("NUMBER child IsNamed = false, want true")
	}

	// Verify the text span.
	if child.Text(tree.Source()) != "42" {
		t.Errorf("NUMBER text = %q, want %q", child.Text(tree.Source()), "42")
	}
	if child.StartByte() != 0 || child.EndByte() != 2 {
		t.Errorf("NUMBER bytes = [%d,%d), want [0,2)", child.StartByte(), child.EndByte())
	}
}

func TestParserSimpleExpression(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	tree := mustParse(t, parser, []byte("1+2"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}

	// Root should be "expression" with 3 children: expression, PLUS, NUMBER.
	if root.Symbol() != 3 {
		t.Errorf("root symbol = %d, want 3 (expression)", root.Symbol())
	}
	if root.ChildCount() != 3 {
		t.Fatalf("root child count = %d, want 3", root.ChildCount())
	}

	// Child 0: inner expression (expression -> NUMBER "1").
	inner := root.Child(0)
	if inner.Symbol() != 3 {
		t.Errorf("child 0 symbol = %d, want 3 (expression)", inner.Symbol())
	}
	if inner.ChildCount() != 1 {
		t.Fatalf("inner expression child count = %d, want 1", inner.ChildCount())
	}
	num1 := inner.Child(0)
	if num1.Text(tree.Source()) != "1" {
		t.Errorf("first NUMBER text = %q, want %q", num1.Text(tree.Source()), "1")
	}

	// Child 1: PLUS "+".
	plus := root.Child(1)
	if plus.Symbol() != 2 {
		t.Errorf("child 1 symbol = %d, want 2 (PLUS)", plus.Symbol())
	}
	if plus.IsNamed() {
		t.Error("PLUS IsNamed = true, want false")
	}
	if plus.Text(tree.Source()) != "+" {
		t.Errorf("PLUS text = %q, want %q", plus.Text(tree.Source()), "+")
	}

	// Child 2: NUMBER "2".
	num2 := root.Child(2)
	if num2.Symbol() != 1 {
		t.Errorf("child 2 symbol = %d, want 1 (NUMBER)", num2.Symbol())
	}
	if num2.Text(tree.Source()) != "2" {
		t.Errorf("second NUMBER text = %q, want %q", num2.Text(tree.Source()), "2")
	}
}

func TestParserChainedExpression(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	// "1+2+3" should parse as left-associative: ((1)+2)+3
	tree := mustParse(t, parser, []byte("1+2+3"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}

	// Root: expression -> expression PLUS NUMBER
	if root.Symbol() != 3 {
		t.Errorf("root symbol = %d, want 3", root.Symbol())
	}
	if root.ChildCount() != 3 {
		t.Fatalf("root child count = %d, want 3", root.ChildCount())
	}

	// root.Child(2) should be NUMBER "3".
	num3 := root.Child(2)
	if num3.Text(tree.Source()) != "3" {
		t.Errorf("rightmost NUMBER text = %q, want %q", num3.Text(tree.Source()), "3")
	}

	// root.Child(0) should be an expression with 3 children (the "1+2" part).
	middle := root.Child(0)
	if middle.Symbol() != 3 {
		t.Errorf("middle expression symbol = %d, want 3", middle.Symbol())
	}
	if middle.ChildCount() != 3 {
		t.Fatalf("middle expression child count = %d, want 3", middle.ChildCount())
	}

	// middle.Child(0) is expression -> NUMBER "1".
	innerExpr := middle.Child(0)
	if innerExpr.Symbol() != 3 {
		t.Errorf("inner expression symbol = %d, want 3", innerExpr.Symbol())
	}
	if innerExpr.ChildCount() != 1 {
		t.Fatalf("inner expression child count = %d, want 1", innerExpr.ChildCount())
	}
	if innerExpr.Child(0).Text(tree.Source()) != "1" {
		t.Errorf("innermost NUMBER text = %q, want %q", innerExpr.Child(0).Text(tree.Source()), "1")
	}

	// middle.Child(2) is NUMBER "2".
	num2 := middle.Child(2)
	if num2.Text(tree.Source()) != "2" {
		t.Errorf("middle NUMBER text = %q, want %q", num2.Text(tree.Source()), "2")
	}
}

func TestParserEmptyInput(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	tree := mustParse(t, parser, []byte(""))

	// Empty input should produce a tree with nil root (nothing to parse).
	root := tree.RootNode()
	if root != nil {
		t.Errorf("expected nil root for empty input, got symbol %d with %d children",
			root.Symbol(), root.ChildCount())
	}
}

func TestParserWhitespace(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	// Whitespace between tokens should be handled correctly.
	tree := mustParse(t, parser, []byte("  1  +  2  "))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}

	if root.Symbol() != 3 {
		t.Errorf("root symbol = %d, want 3 (expression)", root.Symbol())
	}
	if root.ChildCount() != 3 {
		t.Fatalf("root child count = %d, want 3", root.ChildCount())
	}

	// Verify that the inner expression's NUMBER is "1" and the outer NUMBER is "2".
	inner := root.Child(0)
	if inner.ChildCount() < 1 {
		t.Fatal("inner expression has no children")
	}
	if inner.Child(0).Text(tree.Source()) != "1" {
		t.Errorf("first NUMBER text = %q, want %q", inner.Child(0).Text(tree.Source()), "1")
	}
	if root.Child(2).Text(tree.Source()) != "2" {
		t.Errorf("second NUMBER text = %q, want %q", root.Child(2).Text(tree.Source()), "2")
	}
}

func TestParserErrorRecovery(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	// "+1" starts with PLUS which is invalid in state 0.
	// The parser should create an error node for "+" and then parse "1".
	tree := mustParse(t, parser, []byte("+1"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root for error input")
	}

	// The tree should have an error somewhere.
	if !root.HasError() {
		t.Error("expected HasError=true for invalid input")
	}
}

func TestParserPreservesPartialTreeOnNoStacksAlive(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	tree := mustParse(t, parser, []byte("1+"))
	if tree == nil || tree.RootNode() == nil {
		t.Fatal("parse returned nil tree/root")
	}
	root := tree.RootNode()
	switch got := tree.ParseStopReason(); got {
	case ParseStopNoStacksAlive:
		if gotText := root.Text(tree.Source()); gotText != "1+" {
			t.Fatalf("partial tree text = %q, want %q", gotText, "1+")
		}
	case ParseStopAccepted:
		if gotText := root.Text(tree.Source()); gotText != "1+" {
			t.Fatalf("accepted recovered text = %q, want %q", gotText, "1+")
		}
	default:
		t.Fatalf("ParseStopReason = %q, want %q or %q", got, ParseStopNoStacksAlive, ParseStopAccepted)
	}
	if got := root.Symbol(); got == errorSymbol {
		t.Fatalf("root symbol = %d, want partial preserved root", got)
	}
	if got := root.ChildCount(); got == 0 {
		t.Fatal("expected partial tree with children after no_stacks_alive")
	}
}

func TestCanFinalizeNoActionEOFRejectsFragmentStackWithInferredRoot(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	s := newGLRStack(lang.InitialState)
	s.push(2, NewLeafNode(3, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1}), nil, nil)
	s.push(3, NewLeafNode(2, false, 1, 2, Point{Row: 0, Column: 1}, Point{Row: 0, Column: 2}), nil, nil)

	if parser.canFinalizeNoActionEOF(&s) {
		t.Fatal("canFinalizeNoActionEOF() = true, want false for leftover fragments")
	}
}

func TestCanFinalizeNoActionEOFAcceptsSingleNonterminalWithExtras(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	s := newGLRStack(lang.InitialState)
	extra := NewLeafNode(0, false, 0, 0, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 0})
	extra.isExtra = true
	s.push(0, extra, nil, nil)
	s.push(2, NewLeafNode(3, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1}), nil, nil)

	if !parser.canFinalizeNoActionEOF(&s) {
		t.Fatal("canFinalizeNoActionEOF() = false, want true for single nonterminal root")
	}
}

func TestPushOrExtendErrorNodeCoalescesConsecutiveTokens(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)
	arena := acquireNodeArena(arenaClassFull)
	defer arena.Release()

	s := newGLRStack(lang.InitialState)
	nodeCount := 0
	trackChildErrors := false

	parser.pushOrExtendErrorNode(&s, lang.InitialState, Token{
		StartByte:  0,
		EndByte:    1,
		StartPoint: Point{},
		EndPoint:   Point{Row: 0, Column: 1},
	}, &nodeCount, arena, nil, nil, &trackChildErrors)
	if got, want := s.depth(), 2; got != want {
		t.Fatalf("stack depth after first error = %d, want %d", got, want)
	}

	parser.pushOrExtendErrorNode(&s, lang.InitialState, Token{
		StartByte:  1,
		EndByte:    2,
		StartPoint: Point{Row: 0, Column: 1},
		EndPoint:   Point{Row: 0, Column: 2},
	}, &nodeCount, arena, nil, nil, &trackChildErrors)

	if got, want := s.depth(), 2; got != want {
		t.Fatalf("stack depth after extending error = %d, want %d", got, want)
	}
	if got, want := nodeCount, 1; got != want {
		t.Fatalf("nodeCount = %d, want %d", got, want)
	}
	top := s.top().node
	if top == nil {
		t.Fatal("top node is nil")
	}
	if got, want := top.Symbol(), errorSymbol; got != want {
		t.Fatalf("top symbol = %d, want %d", got, want)
	}
	if got, want := top.StartByte(), uint32(0); got != want {
		t.Fatalf("top.StartByte = %d, want %d", got, want)
	}
	if got, want := top.EndByte(), uint32(2); got != want {
		t.Fatalf("top.EndByte = %d, want %d", got, want)
	}
	if !trackChildErrors {
		t.Fatal("expected trackChildErrors=true")
	}
}

func TestParserRecoverAction(t *testing.T) {
	lang := buildArithmeticLanguage()

	// In this custom grammar, NUMBER should trigger ParseActionRecover.
	lang.ParseTable = [][]uint16{
		{0, 1}, // EOF has no action, NUMBER -> recover action.
		{0, 0},
	}
	lang.ParseActions = []ParseActionEntry{
		{}, // index 0 is unused / error
		{Actions: []ParseAction{{Type: ParseActionRecover}}},
	}

	parser := NewParser(lang)
	tree := mustParse(t, parser, []byte("1"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree root is nil after recover action")
	}

	if root.Symbol() != errorSymbol {
		t.Errorf("root symbol = %d, want %d (error symbol)", root.Symbol(), errorSymbol)
	}
	if !root.HasError() {
		t.Error("expected recovered parse root to have HasError=true")
	}
}

func TestBuildStateRecoverTableNilWhenNoRecoverActions(t *testing.T) {
	lang := buildArithmeticLanguage()
	table := buildStateRecoverTable(lang)
	if table != nil {
		t.Fatalf("expected nil recover table when grammar has no recover actions, got len=%d", len(table))
	}
}

func TestBuildStateRecoverTableMarksRecoverStates(t *testing.T) {
	lang := buildArithmeticRecoverLanguage()
	table := buildStateRecoverTable(lang)
	if len(table) == 0 {
		t.Fatal("expected recover table to be populated")
	}
	if len(table) != int(lang.StateCount) {
		t.Fatalf("recover table len = %d, want %d", len(table), lang.StateCount)
	}
	if table[0] {
		t.Fatal("state 0 should not be marked recoverable")
	}
	if !table[2] {
		t.Fatal("state 2 should be marked recoverable")
	}
}

func TestBuildKeywordStatesDense(t *testing.T) {
	lang := buildKeywordStateLanguageDense()
	table := buildKeywordStates(lang)
	if len(table) != int(lang.StateCount) {
		t.Fatalf("keyword state table len = %d, want %d", len(table), lang.StateCount)
	}
	if !table[0] {
		t.Fatal("state 0 should allow keyword promotion")
	}
	if table[1] {
		t.Fatal("state 1 should not allow keyword promotion")
	}
	if !table[2] {
		t.Fatal("state 2 should allow keyword promotion")
	}
}

func TestBuildKeywordStatesSmall(t *testing.T) {
	lang := buildKeywordStateLanguageSmall()
	table := buildKeywordStates(lang)
	if len(table) != int(lang.StateCount) {
		t.Fatalf("keyword state table len = %d, want %d", len(table), lang.StateCount)
	}
	if table[0] {
		t.Fatal("state 0 should not allow keyword promotion")
	}
	if !table[1] {
		t.Fatal("state 1 should allow keyword promotion from small parse table")
	}
}

func TestBuildKeywordStatesNilWhenNoKeywordActions(t *testing.T) {
	lang := buildKeywordStateLanguageDense()
	lang.ParseTable[0][2] = 0
	lang.ParseTable[2][2] = 0
	table := buildKeywordStates(lang)
	if table != nil {
		t.Fatalf("expected nil keyword state table, got len=%d", len(table))
	}
}

func TestBuildRecoverActionsByStateMarksRecoverSymbols(t *testing.T) {
	lang := buildArithmeticRecoverLanguage()
	_, _, symbols := buildRecoverActionsByState(lang)
	if len(symbols) == 0 {
		t.Fatal("expected recover symbol table to be populated")
	}
	if !symbols[3] { // STAR
		t.Fatal("expected STAR to be marked as recoverable lookahead")
	}
	if symbols[1] { // NUMBER
		t.Fatal("did not expect NUMBER to be marked as recoverable lookahead")
	}
}

func TestFindRecoverActionOnStackUsesNearestAncestor(t *testing.T) {
	lang := buildArithmeticRecoverLanguage()
	parser := NewParser(lang)
	s := newGLRStack(lang.InitialState)
	s.push(2, nil, nil, nil)
	s.push(3, nil, nil, nil)

	depth, act, ok := parser.findRecoverActionOnStack(&s, Symbol(3), nil) // STAR
	if !ok {
		t.Fatal("expected recover action on stack for STAR")
	}
	if depth != 1 {
		t.Fatalf("recover depth = %d, want 1 (state 2)", depth)
	}
	if act.Type != ParseActionRecover {
		t.Fatalf("recover action type = %d, want %d", act.Type, ParseActionRecover)
	}
	if act.State != 3 {
		t.Fatalf("recover state = %d, want 3", act.State)
	}
}

func TestRecoverActionForStateUsesSymbolSpecificTable(t *testing.T) {
	lang := buildArithmeticRecoverLanguage()
	parser := NewParser(lang)

	if _, ok := parser.recoverActionForState(2, Symbol(3)); !ok {
		t.Fatal("expected recover action for state 2 on STAR")
	}
	if _, ok := parser.recoverActionForState(2, Symbol(1)); ok {
		t.Fatal("did not expect recover action for state 2 on NUMBER")
	}
}

func TestParserAncestorRecoverActionPreservesLeftExpression(t *testing.T) {
	lang := buildArithmeticRecoverLanguage()
	parser := NewParser(lang)

	tree := mustParse(t, parser, []byte("1+*2"))
	if tree.RootNode() == nil {
		t.Fatal("parse returned nil root")
	}
	root := tree.RootNode()

	if root.Symbol() != 4 {
		t.Fatalf("root symbol = %d, want 4 (expression)", root.Symbol())
	}
	if !root.HasError() {
		t.Fatal("expected recovered tree to have HasError=true")
	}
	if root.ChildCount() != 3 {
		t.Fatalf("root child count = %d, want 3", root.ChildCount())
	}

	if got := root.Child(0).Symbol(); got != 4 {
		t.Fatalf("child[0] symbol = %d, want 4 (left expression preserved)", got)
	}
	if got := root.Child(1).Symbol(); got != errorSymbol {
		t.Fatalf("child[1] symbol = %d, want %d (error node)", got, errorSymbol)
	}
	if got := root.Child(2).Symbol(); got != 1 {
		t.Fatalf("child[2] symbol = %d, want 1 (NUMBER)", got)
	}
}

func TestParserFieldMapFieldNames(t *testing.T) {
	lang := buildArithmeticLanguage()
	lang.FieldCount = 1
	lang.FieldNames = []string{"", "value"}

	// Production 0 (expr -> NUMBER) has one child; map it to field ID 1.
	lang.FieldMapSlices = [][2]uint16{
		{0, 1},
	}
	lang.FieldMapEntries = []FieldMapEntry{
		{FieldID: 1, ChildIndex: 0, Inherited: false},
	}

	lang.ParseActions[2].Actions[0].ProductionID = 0
	lang.ParseActions[7].Actions[0].ProductionID = 1
	lang.ProductionIDCount = 2

	parser := NewParser(lang)
	tree := mustParse(t, parser, []byte("42"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}
	if root.Symbol() != 3 {
		t.Errorf("root symbol = %d, want 3 (expression)", root.Symbol())
	}

	fieldChild := root.ChildByFieldName("value", lang)
	if fieldChild == nil {
		t.Fatal("expected field-mapped child by name \"value\"")
	}
	if fieldChild.Symbol() != 1 {
		t.Errorf("field child symbol = %d, want 1 (NUMBER)", fieldChild.Symbol())
	}
	if fieldChild.Text(tree.Source()) != "42" {
		t.Errorf("field child text = %q, want %q", fieldChild.Text(tree.Source()), "42")
	}
}

func TestBuildResultFoldExtrasPreservesFieldMappings(t *testing.T) {
	lang := buildArithmeticLanguage()
	lang.FieldCount = 1
	lang.FieldNames = []string{"", "value"}
	parser := NewParser(lang)

	source := []byte(" 42 ")

	leadingExtra := NewLeafNode(2, false, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1})
	leadingExtra.isExtra = true

	valueChild := NewLeafNode(1, true, 1, 3, Point{Row: 0, Column: 1}, Point{Row: 0, Column: 3})
	realRoot := NewParentNode(3, true, []*Node{valueChild}, []FieldID{1}, 0)

	trailingExtra := NewLeafNode(2, false, 3, 4, Point{Row: 0, Column: 3}, Point{Row: 0, Column: 4})
	trailingExtra.isExtra = true

	stack := []stackEntry{
		{state: 0, node: leadingExtra},
		{state: 0, node: realRoot},
		{state: 0, node: trailingExtra},
	}

	tree := parser.buildResult(stack, source, nil, nil, nil, nil)
	if tree == nil || tree.RootNode() == nil {
		t.Fatal("buildResult returned nil tree/root")
	}
	root := tree.RootNode()
	if root != realRoot {
		t.Fatal("expected folded result to reuse real root node")
	}
	if root.ChildCount() != 3 {
		t.Fatalf("root child count = %d, want 3", root.ChildCount())
	}
	if root.Child(0) != leadingExtra || root.Child(1) != valueChild || root.Child(2) != trailingExtra {
		t.Fatalf("unexpected child order after folding extras")
	}

	fieldChild := root.ChildByFieldName("value", lang)
	if fieldChild == nil {
		t.Fatal("expected field-mapped child by name \"value\"")
	}
	if fieldChild != valueChild {
		t.Fatal("field mapping shifted after folding extras")
	}
	if len(root.fieldIDs) != 3 || root.fieldIDs[1] != 1 {
		t.Fatalf("fieldIDs not re-aligned after folding extras: %#v", root.fieldIDs)
	}
	if leadingExtra.Parent() != root || trailingExtra.Parent() != root {
		t.Fatal("extra child parent pointers were not updated during fold")
	}
}

func TestBuildReduceChildrenHiddenChildDoesNotDuplicateExistingField(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden", "!=", "identifier"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden", Visible: false, Named: false},
			{Name: "!=", Visible: true, Named: false},
			{Name: "identifier", Visible: true, Named: true},
		},
		FieldNames: []string{"", "operators"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: false},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	operator := newLeafNodeInArena(arena, 2, false, 0, 2, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 2})
	rhs := newLeafNodeInArena(arena, 3, true, 3, 4, Point{Row: 0, Column: 3}, Point{Row: 0, Column: 4})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{operator, rhs}, []FieldID{1, 0}, 0)

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 0, 0, arena)
	if got, want := len(children), 2; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := len(fieldIDs), 2; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got := fieldIDs[1]; got != 0 {
		t.Fatalf("fieldIDs[1] = %d, want 0", got)
	}
}

func TestBuildReduceChildrenInheritedFieldOverridesInheritedInnerFieldOnFlattenedSpan(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_inner", "type_identifier", "with", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_inner", Visible: false, Named: false},
			{Name: "type_identifier", Visible: true, Named: true},
			{Name: "with", Visible: true, Named: false},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames: []string{"", "type", "arguments"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	left := newLeafNodeInArena(arena, 2, true, 0, 12, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 12})
	withTok := newLeafNodeInArena(arena, 3, false, 13, 17, Point{Row: 0, Column: 13}, Point{Row: 0, Column: 17})
	right := newLeafNodeInArena(arena, 2, true, 18, 25, Point{Row: 0, Column: 18}, Point{Row: 0, Column: 25})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{left, withTok, right}, []FieldID{2, 2, 2}, 0)
	hidden.fieldSources = []uint8{fieldSourceInherited, fieldSourceInherited, fieldSourceInherited}

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 4, 0, arena)
	if got, want := len(children), 3; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := len(fieldIDs), 3; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	for i, fid := range fieldIDs {
		if got, want := fid, FieldID(1); got != want {
			t.Fatalf("fieldIDs[%d] = %d, want %d", i, got, want)
		}
	}
}

func TestBuildReduceChildrenDirectFieldOverridesSingleIndirectNamedChild(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_inner", "type_identifier", "arguments", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_inner", Visible: false, Named: false},
			{Name: "type_identifier", Visible: true, Named: true},
			{Name: "arguments", Visible: true, Named: true},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames: []string{"", "type", "arguments"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: false},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	typ := newLeafNodeInArena(arena, 2, true, 0, 9, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 9})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{typ}, []FieldID{2}, 0)
	hidden.fieldSources = []uint8{fieldSourceInherited}

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 4, 0, arena)
	if got, want := len(children), 1; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := len(fieldIDs), 1; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenInheritedFieldDoesNotBlanketSpanWithoutConflict(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_inner", "identifier", ".", "namespace_wildcard", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_inner", Visible: false, Named: false},
			{Name: "identifier", Visible: true, Named: true},
			{Name: ".", Visible: true, Named: false},
			{Name: "namespace_wildcard", Visible: true, Named: true},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames: []string{"", "path"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	head := newLeafNodeInArena(arena, 2, true, 0, 5, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 5})
	dot := newLeafNodeInArena(arena, 3, false, 5, 6, Point{Row: 0, Column: 5}, Point{Row: 0, Column: 6})
	tail := newLeafNodeInArena(arena, 4, true, 6, 7, Point{Row: 0, Column: 6}, Point{Row: 0, Column: 7})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{head, dot, tail}, nil, 0)

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 5, 0, arena)
	if got, want := len(children), 3; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := len(fieldIDs), 3; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got := fieldIDs[1]; got != 0 {
		t.Fatalf("fieldIDs[1] = %d, want 0", got)
	}
	if got := fieldIDs[2]; got != 0 {
		t.Fatalf("fieldIDs[2] = %d, want 0", got)
	}
}

func TestBuildReduceChildrenInheritedFieldSkipsNamedHiddenSpanWithMultipleNamedTargets(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_join_header", "identifier", "in", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_join_header", Visible: false, Named: true},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "in", Visible: true, Named: false},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames: []string{"", "type"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	left := newLeafNodeInArena(arena, 2, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1})
	inTok := newLeafNodeInArena(arena, 3, false, 2, 4, Point{Row: 0, Column: 2}, Point{Row: 0, Column: 4})
	right := newLeafNodeInArena(arena, 2, true, 5, 6, Point{Row: 0, Column: 5}, Point{Row: 0, Column: 6})
	hidden := newParentNodeInArena(arena, 1, true, []*Node{left, inTok, right}, nil, 0)

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 4, 0, arena)
	if got, want := len(children), 3; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := len(fieldIDs), 3; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	for i, fid := range fieldIDs {
		if fid != 0 {
			t.Fatalf("fieldIDs[%d] = %d, want 0", i, fid)
		}
	}
}

func TestBuildReduceChildrenDirectFieldPrefersNamedTargetsOnFlattenedSpan(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_inner", ".", "identifier", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_inner", Visible: false, Named: false},
			{Name: ".", Visible: true, Named: false},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames: []string{"", "path"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: false},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	dot0 := newLeafNodeInArena(arena, 2, false, 4, 5, Point{Row: 0, Column: 4}, Point{Row: 0, Column: 5})
	net := newLeafNodeInArena(arena, 3, true, 5, 8, Point{Row: 0, Column: 5}, Point{Row: 0, Column: 8})
	dot1 := newLeafNodeInArena(arena, 2, false, 8, 9, Point{Row: 0, Column: 8}, Point{Row: 0, Column: 9})
	url := newLeafNodeInArena(arena, 3, true, 9, 12, Point{Row: 0, Column: 9}, Point{Row: 0, Column: 12})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{dot0, net, dot1, url}, []FieldID{0, 1, 0, 1}, 0)
	hidden.fieldSources = []uint8{fieldSourceNone, fieldSourceDirect, fieldSourceNone, fieldSourceDirect}

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 4, 0, arena)
	if got, want := len(children), 4; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := len(fieldIDs), 4; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got := fieldIDs[0]; got != 0 {
		t.Fatalf("fieldIDs[0] = %d, want 0", got)
	}
	if got, want := fieldIDs[1], FieldID(1); got != want {
		t.Fatalf("fieldIDs[1] = %d, want %d", got, want)
	}
	if got := fieldIDs[2]; got != 0 {
		t.Fatalf("fieldIDs[2] = %d, want 0", got)
	}
	if got, want := fieldIDs[3], FieldID(1); got != want {
		t.Fatalf("fieldIDs[3] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenRepeatedDirectFieldOnHiddenPathLeavesAnonymousGapUnfielded(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_inner", ".", "identifier", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_inner", Visible: false, Named: false},
			{Name: ".", Visible: true, Named: false},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames: []string{"", "path"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: false},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	java := newLeafNodeInArena(arena, 3, true, 0, 4, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 4})
	dot0 := newLeafNodeInArena(arena, 2, false, 4, 5, Point{Row: 0, Column: 4}, Point{Row: 0, Column: 5})
	net := newLeafNodeInArena(arena, 3, true, 5, 8, Point{Row: 0, Column: 5}, Point{Row: 0, Column: 8})
	dot1 := newLeafNodeInArena(arena, 2, false, 8, 9, Point{Row: 0, Column: 8}, Point{Row: 0, Column: 9})
	url := newLeafNodeInArena(arena, 3, true, 9, 12, Point{Row: 0, Column: 9}, Point{Row: 0, Column: 12})

	tail := newParentNodeInArena(arena, 1, false, []*Node{net, dot1, url}, []FieldID{1, 0, 1}, 0)
	tail.fieldSources = []uint8{fieldSourceDirect, fieldSourceNone, fieldSourceDirect}
	outer := newParentNodeInArena(arena, 1, false, []*Node{java, dot0, tail}, []FieldID{1, 0, 1}, 0)
	outer.fieldSources = []uint8{fieldSourceDirect, fieldSourceNone, fieldSourceDirect}

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: outer}}, 0, 1, 1, 4, 0, arena)
	if got, want := len(children), 5; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := len(fieldIDs), 5; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got := fieldIDs[1]; got != 0 {
		t.Fatalf("fieldIDs[1] = %d, want 0", got)
	}
	if got, want := fieldIDs[2], FieldID(1); got != want {
		t.Fatalf("fieldIDs[2] = %d, want %d", got, want)
	}
	if got := fieldIDs[3]; got != 0 {
		t.Fatalf("fieldIDs[3] = %d, want 0", got)
	}
	if got, want := fieldIDs[4], FieldID(1); got != want {
		t.Fatalf("fieldIDs[4] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenInheritedFieldYieldsToDirectTargetOnHiddenSpan(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_inner", "modifiers", "def", "identifier", ":", "type_identifier", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_inner", Visible: false, Named: false},
			{Name: "modifiers", Visible: true, Named: true},
			{Name: "def", Visible: true, Named: false},
			{Name: "identifier", Visible: true, Named: true},
			{Name: ":", Visible: true, Named: false},
			{Name: "type_identifier", Visible: true, Named: true},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames: []string{"", "return_type", "name"},
	}

	arena := newNodeArena(arenaClassFull)
	modifiers := newLeafNodeInArena(arena, 2, true, 0, 7, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 7})
	defTok := newLeafNodeInArena(arena, 3, false, 8, 11, Point{Row: 0, Column: 8}, Point{Row: 0, Column: 11})
	name := newLeafNodeInArena(arena, 4, true, 12, 18, Point{Row: 0, Column: 12}, Point{Row: 0, Column: 18})
	colon := newLeafNodeInArena(arena, 5, false, 18, 19, Point{Row: 0, Column: 18}, Point{Row: 0, Column: 19})
	retType := newLeafNodeInArena(arena, 6, true, 20, 23, Point{Row: 0, Column: 20}, Point{Row: 0, Column: 23})

	hidden := newParentNodeInArena(arena, 1, false, []*Node{modifiers, defTok, name, colon, retType}, []FieldID{1, 0, 2, 0, 1}, 0)
	hidden.fieldSources = []uint8{fieldSourceInherited, fieldSourceNone, fieldSourceDirect, fieldSourceNone, fieldSourceDirect}

	children := arena.allocNodeSlice(5)
	fieldIDs := arena.allocFieldIDSlice(5)
	fieldSources := make([]uint8, 5)
	if got, want := appendFlattenedHiddenChildrenWithFields(children, fieldIDs, fieldSources, 0, hidden, lang.SymbolMetadata, 0), 5; got != want {
		t.Fatalf("appendFlattenedHiddenChildrenWithFields() = %d, want %d", got, want)
	}
	if got := fieldIDs[0]; got != 0 {
		t.Fatalf("fieldIDs[0] = %d, want 0", got)
	}
	if got, want := fieldIDs[2], FieldID(2); got != want {
		t.Fatalf("fieldIDs[2] = %d, want %d", got, want)
	}
	if got := fieldIDs[3]; got != 0 {
		t.Fatalf("fieldIDs[3] = %d, want 0", got)
	}
	if got, want := fieldIDs[4], FieldID(1); got != want {
		t.Fatalf("fieldIDs[4] = %d, want %d", got, want)
	}
	if got, want := fieldSources[4], uint8(fieldSourceDirect); got != want {
		t.Fatalf("fieldSources[4] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenDirectFieldDoesNotSpreadToLeadingExtraComment(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden", "comment", "binding", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden", Visible: false, Named: false},
			{Name: "comment", Visible: true, Named: true},
			{Name: "binding", Visible: true, Named: true},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames:     []string{"", "binding"},
		FieldMapSlices: [][2]uint16{{0, 1}},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: false},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	comment := newLeafNodeInArena(arena, 2, true, 0, 9, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 9})
	comment.isExtra = true
	binding := newLeafNodeInArena(arena, 3, true, 10, 16, Point{Row: 0, Column: 10}, Point{Row: 0, Column: 16})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{comment, binding}, nil, 0)

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 4, 0, arena)
	if got, want := len(children), 2; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got := fieldIDs[0]; got != 0 {
		t.Fatalf("fieldIDs[0] = %d, want 0", got)
	}
	if got, want := fieldIDs[1], FieldID(1); got != want {
		t.Fatalf("fieldIDs[1] = %d, want %d", got, want)
	}
}

func TestAppendFlattenedHiddenChildrenRepeatedDirectFieldSkipsCommaSeparator(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_inner", "identifier", ",", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_inner", Visible: false, Named: false},
			{Name: "identifier", Visible: true, Named: true},
			{Name: ",", Visible: true, Named: false},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames: []string{"", "name"},
	}

	arena := newNodeArena(arenaClassFull)
	left := newLeafNodeInArena(arena, 2, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1})
	comma := newLeafNodeInArena(arena, 3, false, 1, 2, Point{Row: 0, Column: 1}, Point{Row: 0, Column: 2})
	right := newLeafNodeInArena(arena, 2, true, 3, 4, Point{Row: 0, Column: 3}, Point{Row: 0, Column: 4})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{left, comma, right}, []FieldID{1, 0, 1}, 0)
	hidden.fieldSources = []uint8{fieldSourceDirect, fieldSourceNone, fieldSourceDirect}

	children := arena.allocNodeSlice(3)
	fieldIDs := arena.allocFieldIDSlice(3)
	fieldSources := make([]uint8, 3)
	if got, want := appendFlattenedHiddenChildrenWithFields(children, fieldIDs, fieldSources, 0, hidden, lang.SymbolMetadata), 3; got != want {
		t.Fatalf("appendFlattenedHiddenChildrenWithFields() = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got := fieldIDs[1]; got != 0 {
		t.Fatalf("fieldIDs[1] = %d, want 0", got)
	}
	if got, want := fieldIDs[2], FieldID(1); got != want {
		t.Fatalf("fieldIDs[2] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenDirectFieldFillsSingleNamedHiddenSpanDelimiters(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_inner", "(", "list_expression", ")", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_inner", Visible: false, Named: false},
			{Name: "(", Visible: true, Named: false},
			{Name: "list_expression", Visible: true, Named: true},
			{Name: ")", Visible: true, Named: false},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames:     []string{"", "right"},
		FieldMapSlices: [][2]uint16{{0, 1}},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: false},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	open := newLeafNodeInArena(arena, 2, false, 10, 11, Point{Row: 0, Column: 10}, Point{Row: 0, Column: 11})
	list := newLeafNodeInArena(arena, 3, true, 11, 20, Point{Row: 0, Column: 11}, Point{Row: 0, Column: 20})
	close := newLeafNodeInArena(arena, 4, false, 20, 21, Point{Row: 0, Column: 20}, Point{Row: 0, Column: 21})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{open, list, close}, nil, 0)

	_, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 5, 0, arena)
	if got, want := len(fieldIDs), 3; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	for i, fid := range fieldIDs {
		if got, want := fid, FieldID(1); got != want {
			t.Fatalf("fieldIDs[%d] = %d, want %d", i, got, want)
		}
	}
}

func TestBuildReduceChildrenDirectFieldAssignsSingleAnonymousHiddenTarget(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "expression", "this", "member_access_expression"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "expression", Visible: false, Named: true},
			{Name: "this", Visible: true, Named: false},
			{Name: "member_access_expression", Visible: true, Named: true},
		},
		FieldNames:     []string{"", "expression"},
		FieldMapSlices: [][2]uint16{{0, 1}},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: false},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	thisTok := newLeafNodeInArena(arena, 2, false, 0, 4, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 4})
	hidden := newParentNodeInArena(arena, 1, true, []*Node{thisTok}, nil, 0)

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 3, 0, arena)
	if got, want := len(children), 1; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := len(fieldIDs), 1; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenInheritedFieldSkipsProjectionWhenFlattenedSpanHasDirectFields(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_inner", "modifier", "predefined_type", "identifier", "parameter_list", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_inner", Visible: false, Named: false},
			{Name: "modifier", Visible: true, Named: true},
			{Name: "predefined_type", Visible: true, Named: true},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "parameter_list", Visible: true, Named: true},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames:     []string{"", "type", "name", "parameters", "type_parameters"},
		FieldMapSlices: [][2]uint16{{0, 1}},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 4, ChildIndex: 0, Inherited: true},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	modifier := newLeafNodeInArena(arena, 2, true, 0, 8, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 8})
	typ := newLeafNodeInArena(arena, 3, true, 9, 13, Point{Row: 0, Column: 9}, Point{Row: 0, Column: 13})
	name := newLeafNodeInArena(arena, 4, true, 14, 15, Point{Row: 0, Column: 14}, Point{Row: 0, Column: 15})
	params := newLeafNodeInArena(arena, 5, true, 15, 21, Point{Row: 0, Column: 15}, Point{Row: 0, Column: 21})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{modifier, typ, name, params}, []FieldID{0, 1, 2, 3}, 0)
	hidden.fieldSources = []uint8{fieldSourceNone, fieldSourceDirect, fieldSourceDirect, fieldSourceDirect}

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 6, 0, arena)
	if got, want := len(children), 4; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got := fieldIDs[0]; got != 0 {
		t.Fatalf("fieldIDs[0] = %d, want 0", got)
	}
	if got, want := fieldIDs[1], FieldID(1); got != want {
		t.Fatalf("fieldIDs[1] = %d, want %d", got, want)
	}
	if got, want := fieldIDs[2], FieldID(2); got != want {
		t.Fatalf("fieldIDs[2] = %d, want %d", got, want)
	}
	if got, want := fieldIDs[3], FieldID(3); got != want {
		t.Fatalf("fieldIDs[3] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenInheritedFieldSkipsProjectionWhenDescendantHasDirectFields(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_inner", "join", "identifier", ".", "member_access_expression", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_inner", Visible: false, Named: false},
			{Name: "join", Visible: true, Named: false},
			{Name: "identifier", Visible: true, Named: true},
			{Name: ".", Visible: true, Named: false},
			{Name: "member_access_expression", Visible: true, Named: true},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames:     []string{"", "type", "expression", "name"},
		FieldMapSlices: [][2]uint16{{0, 1}},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	joinTok := newLeafNodeInArena(arena, 2, false, 0, 4, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 4})
	ident := newLeafNodeInArena(arena, 3, true, 5, 6, Point{Row: 0, Column: 5}, Point{Row: 0, Column: 6})
	exprBase := newLeafNodeInArena(arena, 3, true, 7, 8, Point{Row: 0, Column: 7}, Point{Row: 0, Column: 8})
	dot := newLeafNodeInArena(arena, 4, false, 8, 9, Point{Row: 0, Column: 8}, Point{Row: 0, Column: 9})
	exprName := newLeafNodeInArena(arena, 3, true, 9, 11, Point{Row: 0, Column: 9}, Point{Row: 0, Column: 11})
	access := newParentNodeInArena(arena, 5, true, []*Node{exprBase, dot, exprName}, []FieldID{2, 0, 3}, 0)
	access.fieldSources = []uint8{fieldSourceDirect, fieldSourceNone, fieldSourceDirect}
	hidden := newParentNodeInArena(arena, 1, false, []*Node{joinTok, ident, access}, nil, 0)

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 6, 0, arena)
	if got, want := len(children), 3; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got := fieldIDs[0]; got != 0 {
		t.Fatalf("fieldIDs[0] = %d, want 0", got)
	}
	if got := fieldIDs[1]; got != 0 {
		t.Fatalf("fieldIDs[1] = %d, want 0", got)
	}
	if got := fieldIDs[2]; got != 0 {
		t.Fatalf("fieldIDs[2] = %d, want 0", got)
	}
}

func TestBuildReduceChildrenInheritedFieldProjectsSingleHiddenChildWhenDescendantHasDirectField(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden", "call", "identifier", "arguments", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden", Visible: false, Named: false},
			{Name: "call", Visible: true, Named: true},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "arguments", Visible: true, Named: true},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames: []string{"", "target"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	ident := newLeafNodeInArena(arena, 3, true, 0, 7, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 7})
	callArgs := newLeafNodeInArena(arena, 4, true, 7, 10, Point{Row: 0, Column: 7}, Point{Row: 0, Column: 10})
	call := newParentNodeInArena(arena, 2, true, []*Node{ident, callArgs}, []FieldID{1, 0}, 0)
	call.fieldSources = []uint8{fieldSourceDirect, fieldSourceNone}
	outerArgs := newLeafNodeInArena(arena, 4, true, 10, 13, Point{Row: 0, Column: 10}, Point{Row: 0, Column: 13})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{call, outerArgs}, nil, 0)

	children, fieldIDs, fieldSources := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 5, 0, arena)
	if got, want := len(children), 2; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got, want := fieldSourceAt(fieldSources, 0), uint8(fieldSourceInherited); got != want {
		t.Fatalf("fieldSources[0] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenInheritedFieldSkipsSingleLeafHiddenProjectionWithoutDirectField(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden", "variable_name", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden", Visible: false, Named: false},
			{Name: "variable_name", Visible: true, Named: true},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames: []string{"", "operator"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	name := newLeafNodeInArena(arena, 2, true, 2, 14, Point{Row: 0, Column: 2}, Point{Row: 0, Column: 14})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{name}, nil, 0)

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 3, 0, arena)
	if got, want := len(children), 1; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got := fieldIDs[0]; got != 0 {
		t.Fatalf("fieldIDs[0] = %d, want 0", got)
	}
}

func TestBuildReduceChildrenInheritedFieldProjectsSingleNonLeafHiddenChildWithoutDirectField(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden", "local", "function_declaration", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden", Visible: false, Named: false},
			{Name: "local", Visible: true, Named: false},
			{Name: "function_declaration", Visible: true, Named: true},
			{Name: "visible_parent", Visible: true, Named: true},
		},
		FieldNames: []string{"", "local_declaration"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	localTok := newLeafNodeInArena(arena, 2, false, 0, 5, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 5})
	decl := newParentNodeInArena(arena, 3, true, []*Node{localTok}, nil, 0)
	hidden := newParentNodeInArena(arena, 1, false, []*Node{decl}, nil, 0)

	children, fieldIDs, fieldSources := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 4, 0, arena)
	if got, want := len(children), 1; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got, want := fieldSourceAt(fieldSources, 0), uint8(fieldSourceInherited); got != want {
		t.Fatalf("fieldSources[0] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenCarriesHiddenChildFieldsThroughFieldlessParent(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_inner", "_hidden_outer", "function_declaration", "chunk"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_inner", Visible: false, Named: false},
			{Name: "_hidden_outer", Visible: false, Named: false},
			{Name: "function_declaration", Visible: true, Named: true},
			{Name: "chunk", Visible: true, Named: true},
		},
		FieldNames: []string{"", "local_declaration"},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	fn := newLeafNodeInArena(arena, 3, true, 0, 8, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 8})
	inner := newParentNodeInArena(arena, 1, false, []*Node{fn}, []FieldID{1}, 0)
	inner.fieldSources = []uint8{fieldSourceDirect}
	outer := newParentNodeInArena(arena, 2, false, []*Node{inner}, nil, 0)

	children, fieldIDs, fieldSources := parser.buildReduceChildren([]stackEntry{{node: outer}}, 0, 1, 1, 4, 0, arena)
	if got, want := len(children), 1; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got, want := fieldSourceAt(fieldSources, 0), uint8(fieldSourceDirect); got != want {
		t.Fatalf("fieldSources[0] = %d, want %d", got, want)
	}
}

func TestBuildFieldIDsSkipsConflictingInheritedEntriesOnSameChild(t *testing.T) {
	lang := &Language{
		FieldNames: []string{"", "name", "type"},
		FieldMapSlices: [][2]uint16{
			{0, 2},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
			{FieldID: 2, ChildIndex: 0, Inherited: true},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	fieldIDs, inherited := parser.buildFieldIDs(1, 0, arena)
	if got, want := len(fieldIDs), 1; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got := fieldIDs[0]; got != 0 {
		t.Fatalf("fieldIDs[0] = %d, want 0", got)
	}
	if got := inherited[0]; got {
		t.Fatal("inherited[0] = true, want false")
	}
}

func TestBuildReduceChildrenDirectFieldWinsOverInheritedEntriesOnSameChild(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "function_declaration", "identifier", "parameters", "block", "declaration"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "function_declaration", Visible: true, Named: true},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "parameters", Visible: true, Named: true},
			{Name: "block", Visible: true, Named: true},
			{Name: "declaration", Visible: true, Named: true},
		},
		FieldNames: []string{"", "body", "local_declaration", "name", "parameters"},
		FieldMapSlices: [][2]uint16{
			{0, 4},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
			{FieldID: 2, ChildIndex: 0, Inherited: false},
			{FieldID: 3, ChildIndex: 0, Inherited: true},
			{FieldID: 4, ChildIndex: 0, Inherited: true},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	name := newLeafNodeInArena(arena, 2, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1})
	params := newLeafNodeInArena(arena, 3, true, 1, 3, Point{Row: 0, Column: 1}, Point{Row: 0, Column: 3})
	body := newLeafNodeInArena(arena, 4, true, 4, 7, Point{Row: 0, Column: 4}, Point{Row: 0, Column: 7})
	decl := newParentNodeInArena(arena, 1, true, []*Node{name, params, body}, []FieldID{3, 4, 1}, 0)
	decl.fieldSources = []uint8{fieldSourceDirect, fieldSourceDirect, fieldSourceDirect}

	children, fieldIDs, fieldSources := parser.buildReduceChildren([]stackEntry{{node: decl}}, 0, 1, 1, 5, 0, arena)
	if got, want := len(children), 1; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(2); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got, want := fieldSourceAt(fieldSources, 0), uint8(fieldSourceDirect); got != want {
		t.Fatalf("fieldSources[0] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenDartConstructorParamDoesNotReceiveDirectNameField(t *testing.T) {
	lang := &Language{
		Name:        "dart",
		SymbolNames: []string{"EOF", "formal_parameter", "constructor_param", "this", ".", "identifier"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "formal_parameter", Visible: true, Named: true},
			{Name: "constructor_param", Visible: true, Named: true},
			{Name: "this", Visible: true, Named: true},
			{Name: ".", Visible: true, Named: false},
			{Name: "identifier", Visible: true, Named: true},
		},
		FieldNames: []string{"", "name"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: false},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	thisLeaf := newLeafNodeInArena(arena, 3, true, 0, 4, Point{}, Point{Column: 4})
	dot := newLeafNodeInArena(arena, 4, false, 4, 5, Point{Column: 4}, Point{Column: 5})
	name := newLeafNodeInArena(arena, 5, true, 5, 6, Point{Column: 5}, Point{Column: 6})
	constructorParam := newParentNodeInArena(arena, 2, true, []*Node{thisLeaf, dot, name}, nil, 0)

	children, fieldIDs, fieldSources := parser.buildReduceChildren([]stackEntry{{node: constructorParam}}, 0, 1, 1, 1, 0, arena)
	if got, want := len(children), 1; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got := fieldIDs[0]; got != 0 {
		t.Fatalf("fieldIDs[0] = %d, want 0", got)
	}
	if got := fieldSourceAt(fieldSources, 0); got != 0 {
		t.Fatalf("fieldSources[0] = %d, want 0", got)
	}
}

func TestBuildReduceChildrenDartHiddenConstructorParamDoesNotReceiveNameField(t *testing.T) {
	lang := &Language{
		Name:        "dart",
		SymbolNames: []string{"EOF", "formal_parameter", "_hidden", "constructor_param", "this", ".", "identifier"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "formal_parameter", Visible: true, Named: true},
			{Name: "_hidden", Visible: false, Named: false},
			{Name: "constructor_param", Visible: true, Named: true},
			{Name: "this", Visible: true, Named: true},
			{Name: ".", Visible: true, Named: false},
			{Name: "identifier", Visible: true, Named: true},
		},
		FieldNames: []string{"", "name"},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: false},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	thisLeaf := newLeafNodeInArena(arena, 4, true, 0, 4, Point{}, Point{Column: 4})
	dot := newLeafNodeInArena(arena, 5, false, 4, 5, Point{Column: 4}, Point{Column: 5})
	name := newLeafNodeInArena(arena, 6, true, 5, 6, Point{Column: 5}, Point{Column: 6})
	constructorParam := newParentNodeInArena(arena, 3, true, []*Node{thisLeaf, dot, name}, nil, 0)
	hidden := newParentNodeInArena(arena, 2, false, []*Node{constructorParam}, []FieldID{1}, 0)
	hidden.fieldSources = []uint8{fieldSourceDirect}

	children, fieldIDs, fieldSources := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 1, 0, arena)
	if got, want := len(children), 1; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := children[0].Type(lang), "constructor_param"; got != want {
		t.Fatalf("children[0].Type() = %q, want %q", got, want)
	}
	if got := fieldIDs[0]; got != 0 {
		t.Fatalf("fieldIDs[0] = %d, want 0", got)
	}
	if got := fieldSourceAt(fieldSources, 0); got != 0 {
		t.Fatalf("fieldSources[0] = %d, want 0", got)
	}
}

func TestNormalizeMakeConditionalConsequenceFieldsExtendsAcrossLeadingTabs(t *testing.T) {
	lang := &Language{
		Name:        "make",
		SymbolNames: []string{"EOF", "conditional", "ifneq_directive", "\t", "recipe_line", "else_directive", "endif"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "conditional", Visible: true, Named: true},
			{Name: "ifneq_directive", Visible: true, Named: true},
			{Name: "\t", Visible: true, Named: false},
			{Name: "recipe_line", Visible: true, Named: true},
			{Name: "else_directive", Visible: true, Named: true},
			{Name: "endif", Visible: true, Named: false},
		},
		FieldNames: []string{"", "consequence"},
	}

	arena := newNodeArena(arenaClassFull)
	directive := newLeafNodeInArena(arena, 2, true, 0, 5, Point{}, Point{Column: 5})
	tab := newLeafNodeInArena(arena, 3, false, 5, 6, Point{Column: 5}, Point{Column: 6})
	recipe := newLeafNodeInArena(arena, 4, true, 6, 12, Point{Column: 6}, Point{Column: 12})
	elseDir := newLeafNodeInArena(arena, 5, true, 12, 16, Point{Column: 12}, Point{Column: 16})
	endif := newLeafNodeInArena(arena, 6, false, 16, 21, Point{Column: 16}, Point{Column: 21})
	root := newParentNodeInArena(arena, 1, true, []*Node{directive, tab, recipe, elseDir, endif}, []FieldID{0, 0, 1, 1, 0}, 0)
	root.fieldSources = []uint8{0, 0, fieldSourceDirect, fieldSourceDirect, 0}

	normalizeMakeConditionalConsequenceFields(root, lang)

	if got, want := root.fieldIDs[1], FieldID(1); got != want {
		t.Fatalf("tab field = %d, want %d", got, want)
	}
	if got, want := fieldSourceAt(root.fieldSources, 1), uint8(fieldSourceDirect); got != want {
		t.Fatalf("tab field source = %d, want %d", got, want)
	}
	if got := root.fieldIDs[4]; got != 0 {
		t.Fatalf("endif field = %d, want 0", got)
	}
}

func TestNormalizeIniSectionStartsSnapToFirstChild(t *testing.T) {
	lang := &Language{
		Name:        "ini",
		SymbolNames: []string{"EOF", "section", "section_name", "setting"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "section", Visible: true, Named: true},
			{Name: "section_name", Visible: true, Named: true},
			{Name: "setting", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	sectionName := newLeafNodeInArena(arena, 2, true, 48, 69, Point{Row: 1}, Point{Row: 1, Column: 21})
	setting := newLeafNodeInArena(arena, 3, true, 70, 80, Point{Row: 2}, Point{Row: 2, Column: 10})
	section := newParentNodeInArena(arena, 1, true, []*Node{sectionName, setting}, nil, 0)
	section.startByte = 0
	section.startPoint = Point{}

	normalizeIniSectionStarts(section, lang)

	if got, want := section.startByte, uint32(48); got != want {
		t.Fatalf("section.startByte = %d, want %d", got, want)
	}
	if got, want := section.startPoint, sectionName.startPoint; got != want {
		t.Fatalf("section.startPoint = %#v, want %#v", got, want)
	}
}

func TestNormalizeHaskellZeroWidthTokensDropsEmptySeparators(t *testing.T) {
	lang := &Language{
		Name:        "haskell",
		SymbolNames: []string{"EOF", "haskell", "pragma", "_token1", "haddock", "header"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "haskell", Visible: true, Named: true},
			{Name: "pragma", Visible: true, Named: true},
			{Name: "_token1", Visible: false, Named: false},
			{Name: "haddock", Visible: true, Named: true},
			{Name: "header", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	pragma := newLeafNodeInArena(arena, 2, true, 0, 4, Point{}, Point{Column: 4})
	sep1 := newLeafNodeInArena(arena, 3, false, 4, 4, Point{Column: 4}, Point{Column: 4})
	haddock := newLeafNodeInArena(arena, 4, true, 5, 12, Point{Row: 1}, Point{Row: 1, Column: 7})
	sep2 := newLeafNodeInArena(arena, 3, false, 12, 12, Point{Row: 1, Column: 7}, Point{Row: 1, Column: 7})
	header := newLeafNodeInArena(arena, 5, true, 12, 20, Point{Row: 1, Column: 7}, Point{Row: 2, Column: 8})
	root := newParentNodeInArena(arena, 1, true, []*Node{pragma, sep1, haddock, sep2, header}, nil, 0)

	normalizeHaskellZeroWidthTokens(root, lang)

	if got, want := len(root.children), 3; got != want {
		t.Fatalf("len(root.children) = %d, want %d", got, want)
	}
	if got := root.children[0].Type(lang); got != "pragma" {
		t.Fatalf("child[0] = %q, want pragma", got)
	}
	if got := root.children[1].Type(lang); got != "haddock" {
		t.Fatalf("child[1] = %q, want haddock", got)
	}
	if got := root.children[2].Type(lang); got != "header" {
		t.Fatalf("child[2] = %q, want header", got)
	}
}

func TestNormalizeHaskellRootImportFieldSetsImportsField(t *testing.T) {
	lang := &Language{
		Name:        "haskell",
		SymbolNames: []string{"EOF", "haskell", "pragma", "haddock", "header", "imports", "declarations"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "haskell", Visible: true, Named: true},
			{Name: "pragma", Visible: true, Named: true},
			{Name: "haddock", Visible: true, Named: true},
			{Name: "header", Visible: true, Named: true},
			{Name: "imports", Visible: true, Named: true},
			{Name: "declarations", Visible: true, Named: true},
		},
		FieldNames: []string{"", "imports", "declarations"},
	}

	arena := newNodeArena(arenaClassFull)
	pragma := newLeafNodeInArena(arena, 2, true, 0, 4, Point{}, Point{Column: 4})
	haddock := newLeafNodeInArena(arena, 3, true, 5, 12, Point{Row: 1}, Point{Row: 1, Column: 7})
	header := newLeafNodeInArena(arena, 4, true, 12, 20, Point{Row: 1, Column: 7}, Point{Row: 2, Column: 8})
	imports := newLeafNodeInArena(arena, 5, true, 21, 30, Point{Row: 3}, Point{Row: 3, Column: 9})
	declarations := newLeafNodeInArena(arena, 6, true, 31, 40, Point{Row: 4}, Point{Row: 4, Column: 9})
	root := newParentNodeInArena(arena, 1, true, []*Node{pragma, haddock, header, imports, declarations}, nil, 0)

	normalizeHaskellRootImportField(root, lang)

	if got, want := len(root.fieldIDs), len(root.children); got != want {
		t.Fatalf("len(root.fieldIDs) = %d, want %d", got, want)
	}
	if got, want := root.fieldIDs[3], FieldID(1); got != want {
		t.Fatalf("fieldIDs[3] = %d, want %d", got, want)
	}
	if got, want := fieldSourceAt(root.fieldSources, 3), uint8(fieldSourceInherited); got != want {
		t.Fatalf("fieldSources[3] = %d, want %d", got, want)
	}
	if got, want := root.fieldIDs[4], FieldID(2); got != want {
		t.Fatalf("fieldIDs[4] = %d, want %d", got, want)
	}
	if got, want := fieldSourceAt(root.fieldSources, 4), uint8(fieldSourceInherited); got != want {
		t.Fatalf("fieldSources[4] = %d, want %d", got, want)
	}
}

func TestNormalizeHaskellDeclarationsSpanExtendsToTrailingTrivia(t *testing.T) {
	lang := &Language{
		Name:        "haskell",
		SymbolNames: []string{"EOF", "haskell", "declarations"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "haskell", Visible: true, Named: true},
			{Name: "declarations", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	decls := newLeafNodeInArena(arena, 2, true, 10, 14, Point{Row: 1}, Point{Row: 1, Column: 4})
	root := newParentNodeInArena(arena, 1, true, []*Node{decls}, nil, 0)
	root.endByte = 15
	root.endPoint = Point{Row: 2}

	normalizeHaskellDeclarationsSpan(root, []byte("0123456789body\n"), lang)

	if got, want := decls.endByte, uint32(15); got != want {
		t.Fatalf("decls.endByte = %d, want %d", got, want)
	}
	if got, want := decls.endPoint, root.endPoint; got != want {
		t.Fatalf("decls.endPoint = %#v, want %#v", got, want)
	}
}

func TestNormalizeSvelteTrailingExtraTriviaDropsTrailingToken(t *testing.T) {
	lang := &Language{
		Name:        "svelte",
		SymbolNames: []string{"EOF", "document", "script_element", "style_element", "_tag_value_token1"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "document", Visible: true, Named: true},
			{Name: "script_element", Visible: true, Named: true},
			{Name: "style_element", Visible: true, Named: true},
			{Name: "_tag_value_token1", Visible: false, Named: false},
		},
	}

	arena := newNodeArena(arenaClassFull)
	script := newLeafNodeInArena(arena, 2, true, 0, 8, Point{}, Point{Column: 8})
	style := newLeafNodeInArena(arena, 3, true, 9, 17, Point{Row: 1}, Point{Row: 1, Column: 8})
	trailing := newLeafNodeInArena(arena, 4, false, 17, 18, Point{Row: 1, Column: 8}, Point{Row: 2})
	trailing.isExtra = true
	root := newParentNodeInArena(arena, 1, true, []*Node{script, style, trailing}, []FieldID{0, 0, 0}, 0)
	root.fieldSources = []uint8{fieldSourceNone, fieldSourceNone, fieldSourceNone}
	root.endByte = 18
	root.endPoint = Point{Row: 2}

	normalizeSvelteTrailingExtraTrivia(root, []byte("<script>\n<style>\n\n"), lang)

	if got, want := len(root.children), 2; got != want {
		t.Fatalf("len(root.children) = %d, want %d", got, want)
	}
	if got, want := len(root.fieldIDs), 2; got != want {
		t.Fatalf("len(root.fieldIDs) = %d, want %d", got, want)
	}
	if got, want := len(root.fieldSources), 2; got != want {
		t.Fatalf("len(root.fieldSources) = %d, want %d", got, want)
	}
	if got := root.children[1].Type(lang); got != "style_element" {
		t.Fatalf("child[1] = %q, want style_element", got)
	}
}

func TestNormalizeCTranslationUnitRootRetagsRecoveredTopLevelChildren(t *testing.T) {
	lang := &Language{
		Name:        "c",
		SymbolNames: []string{"EOF", "ERROR", "translation_unit", "preproc_ifdef", "declaration"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "ERROR", Visible: true, Named: true},
			{Name: "translation_unit", Visible: true, Named: true},
			{Name: "preproc_ifdef", Visible: true, Named: true},
			{Name: "declaration", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	ifdef := newLeafNodeInArena(arena, 3, true, 0, 7, Point{}, Point{Column: 7})
	decl := newLeafNodeInArena(arena, 4, true, 8, 18, Point{Row: 1}, Point{Row: 1, Column: 10})
	root := newParentNodeInArena(arena, 1, true, []*Node{ifdef, decl}, nil, 0)
	root.hasError = true

	normalizeCTranslationUnitRoot(root, lang)

	if got, want := root.Type(lang), "translation_unit"; got != want {
		t.Fatalf("root.Type = %q, want %q", got, want)
	}
	if !root.HasError() {
		t.Fatalf("root.HasError = false, want true")
	}
}

func TestNormalizeGoSourceFileRootRetagsRecoveredTopLevelChildren(t *testing.T) {
	lang := &Language{
		Name:        "go",
		SymbolNames: []string{"EOF", "ERROR", "source_file", "package_clause", "function_declaration"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "ERROR", Visible: true, Named: true},
			{Name: "source_file", Visible: true, Named: true},
			{Name: "package_clause", Visible: true, Named: true},
			{Name: "function_declaration", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	pkg := newLeafNodeInArena(arena, 3, true, 0, 12, Point{}, Point{Column: 12})
	fn := newLeafNodeInArena(arena, 4, true, 13, 30, Point{Row: 1}, Point{Row: 1, Column: 17})
	root := newParentNodeInArena(arena, 1, true, []*Node{pkg, fn}, nil, 0)
	root.hasError = true

	normalizeGoSourceFileRoot(root, nil, &Parser{language: lang})

	if got, want := root.Type(lang), "source_file"; got != want {
		t.Fatalf("root.Type = %q, want %q", got, want)
	}
	if root.HasError() {
		t.Fatalf("root.HasError = true, want false")
	}
}

func TestNormalizeJavaScriptTopLevelObjectLiteralsRewritesObjectLiteral(t *testing.T) {
	lang := &Language{
		Name:        "javascript",
		SymbolNames: []string{"EOF", "program", "statement_block", "{", "}", "labeled_statement", "statement_identifier", ":", "expression_statement", "arrow_function", "object", "pair", "property_identifier"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "program", Visible: true, Named: true},
			{Name: "statement_block", Visible: true, Named: true},
			{Name: "{", Visible: true, Named: false},
			{Name: "}", Visible: true, Named: false},
			{Name: "labeled_statement", Visible: true, Named: true},
			{Name: "statement_identifier", Visible: true, Named: true},
			{Name: ":", Visible: true, Named: false},
			{Name: "expression_statement", Visible: true, Named: true},
			{Name: "arrow_function", Visible: true, Named: true},
			{Name: "object", Visible: true, Named: true},
			{Name: "pair", Visible: true, Named: true},
			{Name: "property_identifier", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	open := newLeafNodeInArena(arena, 3, false, 0, 1, Point{}, Point{Column: 1})
	key := newLeafNodeInArena(arena, 6, true, 2, 8, Point{Column: 2}, Point{Column: 8})
	colon := newLeafNodeInArena(arena, 7, false, 8, 9, Point{Column: 8}, Point{Column: 9})
	value := newLeafNodeInArena(arena, 9, true, 10, 16, Point{Column: 10}, Point{Column: 16})
	valueStmt := newParentNodeInArena(arena, 8, true, []*Node{value}, nil, 0)
	label := newParentNodeInArena(arena, 5, true, []*Node{key, colon, valueStmt}, nil, 0)
	close := newLeafNodeInArena(arena, 4, false, 17, 18, Point{Column: 17}, Point{Column: 18})
	block := newParentNodeInArena(arena, 2, true, []*Node{open, label, close}, nil, 0)
	root := newParentNodeInArena(arena, 1, true, []*Node{block}, nil, 0)

	normalizeJavaScriptTopLevelObjectLiterals(root, lang)

	if got, want := root.children[0].Type(lang), "expression_statement"; got != want {
		t.Fatalf("root.children[0].Type = %q, want %q", got, want)
	}
	object := root.children[0].children[0]
	if got, want := object.Type(lang), "object"; got != want {
		t.Fatalf("object.Type = %q, want %q", got, want)
	}
	pair := object.children[1]
	if got, want := pair.Type(lang), "pair"; got != want {
		t.Fatalf("pair.Type = %q, want %q", got, want)
	}
	if got, want := pair.children[0].Type(lang), "property_identifier"; got != want {
		t.Fatalf("pair.children[0].Type = %q, want %q", got, want)
	}
	if got, want := pair.children[2].Type(lang), "arrow_function"; got != want {
		t.Fatalf("pair.children[2].Type = %q, want %q", got, want)
	}
}

func TestNormalizeJavaScriptTopLevelExpressionStatementBoundsSnapToChildren(t *testing.T) {
	lang := &Language{
		Name:        "javascript",
		SymbolNames: []string{"EOF", "program", "expression_statement", "identifier", ";"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "program", Visible: true, Named: true},
			{Name: "expression_statement", Visible: true, Named: true},
			{Name: "identifier", Visible: true, Named: true},
			{Name: ";", Visible: true, Named: false},
		},
	}

	arena := newNodeArena(arenaClassFull)
	expr := newLeafNodeInArena(arena, 3, true, 10, 20, Point{Row: 1, Column: 2}, Point{Row: 1, Column: 12})
	semi := newLeafNodeInArena(arena, 4, false, 20, 21, Point{Row: 1, Column: 12}, Point{Row: 1, Column: 13})
	stmt := newParentNodeInArena(arena, 2, true, []*Node{expr, semi}, nil, 0)
	stmt.startByte = 0
	stmt.startPoint = Point{}
	stmt.endByte = 22
	stmt.endPoint = Point{Row: 2}
	root := newParentNodeInArena(arena, 1, true, []*Node{stmt}, nil, 0)

	normalizeJavaScriptTopLevelExpressionStatementBounds(root, lang)

	if got, want := stmt.StartByte(), uint32(10); got != want {
		t.Fatalf("stmt.StartByte = %d, want %d", got, want)
	}
	if got, want := stmt.EndByte(), uint32(21); got != want {
		t.Fatalf("stmt.EndByte = %d, want %d", got, want)
	}
	if got, want := stmt.StartPoint(), (Point{Row: 1, Column: 2}); got != want {
		t.Fatalf("stmt.StartPoint = %#v, want %#v", got, want)
	}
	if got, want := stmt.EndPoint(), (Point{Row: 1, Column: 13}); got != want {
		t.Fatalf("stmt.EndPoint = %#v, want %#v", got, want)
	}
}

func TestNormalizeLuaChunkLocalDeclarationFields(t *testing.T) {
	lang := &Language{
		Name:        "lua",
		FieldNames:  []string{"", "local_declaration"},
		SymbolNames: []string{"EOF", "chunk", "function_declaration", "function_call", "variable_declaration"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "chunk", Visible: true, Named: true},
			{Name: "function_declaration", Visible: true, Named: true},
			{Name: "function_call", Visible: true, Named: true},
			{Name: "variable_declaration", Visible: true, Named: true},
		},
	}

	source := []byte("local function foo() end\nprint(foo)\nlocal x = 1\n")
	arena := newNodeArena(arenaClassFull)
	localFn := newLeafNodeInArena(arena, 2, true, 0, 24, Point{}, Point{Row: 0, Column: 24})
	call := newLeafNodeInArena(arena, 3, true, 25, 35, Point{Row: 1}, Point{Row: 1, Column: 10})
	localVar := newLeafNodeInArena(arena, 4, true, 36, 47, Point{Row: 2}, Point{Row: 2, Column: 11})
	root := newParentNodeInArena(arena, 1, true, []*Node{localFn, call, localVar}, nil, 0)

	normalizeLuaChunkLocalDeclarationFields(root, source, lang)

	if got, want := root.FieldNameForChild(0, lang), "local_declaration"; got != want {
		t.Fatalf("root.FieldNameForChild(0) = %q, want %q", got, want)
	}
	if got := root.FieldNameForChild(1, lang); got != "" {
		t.Fatalf("root.FieldNameForChild(1) = %q, want empty", got)
	}
	if got, want := root.FieldNameForChild(2, lang), "local_declaration"; got != want {
		t.Fatalf("root.FieldNameForChild(2) = %q, want %q", got, want)
	}
}

func TestRepairPythonRootNodeCollapsesFlatClassFunctionFragments(t *testing.T) {
	lang := &Language{
		Name:        "python",
		FieldNames:  []string{"", "name", "parameters", "body", "superclasses"},
		SymbolNames: []string{"EOF", "module", "class", "identifier", "argument_list", ":", "_indent", "class_definition", "block", "def", "parameters", "function_definition", "assignment"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "module", Visible: true, Named: true},
			{Name: "class", Visible: true, Named: false},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "argument_list", Visible: true, Named: true},
			{Name: ":", Visible: true, Named: false},
			{Name: "_indent", Visible: true, Named: false},
			{Name: "class_definition", Visible: true, Named: true},
			{Name: "block", Visible: true, Named: true},
			{Name: "def", Visible: true, Named: false},
			{Name: "parameters", Visible: true, Named: true},
			{Name: "function_definition", Visible: true, Named: true},
			{Name: "assignment", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	classKw := newLeafNodeInArena(arena, 2, false, 0, 5, Point{}, Point{Column: 5})
	className := newLeafNodeInArena(arena, 3, true, 6, 16, Point{Column: 6}, Point{Column: 16})
	argList := newLeafNodeInArena(arena, 4, true, 16, 21, Point{Column: 16}, Point{Column: 21})
	classColon := newLeafNodeInArena(arena, 5, false, 21, 22, Point{Column: 21}, Point{Column: 22})
	classIndent := newLeafNodeInArena(arena, 6, false, 22, 22, Point{Column: 22}, Point{Column: 22})
	defKw := newLeafNodeInArena(arena, 9, false, 27, 30, Point{Row: 1, Column: 4}, Point{Row: 1, Column: 7})
	fnName := newLeafNodeInArena(arena, 3, true, 31, 40, Point{Row: 1, Column: 8}, Point{Row: 1, Column: 17})
	params := newLeafNodeInArena(arena, 10, true, 40, 46, Point{Row: 1, Column: 17}, Point{Row: 1, Column: 23})
	fnColon := newLeafNodeInArena(arena, 5, false, 46, 47, Point{Row: 1, Column: 23}, Point{Row: 1, Column: 24})
	fnIndent := newLeafNodeInArena(arena, 6, false, 47, 47, Point{Row: 1, Column: 24}, Point{Row: 1, Column: 24})
	assign := newLeafNodeInArena(arena, 12, true, 56, 67, Point{Row: 2, Column: 8}, Point{Row: 2, Column: 19})
	root := newParentNodeInArena(arena, 1, true, []*Node{classKw, className, argList, classColon, classIndent, defKw, fnName, params, fnColon, fnIndent, assign}, nil, 0)

	repaired := repairPythonRootNode(root, arena, lang)

	if got, want := len(repaired.children), 1; got != want {
		t.Fatalf("len(repaired.children) = %d, want %d", got, want)
	}
	classDef := repaired.children[0]
	if got, want := classDef.Type(lang), "class_definition"; got != want {
		t.Fatalf("classDef.Type = %q, want %q", got, want)
	}
	if got, want := classDef.FieldNameForChild(1, lang), "name"; got != want {
		t.Fatalf("classDef.FieldNameForChild(1) = %q, want %q", got, want)
	}
	if got, want := classDef.FieldNameForChild(2, lang), "superclasses"; got != want {
		t.Fatalf("classDef.FieldNameForChild(2) = %q, want %q", got, want)
	}
	if got, want := classDef.FieldNameForChild(4, lang), "body"; got != want {
		t.Fatalf("classDef.FieldNameForChild(4) = %q, want %q", got, want)
	}
	classBlock := classDef.children[4]
	if got, want := len(classBlock.children), 1; got != want {
		t.Fatalf("len(classBlock.children) = %d, want %d", got, want)
	}
	fn := classBlock.children[0]
	if got, want := fn.Type(lang), "function_definition"; got != want {
		t.Fatalf("fn.Type = %q, want %q", got, want)
	}
	if got, want := fn.FieldNameForChild(1, lang), "name"; got != want {
		t.Fatalf("fn.FieldNameForChild(1) = %q, want %q", got, want)
	}
	if got, want := fn.FieldNameForChild(2, lang), "parameters"; got != want {
		t.Fatalf("fn.FieldNameForChild(2) = %q, want %q", got, want)
	}
	if got, want := fn.FieldNameForChild(4, lang), "body"; got != want {
		t.Fatalf("fn.FieldNameForChild(4) = %q, want %q", got, want)
	}
}

func TestRepairPythonBlockFlattensSimpleStatements(t *testing.T) {
	lang := &Language{
		Name:        "python",
		SymbolNames: []string{"EOF", "block", "_simple_statements", "expression_statement", "assignment", "_simple_statements_repeat1", ";", "call"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "block", Visible: true, Named: true},
			{Name: "_simple_statements", Visible: true, Named: true},
			{Name: "expression_statement", Visible: true, Named: true},
			{Name: "assignment", Visible: true, Named: true},
			{Name: "_simple_statements_repeat1", Visible: true, Named: true},
			{Name: ";", Visible: true, Named: false},
			{Name: "call", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	assign := newLeafNodeInArena(arena, 4, true, 0, 5, Point{}, Point{Column: 5})
	call := newLeafNodeInArena(arena, 7, true, 6, 12, Point{Column: 6}, Point{Column: 12})
	exprAssign := newParentNodeInArena(arena, 3, true, []*Node{assign}, nil, 0)
	exprCall := newParentNodeInArena(arena, 3, true, []*Node{call}, nil, 0)
	semi := newLeafNodeInArena(arena, 6, false, 5, 6, Point{Column: 5}, Point{Column: 6})
	repeat := newParentNodeInArena(arena, 5, true, []*Node{semi, exprCall}, nil, 0)
	simple := newParentNodeInArena(arena, 2, true, []*Node{exprAssign, repeat}, nil, 0)
	block := newParentNodeInArena(arena, 1, true, []*Node{simple}, nil, 0)

	repaired, changed := repairPythonBlock(block, arena, lang, false)

	if !changed {
		t.Fatalf("repairPythonBlock changed = false, want true")
	}
	if got, want := len(repaired.children), 3; got != want {
		t.Fatalf("len(repaired.children) = %d, want %d", got, want)
	}
	if got, want := repaired.children[0].Type(lang), "assignment"; got != want {
		t.Fatalf("child[0].Type = %q, want %q", got, want)
	}
	if got, want := repaired.children[1].Type(lang), ";"; got != want {
		t.Fatalf("child[1].Type = %q, want %q", got, want)
	}
	if got, want := repaired.children[2].Type(lang), "call"; got != want {
		t.Fatalf("child[2].Type = %q, want %q", got, want)
	}
}

func TestNormalizePythonStringContinuationEscapesAddsMissingChildren(t *testing.T) {
	lang := &Language{
		Name:        "python",
		SymbolNames: []string{"EOF", "string_content", "escape_sequence"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "string_content", Visible: true, Named: true},
			{Name: "escape_sequence", Visible: true, Named: true},
		},
	}

	source := []byte("\\n\\\nfoo")
	arena := newNodeArena(arenaClassFull)
	first := newLeafNodeInArena(arena, 2, true, 0, 2, Point{}, Point{Column: 2})
	content := newParentNodeInArena(arena, 1, true, []*Node{first}, nil, 0)
	content.startByte = 0
	content.startPoint = Point{}
	content.endByte = uint32(len(source))
	content.endPoint = Point{Row: 1, Column: 3}

	normalizePythonStringContinuationEscapes(content, source, lang)

	if got, want := len(content.children), 2; got != want {
		t.Fatalf("len(content.children) = %d, want %d", got, want)
	}
	if got, want := content.children[1].startByte, uint32(2); got != want {
		t.Fatalf("content.children[1].startByte = %d, want %d", got, want)
	}
	if got, want := content.children[1].endByte, uint32(4); got != want {
		t.Fatalf("content.children[1].endByte = %d, want %d", got, want)
	}
}

func TestNormalizeDModuleDefinitionBoundsSnapToStructuralChildren(t *testing.T) {
	lang := &Language{
		Name:        "d",
		SymbolNames: []string{"EOF", "module_def", "module_declaration", "import_declaration"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "module_def", Visible: true, Named: true},
			{Name: "module_declaration", Visible: true, Named: true},
			{Name: "import_declaration", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	moduleDecl := newLeafNodeInArena(arena, 2, true, 133, 178, Point{Row: 4}, Point{Row: 4, Column: 45})
	importDecl := newLeafNodeInArena(arena, 3, true, 179, 9486, Point{Row: 5}, Point{Row: 319, Column: 1})
	moduleDef := newParentNodeInArena(arena, 1, true, []*Node{moduleDecl, importDecl}, nil, 0)
	moduleDef.startByte = 0
	moduleDef.startPoint = Point{}
	moduleDef.endByte = 9487
	moduleDef.endPoint = Point{Row: 319, Column: 2}

	normalizeDModuleDefinitionBounds(moduleDef, lang)

	if got, want := moduleDef.startByte, uint32(133); got != want {
		t.Fatalf("moduleDef.startByte = %d, want %d", got, want)
	}
	if got, want := moduleDef.startPoint, moduleDecl.startPoint; got != want {
		t.Fatalf("moduleDef.startPoint = %#v, want %#v", got, want)
	}
	if got, want := moduleDef.endByte, uint32(9486); got != want {
		t.Fatalf("moduleDef.endByte = %d, want %d", got, want)
	}
	if got, want := moduleDef.endPoint, importDecl.endPoint; got != want {
		t.Fatalf("moduleDef.endPoint = %#v, want %#v", got, want)
	}
}

func TestNormalizeHCLConfigFileRootDropsTopLevelWhitespace(t *testing.T) {
	lang := &Language{
		Name:        "hcl",
		SymbolNames: []string{"EOF", "config_file", "comment", "_whitespace", "body"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "config_file", Visible: true, Named: true},
			{Name: "comment", Visible: true, Named: true},
			{Name: "_whitespace", Visible: false, Named: false},
			{Name: "body", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	comment := newLeafNodeInArena(arena, 2, true, 0, 4, Point{}, Point{Column: 4})
	ws1 := newLeafNodeInArena(arena, 3, false, 4, 5, Point{Column: 4}, Point{Row: 1})
	body := newLeafNodeInArena(arena, 4, true, 5, 9, Point{Row: 1}, Point{Row: 1, Column: 4})
	ws2 := newLeafNodeInArena(arena, 3, false, 9, 10, Point{Row: 1, Column: 4}, Point{Row: 2})
	root := newParentNodeInArena(arena, 1, true, []*Node{comment, ws1, body, ws2}, nil, 0)

	normalizeHCLConfigFileRoot(root, lang)

	if got, want := len(root.children), 2; got != want {
		t.Fatalf("len(root.children) = %d, want %d", got, want)
	}
	if got, want := root.children[0].Type(lang), "comment"; got != want {
		t.Fatalf("root.children[0].Type = %q, want %q", got, want)
	}
	if got, want := root.children[1].Type(lang), "body"; got != want {
		t.Fatalf("root.children[1].Type = %q, want %q", got, want)
	}
}

func TestNormalizeHCLConfigFileRootSnapsBodyToStructuralChildren(t *testing.T) {
	lang := &Language{
		Name:        "hcl",
		SymbolNames: []string{"EOF", "config_file", "body", "block", "comment"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "config_file", Visible: true, Named: true},
			{Name: "body", Visible: true, Named: true},
			{Name: "block", Visible: true, Named: true},
			{Name: "comment", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	block := newLeafNodeInArena(arena, 3, true, 10, 20, Point{Row: 1}, Point{Row: 1, Column: 10})
	comment := newLeafNodeInArena(arena, 4, true, 22, 30, Point{Row: 2}, Point{Row: 2, Column: 8})
	body := newParentNodeInArena(arena, 2, true, []*Node{block, comment}, nil, 0)
	body.startByte = 0
	body.startPoint = Point{}
	body.endByte = 40
	body.endPoint = Point{Row: 4}
	root := newParentNodeInArena(arena, 1, true, []*Node{body}, nil, 0)

	normalizeHCLConfigFileRoot(root, lang)

	if got, want := body.startByte, uint32(10); got != want {
		t.Fatalf("body.startByte = %d, want %d", got, want)
	}
	if got, want := body.endByte, uint32(30); got != want {
		t.Fatalf("body.endByte = %d, want %d", got, want)
	}
	if got, want := body.startPoint, (Point{Row: 1}); got != want {
		t.Fatalf("body.startPoint = %#v, want %#v", got, want)
	}
	if got, want := body.endPoint, (Point{Row: 2, Column: 8}); got != want {
		t.Fatalf("body.endPoint = %#v, want %#v", got, want)
	}
}

func TestNormalizeSQLRecoveredSelectRootWrapsFlatSelectClause(t *testing.T) {
	lang := &Language{
		Name: "sql",
		SymbolNames: []string{
			"EOF", "source_file", "SELECT", "_aliasable_expression", "_expression", "type_cast",
			"select_clause_body_repeat1", ",", "comment", "select_statement", "select_clause",
			"select_clause_body", "NULL", "NULL",
		},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "source_file", Visible: true, Named: true},
			{Name: "SELECT", Visible: true, Named: false},
			{Name: "_aliasable_expression", Visible: true, Named: true},
			{Name: "_expression", Visible: true, Named: true},
			{Name: "type_cast", Visible: true, Named: true},
			{Name: "select_clause_body_repeat1", Visible: true, Named: true},
			{Name: ",", Visible: true, Named: false},
			{Name: "comment", Visible: true, Named: true},
			{Name: "select_statement", Visible: true, Named: true},
			{Name: "select_clause", Visible: true, Named: true},
			{Name: "select_clause_body", Visible: true, Named: true},
			{Name: "NULL", Visible: true, Named: true},
			{Name: "NULL", Visible: true, Named: false},
		},
	}

	arena := newNodeArena(arenaClassFull)
	selectTok := newLeafNodeInArena(arena, 2, false, 0, 6, Point{}, Point{Column: 6})
	firstExprLeaf := newLeafNodeInArena(arena, 5, true, 7, 8, Point{Column: 7}, Point{Column: 8})
	firstExpr := newParentNodeInArena(arena, 4, true, []*Node{firstExprLeaf}, nil, 0)
	firstAlias := newParentNodeInArena(arena, 3, true, []*Node{firstExpr}, nil, 0)
	comma1 := newLeafNodeInArena(arena, 7, false, 8, 9, Point{Column: 8}, Point{Column: 9})
	comment1 := newLeafNodeInArena(arena, 8, true, 10, 20, Point{Column: 10}, Point{Row: 1})
	secondExprLeaf := newLeafNodeInArena(arena, 5, true, 21, 22, Point{Row: 1}, Point{Row: 1, Column: 1})
	secondExpr := newParentNodeInArena(arena, 4, true, []*Node{secondExprLeaf}, nil, 0)
	secondAlias := newParentNodeInArena(arena, 3, true, []*Node{secondExpr}, nil, 0)
	repeat := newParentNodeInArena(arena, 6, true, []*Node{comma1, comment1, secondAlias}, nil, 0)
	comma2 := newLeafNodeInArena(arena, 7, false, 22, 23, Point{Row: 1, Column: 1}, Point{Row: 1, Column: 2})
	comment2 := newLeafNodeInArena(arena, 8, true, 24, 30, Point{Row: 1, Column: 3}, Point{Row: 1, Column: 9})
	root := newParentNodeInArena(arena, 1, true, []*Node{selectTok, firstAlias, repeat, comma2, comment2}, nil, 0)

	normalizeSQLRecoveredSelectRoot(root, lang)

	if got, want := len(root.children), 1; got != want {
		t.Fatalf("len(root.children) = %d, want %d", got, want)
	}
	if got, want := root.children[0].Type(lang), "select_statement"; got != want {
		t.Fatalf("root.children[0].Type = %q, want %q", got, want)
	}
	body := root.children[0].children[0].children[1]
	if got, want := body.Type(lang), "select_clause_body"; got != want {
		t.Fatalf("body.Type = %q, want %q", got, want)
	}
	if got, want := body.children[0].Type(lang), "type_cast"; got != want {
		t.Fatalf("body.children[0].Type = %q, want %q", got, want)
	}
	if got, want := body.children[len(body.children)-1].Type(lang), "NULL"; got != want {
		t.Fatalf("body.children[last].Type = %q, want %q", got, want)
	}
	if !root.HasError() {
		t.Fatalf("root.HasError = false, want true")
	}
}

func TestNormalizeBashProgramVariableAssignmentsSplitsTopLevelWrapper(t *testing.T) {
	lang := &Language{
		Name:        "bash",
		SymbolNames: []string{"EOF", "program", "comment", "variable_assignments", "variable_assignment", "if_statement"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "program", Visible: true, Named: true},
			{Name: "comment", Visible: true, Named: true},
			{Name: "variable_assignments", Visible: true, Named: true},
			{Name: "variable_assignment", Visible: true, Named: true},
			{Name: "if_statement", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	comment := newLeafNodeInArena(arena, 2, true, 0, 2, Point{}, Point{Column: 2})
	assign1 := newLeafNodeInArena(arena, 4, true, 3, 6, Point{Column: 3}, Point{Column: 6})
	assign2 := newLeafNodeInArena(arena, 4, true, 7, 10, Point{Column: 7}, Point{Column: 10})
	assigns := newParentNodeInArena(arena, 3, true, []*Node{assign1, assign2}, nil, 0)
	ifStmt := newLeafNodeInArena(arena, 5, true, 11, 15, Point{Column: 11}, Point{Column: 15})
	root := newParentNodeInArena(arena, 1, true, []*Node{comment, assigns, ifStmt}, nil, 0)

	normalizeBashProgramVariableAssignments(root, lang)

	if got, want := len(root.children), 4; got != want {
		t.Fatalf("len(root.children) = %d, want %d", got, want)
	}
	if got, want := root.children[1].Type(lang), "variable_assignment"; got != want {
		t.Fatalf("root.children[1].Type = %q, want %q", got, want)
	}
	if got, want := root.children[2].Type(lang), "variable_assignment"; got != want {
		t.Fatalf("root.children[2].Type = %q, want %q", got, want)
	}
	if got, want := root.children[3].Type(lang), "if_statement"; got != want {
		t.Fatalf("root.children[3].Type = %q, want %q", got, want)
	}
}

func TestNormalizeBashProgramVariableAssignmentsSplitsNestedIfWrapper(t *testing.T) {
	lang := &Language{
		Name:        "bash",
		SymbolNames: []string{"EOF", "program", "variable_assignments", "variable_assignment", "if_statement", "fi"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "program", Visible: true, Named: true},
			{Name: "variable_assignments", Visible: true, Named: true},
			{Name: "variable_assignment", Visible: true, Named: true},
			{Name: "if_statement", Visible: true, Named: true},
			{Name: "fi", Visible: true, Named: false},
		},
	}

	arena := newNodeArena(arenaClassFull)
	assign1 := newLeafNodeInArena(arena, 3, true, 0, 3, Point{}, Point{Column: 3})
	assign2 := newLeafNodeInArena(arena, 3, true, 4, 7, Point{Column: 4}, Point{Column: 7})
	assigns := newParentNodeInArena(arena, 2, true, []*Node{assign1, assign2}, nil, 0)
	fi := newLeafNodeInArena(arena, 5, false, 8, 10, Point{Column: 8}, Point{Column: 10})
	ifStmt := newParentNodeInArena(arena, 4, true, []*Node{assigns, fi}, nil, 0)
	root := newParentNodeInArena(arena, 1, true, []*Node{ifStmt}, nil, 0)

	normalizeBashProgramVariableAssignments(root, lang)

	if got, want := len(ifStmt.children), 3; got != want {
		t.Fatalf("len(ifStmt.children) = %d, want %d", got, want)
	}
	if got, want := ifStmt.children[0].Type(lang), "variable_assignment"; got != want {
		t.Fatalf("ifStmt.children[0].Type = %q, want %q", got, want)
	}
	if got, want := ifStmt.children[1].Type(lang), "variable_assignment"; got != want {
		t.Fatalf("ifStmt.children[1].Type = %q, want %q", got, want)
	}
}

func TestNormalizeBashProgramVariableAssignmentsAssignsIfConditionField(t *testing.T) {
	lang := &Language{
		Name:        "bash",
		SymbolNames: []string{"EOF", "program", "if_statement", "if", "test_command", "fi"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "program", Visible: true, Named: true},
			{Name: "if_statement", Visible: true, Named: true},
			{Name: "if", Visible: true, Named: false},
			{Name: "test_command", Visible: true, Named: true},
			{Name: "fi", Visible: true, Named: false},
		},
		FieldNames: []string{"", "condition"},
	}

	arena := newNodeArena(arenaClassFull)
	ifTok := newLeafNodeInArena(arena, 3, false, 0, 2, Point{}, Point{Column: 2})
	testCmd := newLeafNodeInArena(arena, 4, true, 3, 8, Point{Column: 3}, Point{Column: 8})
	fi := newLeafNodeInArena(arena, 5, false, 9, 11, Point{Column: 9}, Point{Column: 11})
	ifStmt := newParentNodeInArena(arena, 2, true, []*Node{ifTok, testCmd, fi}, nil, 0)
	root := newParentNodeInArena(arena, 1, true, []*Node{ifStmt}, nil, 0)

	normalizeBashProgramVariableAssignments(root, lang)

	if got, want := ifStmt.fieldIDs[1], FieldID(1); got != want {
		t.Fatalf("ifStmt.fieldIDs[1] = %d, want %d", got, want)
	}
	if got, want := ifStmt.fieldSources[1], fieldSourceDirect; got != want {
		t.Fatalf("ifStmt.fieldSources[1] = %v, want %v", got, want)
	}
}

func TestNormalizeBashProgramVariableAssignmentsExtendsIfConditionFieldToThenBoundary(t *testing.T) {
	lang := &Language{
		Name:        "bash",
		SymbolNames: []string{"EOF", "program", "if_statement", "if", "test_command", ";", "then", "fi"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "program", Visible: true, Named: true},
			{Name: "if_statement", Visible: true, Named: true},
			{Name: "if", Visible: true, Named: false},
			{Name: "test_command", Visible: true, Named: true},
			{Name: ";", Visible: true, Named: false},
			{Name: "then", Visible: true, Named: false},
			{Name: "fi", Visible: true, Named: false},
		},
		FieldNames: []string{"", "condition"},
	}

	arena := newNodeArena(arenaClassFull)
	ifTok := newLeafNodeInArena(arena, 3, false, 0, 2, Point{}, Point{Column: 2})
	testCmd := newLeafNodeInArena(arena, 4, true, 3, 8, Point{Column: 3}, Point{Column: 8})
	semi := newLeafNodeInArena(arena, 5, false, 8, 9, Point{Column: 8}, Point{Column: 9})
	thenTok := newLeafNodeInArena(arena, 6, false, 10, 14, Point{Column: 10}, Point{Column: 14})
	fi := newLeafNodeInArena(arena, 7, false, 15, 17, Point{Column: 15}, Point{Column: 17})
	ifStmt := newParentNodeInArena(arena, 2, true, []*Node{ifTok, testCmd, semi, thenTok, fi}, nil, 0)
	root := newParentNodeInArena(arena, 1, true, []*Node{ifStmt}, nil, 0)

	normalizeBashProgramVariableAssignments(root, lang)

	if got, want := ifStmt.fieldIDs[1], FieldID(1); got != want {
		t.Fatalf("ifStmt.fieldIDs[1] = %d, want %d", got, want)
	}
	if got, want := ifStmt.fieldIDs[2], FieldID(1); got != want {
		t.Fatalf("ifStmt.fieldIDs[2] = %d, want %d", got, want)
	}
}

func TestNormalizeBashProgramVariableAssignmentsSplitsSubshellWrapper(t *testing.T) {
	lang := &Language{
		Name:        "bash",
		SymbolNames: []string{"EOF", "program", "subshell", "variable_assignments", "variable_assignment", "(", ")"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "program", Visible: true, Named: true},
			{Name: "subshell", Visible: true, Named: true},
			{Name: "variable_assignments", Visible: true, Named: true},
			{Name: "variable_assignment", Visible: true, Named: true},
			{Name: "(", Visible: true, Named: false},
			{Name: ")", Visible: true, Named: false},
		},
	}

	arena := newNodeArena(arenaClassFull)
	open := newLeafNodeInArena(arena, 5, false, 0, 1, Point{}, Point{Column: 1})
	assign1 := newLeafNodeInArena(arena, 4, true, 1, 4, Point{Column: 1}, Point{Column: 4})
	assign2 := newLeafNodeInArena(arena, 4, true, 5, 8, Point{Column: 5}, Point{Column: 8})
	assigns := newParentNodeInArena(arena, 3, true, []*Node{assign1, assign2}, nil, 0)
	close := newLeafNodeInArena(arena, 6, false, 9, 10, Point{Column: 9}, Point{Column: 10})
	subshell := newParentNodeInArena(arena, 2, true, []*Node{open, assigns, close}, nil, 0)
	root := newParentNodeInArena(arena, 1, true, []*Node{subshell}, nil, 0)

	normalizeBashProgramVariableAssignments(root, lang)

	if got, want := len(subshell.children), 4; got != want {
		t.Fatalf("len(subshell.children) = %d, want %d", got, want)
	}
	if got, want := subshell.children[1].Type(lang), "variable_assignment"; got != want {
		t.Fatalf("subshell.children[1].Type = %q, want %q", got, want)
	}
	if got, want := subshell.children[2].Type(lang), "variable_assignment"; got != want {
		t.Fatalf("subshell.children[2].Type = %q, want %q", got, want)
	}
}

func TestNormalizeHTMLRecoveredNestedCustomTagsWrapsStartTagPrefix(t *testing.T) {
	lang := &Language{
		Name:        "html",
		SymbolNames: []string{"EOF", "ERROR", "document", "start_tag", "element", "end_tag", "</", "tag_name", ">"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "ERROR", Visible: true, Named: true},
			{Name: "document", Visible: true, Named: true},
			{Name: "start_tag", Visible: true, Named: true},
			{Name: "element", Visible: true, Named: true},
			{Name: "end_tag", Visible: true, Named: true},
			{Name: "</", Visible: true, Named: false},
			{Name: "tag_name", Visible: true, Named: true},
			{Name: ">", Visible: true, Named: false},
		},
	}

	arena := newNodeArena(arenaClassFull)
	start0 := newLeafNodeInArena(arena, 3, true, 0, 5, Point{}, Point{Column: 5})
	start1 := newLeafNodeInArena(arena, 3, true, 6, 11, Point{Row: 1}, Point{Row: 1, Column: 5})
	wrapped := newParentNodeInArena(arena, 1, true, []*Node{start1}, nil, 0)
	deepStart := newLeafNodeInArena(arena, 3, true, 11, 16, Point{Row: 2}, Point{Row: 2, Column: 5})
	leafElem := newParentNodeInArena(arena, 4, true, []*Node{deepStart}, nil, 0)
	leafElem.endByte = 20
	leafElem.endPoint = Point{Row: 3}
	closeTok := newLeafNodeInArena(arena, 6, false, 21, 23, Point{Row: 4}, Point{Row: 4, Column: 2})
	tagName := newLeafNodeInArena(arena, 7, true, 23, 26, Point{Row: 4, Column: 2}, Point{Row: 4, Column: 5})
	closeAngle := newLeafNodeInArena(arena, 8, false, 26, 27, Point{Row: 4, Column: 5}, Point{Row: 4, Column: 6})
	root := newParentNodeInArena(arena, 1, true, []*Node{start0, wrapped, leafElem, closeTok, tagName, closeAngle}, nil, 0)
	root.endByte = 28
	root.endPoint = Point{Row: 5}
	root.hasError = true

	normalizeHTMLRecoveredNestedCustomTags(root, lang)

	if got, want := root.Type(lang), "document"; got != want {
		t.Fatalf("root.Type = %q, want %q", got, want)
	}
	if got, want := len(root.children), 1; got != want {
		t.Fatalf("len(root.children) = %d, want %d", got, want)
	}
	outer := root.children[0]
	if got, want := outer.Type(lang), "element"; got != want {
		t.Fatalf("outer.Type = %q, want %q", got, want)
	}
	if got, want := len(outer.children), 3; got != want {
		t.Fatalf("len(outer.children) = %d, want %d", got, want)
	}
	if got, want := outer.children[2].Type(lang), "end_tag"; got != want {
		t.Fatalf("outer.children[2].Type = %q, want %q", got, want)
	}
	inner := outer.children[1]
	if got, want := inner.Type(lang), "element"; got != want {
		t.Fatalf("inner.Type = %q, want %q", got, want)
	}
	if got, want := inner.endByte, uint32(21); got != want {
		t.Fatalf("inner.endByte = %d, want %d", got, want)
	}
}

func TestNormalizeHTMLRecoveredNestedCustomTagRangesExtendsInnerChain(t *testing.T) {
	lang := &Language{
		Name:        "html",
		SymbolNames: []string{"EOF", "document", "element", "start_tag", "end_tag", "</", "tag_name", ">"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "document", Visible: true, Named: true},
			{Name: "element", Visible: true, Named: true},
			{Name: "start_tag", Visible: true, Named: true},
			{Name: "end_tag", Visible: true, Named: true},
			{Name: "</", Visible: true, Named: false},
			{Name: "tag_name", Visible: true, Named: true},
			{Name: ">", Visible: true, Named: false},
		},
	}

	arena := newNodeArena(arenaClassFull)
	start0 := newLeafNodeInArena(arena, 3, true, 0, 5, Point{}, Point{Column: 5})
	start1 := newLeafNodeInArena(arena, 3, true, 6, 11, Point{Row: 1}, Point{Row: 1, Column: 5})
	start2 := newLeafNodeInArena(arena, 3, true, 11, 16, Point{Row: 1, Column: 5}, Point{Row: 1, Column: 10})
	leaf := newParentNodeInArena(arena, 2, true, []*Node{start2}, nil, 0)
	leaf.endByte = 20
	leaf.endPoint = Point{Row: 3}
	inner := newParentNodeInArena(arena, 2, true, []*Node{start1, leaf}, nil, 0)
	inner.endByte = 20
	inner.endPoint = Point{Row: 3}
	closeTok := newLeafNodeInArena(arena, 5, false, 21, 23, Point{Row: 4}, Point{Row: 4, Column: 2})
	tagName := newLeafNodeInArena(arena, 6, true, 23, 26, Point{Row: 4, Column: 2}, Point{Row: 4, Column: 5})
	closeAngle := newLeafNodeInArena(arena, 7, false, 26, 27, Point{Row: 4, Column: 5}, Point{Row: 4, Column: 6})
	endTag := newParentNodeInArena(arena, 4, true, []*Node{closeTok, tagName, closeAngle}, nil, 0)
	outer := newParentNodeInArena(arena, 2, true, []*Node{start0, inner, endTag}, nil, 0)
	root := newParentNodeInArena(arena, 1, true, []*Node{outer}, nil, 0)

	source := bytes.Repeat([]byte{'x'}, 27)
	source[20] = '\n'
	normalizeHTMLRecoveredNestedCustomTagRanges(root, source, lang)

	if got, want := inner.endByte, uint32(21); got != want {
		t.Fatalf("inner.endByte = %d, want %d", got, want)
	}
	if got, want := leaf.endByte, uint32(21); got != want {
		t.Fatalf("leaf.endByte = %d, want %d", got, want)
	}
}

func TestNormalizeHTMLRecoveredNestedCustomTagsExtendsContinuationRange(t *testing.T) {
	lang := &Language{
		Name:        "html",
		SymbolNames: []string{"EOF", "ERROR", "document", "start_tag", "element", "end_tag", "</", "tag_name", ">"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "ERROR", Visible: true, Named: true},
			{Name: "document", Visible: true, Named: true},
			{Name: "start_tag", Visible: true, Named: true},
			{Name: "element", Visible: true, Named: true},
			{Name: "end_tag", Visible: true, Named: true},
			{Name: "</", Visible: true, Named: false},
			{Name: "tag_name", Visible: true, Named: true},
			{Name: ">", Visible: true, Named: false},
		},
	}

	arena := newNodeArena(arenaClassFull)
	start0 := newLeafNodeInArena(arena, 3, true, 0, 5, Point{}, Point{Column: 5})
	start1 := newLeafNodeInArena(arena, 3, true, 6, 11, Point{Row: 1}, Point{Row: 1, Column: 5})
	start2 := newLeafNodeInArena(arena, 3, true, 11, 16, Point{Row: 1, Column: 5}, Point{Row: 1, Column: 10})
	leaf := newParentNodeInArena(arena, 4, true, []*Node{start2}, nil, 0)
	leaf.endByte = 20
	leaf.endPoint = Point{Row: 3}
	continuation := newParentNodeInArena(arena, 4, true, []*Node{start1, leaf}, nil, 0)
	continuation.endByte = 20
	continuation.endPoint = Point{Row: 3}
	closeTok := newLeafNodeInArena(arena, 6, false, 21, 23, Point{Row: 4}, Point{Row: 4, Column: 2})
	tagName := newLeafNodeInArena(arena, 7, true, 23, 26, Point{Row: 4, Column: 2}, Point{Row: 4, Column: 5})
	closeAngle := newLeafNodeInArena(arena, 8, false, 26, 27, Point{Row: 4, Column: 5}, Point{Row: 4, Column: 6})
	root := newParentNodeInArena(arena, 1, true, []*Node{start0, continuation, closeTok, tagName, closeAngle}, nil, 0)
	root.hasError = true

	normalizeHTMLRecoveredNestedCustomTags(root, lang)

	if got, want := root.Type(lang), "document"; got != want {
		t.Fatalf("root.Type = %q, want %q", got, want)
	}
	inner := root.children[0].children[1]
	if got, want := inner.endByte, uint32(21); got != want {
		t.Fatalf("inner.endByte = %d, want %d", got, want)
	}
	if got, want := inner.children[1].endByte, uint32(21); got != want {
		t.Fatalf("inner.children[1].endByte = %d, want %d", got, want)
	}
}

func TestNormalizeDSourceFileLeadingTriviaSnapsToFirstChild(t *testing.T) {
	lang := &Language{
		Name:        "d",
		SymbolNames: []string{"EOF", "source_file", "variable_declaration"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "source_file", Visible: true, Named: true},
			{Name: "variable_declaration", Visible: true, Named: true},
		},
	}

	source := []byte("\nint i = 1;\n")
	arena := newNodeArena(arenaClassFull)
	decl := newLeafNodeInArena(arena, 2, true, 1, 11, Point{Row: 1}, Point{Row: 1, Column: 10})
	root := newParentNodeInArena(arena, 1, true, []*Node{decl}, nil, 0)
	root.startByte = 0
	root.startPoint = Point{}

	normalizeDSourceFileLeadingTrivia(root, source, lang)

	if got, want := root.startByte, uint32(1); got != want {
		t.Fatalf("root.startByte = %d, want %d", got, want)
	}
	if got, want := root.startPoint, decl.startPoint; got != want {
		t.Fatalf("root.startPoint = %#v, want %#v", got, want)
	}
}

func TestNormalizeDVariableStorageClassWrappersWrapsStaticLeaf(t *testing.T) {
	lang := &Language{
		Name:        "d",
		SymbolNames: []string{"EOF", "variable_declaration", "storage_class", "static", "type", "declarator", ";"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "variable_declaration", Visible: true, Named: true},
			{Name: "storage_class", Visible: true, Named: true},
			{Name: "static", Visible: true, Named: false},
			{Name: "type", Visible: true, Named: true},
			{Name: "declarator", Visible: true, Named: true},
			{Name: ";", Visible: true, Named: false},
		},
	}

	arena := newNodeArena(arenaClassFull)
	staticLeaf := newLeafNodeInArena(arena, 3, false, 0, 6, Point{}, Point{Column: 6})
	typ := newLeafNodeInArena(arena, 4, true, 7, 17, Point{Column: 7}, Point{Column: 17})
	decl := newLeafNodeInArena(arena, 5, true, 18, 26, Point{Column: 18}, Point{Column: 26})
	semi := newLeafNodeInArena(arena, 6, false, 26, 27, Point{Column: 26}, Point{Column: 27})
	varDecl := newParentNodeInArena(arena, 1, true, []*Node{staticLeaf, typ, decl, semi}, nil, 0)

	normalizeDVariableStorageClassWrappers(varDecl, lang)

	if got, want := varDecl.children[0].Type(lang), "storage_class"; got != want {
		t.Fatalf("varDecl.children[0].Type = %q, want %q", got, want)
	}
	if got, want := len(varDecl.children[0].children), 1; got != want {
		t.Fatalf("storage_class child count = %d, want %d", got, want)
	}
	if got, want := varDecl.children[0].children[0].Type(lang), "static"; got != want {
		t.Fatalf("wrapped child type = %q, want %q", got, want)
	}
}

func TestNormalizeDCallExpressionTemplateTypesWrapsLeadingTemplateInstance(t *testing.T) {
	lang := &Language{
		Name:        "d",
		SymbolNames: []string{"EOF", "call_expression", "type", "template_instance", "named_arguments"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "call_expression", Visible: true, Named: true},
			{Name: "type", Visible: true, Named: true},
			{Name: "template_instance", Visible: true, Named: true},
			{Name: "named_arguments", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	templateInstance := newLeafNodeInArena(arena, 3, true, 0, 15, Point{}, Point{Column: 15})
	args := newLeafNodeInArena(arena, 4, true, 15, 17, Point{Column: 15}, Point{Column: 17})
	call := newParentNodeInArena(arena, 1, true, []*Node{templateInstance, args}, nil, 0)

	normalizeDCallExpressionTemplateTypes(call, lang)

	if got, want := call.children[0].Type(lang), "type"; got != want {
		t.Fatalf("call.children[0].Type = %q, want %q", got, want)
	}
	if got, want := len(call.children[0].children), 1; got != want {
		t.Fatalf("type child count = %d, want %d", got, want)
	}
	if got, want := call.children[0].children[0].Type(lang), "template_instance"; got != want {
		t.Fatalf("wrapped child type = %q, want %q", got, want)
	}
}

func TestNormalizeDVariableTypeQualifiersMergesSharedIntoType(t *testing.T) {
	lang := &Language{
		Name:        "d",
		SymbolNames: []string{"EOF", "variable_declaration", "storage_class", "type_ctor", "shared", "type", "identifier", "declarator", ";"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "variable_declaration", Visible: true, Named: true},
			{Name: "storage_class", Visible: true, Named: true},
			{Name: "type_ctor", Visible: true, Named: true},
			{Name: "shared", Visible: true, Named: false},
			{Name: "type", Visible: true, Named: true},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "declarator", Visible: true, Named: true},
			{Name: ";", Visible: true, Named: false},
		},
	}

	arena := newNodeArena(arenaClassFull)
	sharedLeaf := newLeafNodeInArena(arena, 4, false, 7, 13, Point{Column: 7}, Point{Column: 13})
	typeCtor := newParentNodeInArena(arena, 3, true, []*Node{sharedLeaf}, nil, 0)
	storageClass := newParentNodeInArena(arena, 2, true, []*Node{typeCtor}, nil, 0)
	ident := newLeafNodeInArena(arena, 6, true, 14, 31, Point{Column: 14}, Point{Column: 31})
	typ := newParentNodeInArena(arena, 5, true, []*Node{ident}, nil, 0)
	decl := newLeafNodeInArena(arena, 7, true, 32, 40, Point{Column: 32}, Point{Column: 40})
	semi := newLeafNodeInArena(arena, 8, false, 40, 41, Point{Column: 40}, Point{Column: 41})
	varDecl := newParentNodeInArena(arena, 1, true, []*Node{storageClass, typ, decl, semi}, nil, 0)

	normalizeDVariableTypeQualifiers(varDecl, lang)

	if got, want := len(varDecl.children), 3; got != want {
		t.Fatalf("variable child count = %d, want %d", got, want)
	}
	if got, want := varDecl.children[0].Type(lang), "type"; got != want {
		t.Fatalf("varDecl.children[0].Type = %q, want %q", got, want)
	}
	if got, want := len(varDecl.children[0].children), 2; got != want {
		t.Fatalf("type child count = %d, want %d", got, want)
	}
	if got, want := varDecl.children[0].children[0].Type(lang), "type_ctor"; got != want {
		t.Fatalf("type child[0] = %q, want %q", got, want)
	}
}

func TestNormalizeDCallExpressionPropertyTypesWrapsQualifiedTarget(t *testing.T) {
	lang := &Language{
		Name:        "d",
		SymbolNames: []string{"EOF", "call_expression", "type", "property_expression", "identifier", ".", "named_arguments"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "call_expression", Visible: true, Named: true},
			{Name: "type", Visible: true, Named: true},
			{Name: "property_expression", Visible: true, Named: true},
			{Name: "identifier", Visible: true, Named: true},
			{Name: ".", Visible: true, Named: false},
			{Name: "named_arguments", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	a := newLeafNodeInArena(arena, 4, true, 0, 1, Point{}, Point{Column: 1})
	dot1 := newLeafNodeInArena(arena, 5, false, 1, 2, Point{Column: 1}, Point{Column: 2})
	b := newLeafNodeInArena(arena, 4, true, 2, 3, Point{Column: 2}, Point{Column: 3})
	left := newParentNodeInArena(arena, 3, true, []*Node{a, dot1, b}, nil, 0)
	dot2 := newLeafNodeInArena(arena, 5, false, 3, 4, Point{Column: 3}, Point{Column: 4})
	c := newLeafNodeInArena(arena, 4, true, 4, 5, Point{Column: 4}, Point{Column: 5})
	prop := newParentNodeInArena(arena, 3, true, []*Node{left, dot2, c}, nil, 0)
	args := newLeafNodeInArena(arena, 6, true, 5, 7, Point{Column: 5}, Point{Column: 7})
	call := newParentNodeInArena(arena, 1, true, []*Node{prop, args}, nil, 0)

	normalizeDCallExpressionPropertyTypes(call, lang)

	if got, want := call.children[0].Type(lang), "type"; got != want {
		t.Fatalf("call.children[0].Type = %q, want %q", got, want)
	}
	if got, want := len(call.children[0].children), 5; got != want {
		t.Fatalf("type child count = %d, want %d", got, want)
	}
}

func TestNormalizeDCallExpressionSimpleTypeCalleesUnwrapsSingleIdentifier(t *testing.T) {
	lang := &Language{
		Name:        "d",
		SymbolNames: []string{"EOF", "call_expression", "type", "identifier", "named_arguments"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "call_expression", Visible: true, Named: true},
			{Name: "type", Visible: true, Named: true},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "named_arguments", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	ident := newLeafNodeInArena(arena, 3, true, 0, 10, Point{}, Point{Column: 10})
	typ := newParentNodeInArena(arena, 2, true, []*Node{ident}, nil, 0)
	args := newLeafNodeInArena(arena, 4, true, 10, 12, Point{Column: 10}, Point{Column: 12})
	call := newParentNodeInArena(arena, 1, true, []*Node{typ, args}, nil, 0)

	normalizeDCallExpressionSimpleTypeCallees(call, lang)

	if got, want := call.children[0].Type(lang), "identifier"; got != want {
		t.Fatalf("call.children[0].Type = %q, want %q", got, want)
	}
}

func TestNormalizeZigEmptyInitListFieldConstantCleared(t *testing.T) {
	lang := &Language{
		Name:        "zig",
		SymbolNames: []string{"EOF", "SuffixExpr", ".", "InitList", "{", "}"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "SuffixExpr", Visible: true, Named: true},
			{Name: ".", Visible: true, Named: false},
			{Name: "InitList", Visible: true, Named: true},
			{Name: "{", Visible: true, Named: false},
			{Name: "}", Visible: true, Named: false},
		},
		FieldNames: []string{"", "field_constant"},
	}

	arena := newNodeArena(arenaClassFull)
	dot := newLeafNodeInArena(arena, 2, false, 0, 1, Point{}, Point{Column: 1})
	open := newLeafNodeInArena(arena, 4, false, 1, 2, Point{Column: 1}, Point{Column: 2})
	close := newLeafNodeInArena(arena, 5, false, 2, 3, Point{Column: 2}, Point{Column: 3})
	initList := newParentNodeInArena(arena, 3, true, []*Node{open, close}, nil, 0)
	parent := newParentNodeInArena(arena, 1, true, []*Node{dot, initList}, []FieldID{0, 1}, 0)
	parent.fieldSources = []uint8{0, fieldSourceDirect}

	normalizeZigEmptyInitListFields(parent, lang)

	if got := parent.fieldIDs[1]; got != 0 {
		t.Fatalf("fieldIDs[1] = %d, want 0", got)
	}
	if got := fieldSourceAt(parent.fieldSources, 1); got != 0 {
		t.Fatalf("fieldSources[1] = %d, want 0", got)
	}
}

func TestNormalizeZigDottedInitListFieldConstantCleared(t *testing.T) {
	lang := &Language{
		Name:        "zig",
		SymbolNames: []string{"EOF", "SuffixExpr", ".", "InitList", "{", "STRINGLITERALSINGLE", "}"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "SuffixExpr", Visible: true, Named: true},
			{Name: ".", Visible: true, Named: false},
			{Name: "InitList", Visible: true, Named: true},
			{Name: "{", Visible: true, Named: false},
			{Name: "STRINGLITERALSINGLE", Visible: true, Named: true},
			{Name: "}", Visible: true, Named: false},
		},
		FieldNames: []string{"", "field_constant"},
	}

	arena := newNodeArena(arenaClassFull)
	dot := newLeafNodeInArena(arena, 2, false, 0, 1, Point{}, Point{Column: 1})
	open := newLeafNodeInArena(arena, 4, false, 1, 2, Point{Column: 1}, Point{Column: 2})
	value := newLeafNodeInArena(arena, 5, true, 2, 6, Point{Column: 2}, Point{Column: 6})
	close := newLeafNodeInArena(arena, 6, false, 6, 7, Point{Column: 6}, Point{Column: 7})
	initList := newParentNodeInArena(arena, 3, true, []*Node{open, value, close}, nil, 0)
	parent := newParentNodeInArena(arena, 1, true, []*Node{dot, initList}, []FieldID{0, 1}, 0)
	parent.fieldSources = []uint8{0, fieldSourceDirect}

	normalizeZigEmptyInitListFields(parent, lang)

	if got := parent.fieldIDs[1]; got != 0 {
		t.Fatalf("fieldIDs[1] = %d, want 0", got)
	}
	if got := fieldSourceAt(parent.fieldSources, 1); got != 0 {
		t.Fatalf("fieldSources[1] = %d, want 0", got)
	}
}

func TestBuildReduceChildrenNoAliasNoFieldsInlinesHiddenChildren(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden", "identifier", "operator"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden", Visible: false, Named: false},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "operator", Visible: true, Named: false},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	left := newLeafNodeInArena(arena, 2, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1})
	op := newLeafNodeInArena(arena, 3, false, 2, 3, Point{Row: 0, Column: 2}, Point{Row: 0, Column: 3})
	hidden := newParentNodeInArena(arena, 1, false, []*Node{left, op}, nil, 0)
	right := newLeafNodeInArena(arena, 2, true, 4, 5, Point{Row: 0, Column: 4}, Point{Row: 0, Column: 5})

	children, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hidden}, {node: right}}, 0, 2, 2, 2, 0, arena)
	if got, want := len(children), 3; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if fieldIDs != nil {
		t.Fatalf("fieldIDs = %#v, want nil", fieldIDs)
	}
	if children[0] != left || children[1] != op || children[2] != right {
		t.Fatalf("children order = %#v, want hidden children then right leaf", children)
	}
}

func TestBuildReduceChildrenHiddenParentDefersFlattenUntilVisibleBoundary(t *testing.T) {
	lang := &Language{
		SymbolNames: []string{"EOF", "_hidden_a", "_hidden_b", "identifier", "operator", "visible_parent"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "_hidden_a", Visible: false, Named: false},
			{Name: "_hidden_b", Visible: false, Named: false},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "operator", Visible: true, Named: false},
			{Name: "visible_parent", Visible: true, Named: true},
		},
	}

	parser := NewParser(lang)
	arena := newNodeArena(arenaClassFull)
	left := newLeafNodeInArena(arena, 3, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1})
	op := newLeafNodeInArena(arena, 4, false, 2, 3, Point{Row: 0, Column: 2}, Point{Row: 0, Column: 3})
	right := newLeafNodeInArena(arena, 3, true, 4, 5, Point{Row: 0, Column: 4}, Point{Row: 0, Column: 5})
	tail := newLeafNodeInArena(arena, 3, true, 6, 7, Point{Row: 0, Column: 6}, Point{Row: 0, Column: 7})

	hiddenInner := newParentNodeInArena(arena, 2, false, []*Node{left, op}, nil, 0)
	hiddenOuterChildren, _, _ := parser.buildReduceChildren([]stackEntry{{node: hiddenInner}, {node: right}}, 0, 2, 2, 1, 0, arena)
	if got, want := len(hiddenOuterChildren), 2; got != want {
		t.Fatalf("len(hiddenOuterChildren) = %d, want %d", got, want)
	}
	if hiddenOuterChildren[0] != hiddenInner || hiddenOuterChildren[1] != right {
		t.Fatalf("hidden outer children = %#v, want compact hidden child then right", hiddenOuterChildren)
	}

	hiddenOuter := newParentNodeInArena(arena, 1, false, hiddenOuterChildren, nil, 0)
	visibleChildren, fieldIDs, _ := parser.buildReduceChildren([]stackEntry{{node: hiddenOuter}, {node: tail}}, 0, 2, 2, 5, 0, arena)
	if fieldIDs != nil {
		t.Fatalf("fieldIDs = %#v, want nil", fieldIDs)
	}
	if got, want := len(visibleChildren), 4; got != want {
		t.Fatalf("len(visibleChildren) = %d, want %d", got, want)
	}
	if visibleChildren[0] != left || visibleChildren[1] != op || visibleChildren[2] != right || visibleChildren[3] != tail {
		t.Fatalf("visible children order = %#v, want fully flattened hidden chain plus tail", visibleChildren)
	}
}

func TestNormalizePerlJoinAssignmentListsRewritesBareListOperatorShape(t *testing.T) {
	lang := &Language{
		Name:        "perl",
		SymbolNames: []string{"EOF", "source_file", "expression_statement", "assignment_expression", "variable_declaration", "=", "ambiguous_function_call_expression", "function", "list_expression", ",", "string_literal"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "source_file", Visible: true, Named: true},
			{Name: "expression_statement", Visible: true, Named: true},
			{Name: "assignment_expression", Visible: true, Named: true},
			{Name: "variable_declaration", Visible: true, Named: true},
			{Name: "=", Visible: true, Named: false},
			{Name: "ambiguous_function_call_expression", Visible: true, Named: true},
			{Name: "function", Visible: true, Named: true},
			{Name: "list_expression", Visible: true, Named: true},
			{Name: ",", Visible: true, Named: false},
			{Name: "string_literal", Visible: true, Named: true},
		},
	}

	source := []byte("my $x = join \"\\n\", \"a\", \"b\"")
	arena := newNodeArena(arenaClassFull)

	lhs := newLeafNodeInArena(arena, 4, true, 0, 5, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 5})
	eq := newLeafNodeInArena(arena, 5, false, 6, 7, Point{Row: 0, Column: 6}, Point{Row: 0, Column: 7})
	fn := newLeafNodeInArena(arena, 7, true, 8, 12, Point{Row: 0, Column: 8}, Point{Row: 0, Column: 12})
	arg0 := newLeafNodeInArena(arena, 10, true, 13, 17, Point{Row: 0, Column: 13}, Point{Row: 0, Column: 17})
	comma0 := newLeafNodeInArena(arena, 9, false, 17, 18, Point{Row: 0, Column: 17}, Point{Row: 0, Column: 18})
	arg1 := newLeafNodeInArena(arena, 10, true, 19, 22, Point{Row: 0, Column: 19}, Point{Row: 0, Column: 22})
	comma1 := newLeafNodeInArena(arena, 9, false, 22, 23, Point{Row: 0, Column: 22}, Point{Row: 0, Column: 23})
	arg2 := newLeafNodeInArena(arena, 10, true, 24, 27, Point{Row: 0, Column: 24}, Point{Row: 0, Column: 27})

	args := newParentNodeInArena(arena, 8, true, []*Node{arg0, comma0, arg1, comma1, arg2}, nil, 0)
	call := newParentNodeInArena(arena, 6, true, []*Node{fn, args}, nil, 0)
	assign := newParentNodeInArena(arena, 3, true, []*Node{lhs, eq, call}, nil, 0)
	stmt := newParentNodeInArena(arena, 2, true, []*Node{assign}, nil, 0)
	root := newParentNodeInArena(arena, 1, true, []*Node{stmt}, nil, 0)

	normalizePerlJoinAssignmentLists(root, source, lang)

	rewritten := stmt.Child(0)
	if rewritten == nil {
		t.Fatal("expression_statement lost child after normalization")
	}
	if got := rewritten.Type(lang); got != "list_expression" {
		t.Fatalf("rewritten child type = %q, want list_expression", got)
	}
	if got, want := rewritten.ChildCount(), 5; got != want {
		t.Fatalf("rewritten child count = %d, want %d", got, want)
	}
	assign = rewritten.Child(0)
	if assign == nil || assign.Type(lang) != "assignment_expression" {
		t.Fatalf("rewritten first child = %v, want assignment_expression", assign)
	}
	call = assign.Child(2)
	if call == nil || call.Type(lang) != "ambiguous_function_call_expression" {
		t.Fatalf("rewritten assignment rhs = %v, want ambiguous_function_call_expression", call)
	}
	if got, want := call.ChildCount(), 2; got != want {
		t.Fatalf("rewritten call child count = %d, want %d", got, want)
	}
	if got := call.Child(1).Type(lang); got != "string_literal" {
		t.Fatalf("rewritten first argument type = %q, want string_literal", got)
	}
	if got, want := call.EndByte(), uint32(17); got != want {
		t.Fatalf("rewritten call end byte = %d, want %d", got, want)
	}
	if got := rewritten.Child(2).Type(lang); got != "string_literal" {
		t.Fatalf("rewritten third child type = %q, want string_literal", got)
	}
	if got := rewritten.Child(4).Type(lang); got != "string_literal" {
		t.Fatalf("rewritten fifth child type = %q, want string_literal", got)
	}
}

func TestNormalizePerlPushExpressionListsRewritesRootListShape(t *testing.T) {
	lang := &Language{
		Name:        "perl",
		SymbolNames: []string{"EOF", "source_file", "expression_statement", "ambiguous_function_call_expression", "function", "list_expression", ",", "array", "scalar"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "source_file", Visible: true, Named: true},
			{Name: "expression_statement", Visible: true, Named: true},
			{Name: "ambiguous_function_call_expression", Visible: true, Named: true},
			{Name: "function", Visible: true, Named: true},
			{Name: "list_expression", Visible: true, Named: true},
			{Name: ",", Visible: true, Named: false},
			{Name: "array", Visible: true, Named: true},
			{Name: "scalar", Visible: true, Named: true},
		},
	}

	source := []byte("push @found, $_")
	arena := newNodeArena(arenaClassFull)

	fn := newLeafNodeInArena(arena, 4, true, 0, 4, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 4})
	arg0 := newLeafNodeInArena(arena, 7, true, 5, 11, Point{Row: 0, Column: 5}, Point{Row: 0, Column: 11})
	comma := newLeafNodeInArena(arena, 6, false, 11, 12, Point{Row: 0, Column: 11}, Point{Row: 0, Column: 12})
	arg1 := newLeafNodeInArena(arena, 8, true, 13, 15, Point{Row: 0, Column: 13}, Point{Row: 0, Column: 15})

	call := newParentNodeInArena(arena, 3, true, []*Node{fn, arg0}, nil, 0)
	list := newParentNodeInArena(arena, 5, true, []*Node{call, comma, arg1}, nil, 0)
	stmt := newParentNodeInArena(arena, 2, true, []*Node{list}, nil, 0)
	root := newParentNodeInArena(arena, 1, true, []*Node{stmt}, nil, 0)

	normalizePerlPushExpressionLists(root, source, lang)

	rewritten := stmt.Child(0)
	if rewritten == nil {
		t.Fatal("expression_statement lost child after normalization")
	}
	if got := rewritten.Type(lang); got != "ambiguous_function_call_expression" {
		t.Fatalf("rewritten child type = %q, want ambiguous_function_call_expression", got)
	}
	if got, want := rewritten.ChildCount(), 2; got != want {
		t.Fatalf("rewritten child count = %d, want %d", got, want)
	}
	args := rewritten.Child(1)
	if args == nil || args.Type(lang) != "list_expression" {
		t.Fatalf("rewritten arguments = %v, want list_expression", args)
	}
	if got, want := args.ChildCount(), 3; got != want {
		t.Fatalf("rewritten args child count = %d, want %d", got, want)
	}
	if got := args.Child(0).Type(lang); got != "array" {
		t.Fatalf("rewritten first arg type = %q, want array", got)
	}
	if got := args.Child(2).Type(lang); got != "scalar" {
		t.Fatalf("rewritten third arg type = %q, want scalar", got)
	}
}

func TestNormalizePerlReturnExpressionListsPromotesCommaTail(t *testing.T) {
	lang := &Language{
		Name:        "perl",
		SymbolNames: []string{"EOF", "source_file", "expression_statement", "return_expression", "return", "list_expression", ",", "ambiguous_function_call_expression", "function", "string_literal", "array_element_expression"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "source_file", Visible: true, Named: true},
			{Name: "expression_statement", Visible: true, Named: true},
			{Name: "return_expression", Visible: true, Named: true},
			{Name: "return", Visible: true, Named: false},
			{Name: "list_expression", Visible: true, Named: true},
			{Name: ",", Visible: true, Named: false},
			{Name: "ambiguous_function_call_expression", Visible: true, Named: true},
			{Name: "function", Visible: true, Named: true},
			{Name: "string_literal", Visible: true, Named: true},
			{Name: "array_element_expression", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)

	retTok := newLeafNodeInArena(arena, 4, false, 0, 6, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 6})
	fn := newLeafNodeInArena(arena, 8, true, 7, 14, Point{Row: 0, Column: 7}, Point{Row: 0, Column: 14})
	arg0 := newLeafNodeInArena(arena, 9, true, 15, 18, Point{Row: 0, Column: 15}, Point{Row: 0, Column: 18})
	comma := newLeafNodeInArena(arena, 6, false, 18, 19, Point{Row: 0, Column: 18}, Point{Row: 0, Column: 19})
	arg1 := newLeafNodeInArena(arena, 10, true, 20, 31, Point{Row: 0, Column: 20}, Point{Row: 0, Column: 31})

	call := newParentNodeInArena(arena, 7, true, []*Node{fn, arg0}, nil, 0)
	list := newParentNodeInArena(arena, 5, true, []*Node{call, comma, arg1}, nil, 0)
	ret := newParentNodeInArena(arena, 3, true, []*Node{retTok, list}, nil, 0)
	stmt := newParentNodeInArena(arena, 2, true, []*Node{ret}, nil, 0)
	root := newParentNodeInArena(arena, 1, true, []*Node{stmt}, nil, 0)

	normalizePerlReturnExpressionLists(root, lang)

	rewritten := stmt.Child(0)
	if rewritten == nil {
		t.Fatal("expression_statement lost child after normalization")
	}
	if got := rewritten.Type(lang); got != "list_expression" {
		t.Fatalf("rewritten child type = %q, want list_expression", got)
	}
	if got, want := rewritten.ChildCount(), 3; got != want {
		t.Fatalf("rewritten child count = %d, want %d", got, want)
	}
	ret = rewritten.Child(0)
	if ret == nil || ret.Type(lang) != "return_expression" {
		t.Fatalf("rewritten first child = %v, want return_expression", ret)
	}
	if got, want := ret.ChildCount(), 2; got != want {
		t.Fatalf("rewritten return child count = %d, want %d", got, want)
	}
	if got := ret.Child(1).Type(lang); got != "ambiguous_function_call_expression" {
		t.Fatalf("rewritten return payload type = %q, want ambiguous_function_call_expression", got)
	}
	if got := rewritten.Child(2).Type(lang); got != "array_element_expression" {
		t.Fatalf("rewritten third child type = %q, want array_element_expression", got)
	}
}

func TestParserMultiDigitNumbers(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	tree := mustParse(t, parser, []byte("123+456"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}

	if root.ChildCount() != 3 {
		t.Fatalf("root child count = %d, want 3", root.ChildCount())
	}

	inner := root.Child(0)
	if inner.ChildCount() < 1 {
		t.Fatal("inner expression has no children")
	}
	if inner.Child(0).Text(tree.Source()) != "123" {
		t.Errorf("first NUMBER text = %q, want %q", inner.Child(0).Text(tree.Source()), "123")
	}
	if root.Child(2).Text(tree.Source()) != "456" {
		t.Errorf("second NUMBER text = %q, want %q", root.Child(2).Text(tree.Source()), "456")
	}
}

func TestHiddenTreeHasFieldIDsDeepChain(t *testing.T) {
	arena := newNodeArena(arenaClassFull)
	leaf := newLeafNodeInArena(arena, 1, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1})
	leaf.fieldIDs = []FieldID{1}

	root := leaf
	for i := 0; i < 4096; i++ {
		root = newParentNodeInArena(arena, 2, false, []*Node{root}, nil, 0)
	}

	if !hiddenTreeHasFieldIDs(root) {
		t.Fatal("hiddenTreeHasFieldIDs returned false, want true")
	}
}

func TestNodesFromGSSFiltersNilAndPreservesOrder(t *testing.T) {
	var scratch gssScratch
	n1 := NewLeafNode(1, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1})
	n2 := NewLeafNode(1, true, 2, 3, Point{Row: 0, Column: 2}, Point{Row: 0, Column: 3})

	var s gssStack
	s.push(1, nil, &scratch)
	s.push(2, n1, &scratch)
	s.push(3, nil, &scratch)
	s.push(4, n2, &scratch)

	nodes := nodesFromGSS(s)
	if len(nodes) != 2 {
		t.Fatalf("nodesFromGSS len = %d, want 2", len(nodes))
	}
	if nodes[0] != n1 || nodes[1] != n2 {
		t.Fatalf("nodesFromGSS order mismatch: got [%p %p], want [%p %p]", nodes[0], nodes[1], n1, n2)
	}
}

func TestBuildResultFromGLRWithGSSOnlyStack(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)
	source := []byte("1")
	arena := acquireNodeArena(arenaClassFull)

	leaf := newLeafNodeInArena(arena, 1, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1})
	leaf.parseState = 1
	expr := newParentNodeInArena(arena, 3, true, []*Node{leaf}, nil, 0)
	expr.parseState = 2

	var gScratch gssScratch
	gss := newGSSStack(lang.InitialState, &gScratch)
	gss.push(expr.parseState, expr, &gScratch)
	stack := glrStack{gss: gss}

	tree := parser.buildResultFromGLR([]glrStack{stack}, source, arena, nil, nil, nil)
	if tree == nil || tree.RootNode() == nil {
		t.Fatal("buildResultFromGLR returned nil tree/root")
	}
	if tree.RootNode() != expr {
		t.Fatal("expected GSS-only stack result to reuse the GSS node as root")
	}
	if got := tree.RootNode().Text(tree.Source()); got != "1" {
		t.Fatalf("root text = %q, want %q", got, "1")
	}
	tree.Release()
}

func TestBuildResultFromNodesUsesErrorRootForMultipleFragments(t *testing.T) {
	lang := &Language{
		SymbolNames:    []string{"number", "expression"},
		SymbolMetadata: []SymbolMetadata{{Visible: true, Named: true}, {Visible: true, Named: true}},
		Name:           "test",
	}
	parser := &Parser{language: lang, hasRootSymbol: true, rootSymbol: 1}
	arena := acquireNodeArena(arenaClassFull)
	source := []byte("12")

	left := newLeafNodeInArena(arena, 0, true, 0, 1, Point{}, Point{Column: 1})
	right := newLeafNodeInArena(arena, 0, true, 1, 2, Point{Column: 1}, Point{Column: 2})
	right.hasError = true

	tree := parser.buildResultFromNodes([]*Node{left, right}, source, arena, nil, nil, nil)
	if tree == nil || tree.RootNode() == nil {
		t.Fatal("buildResultFromNodes returned nil tree/root")
	}
	if got := tree.RootNode().Type(lang); got != "ERROR" {
		t.Fatalf("root type = %q, want %q", got, "ERROR")
	}
	if !tree.RootNode().HasError() {
		t.Fatal("expected recovered multi-fragment root to have HasError=true")
	}
	tree.Release()
}

func TestBuildResultFromNodesFlattensLeadingRootFragment(t *testing.T) {
	lang := &Language{
		SymbolNames:    []string{"number", "expression"},
		SymbolMetadata: []SymbolMetadata{{Visible: true, Named: true}, {Visible: true, Named: true}},
		Name:           "test",
	}
	parser := &Parser{language: lang, hasRootSymbol: true, rootSymbol: 1}
	arena := acquireNodeArena(arenaClassFull)
	source := []byte("123")

	left := newLeafNodeInArena(arena, 0, true, 0, 1, Point{}, Point{Column: 1})
	middle := newLeafNodeInArena(arena, 0, true, 1, 2, Point{Column: 1}, Point{Column: 2})
	right := newLeafNodeInArena(arena, 0, true, 2, 3, Point{Column: 2}, Point{Column: 3})
	right.hasError = true
	fragment := newParentNodeInArena(arena, 1, true, []*Node{left, middle}, nil, 0)
	fragment.hasError = true

	tree := parser.buildResultFromNodes([]*Node{fragment, right}, source, arena, nil, nil, nil)
	if tree == nil || tree.RootNode() == nil {
		t.Fatal("buildResultFromNodes returned nil tree/root")
	}
	root := tree.RootNode()
	if got := root.Type(lang); got != "ERROR" {
		t.Fatalf("root type = %q, want %q", got, "ERROR")
	}
	if got, want := root.ChildCount(), 3; got != want {
		t.Fatalf("root child count = %d, want %d", got, want)
	}
	if first := root.Child(0); first == nil || first == fragment {
		t.Fatalf("expected flattened first child, got %v", first)
	}
	tree.Release()
}

func TestBuildResultFromNodesKeepsExpectedRootForValidMultipleFragments(t *testing.T) {
	lang := &Language{
		SymbolNames:    []string{"number", "expression"},
		SymbolMetadata: []SymbolMetadata{{Visible: true, Named: true}, {Visible: true, Named: true}},
		Name:           "test",
	}
	parser := &Parser{language: lang, hasRootSymbol: true, rootSymbol: 1}
	arena := acquireNodeArena(arenaClassFull)
	source := []byte("12")

	left := newLeafNodeInArena(arena, 0, true, 0, 1, Point{}, Point{Column: 1})
	right := newLeafNodeInArena(arena, 0, true, 1, 2, Point{Column: 1}, Point{Column: 2})

	tree := parser.buildResultFromNodes([]*Node{left, right}, source, arena, nil, nil, nil)
	if tree == nil || tree.RootNode() == nil {
		t.Fatal("buildResultFromNodes returned nil tree/root")
	}
	root := tree.RootNode()
	if got := root.Type(lang); got != "expression" {
		t.Fatalf("root type = %q, want %q", got, "expression")
	}
	if root.HasError() {
		t.Fatal("expected valid multi-fragment root to stay error-free")
	}
	tree.Release()
}

func TestBuildResultFromNodesKeepsDartProgramRootWhenOnlyChildNodesHaveErrors(t *testing.T) {
	lang := &Language{
		Name:        "dart",
		SymbolNames: []string{"library_name", "class_definition", "program"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "library_name", Visible: true, Named: true},
			{Name: "class_definition", Visible: true, Named: true},
			{Name: "program", Visible: true, Named: true},
		},
	}
	parser := &Parser{language: lang, hasRootSymbol: true, rootSymbol: 2}
	arena := acquireNodeArena(arenaClassFull)
	source := []byte("library;\nclass A {}\n")

	library := newLeafNodeInArena(arena, 0, true, 0, 8, Point{}, Point{Column: 8})
	library.hasError = true
	classDef := newLeafNodeInArena(arena, 1, true, 9, 19, Point{Row: 1}, Point{Row: 1, Column: 10})

	tree := parser.buildResultFromNodes([]*Node{library, classDef}, source, arena, nil, nil, nil)
	if tree == nil || tree.RootNode() == nil {
		t.Fatal("buildResultFromNodes returned nil tree/root")
	}
	root := tree.RootNode()
	if got := root.Type(lang); got != "program" {
		t.Fatalf("root type = %q, want %q", got, "program")
	}
	if !root.HasError() {
		t.Fatal("expected program root to retain HasError=true when a child has error")
	}
	tree.Release()
}

func TestCompactAcceptedStacksPreservesAllAcceptedForFinalChoice(t *testing.T) {
	lang := buildAmbiguousLanguage()
	parser := NewParser(lang)
	source := []byte("x")
	arena := acquireNodeArena(arenaClassFull)

	low := newLeafNodeInArena(arena, 2, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1})
	low.parseState = 2
	high := newLeafNodeInArena(arena, 3, true, 0, 1, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 1})
	high.parseState = 2

	stacks := []glrStack{
		{accepted: false, score: 99, entries: []stackEntry{{state: 1}}},
		{accepted: true, score: 0, entries: []stackEntry{{state: 2, node: low}}},
		{accepted: true, score: 5, entries: []stackEntry{{state: 2, node: high}}},
	}

	accepted := compactAcceptedStacks(stacks)
	if got, want := len(accepted), 2; got != want {
		t.Fatalf("len(accepted) = %d, want %d", got, want)
	}
	if !accepted[0].accepted || !accepted[1].accepted {
		t.Fatal("expected only accepted stacks after compaction")
	}
	if accepted[0].score != 0 || accepted[1].score != 5 {
		t.Fatalf("accepted scores = [%d %d], want [0 5]", accepted[0].score, accepted[1].score)
	}

	tree := parser.buildResultFromGLR(accepted, source, arena, nil, nil, nil)
	if tree == nil || tree.RootNode() == nil {
		t.Fatal("buildResultFromGLR returned nil tree/root")
	}
	if got, want := tree.RootNode().Symbol(), Symbol(3); got != want {
		t.Fatalf("root symbol = %d, want %d", got, want)
	}
	tree.Release()
}

func TestParserLongChain(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	// "1+2+3+4+5" — deeply left-nested.
	tree := mustParse(t, parser, []byte("1+2+3+4+5"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}

	// The rightmost child should be NUMBER "5".
	if root.ChildCount() != 3 {
		t.Fatalf("root child count = %d, want 3", root.ChildCount())
	}
	if root.Child(2).Text(tree.Source()) != "5" {
		t.Errorf("rightmost NUMBER text = %q, want %q", root.Child(2).Text(tree.Source()), "5")
	}

	// Walk down the left spine and count depth.
	depth := 0
	node := root
	for node.ChildCount() == 3 {
		node = node.Child(0)
		depth++
	}
	// "1+2+3+4+5" has 4 additions, so 4 levels of nesting.
	if depth != 4 {
		t.Errorf("left-nesting depth = %d, want 4", depth)
	}

	// The innermost expression should have 1 child (NUMBER "1").
	if node.ChildCount() != 1 {
		t.Errorf("innermost expression child count = %d, want 1", node.ChildCount())
	}
	if node.Child(0).Text(tree.Source()) != "1" {
		t.Errorf("innermost NUMBER text = %q, want %q", node.Child(0).Text(tree.Source()), "1")
	}
}

func TestParserByteSpans(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	tree := mustParse(t, parser, []byte("1+2"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}

	// Root expression should span the entire input [0, 3).
	if root.StartByte() != 0 {
		t.Errorf("root StartByte = %d, want 0", root.StartByte())
	}
	if root.EndByte() != 3 {
		t.Errorf("root EndByte = %d, want 3", root.EndByte())
	}

	// PLUS token at byte 1.
	plus := root.Child(1)
	if plus.StartByte() != 1 || plus.EndByte() != 2 {
		t.Errorf("PLUS bytes = [%d,%d), want [1,2)", plus.StartByte(), plus.EndByte())
	}
}

func TestParserPointPositions(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	tree := mustParse(t, parser, []byte("1+2"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}

	// Check start/end points of the root.
	if root.StartPoint() != (Point{Row: 0, Column: 0}) {
		t.Errorf("root StartPoint = %v, want {0,0}", root.StartPoint())
	}
	if root.EndPoint() != (Point{Row: 0, Column: 3}) {
		t.Errorf("root EndPoint = %v, want {0,3}", root.EndPoint())
	}

	// NUMBER "2" starts at column 2.
	num2 := root.Child(2)
	if num2.StartPoint() != (Point{Row: 0, Column: 2}) {
		t.Errorf("NUMBER '2' StartPoint = %v, want {0,2}", num2.StartPoint())
	}
}

func TestParserParentPointers(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	tree := mustParse(t, parser, []byte("1+2"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}

	// Root has no parent.
	// (NewParentNode does not set the parent of the root itself.)

	// Each child should have the root as parent.
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Parent() != root {
			t.Errorf("child %d parent != root", i)
		}
	}

	// The inner expression's child should point to the inner expression.
	inner := root.Child(0)
	if inner.ChildCount() > 0 {
		if inner.Child(0).Parent() != inner {
			t.Error("inner expression's child has wrong parent")
		}
	}
}

func TestParserTreeMetadata(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	source := []byte("1+2")
	tree := mustParse(t, parser, source)

	if tree.Language() != lang {
		t.Error("tree.Language() does not match")
	}
	if string(tree.Source()) != "1+2" {
		t.Errorf("tree.Source() = %q, want %q", tree.Source(), "1+2")
	}
}

func TestParserNamedChildAccess(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	tree := mustParse(t, parser, []byte("1+2"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}

	// Root has 3 children: expression (named), PLUS (anonymous), NUMBER (named).
	// So NamedChildCount should be 2.
	if root.NamedChildCount() != 2 {
		t.Errorf("root NamedChildCount = %d, want 2", root.NamedChildCount())
	}

	// NamedChild(0) should be the expression.
	nc0 := root.NamedChild(0)
	if nc0 == nil || nc0.Symbol() != 3 {
		t.Errorf("NamedChild(0) symbol = %v, want 3 (expression)", nc0)
	}

	// NamedChild(1) should be the NUMBER "2".
	nc1 := root.NamedChild(1)
	if nc1 == nil || nc1.Symbol() != 1 {
		t.Errorf("NamedChild(1) symbol = %v, want 1 (NUMBER)", nc1)
	}
}

func TestParserLookupActionOutOfRange(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	// State out of range.
	action := parser.lookupAction(StateID(999), Symbol(0))
	if action != nil {
		t.Error("expected nil for out-of-range state")
	}

	// Symbol out of range.
	action = parser.lookupAction(StateID(0), Symbol(999))
	if action != nil {
		t.Error("expected nil for out-of-range symbol")
	}
}

func TestParserIsNamedSymbol(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	// EOF (0) is not named.
	if parser.isNamedSymbol(Symbol(0)) {
		t.Error("EOF should not be named")
	}
	// NUMBER (1) is named.
	if !parser.isNamedSymbol(Symbol(1)) {
		t.Error("NUMBER should be named")
	}
	// PLUS (2) is not named.
	if parser.isNamedSymbol(Symbol(2)) {
		t.Error("PLUS should not be named")
	}
	// expression (3) is named.
	if !parser.isNamedSymbol(Symbol(3)) {
		t.Error("expression should be named")
	}
	// Out of range symbol.
	if parser.isNamedSymbol(Symbol(999)) {
		t.Error("out-of-range symbol should not be named")
	}
}

func TestParserOnlyWhitespace(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	// Only whitespace — should produce empty tree like empty input.
	tree := mustParse(t, parser, []byte("   "))
	root := tree.RootNode()
	if root != nil {
		t.Errorf("expected nil root for whitespace-only input, got symbol %d", root.Symbol())
	}
}

type hashPlusExternalScanner struct{}

func (s *hashPlusExternalScanner) Create() any                           { return nil }
func (s *hashPlusExternalScanner) Destroy(payload any)                   {}
func (s *hashPlusExternalScanner) Serialize(payload any, buf []byte) int { return 0 }
func (s *hashPlusExternalScanner) Deserialize(payload any, buf []byte)   {}
func (s *hashPlusExternalScanner) Scan(payload any, lexer *ExternalLexer, valid []bool) bool {
	if len(valid) == 0 || !valid[0] {
		return false
	}
	if lexer.Lookahead() != '#' {
		return false
	}
	lexer.Advance(false)
	lexer.MarkEnd()
	lexer.SetResultSymbol(Symbol(2)) // PLUS
	return true
}

func TestParserExternalScannerToken(t *testing.T) {
	lang := buildArithmeticLanguage()
	lang.ExternalScanner = &hashPlusExternalScanner{}
	lang.ExternalSymbols = []Symbol{2} // PLUS token comes from external scanner

	parser := NewParser(lang)
	tree := mustParse(t, parser, []byte("1#2"))
	root := tree.RootNode()
	if root == nil {
		t.Fatal("tree has nil root")
	}
	if root.HasError() {
		t.Fatal("external scanner token path produced error tree")
	}
	if root.ChildCount() != 3 {
		t.Fatalf("root child count = %d, want 3", root.ChildCount())
	}
	if got := root.Child(1).Text(tree.Source()); got != "#" {
		t.Fatalf("operator text = %q, want %q", got, "#")
	}
}

// TestFieldIDsAlignAfterExtrasFold verifies that when buildResult folds
// extra nodes (e.g. leading comments) into a root's children, the fieldIDs
// slice is padded to maintain index alignment with children.
//
// Regression test for: prepending extras into realRoot.children without
// updating fieldIDs caused ChildByFieldName to return wrong nodes.
func TestFieldIDsAlignAfterExtrasFold(t *testing.T) {
	lang := queryTestLanguage()

	// Construct a parent with fielded children:
	//   children:  [ident,        paramList,       block]
	//   fieldIDs:  [name(1),      parameters(5),   body(2)]
	ident := NewLeafNode(Symbol(1), true, 5, 9, Point{}, Point{})
	paramList := NewLeafNode(Symbol(13), true, 9, 11, Point{}, Point{})
	block := NewLeafNode(Symbol(14), true, 12, 20, Point{}, Point{})
	root := NewParentNode(Symbol(5), true,
		[]*Node{ident, paramList, block},
		[]FieldID{1, 5, 2}, 0)

	// Sanity: field lookups work before modification.
	if got := root.ChildByFieldName("name", lang); got != ident {
		t.Fatal("pre-check: name field should return ident")
	}
	if got := root.ChildByFieldName("body", lang); got != block {
		t.Fatal("pre-check: body field should return block")
	}

	// Simulate what buildResult's extras fold does: prepend a leading extra.
	extra := NewLeafNode(Symbol(0), false, 0, 3, Point{}, Point{})
	extra.isExtra = true

	leadingCount := 1
	merged := make([]*Node, 0, 1+len(root.children))
	merged = append(merged, extra)
	merged = append(merged, root.children...)
	root.children = merged

	// Pad fieldIDs to match: extras get 0.
	if len(root.fieldIDs) > 0 {
		padded := make([]FieldID, leadingCount+len(root.fieldIDs))
		copy(padded[leadingCount:], root.fieldIDs)
		root.fieldIDs = padded
	}

	// Verify field lookups still return correct nodes.
	if got := root.ChildByFieldName("name", lang); got != ident {
		t.Fatalf("after fold: name field should return ident (sym 1), got sym %d", got.Symbol())
	}
	if got := root.ChildByFieldName("body", lang); got != block {
		t.Fatalf("after fold: body field should return block (sym 14), got sym %d", got.Symbol())
	}
	if got := root.ChildByFieldName("parameters", lang); got != paramList {
		t.Fatalf("after fold: parameters field should return paramList (sym 13), got sym %d", got.Symbol())
	}
}

func TestParserIncrementalArithmeticEditMatchesFreshParse(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	oldSrc := []byte("1+2")
	oldTree := mustParse(t, parser, oldSrc)

	newSrc := []byte("1+3")
	edit := InputEdit{
		StartByte:   2,
		OldEndByte:  3,
		NewEndByte:  3,
		StartPoint:  Point{Row: 0, Column: 2},
		OldEndPoint: Point{Row: 0, Column: 3},
		NewEndPoint: Point{Row: 0, Column: 3},
	}
	oldTree.Edit(edit)

	incrTree := mustParseIncremental(t, parser, newSrc, oldTree)
	freshTree := mustParse(t, parser, newSrc)

	incrRoot := incrTree.RootNode()
	freshRoot := freshTree.RootNode()
	if incrRoot == nil || freshRoot == nil {
		t.Fatal("expected non-nil roots")
	}
	if got, want := incrRoot.SExpr(lang), freshRoot.SExpr(lang); got != want {
		t.Fatalf("incremental SExpr mismatch:\n  got:  %s\n  want: %s", got, want)
	}
	if incrRoot.HasError() != freshRoot.HasError() {
		t.Fatalf("incremental HasError=%v, fresh HasError=%v", incrRoot.HasError(), freshRoot.HasError())
	}
}

func TestParserIncrementalArithmeticEditThenUndoMatchesFreshParse(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	originalSrc := []byte("1+2")
	tree := mustParse(t, parser, originalSrc)

	editedSrc := []byte("1+9")
	forwardEdit := InputEdit{
		StartByte:   2,
		OldEndByte:  3,
		NewEndByte:  3,
		StartPoint:  Point{Row: 0, Column: 2},
		OldEndPoint: Point{Row: 0, Column: 3},
		NewEndPoint: Point{Row: 0, Column: 3},
	}
	tree.Edit(forwardEdit)
	tree = mustParseIncremental(t, parser, editedSrc, tree)

	undoEdit := InputEdit{
		StartByte:   2,
		OldEndByte:  3,
		NewEndByte:  3,
		StartPoint:  Point{Row: 0, Column: 2},
		OldEndPoint: Point{Row: 0, Column: 3},
		NewEndPoint: Point{Row: 0, Column: 3},
	}
	tree.Edit(undoEdit)
	incrUndo := mustParseIncremental(t, parser, originalSrc, tree)
	freshUndo := mustParse(t, parser, originalSrc)

	incrRoot := incrUndo.RootNode()
	freshRoot := freshUndo.RootNode()
	if incrRoot == nil || freshRoot == nil {
		t.Fatal("expected non-nil roots")
	}
	if got, want := incrRoot.SExpr(lang), freshRoot.SExpr(lang); got != want {
		t.Fatalf("incremental undo SExpr mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestParseRuntimeReportsAcceptedOnCompleteParse(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	tree := mustParse(t, parser, []byte("1+2"))
	rt := tree.ParseRuntime()

	if rt.StopReason != ParseStopAccepted {
		t.Fatalf("StopReason = %q, want %q", rt.StopReason, ParseStopAccepted)
	}
	if tree.ParseStoppedEarly() {
		t.Fatal("ParseStoppedEarly() = true, want false")
	}
	if rt.TokenSourceEOFEarly {
		t.Fatal("TokenSourceEOFEarly = true, want false")
	}
	if rt.Truncated {
		t.Fatal("Truncated = true, want false")
	}
	if rt.IterationLimit <= 0 {
		t.Fatalf("IterationLimit = %d, want > 0", rt.IterationLimit)
	}
	if rt.StackDepthLimit <= 0 {
		t.Fatalf("StackDepthLimit = %d, want > 0", rt.StackDepthLimit)
	}
	if rt.NodeLimit <= 0 {
		t.Fatalf("NodeLimit = %d, want > 0", rt.NodeLimit)
	}
	if rt.Iterations <= 0 {
		t.Fatalf("Iterations = %d, want > 0", rt.Iterations)
	}
}

type eofAtZeroTokenSource struct{}

func (eofAtZeroTokenSource) Next() Token {
	return Token{
		Symbol:    0,
		StartByte: 0,
		EndByte:   0,
	}
}

type slowArithmeticTokenSource struct {
	delay  time.Duration
	tokens []Token
	idx    int
}

func (s *slowArithmeticTokenSource) Next() Token {
	time.Sleep(s.delay)
	if s.idx >= len(s.tokens) {
		return Token{Symbol: 0}
	}
	tok := s.tokens[s.idx]
	s.idx++
	return tok
}

func TestParseRuntimeReportsTokenSourceEOFEarly(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)
	src := []byte("1+2")

	tree, err := parser.ParseWithTokenSource(src, eofAtZeroTokenSource{})
	if err != nil {
		t.Fatalf("ParseWithTokenSource() error = %v", err)
	}
	rt := tree.ParseRuntime()

	if rt.StopReason != ParseStopTokenSourceEOF {
		t.Fatalf("StopReason = %q, want %q", rt.StopReason, ParseStopTokenSourceEOF)
	}
	if !rt.TokenSourceEOFEarly {
		t.Fatal("TokenSourceEOFEarly = false, want true")
	}
	if rt.LastTokenEndByte != 0 {
		t.Fatalf("LastTokenEndByte = %d, want 0", rt.LastTokenEndByte)
	}
	if !tree.ParseStoppedEarly() {
		t.Fatal("ParseStoppedEarly() = false, want true")
	}
}

func TestParserCancellationFlagStopsParse(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	var cancelled uint32 = 1
	parser.SetCancellationFlag(&cancelled)
	if got := parser.CancellationFlag(); got != &cancelled {
		t.Fatalf("CancellationFlag() = %p, want %p", got, &cancelled)
	}

	tree := mustParse(t, parser, []byte("1+2"))
	if got, want := tree.ParseStopReason(), ParseStopCancelled; got != want {
		t.Fatalf("ParseStopReason() = %q, want %q", got, want)
	}
	if !tree.ParseStoppedEarly() {
		t.Fatal("ParseStoppedEarly() = false, want true")
	}
}

func TestParserTimeoutMicrosStopsParse(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)
	parser.SetTimeoutMicros(200)
	if got := parser.TimeoutMicros(); got != 200 {
		t.Fatalf("TimeoutMicros() = %d, want 200", got)
	}

	ts := &slowArithmeticTokenSource{
		delay: 2 * time.Millisecond,
		tokens: []Token{
			{Symbol: 1, StartByte: 0, EndByte: 1},
			{Symbol: 0, StartByte: 1, EndByte: 1},
		},
	}
	tree, err := parser.ParseWithTokenSource([]byte("1"), ts)
	if err != nil {
		t.Fatalf("ParseWithTokenSource() error = %v", err)
	}
	if got, want := tree.ParseStopReason(), ParseStopTimeout; got != want {
		t.Fatalf("ParseStopReason() = %q, want %q", got, want)
	}
	if !tree.ParseStoppedEarly() {
		t.Fatal("ParseStoppedEarly() = false, want true")
	}
}

func TestParserLoggerReceivesEvents(t *testing.T) {
	lang := buildArithmeticLanguage()
	parser := NewParser(lang)

	var parseEvents int
	var lexEvents int
	parser.SetLogger(func(kind ParserLogType, msg string) {
		if msg == "" {
			t.Fatal("logger message should not be empty")
		}
		switch kind {
		case ParserLogParse:
			parseEvents++
		case ParserLogLex:
			lexEvents++
		}
	})

	if _, err := parser.Parse([]byte("1+2")); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parseEvents == 0 {
		t.Fatal("expected at least one parse log event")
	}
	if lexEvents == 0 {
		t.Fatal("expected at least one lex log event")
	}

	// Nil logger disables logging.
	parser.SetLogger(nil)
	parseEvents = 0
	lexEvents = 0
	if _, err := parser.Parse([]byte("1+2")); err != nil {
		t.Fatalf("Parse() with nil logger error = %v", err)
	}
	if parseEvents != 0 || lexEvents != 0 {
		t.Fatalf("expected no events with nil logger, got parse=%d lex=%d", parseEvents, lexEvents)
	}
}

// buildReservedWordLanguage constructs a minimal language to test reserved word
// handling in promoteKeyword. Symbols:
//
//	0: EOF
//	1: IDENT (terminal, named) — keyword capture token
//	2: KW_IF (terminal, anonymous) — keyword matched by DFA
//	3: stmt (nonterminal, named)
//
// The keyword lexer DFA recognises "if" and emits symbol 2 (KW_IF).
//
// LexModes:
//
//	state 0: no lex mode entry (unused)
//	state 1: ReservedWordSetID=1 → set {KW_IF} → "if" is reserved, not promoted
//	state 2: ReservedWordSetID=0 → no reserved words → "if" IS promoted
//
// ReservedWords layout (stride 2):
//
//	set 0 (offset 0): [0, 0]       — empty
//	set 1 (offset 2): [KW_IF, 0]   — KW_IF is reserved
func buildReservedWordLanguage() *Language {
	return &Language{
		Name:                "reserved_word_test",
		SymbolCount:         4,
		TokenCount:          3,
		StateCount:          3,
		LargeStateCount:     3,
		KeywordCaptureToken: 1, // IDENT
		KeywordLexStates: []LexState{
			// State 0: start — dispatch 'i'
			{AcceptToken: 0, Default: -1, EOF: -1, Transitions: []LexTransition{
				{Lo: 'i', Hi: 'i', NextState: 1},
			}},
			// State 1: saw 'i' — dispatch 'f'
			{AcceptToken: 0, Default: -1, EOF: -1, Transitions: []LexTransition{
				{Lo: 'f', Hi: 'f', NextState: 2},
			}},
			// State 2: saw "if" — accept KW_IF (symbol 2)
			{AcceptToken: 2, Default: -1, EOF: -1},
		},
		LexModes: []LexMode{
			{LexState: 0},                       // state 0 — not used in test
			{LexState: 0, ReservedWordSetID: 1}, // state 1 — KW_IF reserved
			{LexState: 0, ReservedWordSetID: 0}, // state 2 — no reserved words
		},
		// Flat reserved word array, stride=2.
		// Set 0 (offset 0..1): empty [0, 0]
		// Set 1 (offset 2..3): [KW_IF(2), 0]
		ReservedWords:          []Symbol{0, 0, 2, 0},
		MaxReservedWordSetSize: 2,
		// Dense parse table — both IDENT and KW_IF valid in all states
		// so context-aware check doesn't interfere.
		// Columns: EOF(0), IDENT(1), KW_IF(2), stmt(3)
		ParseTable: [][]uint16{
			{0, 1, 1, 0}, // state 0
			{0, 1, 1, 0}, // state 1
			{0, 1, 1, 0}, // state 2
		},
		ParseActions: []ParseActionEntry{
			{Actions: nil},
			{Actions: []ParseAction{{Type: ParseActionShift, State: 1}}},
		},
	}
}

func TestReservedWordBlocksPromotion(t *testing.T) {
	lang := buildReservedWordLanguage()
	source := []byte("if")

	// Helper to build a dfaTokenSource with the given parse state and run
	// promoteKeyword on a token matching the keyword capture token.
	testPromote := func(state StateID) Token {
		lx := &Lexer{
			states: lang.LexStates,
			source: source,
		}
		d := &dfaTokenSource{
			lexer:    lx,
			language: lang,
			state:    state,
		}
		tok := Token{
			Symbol:    lang.KeywordCaptureToken, // IDENT
			StartByte: 0,
			EndByte:   2,
		}
		return d.promoteKeyword(tok)
	}

	// State 1 has ReservedWordSetID=1 which contains KW_IF (symbol 2).
	// "if" should NOT be promoted — token stays as IDENT (symbol 1).
	got := testPromote(1)
	if got.Symbol != 1 {
		t.Fatalf("state 1 (reserved): got symbol %d, want 1 (IDENT — not promoted)", got.Symbol)
	}

	// State 2 has ReservedWordSetID=0 — no reserved words.
	// "if" SHOULD be promoted to KW_IF (symbol 2).
	got = testPromote(2)
	if got.Symbol != 2 {
		t.Fatalf("state 2 (not reserved): got symbol %d, want 2 (KW_IF — promoted)", got.Symbol)
	}
}

func TestReservedWordNoReservedWordsArray(t *testing.T) {
	// When ReservedWords is empty, promotion should proceed normally.
	lang := buildReservedWordLanguage()
	lang.ReservedWords = nil
	lang.MaxReservedWordSetSize = 0
	source := []byte("if")

	lx := &Lexer{
		states: lang.LexStates,
		source: source,
	}
	d := &dfaTokenSource{
		lexer:    lx,
		language: lang,
		state:    1, // would be reserved if array were present
	}
	tok := Token{
		Symbol:    lang.KeywordCaptureToken,
		StartByte: 0,
		EndByte:   2,
	}
	got := d.promoteKeyword(tok)
	if got.Symbol != 2 {
		t.Fatalf("empty ReservedWords: got symbol %d, want 2 (KW_IF — promoted)", got.Symbol)
	}
}

func TestReservedWordSetIDZeroDoesNotBlock(t *testing.T) {
	// ReservedWordSetID=0 means no reserved words for this state,
	// even if the ReservedWords array is populated.
	lang := buildReservedWordLanguage()
	source := []byte("if")

	lx := &Lexer{
		states: lang.LexStates,
		source: source,
	}
	d := &dfaTokenSource{
		lexer:    lx,
		language: lang,
		state:    2, // ReservedWordSetID=0
	}
	tok := Token{
		Symbol:    lang.KeywordCaptureToken,
		StartByte: 0,
		EndByte:   2,
	}
	got := d.promoteKeyword(tok)
	if got.Symbol != 2 {
		t.Fatalf("setID=0: got symbol %d, want 2 (KW_IF — promoted)", got.Symbol)
	}
}

func TestNormalizeErlangSourceFileFormsSetsFormsOnlyAndSnapsBounds(t *testing.T) {
	lang := &Language{
		Name:        "erlang",
		FieldNames:  []string{"", "forms_only"},
		SymbolNames: []string{"EOF", "source_file", "comment", "fun_decl", "function_clause", "."},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "source_file", Visible: true, Named: true},
			{Name: "comment", Visible: true, Named: true},
			{Name: "fun_decl", Visible: true, Named: true},
			{Name: "function_clause", Visible: true, Named: true},
			{Name: ".", Visible: true, Named: false},
		},
	}

	arena := newNodeArena(arenaClassFull)
	comment := newLeafNodeInArena(arena, 2, true, 0, 10, Point{}, Point{Column: 10})
	comment.isExtra = true
	clause := newLeafNodeInArena(arena, 4, true, 12, 20, Point{Row: 1}, Point{Row: 1, Column: 8})
	innerComment := newLeafNodeInArena(arena, 2, true, 21, 30, Point{Row: 2}, Point{Row: 2, Column: 9})
	innerComment.isExtra = true
	dot := newLeafNodeInArena(arena, 5, false, 31, 32, Point{Row: 3}, Point{Row: 3, Column: 1})
	funDecl := newParentNodeInArena(arena, 3, true, []*Node{clause, innerComment, dot}, nil, 0)
	funDecl.startByte = 0
	funDecl.startPoint = Point{}
	root := newParentNodeInArena(arena, 1, true, []*Node{comment, funDecl}, nil, 0)

	normalizeErlangSourceFileForms(root, lang)

	if got, want := root.FieldNameForChild(1, lang), "forms_only"; got != want {
		t.Fatalf("root.FieldNameForChild(1) = %q, want %q", got, want)
	}
	if got, want := root.FieldNameForChild(0, lang), ""; got != want {
		t.Fatalf("root.FieldNameForChild(0) = %q, want empty", got)
	}
	if got, want := funDecl.startByte, clause.startByte; got != want {
		t.Fatalf("funDecl.startByte = %d, want %d", got, want)
	}
	if got, want := funDecl.endByte, dot.endByte; got != want {
		t.Fatalf("funDecl.endByte = %d, want %d", got, want)
	}
}

func TestNormalizeErlangSourceFileFormsSkipsExprsMode(t *testing.T) {
	lang := &Language{
		Name:        "erlang",
		FieldNames:  []string{"", "forms_only"},
		SymbolNames: []string{"EOF", "source_file", "comment", "atom"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "source_file", Visible: true, Named: true},
			{Name: "comment", Visible: true, Named: true},
			{Name: "atom", Visible: true, Named: true},
		},
	}

	arena := newNodeArena(arenaClassFull)
	comment := newLeafNodeInArena(arena, 2, true, 0, 2, Point{}, Point{Column: 2})
	comment.isExtra = true
	expr := newLeafNodeInArena(arena, 3, true, 3, 7, Point{Column: 3}, Point{Column: 7})
	root := newParentNodeInArena(arena, 1, true, []*Node{comment, expr}, nil, 0)

	normalizeErlangSourceFileForms(root, lang)

	if got := root.FieldNameForChild(1, lang); got != "" {
		t.Fatalf("root.FieldNameForChild(1) = %q, want empty", got)
	}
}
