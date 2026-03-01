package gotreesitter

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Parser reads parse tables from a Language and produces a syntax tree.
// It supports GLR parsing: when a (state, symbol) pair maps to multiple
// actions, the parser forks the stack and explores all alternatives in
// parallel while preserving distinct parse paths. Duplicate stack
// versions are collapsed and ambiguities are resolved at selection time.
//
// Parser is not safe for concurrent use. Use one parser per goroutine, or
// guard shared parser instances with external synchronization.
type Parser struct {
	language          *Language
	reuseCursor       reuseCursor
	reuseScratch      reuseScratch
	reuseMu           sync.Mutex
	fullArenaHint     uint32
	hasRecoverState   []bool
	hasRecoverSymbol  []bool
	recoverByState    [][]recoverSymbolAction
	hasKeywordState   []bool
	forceRawSpanAll   bool
	forceRawSpanTable []bool
	included          []Range
	denseLimit        int
	smallBase         int
	smallLookup       [][]smallActionPair
}

type smallActionPair struct {
	sym uint16
	val uint16
}

type recoverSymbolAction struct {
	sym    uint16
	action ParseAction
}

type parserScratch struct {
	merge      glrMergeScratch
	entries    glrEntryScratch
	gss        gssScratch
	tmpEntries []stackEntry
	glrStates  []StateID
	nodeLinks  []*Node
}

var parserScratchPool = sync.Pool{
	New: func() any {
		return &parserScratch{}
	},
}

var (
	parseNodeLimitScaleOnce sync.Once
	parseNodeLimitScale     int
	parseMaxGLRStacksOnce   sync.Once
	parseMaxGLRStacks       int
)

const (
	// maxForkCloneDepth limits GLR stack cloning for pathological ambiguity.
	// Above this depth, we execute only the first action to avoid runaway work.
	maxForkCloneDepth = 4 * 1024
	// maxConsecutivePrimaryReduces prevents infinite reduce loops on the
	// primary stack when no token advancement occurs.
	maxConsecutivePrimaryReduces = 10
)

func parseNodeLimitScaleFactor() int {
	parseNodeLimitScaleOnce.Do(func() {
		parseNodeLimitScale = 1
		raw := strings.TrimSpace(os.Getenv("GOT_PARSE_NODE_LIMIT_SCALE"))
		if raw == "" {
			return
		}
		n, err := strconv.Atoi(raw)
		if err == nil && n > 0 {
			parseNodeLimitScale = n
		}
	})
	return parseNodeLimitScale
}

func parseMaxGLRStacksValue() int {
	parseMaxGLRStacksOnce.Do(func() {
		parseMaxGLRStacks = maxGLRStacks
		raw := strings.TrimSpace(os.Getenv("GOT_GLR_MAX_STACKS"))
		if raw == "" {
			return
		}
		n, err := strconv.Atoi(raw)
		if err == nil && n > 0 {
			parseMaxGLRStacks = n
		}
	})
	return parseMaxGLRStacks
}

// IncrementalParseProfile attributes incremental parse time into coarse buckets.
//
// ReuseCursorNanos includes reuse-cursor setup and subtree-candidate checks.
// ReparseNanos includes the remainder of incremental parsing/rebuild work.
type IncrementalParseProfile struct {
	ReuseCursorNanos   int64
	ReparseNanos       int64
	ReusedSubtrees     uint64
	ReusedBytes        uint64
	NewNodesAllocated  uint64
	RecoverSearches    uint64
	RecoverStateChecks uint64
	RecoverStateSkips  uint64
	RecoverSymbolSkips uint64
	RecoverLookups     uint64
	RecoverHits        uint64
	MaxStacksSeen      int
	EntryScratchPeak   uint64
}

type incrementalParseTiming struct {
	totalNanos         int64
	reuseNanos         int64
	reusedSubtrees     uint64
	reusedBytes        uint64
	newNodes           uint64
	recoverSearches    uint64
	recoverStateChecks uint64
	recoverStateSkips  uint64
	recoverSymbolSkips uint64
	recoverLookups     uint64
	recoverHits        uint64
	maxStacksSeen      int
	entryScratchPeak   uint64
}

type parseReuseState struct {
	reusedAny bool
	arenaRefs []*nodeArena
}

func (s *parseReuseState) markReused(node *Node, primary *nodeArena) {
	if s == nil {
		return
	}
	s.reusedAny = true
	if node == nil {
		return
	}
	s.arenaRefs = appendUniqueArenaRef(s.arenaRefs, node.ownerArena, primary)
}

func (s *parseReuseState) retainBorrowed(primary *nodeArena) []*nodeArena {
	if s == nil || !s.reusedAny || len(s.arenaRefs) == 0 {
		return nil
	}
	uniq := uniqueArenas(s.arenaRefs, primary)
	if len(uniq) == 0 {
		return nil
	}
	for _, a := range uniq {
		a.Retain()
	}
	return uniq
}

func (t *incrementalParseTiming) toProfile() IncrementalParseProfile {
	if t == nil {
		return IncrementalParseProfile{}
	}
	reparse := t.totalNanos - t.reuseNanos
	if reparse < 0 {
		reparse = 0
	}
	return IncrementalParseProfile{
		ReuseCursorNanos:   t.reuseNanos,
		ReparseNanos:       reparse,
		ReusedSubtrees:     t.reusedSubtrees,
		ReusedBytes:        t.reusedBytes,
		NewNodesAllocated:  t.newNodes,
		RecoverSearches:    t.recoverSearches,
		RecoverStateChecks: t.recoverStateChecks,
		RecoverStateSkips:  t.recoverStateSkips,
		RecoverSymbolSkips: t.recoverSymbolSkips,
		RecoverLookups:     t.recoverLookups,
		RecoverHits:        t.recoverHits,
		MaxStacksSeen:      t.maxStacksSeen,
		EntryScratchPeak:   t.entryScratchPeak,
	}
}

func acquireParserScratch() *parserScratch {
	return parserScratchPool.Get().(*parserScratch)
}

func releaseParserScratch(s *parserScratch, skipGSSClear bool) {
	if s == nil {
		return
	}
	if len(s.merge.result) > 0 {
		clear(s.merge.result)
	}
	s.merge.result = s.merge.result[:0]
	if len(s.merge.slots) > 0 {
		s.merge.slots = s.merge.slots[:0]
	}
	s.merge.perKeyCap = 0
	if cap(s.tmpEntries) > 0 {
		buf := s.tmpEntries[:cap(s.tmpEntries)]
		clear(buf)
		if cap(buf) > maxRetainedStackEntryCap {
			s.tmpEntries = nil
		} else {
			s.tmpEntries = buf[:0]
		}
	}
	if cap(s.glrStates) > maxGLRStacks {
		s.glrStates = nil
	} else if len(s.glrStates) > 0 {
		s.glrStates = s.glrStates[:0]
	}
	const maxRetainedNodeLinkStack = 256 * 1024
	if cap(s.nodeLinks) > maxRetainedNodeLinkStack {
		s.nodeLinks = nil
	} else if len(s.nodeLinks) > 0 {
		s.nodeLinks = s.nodeLinks[:0]
	}
	s.entries.reset()
	s.gss.skipClear = skipGSSClear
	s.gss.reset()
	parserScratchPool.Put(s)
}

