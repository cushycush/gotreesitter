//go:build cgo && treesitter_c_parity

package cgoharness

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// This suite is intentionally opt-in and designed to maximize parity breakage
// discovery before perf tuning. Enable with:
//   GTS_PARITY_BREAKER=1

func parityBreakerEnabled() bool {
	return envBool("GTS_PARITY_BREAKER", false)
}

func envBool(name string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func envInt(name string, def int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func parityBreakerLangFilter() map[string]struct{} {
	raw := strings.TrimSpace(os.Getenv("GTS_PARITY_BREAKER_LANGS"))
	if raw == "" {
		return nil
	}
	out := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

func parityBreakerPinnedLangs() map[string]struct{} {
	raw := strings.TrimSpace(os.Getenv("GTS_PARITY_BREAKER_PIN_LANGS"))
	if raw == "" {
		return nil
	}
	out := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

func applyParityBreakerShard(in []parityCase) []parityCase {
	shards := envInt("GTS_PARITY_BREAKER_SHARDS", 1)
	if shards <= 1 {
		return in
	}
	shardIndex := envInt("GTS_PARITY_BREAKER_SHARD_INDEX", 0)
	if shardIndex < 0 {
		shardIndex = 0
	}
	if shardIndex >= shards {
		shardIndex = shardIndex % shards
	}

	out := make([]parityCase, 0, (len(in)/shards)+1)
	for i, tc := range in {
		if i%shards == shardIndex {
			out = append(out, tc)
		}
	}
	return out
}

func applyParityBreakerCap(in []parityCase, maxLangs int) []parityCase {
	if maxLangs <= 0 || len(in) <= maxLangs {
		return in
	}

	pinned := parityBreakerPinnedLangs()
	if len(pinned) == 0 {
		return in[:maxLangs]
	}

	out := make([]parityCase, 0, maxLangs)
	seen := map[string]struct{}{}
	for _, tc := range in {
		if len(out) >= maxLangs {
			break
		}
		if _, ok := pinned[tc.name]; !ok {
			continue
		}
		if _, exists := seen[tc.name]; exists {
			continue
		}
		out = append(out, tc)
		seen[tc.name] = struct{}{}
	}
	for _, tc := range in {
		if len(out) >= maxLangs {
			break
		}
		if _, exists := seen[tc.name]; exists {
			continue
		}
		out = append(out, tc)
		seen[tc.name] = struct{}{}
	}
	return out
}

func injectPinnedCases(selected []parityCase, universe []parityCase) []parityCase {
	pinned := parityBreakerPinnedLangs()
	if len(pinned) == 0 {
		return selected
	}
	seen := map[string]struct{}{}
	for _, tc := range selected {
		seen[tc.name] = struct{}{}
	}
	out := append([]parityCase{}, selected...)
	for _, tc := range universe {
		if _, ok := pinned[tc.name]; !ok {
			continue
		}
		if _, ok := seen[tc.name]; ok {
			continue
		}
		out = append(out, tc)
		seen[tc.name] = struct{}{}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].name < out[j].name
	})
	return out
}

func TestApplyParityBreakerShard(t *testing.T) {
	t.Setenv("GTS_PARITY_BREAKER_SHARDS", "3")
	t.Setenv("GTS_PARITY_BREAKER_SHARD_INDEX", "1")

	in := []parityCase{
		{name: "a"},
		{name: "b"},
		{name: "c"},
		{name: "d"},
		{name: "e"},
		{name: "f"},
		{name: "g"},
	}
	got := applyParityBreakerShard(in)
	if len(got) != 2 || got[0].name != "b" || got[1].name != "e" {
		t.Fatalf("unexpected shard selection: %+v", got)
	}
}

func TestApplyParityBreakerCapPinned(t *testing.T) {
	t.Setenv("GTS_PARITY_BREAKER_PIN_LANGS", "scala")

	in := []parityCase{
		{name: "ada"},
		{name: "bash"},
		{name: "scala"},
		{name: "zig"},
	}
	got := applyParityBreakerCap(in, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 cases, got=%d", len(got))
	}
	if got[0].name != "scala" {
		t.Fatalf("expected pinned scala first, got=%q", got[0].name)
	}
	if got[1].name != "ada" {
		t.Fatalf("expected lexical fill after pinned, got=%q", got[1].name)
	}
}

func TestInjectPinnedCasesAcrossShards(t *testing.T) {
	t.Setenv("GTS_PARITY_BREAKER_PIN_LANGS", "scala")

	universe := []parityCase{
		{name: "ada"},
		{name: "bash"},
		{name: "go"},
		{name: "scala"},
	}
	shard := []parityCase{
		{name: "ada"},
		{name: "go"},
	}
	got := injectPinnedCases(shard, universe)
	if len(got) != 3 {
		t.Fatalf("expected 3 cases after pin injection, got=%d", len(got))
	}
	if got[0].name != "ada" || got[1].name != "go" || got[2].name != "scala" {
		t.Fatalf("unexpected injected cases: %+v", got)
	}
}

func parityBreakerCases(requireHighlight bool) []parityCase {
	includeDegraded := envBool("GTS_PARITY_BREAKER_INCLUDE_DEGRADED", false)
	langFilter := parityBreakerLangFilter()
	maxLangs := envInt("GTS_PARITY_BREAKER_MAX_LANGS", 0)

	out := make([]parityCase, 0, len(parityCases))
	for _, tc := range parityCases {
		if parityLanguageExcluded(tc.name) {
			continue
		}
		if !curatedStructuralLanguages[tc.name] {
			continue
		}
		if requireHighlight && !curatedHighlightLanguages[tc.name] {
			continue
		}
		if !includeDegraded && paritySkipReason(tc.name) != "" {
			continue
		}
		if langFilter != nil {
			if _, ok := langFilter[tc.name]; !ok {
				continue
			}
		}
		out = append(out, tc)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].name < out[j].name
	})
	universe := append([]parityCase{}, out...)
	out = applyParityBreakerShard(out)
	out = injectPinnedCases(out, universe)
	out = applyParityBreakerCap(out, maxLangs)
	return out
}

