package gotreesitter

import (
	"testing"
)

func TestTreeCursorNew(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)
	root := tree.RootNode()

	c := NewTreeCursor(root, tree)
	if c.CurrentNode() != root {
		t.Fatal("CurrentNode should be root")
	}
	if c.Depth() != 0 {
		t.Fatalf("Depth at root should be 0, got %d", c.Depth())
	}
}

func TestTreeCursorGotoFirstChild(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	c := NewTreeCursor(tree.RootNode(), tree)

	if !c.GotoFirstChild() {
		t.Fatal("GotoFirstChild should succeed on program node")
	}
	// First child of program is function_declaration
	if c.CurrentNode().Symbol() != Symbol(5) {
		t.Fatalf("expected function_declaration (5), got %d", c.CurrentNode().Symbol())
	}
	if c.Depth() != 1 {
		t.Fatalf("Depth should be 1, got %d", c.Depth())
	}

	// Descend again: first child of function_declaration is "func" keyword
	if !c.GotoFirstChild() {
		t.Fatal("GotoFirstChild should succeed on function_declaration")
	}
	if c.CurrentNode().Symbol() != Symbol(8) {
		t.Fatalf("expected func keyword (8), got %d", c.CurrentNode().Symbol())
	}
	if c.Depth() != 2 {
		t.Fatalf("Depth should be 2, got %d", c.Depth())
	}
}

func TestTreeCursorGotoNextSibling(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	c := NewTreeCursor(tree.RootNode(), tree)
	c.GotoFirstChild() // function_declaration
	c.GotoFirstChild() // "func" keyword

	// Traverse siblings: func -> identifier -> parameter_list -> block
	expected := []Symbol{1, 13, 14}
	for i, sym := range expected {
		if !c.GotoNextSibling() {
			t.Fatalf("GotoNextSibling should succeed at step %d", i)
		}
		if c.CurrentNode().Symbol() != sym {
			t.Fatalf("step %d: expected symbol %d, got %d", i, sym, c.CurrentNode().Symbol())
		}
	}

	// No more siblings
	if c.GotoNextSibling() {
		t.Fatal("GotoNextSibling should return false at last sibling")
	}
}

func TestTreeCursorGotoPrevSibling(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	c := NewTreeCursor(tree.RootNode(), tree)
	c.GotoFirstChild() // function_declaration
	c.GotoFirstChild() // "func" keyword

	// Move to last sibling
	for c.GotoNextSibling() {
	}
	// Now at block (14)
	if c.CurrentNode().Symbol() != Symbol(14) {
		t.Fatalf("expected block (14), got %d", c.CurrentNode().Symbol())
	}

	// Go back: block -> parameter_list -> identifier -> func
	expected := []Symbol{13, 1, 8}
	for i, sym := range expected {
		if !c.GotoPrevSibling() {
			t.Fatalf("GotoPrevSibling should succeed at step %d", i)
		}
		if c.CurrentNode().Symbol() != sym {
			t.Fatalf("step %d: expected symbol %d, got %d", i, sym, c.CurrentNode().Symbol())
		}
	}

	// No more prev siblings
	if c.GotoPrevSibling() {
		t.Fatal("GotoPrevSibling should return false at first sibling")
	}
}

func TestTreeCursorGotoParent(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	c := NewTreeCursor(tree.RootNode(), tree)
	c.GotoFirstChild() // function_declaration
	c.GotoFirstChild() // "func" keyword

	if !c.GotoParent() {
		t.Fatal("GotoParent should succeed")
	}
	if c.CurrentNode().Symbol() != Symbol(5) {
		t.Fatalf("expected function_declaration (5), got %d", c.CurrentNode().Symbol())
	}
	if c.Depth() != 1 {
		t.Fatalf("Depth should be 1, got %d", c.Depth())
	}

	if !c.GotoParent() {
		t.Fatal("GotoParent should succeed to root")
	}
	if c.CurrentNode().Symbol() != Symbol(7) {
		t.Fatalf("expected program (7), got %d", c.CurrentNode().Symbol())
	}
	if c.Depth() != 0 {
		t.Fatalf("Depth should be 0, got %d", c.Depth())
	}
}

func TestTreeCursorGotoLastChild(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	c := NewTreeCursor(tree.RootNode(), tree)
	c.GotoFirstChild() // function_declaration

	if !c.GotoLastChild() {
		t.Fatal("GotoLastChild should succeed")
	}
	// Last child of function_declaration is block (14)
	if c.CurrentNode().Symbol() != Symbol(14) {
		t.Fatalf("expected block (14), got %d", c.CurrentNode().Symbol())
	}
}