// NewParser creates a new Parser for the given language.
func NewParser(lang *Language) *Parser {
	p := &Parser{language: lang}
	if lang != nil {
		p.forceRawSpanAll = lang.Name == "yaml"
		for i, name := range lang.SymbolNames {
			if name != "statement_list" {
				continue
			}
			if p.forceRawSpanTable == nil {
				p.forceRawSpanTable = make([]bool, len(lang.SymbolNames))
			}
			p.forceRawSpanTable[i] = true
		}
		if lang.LargeStateCount > 0 {
			p.denseLimit = int(lang.LargeStateCount)
		} else {
			p.denseLimit = len(lang.ParseTable)
		}
		p.smallBase = int(lang.LargeStateCount)
		if len(lang.SmallParseTableMap) > 0 && len(lang.SmallParseTable) > 0 {
			p.smallLookup = buildSmallLookup(lang)
		}
		p.recoverByState, p.hasRecoverState, p.hasRecoverSymbol = buildRecoverActionsByState(lang)
		p.hasKeywordState = buildKeywordStates(lang)
	}
	return p
}

func buildStateRecoverTable(lang *Language) []bool {
	_, table, _ := buildRecoverActionsByState(lang)
	return table
}

// buildKeywordStates precomputes whether each parser state can produce a
// keyword token, so keyword promotion can skip states where it is impossible.
func buildKeywordStates(lang *Language) []bool {
	if lang == nil || lang.KeywordCaptureToken == 0 || len(lang.KeywordLexStates) == 0 {
		return nil
	}

	symbolCount := int(lang.SymbolCount)
	if symbolCount <= 0 {
		symbolCount = len(lang.SymbolNames)
	}
	if symbolCount <= 0 {
		symbolCount = 64
	}
	keywordSymbols := make([]bool, symbolCount)
	ensureSymbolCap := func(sym Symbol) {
		idx := int(sym)
		if idx < len(keywordSymbols) {
			return
		}
		newLen := len(keywordSymbols)
		if newLen == 0 {
			newLen = 64
		}
		for idx >= newLen {
			newLen *= 2
		}
		expanded := make([]bool, newLen)
		copy(expanded, keywordSymbols)
		keywordSymbols = expanded
	}

	keywordCount := 0
	for i := range lang.KeywordLexStates {
		sym := lang.KeywordLexStates[i].AcceptToken
		if sym == 0 || sym == lang.KeywordCaptureToken {
			continue
		}
		ensureSymbolCap(sym)
		if !keywordSymbols[sym] {
			keywordSymbols[sym] = true
			keywordCount++
		}
	}
	if keywordCount == 0 {
		return nil
	}

	stateCount := int(lang.StateCount)
	if stateCount <= 0 {
		stateCount = len(lang.ParseTable)
	}
	if smallCount := int(lang.LargeStateCount) + len(lang.SmallParseTableMap); smallCount > stateCount {
		stateCount = smallCount
	}
	if stateCount <= 0 {
		return nil
	}

	hasKeyword := make([]bool, stateCount)
	anyState := false
	for state := 0; state < len(lang.ParseTable) && state < stateCount; state++ {
		row := lang.ParseTable[state]
		for sym, idx := range row {
			if idx == 0 {
				continue
			}
			if sym < len(keywordSymbols) && keywordSymbols[sym] {
				hasKeyword[state] = true
				anyState = true
				break
			}
		}
	}

	if len(lang.SmallParseTableMap) > 0 && len(lang.SmallParseTable) > 0 {
		base := int(lang.LargeStateCount)
		table := lang.SmallParseTable
		for smallIdx, offset := range lang.SmallParseTableMap {
			state := base + smallIdx
			if state < 0 || state >= stateCount || hasKeyword[state] {
				continue
			}
			pos := int(offset)
			if pos >= len(table) {
				continue
			}
			groupCount := int(table[pos])
			pos++
			for i := 0; i < groupCount; i++ {
				if pos+1 >= len(table) {
					break
				}
				sectionValue := table[pos]
				groupSymbolCount := int(table[pos+1])
				pos += 2
				end := pos + groupSymbolCount
				if end > len(table) {
					end = len(table)
				}
				if sectionValue != 0 {
					for _, sym := range table[pos:end] {
						if int(sym) < len(keywordSymbols) && keywordSymbols[sym] {
							hasKeyword[state] = true
							anyState = true
							break
						}
					}
				}
				pos = end
				if hasKeyword[state] {
					break
				}
			}
		}
	}

	if !anyState {
		return nil
	}
	return hasKeyword
}

