package grammars

import (
	"testing"

	"github.com/odvcencio/gotreesitter"
)

func TestParseFileCSizeofIdentifierKeepsExpressionBranch(t *testing.T) {
	src := []byte("void f(void) { g(sizeof(TSExternalTokenState)); }\n")

	bt, err := ParseFile("parser.c", src)
	if err != nil {
		t.Fatalf("ParseFile error: %v", err)
	}
	defer bt.Release()

	lang := CLanguage()
	root := bt.RootNode()
	if root == nil {
		t.Fatal("ParseFile returned nil root for C sizeof expression")
	}
	if root.HasError() {
		t.Fatalf("expected error-free C parse tree, got %s", root.SExpr(lang))
	}

	var sizeofExpr *gotreesitter.Node
	gotreesitter.Walk(root, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		if bt.NodeType(node) == "sizeof_expression" {
			sizeofExpr = node
			return gotreesitter.WalkStop
		}
		return gotreesitter.WalkContinue
	})
	if sizeofExpr == nil {
		t.Fatalf("missing sizeof_expression in tree: %s", root.SExpr(lang))
	}

	var parenExpr *gotreesitter.Node
	for i := 0; i < sizeofExpr.ChildCount(); i++ {
		child := sizeofExpr.Child(i)
		if child == nil {
			continue
		}
		switch bt.NodeType(child) {
		case "parenthesized_expression":
			parenExpr = child
		case "type_descriptor":
			t.Fatalf("sizeof(identifier) collapsed to type_descriptor: %s", root.SExpr(lang))
		}
	}
	if parenExpr == nil {
		t.Fatalf("sizeof_expression missing parenthesized_expression: %s", root.SExpr(lang))
	}

	var identifier *gotreesitter.Node
	gotreesitter.Walk(parenExpr, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		if bt.NodeType(node) == "identifier" {
			identifier = node
			return gotreesitter.WalkStop
		}
		return gotreesitter.WalkContinue
	})
	if identifier == nil {
		t.Fatalf("parenthesized_expression missing identifier: %s", root.SExpr(lang))
	}
	if got, want := identifier.Text(src), "TSExternalTokenState"; got != want {
		t.Fatalf("sizeof identifier = %q, want %q", got, want)
	}
}
