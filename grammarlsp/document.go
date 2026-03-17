package grammarlsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Document struct {
	URI        string
	Source     string
	GoCode     string
	ShadowPath string
	Map        *SourceMap
	Version    int
	Extension  *Extension
}

type DocumentManager struct {
	mu         sync.RWMutex
	docs       map[string]*Document
	shadowDir  string
	extensions map[string]*Extension // file extension -> Extension
}

func NewDocumentManager(shadowDir string, extensions []Extension) *DocumentManager {
	os.MkdirAll(shadowDir, 0755)
	extMap := make(map[string]*Extension, len(extensions))
	for i := range extensions {
		extMap[extensions[i].FileExtension] = &extensions[i]
	}
	return &DocumentManager{
		docs:       make(map[string]*Document),
		shadowDir:  shadowDir,
		extensions: extMap,
	}
}

func (dm *DocumentManager) ExtensionFor(uri string) *Extension {
	for ext, e := range dm.extensions {
		if strings.HasSuffix(uri, ext) {
			return e
		}
	}
	return nil
}

func (dm *DocumentManager) IsManaged(uri string) bool {
	return dm.ExtensionFor(uri) != nil
}

func (dm *DocumentManager) Open(uri, source string) error {
	ext := dm.ExtensionFor(uri)
	if ext == nil {
		return fmt.Errorf("no extension for %s", uri)
	}

	goCode, err := ext.Transpile([]byte(source))
	if err != nil {
		return fmt.Errorf("transpile: %w", err)
	}

	// Build source map from line diff
	srcLines := strings.Split(source, "\n")
	dstLines := strings.Split(goCode, "\n")
	sm := BuildFromDiff(srcLines, dstLines)

	// Write shadow file
	shadowName := filepath.Base(strings.TrimSuffix(uriToPath(uri), ext.FileExtension)) + ".go"
	shadowPath := filepath.Join(dm.shadowDir, shadowName)
	if err := os.WriteFile(shadowPath, []byte(goCode), 0644); err != nil {
		return fmt.Errorf("write shadow: %w", err)
	}

	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.docs[uri] = &Document{
		URI:        uri,
		Source:     source,
		GoCode:     goCode,
		ShadowPath: shadowPath,
		Map:        sm,
		Version:    1,
		Extension:  ext,
	}
	return nil
}

func (dm *DocumentManager) Update(uri, source string) error {
	ext := dm.ExtensionFor(uri)
	if ext == nil {
		return fmt.Errorf("no extension for %s", uri)
	}

	goCode, err := ext.Transpile([]byte(source))
	if err != nil {
		return fmt.Errorf("transpile: %w", err)
	}

	srcLines := strings.Split(source, "\n")
	dstLines := strings.Split(goCode, "\n")
	sm := BuildFromDiff(srcLines, dstLines)

	dm.mu.Lock()
	defer dm.mu.Unlock()
	doc, ok := dm.docs[uri]
	if !ok {
		return fmt.Errorf("document not open: %s", uri)
	}
	doc.Source = source
	doc.GoCode = goCode
	doc.Map = sm
	doc.Version++
	return os.WriteFile(doc.ShadowPath, []byte(goCode), 0644)
}

func (dm *DocumentManager) Close(uri string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if doc, ok := dm.docs[uri]; ok {
		os.Remove(doc.ShadowPath)
		delete(dm.docs, uri)
	}
}

func (dm *DocumentManager) Get(uri string) (*Document, bool) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	doc, ok := dm.docs[uri]
	return doc, ok
}

func uriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}
