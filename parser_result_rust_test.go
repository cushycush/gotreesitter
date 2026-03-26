package gotreesitter

import "testing"

func TestNormalizeRustTokenBindingPatterns(t *testing.T) {
	lang := &Language{
		Name: "rust",
		SymbolNames: []string{
			"",
			"source_file",
			"token_tree_pattern",
			"(",
			"identifier",
			"=>",
			"metavariable",
			":",
			")",
			"token_binding_pattern",
			"fragment_specifier",
		},
		SymbolMetadata: []SymbolMetadata{
			{},
			{Named: true},
			{Named: true},
			{Named: false},
			{Named: true},
			{Named: false},
			{Named: true},
			{Named: false},
			{Named: false},
			{Named: true},
			{Named: true},
		},
	}
	arena := acquireNodeArena(arenaClassFull)
	source := []byte("(x => $e:expr)")

	tokenTree := newParentNodeInArena(arena, 2, true, []*Node{
		newLeafNodeInArena(arena, 3, false, 0, 1, Point{}, Point{Column: 1}),
		newLeafNodeInArena(arena, 4, true, 1, 2, Point{Column: 1}, Point{Column: 2}),
		newLeafNodeInArena(arena, 5, false, 3, 5, Point{Column: 3}, Point{Column: 5}),
		newLeafNodeInArena(arena, 6, true, 6, 8, Point{Column: 6}, Point{Column: 8}),
		newLeafNodeInArena(arena, 7, false, 8, 9, Point{Column: 8}, Point{Column: 9}),
		newLeafNodeInArena(arena, 4, true, 9, 13, Point{Column: 9}, Point{Column: 13}),
		newLeafNodeInArena(arena, 8, false, 13, 14, Point{Column: 13}, Point{Column: 14}),
	}, nil, 0)
	root := newParentNodeInArena(arena, 1, true, []*Node{tokenTree}, nil, 0)

	normalizeRustTokenBindingPatterns(root, source, lang)

	pattern := root.Child(0)
	if pattern == nil || pattern.Type(lang) != "token_tree_pattern" {
		t.Fatalf("expected token_tree_pattern child, got %#v", pattern)
	}
	if got, want := pattern.ChildCount(), 5; got != want {
		t.Fatalf("token_tree_pattern child count = %d, want %d", got, want)
	}
	binding := pattern.Child(3)
	if binding == nil || binding.Type(lang) != "token_binding_pattern" {
		t.Fatalf("expected token_binding_pattern, got %s", pattern.SExpr(lang))
	}
	if got, want := binding.ChildCount(), 2; got != want {
		t.Fatalf("token_binding_pattern child count = %d, want %d", got, want)
	}
	if child := binding.Child(1); child == nil || child.Type(lang) != "fragment_specifier" {
		t.Fatalf("expected fragment_specifier child, got %s", binding.SExpr(lang))
	}
}
