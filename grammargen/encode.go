package grammargen

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"fmt"

	"github.com/odvcencio/gotreesitter"
)

// Generate compiles a Grammar definition into a binary blob that
// gotreesitter can load via DecodeLanguageBlob / loadEmbeddedLanguage.
func Generate(g *Grammar) ([]byte, error) {
	// Phase 1: Normalize grammar.
	ng, err := Normalize(g)
	if err != nil {
		return nil, fmt.Errorf("normalize: %w", err)
	}

	// Phase 2: Build LR(1) parse tables.
	tables, err := buildLRTables(ng)
	if err != nil {
		return nil, fmt.Errorf("build LR tables: %w", err)
	}

	// Phase 3: Resolve conflicts.
	if err := resolveConflicts(tables, ng); err != nil {
		return nil, fmt.Errorf("resolve conflicts: %w", err)
	}

	// Phase 3b: Add nonterminal extra parse chains.
	addNonterminalExtraChains(tables, ng)

	// Phase 4: Compute lex modes based on parse table.
	tokenCount := ng.TokenCount()
	immediateTokens := make(map[int]bool)
	for _, t := range ng.Terminals {
		if t.Immediate {
			immediateTokens[t.SymbolID] = true
		}
	}

	keywordSet := make(map[int]bool, len(ng.KeywordSymbols))
	for _, ks := range ng.KeywordSymbols {
		keywordSet[ks] = true
	}

	extraFS := computeExtraFirstSets(ng)
	lexModes, stateToMode := computeLexModes(
		tables.StateCount,
		tokenCount,
		func(state, sym int) bool {
			if acts, ok := tables.ActionTable[state]; ok {
				if entry, ok := acts[sym]; ok && len(entry) > 0 {
					return true
				}
			}
			return false
		},
		ng.ExtraSymbols,
		immediateTokens,
		ng.ExternalSymbols,
		ng.WordSymbolID,
		keywordSet,
		extraFS,
	)

	// Phase 5: Build lex DFA per mode.
	skipExtras := computeSkipExtras(ng)
	lexStates, lexModeOffsets, err := buildLexDFA(ng.Terminals, ng.ExtraSymbols, skipExtras, lexModes)
	if err != nil {
		return nil, fmt.Errorf("build lex DFA: %w", err)
	}

	// Phase 5b: Build keyword DFA if word token is declared.
	var keywordLexStates []gotreesitter.LexState
	if len(ng.KeywordEntries) > 0 {
		kls, _, err := buildLexDFA(ng.KeywordEntries, nil, nil, []lexModeSpec{{
			validSymbols:   allSymbolsSet(ng.KeywordEntries),
			skipWhitespace: false,
		}})
		if err != nil {
			return nil, fmt.Errorf("build keyword DFA: %w", err)
		}
		keywordLexStates = kls
	}

	// Phase 6: Assemble Language struct.
	lang, err := assemble(ng, tables, lexStates, stateToMode, lexModeOffsets)
	if err != nil {
		return nil, fmt.Errorf("assemble: %w", err)
	}
	lang.Name = g.Name

	// Set keyword fields.
	if len(keywordLexStates) > 0 {
		lang.KeywordLexStates = keywordLexStates
		lang.KeywordCaptureToken = gotreesitter.Symbol(ng.WordSymbolID)
	}

	// Phase 7: Encode to binary blob.
	blob, err := encodeLanguageBlob(lang)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	return blob, nil
}

