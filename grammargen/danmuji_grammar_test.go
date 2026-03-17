package grammargen

import (
	"strings"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// danmujiLang is a package-level cached language to avoid regenerating for each test.
var danmujiLang *gotreesitter.Language

func getDanmujiLang(t *testing.T) *gotreesitter.Language {
	t.Helper()
	if danmujiLang != nil {
		return danmujiLang
	}
	g := DanmujiGrammar()
	lang, err := GenerateLanguage(g)
	if err != nil {
		t.Fatalf("GenerateLanguage(DanmujiGrammar) failed: %v", err)
	}
	danmujiLang = lang
	return lang
}

func parseDanmuji(t *testing.T, input string) string {
	t.Helper()
	lang := getDanmujiLang(t)
	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("Root node is nil")
	}
	return root.SExpr(lang)
}

// TestDanmujiGoCompat verifies that pure Go code still parses cleanly.
func TestDanmujiGoCompat(t *testing.T) {
	samples := []struct {
		name  string
		input string
	}{
		{
			"hello_world",
			`package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`,
		},
		{
			"variable_decl",
			`package main

var x int = 42
`,
		},
		{
			"if_else",
			`package main

func f() {
	if x > 0 {
		return
	} else {
		x = 0
	}
}
`,
		},
		{
			"for_loop",
			`package main

func f() {
	for i := 0; i < 10; i++ {
		_ = i
	}
}
`,
		},
		{
			"struct",
			`package main

type Point struct {
	X int
	Y int
}
`,
		},
	}

	for _, tt := range samples {
		t.Run(tt.name, func(t *testing.T) {
			sexp := parseDanmuji(t, tt.input)
			t.Logf("SExpr: %s", sexp)
			if strings.Contains(sexp, "ERROR") {
				t.Errorf("pure Go should parse clean, got ERROR in: %s", sexp)
			}
		})
	}
}

// TestDanmujiUnitBlock tests basic unit test block parsing.
func TestDanmujiUnitBlock(t *testing.T) {
	input := `package main
unit "arithmetic" {
	x := 1
	_ = x
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "test_block") {
		t.Error("expected test_block node in parse tree")
	}
	if !strings.Contains(sexp, "test_category") {
		t.Error("expected test_category node in parse tree")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR in parse tree: %s", sexp)
	}
}

// TestDanmujiGivenWhenThen tests BDD given/when/then structure.
func TestDanmujiGivenWhenThen(t *testing.T) {
	input := `package main
func f() {
	given "a user" {
		when "they login" {
			then "they see dashboard" {
				_ = true
			}
		}
	}
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "given_block") {
		t.Error("expected given_block node")
	}
	if !strings.Contains(sexp, "when_block") {
		t.Error("expected when_block node")
	}
	if !strings.Contains(sexp, "then_block") {
		t.Error("expected then_block node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}

// TestDanmujiExpect tests assertion expressions.
func TestDanmujiExpect(t *testing.T) {
	input := `package main
func f() {
	expect x == 1
	expect err != nil
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "expect_statement") {
		t.Error("expected expect_statement node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}

// TestDanmujiMock tests mock declaration parsing.
func TestDanmujiMock(t *testing.T) {
	input := `package main
func f() {
	mock Repo {
		Save(u User) -> error = nil
	}
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "mock_declaration") {
		t.Error("expected mock_declaration node")
	}
	if !strings.Contains(sexp, "mock_method") {
		t.Error("expected mock_method node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}

// TestDanmujiLifecycle tests lifecycle hooks.
func TestDanmujiLifecycle(t *testing.T) {
	input := `package main
func f() {
	before each {
		x := 0
		_ = x
	}
	after each {
		x := 0
		_ = x
	}
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "lifecycle_hook") {
		t.Error("expected lifecycle_hook node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}

// TestDanmujiTags tests tagged test blocks.
func TestDanmujiTags(t *testing.T) {
	input := `package main
@slow @smoke integration "full suite" {
	_ = true
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "tag") {
		t.Error("expected tag node")
	}
	if !strings.Contains(sexp, "test_block") {
		t.Error("expected test_block node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}

// TestDanmujiBenchmark tests benchmark block parsing.
func TestDanmujiBenchmark(t *testing.T) {
	input := `package main
benchmark "JSON marshal" {
	setup {
		data := makeLargeStruct()
	}
	measure {
		json.Marshal(data)
	}
	report_allocs
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "benchmark_block") {
		t.Error("expected benchmark_block node")
	}
	if !strings.Contains(sexp, "measure_block") {
		t.Error("expected measure_block node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}

// TestDanmujiParallelBenchmark tests parallel measure block parsing.
func TestDanmujiParallelBenchmark(t *testing.T) {
	input := `package main
benchmark "concurrent reads" {
	setup {
		cache := NewCache()
	}
	parallel measure {
		cache.Get("key")
	}
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "parallel_measure_block") {
		t.Error("expected parallel_measure_block node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}

// TestDanmujiNeedsBlock tests needs block parsing.
func TestDanmujiNeedsBlock(t *testing.T) {
	input := `package main
func f() {
	needs postgres db {
		port = 5432
	}
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "needs_block") {
		t.Error("expected needs_block node")
	}
	if !strings.Contains(sexp, "service_type") {
		t.Error("expected service_type node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}

// TestDanmujiLoadBlock tests load block with rate, duration, target parsing.
func TestDanmujiLoadBlock(t *testing.T) {
	input := `package main
load "checkout" {
	rate 50
	duration 2
	target post "http://localhost:8080/api"
	then "fast" {
		expect true
	}
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "load_block") {
		t.Error("expected load_block node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}

// TestDanmujiExecBlock tests exec block with run command parsing.
func TestDanmujiExecBlock(t *testing.T) {
	input := `package main
func f() {
	exec "run migrations" {
		run "echo hello"
		expect exit_code == 0
		expect stdout contains "hello"
	}
}
`
	sexp := parseDanmuji(t, input)
	t.Logf("SExpr: %s", sexp)
	if !strings.Contains(sexp, "exec_block") {
		t.Error("expected exec_block node")
	}
	if strings.Contains(sexp, "ERROR") {
		t.Errorf("unexpected ERROR: %s", sexp)
	}
}
