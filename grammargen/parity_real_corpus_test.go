package grammargen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/odvcencio/gotreesitter"
)

const (
	realCorpusEnableEnv        = "GTS_GRAMMARGEN_REAL_CORPUS_ENABLE"
	realCorpusRootEnv          = "GTS_GRAMMARGEN_REAL_CORPUS_ROOT"
	realCorpusMaxCasesEnv      = "GTS_GRAMMARGEN_REAL_CORPUS_MAX_CASES"
	realCorpusMaxGrammarsEnv   = "GTS_GRAMMARGEN_REAL_CORPUS_MAX_GRAMMARS"
	realCorpusRequireParityEnv = "GTS_GRAMMARGEN_REAL_CORPUS_REQUIRE_PARITY"
)

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

	maxCases := getenvInt(realCorpusMaxCasesEnv, 12)
	maxGrammars := getenvInt(realCorpusMaxGrammarsEnv, 0)
	requireParity := getenvBool(realCorpusRequireParityEnv)

	testedGrammars := 0
	totalEligible := 0
	totalNoError := 0
	totalParity := 0

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

			eligible := 0
			noError := 0
			parity := 0

			for i, sample := range samples {
				genTree, _ := genParser.Parse([]byte(sample))
				refTree, _ := refParser.Parse([]byte(sample))

				genSexp := genTree.RootNode().SExpr(genLang)
				refSexp := refTree.RootNode().SExpr(refLang)
				refHasError := strings.Contains(refSexp, "ERROR") || strings.Contains(refSexp, "MISSING")
				if refHasError {
					continue
				}

				eligible++
				genHasError := strings.Contains(genSexp, "ERROR") || strings.Contains(genSexp, "MISSING")
				if !genHasError {
					noError++
				}
				if genSexp == refSexp {
					parity++
				} else if requireParity {
					t.Fatalf("sample %d parity mismatch\nGEN: %s\nREF: %s", i, genSexp, refSexp)
				}
				if genHasError {
					t.Fatalf("sample %d generated parser has ERROR/MISSING while reference is clean\nGEN: %s\nREF: %s", i, genSexp, refSexp)
				}
			}

			if eligible == 0 {
				t.Skip("no clean reference samples in extracted corpus set")
			}
			totalEligible += eligible
			totalNoError += noError
			totalParity += parity
			t.Logf("real-corpus: %d/%d no-error, %d/%d parity (requireParity=%v)", noError, eligible, parity, eligible, requireParity)
		})
	}

	if testedGrammars == 0 {
		t.Skipf("no grammar repos with corpus samples found under %s", root)
	}
	t.Logf("REAL CORPUS SUMMARY: grammars=%d eligible=%d no-error=%d parity=%d requireParity=%v",
		testedGrammars, totalEligible, totalNoError, totalParity, requireParity)
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
		maxCases = 12
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

	out := make([]string, 0, maxCases)
	seen := map[string]struct{}{}
	for _, path := range files {
		if len(out) >= maxCases {
			break
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, sample := range extractTreeSitterCorpusInputs(data) {
			if len(out) >= maxCases {
				break
			}
			trimmed := strings.TrimSpace(sample)
			if trimmed == "" || len(trimmed) > 64*1024 {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			out = append(out, sample)
		}
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
