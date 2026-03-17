package grammargen

import (
	"strings"
	"testing"
)

func TestGenerateHighlightQueriesDanmuji(t *testing.T) {
	base := GoGrammar()
	ext := DanmujiGrammar()

	queries := GenerateHighlightQueries(base, ext)
	t.Logf("Danmuji highlights:\n%s", queries)

	// Should have danmuji-specific keywords
	if !strings.Contains(queries, `"unit" @keyword`) {
		t.Error("expected unit keyword highlight")
	}
	if !strings.Contains(queries, `"given" @keyword`) {
		t.Error("expected given keyword highlight")
	}
	if !strings.Contains(queries, `"expect" @keyword`) {
		t.Error("expected expect keyword highlight")
	}
	if !strings.Contains(queries, `"mock" @keyword`) {
		t.Error("expected mock keyword highlight")
	}

	// Should have operators
	if !strings.Contains(queries, `"->" @operator`) {
		t.Error("expected -> operator highlight")
	}

	// Should have rule-specific highlights
	if !strings.Contains(queries, "mock_declaration") {
		t.Error("expected mock_declaration highlight rule")
	}
	if !strings.Contains(queries, "@type.definition") {
		t.Error("expected @type.definition for declarations")
	}
	if !strings.Contains(queries, "given_block") {
		t.Error("expected given_block highlight rule")
	}
}

func TestGenerateHighlightQueriesDingo(t *testing.T) {
	base := GoGrammar()
	ext := DingoGrammar()

	queries := GenerateHighlightQueries(base, ext)
	t.Logf("Dingo highlights:\n%s", queries)

	// Keywords
	if !strings.Contains(queries, `"enum" @keyword`) {
		t.Error("expected enum keyword")
	}
	if !strings.Contains(queries, `"match" @keyword`) {
		t.Error("expected match keyword")
	}
	if !strings.Contains(queries, `"let" @keyword`) {
		t.Error("expected let keyword")
	}
	if !strings.Contains(queries, `"fn" @keyword`) {
		t.Error("expected fn keyword")
	}
	if !strings.Contains(queries, `"try" @keyword`) {
		t.Error("expected try keyword")
	}

	// Operators
	if !strings.Contains(queries, `"?." @operator`) {
		t.Error("expected ?. operator")
	}
	if !strings.Contains(queries, `"??" @operator`) {
		t.Error("expected ?? operator")
	}
	if !strings.Contains(queries, `"=>" @operator`) {
		t.Error("expected => operator")
	}

	// Enum variant highlighting
	if !strings.Contains(queries, "enum_variant") {
		t.Error("expected enum_variant highlight")
	}
	if !strings.Contains(queries, "@constructor") {
		t.Error("expected @constructor for variants")
	}

	// Let variable definition
	if !strings.Contains(queries, "let_declaration") {
		t.Error("expected let_declaration highlight")
	}
	if !strings.Contains(queries, "@variable.definition") {
		t.Error("expected @variable.definition for let")
	}

	// Lambda params
	if !strings.Contains(queries, "lambda_expression") {
		t.Error("expected lambda_expression highlight")
	}
	if !strings.Contains(queries, "@variable.parameter") {
		t.Error("expected @variable.parameter for lambda params")
	}
}

func TestGenerateHighlightQueriesNoNewRules(t *testing.T) {
	base := GoGrammar()
	queries := GenerateHighlightQueries(base, base)
	if queries != "" {
		t.Errorf("expected empty queries for identical grammars, got:\n%s", queries)
	}
}
