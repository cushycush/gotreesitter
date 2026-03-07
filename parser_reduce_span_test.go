package gotreesitter

import "testing"

func TestExtendParentSpanCoversInvisibleLeafChild(t *testing.T) {
	// Invisible non-extra leaf child [20-22] dropped by buildReduceChildren
	// should extend parent endByte from 20 to 22 (contiguous).
	parent := NewParentNode(3, true, nil, nil, 0)
	parent.startByte = 10
	parent.endByte = 20
	parent.startPoint = Point{Row: 1, Column: 10}
	parent.endPoint = Point{Row: 1, Column: 20}

	leadingExtra := NewLeafNode(1, false, 8, 9, Point{Row: 1, Column: 8}, Point{Row: 1, Column: 9})
	leadingExtra.isExtra = true
	core := NewLeafNode(2, true, 10, 20, Point{Row: 1, Column: 10}, Point{Row: 1, Column: 20})
	invisible := NewLeafNode(4, false, 20, 22, Point{Row: 1, Column: 20}, Point{Row: 1, Column: 22})

	entries := []stackEntry{
		{state: 0, node: leadingExtra},
		{state: 0, node: core},
		{state: 0, node: invisible},
	}
	meta := []SymbolMetadata{
		{}, {}, {Visible: true}, {}, {Visible: false},
	}
	extendParentSpanToWindow(parent, entries, 0, len(entries), meta, nil)

	if got, want := parent.startByte, uint32(8); got != want {
		t.Fatalf("parent.startByte = %d, want %d", got, want)
	}
	if got, want := parent.endByte, uint32(22); got != want {
		t.Fatalf("parent.endByte = %d, want %d", got, want)
	}
}

func TestExtendParentSpanChainsInvisiblePrefixLeaves(t *testing.T) {
	parent := NewParentNode(5, true, nil, nil, 0)
	parent.startByte = 25
	parent.endByte = 30
	parent.startPoint = Point{Row: 1, Column: 25}
	parent.endPoint = Point{Row: 1, Column: 30}

	prefix1 := NewLeafNode(1, false, 10, 15, Point{Row: 1, Column: 10}, Point{Row: 1, Column: 15})
	prefix2 := NewLeafNode(2, false, 15, 20, Point{Row: 1, Column: 15}, Point{Row: 1, Column: 20})
	prefix3 := NewLeafNode(3, false, 20, 25, Point{Row: 1, Column: 20}, Point{Row: 1, Column: 25})
	core := NewLeafNode(4, true, 25, 30, Point{Row: 1, Column: 25}, Point{Row: 1, Column: 30})

	entries := []stackEntry{
		{state: 0, node: prefix1},
		{state: 0, node: prefix2},
		{state: 0, node: prefix3},
		{state: 0, node: core},
	}
	meta := []SymbolMetadata{
		{},
		{Visible: false},
		{Visible: false},
		{Visible: false},
		{Visible: true},
	}
	extendParentSpanToWindow(parent, entries, 0, len(entries), meta, nil)

	if got, want := parent.startByte, uint32(10); got != want {
		t.Fatalf("parent.startByte = %d, want %d", got, want)
	}
	if got, want := parent.endByte, uint32(30); got != want {
		t.Fatalf("parent.endByte = %d, want %d", got, want)
	}
}

func TestExtendParentSpanSkipsDiscontiguousPhantom(t *testing.T) {
	// A zero-width invisible entry AFTER the parent span (like javascript
	// _automatic_semicolon at [27-27] after statement_block [13-26])
	// must NOT extend the parent span.
	parent := NewParentNode(3, true, nil, nil, 0)
	parent.startByte = 13
	parent.endByte = 26
	parent.startPoint = Point{Row: 1, Column: 13}
	parent.endPoint = Point{Row: 1, Column: 26}

	core := NewLeafNode(2, true, 13, 26, Point{Row: 1, Column: 13}, Point{Row: 1, Column: 26})
	phantom := NewLeafNode(4, false, 27, 27, Point{Row: 1, Column: 27}, Point{Row: 1, Column: 27})

	entries := []stackEntry{
		{state: 0, node: core},
		{state: 0, node: phantom},
	}
	meta := []SymbolMetadata{
		{}, {}, {Visible: true}, {}, {Visible: false},
	}
	extendParentSpanToWindow(parent, entries, 0, len(entries), meta, []string{"", "", "visible", "", "_automatic_semicolon"})

	if got, want := parent.endByte, uint32(26); got != want {
		t.Fatalf("parent.endByte = %d, want %d (phantom should not extend)", got, want)
	}
}

func TestExtendParentSpanCoversInvisibleWithChildren(t *testing.T) {
	// An invisible node WITH children whose span exceeds its children's span
	// (due to nested invisible leaf extension) should still extend the parent.
	parent := NewParentNode(3, true, nil, nil, 0)
	parent.startByte = 5
	parent.endByte = 14
	parent.startPoint = Point{Row: 1, Column: 5}
	parent.endPoint = Point{Row: 1, Column: 14}

	invisibleWithKids := NewParentNode(4, false, []*Node{
		NewLeafNode(5, true, 5, 14, Point{Row: 1, Column: 5}, Point{Row: 1, Column: 14}),
	}, nil, 0)
	invisibleWithKids.startByte = 5
	invisibleWithKids.endByte = 15
	invisibleWithKids.startPoint = Point{Row: 1, Column: 5}
	invisibleWithKids.endPoint = Point{Row: 1, Column: 15}

	entries := []stackEntry{
		{state: 0, node: invisibleWithKids},
	}
	meta := []SymbolMetadata{
		{}, {}, {}, {}, {Visible: false}, {Visible: true},
	}
	extendParentSpanToWindow(parent, entries, 0, len(entries), meta, nil)

	if got, want := parent.endByte, uint32(15); got != want {
		t.Fatalf("parent.endByte = %d, want %d", got, want)
	}
}

