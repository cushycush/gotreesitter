package gotreesitter

import "testing"

func TestNormalizeKnownSpanAttributionExtendsWhitelistedRootChild(t *testing.T) {
	child := NewLeafNode(1, true, 0, 7, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 7})
	root := NewParentNode(2, true, []*Node{child}, nil, 0)
	root.endByte = 8
	root.endPoint = Point{Row: 1, Column: 0}

	normalizeKnownSpanAttribution(root, []byte("p hello\n"), &Language{
		Name:        "pug",
		SymbolNames: []string{"", "tag"},
	})

	if got, want := child.endByte, uint32(8); got != want {
		t.Fatalf("child endByte = %d, want %d", got, want)
	}
	if got, want := child.endPoint, (Point{Row: 1, Column: 0}); got != want {
		t.Fatalf("child endPoint = %+v, want %+v", got, want)
	}
}

func TestNormalizeKnownSpanAttributionLeavesOtherLanguagesAlone(t *testing.T) {
	child := NewLeafNode(1, true, 0, 8, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 8})
	root := NewParentNode(2, true, []*Node{child}, nil, 0)
	root.endByte = 9
	root.endPoint = Point{Row: 1, Column: 0}

	normalizeKnownSpanAttribution(root, []byte("{\"a\": 1}\n"), &Language{Name: "json"})

	if got, want := child.endByte, uint32(8); got != want {
		t.Fatalf("child endByte = %d, want %d", got, want)
	}
	if got, want := child.endPoint, (Point{Row: 0, Column: 8}); got != want {
		t.Fatalf("child endPoint = %+v, want %+v", got, want)
	}
}

func TestNormalizeKnownSpanAttributionExtendsWhitelistedChildToNextSibling(t *testing.T) {
	stmt := NewParentNode(1, true, []*Node{
		NewLeafNode(2, false, 0, 7, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 7}),
		NewLeafNode(3, true, 8, 13, Point{Row: 0, Column: 8}, Point{Row: 0, Column: 13}),
	}, nil, 0)
	root := NewParentNode(4, true, []*Node{
		stmt,
		NewLeafNode(5, true, 14, 18, Point{Row: 1, Column: 0}, Point{Row: 1, Column: 4}),
	}, nil, 0)
	root.endByte = 18
	root.endPoint = Point{Row: 1, Column: 4}

	normalizeKnownSpanAttribution(root, []byte("program hello\nbody"), &Language{
		Name:        "fortran",
		SymbolNames: []string{"", "program_statement"},
	})

	if got, want := stmt.endByte, uint32(14); got != want {
		t.Fatalf("stmt endByte = %d, want %d", got, want)
	}
	if got, want := stmt.endPoint, (Point{Row: 1, Column: 0}); got != want {
		t.Fatalf("stmt endPoint = %+v, want %+v", got, want)
	}
}

func TestNormalizeKnownSpanAttributionDoesNotExtendNonTargetedNestedNode(t *testing.T) {
	line := NewParentNode(1, true, []*Node{
		NewLeafNode(2, true, 11, 21, Point{Row: 1, Column: 2}, Point{Row: 1, Column: 12}),
	}, nil, 0)
	body := NewParentNode(2, true, []*Node{line}, nil, 0)
	body.startByte = 11
	body.endByte = 22
	body.startPoint = Point{Row: 1, Column: 2}
	body.endPoint = Point{Row: 2, Column: 0}
	root := NewParentNode(3, true, []*Node{body}, nil, 0)
	root.startByte = 11
	root.endByte = 22
	root.startPoint = Point{Row: 1, Column: 2}
	root.endPoint = Point{Row: 2, Column: 0}

	normalizeKnownSpanAttribution(root, []byte("default:\n  echo hello\n"), &Language{
		Name:        "just",
		SymbolNames: []string{"", "recipe_line", "recipe_body"},
	})

	if got, want := body.endByte, uint32(22); got != want {
		t.Fatalf("body endByte = %d, want %d", got, want)
	}
	if got, want := line.endByte, uint32(21); got != want {
		t.Fatalf("line endByte = %d, want %d", got, want)
	}
}

func TestNormalizeKnownSpanAttributionCooklangExtendsStepPunctuationAndRecipeNewline(t *testing.T) {
	step := NewLeafNode(1, true, 0, 16, Point{Row: 0, Column: 0}, Point{Row: 0, Column: 16})
	root := NewParentNode(2, true, []*Node{step}, nil, 0)
	root.endByte = 16
	root.endPoint = Point{Row: 0, Column: 16}

	normalizeKnownSpanAttribution(root, []byte("Add @salt{1%tsp}.\n"), &Language{
		Name:        "cooklang",
		SymbolNames: []string{"", "step", "recipe"},
	})

	if got, want := step.endByte, uint32(17); got != want {
		t.Fatalf("step endByte = %d, want %d", got, want)
	}
	if got, want := step.endPoint, (Point{Row: 0, Column: 17}); got != want {
		t.Fatalf("step endPoint = %+v, want %+v", got, want)
	}
	if got, want := root.endByte, uint32(18); got != want {
		t.Fatalf("root endByte = %d, want %d", got, want)
	}
	if got, want := root.endPoint, (Point{Row: 1, Column: 0}); got != want {
		t.Fatalf("root endPoint = %+v, want %+v", got, want)
	}
}

func TestNormalizeRootSourceStartLeavesCobolAreaAOffset(t *testing.T) {
	root := NewLeafNode(1, true, 7, 58, Point{Row: 0, Column: 7}, Point{Row: 2, Column: 0})
	parser := &Parser{language: &Language{Name: "cobol"}}

	parser.normalizeRootSourceStart(root, []byte("       IDENTIFICATION DIVISION.\n"))

	if got, want := root.startByte, uint32(7); got != want {
		t.Fatalf("root startByte = %d, want %d", got, want)
	}
	if got, want := root.startPoint, (Point{Row: 0, Column: 7}); got != want {
		t.Fatalf("root startPoint = %+v, want %+v", got, want)
	}
}
