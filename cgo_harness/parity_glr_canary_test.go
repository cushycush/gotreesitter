//go:build cgo && treesitter_c_parity

package cgoharness

import (
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

func assertGLRCanaryRuntime(t *testing.T, tc parityCase, src []byte, minStacks int) {
	t.Helper()

	goTree, goLang, err := parseWithGo(tc, src, nil)
	if err != nil {
		t.Fatalf("[%s/%s] gotreesitter parse error: %v", tc.name, "glr-canary", err)
	}
	defer goTree.Release()

	root := goTree.RootNode()
	if root == nil {
		t.Fatalf("[%s/%s] nil root", tc.name, "glr-canary")
	}
	if got, want := root.EndByte(), uint32(len(src)); got != want {
		t.Fatalf("[%s/%s] root.EndByte=%d want=%d", tc.name, "glr-canary", got, want)
	}

	rt := goTree.ParseRuntime()
	if rt.Truncated || goTree.ParseStoppedEarly() {
		t.Fatalf("[%s/%s] unexpected early stop: %s", tc.name, "glr-canary", rt.Summary())
	}
	if rt.StopReason != gotreesitter.ParseStopAccepted {
		t.Fatalf("[%s/%s] stop reason=%s want=%s (%s)",
			tc.name, "glr-canary", rt.StopReason, gotreesitter.ParseStopAccepted, rt.Summary())
	}
	if root.HasError() {
		t.Fatalf("[%s/%s] root has error: type=%q %s", tc.name, "glr-canary", root.Type(goLang), rt.Summary())
	}
	if rt.MaxStacksSeen < minStacks {
		t.Fatalf("[%s/%s] expected GLR branching (min=%d), maxStacks=%d %s",
			tc.name, "glr-canary", minStacks, rt.MaxStacksSeen, rt.Summary())
	}
}

// TestParityGLRCanaryGo ensures we keep one adversarial GLR canary in parity
// coverage: force branching, require C-structural parity, and assert no
// truncation/early-stop diagnostics on the Go runtime tree.
func TestParityGLRCanaryGo(t *testing.T) {
	const funcCount = 500
	src := normalizedSource("go", string(makeGoBenchmarkSource(funcCount)))
	tc := parityCase{name: "go", source: string(src)}

	runParityCase(t, tc, "glr-canary", src)
	assertGLRCanaryRuntime(t, tc, src, 2)
}
