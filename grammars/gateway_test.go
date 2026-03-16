package grammars

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// DefaultPolicy
// ---------------------------------------------------------------------------

func TestDefaultPolicyValues(t *testing.T) {
	p := DefaultPolicy()

	if p.LargeFileThreshold != 256*1024 {
		t.Errorf("LargeFileThreshold = %d, want %d", p.LargeFileThreshold, 256*1024)
	}
	if p.MaxConcurrent != runtime.GOMAXPROCS(0) {
		t.Errorf("MaxConcurrent = %d, want %d", p.MaxConcurrent, runtime.GOMAXPROCS(0))
	}
	if p.ChannelBuffer != p.MaxConcurrent+1 {
		t.Errorf("ChannelBuffer = %d, want %d", p.ChannelBuffer, p.MaxConcurrent+1)
	}

	wantDirs := map[string]bool{
		".git": true, ".graft": true, ".hg": true,
		".svn": true, "vendor": true, "node_modules": true,
	}
	for _, d := range p.SkipDirs {
		if !wantDirs[d] {
			t.Errorf("unexpected SkipDir: %s", d)
		}
		delete(wantDirs, d)
	}
	for d := range wantDirs {
		t.Errorf("missing SkipDir: %s", d)
	}

	wantExts := map[string]bool{
		".min.js": true, ".min.css": true, ".map": true, ".wasm": true,
	}
	for _, e := range p.SkipExtensions {
		if !wantExts[e] {
			t.Errorf("unexpected SkipExtension: %s", e)
		}
		delete(wantExts, e)
	}
	for e := range wantExts {
		t.Errorf("missing SkipExtension: %s", e)
	}
}

func TestDefaultPolicyEnvThreshold(t *testing.T) {
	t.Setenv("GTS_LARGE_FILE_THRESHOLD", "1024")
	// Force re-read by calling DefaultPolicy (it reads env each call).
	p := DefaultPolicy()
	if p.LargeFileThreshold != 1024 {
		t.Errorf("LargeFileThreshold = %d, want 1024", p.LargeFileThreshold)
	}
}

func TestDefaultPolicyEnvMaxConcurrent(t *testing.T) {
	t.Setenv("GTS_MAX_CONCURRENT", "3")
	p := DefaultPolicy()
	if p.MaxConcurrent != 3 {
		t.Errorf("MaxConcurrent = %d, want 3", p.MaxConcurrent)
	}
	if p.ChannelBuffer != 4 {
		t.Errorf("ChannelBuffer = %d, want 4", p.ChannelBuffer)
	}
}

func TestDefaultPolicyEnvInvalid(t *testing.T) {
	t.Setenv("GTS_LARGE_FILE_THRESHOLD", "not-a-number")
	t.Setenv("GTS_MAX_CONCURRENT", "bad")
	p := DefaultPolicy()
	// Should fall back to defaults.
	if p.LargeFileThreshold != 256*1024 {
		t.Errorf("LargeFileThreshold = %d, want default", p.LargeFileThreshold)
	}
	if p.MaxConcurrent != runtime.GOMAXPROCS(0) {
		t.Errorf("MaxConcurrent = %d, want default", p.MaxConcurrent)
	}
}

// ---------------------------------------------------------------------------
// ParsedFile.Close
// ---------------------------------------------------------------------------

func TestParsedFileCloseNilTree(t *testing.T) {
	pf := &ParsedFile{
		Path:   "test.go",
		Source: []byte("package main"),
	}
	// Should not panic.
	pf.Close()
	if pf.Source != nil {
		t.Error("Source should be nil after Close")
	}
}

