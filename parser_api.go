package gotreesitter

import (
	"errors"
	"fmt"
)

type parseConfig struct {
	oldTree     *Tree
	tokenSource TokenSource
	profiling   bool
}

// ParserLogType categorizes parser log messages.
type ParserLogType uint8

const (
	// ParserLogParse emits parser-loop lifecycle and control-flow logs.
	ParserLogParse ParserLogType = iota
	// ParserLogLex emits token-source and token-consumption logs.
	ParserLogLex
)

// ParserLogger receives parser debug logs when configured via SetLogger.
type ParserLogger func(kind ParserLogType, message string)

const (
	// Retry no-stacks-alive full parses with a wider GLR cap. Large real-world
	// files (for example this repo's parser.go) can legitimately need >8 stacks
	// at peak even when parse tables report narrower local conflict widths.
	fullParseRetryMaxGLRStacks = 32
	// Keep retry widening bounded to avoid runaway memory growth on very large
	// malformed inputs. Callers can still override via GOT_GLR_MAX_STACKS.
	fullParseRetryMaxSourceBytes = 1 << 20 // 1 MiB
)

type resettableTokenSource interface {
	Reset(source []byte)
}

func shouldRetryFullParse(tree *Tree, sourceLen int) bool {
	if tree == nil {
		return false
	}
	if tree.ParseStopReason() != ParseStopNoStacksAlive {
		return false
	}
	if sourceLen <= 0 {
		return false
	}
	return sourceLen <= fullParseRetryMaxSourceBytes
}

// ParseOption configures ParseWith behavior.
type ParseOption func(*parseConfig)

// WithOldTree enables incremental parsing against an edited prior tree.
func WithOldTree(oldTree *Tree) ParseOption {
	return func(c *parseConfig) {
		c.oldTree = oldTree
	}
}

// WithTokenSource provides a custom token source for parsing.
func WithTokenSource(ts TokenSource) ParseOption {
	return func(c *parseConfig) {
		c.tokenSource = ts
	}
}

// WithProfiling enables incremental parse attribution in ParseResult.Profile.
func WithProfiling() ParseOption {
	return func(c *parseConfig) {
		c.profiling = true
	}
}

// ParseResult is returned by ParseWith.
type ParseResult struct {
	Tree *Tree
	// Profile is populated only when ParseWith uses WithProfiling for
	// incremental parsing.
	Profile IncrementalParseProfile
	// ProfileAvailable reports whether Profile contains attribution data.
	ProfileAvailable bool
}

// Language returns the parser's configured language.
func (p *Parser) Language() *Language {
	if p == nil {
		return nil
	}
	return p.language
}

// SetGLRTrace enables verbose GLR stack tracing to stdout (debug only).
func (p *Parser) SetGLRTrace(enabled bool) {
	if p == nil {
		return
	}
	p.glrTrace = enabled
}

// SetLogger installs a parser debug logger. Pass nil to disable logging.
func (p *Parser) SetLogger(logger ParserLogger) {
	if p == nil {
		return
	}
	p.logger = logger
}

// Logger returns the currently configured parser debug logger.
func (p *Parser) Logger() ParserLogger {
	if p == nil {
		return nil
	}
	return p.logger
}

// SetTimeoutMicros configures a per-parse timeout in microseconds.
// A value of 0 disables timeout checks.
func (p *Parser) SetTimeoutMicros(timeoutMicros uint64) {
	if p == nil {
		return
	}
	p.timeoutMicros = timeoutMicros
}

// TimeoutMicros returns the parser timeout in microseconds.
func (p *Parser) TimeoutMicros() uint64 {
	if p == nil {
		return 0
	}
	return p.timeoutMicros
}

// SetCancellationFlag configures a caller-owned cancellation flag.
// Parsing stops when the pointed value becomes non-zero.
func (p *Parser) SetCancellationFlag(flag *uint32) {
	if p == nil {
		return
	}
	p.cancellationFlag = flag
}

// CancellationFlag returns the parser's current cancellation flag pointer.
func (p *Parser) CancellationFlag() *uint32 {
	if p == nil {
		return nil
	}
	return p.cancellationFlag
}

