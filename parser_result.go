package gotreesitter

import "strings"

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
	if p != nil && p.hasRootSymbol {
		expectedRootSymbol = p.rootSymbol
		hasExpectedRoot = true
	}
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
		extendRootToChildSpans(candidate, source)
		extendNodeToTrailingWhitespace(candidate, source)
		p.normalizeRootSourceStart(candidate, source)
		normalizeKnownSpanAttribution(candidate, source, p.language)
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
		extendRootToChildSpans(root, source)
		extendNodeToTrailingWhitespace(root, source)
		p.normalizeRootSourceStart(root, source)
		normalizeKnownSpanAttribution(root, source, p.language)
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
		} else if n.startByte == n.endByte && p != nil && p.language != nil &&
			int(n.symbol) < len(p.language.SymbolMetadata) &&
			!p.language.SymbolMetadata[n.symbol].Visible {
			// Zero-width invisible nodes (e.g. epsilon reductions of hidden
			// repeat helpers) are ignorable during root recovery — they should
			// not prevent identification of the real root.
			continue
		} else {
			if realRoot != nil {
				realRoot = nil // more than one non-extra -> genuine error
				break
			}
			realRoot = n
		}
	}
	if realRoot == nil && p != nil && p.language != nil {
		// Some grammars can leave detached trivia/comment nodes alongside the
		// real root at EOF. Recover by selecting a single expected/root-like
		// node and folding the detached trivia around it.
		if recoveredRoot, recoveredExtras, recoveredAllExtras, ok := p.recoverDetachedRootCandidate(nodes, hasExpectedRoot, expectedRootSymbol, source); ok {
			realRoot = recoveredRoot
			extras = recoveredExtras
			allExtras = recoveredAllExtras
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
		extendRootToChildSpans(realRoot, source)
		extendNodeToTrailingWhitespace(realRoot, source)
		p.normalizeRootSourceStart(realRoot, source)
		normalizeKnownSpanAttribution(realRoot, source, p.language)
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
	p.normalizeRootSourceStart(root, source)
	normalizeKnownSpanAttribution(root, source, p.language)
	if shouldWireParentLinks {
		wireParentLinksWithScratch(root, linkScratch)
	}
	return newTreeWithArenas(root, source, p.language, arena, getBorrowed())
}

func (p *Parser) recoverDetachedRootCandidate(nodes []*Node, hasExpectedRoot bool, expectedRootSymbol Symbol, source []byte) (*Node, []*Node, []*Node, bool) {
	if p == nil || p.language == nil || len(nodes) == 0 {
		return nil, nil, nil, false
	}

	var candidate *Node
	for _, n := range nodes {
		if n == nil || n.isExtra {
			continue
		}
		if hasExpectedRoot {
			if n.symbol != expectedRootSymbol {
				continue
			}
		} else {
			if int(n.symbol) >= len(p.language.SymbolNames) || !isRootLikeName(p.language.SymbolNames[n.symbol]) {
				continue
			}
		}
		if candidate != nil {
			// Ambiguous candidate set.
			return nil, nil, nil, false
		}
		candidate = n
	}
	if candidate == nil {
		return nil, nil, nil, false
	}

	var visibleExtras []*Node
	var allExtras []*Node
	for _, n := range nodes {
		if n == nil || n == candidate {
			continue
		}
		if n.isExtra {
			allExtras = append(allExtras, n)
			if int(n.symbol) < len(p.language.SymbolMetadata) && p.language.SymbolMetadata[n.symbol].Visible {
				visibleExtras = append(visibleExtras, n)
			}
			continue
		}
		if n.startByte == n.endByte && int(n.symbol) < len(p.language.SymbolMetadata) && !p.language.SymbolMetadata[n.symbol].Visible {
			// Ignore zero-width invisible artifacts.
			continue
		}
		if !isDetachedTriviaNode(n, candidate, source, p.language) {
			return nil, nil, nil, false
		}
		allExtras = append(allExtras, n)
		if int(n.symbol) < len(p.language.SymbolMetadata) && p.language.SymbolMetadata[n.symbol].Visible {
			visibleExtras = append(visibleExtras, n)
		}
	}

	return candidate, visibleExtras, allExtras, true
}

func isDetachedTriviaNode(n, root *Node, source []byte, lang *Language) bool {
	if n == nil || root == nil || lang == nil {
		return false
	}
	// Must be clearly outside the root span.
	if !(n.endByte <= root.startByte || n.startByte >= root.endByte) {
		return false
	}
	if int(n.symbol) < len(lang.SymbolNames) {
		name := lang.SymbolNames[n.symbol]
		if strings.Contains(name, "comment") {
			return true
		}
	}
	if int(n.endByte) <= len(source) && int(n.startByte) <= int(n.endByte) {
		return bytesAreTrivia(source[n.startByte:n.endByte])
	}
	return false
}

func isRootLikeName(name string) bool {
	switch name {
	case "source_file", "program", "module", "document", "file",
		"source", "source_code", "translation_unit", "compilation_unit",
		"makefile", "stylesheet", "config_file", "chunk", "query",
		"pattern", "fragment", "hcl", "template", "script",
		"config", "configuration", "specification", "schema",
		"description_unit", "project":
		return true
	default:
		return false
	}
}

func (p *Parser) normalizeRootSourceStart(root *Node, source []byte) {
	if root == nil || root.startByte == 0 || len(source) == 0 {
		return
	}
	// Included-range parses intentionally preserve range-local root spans.
	if p != nil && len(p.included) > 0 {
		return
	}
	root.startByte = 0
	root.startPoint = Point{}
}

// normalizeKnownSpanAttribution applies narrow compatibility fixes where
// C tree-sitter attributes trailing trivia to a grouped node but this runtime
// currently drops it during child normalization.
func normalizeKnownSpanAttribution(root *Node, source []byte, lang *Language) {
	normalizeTargetedTrailingTriviaSpans(root, source, lang)
	normalizeHaskellImportsSpan(root, source, lang)
}

func bytesAreTrivia(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return false
		}
	}
	return true
}

