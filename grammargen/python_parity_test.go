package grammargen

import (
	"testing"
	"time"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func TestPythonPatternMatchingParity(t *testing.T) {
	genLang := loadGeneratedPythonLanguageForParity(t)
	refLang := grammars.PythonLanguage()
	adaptExternalScanner(refLang, genLang)

	sample := "match event.get():\n" +
		"    case Click(position=(x, y)):\n" +
		"        handle_click_at(x, y)\n" +
		"    case KeyPress(key_name=\"Q\") | Quit():\n" +
		"        game.quit()\n" +
		"    case KeyPress(key_name=\"up arrow\"):\n" +
		"        game.go_north()\n" +
		"        ...\n" +
		"    case KeyPress():\n" +
		"        pass # Ignore other keystrokes\n" +
		"    case other_event:\n" +
		"        raise ValueError(f\"Unrecognized event: {other_event}\")\n"

	assertPythonParity(t, genLang, refLang, sample)
}

func TestPythonFStringLiteralParity(t *testing.T) {
	genLang := loadGeneratedPythonLanguageForParity(t)
	refLang := grammars.PythonLanguage()
	adaptExternalScanner(refLang, genLang)

	sample := "# nested!\n" +
		"f\"a {b(f'c {e} d')} e\"\n" +
		"f\"\"\"a\"{b}c\"\"\"\n" +
		"f\"\"\"a\"\"{b}c\"\"\"\n" +
		"f\"a {{}} e\"\n" +
		"f\"a {b}}}\"\n" +
		"f\"a {{{b}\"\n" +
		"f\"a {{b}}\"\n" +
		"f\"a {{{b}}}\"\n" +
		"f\"{c,}\"\n" +
		"f\"{yield d}\"\n" +
		"f\"{*a,}\"\n" +
		"\n" +
		"def function():\n" +
		"    return f\"\"\"\n" +
		"{\"string1\" if True else\n" +
		" \"string2\"}\"\"\"\n" +
		"\n" +
		"def test(self):\n" +
		"    self.assertEqual(f'''A complex trick: {\n" +
		"2  # two\n" +
		"}''', 'A complex trick: 2')\n"

	assertPythonParity(t, genLang, refLang, sample)
}

func loadGeneratedPythonLanguageForParity(t *testing.T) *gotreesitter.Language {
	t.Helper()

	gram := loadPythonGrammarJSONForTest(t)
	genLang, err := generateWithTimeout(gram, 90*time.Second)
	if err != nil {
		t.Fatalf("generate Python language: %v", err)
	}
	return genLang
}

func assertPythonParity(t *testing.T, genLang, refLang *gotreesitter.Language, sample string) {
	t.Helper()

	genTree, err := gotreesitter.NewParser(genLang).Parse([]byte(sample))
	if err != nil {
		t.Fatalf("generated parse returned error: %v", err)
	}
	refTree, err := gotreesitter.NewParser(refLang).Parse([]byte(sample))
	if err != nil {
		t.Fatalf("reference parse returned error: %v", err)
	}
	t.Cleanup(genTree.Release)
	t.Cleanup(refTree.Release)

	genRoot := genTree.RootNode()
	refRoot := refTree.RootNode()
	genSexp := genRoot.SExpr(genLang)
	refSexp := refRoot.SExpr(refLang)

	if genRoot.HasError() || refRoot.HasError() {
		t.Fatalf("error mismatch\nGEN hasError=%v\nGEN: %s\nREF hasError=%v\nREF: %s",
			genRoot.HasError(), genSexp, refRoot.HasError(), refSexp)
	}
	if genSexp != refSexp {
		t.Fatalf("SExpr mismatch\nGEN: %s\nREF: %s", genSexp, refSexp)
	}
	if divs := compareTreesDeep(genRoot, genLang, refRoot, refLang, "root", 10); len(divs) > 0 {
		t.Fatalf("deep mismatch: %s\nGEN: %s\nREF: %s", divs[0].String(), genSexp, refSexp)
	}
}