func TestExtendParentSpanNoOp(t *testing.T) {
	parent := NewParentNode(3, true, nil, nil, 0)
	parent.startByte = 10
	parent.endByte = 20
	parent.startPoint = Point{Row: 2, Column: 10}
	parent.endPoint = Point{Row: 2, Column: 20}

	core := NewLeafNode(2, true, 10, 20, Point{Row: 2, Column: 10}, Point{Row: 2, Column: 20})
	entries := []stackEntry{{state: 0, node: core}}
	meta := []SymbolMetadata{{}, {}, {Visible: true}}
	extendParentSpanToWindow(parent, entries, 0, len(entries), meta, nil)

	if got, want := parent.startByte, uint32(10); got != want {
		t.Fatalf("parent.startByte = %d, want %d", got, want)
	}
	if got, want := parent.endByte, uint32(20); got != want {
		t.Fatalf("parent.endByte = %d, want %d", got, want)
	}
}

func TestExtendParentSpanAllowsImplicitEndTagGap(t *testing.T) {
	parent := NewParentNode(3, true, nil, nil, 0)
	parent.startByte = 10
	parent.endByte = 20
	parent.startPoint = Point{Row: 1, Column: 10}
	parent.endPoint = Point{Row: 1, Column: 20}

	core := NewLeafNode(2, true, 10, 20, Point{Row: 1, Column: 10}, Point{Row: 1, Column: 20})
	implicitEnd := NewLeafNode(4, false, 21, 21, Point{Row: 1, Column: 21}, Point{Row: 1, Column: 21})

	entries := []stackEntry{
		{state: 0, node: core},
		{state: 0, node: implicitEnd},
	}
	meta := []SymbolMetadata{
		{}, {}, {Visible: true}, {}, {Visible: false},
	}
	names := []string{"", "", "visible", "", "_implicit_end_tag"}
	extendParentSpanToWindow(parent, entries, 0, len(entries), meta, names)

	if got, want := parent.startByte, uint32(10); got != want {
		t.Fatalf("parent.startByte = %d, want %d", got, want)
	}
	if got, want := parent.endByte, uint32(21); got != want {
		t.Fatalf("parent.endByte = %d, want %d", got, want)
	}
}

func TestExtendParentSpanSkipsInvisibleLineEnding(t *testing.T) {
	parent := NewParentNode(3, true, nil, nil, 0)
	parent.startByte = 10
	parent.endByte = 20
	parent.startPoint = Point{Row: 1, Column: 10}
	parent.endPoint = Point{Row: 1, Column: 20}

	core := NewLeafNode(2, true, 10, 20, Point{Row: 1, Column: 10}, Point{Row: 1, Column: 20})
	lineEnd := NewLeafNode(4, false, 20, 21, Point{Row: 1, Column: 20}, Point{Row: 2, Column: 0})

	entries := []stackEntry{
		{state: 0, node: core},
		{state: 0, node: lineEnd},
	}
	meta := []SymbolMetadata{
		{}, {}, {Visible: true}, {}, {Visible: false},
	}
	names := []string{"", "", "visible", "", "_line_ending_or_eof"}
	extendParentSpanToWindow(parent, entries, 0, len(entries), meta, names)

	if got, want := parent.startByte, uint32(10); got != want {
		t.Fatalf("parent.startByte = %d, want %d", got, want)
	}
	if got, want := parent.endByte, uint32(20); got != want {
		t.Fatalf("parent.endByte = %d, want %d", got, want)
	}
}

func TestShouldUseRawSpanForInvisibleReduction(t *testing.T) {
	meta := []SymbolMetadata{
		{},
		{Visible: true},
		{Visible: false},
	}
	children := []*Node{
		NewLeafNode(1, true, 38, 45, Point{Row: 0, Column: 38}, Point{Row: 0, Column: 45}),
	}

	if !shouldUseRawSpanForReduction(2, children, meta, false, nil) {
		t.Fatalf("expected invisible reduction to preserve raw span")
	}
	if shouldUseRawSpanForReduction(1, children, meta, false, nil) {
		t.Fatalf("expected visible reduction with visible children to keep child-derived span")
	}
}

func TestComputeReduceRawSpanKeepsDroppedInvisiblePrefix(t *testing.T) {
	visibleTail := NewLeafNode(1, true, 38, 45, Point{Row: 0, Column: 38}, Point{Row: 0, Column: 45})
	invisibleReduced := NewParentNode(2, false, []*Node{visibleTail}, nil, 0)
	invisibleReduced.startByte = 16
	invisibleReduced.endByte = 45
	invisibleReduced.startPoint = Point{Row: 0, Column: 16}
	invisibleReduced.endPoint = Point{Row: 0, Column: 45}

	entries := []stackEntry{{state: 0, node: invisibleReduced}}
	span := computeReduceRawSpan(entries, 0, len(entries))
	if got, want := span.startByte, uint32(16); got != want {
		t.Fatalf("span.startByte = %d, want %d", got, want)
	}
	if got, want := span.endByte, uint32(45); got != want {
		t.Fatalf("span.endByte = %d, want %d", got, want)
	}
}
