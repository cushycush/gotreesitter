package grammars

import (
	"strings"
	"testing"

	"github.com/odvcencio/gotreesitter"
)

func findFirstNamedDescendantWhere(node *gotreesitter.Node, lang *gotreesitter.Language, typ string, pred func(*gotreesitter.Node) bool) *gotreesitter.Node {
	if node == nil {
		return nil
	}
	if node.IsNamed() && node.Type(lang) == typ && pred(node) {
		return node
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if found := findFirstNamedDescendantWhere(node.NamedChild(i), lang, typ, pred); found != nil {
			return found
		}
	}
	return nil
}

func assertCSharpReadToEndMemberAccessShape(t *testing.T, tree *gotreesitter.Tree, lang *gotreesitter.Language, src []byte) {
	t.Helper()

	if tree == nil || tree.RootNode() == nil {
		t.Fatal("parse returned nil root")
	}
	root := tree.RootNode()
	if root.HasError() {
		t.Fatalf("expected parse without syntax errors, got %s", root.SExpr(lang))
	}

	invocation := findFirstNamedDescendantWhere(root, lang, "invocation_expression", func(node *gotreesitter.Node) bool {
		return strings.Contains(node.Text(src), "process.StandardOutput.ReadToEnd()")
	})
	if invocation == nil {
		t.Fatalf("missing ReadToEnd invocation in tree: %s", root.SExpr(lang))
	}

	function := invocation.ChildByFieldName("function", lang)
	if function == nil {
		t.Fatalf("invocation missing function field: %s", invocation.SExpr(lang))
	}
	if got := function.Type(lang); got != "member_access_expression" {
		t.Fatalf("function type = %q, want member_access_expression: %s", got, invocation.SExpr(lang))
	}

	expression := function.ChildByFieldName("expression", lang)
	if expression == nil {
		t.Fatalf("member access missing expression field: %s", function.SExpr(lang))
	}
	if got := expression.Type(lang); got != "member_access_expression" {
		t.Fatalf("expression type = %q, want member_access_expression: %s", got, function.SExpr(lang))
	}
	if got := expression.Text(src); got != "process.StandardOutput" {
		t.Fatalf("expression text = %q, want %q", got, "process.StandardOutput")
	}
}

func TestCSharpMemberAccessRegression(t *testing.T) {
	lang := CSharpLanguage()
	parser := gotreesitter.NewParser(lang)

	src := []byte(`using System.Diagnostics;

string GetOutput()
{
    var process = new Process();
    process.Start();
    var output = process.StandardOutput.ReadToEnd();
    return output;
}
`)

	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	assertCSharpReadToEndMemberAccessShape(t, tree, lang, src)
}