// SetIncludedRanges configures parser include ranges.
// Tokens outside these ranges are skipped.
func (p *Parser) SetIncludedRanges(ranges []Range) {
	if p == nil {
		return
	}
	p.included = normalizeIncludedRanges(ranges)
}

// IncludedRanges returns a copy of the configured include ranges.
func (p *Parser) IncludedRanges() []Range {
	if p == nil || len(p.included) == 0 {
		return nil
	}
	out := make([]Range, len(p.included))
	copy(out, p.included)
	return out
}

func (p *Parser) wrapIncludedRanges(ts TokenSource) TokenSource {
	if p == nil || len(p.included) == 0 || ts == nil {
		return ts
	}
	return newIncludedRangeTokenSource(ts, p.included)
}

// TokenSource provides tokens to the parser. This interface abstracts over
// different lexer implementations: the built-in DFA lexer (for hand-built
// grammars) or custom bridges like GoTokenSource (for real grammars where
// we can't extract the C lexer DFA).
type TokenSource interface {
	// Next returns the next token. It should skip whitespace and comments
	// as appropriate for the language. Returns a zero-Symbol token at EOF.
	Next() Token
}

// ByteSkippableTokenSource can jump to a byte offset and return the first
// token at or after that position.
type ByteSkippableTokenSource interface {
	TokenSource
	SkipToByte(offset uint32) Token
}

// PointSkippableTokenSource extends ByteSkippableTokenSource with a hint-based
// skip that avoids recomputing row/column from byte offset. During incremental
// parsing the reused node already carries its endpoint, so passing it directly
// eliminates the O(n) offset-to-point scan.
type PointSkippableTokenSource interface {
	ByteSkippableTokenSource
	SkipToByteWithPoint(offset uint32, pt Point) Token
}

// IncrementalReuseTokenSource is an opt-in marker for custom token sources
// that are safe for incremental subtree reuse. Implementations must provide
// stable token boundaries across edits and support deterministic SkipToByte*
// behavior so reused-tree fast-forwarding remains correct.
type IncrementalReuseTokenSource interface {
	TokenSource
	SupportsIncrementalReuse() bool
}

type parserStateTokenSource interface {
	SetParserState(state StateID)
	// SetGLRStates provides all active GLR stack states so the token source
	// can compute valid external symbols as the union across all stacks.
	// This is critical for grammars with external scanners and GLR conflicts.
	SetGLRStates(states []StateID)
}

// stackEntry is a single entry on the parser's LR stack, pairing a parser
// state with the syntax tree node that was shifted or reduced into that state.
type stackEntry struct {
	state StateID
	node  *Node
}

// errorSymbol is the well-known symbol ID used for error nodes.
const errorSymbol = Symbol(65535)

// Parse tokenizes and parses source using the built-in DFA lexer, returning
// a syntax tree. This works for hand-built grammars that provide LexStates.
// For real grammars that need a custom lexer, use ParseWithTokenSource.
// If the input is empty, it returns a tree with a nil root and no error.
func (p *Parser) Parse(source []byte) (*Tree, error) {
	if err := p.checkLanguageCompatible(); err != nil {
		return nil, err
	}
	if err := p.checkDFALexer(); err != nil {
		return nil, err
	}
	lexer := NewLexer(p.language.LexStates, source)
	ts := acquireDFATokenSource(lexer, p.language, p.lookupActionIndex, p.hasKeywordState)
	deterministicExternalConflicts := p.language != nil &&
		p.language.ExternalScanner != nil &&
		(p.language.Name == "yaml" || p.language.Name == "scala")
	tree := p.parseInternal(source, p.wrapIncludedRanges(ts), nil, nil, arenaClassFull, nil, 0, deterministicExternalConflicts)
	if parseMaxGLRStacksValue() < fullParseRetryMaxGLRStacks && shouldRetryFullParse(tree, len(source)) {
		retryLexer := NewLexer(p.language.LexStates, source)
		retryTS := acquireDFATokenSource(retryLexer, p.language, p.lookupActionIndex, p.hasKeywordState)
		tree = p.parseInternal(source, p.wrapIncludedRanges(retryTS), nil, nil, arenaClassFull, nil, fullParseRetryMaxGLRStacks, deterministicExternalConflicts)
	}
	if p.language.ExternalScanner != nil && shouldRetryFullParse(tree, len(source)) {
		retryLexer := NewLexer(p.language.LexStates, source)
		retryTS := acquireDFATokenSource(retryLexer, p.language, p.lookupActionIndex, p.hasKeywordState)
		tree = p.parseInternal(source, p.wrapIncludedRanges(retryTS), nil, nil, arenaClassFull, nil, fullParseRetryMaxGLRStacks, deterministicExternalConflicts)
	}
	return tree, nil
}