func parityBreakerMutationCandidates(src []byte, max int) []incrementalEditCandidate {
	candidates := make([]incrementalEditCandidate, 0, len(src)+32)
	candidates = append(candidates, incrementalEditCandidates(src)...)

	// Add newline insertions at preferred offsets to stress row/column handling.
	for _, pos := range incrementalEditOffsets(src) {
		candidates = append(candidates, incrementalEditCandidate{
			label:       fmt.Sprintf("insert-newline@%d", pos),
			start:       pos,
			oldEnd:      pos,
			replacement: []byte{'\n'},
		})
	}

	// Add whitespace deletions (when present) to stress incremental edit mapping.
	for i, b := range src {
		if b != ' ' && b != '\n' && b != '\t' {
			continue
		}
		candidates = append(candidates, incrementalEditCandidate{
			label:       fmt.Sprintf("delete@%d:%q", i, b),
			start:       i,
			oldEnd:      i + 1,
			replacement: nil,
		})
	}

	deduped := dedupeMutationCandidates(candidates)
	return sampleMutationCandidates(deduped, max)
}

func dedupeMutationCandidates(in []incrementalEditCandidate) []incrementalEditCandidate {
	out := make([]incrementalEditCandidate, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, c := range in {
		key := fmt.Sprintf("%d:%d:%x", c.start, c.oldEnd, c.replacement)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}

func sampleMutationCandidates(in []incrementalEditCandidate, max int) []incrementalEditCandidate {
	if max <= 0 || len(in) <= max {
		return in
	}
	if max == 1 {
		return []incrementalEditCandidate{in[0]}
	}
	out := make([]incrementalEditCandidate, 0, max)
	seen := make(map[int]struct{}, max)
	step := float64(len(in)-1) / float64(max-1)
	for i := 0; i < max; i++ {
		idx := int(math.Round(float64(i) * step))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(in) {
			idx = len(in) - 1
		}
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		out = append(out, in[idx])
	}
	if len(out) == 0 {
		return in[:max]
	}
	return out
}

func structuralParityClean(tc parityCase, src []byte) (clean bool, skip bool, detail string) {
	goTree, goLang, err := parseWithGo(tc, src, nil)
	if err != nil {
		return false, false, fmt.Sprintf("go parse error: %v", err)
	}

	cLang, err := ParityCLanguage(tc.name)
	if err != nil {
		if skipReason := parityReferenceSkipReason(err); skipReason != "" {
			return false, true, skipReason
		}
		return false, false, fmt.Sprintf("load C parser: %v", err)
	}
	cParser := sitter.NewParser()
	defer cParser.Close()
	if err := cParser.SetLanguage(cLang); err != nil {
		if skipReason := parityReferenceSkipReason(err); skipReason != "" {
			return false, true, skipReason
		}
		return false, false, fmt.Sprintf("C SetLanguage: %v", err)
	}
	cTree := cParser.Parse(src, nil)
	if cTree == nil || cTree.RootNode() == nil {
		return false, false, "C parser returned nil tree"
	}
	defer cTree.Close()

	goRoot := goTree.RootNode()
	cRoot := cTree.RootNode()
	if goRoot.HasError() || cRoot.HasError() {
		return false, false, fmt.Sprintf("error nodes go=%v c=%v", goRoot.HasError(), cRoot.HasError())
	}
	var errs []string
	compareNodes(goRoot, goLang, cRoot, "root", &errs)
	if len(errs) > 0 {
		return false, false, errs[0]
	}
	return true, false, ""
}

func TestParityMutationSweepStructural(t *testing.T) {
	if !parityBreakerEnabled() {
		t.Skip("set GTS_PARITY_BREAKER=1 to enable mutation sweep")
	}

	// By default, structural breaker focuses on mutations that are already
	// structurally parity-clean to avoid malformed-input recovery noise.
	// Set GTS_PARITY_BREAKER_STRUCTURAL_STRICT=1 to force strict fail-on-first
	// divergence behavior across all generated mutations.
	strictMode := envBool("GTS_PARITY_BREAKER_STRUCTURAL_STRICT", false)
	maxMutations := envInt("GTS_PARITY_BREAKER_MAX_MUTATIONS", 12)
	cases := parityBreakerCases(false)
	if len(cases) == 0 {
		t.Skip("no parity breaker structural cases selected")
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			src := normalizedSource(tc.name, tc.source)
			candidates := parityBreakerMutationCandidates(src, maxMutations)
			if len(candidates) == 0 {
				t.Skip("no mutation candidates")
			}
			cleanCount := 0
			for i, candidate := range candidates {
				edited := applyEditCandidate(src, candidate)
				label := fmt.Sprintf("breaker-m%02d-%s", i, candidate.label)
				if !strictMode {
					clean, skip, detail := structuralParityClean(tc, edited)
					if skip {
						t.Skipf("skip C reference parser: %s", detail)
					}
					if !clean {
						continue
					}
					cleanCount++
				}
				runParityCase(t, tc, label, edited)
			}
			if !strictMode && cleanCount == 0 {
				t.Skipf("no structurally clean mutations out of %d candidates", len(candidates))
			}
		})
	}
}

