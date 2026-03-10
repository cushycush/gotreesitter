package grammars

import (
	"testing"

	ts "github.com/odvcencio/gotreesitter"
)

func TestDartLibraryDirectiveRecoversMissingName(t *testing.T) {
	src := []byte("library;\n")
	parser := ts.NewParser(DartLanguage())
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("missing root node")
	}
	if tree.ParseStopReason() != ts.ParseStopAccepted {
		t.Fatalf("stop=%s runtime=%s", tree.ParseStopReason(), tree.ParseRuntime().Summary())
	}
	if !root.HasError() {
		t.Fatalf("expected missing-name recovery to retain error flag, got %s", root.SExpr(DartLanguage()))
	}
	if got := root.NamedChildCount(); got != 1 {
		t.Fatalf("named child count = %d, want 1; tree=%s", got, root.SExpr(DartLanguage()))
	}
	libraryName := root.NamedChild(0)
	if libraryName == nil || libraryName.Type(DartLanguage()) != "library_name" {
		t.Fatalf("first named child = %v, want library_name; tree=%s", libraryName, root.SExpr(DartLanguage()))
	}
	if got := libraryName.NamedChildCount(); got != 1 {
		t.Fatalf("library_name named child count = %d, want 1", got)
	}
	dotted := libraryName.NamedChild(0)
	if dotted == nil || dotted.Type(DartLanguage()) != "dotted_identifier_list" {
		t.Fatalf("library_name named child = %v, want dotted_identifier_list", dotted)
	}
	if got := dotted.NamedChildCount(); got != 1 {
		t.Fatalf("dotted_identifier_list named child count = %d, want 1", got)
	}
	ident := dotted.NamedChild(0)
	if ident == nil || ident.Type(DartLanguage()) != "identifier" {
		t.Fatalf("dotted_identifier_list named child = %v, want identifier", ident)
	}
	if !ident.IsMissing() {
		t.Fatalf("identifier should be missing; tree=%s", root.SExpr(DartLanguage()))
	}
}

func TestDartGetterCallWithIfNullArgumentParsesWithoutError(t *testing.T) {
	src := []byte("base class Parser implements Finalizable {\n  List<int> get contents => f(_contents ?? '');\n}\n")
	parser := ts.NewParser(DartLanguage())
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("missing root node")
	}
	if tree.ParseStopReason() != ts.ParseStopAccepted {
		t.Fatalf("stop=%s runtime=%s", tree.ParseStopReason(), tree.ParseRuntime().Summary())
	}
	if root.HasError() {
		t.Fatalf("expected getter call with ?? argument to parse cleanly, got %s", root.SExpr(DartLanguage()))
	}
	if got, want := root.Type(DartLanguage()), "program"; got != want {
		t.Fatalf("root type = %q, want %q", got, want)
	}
	if got, want := root.NamedChildCount(), 1; got != want {
		t.Fatalf("named child count = %d, want %d; tree=%s", got, want, root.SExpr(DartLanguage()))
	}
	classDef := root.NamedChild(0)
	if classDef == nil || classDef.Type(DartLanguage()) != "class_definition" {
		t.Fatalf("first named child = %v, want class_definition; tree=%s", classDef, root.SExpr(DartLanguage()))
	}
}

func TestDartNestedTypeArgumentsBeforeArgumentsParseAsSelectorCall(t *testing.T) {
	src := []byte("base class Parser implements Finalizable {\n  late final c = z.lookup<T<U>>(arg);\n}\n")
	parser := ts.NewParser(DartLanguage())
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("missing root node")
	}
	if tree.ParseStopReason() != ts.ParseStopAccepted {
		t.Fatalf("stop=%s runtime=%s", tree.ParseStopReason(), tree.ParseRuntime().Summary())
	}
	if root.HasError() {
		t.Fatalf("expected generic call canary to parse cleanly, got %s", root.SExpr(DartLanguage()))
	}
	classDef := root.NamedChild(0)
	if classDef == nil || classDef.Type(DartLanguage()) != "class_definition" {
		t.Fatalf("first named child = %v, want class_definition; tree=%s", classDef, root.SExpr(DartLanguage()))
	}
	body := classDef.ChildByFieldName("body", DartLanguage())
	if body == nil {
		t.Fatalf("class body missing; tree=%s", root.SExpr(DartLanguage()))
	}
	if body.NamedChildCount() == 0 {
		t.Fatalf("class body has no named children; tree=%s", root.SExpr(DartLanguage()))
	}
	decl := body.NamedChild(0)
	if decl == nil {
		t.Fatalf("class declaration missing; tree=%s", root.SExpr(DartLanguage()))
	}
	initList := decl.NamedChild(1)
	if initList == nil || initList.Type(DartLanguage()) != "initialized_identifier_list" {
		t.Fatalf("initialized list = %v; tree=%s", initList, root.SExpr(DartLanguage()))
	}
	init := initList.NamedChild(0)
	if init == nil || init.Type(DartLanguage()) != "initialized_identifier" {
		t.Fatalf("initialized identifier = %v; tree=%s", init, root.SExpr(DartLanguage()))
	}
	if got, want := init.NamedChildCount(), 4; got != want {
		t.Fatalf("initialized identifier named child count = %d, want %d; tree=%s", got, want, root.SExpr(DartLanguage()))
	}
	propertySel := init.NamedChild(2)
	if propertySel == nil || propertySel.Type(DartLanguage()) != "selector" {
		t.Fatalf("property selector = %v, want selector; tree=%s", propertySel, root.SExpr(DartLanguage()))
	}
	selector := init.NamedChild(3)
	if selector == nil || selector.Type(DartLanguage()) != "selector" {
		t.Fatalf("call selector = %v, want selector; tree=%s", selector, root.SExpr(DartLanguage()))
	}
	if got, want := selector.NamedChildCount(), 1; got != want {
		t.Fatalf("call selector named child count = %d, want %d; tree=%s", got, want, root.SExpr(DartLanguage()))
	}
	argPart := selector.NamedChild(0)
	if argPart == nil || argPart.Type(DartLanguage()) != "argument_part" {
		t.Fatalf("argument part = %v, want argument_part; tree=%s", argPart, root.SExpr(DartLanguage()))
	}
}