var trailingTriviaSpanTargets = map[string]map[string]struct{}{
	"caddy": {
		"server": {},
	},
	"fortran": {
		"program":           {},
		"program_statement": {},
	},
	"just": {
		"recipe":      {},
		"recipe_body": {},
	},
	"nginx": {
		"attribute": {},
	},
	"pug": {
		"tag": {},
	},
}

func normalizeTargetedTrailingTriviaSpans(root *Node, source []byte, lang *Language) {
	if root == nil || len(source) == 0 || lang == nil {
		return
	}
	targets, ok := trailingTriviaSpanTargets[lang.Name]
	if !ok {
		return
	}
	normalizeTargetedTrailingTriviaSpansRecursive(root, source, lang, targets)
}

func normalizeTargetedTrailingTriviaSpansRecursive(parent *Node, source []byte, lang *Language, targets map[string]struct{}) {
	if parent == nil || len(parent.children) == 0 {
		return
	}
	for i, child := range parent.children {
		if child == nil {
			continue
		}
		if child.isNamed {
			if _, ok := targets[child.Type(lang)]; ok {
				boundaryByte := parent.endByte
				if i+1 < len(parent.children) && parent.children[i+1] != nil {
					boundaryByte = parent.children[i+1].startByte
				}
				if child.endByte < boundaryByte && boundaryByte <= uint32(len(source)) {
					gap := source[child.endByte:boundaryByte]
					if bytesAreTrivia(gap) {
						trimmed := trimTrailingTriviaGap(gap)
						child.endByte += uint32(len(trimmed))
						child.endPoint = advancePointByBytes(child.endPoint, trimmed)
					}
				}
			}
		}
		normalizeTargetedTrailingTriviaSpansRecursive(child, source, lang, targets)
	}
}

func trimTrailingTriviaGap(gap []byte) []byte {
	for i := 0; i < len(gap); i++ {
		switch gap[i] {
		case '\n':
			return gap[:i+1]
		case '\r':
			if i+1 < len(gap) && gap[i+1] == '\n' {
				return gap[:i+2]
			}
			return gap[:i+1]
		}
	}
	return gap
}

func normalizeHaskellImportsSpan(root *Node, source []byte, lang *Language) {
	if root == nil || len(root.children) < 2 || len(source) == 0 || lang == nil || lang.Name != "haskell" {
		return
	}
	for i := 0; i+1 < len(root.children); i++ {
		left := root.children[i]
		right := root.children[i+1]
		if left == nil || right == nil {
			continue
		}
		if left.Type(lang) != "imports" {
			continue
		}
		if left.endByte >= right.startByte {
			continue
		}
		if left.endByte > uint32(len(source)) || right.startByte > uint32(len(source)) {
			continue
		}
		gap := source[left.endByte:right.startByte]
		if !bytesAreTrivia(gap) {
			continue
		}
		left.endByte = right.startByte
		left.endPoint = advancePointByBytes(left.endPoint, gap)
	}
}

func advancePointByBytes(start Point, b []byte) Point {
	p := start
	for _, c := range b {
		if c == '\n' {
			p.Row++
			p.Column = 0
			continue
		}
		p.Column++
	}
	return p
}