// buildRecoverActionsByState precomputes recover actions keyed by parser state
// and symbol to avoid repeated action-table scans in recover lookups.
func buildRecoverActionsByState(lang *Language) ([][]recoverSymbolAction, []bool, []bool) {
	if lang == nil {
		return nil, nil, nil
	}
	if len(lang.ParseActions) == 0 {
		return nil, nil, nil
	}

	recoverActions := make([]ParseAction, len(lang.ParseActions))
	hasRecoverAction := make([]bool, len(lang.ParseActions))
	anyRecoverAction := false
	for i := range lang.ParseActions {
		act, ok := recoverAction(&lang.ParseActions[i])
		if !ok {
			continue
		}
		recoverActions[i] = act
		hasRecoverAction[i] = true
		anyRecoverAction = true
	}
	if !anyRecoverAction {
		return nil, nil, nil
	}

	stateCount := int(lang.StateCount)
	if stateCount <= 0 {
		stateCount = len(lang.ParseTable)
	}
	if smallCount := int(lang.LargeStateCount) + len(lang.SmallParseTableMap); smallCount > stateCount {
		stateCount = smallCount
	}
	if stateCount <= 0 {
		return nil, nil, nil
	}
	symbolCount := int(lang.SymbolCount)
	if symbolCount <= 0 {
		symbolCount = len(lang.SymbolNames)
	}
	if symbolCount <= 0 {
		symbolCount = 64
	}

	recoverByState := make([][]recoverSymbolAction, stateCount)
	hasRecoverSymbol := make([]bool, symbolCount)
	ensureSymbolCap := func(sym uint16) {
		idx := int(sym)
		if idx < len(hasRecoverSymbol) {
			return
		}
		newLen := len(hasRecoverSymbol)
		if newLen == 0 {
			newLen = 64
		}
		for idx >= newLen {
			newLen *= 2
		}
		expanded := make([]bool, newLen)
		copy(expanded, hasRecoverSymbol)
		hasRecoverSymbol = expanded
	}
	for state := 0; state < len(lang.ParseTable) && state < stateCount; state++ {
		row := lang.ParseTable[state]
		for sym, idx := range row {
			if int(idx) < len(hasRecoverAction) && hasRecoverAction[idx] {
				ensureSymbolCap(uint16(sym))
				hasRecoverSymbol[sym] = true
				recoverByState[state] = append(recoverByState[state], recoverSymbolAction{
					sym:    uint16(sym),
					action: recoverActions[idx],
				})
			}
		}
	}

	if len(lang.SmallParseTableMap) > 0 && len(lang.SmallParseTable) > 0 {
		base := int(lang.LargeStateCount)
		table := lang.SmallParseTable
		for smallIdx, offset := range lang.SmallParseTableMap {
			state := base + smallIdx
			if state < 0 || state >= stateCount {
				continue
			}
			pos := int(offset)
			if pos >= len(table) {
				continue
			}
			groupCount := table[pos]
			pos++
			for i := uint16(0); i < groupCount; i++ {
				if pos+1 >= len(table) {
					break
				}
				sectionValue := table[pos]
				symbolCount := int(table[pos+1])
				pos += 2
				hasRecover := int(sectionValue) < len(hasRecoverAction) && hasRecoverAction[sectionValue]
				for j := 0; j < symbolCount; j++ {
					if pos >= len(table) {
						break
					}
					if hasRecover {
						ensureSymbolCap(table[pos])
						hasRecoverSymbol[table[pos]] = true
						recoverByState[state] = append(recoverByState[state], recoverSymbolAction{
							sym:    table[pos],
							action: recoverActions[sectionValue],
						})
					}
					pos++
				}
			}
		}
	}

	hasRecoverState := make([]bool, stateCount)
	anyState := false
	for i := range recoverByState {
		if len(recoverByState[i]) > 0 {
			hasRecoverState[i] = true
			anyState = true
		}
	}
	if !anyState {
		return nil, nil, nil
	}
	anySymbol := false
	for i := range hasRecoverSymbol {
		if hasRecoverSymbol[i] {
			anySymbol = true
			break
		}
	}
	if !anySymbol {
		hasRecoverSymbol = nil
	}
	return recoverByState, hasRecoverState, hasRecoverSymbol
}

func parseActionEntryHasRecover(entry *ParseActionEntry) bool {
	if entry == nil {
		return false
	}
	for _, act := range entry.Actions {
		if act.Type == ParseActionRecover {
			return true
		}
	}
	return false
}

func (p *Parser) stateCanRecover(state StateID) bool {
	if len(p.hasRecoverState) == 0 {
		return true
	}
	idx := int(state)
	return idx >= 0 && idx < len(p.hasRecoverState) && p.hasRecoverState[idx]
}

func (p *Parser) symbolCanRecover(sym Symbol) bool {
	if len(p.hasRecoverSymbol) == 0 {
		return true
	}
	idx := int(sym)
	return idx >= 0 && idx < len(p.hasRecoverSymbol) && p.hasRecoverSymbol[idx]
}

func (p *Parser) recoverActionForState(state StateID, sym Symbol) (ParseAction, bool) {
	if len(p.recoverByState) == 0 {
		return ParseAction{}, false
	}
	idx := int(state)
	if idx < 0 || idx >= len(p.recoverByState) {
		return ParseAction{}, false
	}
	entries := p.recoverByState[idx]
	if len(entries) == 0 {
		return ParseAction{}, false
	}
	target := uint16(sym)
	for i := range entries {
		if entries[i].sym == target {
			return entries[i].action, true
		}
	}
	return ParseAction{}, false
}

func (p *Parser) parseIncrementalInternal(source []byte, oldTree *Tree, ts TokenSource, timing *incrementalParseTiming) *Tree {
	// Fast path: unchanged source and no recorded edits.
	if canReuseUnchangedTree(source, oldTree, p.language) {
		return oldTree
	}

	// DFA with external scanner cannot safely skip — scanner state must be
	// preserved token-by-token, so reuse is disabled for that combination.
	// All other token sources (DFA without external scanner, custom lexers)
	// support efficient skipping via PointSkippableTokenSource.
	if _, isDFA := ts.(*dfaTokenSource); isDFA && p.language != nil && p.language.ExternalScanner != nil {
		return p.parseInternal(source, ts, nil, nil, arenaClassFull, timing)
	}

	p.reuseMu.Lock()
	defer p.reuseMu.Unlock()

	var reuse *reuseCursor
	if timing != nil {
		reuseStart := time.Now()
		reuse = p.reuseCursor.reset(oldTree, source, &p.reuseScratch)
		timing.reuseNanos += time.Since(reuseStart).Nanoseconds()
	} else {
		reuse = p.reuseCursor.reset(oldTree, source, &p.reuseScratch)
	}
	arenaClass := incrementalArenaClassForSource(source)
	tree := p.parseInternal(source, ts, reuse, oldTree, arenaClass, timing)
	if reuse != nil {
		if timing != nil {
			reuseStart := time.Now()
			reuse.commitScratch(&p.reuseScratch)
			timing.reuseNanos += time.Since(reuseStart).Nanoseconds()
		} else {
			reuse.commitScratch(&p.reuseScratch)
		}
	}
	return tree
}

func incrementalArenaClassForSource(source []byte) arenaClass {
	arenaClass := arenaClassIncremental
	// Very large files can outgrow incremental defaults and trigger repeated
	// fallback allocations; use full-parse slab sizing only beyond this point.
	const incrementalUseFullArenaThreshold = 1 * 1024 * 1024
	if len(source) >= incrementalUseFullArenaThreshold {
		arenaClass = arenaClassFull
	}
	return arenaClass
}

func canReuseUnchangedTree(source []byte, oldTree *Tree, lang *Language) bool {
	if oldTree == nil || oldTree.language != lang || len(oldTree.edits) != 0 {
		return false
	}
	oldSource := oldTree.source
	if len(oldSource) != len(source) {
		return false
	}
	if len(source) == 0 {
		return true
	}
	// Common incremental no-edit case: caller passes the same source slice.
	// Pointer equality avoids memcmp on hot no-op reparses.
	if &oldSource[0] == &source[0] {
		return true
	}
	return bytes.Equal(oldSource, source)
}

