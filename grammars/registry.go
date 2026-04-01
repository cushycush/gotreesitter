// Package grammars provides built-in and extension tree-sitter grammars with
// lazy loading. Most built-in grammars are currently shipped as compressed
// ts2go blobs, while extension grammars can come from grammargen-generated
// loaders. Use AllLanguages to enumerate available grammars, DetectLanguage to
// match by file extension or shebang, or call individual language functions
// (e.g. GoLanguage()) for direct access.
package grammars

import (
	"path"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/odvcencio/gotreesitter"
)

// GrammarSource describes where a LangEntry's language loader comes from.
type GrammarSource string

const (
	GrammarSourceUnknown    GrammarSource = "unknown"
	GrammarSourceTS2GoBlob  GrammarSource = "ts2go_blob"
	GrammarSourceGrammargen GrammarSource = "grammargen"
)

// LangEntry holds a registered language with its grammar, extensions, and highlight query.
type LangEntry struct {
	Name               string
	Extensions         []string                      // e.g. [".go", ".mod"]
	Shebangs           []string                      // e.g. ["#!/usr/bin/env python"]
	Language           func() *gotreesitter.Language // lazy loader
	GrammarSource      GrammarSource                 // e.g. ts2go blob or grammargen
	HighlightQuery     string
	InheritHighlights  string                                                                 // language name to inherit highlight queries from (e.g. "javascript" for TypeScript)
	TagsQuery          string                                                                 // tree-sitter tags.scm query for symbol extraction
	TokenSourceFactory func(src []byte, lang *gotreesitter.Language) gotreesitter.TokenSource // nil = use DFA
	Quality            ParseQuality                                                           // populated by AuditParseSupport
}

var registry []LangEntry
var highlightInheritanceResolved bool

// registryMu guards registry, highlightInheritanceResolved, extIndex, and
// extensionAliases. Writers hold Lock(); readers hold RLock(). Never acquire
// registryMu while calling ensureBuiltinLanguagesRegistered — Register itself
// acquires the write lock, which would cause a deadlock.
var registryMu sync.RWMutex

// extIndex caches a suffix→LangEntry map for O(1) extension lookups in DetectLanguage.
// Invalidated (set to nil) whenever Register is called.
var extIndex map[string]*LangEntry

var (
	builtinRegistryOnce sync.Once
	builtinRegistryBusy atomic.Bool
)

func ensureBuiltinLanguagesRegistered() {
	builtinRegistryOnce.Do(func() {
		builtinRegistryBusy.Store(true)
		defer func() {
			builtinRegistryBusy.Store(false)
		}()
		registerBuiltinLanguages()
	})
}

// ensureReady guarantees that builtin languages are registered and the lazy
// metadata (highlight inheritance, extIndex) are up to date. Callers MUST NOT
// hold registryMu when calling this — ensureBuiltinLanguagesRegistered calls
// Register, which acquires the write lock.
func ensureReady() {
	ensureBuiltinLanguagesRegistered()

	registryMu.RLock()
	ready := highlightInheritanceResolved && extIndex != nil
	registryMu.RUnlock()

	if ready {
		return
	}

	// Slow path: acquire write lock and (re-)check before doing the work.
	registryMu.Lock()
	defer registryMu.Unlock()
	if !highlightInheritanceResolved {
		resolveHighlightInheritance()
	}
	if extIndex == nil {
		buildExtIndex()
	}
}

