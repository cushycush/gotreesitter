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

func TestParseFileCFunctionPointerDeclaratorKeepsDeclaratorField(t *testing.T) {
	src := []byte("void (*f)(void *ptr);\n")

	bt, err := ParseFile("decl.c", src)
	if err != nil {
		t.Fatalf("ParseFile error: %v", err)
	}
	defer bt.Release()

	lang := CLanguage()
	root := bt.RootNode()
	if root == nil {
		t.Fatal("ParseFile returned nil root for C function pointer declaration")
	}
	if root.HasError() {
		t.Fatalf("expected error-free C parse tree, got %s", root.SExpr(lang))
	}

	decl := root.NamedChild(0)
	if decl == nil || bt.NodeType(decl) != "declaration" {
		t.Fatalf("expected declaration, got %s", root.SExpr(lang))
	}

	var fnDecl *gotreesitter.Node
	var field string
	for i := 0; i < decl.ChildCount(); i++ {
		child := decl.Child(i)
		if child == nil || bt.NodeType(child) != "function_declarator" {
			continue
		}
		fnDecl = child
		field = decl.FieldNameForChild(i, lang)
		break
	}
	if fnDecl == nil {
		t.Fatalf("missing function_declarator in tree: %s", root.SExpr(lang))
	}
	if got, want := field, "declarator"; got != want {
		t.Fatalf("declaration field for function_declarator = %q, want %q in %s", got, want, root.SExpr(lang))
	}
}