// dfaTokenSource wraps the built-in DFA Lexer as a TokenSource.
// It tracks the current parser state to select the correct lex mode.
func parseIterations(sourceLen int) int {
	return max(10_000, sourceLen*20)
}

// parseStackDepth returns the stack depth limit scaled to input size.
func parseStackDepth(sourceLen int) int {
	return max(1_000, sourceLen*2)
}

// parseNodeLimit returns the maximum number of Node allocations allowed.
// This is the hard ceiling that prevents OOM regardless of iteration count.
func parseNodeLimit(sourceLen int) int {
	// Keep the default budget high enough for large full-parse corpora so
	// correctness gates can run without relying on external scale overrides.
	// The 100k floor avoids premature truncation on small/medium inputs
	// during short-lived ambiguity spikes.
	// The sourceLen*40 budget keeps corpus parity green while GLR/node
	// pressure is still being optimized.
	limit := max(100_000, sourceLen*40)
	scale := parseNodeLimitScaleFactor()
	if scale <= 1 {
		return limit
	}
	maxInt := int(^uint(0) >> 1)
	if limit > maxInt/scale {
		return maxInt
	}
	return limit * scale
}

func parseFullArenaNodeCapacity(sourceLen, hint int) int {
	base := nodeCapacityForClass(arenaClassFull)
	if hint > 0 {
		if hint < base {
			return base
		}
		limit := parseNodeLimit(sourceLen)
		if sourceLen <= 0 {
			return max(base, hint)
		}
		if hint > limit {
			return max(base, limit)
		}
		return hint
	}
	if sourceLen <= 0 {
		return base
	}
	// Conservative first-pass sizing. We refine this with adaptive hints
	// from observed full-parse node usage.
	estimate := sourceLen * 6
	const maxPreallocNodes = 1_500_000
	if estimate > maxPreallocNodes {
		estimate = maxPreallocNodes
	}
	return max(base, estimate)
}

func (p *Parser) fullArenaHintCapacity() int {
	if p == nil {
		return 0
	}
	return int(atomic.LoadUint32(&p.fullArenaHint))
}

func (p *Parser) recordFullArenaUsage(used int) {
	if p == nil || used <= 0 {
		return
	}
	target := used + used/4 // keep 25% headroom above observed peak.
	base := nodeCapacityForClass(arenaClassFull)
	if target < base {
		target = base
	}
	const maxHintNodes = 2_000_000
	if target > maxHintNodes {
		target = maxHintNodes
	}

	for {
		old := atomic.LoadUint32(&p.fullArenaHint)
		var next uint32
		if old == 0 {
			next = uint32(target)
		} else {
			blended := (int(old)*3 + target) / 4
			if blended < base {
				blended = base
			}
			next = uint32(blended)
		}
		if old == next || atomic.CompareAndSwapUint32(&p.fullArenaHint, old, next) {
			return
		}
	}
}

func parseFullEntryScratchCapacity(sourceLen int) int {
	if sourceLen <= 0 {
		return defaultStackEntrySlabCap
	}
	estimate := sourceLen * 12
	if estimate < defaultStackEntrySlabCap {
		estimate = defaultStackEntrySlabCap
	}
	// Keep initial scratch growth bounded; larger capacities are still
	// reached on demand and retained up to maxRetainedStackEntryCap.
	const maxPreallocEntries = 768 * 1024
	if estimate > maxPreallocEntries {
		estimate = maxPreallocEntries
	}
	return estimate
}

func parseIncrementalArenaNodeCapacity(sourceLen int) int {
	base := nodeCapacityForClass(arenaClassIncremental)
	if sourceLen <= 0 {
		return base
	}
	estimate := sourceLen * 4
	const maxPreallocNodes = 512 * 1024
	if estimate > maxPreallocNodes {
		estimate = maxPreallocNodes
	}
	return max(base, estimate)
}

func parseIncrementalEntryScratchCapacity(sourceLen int) int {
	if sourceLen <= 0 {
		return defaultStackEntrySlabCap
	}
	estimate := sourceLen * 8
	if estimate < defaultStackEntrySlabCap {
		estimate = defaultStackEntrySlabCap
	}
	const maxPreallocEntries = 256 * 1024
	if estimate > maxPreallocEntries {
		estimate = maxPreallocEntries
	}
	return estimate
}

func parseErrorTree(source []byte, lang *Language) *Tree {
	end := Point{}
	for i := 0; i < len(source); {
		if source[i] == '\n' {
			end.Row++
			end.Column = 0
			i++
			continue
		}
		_, size := utf8.DecodeRune(source[i:])
		if size <= 0 {
			size = 1
		}
		i += size
		end.Column++
	}

	root := NewLeafNode(errorSymbol, false, 0, uint32(len(source)), Point{}, end)
	root.hasError = true
	return NewTree(root, source, lang)
}

func isWhitespaceOnlySource(source []byte) bool {
	for i := 0; i < len(source); i++ {
		switch source[i] {
		case ' ', '\t', '\n', '\r', '\f':
		default:
			return false
		}
	}
	return true
}

func extendNodeToTrailingWhitespace(n *Node, source []byte) {
	if n == nil {
		return
	}
	sourceEnd := uint32(len(source))
	if n.endByte >= sourceEnd {
		return
	}
	tail := source[n.endByte:sourceEnd]
	for i := 0; i < len(tail); i++ {
		switch tail[i] {
		case ' ', '\t', '\n', '\r', '\f':
		default:
			return
		}
	}

	pt := n.endPoint
	for i := 0; i < len(tail); {
		if tail[i] == '\n' {
			pt.Row++
			pt.Column = 0
			i++
			continue
		}
		_, size := utf8.DecodeRune(tail[i:])
		if size <= 0 {
			size = 1
		}
		i += size
		pt.Column++
	}

	n.endByte = sourceEnd
	n.endPoint = pt
}

