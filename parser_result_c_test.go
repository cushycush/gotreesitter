package gotreesitter

import "testing"

func TestNormalizeCPointerAssignmentPrecedence(t *testing.T) {
	lang := &Language{
		Name:        "c",
		SymbolNames: []string{"EOF", "translation_unit", "assignment_expression", "pointer_expression", "*", "=", "|=", "identifier", "number_literal"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "translation_unit", Visible: true, Named: true},
			{Name: "assignment_expression", Visible: true, Named: true},
			{Name: "pointer_expression", Visible: true, Named: true},
			{Name: "*", Visible: true, Named: false},
			{Name: "=", Visible: true, Named: false},
			{Name: "|=", Visible: true, Named: false},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "number_literal", Visible: true, Named: true},
		},
		FieldNames: []string{"", "left", "operator", "right", "argument"},
	}

	arena := newNodeArena(arenaClassFull)
	opStar := newLeafNodeInArena(arena, 4, false, 0, 1, Point{}, Point{Column: 1})
	ident := newLeafNodeInArena(arena, 7, true, 1, 2, Point{Column: 1}, Point{Column: 2})
	opAssign := newLeafNodeInArena(arena, 5, false, 3, 4, Point{Column: 3}, Point{Column: 4})
	number := newLeafNodeInArena(arena, 8, true, 5, 6, Point{Column: 5}, Point{Column: 6})

	assign := newParentNodeInArena(arena, 2, true, []*Node{ident, opAssign, number}, nil, 0)
	ensureNodeFieldStorage(assign, 3)
	assign.fieldIDs[0] = 1
	assign.fieldIDs[1] = 2
	assign.fieldIDs[2] = 3
	assign.fieldSources[0] = fieldSourceDirect
	assign.fieldSources[1] = fieldSourceDirect
	assign.fieldSources[2] = fieldSourceDirect

	ptr := newParentNodeInArena(arena, 3, true, []*Node{opStar, assign}, nil, 0)
	ensureNodeFieldStorage(ptr, 2)
	ptr.fieldIDs[0] = 2
	ptr.fieldIDs[1] = 4
	ptr.fieldSources[0] = fieldSourceDirect
	ptr.fieldSources[1] = fieldSourceDirect

	root := newParentNodeInArena(arena, 1, true, []*Node{ptr}, nil, 0)

	normalizeCPointerAssignmentPrecedence(root, lang)

	got := root.Child(0)
	if got == nil {
		t.Fatal("root child is nil after normalization")
	}
	if got.Type(lang) != "assignment_expression" {
		t.Fatalf("root child type = %q, want assignment_expression", got.Type(lang))
	}
	if got.ChildCount() != 3 {
		t.Fatalf("assignment child count = %d, want 3", got.ChildCount())
	}
	if got.Child(1).Type(lang) != "=" {
		t.Fatalf("assignment operator = %q, want =", got.Child(1).Type(lang))
	}
	left := got.Child(0)
	if left == nil || left.Type(lang) != "pointer_expression" {
		t.Fatalf("assignment left type = %q, want pointer_expression", left.Type(lang))
	}
	if left.ChildCount() != 2 {
		t.Fatalf("pointer_expression child count = %d, want 2", left.ChildCount())
	}
	if left.Child(0).Type(lang) != "*" {
		t.Fatalf("pointer operator = %q, want *", left.Child(0).Type(lang))
	}
	if left.Child(1).Type(lang) != "identifier" {
		t.Fatalf("pointer argument = %q, want identifier", left.Child(1).Type(lang))
	}
	if got.Child(2).Type(lang) != "number_literal" {
		t.Fatalf("assignment right type = %q, want number_literal", got.Child(2).Type(lang))
	}
}

func TestNormalizeCPointerCompoundAssignmentPrecedence(t *testing.T) {
	lang := &Language{
		Name:        "c",
		SymbolNames: []string{"EOF", "translation_unit", "assignment_expression", "pointer_expression", "*", "=", "|=", "identifier", "number_literal"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "translation_unit", Visible: true, Named: true},
			{Name: "assignment_expression", Visible: true, Named: true},
			{Name: "pointer_expression", Visible: true, Named: true},
			{Name: "*", Visible: true, Named: false},
			{Name: "=", Visible: true, Named: false},
			{Name: "|=", Visible: true, Named: false},
			{Name: "identifier", Visible: true, Named: true},
			{Name: "number_literal", Visible: true, Named: true},
		},
		FieldNames: []string{"", "left", "operator", "right", "argument"},
	}

	arena := newNodeArena(arenaClassFull)
	opStar := newLeafNodeInArena(arena, 4, false, 0, 1, Point{}, Point{Column: 1})
	ident := newLeafNodeInArena(arena, 7, true, 1, 2, Point{Column: 1}, Point{Column: 2})
	opAssign := newLeafNodeInArena(arena, 6, false, 3, 5, Point{Column: 3}, Point{Column: 5})
	number := newLeafNodeInArena(arena, 8, true, 6, 7, Point{Column: 6}, Point{Column: 7})

	assign := newParentNodeInArena(arena, 2, true, []*Node{ident, opAssign, number}, nil, 0)
	ensureNodeFieldStorage(assign, 3)
	assign.fieldIDs[0] = 1
	assign.fieldIDs[1] = 2
	assign.fieldIDs[2] = 3
	assign.fieldSources[0] = fieldSourceDirect
	assign.fieldSources[1] = fieldSourceDirect
	assign.fieldSources[2] = fieldSourceDirect

	ptr := newParentNodeInArena(arena, 3, true, []*Node{opStar, assign}, nil, 0)
	ensureNodeFieldStorage(ptr, 2)
	ptr.fieldIDs[0] = 2
	ptr.fieldIDs[1] = 4
	ptr.fieldSources[0] = fieldSourceDirect
	ptr.fieldSources[1] = fieldSourceDirect

	root := newParentNodeInArena(arena, 1, true, []*Node{ptr}, nil, 0)

	normalizeCPointerAssignmentPrecedence(root, lang)

	got := root.Child(0)
	if got == nil {
		t.Fatal("root child is nil after normalization")
	}
	if got.Type(lang) != "assignment_expression" {
		t.Fatalf("root child type = %q, want assignment_expression", got.Type(lang))
	}
	if got.Child(1).Type(lang) != "|=" {
		t.Fatalf("assignment operator = %q, want |=", got.Child(1).Type(lang))
	}
	left := got.Child(0)
	if left == nil || left.Type(lang) != "pointer_expression" {
		t.Fatalf("assignment left type = %q, want pointer_expression", left.Type(lang))
	}
	if left.Child(1).Type(lang) != "identifier" {
		t.Fatalf("pointer argument = %q, want identifier", left.Child(1).Type(lang))
	}
	if got.Child(2).Type(lang) != "number_literal" {
		t.Fatalf("assignment right type = %q, want number_literal", got.Child(2).Type(lang))
	}
}
