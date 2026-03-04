package gotreesitter

func (p *Parser) applyActionWithReduceChain(s *glrStack, act ParseAction, tok Token, anyReduced *bool, nodeCount *int, arena *nodeArena, entryScratch *glrEntryScratch, gssScratch *gssScratch, tmpEntries *[]stackEntry, deferParentLinks bool, trackChildErrors *bool) {
	p.applyAction(s, act, tok, anyReduced, nodeCount, arena, entryScratch, gssScratch, tmpEntries, deferParentLinks, trackChildErrors)
	if act.Type != ParseActionReduce || s == nil || s.dead || s.accepted || s.shifted {
		return
	}
	p.chainSingleReduceActions(s, tok, anyReduced, nodeCount, arena, entryScratch, gssScratch, tmpEntries, deferParentLinks, trackChildErrors)
}

func (p *Parser) chainSingleReduceActions(s *glrStack, tok Token, anyReduced *bool, nodeCount *int, arena *nodeArena, entryScratch *glrEntryScratch, gssScratch *gssScratch, tmpEntries *[]stackEntry, deferParentLinks bool, trackChildErrors *bool) {
	if s == nil || s.dead || s.accepted || s.shifted {
		return
	}
	const maxInlineReduceChain = 256
	parseActions := p.language.ParseActions
	chainLen := 0
	for chainLen < maxInlineReduceChain {
		currentState := s.top().state
		actionIdx := p.lookupActionIndex(currentState, tok.Symbol)
		if actionIdx == 0 || int(actionIdx) >= len(parseActions) {
			return
		}

		actions := parseActions[actionIdx].Actions
		if len(actions) != 1 {
			if perfCountersEnabled {
				perfRecordReduceChainBreakMulti()
			}
			return
		}

		next := actions[0]
		switch next.Type {
		case ParseActionReduce:
			chainLen++
			if perfCountersEnabled {
				perfRecordReduceChainStep(chainLen)
			}
			p.applyAction(s, next, tok, anyReduced, nodeCount, arena, entryScratch, gssScratch, tmpEntries, deferParentLinks, trackChildErrors)
			if s.dead || s.accepted || s.shifted {
				return
			}
		case ParseActionShift:
			if perfCountersEnabled {
				perfRecordReduceChainBreakShift()
			}
			return
		case ParseActionAccept:
			if perfCountersEnabled {
				perfRecordReduceChainBreakAccept()
			}
			return
		default:
			if perfCountersEnabled {
				perfRecordReduceChainBreakMulti()
			}
			return
		}
	}
}

// applyAction applies a single parse action to a GLR stack.
func (p *Parser) applyAction(s *glrStack, act ParseAction, tok Token, anyReduced *bool, nodeCount *int, arena *nodeArena, entryScratch *glrEntryScratch, gssScratch *gssScratch, tmpEntries *[]stackEntry, deferParentLinks bool, trackChildErrors *bool) {
	switch act.Type {
	case ParseActionShift:
		named := p.isNamedSymbol(tok.Symbol)
		leaf := newLeafNodeInArena(arena, tok.Symbol, named,
			tok.StartByte, tok.EndByte, tok.StartPoint, tok.EndPoint)
		leaf.isExtra = act.Extra
		if leaf.isExtra && perfCountersEnabled {
			perfRecordExtraNode()
		}
		leaf.preGotoState = s.top().state
		leaf.parseState = act.State
		p.pushStackNode(s, act.State, leaf, entryScratch, gssScratch)
		s.shifted = true
		*nodeCount++

	case ParseActionReduce:
		entries := s.entries
		borrowed := false
		if entries == nil {
			if !s.cacheEntries && s.gss.head != nil {
				tmp := []stackEntry(nil)
				if tmpEntries != nil {
					tmp = *tmpEntries
				}
				p.applyReduceActionFromGSS(s, act, anyReduced, nodeCount, arena, entryScratch, gssScratch, tmpEntries, tmp, deferParentLinks, trackChildErrors != nil && *trackChildErrors)
				return
			}
			if s.cacheEntries {
				entries = s.ensureEntries(entryScratch)
			} else {
				tmp := []stackEntry(nil)
				if tmpEntries != nil {
					tmp = *tmpEntries
				}
				entries, borrowed = s.entriesForRead(tmp)
			}
		}
		p.applyReduceAction(s, act, anyReduced, nodeCount, arena, entryScratch, gssScratch, entries, deferParentLinks, trackChildErrors != nil && *trackChildErrors)
		if borrowed && tmpEntries != nil {
			*tmpEntries = entries[:0]
		}

	case ParseActionAccept:
		s.accepted = true

	case ParseActionRecover:
		if tok.Symbol == 0 && tok.StartByte == tok.EndByte {
			s.accepted = true
			return
		}
		errNode := newLeafNodeInArena(arena, errorSymbol, false,
			tok.StartByte, tok.EndByte, tok.StartPoint, tok.EndPoint)
		errNode.hasError = true
		if trackChildErrors != nil {
			*trackChildErrors = true
		}
		if perfCountersEnabled {
			perfRecordErrorNode()
		}
		recoverState := s.top().state
		if act.State != 0 {
			recoverState = act.State
		}
		errNode.parseState = recoverState
		p.pushStackNode(s, recoverState, errNode, entryScratch, gssScratch)
		*nodeCount++
	}
}

