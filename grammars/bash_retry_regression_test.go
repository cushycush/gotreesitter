package grammars

import (
	"testing"

	ts "github.com/odvcencio/gotreesitter"
)

func TestBashRetryFullParseOnChainedIfs(t *testing.T) {
	src := []byte(`a=1
if foo; then
  :
fi
b=x
c=x
tar=x
if [ -z "$tar" ]; then
  tar=x
fi
if [ -z "$tar" ]; then
  tar=foo
fi
if [ 1 -eq 0 ] && [ -x "$tar" ]; then
  :
fi
`)
	for i := range src {
		if src[i] == 0x06 {
			src[i] = '`'
		}
	}
	p := ts.NewParser(BashLanguage())
	tree, err := p.Parse(src)
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
	if root.HasError() {
		t.Fatalf("unexpected error tree: %s", root.SExpr(BashLanguage()))
	}
}
