package gotreesitter

import "testing"

func mustParse(t *testing.T, p *Parser, source []byte) *Tree {
	t.Helper()
	tree, err := p.Parse(source)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	return tree
}

func mustParseWithTS(t *testing.T, p *Parser, source []byte, ts TokenSource) *Tree {
	t.Helper()
	tree, err := p.ParseWithTokenSource(source, ts)
	if err != nil {
		t.Fatalf("ParseWithTokenSource failed: %v", err)
	}
	return tree
}

func mustParseIncremental(t *testing.T, p *Parser, source []byte, oldTree *Tree) *Tree {
	t.Helper()
	tree, err := p.ParseIncremental(source, oldTree)
	if err != nil {
		t.Fatalf("ParseIncremental failed: %v", err)
	}
	return tree
}

func mustParseIncrementalWithTS(t *testing.T, p *Parser, source []byte, oldTree *Tree, ts TokenSource) *Tree {
	t.Helper()
	tree, err := p.ParseIncrementalWithTokenSource(source, oldTree, ts)
	if err != nil {
		t.Fatalf("ParseIncrementalWithTokenSource failed: %v", err)
	}
	return tree
}
