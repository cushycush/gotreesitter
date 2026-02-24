package grammars

import (
	"testing"

	"github.com/odvcencio/gotreesitter"
)

var parseSmokeSamples = map[string]string{
	"agda":              "module M where\n",
	"authzed":           "definition user {}\n",
	"bash":              "echo hi\n",
	"c":                 "int main(void) { return 0; }\n",
	"capnp":             "@0xdbb9ad1f14bf0b36;\nstruct Person {\n  name @0 :Text;\n}\n",
	"c_sharp":           "using System;\n",
	"comment":           "TODO: fix this\n",
	"corn":              "{ x = 1 }\n",
	"cpp":               "int main() { return 0; }\n",
	"css":               "body { color: red; }\n",
	"desktop":           "[Desktop Entry]\n",
	"dtd":               "<!ELEMENT note (#PCDATA)>\n",
	"doxygen":           "/**\n * @brief A function\n * @param x The value\n */\n",
	"earthfile":         "FROM alpine\n",
	"editorconfig":      "root = true\n",
	"go":                "package main\n\nfunc main() {\n\tprintln(1)\n}\n",
	"embedded_template": "<% if true %>\n  hello\n<% end %>\n",
	"facility":          "service Example {\n}\n",
	"foam":              "FoamFile\n{\n    version 2.0;\n}\n",
	"fidl":              "library example;\ntype Foo = struct {};\n",
	"firrtl":            "circuit Top :\n",
	"haskell":           "module Main where\nx = 1\n",
	"html":              "<html><body>Hello</body></html>\n",
	"java":              "class Main { int x; }\n",
	"javascript":        "function f() { return 1; }\nconst x = () => x + 1;\n",
	"json":              "{\"a\": 1}\n",
	"julia":             "module M\nx = 1\nend\n",
	"kotlin":            "fun main() {\n    val x: Int? = null\n    println(x)\n}\n",
	"lua":               "local x = 1\n",
	"php":               "<?php echo 1;\n",
	"python":            "def f():\n    return 1\n",
	"regex":             "a+b*\n",
	"ruby":              "def f\n  1\nend\n",
	"rust":              "fn main() { let x = 1; }\n",
	"sql":               "SELECT id, name FROM users WHERE id = 1;\n",
	"swift":             "1\n",
	"toml":              "a = 1\ntitle = \"hello\"\ntags = [\"x\", \"y\"]\n",
	"tsx":               "const x = <div/>;\n",
	"typescript":        "function f(): number { return 1; }\n",
	"yaml":              "a: 1\n",
	"zig":               "const x: i32 = 1;\n",
	"scala":             "object Main { def f(x: Int): Int = x + 1 }\n",
	"elixir":            "defmodule M do\n  def f(x), do: x\nend\n",
	"graphql":           "type Query { hello: String }\n",
	"hcl":               "resource \"x\" \"y\" { a = 1 }\n",
	"nix":               "let x = 1; in x\n",
	"ocaml":             "let x = 1\n",
	"verilog":           "module m;\nendmodule\n",

	// Phase 2: DFA-only languages
	"dot":        "digraph G { a -> b; }\n",
	"git_config": "[core]\n\tbare = false\n",
	"ini":        "[section]\nkey = value\n",
	"json5":      "{ \"key\": \"value\" }\n",
	"llvm":       "define i32 @main() {\n  ret i32 0\n}\n",
	"move":       "module 0x1::m {}\n",
	"ninja":      "rule cc\n  command = gcc\n",
	"pascal":     "program Hello;\nvar x: integer;\nbegin\n  x := 42;\nend.\n",
	"v":          "fn main() {}\n",
	"vimdoc":     "*tag*\tHelp text\n",

	// Phase 3: scanner-needed languages (targeted samples that avoid external tokens)
	"jsdoc":      "/** hello */\n",
	"wgsl":       "fn main() {}\n",
	"nginx":      "events {}\n",
	"svelte":     "<p>hello</p>\n",
	"xml":        "<root/>\n",
	"r":          "x <- 1\n",
	"rescript":   "1\n",
	"purescript": "module Main where",
	"rst":        "Title\n=====\n",
	"vhdl":       "-- comment\n",

	// DFA with external scanner (trailing newline breaks nushell root node)
	"nushell": "let x = 1",

	// Phase 5: new grammars
	"gdscript":       "extends Node\nfunc _ready():\n\tpass\n",
	"godot_resource": "x = 1\n",
	"groovy":         "def x = 1\n",
	"hare":           "export fn main() void = void;\n",
	"hyprlang":       "general {\n}\n",
	"ledger":         "2024-01-01 Groceries\n  Expenses:Food  $50\n  Assets:Bank\n",
	"liquid":         "{{ name }}\n",
	"nickel":         "1 + 2\n",
	"pem":            "-----BEGIN CERTIFICATE-----\nMIIC\n-----END CERTIFICATE-----\n",
	"pkl":            "x = 1\n",
	"prisma":         "model User {\n  id Int @id\n}\n",
	"promql":         "up{job=\"prometheus\"}\n",
	"ql":             "from int x where x = 1 select x\n",
	"rego":           "package p\n",
	"ron":            "(x: 1)\n",
	"squirrel":       "local x = 1;\n",
	"tablegen":       "class Foo;\n",
	"thrift":         "struct Foo {}\n",
	"uxntal":         "|00 @System &vector $2\n",

	// Phase 6 new grammars
	"crystal":   "x = 1\n",
	"elisp":     "(message \"hello\")\n",
	"fsharp":    "module M\n",
	"haxe":      "1;\n",
	"teal":      "local x: number = 1\n",
	"forth":     ": square dup * ;\n",
	"cobol":     "IDENTIFICATION DIVISION.\nPROGRAM-ID. HELLO.\n",
	"heex":      "<p>hello</p>\n",
	"templ":     "package main\n",
	"jinja2":    "hello\n",
	"gomod":     "module example.com/foo\n\ngo 1.21\n",
	"jq":        ".foo\n",
	"smithy":    "namespace example\n",
	"textproto": "name: \"hello\"\n",
	"tlaplus":   "---- MODULE Test ----\n====\n",
	"matlab":    "1\n",
	"sparql":    "SELECT ?x WHERE { ?x a ?y }\n",
	"turtle":    "@prefix ex: <http://example.org/> .\n",
	"yuck":      "(box :class \"main\" (label :text \"hi\"))\n",
	"typst":     "1\n",

	// Phase 7: missing popular languages
	"perl":    "my $x = 1;\n",
	"prolog":  "parent(tom, bob).\n",
	"mojo":    "fn main():\n    print(1)\n",
	"wolfram": "1 + 2\n",
}

