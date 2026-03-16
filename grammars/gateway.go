package grammars

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/odvcencio/gotreesitter"
)

// ParsePolicy controls how WalkAndParse discovers and parses files.
type ParsePolicy struct {
	// LargeFileThreshold is the byte size above which a file is parsed with
	// exclusive access to all worker slots (serialized). Files at or above
	// this threshold are considered "large."
	LargeFileThreshold int64

	// MaxConcurrent limits the number of files parsed simultaneously.
	MaxConcurrent int

	// ChannelBuffer is the buffer size for the output channel. Must be at
	// least MaxConcurrent+1 to prevent deadlock when workers hold semaphore
	// slots while sending on the channel.
	ChannelBuffer int

	// SkipDirs lists directory base names to skip entirely during the walk.
	SkipDirs []string

	// SkipExtensions lists file suffixes (e.g., ".min.js") to skip.
	SkipExtensions []string

	// ShouldParse, if non-nil, is called for each candidate file after
	// language detection. Return false to skip the file.
	ShouldParse func(path string, size int64, modTime time.Time) bool

	// OnProgress, if non-nil, receives progress events during the walk.
	OnProgress func(ProgressEvent)
}

// ProgressEvent reports progress during WalkAndParse.
type ProgressEvent struct {
	Phase   string // "walking", "parsing", "large_file", "walk_complete", "done"
	Path    string
	Size    int64
	FileNum int
	Total   int
	Message string
}

// ParsedFile is a single result from WalkAndParse. The consumer MUST call
// Close() when done to release the tree's arena memory.
type ParsedFile struct {
	Path   string
	Tree   *gotreesitter.BoundTree // consumer MUST call Close()
	Lang   *LangEntry
	Source []byte
	Size   int64
	Err    error
	IsRead bool // false = I/O error during read, true = parse error (file was read OK)
}

// Close releases the BoundTree and nils Source to free memory. It is safe to
// call Close multiple times and on a nil receiver.
func (pf *ParsedFile) Close() {
	if pf == nil {
		return
	}
	if pf.Tree != nil {
		pf.Tree.Release()
		pf.Tree = nil
	}
	pf.Source = nil
}

// WalkStats summarizes the results of a WalkAndParse run.
type WalkStats struct {
	FilesFound    int
	FilesParsed   int
	FilesFailed   int
	FilesFiltered int
	LargeFiles    int
	BytesParsed   int64
}

// DefaultPolicy returns a ParsePolicy with sensible defaults:
//   - LargeFileThreshold: 256 KB (overridable via GTS_LARGE_FILE_THRESHOLD)
//   - MaxConcurrent: GOMAXPROCS (overridable via GTS_MAX_CONCURRENT)
//   - ChannelBuffer: MaxConcurrent + 1
//   - SkipDirs: .git, .graft, .hg, .svn, vendor, node_modules
//   - SkipExtensions: .min.js, .min.css, .map, .wasm
func DefaultPolicy() ParsePolicy {
	threshold := int64(256 * 1024)
	if v := os.Getenv("GTS_LARGE_FILE_THRESHOLD"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			threshold = n
		}
	}

	maxConc := runtime.GOMAXPROCS(0)
	if v := os.Getenv("GTS_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConc = n
		}
	}

	return ParsePolicy{
		LargeFileThreshold: threshold,
		MaxConcurrent:      maxConc,
		ChannelBuffer:      maxConc + 1,
		SkipDirs:           []string{".git", ".graft", ".hg", ".svn", "vendor", "node_modules"},
		SkipExtensions:     []string{".min.js", ".min.css", ".map", ".wasm"},
	}
}