// ParseWithTokenSource parses source using a custom token source.
// This is used for real grammars where the lexer DFA isn't available
// as data tables (e.g., Go grammar using go/scanner as a bridge).
func (p *Parser) ParseWithTokenSource(source []byte, ts TokenSource) (*Tree, error) {
	if err := p.checkLanguageCompatible(); err != nil {
		return nil, err
	}
	deterministicExternalConflicts := p.language != nil &&
		p.language.ExternalScanner != nil &&
		(p.language.Name == "yaml" || p.language.Name == "scala")
	tree := p.parseInternal(source, p.wrapIncludedRanges(ts), nil, nil, arenaClassFull, nil, 0, deterministicExternalConflicts)
	if parseMaxGLRStacksValue() < fullParseRetryMaxGLRStacks && shouldRetryFullParse(tree, len(source)) {
		if resettable, ok := ts.(resettableTokenSource); ok {
			resettable.Reset(source)
			tree = p.parseInternal(source, p.wrapIncludedRanges(ts), nil, nil, arenaClassFull, nil, fullParseRetryMaxGLRStacks, deterministicExternalConflicts)
		}
	}
	if p.language.ExternalScanner != nil && shouldRetryFullParse(tree, len(source)) {
		if resettable, ok := ts.(resettableTokenSource); ok {
			resettable.Reset(source)
			tree = p.parseInternal(source, p.wrapIncludedRanges(ts), nil, nil, arenaClassFull, nil, fullParseRetryMaxGLRStacks, deterministicExternalConflicts)
		}
	}
	return tree, nil
}

// ParseIncremental re-parses source after edits were applied to oldTree.
// It reuses unchanged subtrees from the old tree for better performance.
// Call oldTree.Edit() for each edit before calling this method.
func (p *Parser) ParseIncremental(source []byte, oldTree *Tree) (*Tree, error) {
	if err := p.checkLanguageCompatible(); err != nil {
		return nil, err
	}
	if canReuseUnchangedTree(source, oldTree, p.language) {
		return oldTree, nil
	}
	if err := p.checkDFALexer(); err != nil {
		return nil, err
	}
	lexer := NewLexer(p.language.LexStates, source)
	ts := acquireDFATokenSource(lexer, p.language, p.lookupActionIndex, p.hasKeywordState)
	return p.parseIncrementalInternal(source, oldTree, p.wrapIncludedRanges(ts), nil), nil
}

// ParseIncrementalWithTokenSource is like ParseIncremental but uses a custom
// token source.
func (p *Parser) ParseIncrementalWithTokenSource(source []byte, oldTree *Tree, ts TokenSource) (*Tree, error) {
	if err := p.checkLanguageCompatible(); err != nil {
		return nil, err
	}
	if canReuseUnchangedTree(source, oldTree, p.language) {
		return oldTree, nil
	}
	return p.parseIncrementalInternal(source, oldTree, p.wrapIncludedRanges(ts), nil), nil
}

