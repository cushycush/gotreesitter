package grammargen

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/odvcencio/gotreesitter/grammars"
)

func TestRustForLifetimeAbstractTypeParity(t *testing.T) {
	jsonPath := rustGrammarJSONPathForTest(t)
	source, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Skipf("Rust grammar.json not available: %v", err)
	}
	gram, err := ImportGrammarJSON(source)
	if err != nil {
		t.Fatalf("import Rust grammar.json: %v", err)
	}
	genLang, err := generateWithTimeout(gram, 90*time.Second)
	if err != nil {
		t.Fatalf("generate Rust language: %v", err)
	}
	refLang := grammars.RustLanguage()
	adaptExternalScanner(refLang, genLang)

	sample := "fn main() {}\n\n" +
		"fn add(x: i32, y: i32) -> i32 {\n" +
		"    return x + y;\n" +
		"}\n\n" +
		"fn takes_slice(slice: &str) {\n" +
		"    println!(\"Got: {}\", slice);\n" +
		"}\n\n" +
		"fn foo() -> [u32; 2] {\n" +
		"    return [1, 2];\n" +
		"}\n\n" +
		"fn foo() -> (u32, u16) {\n" +
		"    return (1, 2);\n" +
		"}\n\n" +
		"fn foo() {\n" +
		"    return\n" +
		"}\n\n" +
		"fn foo(x: impl FnOnce() -> result::Result<T, E>) {}\n\n" +
		"fn foo(#[attr] x: i32, #[attr] x: i64) {}\n\n" +
		"fn accumulate(self) -> Machine<{State::Accumulate}> {}\n\n" +
		"fn foo(bar: impl for<'a> Baz<Quux<'a>>) {}\n"

	assertGeneratedAndReferenceDeepParity(t, genLang, refLang, sample)
}

func TestRustStructExpressionParity(t *testing.T) {
	jsonPath := rustGrammarJSONPathForTest(t)
	source, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Skipf("Rust grammar.json not available: %v", err)
	}
	gram, err := ImportGrammarJSON(source)
	if err != nil {
		t.Fatalf("import Rust grammar.json: %v", err)
	}
	genLang, err := generateWithTimeout(gram, 90*time.Second)
	if err != nil {
		t.Fatalf("generate Rust language: %v", err)
	}
	refLang := grammars.RustLanguage()
	adaptExternalScanner(refLang, genLang)

	sample := "NothingInMe {};\n" +
		"Point {x: 10.0, y: 20.0};\n" +
		"let a = SomeStruct { field1, field2: expression, field3, };\n" +
		"let u = game::User {name: \"Joe\", age: 35, score: 100_000};\n" +
		"let i = Instant { 0: Duration::from_millis(0) };\n"

	assertGeneratedAndReferenceDeepParity(t, genLang, refLang, sample)
}

func rustGrammarJSONPathForTest(t *testing.T) string {
	t.Helper()

	candidates := []string{
		"/tmp/grammar_parity/rust/src/grammar.json",
	}
	globs := []string{
		"/tmp/gotreesitter-parity-*/repos/rust/src/grammar.json",
	}
	for _, pattern := range globs {
		matches, err := filepath.Glob(pattern)
		if err == nil && len(matches) > 0 {
			candidates = append(candidates, matches...)
		}
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	t.Skip("Rust grammar.json not available")
	return ""
}
