//go:build cgo && treesitter_c_parity

package cgoharness

import (
	"fmt"
	"os"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	gotreesitter "github.com/odvcencio/gotreesitter"
)


func TestCppNamespaceDiag(t *testing.T) {
	sources := []struct {
		name string
		src  string
	}{
		{"minimal_ns", "namespace foo { int x; }\n"},
		{"nested_ns", "namespace a { namespace b { int x; } }\n"},
		{"include_then_ns", "#include \"foo.h\"\nnamespace bar { int y; }\n"},
		{"ref_return", "namespace a { int &foo(int x) { return x; } }\n"},
		{"ref_param", "namespace a { void foo(const int &x) {} }\n"},
		{"user_type_ref", "struct Foo {};\nnamespace a { Foo &bar() { static Foo f; return f; } }\n"},
		{"scope_ref", "struct Foo { Foo &get(); };\nnamespace a { Foo &Foo::get() { return *this; } }\n"},
		{"op_eq_ns", "struct R {};\nnamespace a { R &R::operator=(const R &o) { return *this; } }\n"},
		{"real_file", ""}, // loaded from corpus
	}

	// Try to load the real file (last entry in slice)
	realPath := "/workspace/harness_out/corpus_real_205_noscala_bounded/cpp/medium__rule.cc"
	if data, err := os.ReadFile(realPath); err == nil {
		sources[len(sources)-1].src = string(data)
	} else {
		t.Logf("real file not available: %v", err)
	}

	entry, ok := parityEntriesByName["cpp"]
	if !ok {
		t.Fatal("cpp not found in language registry")
	}
	goLang := entry.Language()

	cLang, err := ParityCLanguage("cpp")
	if err != nil {
		t.Fatalf("load C parser: %v", err)
	}

	for _, tc := range sources {
		if tc.src == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)

			// Parse with Go
			parser := gotreesitter.NewParser(goLang)

			goTree, parseErr := parser.Parse(src)
			if parseErr != nil {
				t.Fatalf("Go parse error: %v", parseErr)
			}
			goRoot := goTree.RootNode()

			// Parse with C
			cParser := sitter.NewParser()
			defer cParser.Close()
			if err := cParser.SetLanguage(cLang); err != nil {
				t.Fatalf("C SetLanguage: %v", err)
			}
			cTree := cParser.Parse(src, nil)
			if cTree == nil || cTree.RootNode() == nil {
				t.Fatalf("C parse returned nil")
			}
			defer cTree.Close()
			cRoot := cTree.RootNode()

			goRT := goTree.ParseRuntime()
			t.Logf("GO root: type=%q children=%d hasError=%v endByte=%d stop=%s iter=%d/%d nodes=%d/%d tokens=%d truncated=%v",
				goRoot.Type(goLang), goRoot.ChildCount(), goRoot.HasError(), goRoot.EndByte(),
				goRT.StopReason, goRT.Iterations, goRT.IterationLimit,
				goRT.NodesAllocated, goRT.NodeLimit,
				goRT.TokensConsumed, goRT.Truncated)
			t.Logf("C  root: type=%q children=%d hasError=%v endByte=%d",
				cRoot.Kind(), cRoot.ChildCount(), cRoot.HasError(), cRoot.EndByte())

			// Show first-level children comparison
			t.Log("--- GO children ---")
			for i := 0; i < goRoot.ChildCount() && i < 20; i++ {
				ch := goRoot.Child(i)
				t.Logf("  [%d] type=%q named=%v range=[%d,%d] hasError=%v",
					i, ch.Type(goLang), ch.IsNamed(), ch.StartByte(), ch.EndByte(), ch.HasError())
			}
			if goRoot.ChildCount() > 20 {
				t.Logf("  ... and %d more", goRoot.ChildCount()-20)
			}

			t.Log("--- C children ---")
			for i := 0; i < int(cRoot.ChildCount()) && i < 20; i++ {
				ch := cRoot.Child(uint(i))
				t.Logf("  [%d] type=%q named=%v range=[%d,%d] hasError=%v",
					i, ch.Kind(), ch.IsNamed(), ch.StartByte(), ch.EndByte(), ch.HasError())
			}

			// Check parity
			if goRoot.ChildCount() != int(cRoot.ChildCount()) ||
				goRoot.HasError() != cRoot.HasError() ||
				goRoot.Type(goLang) != cRoot.Kind() {
				t.Errorf("DIVERGENCE detected")
			}

			// Show SExpr for small inputs
			if len(src) < 200 {
				t.Logf("GO sexpr: %s", goRoot.SExpr(goLang))
				fmt.Fprintf(os.Stderr, "C  sexpr: %s\n", cRoot.ToSexp())
			}
		})
	}
}
