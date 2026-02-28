package grammars

import (
	"testing"

	"github.com/odvcencio/gotreesitter"
)

func findEntryByName(t *testing.T, name string) LangEntry {
	t.Helper()
	for _, entry := range AllLanguages() {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("language %q is not registered", name)
	return LangEntry{}
}

func parseSampleForEntry(t *testing.T, entry LangEntry, src []byte) (*gotreesitter.Tree, *gotreesitter.Language) {
	t.Helper()
	lang := entry.Language()
	parser := gotreesitter.NewParser(lang)

	report := EvaluateParseSupport(entry, lang)
	var (
		tree *gotreesitter.Tree
		err  error
	)
	switch report.Backend {
	case ParseBackendTokenSource:
		ts := entry.TokenSourceFactory(src, lang)
		tree, err = parser.ParseWithTokenSource(src, ts)
	case ParseBackendDFA, ParseBackendDFAPartial:
		tree, err = parser.Parse(src)
	default:
		t.Fatalf("%s backend unsupported in repro test: %s", entry.Name, report.Backend)
	}
	if err != nil {
		t.Fatalf("%s parse failed: %v", entry.Name, err)
	}
	if tree == nil || tree.RootNode() == nil {
		t.Fatalf("%s parse returned nil root", entry.Name)
	}
	return tree, lang
}

func assertFieldLookupMatchesFirstTaggedChild(t *testing.T, root *gotreesitter.Node, lang *gotreesitter.Language) {
	t.Helper()
	queue := []*gotreesitter.Node{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]

		firstForField := map[string]*gotreesitter.Node{}
		for i := 0; i < n.ChildCount(); i++ {
			child := n.Child(i)
			if child != nil {
				queue = append(queue, child)
			}
			name := n.FieldNameForChild(i, lang)
			if name == "" {
				continue
			}
			if _, exists := firstForField[name]; !exists {
				firstForField[name] = child
			}
		}

		for name, want := range firstForField {
			got := n.ChildByFieldName(name, lang)
			if got != want {
				gotType := "<nil>"
				if got != nil {
					gotType = got.Type(lang)
				}
				wantType := "<nil>"
				if want != nil {
					wantType = want.Type(lang)
				}
				t.Fatalf(
					"%s field lookup mismatch on node %q for field %q: got %q want %q",
					lang.Name,
					n.Type(lang),
					name,
					gotType,
					wantType,
				)
			}
		}
	}
}

func TestIssue3ReproChildByFieldNamePython(t *testing.T) {
	entry := findEntryByName(t, "python")
	src := []byte("def f(a, b=1, *c, **d):\n    return a + b\n")
	tree, lang := parseSampleForEntry(t, entry, src)
	assertFieldLookupMatchesFirstTaggedChild(t, tree.RootNode(), lang)
}

func TestIssue3ReproChildByFieldNameTSX(t *testing.T) {
	entry := findEntryByName(t, "tsx")
	src := []byte("const el = <A.B x={foo?.bar} y=\"z\" />;\n")
	tree, lang := parseSampleForEntry(t, entry, src)
	assertFieldLookupMatchesFirstTaggedChild(t, tree.RootNode(), lang)
}
