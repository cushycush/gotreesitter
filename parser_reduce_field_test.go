package gotreesitter

import "testing"

func TestBuildReduceChildrenHiddenParentExtendsInheritedFieldToTrailingAnonymousChild(t *testing.T) {
	lang := &Language{
		FieldNames: []string{"", "condition"},
		SymbolMetadata: []SymbolMetadata{
			{},
			{Visible: true, Named: true},
			{Visible: true, Named: false},
			{Visible: false, Named: true},
			{Visible: true, Named: true},
		},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}
	parser := &Parser{language: lang}
	arena := newNodeArena(8)

	cond := NewLeafNode(Symbol(1), true, 0, 1, Point{}, Point{})
	semi := NewLeafNode(Symbol(2), false, 1, 2, Point{}, Point{})
	hidden := NewParentNode(Symbol(3), false, []*Node{cond, semi}, []FieldID{1, 0}, 0)

	children, fieldIDs := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 0, 1, arena)
	if got, want := len(children), 2; got != want {
		t.Fatalf("len(children) = %d, want %d", got, want)
	}
	if got, want := len(fieldIDs), 2; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got, want := fieldIDs[1], FieldID(1); got != want {
		t.Fatalf("fieldIDs[1] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenHiddenParentExtendsInheritedFieldToTrailingAnonymousAfterSingleNamed(t *testing.T) {
	lang := &Language{
		FieldNames: []string{"", "condition"},
		SymbolMetadata: []SymbolMetadata{
			{},
			{Visible: true, Named: true},
			{Visible: true, Named: false},
			{Visible: false, Named: true},
		},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}
	parser := &Parser{language: lang}
	arena := newNodeArena(8)

	cond := NewLeafNode(Symbol(1), true, 0, 1, Point{}, Point{})
	semi := NewLeafNode(Symbol(2), false, 1, 2, Point{}, Point{})
	hidden := NewParentNode(Symbol(3), false, []*Node{cond, semi}, nil, 0)

	_, fieldIDs := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 0, 1, arena)
	if got, want := len(fieldIDs), 2; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got, want := fieldIDs[1], FieldID(1); got != want {
		t.Fatalf("fieldIDs[1] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenHiddenParentDoesNotExtendInheritedFieldAcrossNamedSibling(t *testing.T) {
	lang := &Language{
		FieldNames: []string{"", "body"},
		SymbolMetadata: []SymbolMetadata{
			{},
			{Visible: true, Named: true},
			{Visible: true, Named: false},
			{Visible: false, Named: true},
		},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}
	parser := &Parser{language: lang}
	arena := newNodeArena(8)

	left := NewLeafNode(Symbol(1), true, 0, 1, Point{}, Point{})
	op := NewLeafNode(Symbol(2), false, 1, 2, Point{}, Point{})
	right := NewLeafNode(Symbol(4), true, 2, 3, Point{}, Point{})
	hidden := NewParentNode(Symbol(3), false, []*Node{left, op, right}, []FieldID{1, 0, 0}, 0)

	children, fieldIDs := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 0, 1, arena)
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
	_ = children
}

func TestBuildReduceChildrenHiddenParentExtendsInheritedFieldAcrossMixedNamedRepeat(t *testing.T) {
	lang := &Language{
		FieldNames: []string{"", "value"},
		SymbolMetadata: []SymbolMetadata{
			{},
			{Visible: true, Named: true},
			{Visible: false, Named: true},
			{Visible: true, Named: true},
			{Visible: true, Named: false},
		},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}
	parser := &Parser{language: lang}
	arena := newNodeArena(8)

	first := NewLeafNode(Symbol(1), true, 0, 1, Point{}, Point{})
	second := NewLeafNode(Symbol(3), true, 1, 2, Point{}, Point{})
	semi := NewLeafNode(Symbol(4), false, 2, 3, Point{}, Point{})
	hidden := NewParentNode(Symbol(2), false, []*Node{first, second, semi}, nil, 0)

	_, fieldIDs := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 0, 1, arena)
	if got, want := len(fieldIDs), 3; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got, want := fieldIDs[1], FieldID(1); got != want {
		t.Fatalf("fieldIDs[1] = %d, want %d", got, want)
	}
	if got := fieldIDs[2]; got != 0 {
		t.Fatalf("fieldIDs[2] = %d, want 0", got)
	}
}

