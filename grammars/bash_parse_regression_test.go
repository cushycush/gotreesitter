package grammars

import (
	"testing"

	"github.com/odvcencio/gotreesitter"
)

func bashMustParseNoError(t *testing.T, src []byte) (*gotreesitter.Tree, *gotreesitter.Language) {
	t.Helper()

	lang := BashLanguage()
	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("parse returned nil root")
	}
	if root.HasError() {
		t.Fatalf("expected error-free Bash parse tree, got %s", root.SExpr(lang))
	}
	return tree, lang
}

func TestBashIfSubshellParsesWithoutError(t *testing.T) {
	src := []byte(`if [ $ret -eq 0 ]; then
  (exit 0)
else
  rm npm-install-$$.sh
  echo "Failed to download script" >&2
  exit $ret
fi
`)

	tree, lang := bashMustParseNoError(t, src)
	root := tree.RootNode()

	var subshell *gotreesitter.Node
	gotreesitter.Walk(root, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		if node.IsNamed() && node.Type(lang) == "subshell" {
			subshell = node
			return gotreesitter.WalkStop
		}
		return gotreesitter.WalkContinue
	})
	if subshell == nil {
		t.Fatalf("expected subshell node in Bash parse tree: %s", root.SExpr(lang))
	}
	if got, want := subshell.Text(src), "(exit 0)"; got != want {
		t.Fatalf("subshell text = %q, want %q", got, want)
	}
}

func TestBashRepeatedRedirectsKeepRedirectField(t *testing.T) {
	src := []byte("which $readlink >/dev/null 2>/dev/null\n")

	tree, lang := bashMustParseNoError(t, src)
	root := tree.RootNode()
	stmt := root.NamedChild(0)
	if stmt == nil {
		t.Fatalf("expected redirected_statement, got %s", root.SExpr(lang))
	}
	if got, want := stmt.Type(lang), "redirected_statement"; got != want {
		t.Fatalf("statement type = %q, want %q", got, want)
	}
	if got, want := stmt.ChildCount(), 3; got != want {
		t.Fatalf("redirected_statement child count = %d, want %d", got, want)
	}
	for _, idx := range []int{1, 2} {
		if got, want := stmt.FieldNameForChild(idx, lang), "redirect"; got != want {
			t.Fatalf("child %d field = %q, want %q in %s", idx, got, want, root.SExpr(lang))
		}
	}
}

func TestBashCommandArgumentsDoNotCollapseIntoCommandName(t *testing.T) {
	src := []byte("tar czf \"$tarname\" npm\n")

	tree, lang := bashMustParseNoError(t, src)
	root := tree.RootNode()
	if got, want := root.SExpr(lang), "(program (command (command_name (word)) (word) (string (simple_expansion (variable_name))) (word)))"; got != want {
		t.Fatalf("unexpected Bash tar command shape:\n got: %s\nwant: %s", got, want)
	}
}

func TestBashEchoStringArgumentStaysOutsideCommandName(t *testing.T) {
	src := []byte("echo \"release/$zipname\"\n")

	tree, lang := bashMustParseNoError(t, src)
	root := tree.RootNode()
	if got, want := root.SExpr(lang), "(program (command (command_name (word)) (string (string_content) (simple_expansion (variable_name)))))"; got != want {
		t.Fatalf("unexpected Bash echo command shape:\n got: %s\nwant: %s", got, want)
	}
}

func TestBashCaseHeaderKeepsInKeywordSeparate(t *testing.T) {
	src := []byte(`case $node_version in
  *)
    echo x >&2
    ;;
esac
`)

	tree, lang := bashMustParseNoError(t, src)
	root := tree.RootNode()
	stmt := root.NamedChild(0)
	if stmt == nil {
		t.Fatalf("expected case_statement, got %s", root.SExpr(lang))
	}
	if got, want := stmt.Type(lang), "case_statement"; got != want {
		t.Fatalf("statement type = %q, want %q in %s", got, want, root.SExpr(lang))
	}

	gotreesitter.Walk(root, func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		if node.Type(lang) == "heredoc_redirect_token1" {
			t.Fatalf("unexpected heredoc_redirect_token1 in Bash case tree: %s", root.SExpr(lang))
		}
		return gotreesitter.WalkContinue
	})
}

func TestBashForValueExpansionDoesNotMarkSubscriptAsOperator(t *testing.T) {
	src := []byte("for i in \"${filelist[@]}\"; do echo \"$i\"; done\n")

	tree, lang := bashMustParseNoError(t, src)
	root := tree.RootNode()
	stmt := root.NamedChild(0)
	if stmt == nil {
		t.Fatalf("expected for_statement, got %s", root.SExpr(lang))
	}
	if got, want := stmt.Type(lang), "for_statement"; got != want {
		t.Fatalf("statement type = %q, want %q in %s", got, want, root.SExpr(lang))
	}

	value := stmt.Child(3)
	if value == nil || value.Type(lang) != "string" {
		t.Fatalf("unexpected for value node: %s", root.SExpr(lang))
	}
	expansion := value.Child(1)
	if expansion == nil || expansion.Type(lang) != "expansion" {
		t.Fatalf("unexpected expansion node: %s", root.SExpr(lang))
	}
	subscript := expansion.Child(1)
	if subscript == nil || subscript.Type(lang) != "subscript" {
		t.Fatalf("unexpected subscript node: %s", root.SExpr(lang))
	}
	if got := expansion.FieldNameForChild(1, lang); got != "" {
		t.Fatalf("subscript field = %q, want empty in %s", got, root.SExpr(lang))
	}
}

func TestBashReleasePrefixAssignmentParsesWithoutError(t *testing.T) {
	src := []byte(`mv *.tgz release
cd release
tar xzf *.tgz
mkdir node_modules
mv package node_modules/npm
cp node_modules/npm/bin/*.cmd .
zipname=npm-$(node ../cli.js -v).zip
`)

	tree, lang := bashMustParseNoError(t, src)
	root := tree.RootNode()
	if got, want := root.NamedChildCount(), 7; got != want {
		t.Fatalf("named child count = %d, want %d in %s", got, want, root.SExpr(lang))
	}
	last := root.NamedChild(root.NamedChildCount() - 1)
	if last == nil || last.Type(lang) != "variable_assignment" {
		t.Fatalf("expected trailing variable_assignment, got %s", root.SExpr(lang))
	}
}

func TestBashReleaseTarPhaseParsesWithoutError(t *testing.T) {
	src := []byte(`cd node_modules
tarname=npm-$(node ../../cli.js -v).tgz
tar czf "$tarname" npm

cd ..
mv "node_modules/$tarname" .

rm -rf *.cmd
rm -rf node_modules

echo "release/$tarname"
echo "release/$zipname"
`)

	tree, lang := bashMustParseNoError(t, src)
	root := tree.RootNode()
	if got, want := root.NamedChildCount(), 9; got != want {
		t.Fatalf("named child count = %d, want %d in %s", got, want, root.SExpr(lang))
	}
	if first := root.NamedChild(0); first == nil || first.Type(lang) != "command" {
		t.Fatalf("expected leading command, got %s", root.SExpr(lang))
	}
	if second := root.NamedChild(1); second == nil || second.Type(lang) != "variable_assignment" {
		t.Fatalf("expected tarname assignment as second child, got %s", root.SExpr(lang))
	}
	last := root.NamedChild(root.NamedChildCount() - 1)
	if last == nil || last.Type(lang) != "command" {
		t.Fatalf("expected trailing echo command, got %s", root.SExpr(lang))
	}
}
