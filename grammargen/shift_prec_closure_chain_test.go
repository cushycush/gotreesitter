package grammargen

import (
	"strings"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// TestShiftPrecedencePropagatesThroughRepeat1Closure reproduces the Fortran
// call_expression vs math_expression precedence bug (baseline-2026-04-10
// punch list item #1). The minimal shape is:
//
//	call_expr  = prec(80, seq(_expr, repeat1(argument_list)))
//	math_expr  = prec_left(60, seq(_expr, "/", _expr))
//	_expr      = call_expr | math_expr | identifier
//
// Parsing `f(1)/f(2)` must produce `(math_expr (call_expr ...) (call_expr ...))`.
// Before the fix, shift precedence of `(` in the state after the math_expr
// reduce gets stuck at 0 because `propagateEntryShiftMetadataForState` only
// matches shifts whose lhsSym equals the immediate kernel-item next symbol
// (call_expression_repeat1), while the actual shift on `(` has
// lhsSym=argument_list (the deeper closure item). With prec stuck at 0,
// the math_expr reduce (prec=60) incorrectly wins the S/R conflict and
// parses the input as `(call_expr (math_expr (call identifier arglist)
// identifier) arglist)`.
func TestShiftPrecedencePropagatesThroughRepeat1Closure(t *testing.T) {
	g := NewGrammar("call_chain_prec")
	g.Rules["program"] = Sym("_expr")
	g.Rules["_expr"] = Choice(
		Sym("call_expr"),
		Sym("math_expr"),
		Sym("identifier"),
	)
	g.Rules["call_expr"] = Prec(80, Seq(
		Sym("_expr"),
		Repeat1(Sym("argument_list")),
	))
	g.Rules["math_expr"] = PrecLeft(60, Seq(
		Sym("_expr"),
		Str("/"),
		Sym("_expr"),
	))
	g.Rules["argument_list"] = Seq(Str("("), Sym("_expr"), Str(")"))
	g.Rules["identifier"] = Pat(`[a-z][a-z0-9_]*`)
	g.RuleOrder = []string{
		"program",
		"_expr",
		"call_expr",
		"math_expr",
		"argument_list",
		"identifier",
	}
	g.Extras = []*Rule{Pat(`\s+`)}

	lang, err := GenerateLanguage(g)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	parser := gotreesitter.NewParser(lang)
	// First sanity: just a call_expr alone.
	{
		tree, err := parser.Parse([]byte("f(x)"))
		if err != nil {
			t.Fatalf("parse f(x): %v", err)
		}
		root := tree.RootNode()
		if root.HasError() {
			t.Fatalf("parse f(x) has error: %s\n  tree=%s", tree.ParseRuntime().Summary(), root.SExpr(lang))
		}
		t.Logf("f(x) -> %s", root.SExpr(lang))
	}

	// The critical test: infix divide between two calls.
	tree, err := parser.Parse([]byte("f(x)/g(y)"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	root := tree.RootNode()
	if root.HasError() {
		t.Fatalf("parse has error: %s\n  tree=%s", tree.ParseRuntime().Summary(), root.SExpr(lang))
	}

	sexpr := root.SExpr(lang)
	t.Logf("f(x)/g(y) -> %s", sexpr)
	if !strings.Contains(sexpr, "math_expr") {
		t.Fatalf("expected math_expr in parse tree, got: %s", sexpr)
	}
	want := "(program (math_expr (call_expr"
	if !strings.HasPrefix(sexpr, want) {
		t.Fatalf("shift prec lost through repeat1→argument_list closure chain\n  got:  %s\n  want prefix: %s", sexpr, want)
	}
}