// WalkAndParse walks root, discovers source files, and streams parsed results
// on the returned channel. The caller must drain the channel to completion.
// The returned function provides aggregate statistics and blocks until the
// walk is fully complete.
//
// Pipeline:
//  1. Walk with filepath.WalkDir, skipping SkipDirs and SkipExtensions.
//  2. Detect language; skip unknown files.
//  3. Call ShouldParse hook if set; skip if it returns false.
//  4. Normal files (< LargeFileThreshold): acquire 1 semaphore slot, read+parse
//     in a goroutine, send result, then release.
//  5. Large files (>= LargeFileThreshold): acquire ALL slots, parse inline,
//     send result, release ALL slots.
//
// Workers release the semaphore AFTER sending on the channel, not before.
// ChannelBuffer is MaxConcurrent+1 to prevent deadlock.
func WalkAndParse(ctx context.Context, root string, policy ParsePolicy) (<-chan ParsedFile, func() WalkStats) {
	ch := make(chan ParsedFile, policy.ChannelBuffer)

	var stats WalkStats
	var mu sync.Mutex
	var bytesTotal int64

	sem := make(chan struct{}, policy.MaxConcurrent)
	var wg sync.WaitGroup

	skipDirSet := make(map[string]struct{}, len(policy.SkipDirs))
	for _, d := range policy.SkipDirs {
		skipDirSet[d] = struct{}{}
	}

	var filesFound int32

	progress := func(ev ProgressEvent) {
		if policy.OnProgress != nil {
			policy.OnProgress(ev)
		}
	}

	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(ch)

		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil // skip inaccessible entries
			}

			if d.IsDir() {
				base := filepath.Base(p)
				if _, skip := skipDirSet[base]; skip && p != root {
					return filepath.SkipDir
				}
				return nil
			}

			// Skip non-regular files (symlinks, devices, etc.)
			if !d.Type().IsRegular() {
				return nil
			}

			// Check skip extensions.
			name := d.Name()
			for _, ext := range policy.SkipExtensions {
				if strings.HasSuffix(name, ext) {
					mu.Lock()
					stats.FilesFiltered++
					mu.Unlock()
					return nil
				}
			}

			// Detect language.
			lang := DetectLanguage(name)
			if lang == nil {
				return nil
			}

			num := int(atomic.AddInt32(&filesFound, 1))

			// Get file info for ShouldParse.
			info, infoErr := d.Info()
			if infoErr != nil {
				return nil
			}
			fileSize := info.Size()

			mu.Lock()
			stats.FilesFound++
			mu.Unlock()

			// ShouldParse hook.
			if policy.ShouldParse != nil {
				if !policy.ShouldParse(p, fileSize, info.ModTime()) {
					mu.Lock()
					stats.FilesFiltered++
					mu.Unlock()
					return nil
				}
			}

			progress(ProgressEvent{
				Phase:   "walking",
				Path:    p,
				Size:    fileSize,
				FileNum: num,
			})

			isLarge := fileSize >= policy.LargeFileThreshold

			if isLarge {
				mu.Lock()
				stats.LargeFiles++
				mu.Unlock()

				progress(ProgressEvent{
					Phase:   "large_file",
					Path:    p,
					Size:    fileSize,
					FileNum: num,
					Message: "acquiring exclusive access",
				})

				// Acquire ALL semaphore slots.
				for i := 0; i < policy.MaxConcurrent; i++ {
					sem <- struct{}{}
				}

				// Wait for in-flight workers to finish before parsing inline.
				wg.Wait()

				pf := parseOne(p, lang, fileSize)
				if pf.Err != nil {
					mu.Lock()
					stats.FilesFailed++
					mu.Unlock()
				} else {
					mu.Lock()
					stats.FilesParsed++
					mu.Unlock()
					atomic.AddInt64(&bytesTotal, fileSize)
				}

				progress(ProgressEvent{
					Phase:   "parsing",
					Path:    p,
					Size:    fileSize,
					FileNum: num,
				})

				ch <- pf

				// Release ALL slots.
				for i := 0; i < policy.MaxConcurrent; i++ {
					<-sem
				}
			} else {
				// Normal file: acquire 1 slot.
				sem <- struct{}{}
				wg.Add(1)

				go func(filePath string, entry *LangEntry, size int64, fileNum int) {
					defer wg.Done()

					progress(ProgressEvent{
						Phase:   "parsing",
						Path:    filePath,
						Size:    size,
						FileNum: fileNum,
					})

					pf := parseOne(filePath, entry, size)
					if pf.Err != nil {
						mu.Lock()
						stats.FilesFailed++
						mu.Unlock()
					} else {
						mu.Lock()
						stats.FilesParsed++
						mu.Unlock()
						atomic.AddInt64(&bytesTotal, size)
					}

					// Send BEFORE releasing semaphore (critical for backpressure).
					ch <- pf
					<-sem
				}(p, lang, fileSize, num)
			}

			return nil
		})

		// Walk complete — wait for all in-flight goroutines.
		progress(ProgressEvent{Phase: "walk_complete"})
		wg.Wait()

		mu.Lock()
		stats.BytesParsed = atomic.LoadInt64(&bytesTotal)
		mu.Unlock()

		progress(ProgressEvent{Phase: "done"})
	}()

	statsFn := func() WalkStats {
		<-done
		mu.Lock()
		defer mu.Unlock()
		return stats
	}

	return ch, statsFn
}

// parseOne reads and parses a single file, returning a ParsedFile.
func parseOne(path string, lang *LangEntry, size int64) ParsedFile {
	src, err := os.ReadFile(path)
	if err != nil {
		return ParsedFile{
			Path:   path,
			Lang:   lang,
			Size:   size,
			Err:    err,
			IsRead: false,
		}
	}

	tree, err := ParseFilePooled(filepath.Base(path), src)
	if err != nil {
		return ParsedFile{
			Path:   path,
			Lang:   lang,
			Source: src,
			Size:   size,
			Err:    err,
			IsRead: true,
		}
	}

	return ParsedFile{
		Path:   path,
		Tree:   tree,
		Lang:   lang,
		Source: src,
		Size:   size,
		IsRead: true,
	}
}