// ParseIncrementalProfiled is like ParseIncremental and also returns runtime
// attribution for incremental reuse work vs parse/rebuild work.
func (p *Parser) ParseIncrementalProfiled(source []byte, oldTree *Tree) (*Tree, IncrementalParseProfile, error) {
	if err := p.checkLanguageCompatible(); err != nil {
		return nil, IncrementalParseProfile{}, err
	}
	if canReuseUnchangedTree(source, oldTree, p.language) {
		return oldTree, IncrementalParseProfile{}, nil
	}
	if err := p.checkDFALexer(); err != nil {
		return nil, IncrementalParseProfile{}, err
	}
	lexer := NewLexer(p.language.LexStates, source)
	ts := acquireDFATokenSource(lexer, p.language, p.lookupActionIndex, p.hasKeywordState)
	timing := &incrementalParseTiming{}
	tree := p.parseIncrementalInternal(source, oldTree, p.wrapIncludedRanges(ts), timing)
	return tree, timing.toProfile(), nil
}

// ParseIncrementalWithTokenSourceProfiled is like ParseIncrementalWithTokenSource
// and also returns runtime attribution for incremental reuse work vs parse/rebuild work.
func (p *Parser) ParseIncrementalWithTokenSourceProfiled(source []byte, oldTree *Tree, ts TokenSource) (*Tree, IncrementalParseProfile, error) {
	if err := p.checkLanguageCompatible(); err != nil {
		return nil, IncrementalParseProfile{}, err
	}
	if canReuseUnchangedTree(source, oldTree, p.language) {
		return oldTree, IncrementalParseProfile{}, nil
	}
	timing := &incrementalParseTiming{}
	tree := p.parseIncrementalInternal(source, oldTree, p.wrapIncludedRanges(ts), timing)
	return tree, timing.toProfile(), nil
}

// ParseWith parses source using option-based configuration.
func (p *Parser) ParseWith(source []byte, opts ...ParseOption) (ParseResult, error) {
	var cfg parseConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if cfg.profiling {
		if cfg.oldTree != nil {
			if cfg.tokenSource != nil {
				tree, profile, err := p.ParseIncrementalWithTokenSourceProfiled(source, cfg.oldTree, cfg.tokenSource)
				return ParseResult{Tree: tree, Profile: profile, ProfileAvailable: true}, err
			}
			tree, profile, err := p.ParseIncrementalProfiled(source, cfg.oldTree)
			return ParseResult{Tree: tree, Profile: profile, ProfileAvailable: true}, err
		}
		// Full parses do not currently expose attribution data.
		if cfg.tokenSource != nil {
			tree, err := p.ParseWithTokenSource(source, cfg.tokenSource)
			return ParseResult{Tree: tree, ProfileAvailable: false}, err
		}
		tree, err := p.Parse(source)
		return ParseResult{Tree: tree, ProfileAvailable: false}, err
	}

	if cfg.oldTree != nil {
		if cfg.tokenSource != nil {
			tree, err := p.ParseIncrementalWithTokenSource(source, cfg.oldTree, cfg.tokenSource)
			return ParseResult{Tree: tree, ProfileAvailable: false}, err
		}
		tree, err := p.ParseIncremental(source, cfg.oldTree)
		return ParseResult{Tree: tree, ProfileAvailable: false}, err
	}

	if cfg.tokenSource != nil {
		tree, err := p.ParseWithTokenSource(source, cfg.tokenSource)
		return ParseResult{Tree: tree, ProfileAvailable: false}, err
	}
	tree, err := p.Parse(source)
	return ParseResult{Tree: tree, ProfileAvailable: false}, err
}

// ErrNoLanguage is returned when a Parser has no language configured.
var ErrNoLanguage = errors.New("parser has no language configured")

// checkLanguageCompatible returns an error if the parser's language is nil or
// incompatible with the runtime.
func (p *Parser) checkLanguageCompatible() error {
	if p.language == nil {
		return ErrNoLanguage
	}
	if !p.language.CompatibleWithRuntime() {
		return fmt.Errorf("language version %d incompatible with parser", p.language.LanguageVersion)
	}
	return nil
}

// checkDFALexer returns an error if the parser's language has no DFA lexer tables.
func (p *Parser) checkDFALexer() error {
	if p.language == nil || len(p.language.LexStates) == 0 {
		return fmt.Errorf("no DFA lexer available for language (use ParseWithTokenSource instead)")
	}
	return nil
}
