package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type benchSample struct {
	NsPerOp     float64
	BytesPerOp  float64
	AllocsPerOp float64
}

type benchStats struct {
	Samples      int
	MedianNs     float64
	MedianBytes  float64
	MedianAllocs float64
}

var (
	benchLineRe = regexp.MustCompile(`^Benchmark[^\s]+`)
	suffixNumRe = regexp.MustCompile(`-\d+$`)
)

func main() {
	var (
		basePath            string
		headPath            string
		benchmarksRaw       string
		maxNsRegression     float64
		maxBytesRegression  float64
		maxAllocsRegression float64
		baseRSSPath         string
		headRSSPath         string
		maxRSSRegression    float64
	)

	flag.StringVar(&basePath, "base", "", "base benchmark output path")
	flag.StringVar(&headPath, "head", "", "head benchmark output path")
	flag.StringVar(&benchmarksRaw, "benchmarks", "BenchmarkGoParseFullDFA,BenchmarkGoParseIncrementalSingleByteEditDFA,BenchmarkGoParseIncrementalNoEditDFA", "comma-separated benchmark names to gate")
	flag.Float64Var(&maxNsRegression, "max-ns-regression", 0.08, "max allowed ns/op regression ratio (0.08 = +8%)")
	flag.Float64Var(&maxBytesRegression, "max-bytes-regression", 0.05, "max allowed B/op regression ratio (0.05 = +5%)")
	flag.Float64Var(&maxAllocsRegression, "max-allocs-regression", 0.05, "max allowed allocs/op regression ratio (0.05 = +5%)")
	flag.StringVar(&baseRSSPath, "base-rss", "", "optional /usr/bin/time -v output for base")
	flag.StringVar(&headRSSPath, "head-rss", "", "optional /usr/bin/time -v output for head")
	flag.Float64Var(&maxRSSRegression, "max-rss-regression", 0.10, "max allowed max-RSS regression ratio (0.10 = +10%)")
	flag.Parse()

	if strings.TrimSpace(basePath) == "" || strings.TrimSpace(headPath) == "" {
		fatalf("both -base and -head are required")
	}

	required := parseBenchmarks(benchmarksRaw)
	if len(required) == 0 {
		fatalf("-benchmarks must include at least one benchmark")
	}

	baseRaw, err := parseBenchFile(basePath)
	if err != nil {
		fatalf("parse base benchmarks: %v", err)
	}
	headRaw, err := parseBenchFile(headPath)
	if err != nil {
		fatalf("parse head benchmarks: %v", err)
	}

	base := aggregate(baseRaw)
	head := aggregate(headRaw)

	fmt.Printf("benchgate thresholds: ns<=+%.2f%% B<=+%.2f%% allocs<=+%.2f%%\n",
		maxNsRegression*100.0, maxBytesRegression*100.0, maxAllocsRegression*100.0)
	fmt.Println("benchmark\tmetric\tbase\thead\tdelta\tstatus")

	failed := false
	for _, name := range required {
		baseStats, ok := base[name]
		if !ok {
			fmt.Printf("%s\t-\t-\t-\t-\tFAIL (missing in base)\n", name)
			failed = true
			continue
		}
		headStats, ok := head[name]
		if !ok {
			fmt.Printf("%s\t-\t-\t-\t-\tFAIL (missing in head)\n", name)
			failed = true
			continue
		}
		if baseStats.Samples == 0 || headStats.Samples == 0 {
			fmt.Printf("%s\t-\t-\t-\t-\tFAIL (no samples)\n", name)
			failed = true
			continue
		}

		failed = compareMetric(name, "ns/op", baseStats.MedianNs, headStats.MedianNs, maxNsRegression) || failed
		failed = compareMetric(name, "B/op", baseStats.MedianBytes, headStats.MedianBytes, maxBytesRegression) || failed
		failed = compareMetric(name, "allocs/op", baseStats.MedianAllocs, headStats.MedianAllocs, maxAllocsRegression) || failed
	}

	if baseRSSPath != "" || headRSSPath != "" {
		if baseRSSPath == "" || headRSSPath == "" {
			fatalf("both -base-rss and -head-rss are required when either is set")
		}
		baseRSS, err := parseMaxRSS(baseRSSPath)
		if err != nil {
			fatalf("parse base RSS: %v", err)
		}
		headRSS, err := parseMaxRSS(headRSSPath)
		if err != nil {
			fatalf("parse head RSS: %v", err)
		}
		failed = compareMetric("rss", "max_rss_kb", float64(baseRSS), float64(headRSS), maxRSSRegression) || failed
	}

	if failed {
		os.Exit(1)
	}
}