// parseInternal is the core GLR parsing loop shared by Parse and
// ParseWithTokenSource.
//
// It maintains a set of parse stacks. For unambiguous grammars (single
// action per table entry), there is exactly one stack and the algorithm
// reduces to standard LR parsing. When multiple actions exist for a
// (state, symbol) pair, the parser forks: one stack per alternative.
// Stacks that error out are dropped. Only duplicate stack versions are
// merged; distinct alternatives are preserved.
func (p *Parser) parseInternal(source []byte, ts TokenSource, reuse *reuseCursor, oldTree *Tree, arenaClass arenaClass, timing *incrementalParseTiming) *Tree {
	var parseStart time.Time
	if timing != nil {
		parseStart = time.Now()
	}
	deferParentLinks := reuse == nil && oldTree == nil
	if closer, ok := ts.(interface{ Close() }); ok {
		defer closer.Close()
	}
	scratch := acquireParserScratch()
	defer releaseParserScratch(scratch, deferParentLinks)
	trackChildErrors := !deferParentLinks

	arena := acquireNodeArena(arenaClass)
	arena.skipChildClear = reuse == nil && oldTree == nil
	if timing != nil {
		startUsed := arena.used
		defer func() {
			timing.totalNanos += time.Since(parseStart).Nanoseconds()
			if arena.used >= startUsed {
				timing.newNodes += uint64(arena.used - startUsed)
			}
			peak := uint64(scratch.entries.peakEntriesUsed())
			if peak > timing.entryScratchPeak {
				timing.entryScratchPeak = peak
			}
		}()
	}
	if arenaClass == arenaClassFull {
		defer func() {
			p.recordFullArenaUsage(arena.used)
		}()
	}
	switch arenaClass {
	case arenaClassFull:
		arena.ensureNodeCapacity(parseFullArenaNodeCapacity(len(source), p.fullArenaHintCapacity()))
		scratch.entries.ensureInitialCap(parseFullEntryScratchCapacity(len(source)))
	case arenaClassIncremental:
		arena.ensureNodeCapacity(parseIncrementalArenaNodeCapacity(len(source)))
		scratch.entries.ensureInitialCap(parseIncrementalEntryScratchCapacity(len(source)))
	}
	var reuseState parseReuseState
	nodeCount := 0
	iterationsUsed := 0
	peakStackDepth := 0
	maxStacksSeen := 0
	var perfTokensConsumed uint64
	var lastTokenEndByte uint32
	var lastTokenSymbol Symbol
	var lastTokenWasEOF bool
	tokenSourceEOFEarly := false
	expectedEOFByte := uint32(len(source))
	if len(p.included) > 0 {
		expectedEOFByte = p.included[len(p.included)-1].EndByte
	}
	parseRuntime := ParseRuntime{
		StopReason:      ParseStopNone,
		SourceLen:       uint32(len(source)),
		ExpectedEOFByte: expectedEOFByte,
	}
	finalizeTree := func(tree *Tree, stopReason ParseStopReason) *Tree {
		if tokenSourceEOFEarly && (stopReason == ParseStopAccepted || stopReason == ParseStopNone) {
			stopReason = ParseStopTokenSourceEOF
		}
		parseRuntime.StopReason = stopReason
		parseRuntime.Iterations = iterationsUsed
		parseRuntime.NodesAllocated = nodeCount
		parseRuntime.PeakStackDepth = peakStackDepth
		parseRuntime.MaxStacksSeen = maxStacksSeen
		parseRuntime.TokensConsumed = perfTokensConsumed
		parseRuntime.LastTokenEndByte = lastTokenEndByte
		parseRuntime.LastTokenSymbol = lastTokenSymbol
		parseRuntime.LastTokenWasEOF = lastTokenWasEOF
		parseRuntime.TokenSourceEOFEarly = tokenSourceEOFEarly
		parseRuntime.RootEndByte = 0
		parseRuntime.Truncated = false
		if tree != nil && tree.RootNode() != nil {
			parseRuntime.RootEndByte = tree.RootNode().EndByte()
			parseRuntime.Truncated = parseRuntime.RootEndByte < expectedEOFByte
		}
		if tree != nil {
			tree.setParseRuntime(parseRuntime)
		}
		return tree
	}
	finalize := func(stacks []glrStack, stopReason ParseStopReason) *Tree {
		tree := p.buildResultFromGLR(stacks, source, arena, oldTree, &reuseState, &scratch.nodeLinks)
		return finalizeTree(tree, stopReason)
	}
	finalizeErrorTree := func(stopReason ParseStopReason) *Tree {
		arena.Release()
		return finalizeTree(parseErrorTree(source, p.language), stopReason)
	}

	var stacksBuf [4]glrStack
	stacks := stacksBuf[:1]
	initialStackCap := 64 * 1024
	if reuse != nil {
		// Incremental reparses often borrow scratch slabs from an earlier full
		// parse. Preallocating that full retained capacity forces large memclr
		// work on every edit; keep incremental stack preallocation modest.
		initialStackCap = defaultStackEntrySlabCap
	}
	stacks[0] = newGLRStackWithScratchCap(p.language.InitialState, &scratch.entries, initialStackCap)
	stacks[0].recoverabilityKnown = true
	stacks[0].mayRecover = p.stateCanRecover(p.language.InitialState)
	maxStacksSeen = len(stacks)
	if timing != nil && timing.maxStacksSeen < len(stacks) {
		timing.maxStacksSeen = len(stacks)
	}
	maxStacks := parseMaxGLRStacksValue()
	mergePerKeyCap := maxStacksPerMergeKey
	if reuse != nil {
		// Incremental reparses benefit from tighter GLR retention because
		// edits are localized and we prioritize latency over broad ambiguity fanout.
		if maxStacks > 32 {
			maxStacks = 32
		}
		if mergePerKeyCap > 4 {
			mergePerKeyCap = 4
		}
	}
	scratch.merge.perKeyCap = mergePerKeyCap

	maxIter := parseIterations(len(source))
	maxDepth := parseStackDepth(len(source))
	maxNodes := parseNodeLimit(len(source))
	parseRuntime.IterationLimit = maxIter
	parseRuntime.StackDepthLimit = maxDepth
	parseRuntime.NodeLimit = maxNodes

	needToken := true
	var tok Token

	// Per-primary-stack infinite-reduce detection.
	var lastReduceState StateID
	var consecutiveReduces int

	for iter := 0; iter < maxIter; iter++ {
		iterationsUsed = iter + 1
		if perfCountersEnabled {
			perfRecordMaxConcurrentStacks(len(stacks))
		}
		if timing != nil && len(stacks) > timing.maxStacksSeen {
			timing.maxStacksSeen = len(stacks)
		}
		if len(stacks) > maxStacksSeen {
			maxStacksSeen = len(stacks)
		}
		// Fast-path the overwhelmingly common non-GLR case with one live stack.
		if len(stacks) == 1 {
			if stacks[0].dead {
				return finalizeErrorTree(ParseStopNoStacksAlive)
			}
		} else {
			// Prune dead stacks and collapse only truly duplicate stack versions.
			stacks = mergeStacksWithScratch(stacks, &scratch.merge)
			if len(stacks) == 0 {
				return finalizeErrorTree(ParseStopNoStacksAlive)
			}
		}
		// Cap the number of parallel stacks to prevent combinatorial explosion.
		// Keep the most promising stacks instead of truncating by insertion
		// order, which can discard viable parses on highly-ambiguous inputs.
		if len(stacks) > maxStacks {
			if perfCountersEnabled {
				perfRecordGlobalCapCull(len(stacks), maxStacks)
			}
			stacks = retainTopStacks(stacks, maxStacks)
		}

		// Keep the most promising stack in slot 0 because several parser
		// heuristics (lex-mode selection, reduce-loop detection, depth cap)
		// currently key off the primary stack.
		if len(stacks) > 1 {
			p.promotePrimaryStack(stacks)
		}
		for i := range stacks {
			stacks[i].cacheEntries = false
			if stacks[i].gss.head != nil {
				stacks[i].entries = nil
			}
		}

		// Safety: if the primary stack has grown beyond the depth cap,
		// or we've allocated too many nodes, return what we have.
		primaryDepth := stacks[0].depth()
		if primaryDepth > peakStackDepth {
			peakStackDepth = primaryDepth
		}
		if primaryDepth > maxDepth {
			return finalize(stacks, ParseStopStackDepthLimit)
		}
		if nodeCount > maxNodes {
			return finalize(stacks, ParseStopNodeLimit)
		}

		// Use the primary (first) stack's state for DFA lex mode selection.
		// Pass all active GLR stack states so external scanner valid symbols
		// are computed as the union across all stacks.
		if stateful, ok := ts.(parserStateTokenSource); ok {
			stateful.SetParserState(stacks[0].top().state)
			if len(stacks) > 1 {
				glrBuf := scratch.glrStates[:0]
				if cap(glrBuf) < len(stacks) {
					glrBuf = make([]StateID, 0, len(stacks))
				}
				for si := range stacks {
					if !stacks[si].dead {
						glrBuf = append(glrBuf, stacks[si].top().state)
					}
				}
				scratch.glrStates = glrBuf
				stateful.SetGLRStates(glrBuf)
			} else {
				if len(scratch.glrStates) > 0 {
					scratch.glrStates = scratch.glrStates[:0]
				}
				stateful.SetGLRStates(nil)
			}
		}

		// --- Token acquisition and incremental reuse ---
		if needToken {
			tok = ts.Next()
			perfTokensConsumed++
			lastTokenEndByte = tok.EndByte
			lastTokenSymbol = tok.Symbol
			lastTokenWasEOF = tok.Symbol == 0 && tok.StartByte == tok.EndByte
			if lastTokenWasEOF && tok.EndByte < expectedEOFByte {
				tokenSourceEOFEarly = true
			}
			// Clear per-stack shifted flags so all stacks process the
			// new token.
			for si := range stacks {
				stacks[si].shifted = false
			}
		}

		// Incremental parsing fast-path: when there is a single active stack,
		// try to reuse an unchanged subtree starting at the current token.
		if reuse != nil && len(stacks) == 1 && !stacks[0].dead && tok.Symbol != 0 {
			if timing != nil {
				reuseStart := time.Now()
				nextTok, reusedBytes, ok := p.tryReuseSubtree(&stacks[0], tok, ts, reuse, &scratch.entries, &scratch.gss)
				timing.reuseNanos += time.Since(reuseStart).Nanoseconds()
				if ok {
					timing.reusedSubtrees++
					timing.reusedBytes += uint64(reusedBytes)
					reuseState.markReused(stacks[0].top().node, arena)
					tok = nextTok
					needToken = false
					consecutiveReduces = 0
					continue
				}
			} else {
				if nextTok, _, ok := p.tryReuseSubtree(&stacks[0], tok, ts, reuse, &scratch.entries, &scratch.gss); ok {
					reuseState.markReused(stacks[0].top().node, arena)
					tok = nextTok
					needToken = false
					consecutiveReduces = 0
					continue
				}
			}
		}

		// --- Action application for all alive stacks ---
		// Process all alive stacks for this token.
		// We iterate by index because forks may append to `stacks`.
		numStacks := len(stacks)
		anyReduced := false

		parseActions := p.language.ParseActions
		for si := 0; si < numStacks; si++ {
			s := &stacks[si]
			if s.dead || s.shifted {
				continue
			}

			currentState := s.top().state
			actionIdx := p.lookupActionIndex(currentState, tok.Symbol)
			var actions []ParseAction
			if actionIdx != 0 && int(actionIdx) < len(parseActions) {
				actions = parseActions[actionIdx].Actions
			}

			// --- Extra token handling (comments, whitespace) ---
			if len(actions) > 0 &&
				actions[0].Type == ParseActionShift && actions[0].Extra {
				named := p.isNamedSymbol(tok.Symbol)
				leaf := newLeafNodeInArena(arena, tok.Symbol, named,
					tok.StartByte, tok.EndByte, tok.StartPoint, tok.EndPoint)
				leaf.isExtra = true
				leaf.preGotoState = currentState
				leaf.parseState = currentState
				p.pushStackNode(s, currentState, leaf, &scratch.entries, &scratch.gss)
				nodeCount++
				needToken = true
				continue
			}

			// --- No action: error handling ---
			if len(actions) == 0 {
				if tok.Symbol == 0 {
					if tok.StartByte == tok.EndByte {
						// True EOF. If this is the only stack, return result.
						if len(stacks) == 1 {
							return finalize(stacks, ParseStopAccepted)
						}
						// Multiple stacks at EOF: this one is done.
						// Mark dead so merge picks the best remaining.
						s.dead = true
						continue
					}
					// Zero-symbol width token: skip.
					needToken = true
					continue
				}

				// When multiple alternatives exist, drop no-action stacks
				// immediately instead of running deep recovery scans.
				if len(stacks) > 1 {
					s.dead = true
					continue
				}

				// Try grammar-directed recovery by searching the stack for
				// the nearest state that can recover on this lookahead.
				if depth, recoverAct, ok := p.findRecoverActionOnStack(s, tok.Symbol, timing); ok {
					if !s.truncate(depth + 1) {
						s.dead = true
						continue
					}
					p.applyAction(s, recoverAct, tok, &anyReduced, &nodeCount, arena, &scratch.entries, &scratch.gss, &scratch.tmpEntries, deferParentLinks, &trackChildErrors)
					needToken = true
					continue
				}

				// Only stack: error recovery — wrap token in error node.
				if s.depth() == 0 {
					return finalize(stacks, ParseStopNoStacksAlive)
				}
				errNode := newLeafNodeInArena(arena, errorSymbol, false,
					tok.StartByte, tok.EndByte, tok.StartPoint, tok.EndPoint)
				errNode.hasError = true
				trackChildErrors = true
				if perfCountersEnabled {
					perfRecordErrorNode()
				}
				errNode.parseState = currentState
				p.pushStackNode(s, currentState, errNode, &scratch.entries, &scratch.gss)
				nodeCount++
				needToken = true
				continue
			}

			// --- GLR: fork for multiple actions ---
			// For single-action entries (the common case), no fork occurs.
			// For multi-action entries, clone the stack for each alternative.
			if len(actions) > 1 {
				if perfCountersEnabled {
					rrConflict, rsConflict := classifyConflictShape(actions)
					switch {
					case rrConflict:
						perfRecordConflictRR()
					case rsConflict:
						perfRecordConflictRS()
					default:
						perfRecordConflictOther()
					}
				}
				if reuse != nil {
					act := actions[0]
					p.applyAction(s, act, tok, &anyReduced, &nodeCount, arena, &scratch.entries, &scratch.gss, &scratch.tmpEntries, deferParentLinks, &trackChildErrors)
					continue
				}
				if perfCountersEnabled {
					perfRecordFork(len(actions), perfTokensConsumed)
				}
				// Deep-stack GLR forks can trigger pathological clone volumes on
				// very large inputs. At extreme depths, take the primary action
				// to keep parsing bounded.
				if s.depth() > maxForkCloneDepth {
					act := actions[0]
					p.applyAction(s, act, tok, &anyReduced, &nodeCount, arena, &scratch.entries, &scratch.gss, &scratch.tmpEntries, deferParentLinks, &trackChildErrors)
					continue
				}
				// Copy the current stack value before appending forks.
				// Appending can reallocate `stacks`, which would invalidate `s`.
				base := *s
				for ai := 1; ai < len(actions); ai++ {
					fork := base.cloneWithScratch(&scratch.gss)
					act := actions[ai]
					p.applyAction(&fork, act, tok, &anyReduced, &nodeCount, arena, &scratch.entries, &scratch.gss, &scratch.tmpEntries, deferParentLinks, &trackChildErrors)
					stacks = append(stacks, fork)
				}
				// Re-acquire the pointer after possible reallocation.
				s = &stacks[si]
				act := actions[0]
				p.applyAction(s, act, tok, &anyReduced, &nodeCount, arena, &scratch.entries, &scratch.gss, &scratch.tmpEntries, deferParentLinks, &trackChildErrors)
			} else {
				act := actions[0]
				if act.Type == ParseActionReduce {
					p.applyActionWithReduceChain(s, act, tok, &anyReduced, &nodeCount, arena, &scratch.entries, &scratch.gss, &scratch.tmpEntries, deferParentLinks, &trackChildErrors)
				} else {
					p.applyAction(s, act, tok, &anyReduced, &nodeCount, arena, &scratch.entries, &scratch.gss, &scratch.tmpEntries, deferParentLinks, &trackChildErrors)
				}
			}
		}

		// After processing all stacks: determine whether to advance the
		// token. If any stack reduced, reuse the same token (the reducing
		// stacks have new top states and need to re-check the action for
		// the current lookahead). Otherwise, advance to next token.
		if anyReduced {
			needToken = false

			// Infinite-reduce detection (for the primary stack).
			if len(stacks) > 0 && !stacks[0].dead {
				topState := stacks[0].top().state
				if topState == lastReduceState {
					consecutiveReduces++
				} else {
					lastReduceState = topState
					consecutiveReduces = 1
				}
				if consecutiveReduces > maxConsecutivePrimaryReduces {
					needToken = true
					consecutiveReduces = 0
				}
			}
		} else {
			needToken = true
			consecutiveReduces = 0
		}

		// Check for accept on any stack.
		for si := range stacks {
			if stacks[si].accepted {
				return finalize(stacks[si:si+1], ParseStopAccepted)
			}
		}
	}

	// Iteration limit reached.
	return finalize(stacks, ParseStopIterationLimit)
}

