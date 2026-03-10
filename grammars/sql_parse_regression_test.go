package grammars

import (
	"testing"

	ts "github.com/odvcencio/gotreesitter"
)

func TestSQLTrailingCommaAtEOFRecoversStatementPrefix(t *testing.T) {
	src := []byte("SELECT a::int,\n-- x\n")
	parser := ts.NewParser(SqlLanguage())
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("missing root node")
	}
	if tree.ParseStopReason() != ts.ParseStopAccepted {
		t.Fatalf("stop=%s runtime=%s", tree.ParseStopReason(), tree.ParseRuntime().Summary())
	}
	if !root.HasError() {
		t.Fatalf("expected recovered SQL tree to retain error flag, got %s", root.SExpr(SqlLanguage()))
	}
	if got := root.ChildCount(); got != 3 {
		t.Fatalf("root child count = %d, want 3; tree=%s", got, root.SExpr(SqlLanguage()))
	}
	if first := root.Child(0); first == nil || first.Type(SqlLanguage()) != "select_statement" {
		t.Fatalf("first child = %v, want select_statement; tree=%s", first, root.SExpr(SqlLanguage()))
	}
	if second := root.Child(1); second == nil || !second.IsError() {
		t.Fatalf("second child = %v, want ERROR; tree=%s", second, root.SExpr(SqlLanguage()))
	} else if got := second.Type(SqlLanguage()); got != "ERROR" {
		t.Fatalf("second child type = %q, want ERROR", got)
	} else if got := second.ChildCount(); got != 1 {
		t.Fatalf("ERROR child count = %d, want 1", got)
	}
	if third := root.Child(2); third == nil || third.Type(SqlLanguage()) != "comment" {
		t.Fatalf("third child = %v, want comment; tree=%s", third, root.SExpr(SqlLanguage()))
	}
}
