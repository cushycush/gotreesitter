package grammargen

import (
	"os"
	"testing"
	"time"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func TestCSharpInterfaceDefaultMethodInvocationParity(t *testing.T) {
	genLang := loadGeneratedCSharpLanguageForParity(t)
	refLang := grammars.CSharpLanguage()
	adaptExternalScanner(refLang, genLang)

	sample := "interface MyDefault {\n" +
		"  void Log(string message) {\n" +
		"    Console.WriteLine(message);\n" +
		"  }\n" +
		"}\n"

	assertGeneratedAndReferenceDeepParity(t, genLang, refLang, sample)
}

func TestCSharpContextualFileInvocationParity(t *testing.T) {
	genLang := loadGeneratedCSharpLanguageForParity(t)
	refLang := grammars.CSharpLanguage()
	adaptExternalScanner(refLang, genLang)

	sample := "file.Method(1, 2);\n"
	assertGeneratedAndReferenceDeepParity(t, genLang, refLang, sample)
}

func TestCSharpQueryJoinClauseParity(t *testing.T) {
	genLang := loadGeneratedCSharpLanguageForParity(t)
	refLang := grammars.CSharpLanguage()
	adaptExternalScanner(refLang, genLang)

	sample := "class C\n" +
		"{\n" +
		"    void M()\n" +
		"    {\n" +
		"        var x = from a in sourceA\n" +
		"                join b in sourceB on a.FK equals b.PK\n" +
		"                select a;\n" +
		"    }\n" +
		"}\n"

	assertGeneratedAndReferenceDeepParity(t, genLang, refLang, sample)
}

func TestCSharpQuerySyntaxClauseParity(t *testing.T) {
	genLang := loadGeneratedCSharpLanguageForParity(t)
	refLang := grammars.CSharpLanguage()
	adaptExternalScanner(refLang, genLang)

	cases := []struct {
		name string
		src  string
	}{
		{
			name: "select_conditional",
			src:  "var x = from a in source select a.B() ? c : c * 2;\n",
		},
		{
			name: "select_assignment",
			src:  "var x = from a in source select somevar = a;\n",
		},
		{
			name: "select_anonymous_object",
			src:  "var x = from a in source select new { Name = a.B };\n",
		},
		{
			name: "where_clause",
			src: "var x = from a in source\n" +
				"  where a.B == \"A\"\n" +
				"  select new { Name = a.B };\n",
		},
		{
			name: "order_by_clause",
			src: "var x = from a in source\n" +
				"  orderby a.A descending\n" +
				"  orderby a.C ascending\n" +
				"  orderby 1\n" +
				"  select a;\n",
		},
		{
			name: "let_clause",
			src: "var x = from a in source\n" +
				"  let z = new { a.A, a.B }\n" +
				"  select z;\n",
		},
		{
			name: "nested_from_clause",
			src: "var x = from a in sourceA\n" +
				"  from b in sourceB\n" +
				"  where a.FK == b.FK\n" +
				"  select new { A.A, B.B };\n",
		},
		{
			name: "group_into_clause",
			src: "var x = from a in sourceA\n" +
				"  group a by a.Country into g\n" +
				"  select new { Country = g.Key, Population = g.Sum(p => p.Population) };\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGeneratedAndReferenceDeepParity(t, genLang, refLang, tc.src)
		})
	}
}

func TestCSharpConstrainedTypeDeclarationParity(t *testing.T) {
	genLang := loadGeneratedCSharpLanguageForParity(t)
	refLang := grammars.CSharpLanguage()
	adaptExternalScanner(refLang, genLang)

	cases := []struct {
		name string
		src  string
	}{
		{
			name: "class_constraint",
			src:  "public class F<T> where T:struct {}\n",
		},
		{
			name: "struct_constraint",
			src:  "public struct F<T> where T:struct {}\n",
		},
		{
			name: "record_constraints",
			src:  "private record F<T1, T2> where T1 : I1, I2, new() where T2 : I2 { }\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGeneratedAndReferenceDeepParity(t, genLang, refLang, tc.src)
		})
	}
}

