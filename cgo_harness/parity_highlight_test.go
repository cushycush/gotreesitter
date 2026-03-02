//go:build cgo && treesitter_c_parity

package cgoharness

import (
	"fmt"
	"sort"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// highlightCapture is a normalized capture for comparison between Go and C.
type highlightCapture struct {
	Name      string
	StartByte uint32
	EndByte   uint32
}

func (h highlightCapture) String() string {
	return fmt.Sprintf("@%s [%d-%d]", h.Name, h.StartByte, h.EndByte)
}

// --- Three-tier highlight parity tracking ---
//
// Tier 1: curatedHighlightLanguages (in parity_cgo_test.go) — merge-blocking. These
//         use knownHighlightDivergence / knownHighlightGoOnly as strict
//         thresholds. Any regression fails the test.
//
// Tier 2: knownDegradedHighlight — "no new degradations" list. Non-curated
//         languages with known parity gaps. This list can shrink but not grow.
//         New languages appearing here means a regression.
//
// Tier 3: All other languages — diagnostic only (logged, never blocking).

// knownHighlightDivergence tracks C-only capture counts for curated languages
// with strict custom thresholds.
var knownHighlightDivergence = map[string]int{
	"julia": 1, // keyword.import "end" in module_definition alternation
}

// knownHighlightGoOnly tracks Go-only capture counts for curated languages with
// strict custom thresholds.
var knownHighlightGoOnly = map[string]int{
	// All curated languages currently have 0 Go-only captures.
}

// highlightTolerance records maximum tolerated divergence counts.
type highlightTolerance struct {
	cMissing int
	goOnly   int
}

// knownDegradedHighlight is the "no new degradations" list for non-curated
// languages. Each entry records the maximum tolerated (cMissing, goOnly).
// This list can shrink (as fixes land) but must not grow (regressions block).
var knownDegradedHighlight = map[string]highlightTolerance{
	"astro":         {cMissing: 4, goOnly: 6},
	"authzed":       {cMissing: 1},
	"awk":           {cMissing: 1, goOnly: 1},
	"bicep":         {goOnly: 1},
	"bitbake":       {cMissing: 1},
	"blade":         {cMissing: 1},
	"cairo":         {cMissing: 2},
	"chatito":       {goOnly: 5},
	"cmake":         {cMissing: 1},
	"cylc":          {cMissing: 3},
	"diff":          {cMissing: 11},
	"disassembly":   {goOnly: 1},
	"eex":           {cMissing: 1},
	"enforce":       {cMissing: 3, goOnly: 1},
	"fish":          {cMissing: 2},
	"git_config":    {cMissing: 1, goOnly: 1},
	"git_rebase":    {cMissing: 6},
	"gitattributes": {cMissing: 1},
	"gitcommit":     {cMissing: 2},
	"glsl":          {cMissing: 2},
	"gomod":         {cMissing: 1},
	"hare":          {cMissing: 3, goOnly: 1},
	"haskell":       {cMissing: 2},
	"haxe":          {cMissing: 4},
	"http":          {cMissing: 5},
	"jsonnet":       {cMissing: 1},
	"kconfig":       {cMissing: 2},
	"less":          {cMissing: 2},
	"linkerscript":  {cMissing: 2},
	"llvm":          {cMissing: 1, goOnly: 1},
	"luau":          {cMissing: 2},
	"mermaid":       {cMissing: 6},
	"nginx":         {cMissing: 2},
	"nim":           {goOnly: 4},
	"ninja":         {cMissing: 3},
	"objc":          {cMissing: 4},
	"odin":          {cMissing: 2},
	"org":           {cMissing: 4},
	"perl":          {goOnly: 4},
	"purescript":    {goOnly: 1},
	"requirements":  {goOnly: 1},
	"robot":         {cMissing: 3, goOnly: 1},
	"smithy":        {goOnly: 1},
	"squirrel":      {cMissing: 3},
	"ssh_config":    {cMissing: 5},
	"thrift":        {cMissing: 1},
	"tlaplus":       {cMissing: 1, goOnly: 2},
	"todotxt":       {cMissing: 2},
	"uxntal":        {cMissing: 4},
	"zig":           {goOnly: 2},
}

// collectGoHighlightCaptures runs a highlight query against a Go parse tree
// and returns sorted, deduplicated captures.
func collectGoHighlightCaptures(t *testing.T, lang *gotreesitter.Language, tree *gotreesitter.Tree, queryStr string, source []byte) []highlightCapture {
	t.Helper()

	q, err := gotreesitter.NewQuery(queryStr, lang)
	if err != nil {
		t.Fatalf("Go NewQuery error: %v", err)
	}

	matches := q.Execute(tree)
	var caps []highlightCapture
	for _, m := range matches {
		for _, c := range m.Captures {
			caps = append(caps, highlightCapture{
				Name:      c.Name,
				StartByte: c.Node.StartByte(),
				EndByte:   c.Node.EndByte(),
			})
		}
	}
	return deduplicateCaptures(caps)
}

// collectCHighlightCaptures runs the same highlight query against a C parse tree
// and returns sorted, deduplicated captures.
func collectCHighlightCaptures(t *testing.T, cLang *sitter.Language, cTree *sitter.Tree, queryStr string, source []byte) []highlightCapture {
	t.Helper()

	cQuery, qErr := sitter.NewQuery(cLang, queryStr)
	if qErr != nil {
		t.Fatalf("C NewQuery error: %v", qErr)
	}
	defer cQuery.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	cRoot := cTree.RootNode()
	matches := cursor.Matches(cQuery, cRoot, source)

	captureNames := cQuery.CaptureNames()

	var caps []highlightCapture
	for {
		m := matches.Next()
		if m == nil {
			break
		}
		for _, c := range m.Captures {
			name := ""
			if int(c.Index) < len(captureNames) {
				name = captureNames[c.Index]
			}
			caps = append(caps, highlightCapture{
				Name:      name,
				StartByte: uint32(c.Node.StartByte()),
				EndByte:   uint32(c.Node.EndByte()),
			})
		}
	}
	return deduplicateCaptures(caps)
}

// deduplicateCaptures sorts captures by (start, end, name) and removes exact duplicates.
func deduplicateCaptures(caps []highlightCapture) []highlightCapture {
	sort.Slice(caps, func(i, j int) bool {
		if caps[i].StartByte != caps[j].StartByte {
			return caps[i].StartByte < caps[j].StartByte
		}
		if caps[i].EndByte != caps[j].EndByte {
			return caps[i].EndByte < caps[j].EndByte
		}
		return caps[i].Name < caps[j].Name
	})
	out := caps[:0]
	for i, c := range caps {
		if i > 0 && c == caps[i-1] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// runHighlightParity runs highlight capture comparison for a single language.
// Returns (goOnlyCount, cMissingCount).
func runHighlightParity(t *testing.T, tc parityCase) (goOnlyCount, cMissingCount int) {
	t.Helper()

	entry, ok := parityEntriesByName[tc.name]
	if !ok {
		t.Skipf("no registry entry for %q", tc.name)
	}
	queryStr := entry.HighlightQuery
	if queryStr == "" {
		t.Skipf("no highlight query for %q", tc.name)
	}

	src := normalizedSource(tc.name, tc.source)

	// --- Go side ---
	goTree, goLang, err := parseWithGo(tc, src, nil)
	if err != nil {
		t.Fatalf("Go parse error: %v", err)
	}

	goCaps := collectGoHighlightCaptures(t, goLang, goTree, queryStr, src)

	// --- C side ---
	cLang, err := ParityCLanguage(tc.name)
	if err != nil {
		if skipReason := parityReferenceSkipReason(err); skipReason != "" {
			t.Skipf("skip C reference: %s", skipReason)
		}
		t.Fatalf("load C parser: %v", err)
	}

	cParser := sitter.NewParser()
	defer cParser.Close()
	if err := cParser.SetLanguage(cLang); err != nil {
		if skipReason := parityReferenceSkipReason(err); skipReason != "" {
			t.Skipf("skip C SetLanguage: %s", skipReason)
		}
		t.Fatalf("C SetLanguage: %v", err)
	}
	cTree := cParser.Parse(src, nil)
	if cTree == nil {
		t.Fatal("C parser returned nil tree")
	}
	defer cTree.Close()

	_, cQueryErr := sitter.NewQuery(cLang, queryStr)
	if cQueryErr != nil {
		t.Skipf("C query compilation error (ABI mismatch): %v", cQueryErr)
	}

	cCaps := collectCHighlightCaptures(t, cLang, cTree, queryStr, src)

	// --- Compare ---
	onlyGo, onlyC := diffCaptures(goCaps, cCaps)

	for _, c := range onlyGo {
		t.Logf("  Go-only: %s %q", c, textSlice(src, c))
	}
	for _, c := range onlyC {
		t.Logf("  C-only: %s %q", c, textSlice(src, c))
	}

	if len(onlyGo) == 0 && len(onlyC) == 0 {
		t.Logf("HIGHLIGHT PARITY OK: %d captures match", len(goCaps))
	} else {
		t.Logf("highlight parity: %d match, %d Go-only, %d C-only",
			len(goCaps)-len(onlyGo), len(onlyGo), len(onlyC))
	}

	return len(onlyGo), len(onlyC)
}

// TestParityHighlight is the merge-blocking highlight parity test for
// curated languages. Failures here block CI.
func TestParityHighlight(t *testing.T) {
	for _, tc := range parityCases {
		if !curatedHighlightLanguages[tc.name] {
			continue
		}
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			goOnlyCount, cMissingCount := runHighlightParity(t, tc)
			goThresh, cThresh := highlightThresholdsForLanguage(tc.name)

			// Check Go-only (false positives) against curated threshold.
			if goOnlyCount > goThresh {
				t.Errorf("Go-only captures: %d (threshold %d, %d new)",
					goOnlyCount, goThresh, goOnlyCount-goThresh)
			} else if goOnlyCount == 0 && goThresh > 0 {
				t.Logf("IMPROVED: Go-only was %d, now 0 — lower threshold for %q", goThresh, tc.name)
			}

			// Check C-missing against curated threshold.
			if cMissingCount > cThresh {
				t.Errorf("C-missing captures: %d (threshold %d, %d new)",
					cMissingCount, cThresh, cMissingCount-cThresh)
			} else if cMissingCount == 0 && cThresh > 0 {
				t.Logf("IMPROVED: C-missing was %d, now 0 — lower threshold for %q", cThresh, tc.name)
			}
		})
	}
}

func highlightThresholdsForLanguage(name string) (goOnly, cMissing int) {
	if v, ok := knownHighlightGoOnly[name]; ok {
		goOnly = v
	}
	if v, ok := knownHighlightDivergence[name]; ok {
		cMissing = v
	}
	if tol, ok := knownDegradedHighlight[name]; ok {
		if tol.goOnly > goOnly {
			goOnly = tol.goOnly
		}
		if tol.cMissing > cMissing {
			cMissing = tol.cMissing
		}
	}
	return goOnly, cMissing
}

// TestParityHighlightAllGrammars runs highlight parity across all 206 languages
// as a diagnostic. Non-curated languages use the knownDegradedHighlight list:
// regressions (worse than recorded) fail; improvements are logged.
func TestParityHighlightAllGrammars(t *testing.T) {
	for _, tc := range parityCases {
		if curatedHighlightLanguages[tc.name] {
			continue // tested by TestParityHighlight
		}
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			goOnlyCount, cMissingCount := runHighlightParity(t, tc)

			tol, isDegraded := knownDegradedHighlight[tc.name]

			if goOnlyCount == 0 && cMissingCount == 0 {
				if isDegraded {
					t.Logf("IMPROVED to full parity — remove %q from knownDegradedHighlight", tc.name)
				}
				return
			}

			if !isDegraded {
				// New degradation: this language was clean but now has issues.
				t.Errorf("NEW DEGRADATION: Go-only=%d C-missing=%d — add %q to knownDegradedHighlight",
					goOnlyCount, cMissingCount, tc.name)
				return
			}

			// Check for regressions against known tolerance.
			if goOnlyCount > tol.goOnly {
				t.Errorf("Go-only REGRESSION: %d (tolerance %d)", goOnlyCount, tol.goOnly)
			}
			if cMissingCount > tol.cMissing {
				t.Errorf("C-missing REGRESSION: %d (tolerance %d)", cMissingCount, tol.cMissing)
			}

			// Log improvements.
			if goOnlyCount < tol.goOnly {
				t.Logf("Go-only improved: %d (was %d)", goOnlyCount, tol.goOnly)
			}
			if cMissingCount < tol.cMissing {
				t.Logf("C-missing improved: %d (was %d)", cMissingCount, tol.cMissing)
			}
		})
	}
}