func (p *Parser) pushStackNode(s *glrStack, state StateID, node *Node, entryScratch *glrEntryScratch, gssScratch *gssScratch) {
	s.push(state, node, entryScratch, gssScratch)
	if !s.recoverabilityKnown {
		return
	}
	if !s.mayRecover && p.stateCanRecover(state) {
		s.mayRecover = true
	}
}

func reduceWindowFromGSS(s *glrStack, childCount int, buf []stackEntry) ([]stackEntry, StateID, bool) {
	if s == nil || s.gss.head == nil || s.depth() == 0 {
		return nil, 0, false
	}
	if childCount == 0 {
		return buf[:0], s.top().state, true
	}

	rev := buf[:0]
	nonExtraFound := 0
	n := s.gss.head
	for n != nil {
		rev = append(rev, n.entry)
		if n.entry.node != nil && !n.entry.node.isExtra {
			nonExtraFound++
			if nonExtraFound == childCount {
				break
			}
		}
		n = n.prev
	}
	if nonExtraFound < childCount || n == nil || n.prev == nil {
		return rev[:0], 0, false
	}
	topState := n.prev.entry.state

	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, topState, true
}

func (p *Parser) applyReduceActionFromGSS(s *glrStack, act ParseAction, anyReduced *bool, nodeCount *int, arena *nodeArena, entryScratch *glrEntryScratch, gssScratch *gssScratch, tmpEntries *[]stackEntry, tmp []stackEntry, deferParentLinks bool, trackChildErrors bool) {
	childCount := int(act.ChildCount)
	windowEntries, topState, ok := reduceWindowFromGSS(s, childCount, tmp)
	if !ok {
		s.dead = true
		if tmpEntries != nil {
			*tmpEntries = windowEntries[:0]
		}
		return
	}

	actualEnd := len(windowEntries)
	reducedEnd := actualEnd
	for i := actualEnd - 1; i >= 0; i-- {
		n := windowEntries[i].node
		if n == nil || !n.isExtra {
			break
		}
		reducedEnd--
	}

	children, fieldIDs := p.buildReduceChildren(windowEntries, 0, reducedEnd, childCount, act.ProductionID, arena)

	targetDepth := s.depth() - actualEnd
	if targetDepth < 0 || !s.truncate(targetDepth) {
		s.dead = true
		if tmpEntries != nil {
			*tmpEntries = windowEntries[:0]
		}
		return
	}

	named := p.isNamedSymbol(act.Symbol)
	var parent *Node
	if deferParentLinks {
		parent = newParentNodeInArenaNoLinks(arena, act.Symbol, named, children, fieldIDs, act.ProductionID, trackChildErrors)
	} else {
		parent = newParentNodeInArena(arena, act.Symbol, named, children, fieldIDs, act.ProductionID)
	}
	shouldUseRawSpan := len(children) == 0
	if !shouldUseRawSpan && p.forceRawSpanAll {
		shouldUseRawSpan = true
	}
	if !shouldUseRawSpan && int(act.Symbol) < len(p.forceRawSpanTable) && p.forceRawSpanTable[act.Symbol] {
		shouldUseRawSpan = true
	}
	if shouldUseRawSpan && reducedEnd > 0 {
		span := computeReduceRawSpan(windowEntries, 0, reducedEnd)
		parent.startByte = span.startByte
		parent.endByte = span.endByte
		parent.startPoint = span.startPoint
		parent.endPoint = span.endPoint
	}
	// Extend parent span to cover invisible children dropped by buildReduceChildren.
	extendParentSpanToWindow(parent, windowEntries, 0, reducedEnd, p.language.SymbolMetadata)
	*nodeCount++

	gotoState := p.lookupGoto(topState, act.Symbol)
	targetState := topState
	if gotoState != 0 {
		targetState = gotoState
	}
	// Nonterminal extras: mark the reduced node as extra and stay in same state.
	if act.Extra {
		parent.isExtra = true
		targetState = topState
	}
	parent.preGotoState = topState
	parent.parseState = targetState
	p.pushStackNode(s, targetState, parent, entryScratch, gssScratch)
	for i := reducedEnd; i < actualEnd; i++ {
		extra := windowEntries[i].node
		if extra == nil {
			continue
		}
		extra.parseState = targetState
		p.pushStackNode(s, targetState, extra, entryScratch, gssScratch)
	}

	s.score += int(act.DynamicPrecedence)
	*anyReduced = true
	if tmpEntries != nil {
		*tmpEntries = windowEntries[:0]
	}
}