func TestDartSingleTypeArgumentFreeCallRemainsRelationalExpression(t *testing.T) {
	src := []byte("class CancelToken {\n  final _token = calloc<Size>(1);\n}\n")
	parser := ts.NewParser(DartLanguage())
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("missing root node")
	}
	if tree.ParseStopReason() != ts.ParseStopAccepted {
		t.Fatalf("stop=%s runtime=%s", tree.ParseStopReason(), tree.ParseRuntime().Summary())
	}
	if root.HasError() {
		t.Fatalf("expected single-type free call to parse cleanly, got %s", root.SExpr(DartLanguage()))
	}
	classDef := root.NamedChild(0)
	if classDef == nil || classDef.Type(DartLanguage()) != "class_definition" {
		t.Fatalf("first named child = %v, want class_definition; tree=%s", classDef, root.SExpr(DartLanguage()))
	}
	body := classDef.ChildByFieldName("body", DartLanguage())
	if body == nil || body.NamedChildCount() == 0 {
		t.Fatalf("class body missing; tree=%s", root.SExpr(DartLanguage()))
	}
	decl := body.NamedChild(0)
	if decl == nil {
		t.Fatalf("class declaration missing; tree=%s", root.SExpr(DartLanguage()))
	}
	initList := decl.NamedChild(1)
	if initList == nil || initList.Type(DartLanguage()) != "initialized_identifier_list" {
		t.Fatalf("initialized list = %v; tree=%s", initList, root.SExpr(DartLanguage()))
	}
	init := initList.NamedChild(0)
	if init == nil || init.Type(DartLanguage()) != "initialized_identifier" {
		t.Fatalf("initialized identifier = %v; tree=%s", init, root.SExpr(DartLanguage()))
	}
	if got, want := init.NamedChildCount(), 2; got != want {
		t.Fatalf("initialized identifier named child count = %d, want %d; tree=%s", got, want, root.SExpr(DartLanguage()))
	}
	value := init.NamedChild(1)
	if value == nil || value.Type(DartLanguage()) != "relational_expression" {
		t.Fatalf("value = %v, want relational_expression; tree=%s", value, root.SExpr(DartLanguage()))
	}
	if got, want := value.NamedChildCount(), 3; got != want {
		t.Fatalf("value named child count = %d, want %d; tree=%s", got, want, root.SExpr(DartLanguage()))
	}
	left := value.NamedChild(0)
	if left == nil || left.Type(DartLanguage()) != "relational_expression" {
		t.Fatalf("left child = %v, want relational_expression; tree=%s", left, root.SExpr(DartLanguage()))
	}
	if got, want := left.NamedChildCount(), 3; got != want {
		t.Fatalf("left named child count = %d, want %d; tree=%s", got, want, root.SExpr(DartLanguage()))
	}
	typeName := left.NamedChild(2)
	if typeName == nil || typeName.Type(DartLanguage()) != "identifier" {
		t.Fatalf("type argument child = %v, want identifier; tree=%s", typeName, root.SExpr(DartLanguage()))
	}
}

func TestDartConstructorNamedLikeClassBuildsConstructorSignature(t *testing.T) {
	src := []byte("base class QueryCursor {\n  QueryCursor() {}\n}\n")
	parser := ts.NewParser(DartLanguage())
	tree, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	root := tree.RootNode()
	if root == nil {
		t.Fatal("missing root node")
	}
	if tree.ParseStopReason() != ts.ParseStopAccepted {
		t.Fatalf("stop=%s runtime=%s", tree.ParseStopReason(), tree.ParseRuntime().Summary())
	}
	if root.HasError() {
		t.Fatalf("expected constructor to parse cleanly, got %s", root.SExpr(DartLanguage()))
	}
	classDef := root.NamedChild(0)
	if classDef == nil || classDef.Type(DartLanguage()) != "class_definition" {
		t.Fatalf("first named child = %v, want class_definition; tree=%s", classDef, root.SExpr(DartLanguage()))
	}
	body := classDef.ChildByFieldName("body", DartLanguage())
	if body == nil || body.NamedChildCount() == 0 {
		t.Fatalf("class body missing; tree=%s", root.SExpr(DartLanguage()))
	}
	methodSig := body.NamedChild(0)
	if methodSig == nil || methodSig.Type(DartLanguage()) != "method_signature" {
		t.Fatalf("method signature = %v; tree=%s", methodSig, root.SExpr(DartLanguage()))
	}
	sig := methodSig.NamedChild(0)
	if sig == nil || sig.Type(DartLanguage()) != "constructor_signature" {
		t.Fatalf("signature = %v, want constructor_signature; tree=%s", sig, root.SExpr(DartLanguage()))
	}
}
