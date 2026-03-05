package grammargen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/odvcencio/gotreesitter"
)

const (
	realCorpusEnableEnv         = "GTS_GRAMMARGEN_REAL_CORPUS_ENABLE"
	realCorpusRootEnv           = "GTS_GRAMMARGEN_REAL_CORPUS_ROOT"
	realCorpusMaxCasesEnv       = "GTS_GRAMMARGEN_REAL_CORPUS_MAX_CASES"
	realCorpusMaxGrammarsEnv    = "GTS_GRAMMARGEN_REAL_CORPUS_MAX_GRAMMARS"
	realCorpusRequireParityEnv  = "GTS_GRAMMARGEN_REAL_CORPUS_REQUIRE_PARITY"
	realCorpusRatchetUpdateEnv  = "GTS_GRAMMARGEN_REAL_CORPUS_RATCHET_UPDATE"
	realCorpusFloorsPathEnv     = "GTS_GRAMMARGEN_REAL_CORPUS_FLOORS_PATH"
	realCorpusAllowPartialEnv   = "GTS_GRAMMARGEN_REAL_CORPUS_ALLOW_PARTIAL"
	realCorpusFloorsFileVersion = 1
)

type realCorpusMetrics struct {
	Eligible    int `json:"eligible"`
	NoError     int `json:"no_error"`
	SExprParity int `json:"sexpr_parity"`
	DeepParity  int `json:"deep_parity"`
}

type realCorpusFloorFile struct {
	Version     int                          `json:"version"`
	GeneratedAt string                       `json:"generated_at"`
	CorpusRoot  string                       `json:"corpus_root"`
	MaxCases    int                          `json:"max_cases"`
	Metrics     map[string]realCorpusMetrics `json:"metrics"`
}