func TestTreeCursorAtLeaf(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	c := NewTreeCursor(tree.RootNode(), tree)
	c.GotoFirstChild() // function_declaration
	c.GotoFirstChild() // "func" keyword (leaf)

	if c.GotoFirstChild() {
		t.Fatal("GotoFirstChild should return false on leaf node")
	}
	if c.GotoLastChild() {
		t.Fatal("GotoLastChild should return false on leaf node")
	}
}

func TestTreeCursorBoundary(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	c := NewTreeCursor(tree.RootNode(), tree)

	// At root: parent should fail
	if c.GotoParent() {
		t.Fatal("GotoParent at root should return false")
	}

	// At root: siblings should fail
	if c.GotoNextSibling() {
		t.Fatal("GotoNextSibling at root should return false")
	}
	if c.GotoPrevSibling() {
		t.Fatal("GotoPrevSibling at root should return false")
	}
}

func TestTreeCursorFromTree(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	c := NewTreeCursorFromTree(tree)
	if c.CurrentNode() != tree.RootNode() {
		t.Fatal("NewTreeCursorFromTree should start at root")
	}
	if c.Depth() != 0 {
		t.Fatalf("Depth should be 0, got %d", c.Depth())
	}
}

func TestTreeCursorCurrentFieldID(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	// buildSimpleTree: function_declaration children have fields:
	// [0: func(no field), 1: identifier(name=1), 2: parameter_list(parameters=5), 3: block(body=2)]
	c := NewTreeCursor(tree.RootNode(), tree)
	c.GotoFirstChild() // function_declaration
	c.GotoFirstChild() // "func" keyword — no field

	if fid := c.CurrentFieldID(); fid != 0 {
		t.Fatalf("func keyword should have field ID 0, got %d", fid)
	}

	c.GotoNextSibling() // identifier — field "name" (1)
	if fid := c.CurrentFieldID(); fid != FieldID(1) {
		t.Fatalf("identifier should have field ID 1 (name), got %d", fid)
	}

	c.GotoNextSibling() // parameter_list — field "parameters" (5)
	if fid := c.CurrentFieldID(); fid != FieldID(5) {
		t.Fatalf("parameter_list should have field ID 5 (parameters), got %d", fid)
	}

	c.GotoNextSibling() // block — field "body" (2)
	if fid := c.CurrentFieldID(); fid != FieldID(2) {
		t.Fatalf("block should have field ID 2 (body), got %d", fid)
	}
}

func TestTreeCursorCurrentFieldName(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	c := NewTreeCursor(tree.RootNode(), tree)
	c.GotoFirstChild() // function_declaration
	c.GotoFirstChild() // "func" keyword

	if name := c.CurrentFieldName(); name != "" {
		t.Fatalf("func keyword should have empty field name, got %q", name)
	}

	c.GotoNextSibling() // identifier
	if name := c.CurrentFieldName(); name != "name" {
		t.Fatalf("identifier field name should be 'name', got %q", name)
	}

	c.GotoNextSibling() // parameter_list
	if name := c.CurrentFieldName(); name != "parameters" {
		t.Fatalf("parameter_list field name should be 'parameters', got %q", name)
	}

	c.GotoNextSibling() // block
	if name := c.CurrentFieldName(); name != "body" {
		t.Fatalf("block field name should be 'body', got %q", name)
	}
}

func TestTreeCursorGotoChildByFieldName(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	c := NewTreeCursor(tree.RootNode(), tree)
	c.GotoFirstChild() // function_declaration

	if !c.GotoChildByFieldName("body") {
		t.Fatal("GotoChildByFieldName('body') should succeed")
	}
	if c.CurrentNode().Symbol() != Symbol(14) {
		t.Fatalf("expected block (14), got %d", c.CurrentNode().Symbol())
	}

	// Go back to function_declaration and try "name"
	c.GotoParent()
	if !c.GotoChildByFieldName("name") {
		t.Fatal("GotoChildByFieldName('name') should succeed")
	}
	if c.CurrentNode().Symbol() != Symbol(1) {
		t.Fatalf("expected identifier (1), got %d", c.CurrentNode().Symbol())
	}

	// Non-existent field
	c.GotoParent()
	if c.GotoChildByFieldName("nonexistent") {
		t.Fatal("GotoChildByFieldName('nonexistent') should return false")
	}
}

func TestTreeCursorFieldIDAtRoot(t *testing.T) {
	lang := queryTestLanguage()
	tree := buildSimpleTree(lang)

	c := NewTreeCursor(tree.RootNode(), tree)
	if fid := c.CurrentFieldID(); fid != 0 {
		t.Fatalf("field ID at root should be 0, got %d", fid)
	}
	if name := c.CurrentFieldName(); name != "" {
		t.Fatalf("field name at root should be empty, got %q", name)
	}
}
