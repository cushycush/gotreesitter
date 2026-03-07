package grammars

import (
	"testing"

	"github.com/odvcencio/gotreesitter"
)

func TestDartNamedConstructorArgumentsDoNotMisparseAsObjectPattern(t *testing.T) {
	lang := DartLanguage()
	parser := gotreesitter.NewParser(lang)

	src := []byte("void main(){ final parser = Parser(sharedLibrary: x, entryPoint: y); }\n")
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("parse returned nil root")
	}
	if root.HasError() {
		t.Fatalf("expected error-free Dart parse tree, got %s", root.SExpr(lang))
	}

	var localDecl *gotreesitter.Node
	gotreesitter.Walk(root, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		if node.IsNamed() && node.Type(lang) == "local_variable_declaration" {
			localDecl = node
			return gotreesitter.WalkStop
		}
		return gotreesitter.WalkContinue
	})
	if localDecl == nil {
		t.Fatalf("missing local_variable_declaration in tree: %s", root.SExpr(lang))
	}

	var objectPattern *gotreesitter.Node
	gotreesitter.Walk(localDecl, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		if node.IsNamed() && node.Type(lang) == "object_pattern" {
			objectPattern = node
			return gotreesitter.WalkStop
		}
		return gotreesitter.WalkContinue
	})
	if objectPattern != nil {
		t.Fatalf("named constructor arguments collapsed into object_pattern: %s", localDecl.SExpr(lang))
	}
}

func TestDartStringLiteralPointsUseByteColumns(t *testing.T) {
	lang := DartLanguage()
	parser := gotreesitter.NewParser(lang)

	src := []byte("void main(){ 'åÅ'; }\n")
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("parse returned nil root")
	}
	if root.HasError() {
		t.Fatalf("expected error-free Dart parse tree, got %s", root.SExpr(lang))
	}

	var lit *gotreesitter.Node
	gotreesitter.Walk(root, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		if node.IsNamed() && node.Type(lang) == "string_literal" {
			lit = node
			return gotreesitter.WalkStop
		}
		return gotreesitter.WalkContinue
	})
	if lit == nil {
		t.Fatalf("missing string_literal in tree: %s", root.SExpr(lang))
	}

	if got, want := lit.StartPoint().Column, uint32(13); got != want {
		t.Fatalf("string_literal start column = %d, want %d", got, want)
	}
	if got, want := lit.EndPoint().Column, uint32(19); got != want {
		t.Fatalf("string_literal end column = %d, want %d", got, want)
	}
}
