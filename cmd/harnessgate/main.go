package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type step struct {
	Name    string
	Dir     string
	Env     map[string]string
	Command []string
	LogPath string
}

type stepResult struct {
	Step     step
	Duration time.Duration
	Err      error
}

func main() {
	var (
		mode                   string
		outDir                 string
		runRootTests           bool
		runCgoParity           bool
		runPerf                bool
		parityRunRegex         string
		parityTags             string
		benchRegex             string
		benchCount             int
		benchBenchtime         string
		benchBase              string
		maxNSRegression        float64
		maxBytesRegression     float64
		maxAllocsRegression    float64
		goMaxProcs             int
		realCorpusDir          string
		realCorpusLangs        string
		realCorpusResultPath   string
		realCorpusArtifactDir  string
		realCorpusScoreboardMD string
	)

	flag.StringVar(&mode, "mode", "all", "one of: all, correctness, perf")
	flag.StringVar(&outDir, "out-dir", "harness_out", "artifact output directory")
	flag.BoolVar(&runRootTests, "root-tests", true, "run root go test ./... -count=1")
	flag.BoolVar(&runCgoParity, "cgo-parity", true, "run cgo_harness parity gate")
	flag.BoolVar(&runPerf, "perf", true, "run benchmark trio and optional benchgate compare")
	flag.StringVar(&parityRunRegex, "parity-run", "^TestParityFreshParse$|^TestParityIncrementalParse$|^TestParityHasNoErrors$|^TestParityIssue3Repros$|^TestParityGLRCanaryGo$|^TestParityHighlight$", "regex passed to cgo parity go test -run")
	flag.StringVar(&parityTags, "parity-tags", "treesitter_c_parity", "build tags for cgo parity tests")
	flag.StringVar(&benchRegex, "bench-regex", "^(BenchmarkGoParseFullDFA|BenchmarkGoParseIncrementalSingleByteEditDFA|BenchmarkGoParseIncrementalNoEditDFA)$", "benchmark regex for perf run")
	flag.IntVar(&benchCount, "bench-count", 10, "benchmark count")
	flag.StringVar(&benchBenchtime, "bench-benchtime", "750ms", "benchmark benchtime")
	flag.StringVar(&benchBase, "bench-base", "", "optional base benchmark output path for benchgate comparison")
	flag.Float64Var(&maxNSRegression, "max-ns-regression", 0.08, "max allowed ns/op regression ratio for benchgate")
	flag.Float64Var(&maxBytesRegression, "max-bytes-regression", 0.05, "max allowed B/op regression ratio for benchgate")
	flag.Float64Var(&maxAllocsRegression, "max-allocs-regression", 0.05, "max allowed allocs/op regression ratio for benchgate")
	flag.IntVar(&goMaxProcs, "gomaxprocs", 1, "GOMAXPROCS used for benchmarks")
	flag.StringVar(&realCorpusDir, "real-corpus-dir", "", "optional real corpus root; when set, run cgo_harness/cmd/corpus_parity")
	flag.StringVar(&realCorpusLangs, "real-corpus-langs", "top10", "value passed to corpus_parity --lang")
	flag.StringVar(&realCorpusResultPath, "real-corpus-out", "", "optional explicit corpus JSONL output path")
	flag.StringVar(&realCorpusArtifactDir, "real-corpus-artifacts", "", "optional explicit corpus artifact dir")
	flag.StringVar(&realCorpusScoreboardMD, "real-corpus-scoreboard", "", "optional explicit corpus scoreboard markdown path")
	flag.Parse()

	switch mode {
	case "all":
		// honor individual flags
	case "correctness":
		runPerf = false
	case "perf":
		runRootTests = false
		runCgoParity = false
	default:
		fatalf("invalid -mode %q (want all|correctness|perf)", mode)
	}

	if benchCount <= 0 {
		fatalf("-bench-count must be > 0")
	}
	if strings.TrimSpace(benchBenchtime) == "" {
		fatalf("-bench-benchtime must be non-empty")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("create out dir: %v", err)
	}

	results := make([]stepResult, 0, 8)
	appendResult := func(res stepResult) {
		results = append(results, res)
	}

	if runRootTests {
		appendResult(runStep(step{
			Name:    "root-tests",
			Dir:     ".",
			Command: []string{"go", "test", "./...", "-count=1"},
			LogPath: filepath.Join(outDir, "01_root_tests.log"),
		}))
	}

	if runCgoParity {
		appendResult(runStep(step{
			Name:    "cgo-parity-gate",
			Dir:     "cgo_harness",
			Command: []string{"go", "test", ".", "-tags", parityTags, "-run", parityRunRegex, "-count=1", "-v"},
			LogPath: filepath.Join(outDir, "02_cgo_parity.log"),
		}))
	}

	if strings.TrimSpace(realCorpusDir) != "" {
		outJSONL := realCorpusResultPath
		if strings.TrimSpace(outJSONL) == "" {
			outJSONL = filepath.Join("..", outDir, "03_real_corpus_results.jsonl")
		}
		artifactDir := realCorpusArtifactDir
		if strings.TrimSpace(artifactDir) == "" {
			artifactDir = filepath.Join("..", outDir, "03_real_corpus_dump_v1")
		}
		scoreboard := realCorpusScoreboardMD
		if strings.TrimSpace(scoreboard) == "" {
			scoreboard = filepath.Join("..", outDir, "03_real_corpus_PARITY.md")
		}
		appendResult(runStep(step{
			Name: "cgo-real-corpus-parity",
			Dir:  "cgo_harness",
			Command: []string{
				"go", "run", "-tags", parityTags, "./cmd/corpus_parity",
				"--lang", realCorpusLangs,
				"--corpus", realCorpusDir,
				"--out", outJSONL,
				"--artifact-dir", artifactDir,
				"--scoreboard", scoreboard,
			},
			LogPath: filepath.Join(outDir, "03_cgo_real_corpus.log"),
		}))
	}

	benchHeadPath := filepath.Join(outDir, "04_perf_head.txt")
	if runPerf {
		env := map[string]string{}
		if goMaxProcs > 0 {
			env["GOMAXPROCS"] = fmt.Sprintf("%d", goMaxProcs)
		}
		appendResult(runStep(step{
			Name:    "perf-bench-trio",
			Dir:     ".",
			Env:     env,
			Command: []string{"go", "test", ".", "-run", "^$", "-bench", benchRegex, "-benchmem", "-count", fmt.Sprintf("%d", benchCount), "-benchtime", benchBenchtime},
			LogPath: benchHeadPath,
		}))
		if strings.TrimSpace(benchBase) != "" {
			appendResult(runStep(step{
				Name: "perf-benchgate-compare",
				Dir:  ".",
				Command: []string{
					"go", "run", "./cmd/benchgate",
					"-base", benchBase,
					"-head", benchHeadPath,
					"-max-ns-regression", fmt.Sprintf("%.6f", maxNSRegression),
					"-max-bytes-regression", fmt.Sprintf("%.6f", maxBytesRegression),
					"-max-allocs-regression", fmt.Sprintf("%.6f", maxAllocsRegression),
				},
				LogPath: filepath.Join(outDir, "05_perf_benchgate.log"),
			}))
		}
	}

	summaryPath := filepath.Join(outDir, "SUMMARY.md")
	if err := writeSummary(summaryPath, results); err != nil {
		fatalf("write summary: %v", err)
	}
	fmt.Printf("\nHarness summary: %s\n", summaryPath)

	for _, res := range results {
		if res.Err != nil {
			os.Exit(1)
		}
	}
}

