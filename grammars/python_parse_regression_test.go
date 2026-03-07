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

func TestParseFilePythonNestedMethodDedentsReturnToModule(t *testing.T) {
	src := []byte("import unittest\n\nclass GrammarTests(unittest.TestCase):\n    def test_case(self):\n        keywords = (1,)\n        cases = (2,)\n        for keyword in (1,):\n            for case in (2,):\n                pass\n\nif __name__ == '__main__':\n    unittest.main()\n")

	bt, err := ParseFile("script.py", src)
	if err != nil {
		t.Fatalf("ParseFile error: %v", err)
	}
	defer bt.Release()

	lang := PythonLanguage()
	root := bt.RootNode()
	if root == nil {
		t.Fatal("ParseFile returned nil root for Python nested-loop source")
	}
	if root.HasError() {
		t.Fatalf("expected error-free Python parse tree, got %s", root.SExpr(lang))
	}

	if got, want := root.NamedChildCount(), 3; got != want {
		t.Fatalf("root named child count = %d, want %d: %s", got, want, root.SExpr(lang))
	}
	if got, want := root.NamedChild(0).Type(lang), "import_statement"; got != want {
		t.Fatalf("root named child 0 type = %q, want %q", got, want)
	}
	if got, want := root.NamedChild(1).Type(lang), "class_definition"; got != want {
		t.Fatalf("root named child 1 type = %q, want %q", got, want)
	}
	if got, want := root.NamedChild(2).Type(lang), "if_statement"; got != want {
		t.Fatalf("root named child 2 type = %q, want %q", got, want)
	}
}
