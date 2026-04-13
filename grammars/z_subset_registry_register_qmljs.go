//go:build grammar_subset && grammar_subset_qmljs

package grammars

func init() {
	Register(LangEntry{
		Name:               "qmljs",
		Extensions:         []string{".qml"},
		Language:           QmljsLanguage,
		GrammarSource:      GrammarSourceTS2GoBlob,
		HighlightQuery:     qmljsHighlightQuery,
		TokenSourceFactory: defaultTokenSourceFactory("qmljs"),
	})
}