type reduceRange struct {
	start      int
	reducedEnd int
	actualEnd  int
	topState   StateID
}

type reduceRawSpan struct {
	startByte  uint32
	endByte    uint32
	startPoint Point
	endPoint   Point
}

func computeReduceRange(entries []stackEntry, childCount int) (reduceRange, bool) {
	start := len(entries)
	nonExtraFound := 0
	for nonExtraFound < childCount && start > 1 {
		start--
		if entries[start].node != nil && !entries[start].node.isExtra {
			nonExtraFound++
		}
	}
	if nonExtraFound < childCount {
		return reduceRange{}, false
	}

	actualEnd := len(entries)
	reducedEnd := actualEnd
	for i := actualEnd - 1; i >= start; i-- {
		n := entries[i].node
		if n == nil || !n.isExtra {
			break
		}
		reducedEnd--
	}
	return reduceRange{
		start:      start,
		reducedEnd: reducedEnd,
		actualEnd:  actualEnd,
		topState:   entries[start-1].state,
	}, true
}

func computeReduceRawSpan(entries []stackEntry, start, end int) reduceRawSpan {
	span := reduceRawSpan{}
	if end <= start {
		return span
	}

	foundStart := false
	for i := start; i < end; i++ {
		n := entries[i].node
		if n != nil && !n.isExtra {
			span.startByte = n.startByte
			span.startPoint = n.startPoint
			foundStart = true
			break
		}
	}

	foundEnd := false
	for i := end - 1; i >= start; i-- {
		n := entries[i].node
		if n != nil && !n.isExtra {
			span.endByte = n.endByte
			span.endPoint = n.endPoint
			foundEnd = true
			break
		}
	}

	firstRaw := entries[start].node
	lastRaw := entries[end-1].node
	if !foundStart && firstRaw != nil {
		span.startByte = firstRaw.startByte
		span.startPoint = firstRaw.startPoint
	}
	if !foundEnd && lastRaw != nil {
		span.endByte = lastRaw.endByte
		span.endPoint = lastRaw.endPoint
	}
	return span
}