func TestMultiGrammarImportRealCorpusParity(t *testing.T) {
	if !getenvBool(realCorpusEnableEnv) {
		t.Skipf("set %s=1 to enable real-corpus grammargen parity checks", realCorpusEnableEnv)
	}

	root := strings.TrimSpace(os.Getenv(realCorpusRootEnv))
	if root == "" {
		root = "/tmp/grammar_parity"
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("real corpus root unavailable: %s (%v)", root, err)
	}

	maxCases := getenvInt(realCorpusMaxCasesEnv, 8)
	maxGrammars := getenvInt(realCorpusMaxGrammarsEnv, 0)
	requireParity := getenvBool(realCorpusRequireParityEnv)
	updateRatchet := getenvBool(realCorpusRatchetUpdateEnv)
	allowPartial := getenvBool(realCorpusAllowPartialEnv)

	floorsPath := strings.TrimSpace(os.Getenv(realCorpusFloorsPathEnv))
	if floorsPath == "" {
		floorsPath = defaultRealCorpusFloorsPath()
	}
	floorFile, foundFloors, err := loadRealCorpusFloorFile(floorsPath)
	if err != nil {
		t.Fatalf("load floor file %s: %v", floorsPath, err)
	}
	if floorFile.Metrics == nil {
		floorFile.Metrics = map[string]realCorpusMetrics{}
	}
	if !updateRatchet && foundFloors && floorFile.MaxCases > 0 && maxCases < floorFile.MaxCases {
		t.Fatalf("max cases %d is below ratchet floor file max_cases=%d; increase %s or regenerate floors", maxCases, floorFile.MaxCases, realCorpusMaxCasesEnv)
	}

	testedGrammars := 0
	totalEligible := 0
	totalNoError := 0
	totalSExprParity := 0
	totalDeepParity := 0
	observed := map[string]realCorpusMetrics{}

	for _, g := range importParityGrammars {
		if maxGrammars > 0 && testedGrammars >= maxGrammars {
			break
		}

		repoRoot := parityGrammarRepoRoot(g, root)
		if repoRoot == "" {
			continue
		}
		samples := collectTreeSitterCorpusSamples(t, repoRoot, maxCases)
		if len(samples) == 0 {
			continue
		}

		testedGrammars++
		g := g
		t.Run(g.name, func(t *testing.T) {
			gram, err := importParityGrammarSource(g)
			if err != nil {
				t.Fatalf("import failed: %v", err)
			}

			timeout := g.genTimeout
			if timeout == 0 {
				timeout = 30 * time.Second
			}
			genLang, err := generateWithTimeout(gram, timeout)
			if err != nil {
				t.Fatalf("generate failed: %v", err)
			}
			refLang := g.blobFunc()
			if shouldAdaptExternalScanner(g.name) {
				if scanner, ok := gotreesitter.AdaptExternalScannerByExternalOrder(refLang, genLang); ok {
					genLang.ExternalScanner = scanner
				}
			}

			genParser := gotreesitter.NewParser(genLang)
			refParser := gotreesitter.NewParser(refLang)

			metrics := realCorpusMetrics{}
			mismatchLogs := 0

			for i, sample := range samples {
				genTree, _ := genParser.Parse([]byte(sample))
				refTree, _ := refParser.Parse([]byte(sample))

				genRoot := genTree.RootNode()
				refRoot := refTree.RootNode()
				genSexp := genRoot.SExpr(genLang)
				refSexp := refRoot.SExpr(refLang)

				refHasError := strings.Contains(refSexp, "ERROR") || strings.Contains(refSexp, "MISSING")
				if refHasError {
					continue
				}
				metrics.Eligible++

				genHasError := strings.Contains(genSexp, "ERROR") || strings.Contains(genSexp, "MISSING")
				if genHasError {
					t.Fatalf("sample %d generated parser has ERROR/MISSING while reference is clean\nGEN: %s\nREF: %s", i, genSexp, refSexp)
				}
				metrics.NoError++

				if genSexp == refSexp {
					metrics.SExprParity++
				}
				divs := compareTreesDeep(genRoot, genLang, refRoot, refLang, "root", 10)
				if len(divs) == 0 {
					metrics.DeepParity++
				} else if requireParity {
					t.Fatalf("sample %d deep parity mismatch: %s\nGEN: %s\nREF: %s", i, divs[0].String(), genSexp, refSexp)
				} else if mismatchLogs < 3 {
					mismatchLogs++
					t.Logf("sample %d deep mismatch: %s", i, divs[0].String())
				}
			}

			if metrics.Eligible == 0 {
				t.Skip("no clean reference samples in extracted corpus set")
			}

			if !updateRatchet && len(floorFile.Metrics) > 0 {
				floor, ok := floorFile.Metrics[g.name]
				if !ok {
					t.Errorf("missing ratchet floor for grammar %q in %s", g.name, floorsPath)
				} else {
					enforceRealCorpusRatchet(t, floor, metrics)
				}
			}

			observed[g.name] = metrics
			totalEligible += metrics.Eligible
			totalNoError += metrics.NoError
			totalSExprParity += metrics.SExprParity
			totalDeepParity += metrics.DeepParity

			t.Logf("real-corpus: no-error %d/%d, sexpr parity %d/%d, deep parity %d/%d (requireParity=%v)",
				metrics.NoError, metrics.Eligible,
				metrics.SExprParity, metrics.Eligible,
				metrics.DeepParity, metrics.Eligible,
				requireParity)
		})
	}

	if testedGrammars == 0 {
		t.Skipf("no grammar repos with corpus samples found under %s", root)
	}

	if !updateRatchet && len(floorFile.Metrics) > 0 && !allowPartial {
		for grammarName := range floorFile.Metrics {
			if _, ok := observed[grammarName]; !ok {
				t.Errorf("ratchet floor grammar %q not exercised in this run (set %s=1 to allow partial runs)", grammarName, realCorpusAllowPartialEnv)
			}
		}
	}

	if updateRatchet {
		for grammarName, cur := range observed {
			if prev, ok := floorFile.Metrics[grammarName]; ok {
				if cur.Eligible < prev.Eligible ||
					cur.NoError < prev.NoError ||
					cur.SExprParity < prev.SExprParity ||
					cur.DeepParity < prev.DeepParity {
					t.Fatalf("ratchet update would decrease floor for %s: prev=%+v new=%+v", grammarName, prev, cur)
				}
			}
			floorFile.Metrics[grammarName] = cur
		}
		floorFile.Version = realCorpusFloorsFileVersion
		floorFile.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
		floorFile.CorpusRoot = root
		floorFile.MaxCases = maxCases
		if err := writeRealCorpusFloorFile(floorsPath, floorFile); err != nil {
			t.Fatalf("write floor file %s: %v", floorsPath, err)
		}
		t.Logf("updated ratchet floor file: %s", floorsPath)
	}

	t.Logf("REAL CORPUS SUMMARY: grammars=%d eligible=%d no-error=%d sexpr_parity=%d deep_parity=%d requireParity=%v ratchetUpdate=%v",
		testedGrammars, totalEligible, totalNoError, totalSExprParity, totalDeepParity, requireParity, updateRatchet)
}

func enforceRealCorpusRatchet(t *testing.T, floor, cur realCorpusMetrics) {
	t.Helper()
	if cur.Eligible < floor.Eligible {
		t.Errorf("ratchet regression eligible: %d < floor %d", cur.Eligible, floor.Eligible)
	}
	if cur.NoError < floor.NoError {
		t.Errorf("ratchet regression no-error: %d < floor %d", cur.NoError, floor.NoError)
	}
	if cur.SExprParity < floor.SExprParity {
		t.Errorf("ratchet regression sexpr parity: %d < floor %d", cur.SExprParity, floor.SExprParity)
	}
	if cur.DeepParity < floor.DeepParity {
		t.Errorf("ratchet regression deep parity: %d < floor %d", cur.DeepParity, floor.DeepParity)
	}
	if floor.Eligible > 0 && cur.Eligible > 0 {
		if cur.NoError*floor.Eligible < floor.NoError*cur.Eligible {
			t.Errorf("ratchet regression no-error ratio: %d/%d < floor %d/%d", cur.NoError, cur.Eligible, floor.NoError, floor.Eligible)
		}
		if cur.SExprParity*floor.Eligible < floor.SExprParity*cur.Eligible {
			t.Errorf("ratchet regression sexpr parity ratio: %d/%d < floor %d/%d", cur.SExprParity, cur.Eligible, floor.SExprParity, floor.Eligible)
		}
		if cur.DeepParity*floor.Eligible < floor.DeepParity*cur.Eligible {
			t.Errorf("ratchet regression deep parity ratio: %d/%d < floor %d/%d", cur.DeepParity, cur.Eligible, floor.DeepParity, floor.Eligible)
		}
	}
}