func TestParityMutationSweepHighlight(t *testing.T) {
	if !parityBreakerEnabled() {
		t.Skip("set GTS_PARITY_BREAKER=1 to enable mutation sweep")
	}
	if !envBool("GTS_PARITY_BREAKER_HIGHLIGHT", true) {
		t.Skip("set GTS_PARITY_BREAKER_HIGHLIGHT=1 to run highlight mutation sweep")
	}

	maxMutations := envInt("GTS_PARITY_BREAKER_MAX_MUTATIONS", 12)
	cases := parityBreakerCases(true)
	if len(cases) == 0 {
		t.Skip("no parity breaker highlight cases selected")
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			src := normalizedSource(tc.name, tc.source)
			candidates := parityBreakerMutationCandidates(src, maxMutations)
			if len(candidates) == 0 {
				t.Skip("no mutation candidates")
			}

			goThresh, cThresh := breakerHighlightThresholdsForLanguage(tc.name)
			cleanCount := 0
			for i, candidate := range candidates {
				edited := applyEditCandidate(src, candidate)
				clean, skip, detail := structuralParityClean(tc, edited)
				if skip {
					t.Skipf("skip C reference parser: %s", detail)
				}
				if !clean {
					continue
				}
				cleanCount++
				goOnlyCount, cMissingCount := runHighlightParityForSource(t, tc, edited)
				if goOnlyCount > goThresh {
					t.Errorf("[mut=%d:%s] Go-only captures: %d (threshold %d, %d new)",
						i, candidate.label, goOnlyCount, goThresh, goOnlyCount-goThresh)
				}
				if cMissingCount > cThresh {
					t.Errorf("[mut=%d:%s] C-missing captures: %d (threshold %d, %d new)",
						i, candidate.label, cMissingCount, cThresh, cMissingCount-cThresh)
				}
			}

			if cleanCount == 0 {
				t.Skipf("no structurally clean mutations out of %d candidates", len(candidates))
			}
		})
	}
}

