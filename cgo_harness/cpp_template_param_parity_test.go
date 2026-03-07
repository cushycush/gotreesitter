//go:build cgo && treesitter_c_parity

package cgoharness

import "testing"

func TestCppTemplateTypeParameterParity(t *testing.T) {
	src := []byte("void f(Vector<Rule> *elements) {}\n")
	tc := parityCase{name: "cpp", source: string(src)}
	runParityCase(t, tc, "template-type-parameter", src)
}
