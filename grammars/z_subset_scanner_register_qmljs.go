//go:build grammar_subset && grammar_subset_qmljs

package grammars

func init() {
	RegisterExternalScanner("qmljs", QmljsExternalScanner{})
}