// extendParentSpanToWindow widens the parent node's [startByte, endByte] to
// recover span from entries that buildReduceChildren drops. Two categories:
//
//  1. Leading extras: extend startByte backward (extras before first structural child).
//  2. Invisible non-extra leaf children: these are structural children whose symbol
//     is not visible AND that have no children to inline. buildReduceChildren skips
//     them entirely (the "if len(kids) == 0 { continue }" path), losing their span.
//     In C tree-sitter, ts_subtree_set_children includes ALL children in the parent
//     span, so we must recover these dropped spans to match.
//
// Trailing extras (separated into [reducedEnd, actualEnd)) are NOT scanned because
// they become siblings of the parent, not children.
func extendParentSpanToWindow(parent *Node, entries []stackEntry, start, reducedEnd int, symbolMeta []SymbolMetadata) {
	// Leading extras: extend startByte backward until the first structural child.
	for i := start; i < reducedEnd; i++ {
		n := entries[i].node
		if n == nil {
			continue
		}
		if !n.isExtra {
			break
		}
		if n.startByte < parent.startByte {
			parent.startByte = n.startByte
			parent.startPoint = n.startPoint
		}
	}
	// Invisible non-extra leaf children: extend parent span for entries that
	// buildReduceChildren drops (invisible symbol, no children to inline).
	// Only extend for children that are contiguous with the current parent
	// span (child.startByte <= parent.endByte for end extension, or
	// child.endByte >= parent.startByte for start extension). Entries
	// that start beyond the parent (e.g. zero-width _automatic_semicolon
	// markers in javascript ASI) are not real content children and must
	// not inflate the parent span.
	for i := start; i < reducedEnd; i++ {
		n := entries[i].node
		if n == nil || n.isExtra {
			continue
		}
		visible := true
		if idx := int(n.symbol); idx < len(symbolMeta) {
			visible = symbolMeta[n.symbol].Visible
		}
		if visible {
			continue // visible children are already represented in parent's children
		}
		// Invisible entries (with or without children) may have span that
		// extends beyond their inlined children due to nested invisible leaf
		// extensions. Apply contiguity check below.
		if n.endByte >= parent.startByte && n.startByte < parent.startByte {
			parent.startByte = n.startByte
			parent.startPoint = n.startPoint
		}
		if n.startByte <= parent.endByte && n.endByte > parent.endByte {
			parent.endByte = n.endByte
			parent.endPoint = n.endPoint
		}
	}
}

func (p *Parser) buildReduceChildren(entries []stackEntry, start, end, childCount int, productionID uint16, arena *nodeArena) ([]*Node, []FieldID) {
	lang := p.language
	symbolMeta := lang.SymbolMetadata

	var aliasSeq []Symbol
	if pid := int(productionID); pid >= 0 && pid < len(lang.AliasSequences) {
		aliasSeq = lang.AliasSequences[pid]
	}

	normalizedCount := 0
	structuralChildIndex := 0
	for i := start; i < end; i++ {
		n := entries[i].node
		if n == nil {
			continue
		}
		effectiveSymbol := n.symbol
		if !n.isExtra {
			if structuralChildIndex < len(aliasSeq) {
				if alias := aliasSeq[structuralChildIndex]; alias != 0 {
					effectiveSymbol = alias
				}
			}
			structuralChildIndex++
		}
		visible := true
		if idx := int(effectiveSymbol); idx < len(symbolMeta) {
			visible = symbolMeta[effectiveSymbol].Visible
		}
		if visible {
			normalizedCount++
		} else {
			normalizedCount += len(n.children)
		}
	}

	rawFieldIDs := p.buildFieldIDs(childCount, productionID, arena)
	children := arena.allocNodeSlice(normalizedCount)
	var fieldIDs []FieldID
	if rawFieldIDs != nil {
		fieldIDs = arena.allocFieldIDSlice(normalizedCount)
	}

	out := 0
	structuralChildIndex = 0
	for i := start; i < end; i++ {
		n := entries[i].node
		if n == nil {
			continue
		}
		var fid FieldID
		if !n.isExtra {
			if structuralChildIndex < len(rawFieldIDs) {
				fid = rawFieldIDs[structuralChildIndex]
			}
			if structuralChildIndex < len(aliasSeq) {
				if alias := aliasSeq[structuralChildIndex]; alias != 0 {
					n = aliasedNodeInArena(arena, lang, n, alias)
				}
			}
			structuralChildIndex++
		}

		visible := true
		if idx := int(n.symbol); idx < len(symbolMeta) {
			visible = symbolMeta[n.symbol].Visible
		}
		if visible {
			children[out] = n
			if fieldIDs != nil {
				fieldIDs[out] = fid
			}
			out++
			continue
		}

		kids := n.children
		if len(kids) == 0 {
			continue
		}
		copy(children[out:], kids)
		if fieldIDs != nil {
			// Propagate the hidden node's own field IDs to the
			// inlined children. This implements "inherited" field
			// semantics: fields assigned inside hidden/non-visible
			// nodes become visible on the parent.
			if len(n.fieldIDs) > 0 {
				fieldEnd := out + len(kids)
				if fieldEnd > len(fieldIDs) {
					fieldEnd = len(fieldIDs)
				}
				for j := out; j < fieldEnd; j++ {
					kidIdx := j - out
					if kidIdx < len(n.fieldIDs) && n.fieldIDs[kidIdx] != 0 {
						fieldIDs[j] = n.fieldIDs[kidIdx]
					}
				}
			}
			// Apply the parent's inherited field assignment to the
			// first named inlined child that has no field yet. Named
			// nodes are the typical field targets; applying to an
			// anonymous token (e.g. "=") would be incorrect.
			if fid != 0 {
				for j := out; j < out+len(kids); j++ {
					if j < len(fieldIDs) && fieldIDs[j] == 0 && children[j] != nil && children[j].isNamed {
						fieldIDs[j] = fid
						break
					}
				}
			}
		}
		out += len(kids)
	}
	if out != len(children) {
		children = children[:out]
		if fieldIDs != nil {
			fieldIDs = fieldIDs[:out]
		}
	}
	return children, fieldIDs
}

