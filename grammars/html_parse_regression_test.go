package grammars

import (
	"strings"
	"testing"

	"github.com/odvcencio/gotreesitter"
)

func TestParseHTMLDeeplyNestedCustomKeepsRecoveredInnerRange(t *testing.T) {
	lang := HtmlLanguage()
	src := []byte("<div>\n" + strings.Repeat("<abc>", 900) + "\n</div>\n")

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if tree == nil || tree.RootNode() == nil {
		t.Fatal("Parse() returned nil root")
	}
	root := tree.RootNode()
	if got, want := root.Type(lang), "document"; got != want {
		t.Fatalf("root.Type = %q, want %q", got, want)
	}
	if root.HasError() {
		t.Fatal("root.HasError = true, want false")
	}

	outer := root.Child(0)
	if outer == nil || outer.ChildCount() < 3 {
		t.Fatalf("outer = %#v, childCount = %d, want >= 3", outer, outer.ChildCount())
	}
	inner := outer.Child(1)
	endTag := outer.Child(2)
	if inner == nil || endTag == nil || endTag.ChildCount() == 0 {
		t.Fatalf("unexpected outer children: inner=%#v endTag=%#v", inner, endTag)
	}
	closeTok := endTag.Child(0)
	if closeTok == nil {
		t.Fatal("endTag.Child(0) = nil")
	}
	if got, want := inner.EndByte(), closeTok.StartByte(); got != want {
		t.Fatalf("inner.EndByte = %d, want %d", got, want)
	}
	if got, want := inner.EndPoint(), closeTok.StartPoint(); got != want {
		t.Fatalf("inner.EndPoint = %#v, want %#v", got, want)
	}
}
