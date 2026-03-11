package gotreesitter

import "testing"

func TestBuildPowerShellVariableMemberAccessBuildsRecoveredPath(t *testing.T) {
	lang := &Language{
		Name: "powershell",
		SymbolNames: []string{
			"EOF", "member_access", "variable", "\\", ".", "member_name", "simple_name", "ERROR",
		},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "member_access", Visible: true, Named: true},
			{Name: "variable", Visible: true, Named: true},
			{Name: "\\", Visible: true, Named: false},
			{Name: ".", Visible: true, Named: false},
			{Name: "member_name", Visible: true, Named: true},
			{Name: "simple_name", Visible: true, Named: true},
			{Name: "ERROR", Visible: true, Named: true},
		},
	}

	source := []byte("$targetPsHome\\pwrshplugin.dll")
	arena := newNodeArena(arenaClassFull)
	node := buildPowerShellVariableMemberAccess(arena, source, lang, 0, len(source))
	if node == nil {
		t.Fatal("node = nil")
	}
	if got, want := node.Type(lang), "member_access"; got != want {
		t.Fatalf("node.Type = %q, want %q", got, want)
	}
	if got, want := len(node.children), 4; got != want {
		t.Fatalf("len(node.children) = %d, want %d", got, want)
	}
	if got, want := node.children[1].Type(lang), "ERROR"; got != want {
		t.Fatalf("node.children[1].Type = %q, want %q", got, want)
	}
	if !node.children[1].isExtra {
		t.Fatalf("node.children[1].isExtra = false, want true")
	}
}

func TestBuildPowerShellExpandableStringLiteralKeepsFullRangeWithVariable(t *testing.T) {
	lang := &Language{
		Name:        "powershell",
		SymbolNames: []string{"EOF", "expandable_string_literal", "variable"},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "expandable_string_literal", Visible: true, Named: true},
			{Name: "variable", Visible: true, Named: true},
		},
	}

	source := []byte("\"Creating $pluginBasePath\"")
	arena := newNodeArena(arenaClassFull)
	node := buildPowerShellExpandableStringLiteral(arena, source, lang, 0, len(source))
	if node == nil {
		t.Fatal("node = nil")
	}
	if got, want := node.Type(lang), "expandable_string_literal"; got != want {
		t.Fatalf("node.Type = %q, want %q", got, want)
	}
	if got, want := node.startByte, uint32(0); got != want {
		t.Fatalf("node.startByte = %d, want %d", got, want)
	}
	if got, want := node.endByte, uint32(len(source)); got != want {
		t.Fatalf("node.endByte = %d, want %d", got, want)
	}
	if got, want := len(node.children), 1; got != want {
		t.Fatalf("len(node.children) = %d, want %d", got, want)
	}
	if got, want := node.children[0].Type(lang), "variable"; got != want {
		t.Fatalf("node.children[0].Type = %q, want %q", got, want)
	}
}

func TestBuildPowerShellTypeLiteralMarksRecoveredPlusTailExtra(t *testing.T) {
	lang := &Language{
		Name: "powershell",
		SymbolNames: []string{
			"EOF", "type_literal", "[", "]", "type_spec", "type_name", "type_identifier", ".", "+", "simple_name", "ERROR",
		},
		SymbolMetadata: []SymbolMetadata{
			{Name: "EOF", Visible: false, Named: false},
			{Name: "type_literal", Visible: true, Named: true},
			{Name: "[", Visible: true, Named: false},
			{Name: "]", Visible: true, Named: false},
			{Name: "type_spec", Visible: true, Named: true},
			{Name: "type_name", Visible: true, Named: true},
			{Name: "type_identifier", Visible: true, Named: true},
			{Name: ".", Visible: true, Named: false},
			{Name: "+", Visible: true, Named: false},
			{Name: "simple_name", Visible: true, Named: true},
			{Name: "ERROR", Visible: true, Named: true},
		},
	}

	source := []byte("[System.Environment+SpecialFolder]")
	arena := newNodeArena(arenaClassFull)
	node := buildPowerShellTypeLiteral(arena, source, lang, 0, len(source))
	if node == nil {
		t.Fatal("node = nil")
	}
	if got, want := len(node.children), 4; got != want {
		t.Fatalf("len(node.children) = %d, want %d", got, want)
	}
	if got, want := node.children[2].Type(lang), "ERROR"; got != want {
		t.Fatalf("node.children[2].Type = %q, want %q", got, want)
	}
	if !node.children[2].isExtra {
		t.Fatalf("node.children[2].isExtra = false, want true")
	}
	if !node.HasError() {
		t.Fatalf("node.HasError = false, want true")
	}
}