func runStep(s step) stepResult {
	start := time.Now()
	fmt.Printf("\n==> %s\n", s.Name)
	if len(s.Command) == 0 {
		return stepResult{Step: s, Duration: time.Since(start), Err: errors.New("empty command")}
	}

	if err := os.MkdirAll(filepath.Dir(s.LogPath), 0o755); err != nil {
		return stepResult{Step: s, Duration: time.Since(start), Err: err}
	}
	logFile, err := os.Create(s.LogPath)
	if err != nil {
		return stepResult{Step: s, Duration: time.Since(start), Err: err}
	}
	defer logFile.Close()

	cmd := exec.Command(s.Command[0], s.Command[1:]...)
	if strings.TrimSpace(s.Dir) != "" {
		cmd.Dir = s.Dir
	}
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	mw := io.MultiWriter(os.Stdout, logFile)
	cmd.Stdout = mw
	cmd.Stderr = mw

	err = cmd.Run()
	dur := time.Since(start)
	if err != nil {
		fmt.Printf("FAIL %s (%s) -> %s\n", s.Name, dur.Round(time.Millisecond), s.LogPath)
		return stepResult{Step: s, Duration: dur, Err: err}
	}
	fmt.Printf("PASS %s (%s) -> %s\n", s.Name, dur.Round(time.Millisecond), s.LogPath)
	return stepResult{Step: s, Duration: dur}
}

func writeSummary(path string, results []stepResult) error {
	var b strings.Builder
	b.WriteString("# Harness Summary\n\n")
	for _, res := range results {
		status := "PASS"
		if res.Err != nil {
			status = "FAIL"
		}
		b.WriteString(fmt.Sprintf("- `%s` %s (%s) log: `%s`\n", status, res.Step.Name, res.Duration.Round(time.Millisecond), res.Step.LogPath))
		if res.Err != nil {
			b.WriteString(fmt.Sprintf("  - error: `%v`\n", res.Err))
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "harnessgate: "+format+"\n", args...)
	os.Exit(2)
}