func textSlice(src []byte, c highlightCapture) string {
	if c.StartByte < uint32(len(src)) && c.EndByte <= uint32(len(src)) {
		s := string(src[c.StartByte:c.EndByte])
		if len(s) > 40 {
			return s[:40] + "..."
		}
		return s
	}
	return ""
}

// diffCaptures returns captures only in Go and only in C.
func diffCaptures(goCaps, cCaps []highlightCapture) (onlyGo, onlyC []highlightCapture) {
	type capKey struct {
		Name      string
		StartByte uint32
		EndByte   uint32
	}
	goSet := make(map[capKey]bool, len(goCaps))
	for _, c := range goCaps {
		goSet[capKey{c.Name, c.StartByte, c.EndByte}] = true
	}
	cSet := make(map[capKey]bool, len(cCaps))
	for _, c := range cCaps {
		cSet[capKey{c.Name, c.StartByte, c.EndByte}] = true
	}

	for _, c := range goCaps {
		if !cSet[capKey{c.Name, c.StartByte, c.EndByte}] {
			onlyGo = append(onlyGo, c)
		}
	}
	for _, c := range cCaps {
		if !goSet[capKey{c.Name, c.StartByte, c.EndByte}] {
			onlyC = append(onlyC, c)
		}
	}
	return
}
