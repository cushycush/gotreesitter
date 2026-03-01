//go:build cgo && treesitter_c_parity

package cgoharness

import "testing"

// TestParityGLRCanaryGo ensures we keep one adversarial GLR canary in parity
// coverage: force branching, require C-structural parity, and assert no
// truncation/early-stop diagnostics on the Go runtime tree.
func TestParityGLRCanaryGo(t *testing.T) {
	const funcCount = 500
	src := normalizedSource("go", string(makeGoBenchmarkSource(funcCount)))
	tc := parityCase{name: "go", source: string(src)}

	runParityCase(t, tc, "glr-canary", src)

	goTree, goLang, err := parseWithGo(tc, src, nil)
	if err != nil {
		t.Fatalf("[go/glr-canary] gotreesitter parse error: %v", err)
	}
	defer goTree.Release()

	root := goTree.RootNode()
	if root == nil {
		t.Fatalf("[go/glr-canary] nil root")
	}
	if got, want := root.EndByte(), uint32(len(src)); got != want {
		t.Fatalf("[go/glr-canary] root.EndByte=%d want=%d", got, want)
	}

	rt := goTree.ParseRuntime()
	if rt.Truncated || goTree.ParseStoppedEarly() {
		t.Fatalf("[go/glr-canary] unexpected early stop: %s", rt.Summary())
	}
	if root.HasError() {
		t.Fatalf("[go/glr-canary] root has error: type=%q %s", root.Type(goLang), rt.Summary())
	}
	if rt.MaxStacksSeen <= 1 {
		t.Fatalf("[go/glr-canary] expected GLR branching, maxStacks=%d %s", rt.MaxStacksSeen, rt.Summary())
	}
}