func appendUniqueArenaRef(refs []*nodeArena, arenaRef, exclude *nodeArena) []*nodeArena {
	if arenaRef == nil || arenaRef == exclude {
		return refs
	}
	for i := range refs {
		if refs[i] == arenaRef {
			return refs
		}
	}
	return append(refs, arenaRef)
}

func (p *Parser) promotePrimaryStack(stacks []glrStack) {
	if len(stacks) <= 1 {
		return
	}
	best := 0
	for i := 1; i < len(stacks); i++ {
		if stackComparePtr(&stacks[i], &stacks[best]) > 0 {
			best = i
		}
	}
	if best != 0 {
		stacks[0], stacks[best] = stacks[best], stacks[0]
	}
}

func retainTopStacks(stacks []glrStack, keep int) []glrStack {
	if keep <= 0 {
		return stacks[:0]
	}
	if len(stacks) <= keep {
		return stacks
	}
	for i := 0; i < keep; i++ {
		best := i
		for j := i + 1; j < len(stacks); j++ {
			if stackComparePtr(&stacks[j], &stacks[best]) > 0 {
				best = j
			}
		}
		if best != i {
			stacks[i], stacks[best] = stacks[best], stacks[i]
		}
	}
	return stacks[:keep]
}

func classifyConflictShape(actions []ParseAction) (rrConflict, rsConflict bool) {
	if len(actions) < 2 {
		return false, false
	}
	reduceCount := 0
	hasShift := false
	hasOther := false
	for i := range actions {
		switch actions[i].Type {
		case ParseActionReduce:
			reduceCount++
		case ParseActionShift:
			hasShift = true
		default:
			hasOther = true
		}
	}
	if hasOther || reduceCount == 0 {
		return false, false
	}
	if hasShift {
		return false, true
	}
	return reduceCount >= 2, false
}