// Register adds a language to the registry. If an entry with the same name
// already exists, it is replaced so that grammar updates take effect.
func Register(entry LangEntry) {
	if !builtinRegistryBusy.Load() {
		ensureBuiltinLanguagesRegistered()
	}
	if !languageEnabled(entry.Name) {
		return
	}
	if entry.GrammarSource == "" {
		entry.GrammarSource = GrammarSourceUnknown
	}
	if entry.TokenSourceFactory == nil {
		entry.TokenSourceFactory = defaultTokenSourceFactory(entry.Name)
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	for i := range registry {
		if registry[i].Name == entry.Name {
			registry[i] = entry
			highlightInheritanceResolved = false
			extIndex = nil
			return
		}
	}
	registry = append(registry, entry)
	highlightInheritanceResolved = false
	extIndex = nil
}

// RegisterExtension registers a grammargen-based grammar extension with the
// language registry. This enables detection by file extension, markdown code
// fence highlighting, and LSP support. The language is generated lazily on
// first access.
//
// Usage from an extension package:
//
//	func init() {
//	    grammars.RegisterExtension(grammars.ExtensionEntry{
//	        Name:           "danmuji",
//	        Extensions:     []string{".dmj"},
//	        Aliases:        []string{"dmj"},
//	        GenerateLanguage: func() (*gotreesitter.Language, error) {
//	            return grammargen.GenerateLanguage(danmuji.Grammar())
//	        },
//	        HighlightQuery: danmuji.HighlightQueries(),
//	    })
//	}
type ExtensionEntry struct {
	Name              string
	Extensions        []string // file extensions: [".dmj", ".dingo", ".fw"]
	Aliases           []string // markdown fence aliases: ["dmj", "danmuji"]
	GenerateLanguage  func() (*gotreesitter.Language, error)
	HighlightQuery    string
	InheritHighlights string // parent language for highlight query composition (e.g. "go")
}

// RegisterExtension registers a grammar extension for file detection and
// markdown code fence highlighting.
func RegisterExtension(ext ExtensionEntry) {
	var cached *gotreesitter.Language
	loader := func() *gotreesitter.Language {
		if cached != nil {
			return cached
		}
		lang, err := ext.GenerateLanguage()
		if err != nil {
			return nil
		}
		cached = lang
		return lang
	}

	Register(LangEntry{
		Name:              ext.Name,
		Extensions:        ext.Extensions,
		Language:          loader,
		GrammarSource:     GrammarSourceGrammargen,
		HighlightQuery:    ext.HighlightQuery,
		InheritHighlights: ext.InheritHighlights,
	})

	// Register aliases for markdown fence resolution under the write lock so
	// readers in DetectLanguageByName see a consistent extensionAliases state.
	if len(ext.Aliases) > 0 {
		registryMu.Lock()
		for _, alias := range ext.Aliases {
			if alias != ext.Name {
				extensionAliases[alias] = ext.Name
			}
		}
		registryMu.Unlock()
	}
}

// extensionAliases maps markdown fence aliases to canonical names.
// Protected by registryMu.
var extensionAliases = map[string]string{}

// resolveHighlightInheritance composes highlight queries for languages that
// inherit from a parent. MUST be called with registryMu.Lock() held.
// Callers are responsible for ensuring builtins are registered first.
func resolveHighlightInheritance() {
	if highlightInheritanceResolved {
		return
	}
	highlightInheritanceResolved = true
	for i := range registry {
		parent := registry[i].InheritHighlights
		if parent == "" {
			continue
		}
		for j := range registry {
			if registry[j].Name == parent {
				// Prepend parent query so child overrides win (last match wins in tree-sitter).
				registry[i].HighlightQuery = registry[j].HighlightQuery + "\n" + registry[i].HighlightQuery
				break
			}
		}
	}
}

// buildExtIndex builds a suffix→LangEntry map from the current registry.
// MUST be called with registryMu.Lock() held.
func buildExtIndex() {
	idx := make(map[string]*LangEntry, len(registry)*2)
	for i := range registry {
		for _, ext := range registry[i].Extensions {
			// First registration wins; mirrors the O(n²) loop behaviour.
			if _, exists := idx[ext]; !exists {
				idx[ext] = &registry[i]
			}
		}
	}
	extIndex = idx
}

// DetectLanguage returns the LangEntry for a filename, or nil if unknown.
// Checks in order: exact filename match (linguist), registry extensions,
// then linguist extended extensions. Exact filenames take priority over
// suffix matching so that e.g. ".tmux.conf" resolves to bash rather than
// matching the generic ".conf" extension.
func DetectLanguage(filename string) *LangEntry {
	ensureReady()
	registryMu.RLock()
	defer registryMu.RUnlock()

	// 1. Exact filename match (e.g., "Makefile", "Dockerfile", ".bashrc",
	//    "nginx.conf"). Most specific, so checked first.
	base := path.Base(filename)
	if grammarName, ok := linguistFilenames[base]; ok {
		return lookupByName(grammarName)
	}

	// 2. Match by registry extensions — O(1) map lookup.
	// Collect up to 4 dot-suffixes from longest to shortest using a stack
	// array to avoid heap allocation (e.g. ".blade.php" before ".php").
	var suffixes [4]string
	nsuf := 0
	for i := len(base) - 1; i > 0 && nsuf < len(suffixes); i-- {
		if base[i] == '.' {
			suffixes[nsuf] = base[i:]
			nsuf++
		}
	}
	// suffixes[0] is shortest; check longest (highest index) first.
	for i := nsuf - 1; i >= 0; i-- {
		if entry, ok := extIndex[suffixes[i]]; ok {
			return entry
		}
	}

	// 3. Linguist extended extensions (e.g., ".mk" for make, ".rake" for ruby).
	ext := strings.ToLower(path.Ext(filename))
	if ext != "" {
		if grammarName, ok := linguistExtensions[ext]; ok {
			return lookupByName(grammarName)
		}
	}

	return nil
}

// DetectLanguageByShebang checks the first line of content for shebang matches.
// Handles both "#!/usr/bin/env python3" and "#!/usr/bin/python3" forms.
func DetectLanguageByShebang(firstLine string) *LangEntry {
	ensureReady()
	registryMu.RLock()
	defer registryMu.RUnlock()

	// 1. Registry shebangs (exact prefix match).
	for i := range registry {
		for _, shebang := range registry[i].Shebangs {
			if strings.HasPrefix(firstLine, shebang) {
				return &registry[i]
			}
		}
	}

	// 2. Extract interpreter from shebang and look up in linguist map.
	interp := extractInterpreter(firstLine)
	if interp != "" {
		if grammarName, ok := linguistInterpreters[interp]; ok {
			return lookupByName(grammarName)
		}
	}

	return nil
}

// extractInterpreter parses a shebang line and returns the interpreter name.
// Handles "#!/usr/bin/env python3" → "python3" and "#!/usr/bin/python3" → "python3".
func extractInterpreter(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "#!") {
		return ""
	}
	line = line[2:]
	line = strings.TrimSpace(line)

	// Split into path and args.
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return ""
	}

	// Get the binary name from the path.
	binary := path.Base(parts[0])

	// If it's "env", the interpreter is the next non-flag, non-VAR=val argument.
	if binary == "env" {
		for _, arg := range parts[1:] {
			if strings.HasPrefix(arg, "-") {
				continue // skip flags like -S, -u
			}
			if strings.Contains(arg, "=") {
				continue // skip VAR=value env assignments
			}
			return strings.ToLower(arg)
		}
		return ""
	}

	return strings.ToLower(binary)
}

