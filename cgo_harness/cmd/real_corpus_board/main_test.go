package main

import "testing"

func TestBuildBoardAggregatesL3AndL4(t *testing.T) {
	manifest := &corpusManifest{
		Languages: []string{"go", "rust", "yaml"},
		Entries: []corpusManifestEntry{
			{Language: "go", Bucket: "medium", SHA256: "m1", OutputPath: "/tmp/go/medium__a.go"},
			{Language: "go", Bucket: "large", SHA256: "l1", OutputPath: "/tmp/go/large__a.go"},
			{Language: "rust", Bucket: "medium", SHA256: "m2", OutputPath: "/tmp/rust/medium__a.rs"},
			{Language: "yaml", Bucket: "large", SHA256: "l2", OutputPath: "/tmp/yaml/large__a.yaml"},
		},
	}
	results := []parityResult{
		{Language: "go", FileID: "medium__a.go", SourceSHA256: "m1", Pass: true},
		{Language: "go", FileID: "large__a.go", SourceSHA256: "l1", Pass: false, Category: "error"},
		{Language: "rust", FileID: "medium__a.rs", SourceSHA256: "m2", Pass: true},
		{Language: "yaml", FileID: "large__a.yaml", SourceSHA256: "l2", Pass: true},
	}

	board := buildBoard("manifest.json", "results.jsonl", manifest, results, boardOptions{})

	if got, want := board.L3.ApplicableLangs, 2; got != want {
		t.Fatalf("L3.ApplicableLangs = %d, want %d", got, want)
	}
	if got, want := board.L3.GreenLangs, 2; got != want {
		t.Fatalf("L3.GreenLangs = %d, want %d", got, want)
	}
	if got, want := board.L4.ApplicableLangs, 2; got != want {
		t.Fatalf("L4.ApplicableLangs = %d, want %d", got, want)
	}
	if got, want := board.L4.GreenLangs, 1; got != want {
		t.Fatalf("L4.GreenLangs = %d, want %d", got, want)
	}

	for _, lang := range board.Languages {
		switch lang.Language {
		case "go":
			if lang.L3.Status != "green" {
				t.Fatalf("go L3 status = %q, want green", lang.L3.Status)
			}
			if lang.L4.Status != "red" {
				t.Fatalf("go L4 status = %q, want red", lang.L4.Status)
			}
		case "rust":
			if lang.L3.Status != "green" {
				t.Fatalf("rust L3 status = %q, want green", lang.L3.Status)
			}
			if lang.L4.Status != "na" {
				t.Fatalf("rust L4 status = %q, want na", lang.L4.Status)
			}
		case "yaml":
			if lang.L3.Status != "na" {
				t.Fatalf("yaml L3 status = %q, want na", lang.L3.Status)
			}
			if lang.L4.Status != "green" {
				t.Fatalf("yaml L4 status = %q, want green", lang.L4.Status)
			}
		}
	}
}

func TestSummarizeLevelFlagsMissingFiles(t *testing.T) {
	entries := []corpusManifestEntry{
		{Language: "go", Bucket: "medium", SHA256: "a", OutputPath: "/tmp/go/medium__a.go"},
	}

	summary := summarizeLevel(entries, "go", map[string]parityResult{})
	if summary.Status != "red" {
		t.Fatalf("status = %q, want red", summary.Status)
	}
	if len(summary.MissingFiles) != 1 || summary.MissingFiles[0] != "medium__a.go" {
		t.Fatalf("missing files = %#v, want medium__a.go", summary.MissingFiles)
	}
}

func TestSummarizeLevelExcludesOracleErrorFiles(t *testing.T) {
	entries := []corpusManifestEntry{
		{Language: "awk", Bucket: "medium", SHA256: "a", OutputPath: "/tmp/awk/medium__a.awk"},
	}
	results := map[string]parityResult{
		resultKey("awk", "medium__a.awk"): {
			Language:       "awk",
			FileID:         "medium__a.awk",
			SourceSHA256:   "a",
			Pass:           false,
			Category:       "type",
			CRootType:      "program",
			CRootHasError:  true,
			GoRootType:     "ERROR",
			GoRootHasError: true,
		},
	}

	summary := summarizeLevel(entries, "awk", results)
	if got, want := summary.Status, "na"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if got, want := summary.TotalFiles, 0; got != want {
		t.Fatalf("TotalFiles = %d, want %d", got, want)
	}
	if got, want := len(summary.ExcludedFiles), 1; got != want || summary.ExcludedFiles[0] != "medium__a.awk" {
		t.Fatalf("ExcludedFiles = %#v, want medium__a.awk", summary.ExcludedFiles)
	}
}