// buildResultFromGLR picks the best stack and constructs the final tree.
// Prefers accepted stacks, then highest score, then most entries.
func (p *Parser) buildResultFromGLR(stacks []glrStack, source []byte, arena *nodeArena, oldTree *Tree, reuseState *parseReuseState, linkScratch *[]*Node) *Tree {
	if len(stacks) == 0 {
		arena.Release()
		return parseErrorTree(source, p.language)
	}

	best := 0
	for i := 1; i < len(stacks); i++ {
		if stackComparePtr(&stacks[i], &stacks[best]) > 0 {
			best = i
		}
	}

	selected := stacks[best]
	if len(selected.entries) > 0 {
		return p.buildResult(selected.entries, source, arena, oldTree, reuseState, linkScratch)
	}
	if selected.gss.head == nil {
		return p.buildResult(nil, source, arena, oldTree, reuseState, linkScratch)
	}
	return p.buildResultFromNodes(nodesFromGSS(selected.gss), source, arena, oldTree, reuseState, linkScratch)
}

// isNamedSymbol checks whether a symbol is a named symbol.
func (p *Parser) isNamedSymbol(sym Symbol) bool {
	if int(sym) < len(p.language.SymbolMetadata) {
		return p.language.SymbolMetadata[sym].Named
	}
	return false
}