// AllLanguages returns all registered languages. This is a metadata-only
// operation — it does NOT load grammar parse tables or decompress blobs.
// Languages that lack an explicit TagsQuery will have an empty TagsQuery
// field; call [ResolveTagsQuery] when you actually need the inferred query.
func AllLanguages() []LangEntry {
	ensureReady()
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]LangEntry, len(registry))
	copy(out, registry)
	return out
}

// ResolveTagsQuery returns the tags query for a LangEntry, computing an
// inferred query from grammar symbols on first call if the entry lacks an
// explicit TagsQuery. The inferred result is cached by language name.
// This may trigger grammar loading for languages without an explicit TagsQuery.
func ResolveTagsQuery(entry LangEntry) string {
	if q := strings.TrimSpace(entry.TagsQuery); q != "" {
		return entry.TagsQuery
	}
	return inferredTagsQuery(entry)
}

// lookupByName returns the LangEntry with the given grammar name, or nil.
// MUST be called with registryMu.RLock() or Lock() held.
func lookupByName(name string) *LangEntry {
	for i := range registry {
		if registry[i].Name == name {
			return &registry[i]
		}
	}
	return nil
}

// normalizeLinguistKey lowercases and trims input, preserving special
// characters (+, #, etc.) so "C++" and "F#" map correctly.
func normalizeLinguistKey(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

// DetectLanguageByName returns the LangEntry for any linguist canonical name,
// alias, or gotreesitter grammar name. Returns nil if unknown.
//
// Accepts: "C++", "cpp", "Go", "golang", "Shell", "bash", "F#", "fsharp", etc.
// Direct grammar names always take priority over linguist aliases to prevent
// shadowing (e.g., "eex" resolves to the eex grammar, not heex via alias).
func DetectLanguageByName(name string) *LangEntry {
	ensureReady()
	key := normalizeLinguistKey(name)

	registryMu.RLock()
	defer registryMu.RUnlock()

	// Direct grammar name takes priority over alias mapping.
	if entry := lookupByName(key); entry != nil {
		return entry
	}
	if grammarName, ok := linguistToGrammar[key]; ok {
		return lookupByName(grammarName)
	}
	// Check extension aliases (e.g., "dmj" → "danmuji", "fw" → "ferrous-wheel")
	if canonical, ok := extensionAliases[key]; ok {
		return lookupByName(canonical)
	}
	return nil
}

// DisplayName returns the linguist canonical display name for a language
// (e.g., "C++" for cpp, "JavaScript" for javascript). Falls back to
// title-casing the grammar name if no linguist match exists.
func DisplayName(entry *LangEntry) string {
	if entry == nil {
		return ""
	}
	if dn, ok := grammarDisplayNames[entry.Name]; ok {
		return dn
	}
	// Fallback: title-case with underscores as spaces.
	words := strings.Split(entry.Name, "_")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
