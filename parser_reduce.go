package gotreesitter

import "fmt"

func (p *Parser) applyActionWithReduceChain(s *glrStack, act ParseAction, tok Token, anyReduced *bool, nodeCount *int, arena *nodeArena, entryScratch *glrEntryScratch, gssScratch *gssScratch, tmpEntries *[]stackEntry, deferParentLinks bool, trackChildErrors *bool) {
	p.applyAction(s, act, tok, anyReduced, nodeCount, arena, entryScratch, gssScratch, tmpEntries, deferParentLinks, trackChildErrors)
	if act.Type != ParseActionReduce || tok.NoLookahead || s == nil || s.dead || s.accepted || s.shifted {
		return
	}
	p.chainSingleReduceActions(s, tok, anyReduced, nodeCount, arena, entryScratch, gssScratch, tmpEntries, deferParentLinks, trackChildErrors)
}

func (p *Parser) pushOrExtendErrorNode(s *glrStack, state StateID, tok Token, nodeCount *int, arena *nodeArena, entryScratch *glrEntryScratch, gssScratch *gssScratch, trackChildErrors *bool) {
	if s != nil {
		top := s.top().node
		if top != nil &&
			top.symbol == errorSymbol &&
			!top.isMissing &&
			len(top.children) == 0 &&
			top.parseState == state &&
			tok.StartByte >= top.endByte {
			top.endByte = tok.EndByte
			top.endPoint = tok.EndPoint
			top.hasError = true
			if s.byteOffset < top.endByte {
				s.byteOffset = top.endByte
			}
			if trackChildErrors != nil {
				*trackChildErrors = true
			}
			return
		}
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
	errNode.parseState = state
	p.pushStackNode(s, state, errNode, entryScratch, gssScratch)
	if nodeCount != nil {
		*nodeCount = *nodeCount + 1
	}
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
	if p != nil && p.glrTrace && s != nil {
		fmt.Printf("    APPLY type=%d cur_state=%d tok=%d act_state=%d act_sym=%d act_cnt=%d extra=%v rep=%v depth=%d\n",
			act.Type, s.top().state, tok.Symbol, act.State, act.Symbol, act.ChildCount, act.Extra, act.Repetition, s.depth())
	}
	switch act.Type {
	case ParseActionShift:
		named := p.isNamedSymbol(tok.Symbol)
		leaf := newLeafNodeInArena(arena, tok.Symbol, named,
			tok.StartByte, tok.EndByte, tok.StartPoint, tok.EndPoint)
		if p != nil && p.language != nil &&
			(p.language.Name == "c" || p.language.Name == "cpp" || p.language.Name == "objc") &&
			tok.Symbol != 0 && tok.StartByte == tok.EndByte && tok.Text == "" {
			leaf.isMissing = true
			leaf.hasError = true
			if trackChildErrors != nil {
				*trackChildErrors = true
			}
		}
		leaf.isExtra = act.Extra
		if leaf.isExtra && perfCountersEnabled {
			perfRecordExtraNode()
		}
		targetState := act.State
		if leaf.isExtra {
			targetState = s.top().state
		}
		leaf.preGotoState = s.top().state
		leaf.parseState = targetState
		p.pushStackNode(s, targetState, leaf, entryScratch, gssScratch)
		s.shifted = true
		*nodeCount++
		if p != nil && p.glrTrace {
			fmt.Printf("      -> SHIFT new_state=%d depth=%d\n", targetState, s.depth())
		}

	case ParseActionReduce:
		entries := s.entries
		borrowed := false
		if entries == nil {
			if !s.cacheEntries && s.gss.head != nil {
				tmp := []stackEntry(nil)
				if tmpEntries != nil {
					tmp = *tmpEntries
				}
				p.applyReduceActionFromGSS(s, act, tok, anyReduced, nodeCount, arena, entryScratch, gssScratch, tmpEntries, tmp, deferParentLinks, trackChildErrors != nil && *trackChildErrors)
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
		p.applyReduceAction(s, act, tok, anyReduced, nodeCount, arena, entryScratch, gssScratch, entries, deferParentLinks, trackChildErrors != nil && *trackChildErrors)
		if borrowed && tmpEntries != nil {
			*tmpEntries = entries[:0]
		}
		if p != nil && p.glrTrace && s != nil && !s.dead {
			fmt.Printf("      -> REDUCE top_state=%d depth=%d\n", s.top().state, s.depth())
		}

	case ParseActionAccept:
		s.accepted = true
		if p != nil && p.glrTrace {
			fmt.Printf("      -> ACCEPT\n")
		}

	case ParseActionRecover:
		if tok.Symbol == 0 && tok.StartByte == tok.EndByte {
			s.accepted = true
			return
		}
		recoverState := s.top().state
		if act.State != 0 {
			recoverState = act.State
		}
		p.pushOrExtendErrorNode(s, recoverState, tok, nodeCount, arena, entryScratch, gssScratch, trackChildErrors)
		if p != nil && p.glrTrace && s != nil && !s.dead {
			fmt.Printf("      -> RECOVER state=%d depth=%d\n", s.top().state, s.depth())
		}
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

func (p *Parser) applyReduceActionFromGSS(s *glrStack, act ParseAction, tok Token, anyReduced *bool, nodeCount *int, arena *nodeArena, entryScratch *glrEntryScratch, gssScratch *gssScratch, tmpEntries *[]stackEntry, tmp []stackEntry, deferParentLinks bool, trackChildErrors bool) {
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

	children, fieldIDs := p.buildReduceChildren(windowEntries, 0, reducedEnd, childCount, act.ProductionID, act.Symbol, arena)

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
	shouldUseRawSpan := shouldUseRawSpanForReduction(act.Symbol, children, p.language.SymbolMetadata, p.forceRawSpanAll, p.forceRawSpanTable)
	if shouldUseRawSpan && reducedEnd > 0 {
		span := computeReduceRawSpan(windowEntries, 0, reducedEnd)
		parent.startByte = span.startByte
		parent.endByte = span.endByte
		parent.startPoint = span.startPoint
		parent.endPoint = span.endPoint
	}
	// Extend parent span to cover invisible children dropped by buildReduceChildren.
	extendParentSpanToWindow(parent, windowEntries, 0, reducedEnd, p.language.SymbolMetadata, p.language.SymbolNames)
	*nodeCount++

	gotoState := p.lookupGoto(topState, act.Symbol)
	targetState := topState
	if gotoState != 0 {
		targetState = gotoState
	}
	if tok.NoLookahead && targetState == topState {
		parent.isExtra = true
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

func shouldUseRawSpanForReduction(sym Symbol, children []*Node, symbolMeta []SymbolMetadata, forceRawSpanAll bool, forceRawSpanTable []bool) bool {
	if len(children) == 0 {
		return true
	}
	if forceRawSpanAll {
		return true
	}
	if int(sym) < len(forceRawSpanTable) && forceRawSpanTable[sym] {
		return true
	}
	if int(sym) < len(symbolMeta) && !symbolMeta[sym].Visible {
		return true
	}
	return false
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
func extendParentSpanToWindow(parent *Node, entries []stackEntry, start, reducedEnd int, symbolMeta []SymbolMetadata, symbolNames []string) {
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
	// Invisible non-extra children: extend parent span for entries that
	// buildReduceChildren drops or inlines away.
	//
	// Scan from the end toward the beginning so backward extension can chain
	// across adjacent hidden leaves. A forward-only pass misses prefixes like
	// markdown plain-text runs because the earlier hidden tokens become
	// contiguous only after a later sibling has already pulled startByte back.
	// The same reverse scan is still safe for endByte growth because the
	// contiguity checks below prevent phantom gaps from inflating the span.
	for i := reducedEnd - 1; i >= start; i-- {
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
		if isNonSpanExtendingInvisibleSymbol(n.symbol, symbolNames) {
			continue
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
		if n.startByte == n.endByte && n.startByte > parent.endByte &&
			isSpanExtendingInvisibleSymbol(n.symbol, symbolNames) {
			parent.endByte = n.endByte
			parent.endPoint = n.endPoint
		}
	}
}

func isSpanExtendingInvisibleSymbol(sym Symbol, symbolNames []string) bool {
	idx := int(sym)
	if idx < 0 || idx >= len(symbolNames) {
		return false
	}
	switch symbolNames[idx] {
	case "_implicit_end_tag":
		return true
	default:
		return false
	}
}

func isNonSpanExtendingInvisibleSymbol(sym Symbol, symbolNames []string) bool {
	idx := int(sym)
	if idx < 0 || idx >= len(symbolNames) {
		return false
	}
	switch symbolNames[idx] {
	case "_line_ending_or_eof":
		return true
	default:
		return false
	}
}

func (p *Parser) buildReduceChildren(entries []stackEntry, start, end, childCount int, productionID uint16, _ Symbol, arena *nodeArena) ([]*Node, []FieldID) {
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

	rawFieldIDs, rawInherited := p.buildFieldIDs(childCount, productionID, arena)
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
		inherited := false
		if !n.isExtra {
			if structuralChildIndex < len(rawFieldIDs) {
				fid = rawFieldIDs[structuralChildIndex]
				if structuralChildIndex < len(rawInherited) {
					inherited = rawInherited[structuralChildIndex]
				}
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
			fieldEnd := out + len(kids)
			if fieldEnd > len(fieldIDs) {
				fieldEnd = len(fieldIDs)
			}
			// Propagate the hidden node's own field IDs to the
			// inlined children. This implements "inherited" field
			// semantics: fields assigned inside hidden/non-visible
			// nodes become visible on the parent.
			if len(n.fieldIDs) > 0 {
				for j := out; j < fieldEnd; j++ {
					kidIdx := j - out
					if kidIdx < len(n.fieldIDs) && n.fieldIDs[kidIdx] != 0 {
						fieldIDs[j] = n.fieldIDs[kidIdx]
					}
				}
			}
			// Apply the parent's inherited field assignment to the
			// inlined named children when appropriate. Hidden repeat
			// nodes like Bash redirected_statement tails can surface
			// multiple visible children that all inherit the same
			// field, but mixed hidden nodes (for example operator +
			// rhs) should not spray that field onto unrelated children.
			if fid != 0 {
				assignedToUnnamed := false
				var assignedNamedSymbol Symbol
				assignedNamedCount := 0
				namedCount := 0
				lastNamedIndex := -1
				hadDirectFieldedChild := false
				for j := out; j < fieldEnd; j++ {
					child := children[j]
					if child != nil && child.isNamed {
						namedCount++
						lastNamedIndex = j
					}
					if fieldIDs[j] != fid || children[j] == nil {
						continue
					}
					hadDirectFieldedChild = true
					if !children[j].isNamed {
						assignedToUnnamed = true
						break
					}
					if assignedNamedCount == 0 {
						assignedNamedSymbol = children[j].symbol
					}
					assignedNamedCount++
				}
				if !assignedToUnnamed {
					hasUnnamedBetweenNamed := false
					if lastNamedIndex >= out {
						for j := out; j < lastNamedIndex; j++ {
							child := children[j]
							if child != nil && !child.isNamed {
								hasUnnamedBetweenNamed = true
								break
							}
						}
					}
					assignAllNamed := assignedNamedCount == 0 && namedCount > 1 && !hasUnnamedBetweenNamed &&
						inheritedFieldAllowsMixedNamedRepeat(lang, fid)
					extendTrailingAfterSingleNamed := assignedNamedCount == 0 && namedCount == 1 && !hasUnnamedBetweenNamed
					allowSingleNamedLift := inheritedFieldAllowsSingleNamedLift(lang, fid)
					for j := out; j < fieldEnd; j++ {
						child := children[j]
						if fieldIDs[j] != 0 || child == nil || !child.isNamed {
							continue
						}
						if assignedNamedCount == 0 && !assignAllNamed && !allowSingleNamedLift {
							break
						}
						if inherited && nodeHasDirectFieldID(child, fid) {
							continue
						}
						if assignedNamedCount > 0 && child.symbol != assignedNamedSymbol {
							continue
						}
						fieldIDs[j] = fid
						if assignedNamedCount == 0 && !assignAllNamed {
							break
						}
					}
					allowTrailingAnonymous := inheritedFieldAllowsTrailingAnonymous(lang, fid)
					if allowTrailingAnonymous && (hadDirectFieldedChild || extendTrailingAfterSingleNamed) {
						lastFielded := -1
						for j := out; j < fieldEnd; j++ {
							if fieldIDs[j] == fid {
								lastFielded = j
							}
						}
						if lastFielded >= out {
							hasNamedAfter := false
							for j := lastFielded + 1; j < fieldEnd; j++ {
								child := children[j]
								if child != nil && child.isNamed {
									hasNamedAfter = true
									break
								}
							}
							if !hasNamedAfter {
								for j := lastFielded + 1; j < fieldEnd; j++ {
									child := children[j]
									if child == nil || child.isNamed || fieldIDs[j] != 0 {
										break
									}
									fieldIDs[j] = fid
								}
							}
						}
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

func inheritedFieldAllowsMixedNamedRepeat(lang *Language, fid FieldID) bool {
	if lang == nil {
		return false
	}
	idx := int(fid)
	if idx < 0 || idx >= len(lang.FieldNames) {
		return false
	}
	switch lang.FieldNames[idx] {
	case "redirect", "value":
		return true
	default:
		return false
	}
}

func inheritedFieldAllowsSingleNamedLift(lang *Language, fid FieldID) bool {
	if lang == nil {
		return true
	}
	idx := int(fid)
	if idx < 0 || idx >= len(lang.FieldNames) {
		return true
	}
	switch lang.FieldNames[idx] {
	case "operator":
		return false
	default:
		return true
	}
}

func inheritedFieldAllowsTrailingAnonymous(lang *Language, fid FieldID) bool {
	if lang == nil {
		return true
	}
	idx := int(fid)
	if idx < 0 || idx >= len(lang.FieldNames) {
		return true
	}
	switch lang.FieldNames[idx] {
	case "scope":
		return false
	default:
		return true
	}
}

func nodeHasDirectFieldID(n *Node, fid FieldID) bool {
	if n == nil || fid == 0 {
		return false
	}
	for i := range n.fieldIDs {
		if n.fieldIDs[i] == fid {
			return true
		}
	}
	return false
}

func (p *Parser) applyReduceAction(s *glrStack, act ParseAction, tok Token, anyReduced *bool, nodeCount *int, arena *nodeArena, entryScratch *glrEntryScratch, gssScratch *gssScratch, entries []stackEntry, deferParentLinks bool, trackChildErrors bool) {
	childCount := int(act.ChildCount)
	window, ok := computeReduceRange(entries, childCount)
	if !ok {
		// Not enough stack entries — kill this stack version.
		s.dead = true
		return
	}

	children, fieldIDs := p.buildReduceChildren(entries, window.start, window.reducedEnd, childCount, act.ProductionID, act.Symbol, arena)

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
	shouldUseRawSpan := shouldUseRawSpanForReduction(act.Symbol, children, p.language.SymbolMetadata, p.forceRawSpanAll, p.forceRawSpanTable)
	if shouldUseRawSpan && window.reducedEnd > window.start {
		span := computeReduceRawSpan(entries, window.start, window.reducedEnd)
		parent.startByte = span.startByte
		parent.endByte = span.endByte
		parent.startPoint = span.startPoint
		parent.endPoint = span.endPoint
	}
	// Extend parent span to cover invisible children dropped by buildReduceChildren.
	extendParentSpanToWindow(parent, entries, window.start, window.reducedEnd, p.language.SymbolMetadata, p.language.SymbolNames)
	*nodeCount++

	gotoState := p.lookupGoto(window.topState, act.Symbol)
	targetState := window.topState
	if gotoState != 0 {
		targetState = gotoState
	}
	if tok.NoLookahead && targetState == window.topState {
		parent.isExtra = true
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
func (p *Parser) buildFieldIDs(childCount int, productionID uint16, arena *nodeArena) ([]FieldID, []bool) {
	if childCount <= 0 || len(p.language.FieldMapEntries) == 0 {
		return nil, nil
	}

	pid := int(productionID)
	if pid >= len(p.language.FieldMapSlices) {
		return nil, nil
	}

	fm := p.language.FieldMapSlices[pid]
	count := int(fm[1])
	if count == 0 {
		return nil, nil
	}

	fieldIDs := arena.allocFieldIDSlice(childCount)
	inherited := make([]bool, childCount)
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
			inherited[entry.ChildIndex] = entry.Inherited
			assigned = true
		}
	}

	if !assigned {
		return nil, nil
	}
	return fieldIDs, inherited
}