func nodesFromGSS(stack gssStack) []*Node {
	if stack.head == nil {
		return nil
	}
	count := 0
	for n := stack.head; n != nil; n = n.prev {
		if n.entry.node != nil {
			count++
		}
	}
	if count == 0 {
		return nil
	}
	nodes := make([]*Node, count)
	i := count - 1
	for n := stack.head; n != nil; n = n.prev {
		if n.entry.node != nil {
			nodes[i] = n.entry.node
			i--
		}
	}
	return nodes
}

// buildResult constructs the final Tree from a stack of entries.
func (p *Parser) buildResult(stack []stackEntry, source []byte, arena *nodeArena, oldTree *Tree, reuseState *parseReuseState, linkScratch *[]*Node) *Tree {
	var nodes []*Node
	for _, entry := range stack {
		if entry.node != nil {
			nodes = append(nodes, entry.node)
		}
	}
	return p.buildResultFromNodes(nodes, source, arena, oldTree, reuseState, linkScratch)
}

func (p *Parser) buildResultFromNodes(nodes []*Node, source []byte, arena *nodeArena, oldTree *Tree, reuseState *parseReuseState, linkScratch *[]*Node) *Tree {
	if len(nodes) == 0 {
		arena.Release()
		if isWhitespaceOnlySource(source) {
			return NewTree(nil, source, p.language)
		}
		return parseErrorTree(source, p.language)
	}

	if arena != nil && arena.used == 0 {
		arena.Release()
		arena = nil
	}

	expectedRootSymbol := Symbol(0)
	hasExpectedRoot := false
	shouldWireParentLinks := oldTree == nil
	if oldTree != nil && oldTree.RootNode() != nil {
		expectedRootSymbol = oldTree.RootNode().symbol
		hasExpectedRoot = true
	}
	borrowedResolved := false
	var borrowed []*nodeArena
	getBorrowed := func() []*nodeArena {
		if borrowedResolved {
			return borrowed
		}
		borrowed = reuseState.retainBorrowed(arena)
		borrowedResolved = true
		return borrowed
	}

	if len(nodes) == 1 {
		candidate := nodes[0]
		extendNodeToTrailingWhitespace(candidate, source)
		if !hasExpectedRoot || candidate.symbol == expectedRootSymbol {
			if shouldWireParentLinks {
				wireParentLinksWithScratch(candidate, linkScratch)
			}
			return newTreeWithArenas(candidate, source, p.language, arena, getBorrowed())
		}

		// Incremental reuse guard: if the only stacked node doesn't match the
		// previous root symbol, synthesize an expected root wrapper instead of
		// returning a reused child as the new tree root.
		rootChildren := make([]*Node, 1)
		rootChildren[0] = candidate
		if arena != nil {
			rootChildren = arena.allocNodeSlice(1)
			rootChildren[0] = candidate
		}
		root := newParentNodeInArena(arena, expectedRootSymbol, true, rootChildren, nil, 0)
		extendNodeToTrailingWhitespace(root, source)
		if shouldWireParentLinks {
			wireParentLinksWithScratch(root, linkScratch)
		}
		return newTreeWithArenas(root, source, p.language, arena, getBorrowed())
	}

	// When multiple nodes remain on the stack, check whether all but one
	// are extras (e.g. leading whitespace/comments). If so, fold the extras
	// into the real root rather than wrapping everything in an error node.
	var realRoot *Node
	var allExtras []*Node
	var extras []*Node
	for _, n := range nodes {
		if n.isExtra {
			allExtras = append(allExtras, n)
			// Ignore invisible extras in final-root recovery; they should not
			// force an error wrapper or inflate root child counts.
			if p != nil && p.language != nil && int(n.symbol) < len(p.language.SymbolMetadata) && p.language.SymbolMetadata[n.symbol].Visible {
				extras = append(extras, n)
			}
		} else {
			if realRoot != nil {
				realRoot = nil // more than one non-extra → genuine error
				break
			}
			realRoot = n
		}
	}
	if realRoot != nil {
		if reuseState != nil && reuseState.reusedAny {
			realRoot = cloneNodeInArena(arena, realRoot)
			realRoot.parent = nil
			realRoot.childIndex = -1
		}
		if len(extras) > 0 {
			// Fold visible extras into the real root as leading/trailing children.
			merged := make([]*Node, 0, len(extras)+len(realRoot.children))
			leadingCount := 0
			for _, e := range extras {
				if e.startByte <= realRoot.startByte {
					merged = append(merged, e)
					leadingCount++
				}
			}
			merged = append(merged, realRoot.children...)
			for _, e := range extras {
				if e.startByte > realRoot.startByte {
					merged = append(merged, e)
				}
			}
			if arena != nil {
				out := arena.allocNodeSlice(len(merged))
				copy(out, merged)
				merged = out
			}
			realRoot.children = merged
			// Keep fieldIDs aligned with children: extras have no field (0).
			if len(realRoot.fieldIDs) > 0 {
				trailingCount := len(extras) - leadingCount
				padded := make([]FieldID, leadingCount+len(realRoot.fieldIDs)+trailingCount)
				copy(padded[leadingCount:], realRoot.fieldIDs)
				realRoot.fieldIDs = padded
			}
			// Extend root range to cover the extras.
			for _, e := range extras {
				if e.startByte < realRoot.startByte {
					realRoot.startByte = e.startByte
					realRoot.startPoint = e.startPoint
				}
				if e.endByte > realRoot.endByte {
					realRoot.endByte = e.endByte
					realRoot.endPoint = e.endPoint
				}
			}
		}
		// Invisible extras should still contribute to the root byte/point range.
		for _, e := range allExtras {
			if e.startByte < realRoot.startByte {
				realRoot.startByte = e.startByte
				realRoot.startPoint = e.startPoint
			}
			if e.endByte > realRoot.endByte {
				realRoot.endByte = e.endByte
				realRoot.endPoint = e.endPoint
			}
		}
		extendNodeToTrailingWhitespace(realRoot, source)
		if !hasExpectedRoot || realRoot.symbol == expectedRootSymbol {
			if shouldWireParentLinks {
				wireParentLinksWithScratch(realRoot, linkScratch)
			}
			return newTreeWithArenas(realRoot, source, p.language, arena, getBorrowed())
		}
	}

	rootChildren := nodes
	rootSymbol := nodes[len(nodes)-1].symbol
	if hasExpectedRoot {
		rootSymbol = expectedRootSymbol
	}
	root := newParentNodeInArena(arena, rootSymbol, true, rootChildren, nil, 0)
	root.hasError = true
	extendNodeToTrailingWhitespace(root, source)
	if shouldWireParentLinks {
		wireParentLinksWithScratch(root, linkScratch)
	}
	return newTreeWithArenas(root, source, p.language, arena, getBorrowed())
}