func TestBuildBoardSkipsOracleErrorFilesFromAggregates(t *testing.T) {
	manifest := &corpusManifest{
		Languages: []string{"awk", "go"},
		Entries: []corpusManifestEntry{
			{Language: "awk", Bucket: "medium", SHA256: "a1", OutputPath: "/tmp/awk/medium__a.awk"},
			{Language: "go", Bucket: "medium", SHA256: "g1", OutputPath: "/tmp/go/medium__a.go"},
		},
	}
	results := []parityResult{
		{
			Language:       "awk",
			FileID:         "medium__a.awk",
			SourceSHA256:   "a1",
			Pass:           false,
			Category:       "type",
			CRootType:      "program",
			CRootHasError:  true,
			GoRootType:     "ERROR",
			GoRootHasError: true,
		},
		{Language: "go", FileID: "medium__a.go", SourceSHA256: "g1", Pass: true},
	}

	board := buildBoard("manifest.json", "results.jsonl", manifest, results, boardOptions{})
	if got, want := board.L3.ApplicableLangs, 1; got != want {
		t.Fatalf("L3.ApplicableLangs = %d, want %d", got, want)
	}
	if got, want := board.L3.GreenLangs, 1; got != want {
		t.Fatalf("L3.GreenLangs = %d, want %d", got, want)
	}
	for _, lang := range board.Languages {
		switch lang.Language {
		case "awk":
			if got, want := lang.L3.Status, "na"; got != want {
				t.Fatalf("awk L3 status = %q, want %q", got, want)
			}
		case "go":
			if got, want := lang.L3.Status, "green"; got != want {
				t.Fatalf("go L3 status = %q, want %q", got, want)
			}
		}
	}
}

func TestSummarizeLevelExcludesOracleTimeoutFiles(t *testing.T) {
	entries := []corpusManifestEntry{
		{Language: "d", Bucket: "large", SHA256: "d1", OutputPath: "/tmp/d/large__date.d"},
	}
	results := map[string]parityResult{
		resultKey("d", "large__date.d"): {
			Language:     "d",
			FileID:       "large__date.d",
			SourceSHA256: "d1",
			Pass:         false,
			Category:     "oracle_timeout",
			Error:        "C oracle parse aborted after 5000ms timeout",
		},
	}

	summary := summarizeLevel(entries, "d", results)
	if got, want := summary.Status, "na"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if got, want := summary.TotalFiles, 0; got != want {
		t.Fatalf("TotalFiles = %d, want %d", got, want)
	}
	if got, want := len(summary.ExcludedFiles), 1; got != want || summary.ExcludedFiles[0] != "large__date.d" {
		t.Fatalf("ExcludedFiles = %#v, want large__date.d", summary.ExcludedFiles)
	}
}

func TestBuildBoardSkipsOracleTimeoutFilesFromAggregates(t *testing.T) {
	manifest := &corpusManifest{
		Languages: []string{"d", "go"},
		Entries: []corpusManifestEntry{
			{Language: "d", Bucket: "large", SHA256: "d1", OutputPath: "/tmp/d/large__date.d"},
			{Language: "go", Bucket: "large", SHA256: "g1", OutputPath: "/tmp/go/large__a.go"},
		},
	}
	results := []parityResult{
		{
			Language:     "d",
			FileID:       "large__date.d",
			SourceSHA256: "d1",
			Pass:         false,
			Category:     "oracle_timeout",
			Error:        "C oracle parse aborted after 5000ms timeout",
		},
		{Language: "go", FileID: "large__a.go", SourceSHA256: "g1", Pass: true},
	}

	board := buildBoard("manifest.json", "results.jsonl", manifest, results, boardOptions{})
	if got, want := board.L4.ApplicableLangs, 1; got != want {
		t.Fatalf("L4.ApplicableLangs = %d, want %d", got, want)
	}
	if got, want := board.L4.GreenLangs, 1; got != want {
		t.Fatalf("L4.GreenLangs = %d, want %d", got, want)
	}
	for _, lang := range board.Languages {
		switch lang.Language {
		case "d":
			if got, want := lang.L4.Status, "na"; got != want {
				t.Fatalf("d L4 status = %q, want %q", got, want)
			}
		case "go":
			if got, want := lang.L4.Status, "green"; got != want {
				t.Fatalf("go L4 status = %q, want %q", got, want)
			}
		}
	}
}