func (p *Parser) applyReduceAction(s *glrStack, act ParseAction, anyReduced *bool, nodeCount *int, arena *nodeArena, entryScratch *glrEntryScratch, gssScratch *gssScratch, entries []stackEntry, deferParentLinks bool, trackChildErrors bool) {
	childCount := int(act.ChildCount)
	window, ok := computeReduceRange(entries, childCount)
	if !ok {
		// Not enough stack entries — kill this stack version.
		s.dead = true
		return
	}

	children, fieldIDs := p.buildReduceChildren(entries, window.start, window.reducedEnd, childCount, act.ProductionID, arena)

	trailingStart := window.reducedEnd
	trailingEnd := window.actualEnd

	// Pop all reduced entries in one step after collection.
	if !s.truncate(window.start) {
		s.dead = true
		return
	}

	named := p.isNamedSymbol(act.Symbol)
	var parent *Node
	if deferParentLinks {
		parent = newParentNodeInArenaNoLinks(arena, act.Symbol, named, children, fieldIDs, act.ProductionID, trackChildErrors)
	} else {
		parent = newParentNodeInArena(arena, act.Symbol, named, children, fieldIDs, act.ProductionID)
	}
	shouldUseRawSpan := len(children) == 0
	if !shouldUseRawSpan && p.forceRawSpanAll {
		shouldUseRawSpan = true
	}
	if !shouldUseRawSpan && int(act.Symbol) < len(p.forceRawSpanTable) && p.forceRawSpanTable[act.Symbol] {
		shouldUseRawSpan = true
	}
	if shouldUseRawSpan && window.reducedEnd > window.start {
		span := computeReduceRawSpan(entries, window.start, window.reducedEnd)
		parent.startByte = span.startByte
		parent.endByte = span.endByte
		parent.startPoint = span.startPoint
		parent.endPoint = span.endPoint
	}
	// Extend parent span to cover invisible children dropped by buildReduceChildren.
	extendParentSpanToWindow(parent, entries, window.start, window.reducedEnd, p.language.SymbolMetadata)
	*nodeCount++

	gotoState := p.lookupGoto(window.topState, act.Symbol)
	targetState := window.topState
	if gotoState != 0 {
		targetState = gotoState
	}
	// Nonterminal extras: mark the reduced node as extra and stay in same state.
	if act.Extra {
		parent.isExtra = true
		targetState = window.topState
	}
	parent.preGotoState = window.topState
	parent.parseState = targetState
	p.pushStackNode(s, targetState, parent, entryScratch, gssScratch)
	for i := trailingStart; i < trailingEnd; i++ {
		extra := entries[i].node
		if extra == nil {
			continue
		}
		extra.parseState = targetState
		p.pushStackNode(s, targetState, extra, entryScratch, gssScratch)
	}

	s.score += int(act.DynamicPrecedence)
	*anyReduced = true
}

func recoverAction(entry *ParseActionEntry) (ParseAction, bool) {
	if entry == nil {
		return ParseAction{}, false
	}
	for _, act := range entry.Actions {
		if act.Type == ParseActionRecover {
			return act, true
		}
	}
	return ParseAction{}, false
}

