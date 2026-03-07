package grammars

import (
	"testing"

	"github.com/odvcencio/gotreesitter"
)

func TestPythonComparisonOperatorFieldStaysOnOperatorToken(t *testing.T) {
	lang := PythonLanguage()
	parser := gotreesitter.NewParser(lang)

	src := []byte("if left != right:\n    pass\n")
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("parse returned nil root")
	}
	if root.HasError() {
		t.Fatalf("expected error-free Python parse tree, got %s", root.SExpr(lang))
	}

	var cmp *gotreesitter.Node
	gotreesitter.Walk(root, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		if node.IsNamed() && node.Type(lang) == "comparison_operator" {
			cmp = node
			return gotreesitter.WalkStop
		}
		return gotreesitter.WalkContinue
	})
	if cmp == nil {
		t.Fatal("expected to find comparison_operator in Python parse tree")
	}

	operator := cmp.ChildByFieldName("operators", lang)
	if operator == nil {
		t.Fatal("comparison_operator missing operators field")
	}
	if got, want := operator.Text(src), "!="; got != want {
		t.Fatalf("operators field text = %q, want %q", got, want)
	}

	for i := 0; i < cmp.ChildCount(); i++ {
		child := cmp.Child(i)
		if child == nil || child == operator {
			continue
		}
		if got := cmp.FieldNameForChild(i, lang); got == "operators" {
			t.Fatalf("child %d (%s %q) unexpectedly has operators field", i, child.Type(lang), child.Text(src))
		}
	}
}