func TestBuildReduceChildrenHiddenParentExtendsRedirectFieldOntoStructuredNamedChildren(t *testing.T) {
	lang := &Language{
		FieldNames: []string{"", "redirect", "destination"},
		SymbolMetadata: []SymbolMetadata{
			{},
			{Visible: true, Named: true},
			{Visible: false, Named: true},
			{Visible: true, Named: true},
		},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}
	parser := &Parser{language: lang}
	arena := newNodeArena(8)

	first := NewParentNode(Symbol(1), true, []*Node{NewLeafNode(Symbol(1), true, 0, 1, Point{}, Point{})}, []FieldID{2}, 0)
	second := NewParentNode(Symbol(3), true, []*Node{NewLeafNode(Symbol(1), true, 1, 2, Point{}, Point{})}, []FieldID{2}, 0)
	hidden := NewParentNode(Symbol(2), false, []*Node{first, second}, nil, 0)

	_, fieldIDs := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 0, 1, arena)
	if got, want := len(fieldIDs), 2; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got, want := fieldIDs[0], FieldID(1); got != want {
		t.Fatalf("fieldIDs[0] = %d, want %d", got, want)
	}
	if got, want := fieldIDs[1], FieldID(1); got != want {
		t.Fatalf("fieldIDs[1] = %d, want %d", got, want)
	}
}

func TestBuildReduceChildrenHiddenParentDoesNotExtendMixedRepeatOperatorField(t *testing.T) {
	lang := &Language{
		FieldNames: []string{"", "operator"},
		SymbolMetadata: []SymbolMetadata{
			{},
			{Visible: true, Named: true},
			{Visible: false, Named: true},
			{Visible: true, Named: true},
		},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}
	parser := &Parser{language: lang}
	arena := newNodeArena(8)

	first := NewLeafNode(Symbol(1), true, 0, 1, Point{}, Point{})
	second := NewParentNode(Symbol(3), true, []*Node{NewLeafNode(Symbol(1), true, 1, 2, Point{}, Point{})}, []FieldID{1}, 0)
	hidden := NewParentNode(Symbol(2), false, []*Node{first, second}, nil, 0)

	_, fieldIDs := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 0, 1, arena)
	if got, want := len(fieldIDs), 2; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got := fieldIDs[0]; got != 0 {
		t.Fatalf("fieldIDs[0] = %d, want 0", got)
	}
	if got := fieldIDs[1]; got != 0 {
		t.Fatalf("fieldIDs[1] = %d, want 0", got)
	}
}

func TestBuildReduceChildrenHiddenParentDoesNotLiftSingleNamedOperatorField(t *testing.T) {
	lang := &Language{
		FieldNames: []string{"", "operator"},
		SymbolMetadata: []SymbolMetadata{
			{},
			{Visible: true, Named: true},
			{Visible: false, Named: true},
		},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}
	parser := &Parser{language: lang}
	arena := newNodeArena(8)

	child := NewParentNode(Symbol(1), true, []*Node{NewLeafNode(Symbol(1), true, 0, 1, Point{}, Point{})}, []FieldID{1}, 0)
	hidden := NewParentNode(Symbol(2), false, []*Node{child}, nil, 0)

	_, fieldIDs := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 0, 1, arena)
	if got, want := len(fieldIDs), 1; got != want {
		t.Fatalf("len(fieldIDs) = %d, want %d", got, want)
	}
	if got := fieldIDs[0]; got != 0 {
		t.Fatalf("fieldIDs[0] = %d, want 0", got)
	}
}

func TestBuildReduceChildrenHiddenParentDoesNotExtendScopeFieldToTrailingAnonymous(t *testing.T) {
	lang := &Language{
		FieldNames: []string{"", "scope"},
		SymbolMetadata: []SymbolMetadata{
			{},
			{Visible: true, Named: true},
			{Visible: true, Named: false},
			{Visible: false, Named: true},
		},
		FieldMapSlices: [][2]uint16{
			{0, 1},
		},
		FieldMapEntries: []FieldMapEntry{
			{FieldID: 1, ChildIndex: 0, Inherited: true},
		},
	}
	parser := &Parser{language: lang}
	arena := newNodeArena(8)

	scope := NewLeafNode(Symbol(1), true, 0, 3, Point{}, Point{})
	sep := NewLeafNode(Symbol(2), false, 3, 5, Point{}, Point{})
	hidden := NewParentNode(Symbol(3), false, []*Node{scope, sep}, []FieldID{1, 0}, 0)

	_, fieldIDs := parser.buildReduceChildren([]stackEntry{{node: hidden}}, 0, 1, 1, 0, 1, arena)
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