func (p *Parser) findRecoverActionOnStack(s *glrStack, sym Symbol, timing *incrementalParseTiming) (int, ParseAction, bool) {
	if s == nil {
		return 0, ParseAction{}, false
	}
	if s.recoverabilityKnown && !s.mayRecover {
		return 0, ParseAction{}, false
	}
	if timing != nil {
		timing.recoverSearches++
	}
	if !p.symbolCanRecover(sym) {
		if timing != nil {
			timing.recoverSymbolSkips++
		}
		return 0, ParseAction{}, false
	}

	if len(s.entries) > 0 {
		entries := s.entries
		for depth := len(entries) - 1; depth >= 0; depth-- {
			state := entries[depth].state
			if timing != nil {
				timing.recoverStateChecks++
			}
			if !p.stateCanRecover(state) {
				if timing != nil {
					timing.recoverStateSkips++
				}
				continue
			}
			if timing != nil {
				timing.recoverLookups++
			}
			if act, ok := p.recoverActionForState(state, sym); ok {
				if timing != nil {
					timing.recoverHits++
				}
				return depth, act, true
			}
		}
		return 0, ParseAction{}, false
	}

	if s.gss.head == nil {
		return 0, ParseAction{}, false
	}

	depth := s.gss.len() - 1
	for n := s.gss.head; n != nil; n = n.prev {
		state := n.entry.state
		if timing != nil {
			timing.recoverStateChecks++
		}
		if !p.stateCanRecover(state) {
			if timing != nil {
				timing.recoverStateSkips++
			}
			depth--
			continue
		}
		if timing != nil {
			timing.recoverLookups++
		}
		if act, ok := p.recoverActionForState(state, sym); ok {
			if timing != nil {
				timing.recoverHits++
			}
			return depth, act, true
		}
		depth--
	}
	return 0, ParseAction{}, false
}

func (p *Parser) aliasSymbolForChild(productionID uint16, childIndex int) Symbol {
	if p == nil || p.language == nil || childIndex < 0 {
		return 0
	}
	pid := int(productionID)
	if pid < 0 || pid >= len(p.language.AliasSequences) {
		return 0
	}
	seq := p.language.AliasSequences[pid]
	if childIndex >= len(seq) {
		return 0
	}
	return seq[childIndex]
}

func aliasedNodeInArena(arena *nodeArena, lang *Language, n *Node, alias Symbol) *Node {
	if n == nil || alias == 0 || n.symbol == alias {
		return n
	}

	if arena == nil {
		cloned := &Node{}
		*cloned = *n
		cloned.symbol = alias
		if lang != nil && int(alias) < len(lang.SymbolMetadata) {
			cloned.isNamed = lang.SymbolMetadata[alias].Named
		}
		return cloned
	}

	cloned := arena.allocNode()
	*cloned = *n
	cloned.symbol = alias
	if lang != nil && int(alias) < len(lang.SymbolMetadata) {
		cloned.isNamed = lang.SymbolMetadata[alias].Named
	}
	cloned.ownerArena = arena
	return cloned
}

func cloneNodeInArena(arena *nodeArena, n *Node) *Node {
	if n == nil {
		return nil
	}
	if arena == nil {
		cloned := &Node{}
		*cloned = *n
		return cloned
	}
	cloned := arena.allocNode()
	*cloned = *n
	cloned.ownerArena = arena
	return cloned
}

// buildFieldIDs creates the field ID slice for a reduce action.
func (p *Parser) buildFieldIDs(childCount int, productionID uint16, arena *nodeArena) []FieldID {
	if childCount <= 0 || len(p.language.FieldMapEntries) == 0 {
		return nil
	}

	pid := int(productionID)
	if pid >= len(p.language.FieldMapSlices) {
		return nil
	}

	fm := p.language.FieldMapSlices[pid]
	count := int(fm[1])
	if count == 0 {
		return nil
	}

	fieldIDs := arena.allocFieldIDSlice(childCount)
	start := int(fm[0])
	assigned := false
	for i := 0; i < count; i++ {
		entryIdx := start + i
		if entryIdx >= len(p.language.FieldMapEntries) {
			break
		}
		entry := p.language.FieldMapEntries[entryIdx]
		if int(entry.ChildIndex) < len(fieldIDs) {
			fieldIDs[entry.ChildIndex] = entry.FieldID
			assigned = true
		}
	}

	if !assigned {
		return nil
	}
	return fieldIDs
}
