//go:build cgo && treesitter_c_parity

package cgoharness

import (
	"os"
	"strings"
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"
)

var top50ParityLanguages = []string{
	"bash",
	"c",
	"cpp",
	"c_sharp",
	"cmake",
	"css",
	"dart",
	"elixir",
	"elm",
	"erlang",
	"go",
	"gomod",
	"graphql",
	"haskell",
	"hcl",
	"html",
	"ini",
	"java",
	"javascript",
	"json",
	"json5",
	"julia",
	"kotlin",
	"lua",
	"make",
	"markdown",
	"nix",
	"objc",
	"ocaml",
	"perl",
	"php",
	"powershell",
	"python",
	"r",
	"ruby",
	"rust",
	"scala",
	"scss",
	"sql",
	"svelte",
	"swift",
	"toml",
	"tsx",
	"typescript",
	"xml",
	"yaml",
	"zig",
	"awk",
	"clojure",
	"d",
}

var top50ParitySkips = map[string]string{
	// These are known structural mismatches under dump.v1 parity today.
	// Keep this list explicit so we can burn it down as parser/scanner parity
	// improves without disabling the wider top-50 sweep.
	"d":     "child-count mismatch in return_statement subtree",
	"julia": "range mismatch around module/function trailing block span",
	"scss":  "external scanner produces spurious _descendant_operator inside rule blocks",
}

func top50ParityEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GTS_PARITY_TOP50"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// TestParityTop50FreshParse extends structural C-oracle parity coverage to the
// top-50 language gate. It is opt-in because it compiles many upstream parsers
// and is intentionally heavier than the default parity suite.
func TestParityTop50FreshParse(t *testing.T) {
	if !top50ParityEnabled() {
		t.Skip("set GTS_PARITY_TOP50=1 to enable top-50 C-oracle parity")
	}
	for _, name := range top50ParityLanguages {
		name := name
		t.Run(name, func(t *testing.T) {
			if reason, skip := top50ParitySkips[name]; skip {
				t.Skipf("known mismatch: %s", reason)
			}
			tc := parityCase{name: name, source: grammars.ParseSmokeSample(name)}
			runParityCase(t, tc, "top50-fresh", normalizedSource(tc.name, tc.source))
		})
	}
}
