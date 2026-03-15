package grammars

import "testing"

func TestLangEntryLSPFields(t *testing.T) {
	entry := LangEntry{
		Name:        "test",
		ScopeQuery:  "(function_declaration name: (identifier) @def.function)",
		TypeQuery:   "(function_declaration result: (_) @def.function.return)",
		ImportQuery: "(import_spec path: (interpreted_string_literal) @import.path)",
	}
	if entry.ScopeQuery == "" {
		t.Error("ScopeQuery should be set")
	}
	if entry.TypeQuery == "" {
		t.Error("TypeQuery should be set")
	}
	if entry.ImportQuery == "" {
		t.Error("ImportQuery should be set")
	}
}