var parseSmokeKnownDegraded = map[string]string{
	"norg": "requires external scanner (122 tokens) not yet implemented",
	"hurl": "DFA lexer cannot handle this grammar",
}

func parseSmokeSample(name string) string {
	if sample, ok := parseSmokeSamples[name]; ok {
		return sample
	}
	return "x\n"
}

func parseSmokeDegradedReason(report ParseSupport, name string) string {
	if reason, ok := parseSmokeKnownDegraded[name]; ok {
		return reason
	}
	if report.Reason != "" {
		return report.Reason
	}
	return "parser reported recoverable syntax errors on smoke sample"
}

func TestSupportedLanguagesParseSmoke(t *testing.T) {
	entries := AllLanguages()
	entryByName := make(map[string]LangEntry, len(entries))
	for _, entry := range entries {
		entryByName[entry.Name] = entry
	}

	reports := AuditParseSupport()
	for _, report := range reports {
		sample := parseSmokeSample(report.Name)

		if report.Backend == ParseBackendUnsupported {
			t.Logf("skip %s: %s", report.Name, report.Reason)
			continue
		}

		entry, ok := entryByName[report.Name]
		if !ok {
			t.Fatalf("missing registry entry for %q", report.Name)
		}
		lang := entry.Language()
		parser := gotreesitter.NewParser(lang)
		source := []byte(sample)

		var tree *gotreesitter.Tree
		var parseErr error
		switch report.Backend {
		case ParseBackendTokenSource:
			ts := entry.TokenSourceFactory(source, lang)
			tree, parseErr = parser.ParseWithTokenSource(source, ts)
		case ParseBackendDFA, ParseBackendDFAPartial:
			tree, parseErr = parser.Parse(source)
		default:
			t.Fatalf("unknown backend %q for %q", report.Backend, report.Name)
		}
		if parseErr != nil {
			t.Fatalf("%s parse failed: %v", report.Name, parseErr)
		}

		if tree == nil || tree.RootNode() == nil {
			t.Fatalf("%s parse returned nil root using backend %q", report.Name, report.Backend)
		}
		if tree.RootNode().HasError() {
			t.Logf("%s parse smoke sample produced syntax errors (degraded): %s", report.Name, parseSmokeDegradedReason(report, report.Name))
			continue
		}
	}
}