func TestParsedFileCloseDoubleClose(t *testing.T) {
	src := []byte("package main\n")
	tree, err := ParseFilePooled("test.go", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	pf := &ParsedFile{
		Path:   "test.go",
		Tree:   tree,
		Source: src,
	}
	pf.Close()
	// Second close should not panic.
	pf.Close()

	if pf.Tree != nil {
		t.Error("Tree should be nil after Close")
	}
	if pf.Source != nil {
		t.Error("Source should be nil after Close")
	}
}

func TestParsedFileCloseNilReceiver(t *testing.T) {
	var pf *ParsedFile
	// Should not panic.
	pf.Close()
}

// ---------------------------------------------------------------------------
// WalkAndParse — Go files
// ---------------------------------------------------------------------------

func TestWalkAndParseGoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(dir, "lib.go"), "package main\n\nfunc helper() int { return 42 }\n")

	policy := DefaultPolicy()
	policy.MaxConcurrent = 2
	policy.ChannelBuffer = 3

	ch, statsFn := WalkAndParse(context.Background(), dir, policy)

	var results []ParsedFile
	for pf := range ch {
		if pf.Err != nil {
			t.Errorf("unexpected error for %s: %v", pf.Path, pf.Err)
		}
		results = append(results, pf)
	}

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	for i := range results {
		pf := &results[i]
		if pf.Tree == nil {
			t.Errorf("result %d (%s): Tree is nil", i, pf.Path)
			continue
		}
		root := pf.Tree.RootNode()
		if root == nil {
			t.Errorf("result %d (%s): RootNode is nil", i, pf.Path)
			continue
		}
		if got := pf.Tree.NodeType(root); got != "source_file" {
			t.Errorf("result %d (%s): root type = %q, want source_file", i, pf.Path, got)
		}
		if pf.Lang == nil {
			t.Errorf("result %d (%s): Lang is nil", i, pf.Path)
		}
		if pf.Lang != nil && pf.Lang.Name != "go" {
			t.Errorf("result %d (%s): Lang.Name = %q, want go", i, pf.Path, pf.Lang.Name)
		}
		if len(pf.Source) == 0 {
			t.Errorf("result %d (%s): Source is empty", i, pf.Path)
		}
		pf.Close()
	}

	stats := statsFn()
	if stats.FilesFound != 2 {
		t.Errorf("FilesFound = %d, want 2", stats.FilesFound)
	}
	if stats.FilesParsed != 2 {
		t.Errorf("FilesParsed = %d, want 2", stats.FilesParsed)
	}
	if stats.FilesFailed != 0 {
		t.Errorf("FilesFailed = %d, want 0", stats.FilesFailed)
	}
	if stats.BytesParsed == 0 {
		t.Error("BytesParsed should be > 0")
	}
}

// ---------------------------------------------------------------------------
// SkipDirs
// ---------------------------------------------------------------------------

func TestWalkAndParseSkipDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "root.go"), "package main\n")

	// These should be skipped.
	gitDir := filepath.Join(dir, ".git")
	os.MkdirAll(gitDir, 0o755)
	writeFile(t, filepath.Join(gitDir, "config.go"), "package git\n")

	vendorDir := filepath.Join(dir, "vendor")
	os.MkdirAll(vendorDir, 0o755)
	writeFile(t, filepath.Join(vendorDir, "dep.go"), "package dep\n")

	nodeDir := filepath.Join(dir, "node_modules")
	os.MkdirAll(nodeDir, 0o755)
	writeFile(t, filepath.Join(nodeDir, "index.js"), "module.exports = {};\n")

	policy := DefaultPolicy()
	policy.MaxConcurrent = 1
	policy.ChannelBuffer = 2
	ch, statsFn := WalkAndParse(context.Background(), dir, policy)

	var paths []string
	for pf := range ch {
		paths = append(paths, pf.Path)
		pf.Close()
	}

	if len(paths) != 1 {
		t.Fatalf("got %d files, want 1 (only root.go); paths: %v", len(paths), paths)
	}
	if filepath.Base(paths[0]) != "root.go" {
		t.Errorf("expected root.go, got %s", paths[0])
	}

	stats := statsFn()
	if stats.FilesFound != 1 {
		t.Errorf("FilesFound = %d, want 1", stats.FilesFound)
	}
}

// ---------------------------------------------------------------------------
// SkipExtensions
// ---------------------------------------------------------------------------

func TestWalkAndParseSkipExtensions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app.js"), "var x = 1;\n")
	writeFile(t, filepath.Join(dir, "app.min.js"), "var x=1;\n")
	writeFile(t, filepath.Join(dir, "style.min.css"), "body{}\n")
	writeFile(t, filepath.Join(dir, "data.wasm"), "\x00\x61\x73\x6d")
	writeFile(t, filepath.Join(dir, "source.map"), "{}")

	policy := DefaultPolicy()
	policy.MaxConcurrent = 1
	policy.ChannelBuffer = 2
	ch, statsFn := WalkAndParse(context.Background(), dir, policy)

	var paths []string
	for pf := range ch {
		paths = append(paths, filepath.Base(pf.Path))
		pf.Close()
	}

	if len(paths) != 1 {
		t.Fatalf("got %d files, want 1; paths: %v", len(paths), paths)
	}
	if paths[0] != "app.js" {
		t.Errorf("expected app.js, got %s", paths[0])
	}

	stats := statsFn()
	if stats.FilesFiltered < 1 {
		t.Errorf("FilesFiltered = %d, want >= 1", stats.FilesFiltered)
	}
}

// ---------------------------------------------------------------------------
// ShouldParse hook
// ---------------------------------------------------------------------------