// breakerHighlightThresholdsForLanguage allows mutation-sweep-specific
// tolerances without loosening the core highlight parity gate.
func breakerHighlightThresholdsForLanguage(name string) (goOnly, cMissing int) {
	goOnly, cMissing = highlightThresholdsForLanguage(name)
	if tol, ok := breakerMutationHighlightTolerance[name]; ok {
		if tol.goOnly > goOnly {
			goOnly = tol.goOnly
		}
		if tol.cMissing > cMissing {
			cMissing = tol.cMissing
		}
	}
	return goOnly, cMissing
}

var breakerMutationHighlightTolerance = map[string]highlightTolerance{
	"hare":         {cMissing: 4},
	"linkerscript": {cMissing: 4},
	"org":          {goOnly: 2},
	"puppet":       {cMissing: 3},
	"squirrel":     {cMissing: 6},
	"uxntal":       {cMissing: 6},
}

type parityBreakerManifest struct {
	Entries []struct {
		Language   string `json:"language"`
		Bucket     string `json:"bucket"`
		Bytes      int64  `json:"bytes"`
		SourcePath string `json:"source_path"`
		OutputPath string `json:"output_path"`
	} `json:"entries"`
}

func safeCorpusBaseName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "file"
	}
	return out
}

func resolveManifestOutputPath(manifestPath string, lang string, bucket string, sourcePath string, outputPath string) string {
	if outputPath != "" {
		if st, err := os.Stat(outputPath); err == nil && !st.IsDir() {
			return outputPath
		}
	}
	manifestDir := filepath.Dir(manifestPath)
	name := fmt.Sprintf("%s__%s", bucket, safeCorpusBaseName(filepath.Base(sourcePath)))
	fallback := filepath.Join(manifestDir, lang, name)
	if st, err := os.Stat(fallback); err == nil && !st.IsDir() {
		return fallback
	}
	return outputPath
}

func TestParityBreakerRealCorpusStructural(t *testing.T) {
	if !parityBreakerEnabled() {
		t.Skip("set GTS_PARITY_BREAKER=1 to enable real-corpus sweep")
	}

	manifestPath := strings.TrimSpace(os.Getenv("GTS_PARITY_BREAKER_CORPUS_MANIFEST"))
	if manifestPath == "" {
		t.Skip("set GTS_PARITY_BREAKER_CORPUS_MANIFEST to run real-corpus structural sweep")
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest parityBreakerManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	maxBytes := int64(envInt("GTS_PARITY_BREAKER_CORPUS_MAX_BYTES", 64*1024))
	maxFilesPerLang := envInt("GTS_PARITY_BREAKER_CORPUS_MAX_FILES", 2)
	selected := parityBreakerCases(false)
	if len(selected) == 0 {
		t.Skip("no selected languages for corpus sweep")
	}
	selectedLangs := map[string]parityCase{}
	for _, tc := range selected {
		selectedLangs[tc.name] = tc
	}

	perLangCount := map[string]int{}
	total := 0
	for _, entry := range manifest.Entries {
		tc, ok := selectedLangs[entry.Language]
		if !ok {
			continue
		}
		if entry.Bytes <= 0 {
			continue
		}
		if maxBytes > 0 && entry.Bytes > maxBytes {
			continue
		}
		if maxFilesPerLang > 0 && perLangCount[entry.Language] >= maxFilesPerLang {
			continue
		}

		path := resolveManifestOutputPath(manifestPath, entry.Language, entry.Bucket, entry.SourcePath, entry.OutputPath)
		if strings.TrimSpace(path) == "" {
			t.Logf("[%s/%s] unresolved output path for source %q", entry.Language, entry.Bucket, entry.SourcePath)
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Logf("[%s/%s] read %q: %v", entry.Language, entry.Bucket, path, err)
			continue
		}

		label := fmt.Sprintf("real-%s-%s", entry.Bucket, filepath.Base(path))
		runParityCase(t, tc, label, src)
		perLangCount[entry.Language]++
		total++
	}

	if total == 0 {
		t.Skip("no corpus files selected under current breaker filters")
	}
}