func TestCSharpSourceFileStructureParity(t *testing.T) {
	genLang := loadGeneratedCSharpLanguageForParity(t)
	refLang := grammars.CSharpLanguage()
	adaptExternalScanner(refLang, genLang)

	cases := []struct {
		name string
		src  string
	}{
		{
			name: "namespace_with_using",
			src: "namespace Foo {\n" +
				"  using A;\n" +
				"}\n",
		},
		{
			name: "extern_alias_then_namespace",
			src: "extern alias A;\n" +
				"namespace Foo {\n" +
				"  using A;\n" +
				"}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGeneratedAndReferenceDeepParity(t, genLang, refLang, tc.src)
		})
	}
}

func TestCSharpTopLevelChunkParity(t *testing.T) {
	genLang := loadGeneratedCSharpLanguageForParity(t)
	refLang := grammars.CSharpLanguage()
	adaptExternalScanner(refLang, genLang)

	cases := []struct {
		name string
		src  string
	}{
		{
			name: "extern_usings_namespace",
			src: "extern alias A;\n" +
				"// alias comment\n" +
				"using System;\n" +
				"// using comment\n" +
				"using static System.Console;\n" +
				"namespace Foo {\n" +
				"  using A;\n" +
				"}\n",
		},
		{
			name: "multiple_top_level_classes",
			src: "public class F {}\n" +
				"public class G<T> where T:struct {}\n" +
				"file class A {}\n" +
				"public class NoBody;\n",
		},
		{
			name: "globals_then_class",
			src: "(string a, bool b) c = default;\n" +
				"A<B> a = null;\n" +
				"class A {\n" +
				"  int Sample() {\n" +
				"    return 1;\n" +
				"  }\n" +
				"}\n",
		},
		{
			name: "global_lambda_chain",
			src:  "var result = list.Select(c => (c.f1, c.f2)).Where(t => t.f2 == 1);\n",
		},
		{
			name: "top_level_local_function_switch_patterns",
			src: "int Sample9(int a) {\n" +
				"  switch (a, a) {\n" +
				"    case (1, 1):\n" +
				"      return 1;\n" +
				"    default:\n" +
				"      return 0;\n" +
				"  }\n" +
				"\n" +
				"  switch (A, B) {\n" +
				"      case (_, _) when !c:\n" +
				"        break;\n" +
				"  }\n" +
				"\n" +
				"  switch (A) {\n" +
				"      case {Length: 2} when !c:\n" +
				"        break;\n" +
				"  }\n" +
				"\n" +
				"  int i = 123;\n" +
				"  switch (i)\n" +
				"  {\n" +
				"      case int when i < 5:\n" +
				"          break;\n" +
				"  }\n" +
				"}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGeneratedAndReferenceDeepParity(t, genLang, refLang, tc.src)
		})
	}
}

func TestCSharpAttributeDeclarationParity(t *testing.T) {
	genLang := loadGeneratedCSharpLanguageForParity(t)
	refLang := grammars.CSharpLanguage()
	adaptExternalScanner(refLang, genLang)

	cases := []struct {
		name string
		src  string
	}{
		{
			name: "attribute_member_access_argument",
			src:  "[A(B.C)] class D {}\n",
		},
		{
			name: "qualified_attribute_member_access_argument",
			src:  "[NS.A(B.C)] class D {}\n",
		},
		{
			name: "stacked_attribute_lists",
			src: "[One][Two]\n" +
				"[Three]\n" +
				"class A { }\n",
		},
		{
			name: "multi_attribute_struct",
			src:  "[A,B()][C] struct A { }\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGeneratedAndReferenceDeepParity(t, genLang, refLang, tc.src)
		})
	}
}

func TestCSharpTypeDeclarationBodyParity(t *testing.T) {
	genLang := loadGeneratedCSharpLanguageForParity(t)
	refLang := grammars.CSharpLanguage()
	adaptExternalScanner(refLang, genLang)

	cases := []struct {
		name string
		src  string
	}{
		{
			name: "using_declaration_method",
			src: "class A {\n" +
				"  void Sample() {\n" +
				"    using var a = new A();\n" +
				"  }\n" +
				"}\n",
		},
		{
			name: "local_function_tuple_method",
			src: "class A {\n" +
				"  void Sample() {\n" +
				"    (bool a, bool b) M2() {\n" +
				"      return (true, false);\n" +
				"    }\n" +
				"  }\n" +
				"}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGeneratedAndReferenceDeepParity(t, genLang, refLang, tc.src)
		})
	}
}

