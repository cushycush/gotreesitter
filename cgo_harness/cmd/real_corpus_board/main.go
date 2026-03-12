package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type corpusManifest struct {
	Languages []string              `json:"languages"`
	Entries   []corpusManifestEntry `json:"entries"`
}

type corpusManifestEntry struct {
	Language   string `json:"language"`
	Bucket     string `json:"bucket"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	SourcePath string `json:"source_path"`
	OutputPath string `json:"output_path"`
}

type parityResult struct {
	Language       string           `json:"language"`
	FileID         string           `json:"file_id"`
	FilePath       string           `json:"file_path"`
	SourceSHA256   string           `json:"source_sha256"`
	GoRootType     string           `json:"go_root_type,omitempty"`
	GoRootHasError bool             `json:"go_root_has_error,omitempty"`
	CRootType      string           `json:"c_root_type,omitempty"`
	CRootHasError  bool             `json:"c_root_has_error,omitempty"`
	Pass           bool             `json:"pass"`
	Category       string           `json:"category,omitempty"`
	Error          string           `json:"error,omitempty"`
	FirstDiv       *firstDivergence `json:"first_divergence,omitempty"`
}

type firstDivergence struct {
	Path     string `json:"path,omitempty"`
	Category string `json:"category,omitempty"`
	GoValue  string `json:"goValue,omitempty"`
	CValue   string `json:"cValue,omitempty"`
}

type board struct {
	GeneratedAt         string          `json:"generated_at"`
	ManifestPath        string          `json:"manifest_path"`
	ResultsPath         string          `json:"results_path"`
	L3Scope             string          `json:"l3_scope"`
	L4Scope             string          `json:"l4_scope"`
	L4SelectedLanguages []string        `json:"l4_selected_languages,omitempty"`
	L3                  boardAggregate  `json:"l3"`
	L4                  boardAggregate  `json:"l4"`
	Languages           []languageBoard `json:"languages"`
}

type boardAggregate struct {
	Name             string  `json:"name"`
	ApplicableLangs  int     `json:"applicable_languages"`
	GreenLangs       int     `json:"green_languages"`
	TotalFiles       int     `json:"total_files"`
	PassingFiles     int     `json:"passing_files"`
	LanguageProgress float64 `json:"language_progress_pct"`
	FileProgress     float64 `json:"file_progress_pct"`
}

type languageBoard struct {
	Language string       `json:"language"`
	L3       levelSummary `json:"l3"`
	L4       levelSummary `json:"l4"`
}

type levelSummary struct {
	Status        string        `json:"status"`
	TotalFiles    int           `json:"total_files"`
	PassingFiles  int           `json:"passing_files"`
	FailingFiles  []fileFailure `json:"failing_files,omitempty"`
	MissingFiles  []string      `json:"missing_files,omitempty"`
	ExcludedFiles []string      `json:"excluded_files,omitempty"`
}

type fileFailure struct {
	FileID          string `json:"file_id"`
	Category        string `json:"category,omitempty"`
	Error           string `json:"error,omitempty"`
	FirstDivergence string `json:"first_divergence,omitempty"`
}

type boardOptions struct {
	L4Limit     int
	L4Languages []string
}

func main() {
	var (
		manifestPath string
		resultsPath  string
		outJSON      string
		outMD        string
		l4Limit      int
		l4Languages  string
	)

	flag.StringVar(&manifestPath, "manifest", "", "path to build_real_corpus manifest.json")
	flag.StringVar(&resultsPath, "results", "", "path to corpus_parity results.jsonl")
	flag.StringVar(&outJSON, "out-json", "real_corpus_board.json", "JSON output path")
	flag.StringVar(&outMD, "out-md", "REAL_CORPUS_BOARD.md", "Markdown output path")
	flag.IntVar(&l4Limit, "l4-limit", 0, "if >0, limit L4 to the top N languages by max large-file bytes")
	flag.StringVar(&l4Languages, "l4-languages", "", "optional comma-separated explicit L4 language subset")
	flag.Parse()

	if strings.TrimSpace(manifestPath) == "" {
		fatalf("--manifest is required")
	}
	if strings.TrimSpace(resultsPath) == "" {
		fatalf("--results is required")
	}
	if l4Limit < 0 {
		fatalf("--l4-limit must be >= 0")
	}
	if l4Limit > 0 && strings.TrimSpace(l4Languages) != "" {
		fatalf("set only one of --l4-limit or --l4-languages")
	}

	manifest, err := loadManifest(manifestPath)
	if err != nil {
		fatalf("load manifest: %v", err)
	}
	results, err := loadResults(resultsPath)
	if err != nil {
		fatalf("load results: %v", err)
	}

	b := buildBoard(manifestPath, resultsPath, manifest, results, boardOptions{
		L4Limit:     l4Limit,
		L4Languages: parseCSVList(l4Languages),
	})
	if err := writeJSON(outJSON, b); err != nil {
		fatalf("write %s: %v", outJSON, err)
	}
	if err := writeMarkdown(outMD, b); err != nil {
		fatalf("write %s: %v", outMD, err)
	}

	fmt.Printf("wrote board json: %s\n", outJSON)
	fmt.Printf("wrote board markdown: %s\n", outMD)
	fmt.Printf("L3: %d/%d languages green, %d/%d files green\n",
		b.L3.GreenLangs, b.L3.ApplicableLangs, b.L3.PassingFiles, b.L3.TotalFiles)
	fmt.Printf("L4: %d/%d languages green, %d/%d files green\n",
		b.L4.GreenLangs, b.L4.ApplicableLangs, b.L4.PassingFiles, b.L4.TotalFiles)
}

func loadManifest(path string) (*corpusManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m corpusManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func loadResults(path string) ([]parityResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	results := make([]parityResult, 0, 128)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var r parityResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("decode line %d: %w", len(results)+1, err)
		}
		results = append(results, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func buildBoard(manifestPath, resultsPath string, manifest *corpusManifest, results []parityResult, opts boardOptions) board {
	resultByLangAndFile := make(map[string]parityResult, len(results))
	for _, r := range results {
		resultByLangAndFile[resultKey(r.Language, r.FileID)] = r
	}

	langs := append([]string(nil), manifest.Languages...)
	sort.Strings(langs)

	l4Selected, l4Scope := selectL4Languages(manifest.Entries, opts)
	l4SelectedList := make([]string, 0, len(l4Selected))
	for name := range l4Selected {
		l4SelectedList = append(l4SelectedList, name)
	}
	sort.Strings(l4SelectedList)

	out := board{
		GeneratedAt:         time.Now().UTC().Format(time.RFC3339),
		ManifestPath:        manifestPath,
		ResultsPath:         resultsPath,
		L3Scope:             "all medium entries present in manifest",
		L4Scope:             l4Scope,
		L4SelectedLanguages: l4SelectedList,
		L3:                  boardAggregate{Name: "L3"},
		L4:                  boardAggregate{Name: "L4"},
		Languages:           make([]languageBoard, 0, len(langs)),
	}

	for _, lang := range langs {
		lb := languageBoard{Language: lang}
		mediumEntries := manifestEntriesFor(manifest.Entries, lang, "medium")
		largeEntries := []corpusManifestEntry(nil)
		if len(l4Selected) == 0 || containsStringKey(l4Selected, lang) {
			largeEntries = manifestEntriesFor(manifest.Entries, lang, "large")
		}
		lb.L3 = summarizeLevel(mediumEntries, lang, resultByLangAndFile)
		lb.L4 = summarizeLevel(largeEntries, lang, resultByLangAndFile)
		out.Languages = append(out.Languages, lb)
		accumulateAggregate(&out.L3, lb.L3)
		accumulateAggregate(&out.L4, lb.L4)
	}

	finalizeAggregate(&out.L3)
	finalizeAggregate(&out.L4)
	return out
}

func parseCSVList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 16)
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

type l4LangRank struct {
	Language string
	MaxBytes int64
}

func selectL4Languages(entries []corpusManifestEntry, opts boardOptions) (map[string]struct{}, string) {
	if len(opts.L4Languages) > 0 {
		out := make(map[string]struct{}, len(opts.L4Languages))
		for _, name := range opts.L4Languages {
			out[name] = struct{}{}
		}
		return out, fmt.Sprintf("explicit L4 language subset (%d): %s", len(opts.L4Languages), strings.Join(opts.L4Languages, ", "))
	}
	if opts.L4Limit <= 0 {
		return nil, "all large entries present in manifest"
	}
	maxByLang := map[string]int64{}
	for _, entry := range entries {
		if entry.Bucket != "large" {
			continue
		}
		if entry.Bytes > maxByLang[entry.Language] {
			maxByLang[entry.Language] = entry.Bytes
		}
	}
	ranks := make([]l4LangRank, 0, len(maxByLang))
	for lang, maxBytes := range maxByLang {
		ranks = append(ranks, l4LangRank{Language: lang, MaxBytes: maxBytes})
	}
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].MaxBytes != ranks[j].MaxBytes {
			return ranks[i].MaxBytes > ranks[j].MaxBytes
		}
		return ranks[i].Language < ranks[j].Language
	})
	if opts.L4Limit > len(ranks) {
		opts.L4Limit = len(ranks)
	}
	out := make(map[string]struct{}, opts.L4Limit)
	names := make([]string, 0, opts.L4Limit)
	for _, rank := range ranks[:opts.L4Limit] {
		out[rank.Language] = struct{}{}
		names = append(names, rank.Language)
	}
	return out, fmt.Sprintf("top %d languages by max large-file bytes: %s", len(names), strings.Join(names, ", "))
}

func containsStringKey(m map[string]struct{}, key string) bool {
	_, ok := m[key]
	return ok
}

func manifestEntriesFor(entries []corpusManifestEntry, lang, bucket string) []corpusManifestEntry {
	out := make([]corpusManifestEntry, 0, 4)
	for _, entry := range entries {
		if entry.Language == lang && entry.Bucket == bucket {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return filepath.Base(out[i].OutputPath) < filepath.Base(out[j].OutputPath)
	})
	return out
}

func summarizeLevel(entries []corpusManifestEntry, lang string, results map[string]parityResult) levelSummary {
	if len(entries) == 0 {
		return levelSummary{Status: "na"}
	}
	summary := levelSummary{}
	for _, entry := range entries {
		fileID := filepath.Base(entry.OutputPath)
		r, ok := results[resultKey(lang, fileID)]
		if !ok {
			summary.MissingFiles = append(summary.MissingFiles, fileID)
			continue
		}
		if entry.SHA256 != "" && r.SourceSHA256 != "" && entry.SHA256 != r.SourceSHA256 {
			summary.FailingFiles = append(summary.FailingFiles, fileFailure{
				FileID:   fileID,
				Category: "sha_mismatch",
				Error:    fmt.Sprintf("manifest=%s result=%s", entry.SHA256, r.SourceSHA256),
			})
			continue
		}
		if shouldExcludeResult(r) {
			summary.ExcludedFiles = append(summary.ExcludedFiles, fileID)
			continue
		}
		summary.TotalFiles++
		if r.Pass {
			summary.PassingFiles++
			continue
		}
		ff := fileFailure{
			FileID:   fileID,
			Category: r.Category,
			Error:    r.Error,
		}
		if r.FirstDiv != nil {
			ff.FirstDivergence = strings.TrimSpace(r.FirstDiv.Path + " " + r.FirstDiv.Category)
		}
		summary.FailingFiles = append(summary.FailingFiles, ff)
	}
	if len(summary.MissingFiles) > 0 || len(summary.FailingFiles) > 0 {
		summary.Status = "red"
		return summary
	}
	if summary.TotalFiles == 0 {
		summary.Status = "na"
		return summary
	}
	summary.Status = "green"
	return summary
}

func shouldExcludeResult(r parityResult) bool {
	if r.CRootHasError {
		return true
	}
	switch r.Category {
	case "oracle_error", "oracle_timeout":
		return true
	default:
		return false
	}
}

func resultKey(lang, fileID string) string {
	return lang + "\x00" + fileID
}

func accumulateAggregate(agg *boardAggregate, level levelSummary) {
	if level.Status == "na" {
		return
	}
	agg.ApplicableLangs++
	if level.Status == "green" {
		agg.GreenLangs++
	}
	agg.TotalFiles += level.TotalFiles
	agg.PassingFiles += level.PassingFiles
}

func finalizeAggregate(agg *boardAggregate) {
	if agg.ApplicableLangs > 0 {
		agg.LanguageProgress = 100 * float64(agg.GreenLangs) / float64(agg.ApplicableLangs)
	}
	if agg.TotalFiles > 0 {
		agg.FileProgress = 100 * float64(agg.PassingFiles) / float64(agg.TotalFiles)
	}
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeMarkdown(path string, b board) error {
	var md strings.Builder
	md.WriteString("# Real Corpus Board\n\n")
	md.WriteString(fmt.Sprintf("_Generated: %s_\n\n", b.GeneratedAt))
	md.WriteString(fmt.Sprintf("- Manifest: `%s`\n", b.ManifestPath))
	md.WriteString(fmt.Sprintf("- Results: `%s`\n", b.ResultsPath))
	md.WriteString(fmt.Sprintf("- L3 Scope: %s\n", b.L3Scope))
	md.WriteString(fmt.Sprintf("- L4 Scope: %s\n\n", b.L4Scope))

	md.WriteString("## Summary\n\n")
	md.WriteString("| Level | Green Languages | Applicable Languages | Passing Files | Total Files | Language Progress | File Progress |\n")
	md.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, agg := range []boardAggregate{b.L3, b.L4} {
		md.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %d | %.1f%% | %.1f%% |\n",
			agg.Name, agg.GreenLangs, agg.ApplicableLangs, agg.PassingFiles, agg.TotalFiles, agg.LanguageProgress, agg.FileProgress))
	}
	md.WriteString("\n## By Language\n\n")
	md.WriteString("| Language | L3 | L4 |\n")
	md.WriteString("| --- | --- | --- |\n")
	for _, lang := range b.Languages {
		md.WriteString(fmt.Sprintf("| %s | %s | %s |\n", lang.Language, formatLevelSummary(lang.L3), formatLevelSummary(lang.L4)))
	}
	return os.WriteFile(path, []byte(md.String()), 0o644)
}

func formatLevelSummary(level levelSummary) string {
	switch level.Status {
	case "na":
		if len(level.ExcludedFiles) > 0 {
			return fmt.Sprintf("N/A (excluded=%d)", len(level.ExcludedFiles))
		}
		return "N/A"
	case "green":
		s := fmt.Sprintf("green (%d/%d)", level.PassingFiles, level.TotalFiles)
		if len(level.ExcludedFiles) > 0 {
			s += fmt.Sprintf(", excluded=%d", len(level.ExcludedFiles))
		}
		return s
	default:
		if len(level.MissingFiles) > 0 {
			s := fmt.Sprintf("red (%d/%d, missing=%d)", level.PassingFiles, level.TotalFiles, len(level.MissingFiles))
			if len(level.ExcludedFiles) > 0 {
				s += fmt.Sprintf(", excluded=%d", len(level.ExcludedFiles))
			}
			return s
		}
		s := fmt.Sprintf("red (%d/%d)", level.PassingFiles, level.TotalFiles)
		if len(level.ExcludedFiles) > 0 {
			s += fmt.Sprintf(", excluded=%d", len(level.ExcludedFiles))
		}
		return s
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
