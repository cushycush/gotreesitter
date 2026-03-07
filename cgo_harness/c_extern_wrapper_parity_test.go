//go:build cgo && treesitter_c_parity

package cgoharness

import "testing"

func TestCExternCWrapperParity(t *testing.T) {
	src := []byte("#ifdef __cplusplus\nextern \"C\" {\n#endif\n\nint x;\n\n#ifdef __cplusplus\n}\n#endif\n")
	tc := parityCase{name: "c", source: string(src)}
	runParityCase(t, tc, "extern-c-wrapper", src)
}