// GenerateLanguage compiles a Grammar into a Language struct without encoding.
func GenerateLanguage(g *Grammar) (*gotreesitter.Language, error) {
	ng, err := Normalize(g)
	if err != nil {
		return nil, fmt.Errorf("normalize: %w", err)
	}

	tables, err := buildLRTables(ng)
	if err != nil {
		return nil, fmt.Errorf("build LR tables: %w", err)
	}

	if err := resolveConflicts(tables, ng); err != nil {
		return nil, fmt.Errorf("resolve conflicts: %w", err)
	}

	addNonterminalExtraChains(tables, ng)

	tokenCount := ng.TokenCount()
	immediateTokens := make(map[int]bool)
	for _, t := range ng.Terminals {
		if t.Immediate {
			immediateTokens[t.SymbolID] = true
		}
	}

	keywordSet := make(map[int]bool, len(ng.KeywordSymbols))
	for _, ks := range ng.KeywordSymbols {
		keywordSet[ks] = true
	}

	extraFS := computeExtraFirstSets(ng)
	lexModes, stateToMode := computeLexModes(
		tables.StateCount,
		tokenCount,
		func(state, sym int) bool {
			if acts, ok := tables.ActionTable[state]; ok {
				if entry, ok := acts[sym]; ok && len(entry) > 0 {
					return true
				}
			}
			return false
		},
		ng.ExtraSymbols,
		immediateTokens,
		ng.ExternalSymbols,
		ng.WordSymbolID,
		keywordSet,
		extraFS,
	)

	skipExtras := computeSkipExtras(ng)
	lexStates, lexModeOffsets, err := buildLexDFA(ng.Terminals, ng.ExtraSymbols, skipExtras, lexModes)
	if err != nil {
		return nil, fmt.Errorf("build lex DFA: %w", err)
	}

	// Build keyword DFA if word token is declared.
	var keywordLexStates []gotreesitter.LexState
	if len(ng.KeywordEntries) > 0 {
		kls, _, err := buildLexDFA(ng.KeywordEntries, nil, nil, []lexModeSpec{{
			validSymbols:   allSymbolsSet(ng.KeywordEntries),
			skipWhitespace: false,
		}})
		if err != nil {
			return nil, fmt.Errorf("build keyword DFA: %w", err)
		}
		keywordLexStates = kls
	}

	lang, err := assemble(ng, tables, lexStates, stateToMode, lexModeOffsets)
	if err != nil {
		return nil, fmt.Errorf("assemble: %w", err)
	}
	lang.Name = g.Name

	// Set keyword fields.
	if len(keywordLexStates) > 0 {
		lang.KeywordLexStates = keywordLexStates
		lang.KeywordCaptureToken = gotreesitter.Symbol(ng.WordSymbolID)
	}

	return lang, nil
}

// allSymbolsSet returns a set containing all symbol IDs from the patterns.
func allSymbolsSet(patterns []TerminalPattern) map[int]bool {
	s := make(map[int]bool, len(patterns))
	for _, p := range patterns {
		s[p.SymbolID] = true
	}
	return s
}

// computeExtraFirstSets computes the first-set terminals for nonterminal extras.
// When extras include nonterminal rules (like `comment`), the lexer needs to
// recognize their constituent first-set terminals in every lex mode.
func computeExtraFirstSets(ng *NormalizedGrammar) map[int]map[int]bool {
	tokenCount := ng.TokenCount()
	extraSet := make(map[int]bool)
	for _, e := range ng.ExtraSymbols {
		extraSet[e] = true
	}

	// Only compute for nonterminal extras.
	result := make(map[int]map[int]bool)
	for _, e := range ng.ExtraSymbols {
		if e < tokenCount {
			continue // terminal extra, no first-set needed
		}
		first := make(map[int]bool)
		computeFirst(e, ng.Productions, tokenCount, first, make(map[int]bool))
		if len(first) > 0 {
			result[e] = first
		}
	}
	return result
}

// computeFirst computes the first-set terminals for a nonterminal symbol.
func computeFirst(sym int, prods []Production, tokenCount int, result map[int]bool, visited map[int]bool) {
	if visited[sym] {
		return
	}
	visited[sym] = true

	for _, prod := range prods {
		if prod.LHS != sym {
			continue
		}
		for _, rhsSym := range prod.RHS {
			if rhsSym < tokenCount {
				result[rhsSym] = true
				break // terminal — not nullable
			}
			// Nonterminal — recurse and check if nullable.
			computeFirst(rhsSym, prods, tokenCount, result, visited)
			// Simple approximation: stop at first symbol (not handling nullables).
			// This is sufficient for typical extras like `comment → [;#] [^\n]*`.
			break
		}
	}
}

// computeSkipExtras returns the set of extra symbol IDs that should be
// silently consumed (Skip=true in the DFA). Only invisible/anonymous extras
// are skipped. Visible extras like `comment` produce tree nodes.
func computeSkipExtras(ng *NormalizedGrammar) map[int]bool {
	skip := make(map[int]bool)
	for _, e := range ng.ExtraSymbols {
		if e > 0 && e < len(ng.Symbols) && !ng.Symbols[e].Visible {
			skip[e] = true
		}
	}
	return skip
}

// encodeLanguageBlob serializes a Language using gob+gzip.
func encodeLanguageBlob(lang *gotreesitter.Language) ([]byte, error) {
	var out bytes.Buffer
	gzw := gzip.NewWriter(&out)
	if err := gob.NewEncoder(gzw).Encode(lang); err != nil {
		_ = gzw.Close()
		return nil, fmt.Errorf("encode language blob: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return nil, fmt.Errorf("finalize language blob: %w", err)
	}
	return out.Bytes(), nil
}

// decodeLanguageBlob deserializes a gob+gzip Language blob.
func decodeLanguageBlob(data []byte) (*gotreesitter.Language, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gzr.Close()

	var lang gotreesitter.Language
	if err := gob.NewDecoder(gzr).Decode(&lang); err != nil {
		return nil, fmt.Errorf("decode language blob: %w", err)
	}
	return &lang, nil
}