func parseBenchmarks(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func parseBenchFile(path string) (map[string][]benchSample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string][]benchSample{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !benchLineRe.MatchString(line) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		name := suffixNumRe.ReplaceAllString(fields[0], "")
		sample := benchSample{}
		for i := 2; i+1 < len(fields); i += 2 {
			v, err := strconv.ParseFloat(fields[i], 64)
			if err != nil {
				continue
			}
			switch fields[i+1] {
			case "ns/op":
				sample.NsPerOp = v
			case "B/op":
				sample.BytesPerOp = v
			case "allocs/op":
				sample.AllocsPerOp = v
			}
		}
		out[name] = append(out[name], sample)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func aggregate(raw map[string][]benchSample) map[string]benchStats {
	out := make(map[string]benchStats, len(raw))
	for name, runs := range raw {
		ns := make([]float64, 0, len(runs))
		bytes := make([]float64, 0, len(runs))
		allocs := make([]float64, 0, len(runs))
		for _, s := range runs {
			if s.NsPerOp > 0 {
				ns = append(ns, s.NsPerOp)
			}
			if s.BytesPerOp > 0 {
				bytes = append(bytes, s.BytesPerOp)
			}
			if s.AllocsPerOp > 0 {
				allocs = append(allocs, s.AllocsPerOp)
			}
		}
		out[name] = benchStats{
			Samples:      len(runs),
			MedianNs:     median(ns),
			MedianBytes:  median(bytes),
			MedianAllocs: median(allocs),
		}
	}
	return out
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	ys := make([]float64, len(xs))
	copy(ys, xs)
	sort.Float64s(ys)
	mid := len(ys) / 2
	if len(ys)%2 == 1 {
		return ys[mid]
	}
	return (ys[mid-1] + ys[mid]) / 2.0
}

func compareMetric(name, metric string, base, head, maxRegression float64) bool {
	// Zero/zero is valid for some metrics (e.g. B/op and allocs/op on no-edit fast paths).
	if base == 0 && head == 0 {
		fmt.Printf("%s\t%s\t%s\t%s\t%+.2f%%\tOK\n",
			name, metric, fmtFloat(base), fmtFloat(head), 0.0)
		return false
	}
	if base <= 0 || head <= 0 {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\tFAIL (missing metric)\n",
			name, metric, fmtFloat(base), fmtFloat(head), "-")
		return true
	}

	delta := (head / base) - 1.0
	status := "OK"
	failed := false
	if delta > maxRegression {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s\t%s\t%s\t%s\t%+.2f%%\t%s\n",
		name, metric, fmtFloat(base), fmtFloat(head), delta*100.0, status)
	return failed
}

func parseMaxRSS(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "Maximum resident set size (kbytes):") {
			continue
		}
		idx := strings.LastIndex(line, ":")
		if idx < 0 || idx+1 >= len(line) {
			return 0, fmt.Errorf("unexpected max RSS line format: %q", line)
		}
		v, err := strconv.ParseInt(strings.TrimSpace(line[idx+1:]), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse max RSS %q: %w", line, err)
		}
		return v, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("max RSS line not found in %s", path)
}

func fmtFloat(v float64) string {
	if v == 0 {
		return "0"
	}
	if v >= 1000 {
		return fmt.Sprintf("%.0f", v)
	}
	if v >= 10 {
		return fmt.Sprintf("%.2f", v)
	}
	return fmt.Sprintf("%.4f", v)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