func TestBuildBoardL4LimitSelectsLargestLanguages(t *testing.T) {
	manifest := &corpusManifest{
		Languages: []string{"go", "rust", "yaml", "swift"},
		Entries: []corpusManifestEntry{
			{Language: "go", Bucket: "large", Bytes: 900, SHA256: "g1", OutputPath: "/tmp/go/large__a.go"},
			{Language: "rust", Bucket: "large", Bytes: 400, SHA256: "r1", OutputPath: "/tmp/rust/large__a.rs"},
			{Language: "yaml", Bucket: "large", Bytes: 700, SHA256: "y1", OutputPath: "/tmp/yaml/large__a.yaml"},
			{Language: "swift", Bucket: "medium", Bytes: 600, SHA256: "s1", OutputPath: "/tmp/swift/medium__a.swift"},
		},
	}
	results := []parityResult{
		{Language: "go", FileID: "large__a.go", SourceSHA256: "g1", Pass: true},
		{Language: "rust", FileID: "large__a.rs", SourceSHA256: "r1", Pass: true},
		{Language: "yaml", FileID: "large__a.yaml", SourceSHA256: "y1", Pass: true},
		{Language: "swift", FileID: "medium__a.swift", SourceSHA256: "s1", Pass: true},
	}

	board := buildBoard("manifest.json", "results.jsonl", manifest, results, boardOptions{L4Limit: 2})

	if got, want := board.L4.ApplicableLangs, 2; got != want {
		t.Fatalf("L4.ApplicableLangs = %d, want %d", got, want)
	}
	if got, want := board.L4SelectedLanguages, []string{"go", "yaml"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("L4SelectedLanguages = %#v, want %#v", got, want)
	}
	for _, lang := range board.Languages {
		switch lang.Language {
		case "go", "yaml":
			if lang.L4.Status != "green" {
				t.Fatalf("%s L4 status = %q, want green", lang.Language, lang.L4.Status)
			}
		case "rust":
			if lang.L4.Status != "na" {
				t.Fatalf("rust L4 status = %q, want na", lang.L4.Status)
			}
		}
	}
}

func TestBuildBoardL4ExplicitLanguages(t *testing.T) {
	manifest := &corpusManifest{
		Languages: []string{"go", "rust"},
		Entries: []corpusManifestEntry{
			{Language: "go", Bucket: "large", Bytes: 100, SHA256: "g1", OutputPath: "/tmp/go/large__a.go"},
			{Language: "rust", Bucket: "large", Bytes: 100, SHA256: "r1", OutputPath: "/tmp/rust/large__a.rs"},
		},
	}
	results := []parityResult{
		{Language: "go", FileID: "large__a.go", SourceSHA256: "g1", Pass: true},
		{Language: "rust", FileID: "large__a.rs", SourceSHA256: "r1", Pass: false, Category: "shape"},
	}

	board := buildBoard("manifest.json", "results.jsonl", manifest, results, boardOptions{L4Languages: []string{"rust"}})

	if got, want := board.L4.ApplicableLangs, 1; got != want {
		t.Fatalf("L4.ApplicableLangs = %d, want %d", got, want)
	}
	for _, lang := range board.Languages {
		switch lang.Language {
		case "go":
			if lang.L4.Status != "na" {
				t.Fatalf("go L4 status = %q, want na", lang.L4.Status)
			}
		case "rust":
			if lang.L4.Status != "red" {
				t.Fatalf("rust L4 status = %q, want red", lang.L4.Status)
			}
		}
	}
}