func TestGeneratedCSharpTypeDeclarationBodyRecovery(t *testing.T) {
	genLang := loadGeneratedCSharpLanguageForParity(t)
	refLang := grammars.CSharpLanguage()
	adaptExternalScanner(refLang, genLang)
	parser := gotreesitter.NewParser(genLang)

	cases := []struct {
		name     string
		src      string
		wantStmt string
	}{
		{
			name: "initializers_prefix_method",
			src: "class A {\n" +
				"  void Sample() {\n" +
				"    int a;\n" +
				"    int a = 1, b = 2;\n" +
				"    const int a = 1;\n" +
				"    const int a = 1, b = 2;\n" +
				"    ref var value = ref data[i];\n" +
				"    var g = args[0].Length;\n" +
				"  }\n" +
				"}\n",
			wantStmt: "local_declaration_statement",
		},
		{
			name: "using_prefix_method",
			src: "class A {\n" +
				"  void Sample() {\n" +
				"    using (var a = b) {\n" +
				"      return;\n" +
				"    }\n" +
				"\n" +
				"    using (Stream a = File.OpenRead(\"a\"), b = new BinaryReader(a)) {\n" +
				"      return;\n" +
				"    }\n" +
				"  }\n" +
				"}\n",
			wantStmt: "using_statement",
		},
		{
			name: "variable_declarations_prefix_method",
			src: "class A\n" +
				"{\n" +
				"    public void M()\n" +
				"    {\n" +
				"        foreach (int i in new[] { 1 })\n" +
				"        {\n" +
				"            int j = i;\n" +
				"        }\n" +
				"\n" +
				"        var x = from a in sourceA\n" +
				"                join b in sourceB on a.FK equals b.PK\n" +
				"                group a by a.X into g\n" +
				"                orderby g ascending\n" +
				"                select new { A.A, B.B };\n" +
				"    }\n" +
				"}\n",
			wantStmt: "foreach_statement",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := parser.Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			root := tree.RootNode()
			if root == nil || root.HasError() {
				t.Fatalf("expected no-error tree, got %s", root.SExpr(genLang))
			}
			if got := findFirstNamedDescendantOfType(root, genLang, "class_declaration"); got == nil {
				t.Fatalf("missing class_declaration: %s", root.SExpr(genLang))
			}
			method := findFirstNamedDescendantOfType(root, genLang, "method_declaration")
			if method == nil {
				t.Fatalf("missing method_declaration: %s", root.SExpr(genLang))
			}
			if got := findFirstNamedDescendantOfType(method, genLang, tc.wantStmt); got == nil {
				t.Fatalf("missing %s in recovered method: %s", tc.wantStmt, method.SExpr(genLang))
			}
		})
	}
}

func TestCSharpUnicodeIdentifierParity(t *testing.T) {
	genLang := loadGeneratedCSharpLanguageForParity(t)
	refLang := grammars.CSharpLanguage()
	adaptExternalScanner(refLang, genLang)

	sample := "int ග්‍රහලෝකය = 0;\n"
	assertGeneratedAndReferenceDeepParity(t, genLang, refLang, sample)
}

func loadGeneratedCSharpLanguageForParity(t *testing.T) *gotreesitter.Language {
	t.Helper()

	candidates := []string{
		"/tmp/grammar_parity/c_sharp/src/grammar.json",
		".claude/worktrees/grammargen-pr9-resume/harness_out/grammar_seeds/c_sharp/src/grammar.json",
		"../.claude/worktrees/grammargen-pr9-resume/harness_out/grammar_seeds/c_sharp/src/grammar.json",
	}

	var grammarPath string
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			grammarPath = path
			break
		}
	}
	if grammarPath == "" {
		t.Skip("C# grammar.json not available")
	}

	source, err := os.ReadFile(grammarPath)
	if err != nil {
		t.Fatalf("read C# grammar.json: %v", err)
	}
	gram, err := ImportGrammarJSON(source)
	if err != nil {
		t.Fatalf("import C# grammar.json: %v", err)
	}
	genLang, err := generateWithTimeout(gram, 90*time.Second)
	if err != nil {
		t.Fatalf("generate C# language: %v", err)
	}
	return genLang
}

func findFirstNamedDescendantOfType(node *gotreesitter.Node, lang *gotreesitter.Language, typ string) *gotreesitter.Node {
	if node == nil {
		return nil
	}
	if node.IsNamed() && node.Type(lang) == typ {
		return node
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if got := findFirstNamedDescendantOfType(node.NamedChild(i), lang, typ); got != nil {
			return got
		}
	}
	return nil
}