func defaultRealCorpusFloorsPath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "grammargen/testdata/real_corpus_parity_floors.json"
	}
	return filepath.Join(filepath.Dir(file), "testdata", "real_corpus_parity_floors.json")
}

func loadRealCorpusFloorFile(path string) (realCorpusFloorFile, bool, error) {
	var out realCorpusFloorFile
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			out.Version = realCorpusFloorsFileVersion
			out.Metrics = map[string]realCorpusMetrics{}
			return out, false, nil
		}
		return out, false, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, false, err
	}
	if out.Metrics == nil {
		out.Metrics = map[string]realCorpusMetrics{}
	}
	if out.Version == 0 {
		out.Version = realCorpusFloorsFileVersion
	}
	return out, true, nil
}

func writeRealCorpusFloorFile(path string, f realCorpusFloorFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func parityGrammarRepoRoot(g importParityGrammar, root string) string {
	for _, p := range []string{g.jsonPath, g.path} {
		if p == "" {
			continue
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "../") || rel == ".." {
			continue
		}
		parts := strings.Split(rel, "/")
		if len(parts) == 0 || parts[0] == "." || parts[0] == "" {
			continue
		}
		repoRoot := filepath.Join(root, parts[0])
		if info, statErr := os.Stat(repoRoot); statErr == nil && info.IsDir() {
			return repoRoot
		}
	}
	return ""
}

func importParityGrammarSource(g importParityGrammar) (*Grammar, error) {
	if g.jsonPath != "" {
		source, err := os.ReadFile(g.jsonPath)
		if err != nil {
			return nil, fmt.Errorf("read grammar.json: %w", err)
		}
		return ImportGrammarJSON(source)
	}
	source, err := os.ReadFile(g.path)
	if err != nil {
		return nil, fmt.Errorf("read grammar.js: %w", err)
	}
	return ImportGrammarJS(source)
}

func collectTreeSitterCorpusSamples(t *testing.T, repoRoot string, maxCases int) []string {
	t.Helper()
	if maxCases <= 0 {
		maxCases = 8
	}
	dirs := []string{
		filepath.Join(repoRoot, "test", "corpus"),
		filepath.Join(repoRoot, "tests", "corpus"),
		filepath.Join(repoRoot, "corpus"),
	}

	var files []string
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			files = append(files, path)
			return nil
		})
		if walkErr != nil {
			t.Logf("walk corpus dir %s: %v", dir, walkErr)
		}
	}
	if len(files) == 0 {
		return nil
	}
	sort.Strings(files)

	type sampleEntry struct {
		text string
		size int
	}
	entries := make([]sampleEntry, 0, maxCases*2)
	seen := map[string]struct{}{}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, sample := range extractTreeSitterCorpusInputs(data) {
			trimmed := strings.TrimSpace(sample)
			if trimmed == "" || len(trimmed) > 64*1024 {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			entries = append(entries, sampleEntry{text: sample, size: len(trimmed)})
		}
	}
	if len(entries) == 0 {
		return nil
	}
	// Prefer smaller samples for stable runtime while preserving deterministic
	// selection for parity ratcheting.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].size != entries[j].size {
			return entries[i].size < entries[j].size
		}
		return entries[i].text < entries[j].text
	})
	if len(entries) > maxCases {
		entries = entries[:maxCases]
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.text)
	}
	return out
}

func extractTreeSitterCorpusInputs(data []byte) []string {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	out := make([]string, 0, 8)

	for i := 0; i < len(lines); {
		if !isEqualsFence(lines[i]) {
			i++
			continue
		}
		// Skip title block.
		i++
		for i < len(lines) && !isEqualsFence(lines[i]) {
			i++
		}
		if i >= len(lines) {
			break
		}
		// After second fence, parse source until --- separator.
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		start := i
		for i < len(lines) && strings.TrimSpace(lines[i]) != "---" {
			i++
		}
		if i > start {
			src := strings.Trim(strings.Join(lines[start:i], "\n"), "\n")
			if strings.TrimSpace(src) != "" {
				out = append(out, src)
			}
		}
		if i < len(lines) {
			i++
		}
	}
	return out
}

func isEqualsFence(line string) bool {
	s := strings.TrimSpace(line)
	if len(s) < 3 {
		return false
	}
	for _, r := range s {
		if r != '=' {
			return false
		}
	}
	return true
}

func getenvInt(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func getenvBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
