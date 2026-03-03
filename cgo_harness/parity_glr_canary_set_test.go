//go:build cgo && treesitter_c_parity

package cgoharness

import "testing"

type glrCanaryCase struct {
	lang      string
	name      string
	source    string
	minStacks int
}

var glrCanaryCases = []glrCanaryCase{
	{
		lang:      "go",
		name:      "ambiguous-call",
		source:    "package main\nfunc main(){ println(\"hello\") }\n",
		minStacks: 2,
	},
	{
		lang:      "java",
		name:      "object-method-call",
		source:    "class A { void f() { obj.method(); } }\n",
		minStacks: 2,
	},
	{
		lang:      "c",
		name:      "decl-vs-call",
		source:    "int f(){ T(x); }\n",
		minStacks: 2,
	},
	{
		lang:      "cpp",
		name:      "decl-vs-call",
		source:    "int f(){ T(x); }\n",
		minStacks: 2,
	},
	{
		lang:      "dart",
		name:      "object-method-call",
		source:    "void f(){ obj.method(); }\n",
		minStacks: 2,
	},
	{
		lang:      "purescript",
		name:      "type-vs-term-application",
		source:    "module Main where\nx = Foo Bar\n",
		minStacks: 2,
	},
	{
		lang:      "tsx",
		name:      "jsx-embedded-call",
		source:    "function App(){ return <div>{obj.method()}</div> }\n",
		minStacks: 2,
	},
}

// TestParityGLRCanarySet extends GLR parity canaries beyond Go so merge/pruning
// changes are validated against conflict-heavy real grammars, not only smoke
// inputs that often stay single-stack.
func TestParityGLRCanarySet(t *testing.T) {
	for _, cc := range glrCanaryCases {
		cc := cc
		t.Run(cc.lang+"/"+cc.name, func(t *testing.T) {
			src := normalizedSource(cc.lang, cc.source)
			tc := parityCase{name: cc.lang, source: string(src)}
			runParityCase(t, tc, "glr-canary-"+cc.name, src)
			assertGLRCanaryRuntime(t, tc, src, cc.minStacks)
		})
	}
}
