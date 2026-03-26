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

func TestRustPatternStatementParity(t *testing.T) {
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

	sample := "if let A(x) | B(x) = expr {\n" +
		"    do_stuff_with(x);\n" +
		"}\n\n" +
		"while let A(x) | B(x) = expr {\n" +
		"    do_stuff_with(x);\n" +
		"}\n\n" +
		"let Ok(index) | Err(index) = slice.binary_search(&x);\n\n" +
		"for ref a | b in c {}\n\n" +
		"let Ok(x) | Err(x) = binary_search(x);\n\n" +
		"for A | B | C in c {}\n\n" +
		"|(Ok(x) | Err(x))| expr();\n\n" +
		"let ref mut x @ (A | B | C);\n\n" +
		"fn foo((1 | 2 | 3): u8) {}\n\n" +
		"if let x!() | y!() = () {}\n"

	assertGeneratedAndReferenceDeepParity(t, genLang, refLang, sample)
}

func TestRustMacroInvocationParity(t *testing.T) {
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

	sample := "a!(* a *);\n" +
		"a!(& a &);\n" +
		"a!(- a -);\n" +
		"a!(b + c + +);\n" +
		"a!('a'..='z');\n" +
		"a!('\\u{0}'..='\\u{2}');\n" +
		"a!('lifetime)\n" +
		"default!(a);\n" +
		"union!(a);\n" +
		"a!($);\n" +
		"a!($());\n" +
		"a!($ a $);\n" +
		"a!(${$([ a ])});\n" +
		"a!($a $a:ident $($a);*);\n"

	assertGeneratedAndReferenceDeepParity(t, genLang, refLang, sample)
}

func TestRustWeirdExpressionsParity(t *testing.T) {
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

	sample := "fn angrydome() {\n" +
		"    loop { if break { } }\n" +
		"    let mut i = 0;\n" +
		"    loop { i += 1; if i == 1 { match (continue) { 1 => { }, _ => panic!(\"wat\") } }\n" +
		"      break; }\n" +
		"}\n\n" +
		"fn special_characters() {\n" +
		"    let val = !((|(..):(_,_),(|__@_|__)|__)((&*\"\\\\\",'🤔')/**/,{})=={&[..=..][..];})//\n" +
		"    ;\n" +
		"    assert!(!val);\n" +
		"}\n\n" +
		"fn function() {\n" +
		"    struct foo;\n" +
		"    impl Deref for foo {\n" +
		"        type Target = fn() -> Self;\n" +
		"        fn deref(&self) -> &Self::Target {\n" +
		"            &((|| foo) as _)\n" +
		"        }\n" +
		"    }\n" +
		"    let foo = foo () ()() ()()() ()()()() ()()()()();\n" +
		"}\n\n" +
		"fn closure_matching() {\n" +
		"    let x = |_| Some(1);\n" +
		"    let (|x| x) = match x(..) {\n" +
		"        |_| Some(2) => |_| Some(3),\n" +
		"        |_| _ => unreachable!(),\n" +
		"    };\n" +
		"    assert!(matches!(x(..), |_| Some(4)));\n" +
		"}\n"

	assertGeneratedAndReferenceDeepParity(t, genLang, refLang, sample)
}

func TestRustWeirdTopLevelParity(t *testing.T) {
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

	sample := "// Just a grab bag of stuff that you wouldn't want to actually write.\n\n" +
		"fn strange() -> bool { let _x: bool = return true; }\n\n" +
		"fn what() {\n" +
		"    fn the(x: &Cell<bool>) {\n" +
		"        return while !x.get() { x.set(true); };\n" +
		"    }\n" +
		"    let i = &Cell::new(false);\n" +
		"    let dont = {||the(i)};\n" +
		"    dont();\n" +
		"    assert!((i.get()));\n" +
		"}\n\n" +
		"fn punch_card() -> impl std::fmt::Debug {\n" +
		"    ..=..=.. ..    .. .. .. ..    .. .. .. ..    .. ..=.. ..\n" +
		"    ..=.. ..=..    .. .. .. ..    .. .. .. ..    ..=..=..=..\n" +
		"    ..=.. ..=..    ..=.. ..=..    .. ..=..=..    .. ..=.. ..\n" +
		"    ..=..=.. ..    ..=.. ..=..    ..=.. .. ..    .. ..=.. ..\n" +
		"    ..=.. ..=..    ..=.. ..=..    .. ..=.. ..    .. ..=.. ..\n" +
		"    ..=.. ..=..    ..=.. ..=..    .. .. ..=..    .. ..=.. ..\n" +
		"    ..=.. ..=..    .. ..=..=..    ..=..=.. ..    .. ..=.. ..\n" +
		"}\n"

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