func TestWalkAndParseShouldParse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "include.go"), "package main\n")
	writeFile(t, filepath.Join(dir, "exclude.go"), "package main\n")

	policy := DefaultPolicy()
	policy.MaxConcurrent = 1
	policy.ChannelBuffer = 2
	policy.ShouldParse = func(path string, size int64, modTime time.Time) bool {
		return filepath.Base(path) == "include.go"
	}

	ch, statsFn := WalkAndParse(context.Background(), dir, policy)

	var paths []string
	for pf := range ch {
		paths = append(paths, filepath.Base(pf.Path))
		pf.Close()
	}

	if len(paths) != 1 {
		t.Fatalf("got %d files, want 1; paths: %v", len(paths), paths)
	}
	if paths[0] != "include.go" {
		t.Errorf("expected include.go, got %s", paths[0])
	}

	stats := statsFn()
	if stats.FilesFiltered != 1 {
		t.Errorf("FilesFiltered = %d, want 1", stats.FilesFiltered)
	}
}

// ---------------------------------------------------------------------------
// Empty directory
// ---------------------------------------------------------------------------

func TestWalkAndParseEmptyDir(t *testing.T) {
	dir := t.TempDir()

	policy := DefaultPolicy()
	policy.MaxConcurrent = 1
	policy.ChannelBuffer = 2
	ch, statsFn := WalkAndParse(context.Background(), dir, policy)

	count := 0
	for range ch {
		count++
	}

	if count != 0 {
		t.Errorf("got %d results from empty dir, want 0", count)
	}

	stats := statsFn()
	if stats.FilesFound != 0 {
		t.Errorf("FilesFound = %d, want 0", stats.FilesFound)
	}
	if stats.FilesParsed != 0 {
		t.Errorf("FilesParsed = %d, want 0", stats.FilesParsed)
	}
}

// ---------------------------------------------------------------------------
// Context cancellation
// ---------------------------------------------------------------------------

func TestWalkAndParseCancellation(t *testing.T) {
	dir := t.TempDir()
	// Create enough files so that cancellation has a chance to fire.
	for i := 0; i < 20; i++ {
		writeFile(t, filepath.Join(dir, filepath.Base(t.TempDir())+".go"),
			"package main\n")
	}

	ctx, cancel := context.WithCancel(context.Background())

	policy := DefaultPolicy()
	policy.MaxConcurrent = 1
	policy.ChannelBuffer = 2
	ch, _ := WalkAndParse(ctx, dir, policy)

	// Read one result then cancel.
	<-ch
	cancel()

	// Drain remaining — channel must close eventually.
	for pf := range ch {
		pf.Close()
	}
}

// ---------------------------------------------------------------------------
// Large file handling
// ---------------------------------------------------------------------------

func TestWalkAndParseLargeFile(t *testing.T) {
	dir := t.TempDir()

	// Small file.
	writeFile(t, filepath.Join(dir, "small.go"), "package main\n")

	// "Large" file — we set a tiny threshold to trigger the large-file path.
	large := "package main\n\nfunc big() {}\n"
	writeFile(t, filepath.Join(dir, "big.go"), large)

	policy := DefaultPolicy()
	policy.LargeFileThreshold = 10 // anything > 10 bytes is "large"
	policy.MaxConcurrent = 2
	policy.ChannelBuffer = 3

	var largeFileSeen bool
	policy.OnProgress = func(ev ProgressEvent) {
		if ev.Phase == "large_file" {
			largeFileSeen = true
		}
	}

	ch, statsFn := WalkAndParse(context.Background(), dir, policy)

	for pf := range ch {
		if pf.Err != nil {
			t.Errorf("error for %s: %v", pf.Path, pf.Err)
		}
		pf.Close()
	}

	stats := statsFn()
	if stats.FilesParsed != 2 {
		t.Errorf("FilesParsed = %d, want 2", stats.FilesParsed)
	}
	if stats.LargeFiles < 1 {
		t.Errorf("LargeFiles = %d, want >= 1", stats.LargeFiles)
	}
	if !largeFileSeen {
		t.Error("OnProgress never received large_file event")
	}
}

// ---------------------------------------------------------------------------
// Progress callback
// ---------------------------------------------------------------------------

func TestWalkAndParseProgress(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package main\n")

	policy := DefaultPolicy()
	policy.MaxConcurrent = 1
	policy.ChannelBuffer = 2

	phases := map[string]int{}
	policy.OnProgress = func(ev ProgressEvent) {
		phases[ev.Phase]++
	}

	ch, statsFn := WalkAndParse(context.Background(), dir, policy)
	for pf := range ch {
		pf.Close()
	}
	_ = statsFn()

	for _, required := range []string{"walking", "parsing", "walk_complete", "done"} {
		if phases[required] == 0 {
			t.Errorf("missing progress phase: %s", required)
		}
	}
}

// ---------------------------------------------------------------------------
// helper
// ---------------------------------------------------------------------------

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
