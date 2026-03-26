package gotreesitter

import (
	"bytes"
	"strings"
)

// buildResultFromGLR picks the best stack and constructs the final tree.
// Prefers accepted stacks, then highest score, then most entries. When
// accepted stacks are otherwise tied, prefer the tree that retains an
// alias-target symbol before falling back to branch order.
func (p *Parser) buildResultFromGLR(stacks []glrStack, source []byte, arena *nodeArena, oldTree *Tree, reuseState *parseReuseState, linkScratch *[]*Node) *Tree {
	if len(stacks) == 0 {
		arena.Release()
		return parseErrorTree(source, p.language)
	}
	best := 0
	for i := 1; i < len(stacks); i++ {
		if stackCompareForResultSelection(p, &stacks[i], &stacks[best]) > 0 {
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

func stackCompareForResultSelection(p *Parser, a, b *glrStack) int {
	if a.dead != b.dead {
		if a.dead {
			return -1
		}
		return 1
	}
	if a.accepted != b.accepted {
		if a.accepted {
			return 1
		}
		return -1
	}
	if aErr, bErr := stackErrorRank(a), stackErrorRank(b); aErr != bErr {
		if aErr < bErr {
			return 1
		}
		return -1
	}
	if cmp := compareAcceptedStackAliasPreference(p, *a, *b); cmp != 0 {
		return cmp
	}
	if a.score != b.score {
		if a.score > b.score {
			return 1
		}
		return -1
	}
	if a.shifted != b.shifted {
		if !a.shifted {
			return 1
		}
		return -1
	}
	aDepth := a.depth()
	bDepth := b.depth()
	if aDepth != bDepth {
		if aDepth > bDepth {
			return 1
		}
		return -1
	}
	if a.byteOffset != b.byteOffset {
		if a.byteOffset > b.byteOffset {
			return 1
		}
		return -1
	}
	if a.branchOrder != b.branchOrder {
		if a.branchOrder < b.branchOrder {
			return 1
		}
		return -1
	}
	return 0
}

func compareAcceptedStackAliasPreference(p *Parser, a, b glrStack) int {
	if p == nil || p.language == nil {
		return 0
	}
	aNodes := resultNodesFromStack(a)
	bNodes := resultNodesFromStack(b)
	if len(aNodes) != len(bNodes) {
		return 0
	}
	for i := range aNodes {
		if cmp := compareNodeAliasPreference(p, aNodes[i], bNodes[i]); cmp != 0 {
			return cmp
		}
	}
	return 0
}

func resultNodesFromStack(s glrStack) []*Node {
	if len(s.entries) > 0 {
		count := 0
		for i := range s.entries {
			if s.entries[i].node != nil {
				count++
			}
		}
		if count == 0 {
			return nil
		}
		nodes := make([]*Node, 0, count)
		for i := range s.entries {
			if s.entries[i].node != nil {
				nodes = append(nodes, s.entries[i].node)
			}
		}
		return nodes
	}
	if s.gss.head == nil {
		return nil
	}
	return nodesFromGSS(s.gss)
}

func compareNodeAliasPreference(p *Parser, a, b *Node) int {
	if a == b || a == nil || b == nil {
		return 0
	}
	if a.startByte != b.startByte ||
		a.endByte != b.endByte ||
		a.isExtra != b.isExtra ||
		a.isMissing != b.isMissing ||
		len(a.children) != len(b.children) {
		return 0
	}
	if a.symbol != b.symbol {
		aType := a.Type(p.language)
		bType := b.Type(p.language)
		if aType == bType {
			for i := range a.children {
				if cmp := compareNodeAliasPreference(p, a.children[i], b.children[i]); cmp != 0 {
					return cmp
				}
			}
			return 0
		}
		aAlias := p.isAliasTargetSymbol(a.symbol)
		bAlias := p.isAliasTargetSymbol(b.symbol)
		if aAlias != bAlias {
			if aAlias {
				return 1
			}
			return -1
		}
		return 0
	}
	for i := range a.children {
		if cmp := compareNodeAliasPreference(p, a.children[i], b.children[i]); cmp != 0 {
			return cmp
		}
	}
	return 0
}

func (p *Parser) isAliasTargetSymbol(sym Symbol) bool {
	if p == nil || int(sym) >= len(p.aliasTargetSymbol) {
		return false
	}
	return p.aliasTargetSymbol[sym]
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

func filterZeroWidthExtras(nodes []*Node, arena *nodeArena) []*Node {
	if len(nodes) == 0 {
		return nodes
	}
	keep := 0
	for _, n := range nodes {
		if n == nil || !n.isExtra || n.endByte > n.startByte {
			keep++
		}
	}
	if keep == len(nodes) || keep == 0 {
		return nodes
	}
	filtered := make([]*Node, 0, keep)
	for _, n := range nodes {
		if n != nil && n.isExtra && n.endByte == n.startByte {
			continue
		}
		filtered = append(filtered, n)
	}
	if arena != nil {
		out := arena.allocNodeSlice(len(filtered))
		copy(out, filtered)
		return out
	}
	return filtered
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
	if p != nil && p.language != nil && p.language.Name == "python" {
		nodes = collapsePythonRootFragments(nodes, arena, p.language)
	}
	if hasExpectedRoot && len(nodes) > 1 {
		nodes = flattenRootSelfFragments(nodes, arena, expectedRootSymbol)
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
		candidate = flattenInvisibleRootChildren(candidate, arena, p.language)
		candidate = repairPythonRootNode(candidate, arena, p.language)
		if !hasExpectedRoot || candidate.symbol == expectedRootSymbol {
			extendNodeToTrailingWhitespace(candidate, source)
			p.normalizeRootSourceStart(candidate, source)
			normalizeKnownSpanAttribution(candidate, source, p)
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
		p.normalizeRootSourceStart(root, source)
		normalizeKnownSpanAttribution(root, source, p)
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
			// Ignore invisible extras and zero-width extras in final-root
			// recovery; they should not force an error wrapper or inflate root
			// child counts.
			if p != nil && p.language != nil &&
				int(n.symbol) < len(p.language.SymbolMetadata) &&
				p.language.SymbolMetadata[n.symbol].Visible &&
				n.endByte > n.startByte {
				extras = append(extras, n)
			}
		} else {
			if realRoot != nil {
				realRoot = nil // more than one non-extra -> genuine error
				break
			}
			realRoot = n
		}
	}
	if realRoot != nil {
		returnRealRoot := !hasExpectedRoot || realRoot.symbol == expectedRootSymbol
		if reuseState != nil && reuseState.reusedAny {
			realRoot = cloneNodeInArena(arena, realRoot)
			realRoot.parent = nil
			realRoot.childIndex = -1
		}
		foldExtras := returnRealRoot && len(extras) > 0
		if foldExtras {
			for _, e := range allExtras {
				if e != nil && (e.IsError() || e.HasError()) {
					foldExtras = false
					break
				}
			}
		}
		if foldExtras {
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
				if len(realRoot.fieldSources) > 0 {
					paddedSources := make([]uint8, len(padded))
					copy(paddedSources[leadingCount:], realRoot.fieldSources)
					realRoot.fieldSources = paddedSources
				}
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
		// Invisible extras should still contribute to the final root byte/point range.
		if returnRealRoot {
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
		}
		realRoot = repairPythonRootNode(realRoot, arena, p.language)
		if returnRealRoot || !realRoot.hasError {
			extendNodeToTrailingWhitespace(realRoot, source)
		}
		p.normalizeRootSourceStart(realRoot, source)
		normalizeKnownSpanAttribution(realRoot, source, p)
		if returnRealRoot {
			if shouldWireParentLinks {
				wireParentLinksWithScratch(realRoot, linkScratch)
			}
			return newTreeWithArenas(realRoot, source, p.language, arena, getBorrowed())
		}
	}

	rootChildren := filterZeroWidthExtras(nodes, arena)
	rootSymbol := rootChildren[len(rootChildren)-1].symbol
	rootHasError := false
	for _, n := range rootChildren {
		if n != nil && (n.IsError() || n.HasError()) {
			rootHasError = true
			break
		}
	}
	if hasExpectedRoot {
		if rootHasError {
			if p != nil && p.language != nil && p.language.Name == "dart" && dartProgramChildrenLookComplete(nodes, p.language) {
				rootSymbol = expectedRootSymbol
			} else {
				rootSymbol = errorSymbol
			}
		} else {
			rootSymbol = expectedRootSymbol
		}
	}
	root := newParentNodeInArena(arena, rootSymbol, true, rootChildren, nil, 0)
	if rootHasError && !(p != nil && p.language != nil && p.language.Name == "python" && hasExpectedRoot && pythonModuleChildrenLookComplete(rootChildren, p.language)) {
		root.hasError = true
	}
	root = repairPythonRootNode(root, arena, p.language)
	extendNodeToTrailingWhitespace(root, source)
	p.normalizeRootSourceStart(root, source)
	normalizeKnownSpanAttribution(root, source, p)
	if shouldWireParentLinks {
		wireParentLinksWithScratch(root, linkScratch)
	}
	return newTreeWithArenas(root, source, p.language, arena, getBorrowed())
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

// maxTreeWalkDepth prevents stack overflow in recursive tree walkers when
// parsing with grammargen-produced grammars that can create pathologically deep
// hidden-node chains (e.g. Scala with >1M levels).
const maxTreeWalkDepth = 5000

// normalizeKnownSpanAttribution applies narrow compatibility fixes where
// C tree-sitter attributes trailing trivia to a grouped node but this runtime
// currently drops it during child normalization.
func normalizeKnownSpanAttribution(root *Node, source []byte, p *Parser) {
	var lang *Language
	if p != nil {
		lang = p.language
	}
	if root == nil || lang == nil {
		return
	}

	switch lang.Name {
	case "bash":
		normalizeBashProgramVariableAssignments(root, lang)
	case "c":
		normalizeCTranslationUnitRoot(root, lang)
		normalizeCPreprocessorDirectiveShapes(root, source, lang)
		normalizeCDeclarationBounds(root, source, lang)
		normalizeCBuiltinPrimitiveTypeIdentifiers(root, source, lang)
		normalizeCVariadicParameterEllipsis(root, lang)
		normalizeCSizeofUnknownTypeIdentifiers(root, source, lang)
		normalizeCCastUnknownTypeIdentifiers(root, source, lang)
		normalizeCBareTypeIdentifierExpressionStatements(root, source, lang)
		normalizeCPreprocNewlineSpans(root, source, lang)
		normalizeCPointerAssignmentPrecedence(root, lang)
	case "c_sharp":
		normalizeCSharpTypeConstraintKeywords(root, lang)
		normalizeCSharpSwitchTupleCasePatterns(root, lang)
	case "caddy":
		normalizeTopLevelTrailingLineBreakSpan(root, source, lang)
	case "cobol", "COBOL":
		normalizeCobolLeadingAreaStart(root, source, lang)
		normalizeCobolTopLevelDefinitionEnd(root, source, lang)
		normalizeCobolDivisionSiblingEnds(root, source, lang)
		normalizeCobolPeriodChildren(root, source, lang)
	case "comment":
		normalizeCommentTrailingExtraTrivia(root, source, lang)
	case "cooklang":
		normalizeCooklangTrailingStepTail(root, source, lang)
	case "d":
		normalizeDSourceFileLeadingTrivia(root, source, lang)
		normalizeDModuleDefinitionBounds(root, lang)
		normalizeDCallExpressionTemplateTypes(root, lang)
		normalizeDCallExpressionPropertyTypes(root, lang)
		normalizeDCallExpressionSimpleTypeCallees(root, lang)
		normalizeDVariableTypeQualifiers(root, lang)
		normalizeDVariableStorageClassWrappers(root, lang)
	case "dart":
		normalizeDartConstructorSignatureKinds(root, source, lang)
		normalizeDartSingleTypeArgumentFreeCalls(root, lang)
		normalizeDartSwitchExpressionBodyFields(root, lang)
	case "elixir":
		normalizeElixirNestedCallTargetFields(root, lang)
	case "erlang":
		normalizeErlangSourceFileForms(root, lang)
	case "fortran":
		normalizeFortranStatementLineBreaks(root, source, lang)
		normalizeTopLevelTrailingLineBreakSpan(root, source, lang)
	case "go":
		normalizeGoSourceFileRoot(root, source, p)
		normalizeGoCompatibility(root, source, lang)
		normalizeRootEOFNewlineSpan(root, source, lang)
	case "haskell":
		normalizeHaskellImportsSpan(root, source, lang)
		normalizeHaskellZeroWidthTokens(root, lang)
		normalizeHaskellRootImportField(root, lang)
		normalizeHaskellDeclarationsSpan(root, source, lang)
		normalizeHaskellLocalBindsStarts(root, source, lang)
		normalizeHaskellQuasiquoteStarts(root, source, lang)
	case "hcl":
		normalizeHCLConfigFileRoot(root, lang)
	case "html":
		normalizeHTMLRecoveredNestedCustomTags(root, lang)
		normalizeHTMLRecoveredNestedCustomTagRanges(root, source, lang)
	case "ini":
		normalizeIniSectionStarts(root, lang)
	case "javascript":
		normalizeJavaScriptProgramStart(root, lang)
		normalizeJavaScriptTypeScriptOptionalChainLeaves(root, lang)
		normalizeJavaScriptTypeScriptCallPrecedence(root, lang)
		normalizeJavaScriptTypeScriptUnaryPrecedence(root, lang)
		normalizeJavaScriptTypeScriptBinaryPrecedence(root, lang)
		normalizeJavaScriptTrailingContinueComments(root, source, lang)
		normalizeJavaScriptTopLevelExpressionStatementBounds(root, lang)
		normalizeJavaScriptTopLevelObjectLiterals(root, lang)
	case "lua":
		normalizeLuaChunkLocalDeclarationFields(root, source, lang)
	case "make":
		normalizeMakeConditionalConsequenceFields(root, lang)
	case "nginx":
		normalizeNginxAttributeLineBreaks(root, source, lang)
	case "nim":
		normalizeNimTopLevelCallEnd(root, source, lang)
	case "pascal":
		normalizePascalTopLevelProgramEnd(root, source, lang)
		normalizePascalTrailingExtraTrivia(root, source, lang)
	case "perl":
		normalizePerlJoinAssignmentLists(root, source, lang)
		normalizePerlPushExpressionLists(root, source, lang)
		normalizePerlReturnExpressionLists(root, lang)
	case "php":
		normalizePHPSingletonTypeWrappers(root, lang)
		normalizePHPStaticFunctionFragments(root, source, lang)
	case "powershell":
		normalizePowerShellProgramShape(root, source, lang)
	case "pug":
		normalizeTopLevelTrailingLineBreakSpan(root, source, lang)
	case "python":
		normalizePythonStringContinuationEscapes(root, source, lang)
	case "rst":
		normalizeRSTTopLevelSectionEnd(root, source, lang)
	case "ruby":
		normalizeRubyThenStarts(root, lang)
		normalizeRubyTopLevelModuleBounds(root, source, lang)
	case "scala":
		normalizeScalaObjectTemplateBodyFragments(root, source, lang)
		normalizeScalaTemplateBodyObjectFragments(root, source, lang)
		normalizeScalaTemplateBodyRecoveredMembers(root, source, lang)
		normalizeScalaRecoveredObjectTemplateBodies(root, source, lang)
		normalizeScalaSplitFunctionDefinitions(root, source, lang)
		normalizeScalaTopLevelClassFragments(root, source, lang)
		normalizeScalaCompilationUnitRoot(root, source, lang)
		normalizeScalaDefinitionFields(root, source, lang)
		normalizeScalaTemplateBodyFunctionAnnotations(root, source, lang)
		normalizeScalaImportPathFields(root, lang)
		normalizeScalaTemplateBodyFunctionEnds(root, source, lang)
		normalizeScalaTrailingCommentOwnership(root, source, lang)
		normalizeScalaFunctionModifierFields(root, lang)
		normalizeScalaInterpolatedStringTail(root, source, lang)
		normalizeScalaCaseClauseEnds(root, source, lang)
		normalizeRootEOFNewlineSpan(root, source, lang)
	case "sql":
		normalizeSQLRecoveredSelectRoot(root, lang)
	case "svelte":
		normalizeSvelteTrailingExtraTrivia(root, source, lang)
	case "tsx", "typescript":
		normalizeJavaScriptTypeScriptOptionalChainLeaves(root, lang)
		normalizeJavaScriptTypeScriptCallPrecedence(root, lang)
		normalizeJavaScriptTypeScriptUnaryPrecedence(root, lang)
		normalizeJavaScriptTypeScriptBinaryPrecedence(root, lang)
		normalizeTypeScriptRecoveredNamespaceRoot(root, source, lang)
		normalizeTypeScriptCompatibility(root, source, lang)
		normalizeCollapsedNamedLeafChildren(root, lang, "existential_type", "*")
	case "zig":
		normalizeZigEmptyInitListFields(root, lang)
	}
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

func lastNonTriviaByteEnd(source []byte) uint32 {
	for i := len(source); i > 0; i-- {
		switch source[i-1] {
		case ' ', '\t', '\n', '\r', '\f':
			continue
		default:
			return uint32(i)
		}
	}
	return 0
}

func normalizeHCLConfigFileRoot(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "hcl" || root.Type(lang) != "config_file" || len(root.children) == 0 {
		return
	}
	filtered := make([]*Node, 0, len(root.children))
	filteredChanged := false
	for _, child := range root.children {
		if child == nil {
			continue
		}
		if child.Type(lang) == "_whitespace" {
			filteredChanged = true
			continue
		}
		filtered = append(filtered, child)
	}
	if filteredChanged {
		if root.ownerArena != nil {
			buf := root.ownerArena.allocNodeSlice(len(filtered))
			copy(buf, filtered)
			filtered = buf
		}
		root.children = filtered
		root.fieldIDs = nil
		root.fieldSources = nil
	}
	for _, child := range root.children {
		if child == nil || child.Type(lang) != "body" {
			continue
		}
		snapHCLBodyBounds(child)
	}
}

func snapHCLBodyBounds(body *Node) {
	if body == nil || len(body.children) == 0 {
		return
	}
	first, last := firstAndLastNonNilChild(body.children)
	if first == nil || last == nil {
		return
	}
	body.startByte = first.startByte
	body.startPoint = first.startPoint
	body.endByte = last.endByte
	body.endPoint = last.endPoint
}

func firstAndLastNonNilChild(children []*Node) (*Node, *Node) {
	var first *Node
	for _, child := range children {
		if child != nil {
			first = child
			break
		}
	}
	if first == nil {
		return nil, nil
	}
	for i := len(children) - 1; i >= 0; i-- {
		if children[i] != nil {
			return first, children[i]
		}
	}
	return first, first
}

func normalizeHTMLRecoveredNestedCustomTags(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "html" || root.Type(lang) != "ERROR" || len(root.children) < 5 {
		return
	}
	documentSym, ok := symbolByName(lang, "document")
	if !ok {
		return
	}
	elementSym, ok := symbolByName(lang, "element")
	if !ok {
		return
	}
	endTagSym, ok := symbolByName(lang, "end_tag")
	if !ok {
		return
	}
	startTags, nextIdx, ok := collectHTMLRecoveredStartTags(root.children, lang)
	if !ok || nextIdx+4 != len(root.children) {
		return
	}
	continuation := root.children[nextIdx]
	closeTok := root.children[nextIdx+1]
	tagName := root.children[nextIdx+2]
	closeAngle := root.children[nextIdx+3]
	if continuation == nil || continuation.Type(lang) != "element" || closeTok == nil || closeTok.Type(lang) != "</" || tagName == nil || tagName.Type(lang) != "tag_name" || closeAngle == nil || closeAngle.Type(lang) != ">" {
		return
	}
	htmlExtendOpenElementChain(continuation, closeTok.startByte, closeTok.startPoint, lang)
	endTagChildren := []*Node{closeTok, tagName, closeAngle}
	if root.ownerArena != nil {
		buf := root.ownerArena.allocNodeSlice(len(endTagChildren))
		copy(buf, endTagChildren)
		endTagChildren = buf
	}
	endTag := newParentNodeInArena(root.ownerArena, endTagSym, lang.SymbolMetadata[endTagSym].Named, endTagChildren, nil, 0)
	inner := continuation
	for i := len(startTags) - 1; i >= 1; i-- {
		children := []*Node{startTags[i], inner}
		if root.ownerArena != nil {
			buf := root.ownerArena.allocNodeSlice(len(children))
			copy(buf, children)
			children = buf
		}
		wrapper := newParentNodeInArena(root.ownerArena, elementSym, lang.SymbolMetadata[elementSym].Named, children, nil, 0)
		wrapper.endByte = closeTok.startByte
		wrapper.endPoint = closeTok.startPoint
		inner = wrapper
	}
	htmlExtendLeadingElementChain(inner, closeTok.startByte, closeTok.startPoint, lang)
	outerChildren := []*Node{startTags[0], inner, endTag}
	if root.ownerArena != nil {
		buf := root.ownerArena.allocNodeSlice(len(outerChildren))
		copy(buf, outerChildren)
		outerChildren = buf
	}
	outer := newParentNodeInArena(root.ownerArena, elementSym, lang.SymbolMetadata[elementSym].Named, outerChildren, nil, 0)
	root.children = []*Node{outer}
	if root.ownerArena != nil {
		buf := root.ownerArena.allocNodeSlice(1)
		buf[0] = outer
		root.children = buf
	}
	root.fieldIDs = nil
	root.fieldSources = nil
	root.symbol = documentSym
	root.isNamed = lang.SymbolMetadata[documentSym].Named
	root.hasError = outer.HasError()
}

func collectHTMLRecoveredStartTags(children []*Node, lang *Language) ([]*Node, int, bool) {
	startTags := make([]*Node, 0, len(children))
	for i, child := range children {
		if child == nil {
			continue
		}
		if startTag := htmlRecoveredStartTag(child, lang); startTag != nil {
			startTags = append(startTags, startTag)
			continue
		}
		if len(startTags) == 0 {
			return nil, 0, false
		}
		return startTags, i, true
	}
	return nil, 0, false
}

func htmlRecoveredStartTag(node *Node, lang *Language) *Node {
	if node == nil || lang == nil {
		return nil
	}
	if node.Type(lang) == "start_tag" {
		return node
	}
	if node.Type(lang) == "ERROR" && len(node.children) == 1 && node.children[0] != nil && node.children[0].Type(lang) == "start_tag" {
		return node.children[0]
	}
	return nil
}

func htmlExtendOpenElementChain(node *Node, endByte uint32, endPoint Point, lang *Language) {
	if node == nil || lang == nil || node.Type(lang) != "element" {
		return
	}
	hasEndTag := false
	for _, child := range node.children {
		if child == nil {
			continue
		}
		if child.Type(lang) == "end_tag" {
			hasEndTag = true
		}
		htmlExtendOpenElementChain(child, endByte, endPoint, lang)
	}
	if !hasEndTag {
		node.endByte = endByte
		node.endPoint = endPoint
	}
}

func htmlExtendLeadingElementChain(node *Node, endByte uint32, endPoint Point, lang *Language) {
	for cur := node; cur != nil && lang != nil && cur.Type(lang) == "element"; {
		cur.endByte = endByte
		cur.endPoint = endPoint
		if len(cur.children) < 2 || cur.children[1] == nil || cur.children[1].Type(lang) != "element" {
			return
		}
		cur = cur.children[1]
	}
}

func normalizeHTMLRecoveredNestedCustomTagRanges(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "html" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(node *Node) {
		if node == nil {
			return
		}
		for _, child := range node.children {
			walk(child)
		}
		if node.Type(lang) != "element" || len(node.children) < 2 {
			return
		}
		for i := 0; i+1 < len(node.children); i++ {
			left := node.children[i]
			right := node.children[i+1]
			if left == nil || right == nil || left.Type(lang) != "element" || right.Type(lang) != "end_tag" || len(right.children) == 0 {
				continue
			}
			closeTok := right.children[0]
			if closeTok == nil || closeTok.Type(lang) != "</" || left.endByte >= closeTok.startByte || closeTok.startByte > uint32(len(source)) {
				continue
			}
			if !bytesAreTrivia(source[left.endByte:closeTok.startByte]) {
				continue
			}
			htmlExtendLeadingElementChain(left, closeTok.startByte, closeTok.startPoint, lang)
		}
	}
	walk(root)
}

func normalizeBashProgramVariableAssignments(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "bash" || root.Type(lang) != "program" || len(root.children) == 0 {
		return
	}
	normalizeBashVariableAssignmentsInNode(root, lang)
}

func normalizeBashVariableAssignmentsInNode(node *Node, lang *Language) {
	if node == nil || lang == nil || len(node.children) == 0 {
		return
	}
	for _, child := range node.children {
		if child != nil {
			normalizeBashVariableAssignmentsInNode(child, lang)
		}
	}
	out := make([]*Node, 0, len(node.children))
	changed := false
	for _, child := range node.children {
		if child == nil {
			continue
		}
		if child.Type(lang) == "variable_assignments" && bashAllVariableAssignments(child, lang) && bashShouldSplitVariableAssignments(node.Type(lang)) {
			out = append(out, child.children...)
			changed = true
			continue
		}
		out = append(out, child)
	}
	if !changed {
		assignBashIfConditionField(node, lang)
		return
	}
	if node.ownerArena != nil {
		buf := node.ownerArena.allocNodeSlice(len(out))
		copy(buf, out)
		out = buf
	}
	node.children = out
	node.fieldIDs = nil
	node.fieldSources = nil
	assignBashIfConditionField(node, lang)
}

func bashAllVariableAssignments(node *Node, lang *Language) bool {
	if node == nil || lang == nil || len(node.children) < 2 {
		return false
	}
	for _, child := range node.children {
		if child == nil || child.Type(lang) != "variable_assignment" {
			return false
		}
	}
	return true
}

func bashShouldSplitVariableAssignments(parentType string) bool {
	switch parentType {
	case "command", "redirected_statement", "declaration_command", "unset_command":
		return false
	default:
		return true
	}
}

func assignBashIfConditionField(node *Node, lang *Language) {
	if node == nil || lang == nil || node.Type(lang) != "if_statement" || len(node.children) <= 1 {
		return
	}
	fid, ok := lang.FieldByName("condition")
	if !ok {
		return
	}
	ensureNodeFieldStorage(node, len(node.children))
	thenIndex := -1
	for i, child := range node.children {
		if child != nil && child.Type(lang) == "then" {
			thenIndex = i
			break
		}
	}
	if thenIndex < 0 {
		thenIndex = len(node.children)
	}
	for i := 1; i < thenIndex; i++ {
		if node.children[i] == nil {
			continue
		}
		node.fieldIDs[i] = fid
		node.fieldSources[i] = fieldSourceDirect
	}
}

func normalizeSQLRecoveredSelectRoot(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "sql" || root.Type(lang) != "source_file" || len(root.children) < 3 {
		return
	}
	if !sqlLooksLikeFlatRecoveredSelect(root, lang) {
		return
	}
	selectStmtSym, ok := symbolByName(lang, "select_statement")
	if !ok {
		return
	}
	selectClauseSym, ok := symbolByName(lang, "select_clause")
	if !ok {
		return
	}
	selectClauseBodySym, ok := symbolByName(lang, "select_clause_body")
	if !ok {
		return
	}
	nullParentSym, ok := findVisibleSymbolByName(lang, "NULL", true)
	if !ok {
		return
	}
	nullLeafSym, ok := findVisibleSymbolByName(lang, "NULL", false)
	if !ok {
		return
	}
	bodyChildren := sqlFlattenRecoveredSelectBody(root.children[1:], nil, lang)
	if !sqlNeedsRecoveredMissingNull(bodyChildren, lang) {
		return
	}
	bodyChildren = append(bodyChildren, sqlRecoveredNullNode(root.ownerArena, bodyChildren[len(bodyChildren)-1], nullParentSym, nullLeafSym))
	if root.ownerArena != nil {
		buf := root.ownerArena.allocNodeSlice(len(bodyChildren))
		copy(buf, bodyChildren)
		bodyChildren = buf
	}
	selectClauseBody := newParentNodeInArena(root.ownerArena, selectClauseBodySym, lang.SymbolMetadata[selectClauseBodySym].Named, bodyChildren, nil, 0)
	selectClause := newParentNodeInArena(root.ownerArena, selectClauseSym, lang.SymbolMetadata[selectClauseSym].Named, []*Node{root.children[0], selectClauseBody}, nil, 0)
	selectStatement := newParentNodeInArena(root.ownerArena, selectStmtSym, lang.SymbolMetadata[selectStmtSym].Named, []*Node{selectClause}, nil, 0)
	children := []*Node{selectStatement}
	if root.ownerArena != nil {
		buf := root.ownerArena.allocNodeSlice(1)
		buf[0] = selectStatement
		children = buf
	}
	root.children = children
	root.fieldIDs = nil
	root.fieldSources = nil
	root.hasError = selectStatement.HasError()
}

func sqlLooksLikeFlatRecoveredSelect(root *Node, lang *Language) bool {
	if len(root.children) < 3 || root.children[0] == nil || root.children[0].Type(lang) != "SELECT" {
		return false
	}
	sawRepeat := false
	for _, child := range root.children[1:] {
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "_aliasable_expression", "_expression", ",", "comment":
			continue
		case "select_clause_body_repeat1":
			sawRepeat = true
			continue
		default:
			return false
		}
	}
	return sawRepeat
}

func sqlFlattenRecoveredSelectBody(nodes []*Node, out []*Node, lang *Language) []*Node {
	for _, node := range nodes {
		if node == nil {
			continue
		}
		switch node.Type(lang) {
		case "_aliasable_expression", "_expression", "select_clause_body_repeat1":
			if len(node.children) > 0 {
				out = sqlFlattenRecoveredSelectBody(node.children, out, lang)
				continue
			}
		}
		out = append(out, node)
	}
	return out
}

func sqlNeedsRecoveredMissingNull(children []*Node, lang *Language) bool {
	last, prev := sqlLastAndPrevNonNilChild(children)
	if last == nil {
		return false
	}
	if last.Type(lang) == "NULL" {
		return false
	}
	if last.Type(lang) == "comment" && prev != nil && prev.Type(lang) == "," {
		return true
	}
	return last.Type(lang) == ","
}

func sqlLastAndPrevNonNilChild(children []*Node) (last *Node, prev *Node) {
	for i := len(children) - 1; i >= 0; i-- {
		if children[i] == nil {
			continue
		}
		last = children[i]
		for j := i - 1; j >= 0; j-- {
			if children[j] != nil {
				prev = children[j]
				break
			}
		}
		return last, prev
	}
	return nil, nil
}

func sqlRecoveredNullNode(arena *nodeArena, anchor *Node, nullParentSym, nullLeafSym Symbol) *Node {
	if anchor == nil {
		return nil
	}
	leaf := newLeafNodeInArena(arena, nullLeafSym, false, anchor.endByte, anchor.endByte, anchor.endPoint, anchor.endPoint)
	leaf.isMissing = true
	leaf.hasError = true
	node := newParentNodeInArena(arena, nullParentSym, true, []*Node{leaf}, nil, 0)
	node.hasError = true
	return node
}

func normalizeCTranslationUnitRoot(root *Node, lang *Language) {
	if root == nil || lang == nil || root.Type(lang) != "ERROR" {
		return
	}
	if lang.Name != "c" && lang.Name != "cpp" {
		return
	}
	sym, ok := symbolByName(lang, "translation_unit")
	if !ok || !rootLooksLikeCTopLevel(root, lang) {
		return
	}
	root.symbol = sym
	root.isNamed = int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
}

func rootLooksLikeCTopLevel(root *Node, lang *Language) bool {
	if root == nil || lang == nil || len(root.children) == 0 {
		return false
	}
	sawTopLevel := false
	for _, child := range root.children {
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "preproc_if",
			"preproc_ifdef",
			"preproc_include",
			"preproc_def",
			"preproc_function_def",
			"preproc_call",
			"declaration",
			"function_definition",
			"linkage_specification",
			"type_definition",
			"struct_specifier",
			"union_specifier",
			"enum_specifier",
			"class_specifier",
			"namespace_definition",
			"template_declaration",
			"comment":
			sawTopLevel = true
		default:
			return false
		}
	}
	return sawTopLevel
}

func normalizeCDeclarationBounds(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil {
		return
	}
	if lang.Name != "c" && lang.Name != "cpp" {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "declaration" {
			first, last := firstAndLastNonNilChild(n.children)
			if first != nil && n.startByte < first.startByte &&
				first.startByte <= uint32(len(source)) &&
				cBytesAreCommentOrTrivia(source[n.startByte:first.startByte]) {
				n.startByte = first.startByte
				n.startPoint = first.startPoint
			}
			if last != nil && n.endByte > last.endByte &&
				n.endByte <= uint32(len(source)) &&
				bytesAreTrivia(source[last.endByte:n.endByte]) {
				setNodeEndTo(n, last.endByte, source)
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeCPreprocessorDirectiveShapes(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || len(root.children) == 0 {
		return
	}
	if lang.Name != "c" && lang.Name != "cpp" {
		return
	}
	if root.Type(lang) != "translation_unit" {
		return
	}
	preprocDefSym, hasPreprocDef := symbolByName(lang, "preproc_def")
	preprocArgSym, hasPreprocArg := symbolByName(lang, "preproc_arg")
	nameFieldID, hasNameField := lang.FieldByName("name")
	valueFieldID, hasValueField := lang.FieldByName("value")
	preprocArgNamed := hasPreprocArg && int(preprocArgSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[preprocArgSym].Named

	out := make([]*Node, 0, len(root.children))
	changed := false
	for i := 0; i < len(root.children); i++ {
		child := root.children[i]
		if child == nil {
			continue
		}
		if hasPreprocDef && hasPreprocArg && hasNameField && hasValueField {
			if normalizeCWhitespaceSeparatedFunctionMacro(child, source, lang, preprocDefSym, preprocArgSym, preprocArgNamed, nameFieldID, valueFieldID) {
				changed = true
			}
		}
		if consumed, ok := normalizeCPreprocessorDirectiveRange(child, source, lang); ok {
			changed = true
			for i+1 < len(root.children) && root.children[i+1] != nil && root.children[i+1].startByte < consumed && root.children[i+1].endByte <= consumed {
				i++
			}
		}
		out = append(out, child)
	}
	if !changed {
		return
	}
	if root.ownerArena != nil {
		buf := root.ownerArena.allocNodeSlice(len(out))
		copy(buf, out)
		out = buf
	}
	root.children = out
	root.fieldIDs = nil
	root.fieldSources = nil
	populateParentNode(root, out)
	extendNodeToTrailingWhitespace(root, source)
}

func normalizeCWhitespaceSeparatedFunctionMacro(node *Node, source []byte, lang *Language, preprocDefSym, preprocArgSym Symbol, preprocArgNamed bool, nameFieldID, valueFieldID FieldID) bool {
	if node == nil || lang == nil || node.Type(lang) != "preproc_function_def" || len(node.children) < 3 || len(node.children) > 4 {
		return false
	}
	name := node.children[1]
	params := node.children[2]
	if name == nil || params == nil || name.Type(lang) != "identifier" || params.Type(lang) != "preproc_params" {
		return false
	}
	value := (*Node)(nil)
	if len(node.children) == 4 {
		value = node.children[3]
		if value == nil || value.Type(lang) != "preproc_arg" {
			return false
		}
	} else {
		value = newParentNodeInArena(node.ownerArena, preprocArgSym, preprocArgNamed, nil, nil, 0)
		value.startByte = params.startByte
		value.startPoint = params.startPoint
		value.endByte = params.endByte
		value.endPoint = params.endPoint
	}
	if name.endByte >= params.startByte || params.startByte > uint32(len(source)) {
		return false
	}
	if !bytesAreTrivia(source[name.endByte:params.startByte]) {
		return false
	}

	value.startByte = params.startByte
	value.startPoint = advancePointByBytes(Point{}, source[:params.startByte])
	if value.endByte < value.startByte {
		value.endByte = value.startByte
		value.endPoint = value.startPoint
	}

	children := []*Node{node.children[0], name, value}
	if node.ownerArena != nil {
		buf := node.ownerArena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	node.symbol = preprocDefSym
	node.isNamed = int(preprocDefSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[preprocDefSym].Named
	node.children = children
	ensureNodeFieldStorage(node, len(children))
	for i := range node.fieldIDs {
		node.fieldIDs[i] = 0
	}
	for i := range node.fieldSources {
		node.fieldSources[i] = fieldSourceNone
	}
	node.fieldIDs[1] = nameFieldID
	node.fieldIDs[2] = valueFieldID
	node.fieldSources[1] = fieldSourceDirect
	node.fieldSources[2] = fieldSourceDirect
	populateParentNode(node, node.children)
	return true
}

func normalizeCPreprocessorDirectiveRange(node *Node, source []byte, lang *Language) (uint32, bool) {
	if node == nil || lang == nil || len(node.children) == 0 {
		return 0, false
	}
	switch node.Type(lang) {
	case "preproc_def", "preproc_function_def", "preproc_call":
	default:
		return 0, false
	}
	arg := node.children[len(node.children)-1]
	if arg == nil || arg.Type(lang) != "preproc_arg" || node.startByte >= uint32(len(source)) {
		return 0, false
	}
	directiveEnd, valueEnd, ok := cScanPreprocessorDirectiveExtent(source, node.startByte)
	if !ok || directiveEnd <= node.endByte {
		return 0, false
	}
	valueStart := cScanPreprocessorValueStart(source, arg.startByte, valueEnd)
	if valueStart < arg.startByte || valueStart > valueEnd {
		valueStart = arg.startByte
	}
	arg.startByte = valueStart
	arg.startPoint = advancePointByBytes(Point{}, source[:valueStart])
	setNodeEndTo(arg, valueEnd, source)
	populateParentNode(node, node.children)
	extendNodeEndTo(node, directiveEnd, source)
	return directiveEnd, true
}

func cScanPreprocessorDirectiveExtent(source []byte, start uint32) (directiveEnd uint32, valueEnd uint32, ok bool) {
	if start >= uint32(len(source)) {
		return 0, 0, false
	}
	lineStart := int(start)
	lastValueEnd := lineStart
	for lineStart < len(source) {
		lineEnd := lineStart
		for lineEnd < len(source) && source[lineEnd] != '\n' {
			lineEnd++
		}
		lastValueEnd = lineEnd
		if lineEnd > lineStart && source[lineEnd-1] == '\r' {
			lastValueEnd--
		}
		directiveEnd = uint32(lineEnd)
		if lineEnd < len(source) && source[lineEnd] == '\n' {
			directiveEnd++
		}
		if !cLineEndsWithContinuation(source[lineStart:lineEnd]) {
			return directiveEnd, uint32(lastValueEnd), true
		}
		lineStart = lineEnd + 1
	}
	return uint32(len(source)), uint32(lastValueEnd), true
}

func cScanPreprocessorValueStart(source []byte, start, end uint32) uint32 {
	if start > end || end > uint32(len(source)) {
		return start
	}
	i := start
	for i < end {
		switch source[i] {
		case ' ', '\t', '\n', '\r', '\f', '\\':
			i++
			continue
		default:
			return i
		}
	}
	return end
}

func cLineEndsWithContinuation(line []byte) bool {
	end := len(line)
	for end > 0 && (line[end-1] == ' ' || line[end-1] == '\t' || line[end-1] == '\f' || line[end-1] == '\r') {
		end--
	}
	if end == 0 || line[end-1] != '\\' {
		return false
	}
	backslashes := 0
	for i := end - 1; i >= 0 && line[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func cBytesAreCommentOrTrivia(b []byte) bool {
	for i := 0; i < len(b); {
		switch b[i] {
		case ' ', '\t', '\n', '\r', '\f':
			i++
		case '/':
			if i+1 >= len(b) {
				return false
			}
			switch b[i+1] {
			case '/':
				end, ok := cScanLineCommentEnd(b, i)
				if !ok {
					return false
				}
				i = end
			case '*':
				end, ok := cScanBlockCommentEnd(b, i)
				if !ok {
					return false
				}
				i = end
			default:
				return false
			}
		default:
			return false
		}
	}
	return true
}

func cScanLineCommentEnd(b []byte, start int) (int, bool) {
	if start+1 >= len(b) || b[start] != '/' || b[start+1] != '/' {
		return 0, false
	}
	i := start + 2
	for i < len(b) {
		if b[i] == '\n' {
			lineEnd := i
			if cLineEndsWithContinuation(b[start:lineEnd]) {
				i++
				continue
			}
			return i + 1, true
		}
		i++
	}
	return len(b), true
}

func cScanBlockCommentEnd(b []byte, start int) (int, bool) {
	if start+1 >= len(b) || b[start] != '/' || b[start+1] != '*' {
		return 0, false
	}
	for i := start + 2; i+1 < len(b); i++ {
		if b[i] == '*' && b[i+1] == '/' {
			return i + 2, true
		}
	}
	return 0, false
}

func normalizeCSizeofUnknownTypeIdentifiers(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "c" {
		return
	}
	typeDescriptorSym, ok := lang.SymbolByName("type_descriptor")
	if !ok {
		return
	}
	typeIdentifierSym, ok := lang.SymbolByName("type_identifier")
	if !ok {
		return
	}
	identifierSym, ok := lang.SymbolByName("identifier")
	if !ok {
		return
	}
	parenthesizedSym, ok := lang.SymbolByName("parenthesized_expression")
	if !ok {
		return
	}
	identifierNamed := false
	if int(identifierSym) < len(lang.SymbolMetadata) {
		identifierNamed = lang.SymbolMetadata[identifierSym].Named
	}
	parenthesizedNamed := false
	if int(parenthesizedSym) < len(lang.SymbolMetadata) {
		parenthesizedNamed = lang.SymbolMetadata[parenthesizedSym].Named
	}
	valueFieldID, hasValueField := lang.FieldByName("value")
	localTypes := collectCLocalTypeNames(root, source, lang)

	var rewrite func(*Node)
	rewrite = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "sizeof_expression" && len(n.children) == 4 {
			typeDescriptor := n.children[2]
			if typeDescriptor != nil && typeDescriptor.symbol == typeDescriptorSym && len(typeDescriptor.children) == 1 {
				typeIdent := typeDescriptor.children[0]
				if typeIdent != nil && typeIdent.symbol == typeIdentifierSym {
					name := canonicalCTypeName(typeIdent.Text(source))
					if _, ok := localTypes[name]; !ok {
						ident := newLeafNodeInArena(n.ownerArena, identifierSym, identifierNamed, typeIdent.startByte, typeIdent.endByte, typeIdent.startPoint, typeIdent.endPoint)
						paren := newParentNodeInArena(n.ownerArena, parenthesizedSym, parenthesizedNamed, []*Node{n.children[1], ident, n.children[3]}, nil, 0)
						replaceChildRangeWithSingleNode(n, 1, 4, paren)
						if hasValueField && len(n.children) > 1 {
							ensureNodeFieldStorage(n, len(n.children))
							n.fieldIDs[1] = valueFieldID
							n.fieldSources[1] = fieldSourceDirect
						}
					}
				}
			}
		}
		for _, child := range n.children {
			rewrite(child)
		}
	}
	rewrite(root)
}

func normalizeCBuiltinPrimitiveTypeIdentifiers(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "c" {
		return
	}
	primitiveTypeSym, ok := lang.SymbolByName("primitive_type")
	if !ok {
		return
	}
	primitiveTypeNamed := false
	if int(primitiveTypeSym) < len(lang.SymbolMetadata) {
		primitiveTypeNamed = lang.SymbolMetadata[primitiveTypeSym].Named
	}
	var rewrite func(*Node)
	rewrite = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "type_identifier" && isCBuiltinPrimitiveTypeName(canonicalCTypeName(n.Text(source))) {
			n.symbol = primitiveTypeSym
			n.isNamed = primitiveTypeNamed
		}
		for _, child := range n.children {
			rewrite(child)
		}
	}
	rewrite(root)
}

func normalizeCVariadicParameterEllipsis(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "c" {
		return
	}
	variadicSym, ok := lang.SymbolByName("variadic_parameter")
	if !ok {
		return
	}
	ellipsisSym, ok := lang.SymbolByName("...")
	if !ok {
		return
	}
	ellipsisNamed := false
	if int(ellipsisSym) < len(lang.SymbolMetadata) {
		ellipsisNamed = lang.SymbolMetadata[ellipsisSym].Named
	}
	var rewrite func(*Node)
	rewrite = func(n *Node) {
		if n == nil {
			return
		}
		if n.symbol == variadicSym && len(n.children) == 0 {
			child := newLeafNodeInArena(n.ownerArena, ellipsisSym, ellipsisNamed, n.startByte, n.endByte, n.startPoint, n.endPoint)
			n.children = cloneNodeSliceInArena(n.ownerArena, []*Node{child})
			populateParentNode(n, n.children)
		}
		for _, child := range n.children {
			rewrite(child)
		}
	}
	rewrite(root)
}

func normalizeCPreprocNewlineSpans(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || (lang.Name != "c" && lang.Name != "cpp") || len(source) == 0 {
		return
	}
	nlSym, ok := symbolByName(lang, "\n")
	if !ok {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		for _, child := range n.children {
			if child != nil && child.symbol == nlSym && child.endByte < uint32(len(source)) {
				// Extend newline tokens to include consecutive newlines/whitespace
				end := child.endByte
				for end < uint32(len(source)) && (source[end] == '\n' || source[end] == '\r') {
					end++
				}
				if end > child.endByte {
					child.endByte = end
					child.endPoint = advancePointByBytes(Point{}, source[:end])
				}
			}
			walk(child)
		}
	}
	walk(root)
}

func normalizeCBareTypeIdentifierExpressionStatements(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "c" {
		return
	}
	compoundSym, ok1 := symbolByName(lang, "compound_statement")
	typeIdSym, ok2 := symbolByName(lang, "type_identifier")
	semiSym, ok3 := symbolByName(lang, ";")
	exprStmtSym, ok4 := symbolByName(lang, "expression_statement")
	identSym, ok5 := symbolByName(lang, "identifier")
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return
	}
	exprStmtNamed := int(exprStmtSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[exprStmtSym].Named
	identNamed := int(identSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[identSym].Named
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.symbol == compoundSym {
			// Look for bare type_identifier ; pairs that should be expression_statement(identifier ;)
			newChildren := make([]*Node, 0, len(n.children))
			for i := 0; i < len(n.children); i++ {
				child := n.children[i]
				if child != nil && child.symbol == typeIdSym && i+1 < len(n.children) && n.children[i+1] != nil && n.children[i+1].symbol == semiSym {
					semi := n.children[i+1]
					ident := newLeafNodeInArena(n.ownerArena, identSym, identNamed, child.startByte, child.endByte, child.startPoint, child.endPoint)
					exprStmt := newParentNodeInArena(n.ownerArena, exprStmtSym, exprStmtNamed, []*Node{ident, semi}, nil, 0)
					exprStmt.startByte = child.startByte
					exprStmt.startPoint = child.startPoint
					exprStmt.endByte = semi.endByte
					exprStmt.endPoint = semi.endPoint
					newChildren = append(newChildren, exprStmt)
					i++ // skip the semicolon
					continue
				}
				newChildren = append(newChildren, child)
			}
			if len(newChildren) != len(n.children) {
				n.children = newChildren
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeCCastUnknownTypeIdentifiers(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "c" {
		return
	}
	typeDescriptorSym, ok := lang.SymbolByName("type_descriptor")
	if !ok {
		return
	}
	typeIdentifierSym, ok := lang.SymbolByName("type_identifier")
	if !ok {
		return
	}
	identifierSym, ok := lang.SymbolByName("identifier")
	if !ok {
		return
	}
	parenthesizedSym, ok := lang.SymbolByName("parenthesized_expression")
	if !ok {
		return
	}
	callSym, ok := lang.SymbolByName("call_expression")
	if !ok {
		return
	}
	castSym, ok := lang.SymbolByName("cast_expression")
	if !ok {
		return
	}
	argumentListSym, ok := lang.SymbolByName("argument_list")
	if !ok {
		return
	}
	functionFieldID, hasFunctionField := lang.FieldByName("function")
	argumentsFieldID, hasArgumentsField := lang.FieldByName("arguments")
	if !hasFunctionField || !hasArgumentsField {
		return
	}
	typeFieldID, hasTypeField := lang.FieldByName("type")
	valueFieldID, hasValueField := lang.FieldByName("value")
	if !hasTypeField || !hasValueField {
		return
	}
	identifierNamed := false
	if int(identifierSym) < len(lang.SymbolMetadata) {
		identifierNamed = lang.SymbolMetadata[identifierSym].Named
	}
	typeDescriptorNamed := false
	if int(typeDescriptorSym) < len(lang.SymbolMetadata) {
		typeDescriptorNamed = lang.SymbolMetadata[typeDescriptorSym].Named
	}
	typeIdentifierNamed := false
	if int(typeIdentifierSym) < len(lang.SymbolMetadata) {
		typeIdentifierNamed = lang.SymbolMetadata[typeIdentifierSym].Named
	}
	parenthesizedNamed := false
	if int(parenthesizedSym) < len(lang.SymbolMetadata) {
		parenthesizedNamed = lang.SymbolMetadata[parenthesizedSym].Named
	}
	callNamed := false
	if int(callSym) < len(lang.SymbolMetadata) {
		callNamed = lang.SymbolMetadata[callSym].Named
	}
	castNamed := false
	if int(castSym) < len(lang.SymbolMetadata) {
		castNamed = lang.SymbolMetadata[castSym].Named
	}
	argumentListNamed := false
	if int(argumentListSym) < len(lang.SymbolMetadata) {
		argumentListNamed = lang.SymbolMetadata[argumentListSym].Named
	}
	localTypes := collectCLocalTypeNames(root, source, lang)

	var rewrite func(*Node)
	rewrite = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "cast_expression" && len(n.children) == 4 {
			typeDescriptor := n.children[1]
			value := n.children[3]
			if typeDescriptor != nil && value != nil && typeDescriptor.symbol == typeDescriptorSym && len(typeDescriptor.children) == 1 {
				typeIdent := typeDescriptor.children[0]
				if typeIdent != nil && typeIdent.symbol == typeIdentifierSym && value.Type(lang) == "parenthesized_expression" {
					name := typeIdent.Text(source)
					if _, ok := localTypes[name]; !ok {
						ident := newLeafNodeInArena(n.ownerArena, identifierSym, identifierNamed, typeIdent.startByte, typeIdent.endByte, typeIdent.startPoint, typeIdent.endPoint)
						function := newParentNodeInArena(n.ownerArena, parenthesizedSym, parenthesizedNamed, []*Node{n.children[0], ident, n.children[2]}, nil, 0)
						argsChildren := append([]*Node(nil), value.children...)
						if n.ownerArena != nil && len(argsChildren) > 0 {
							buf := n.ownerArena.allocNodeSlice(len(argsChildren))
							copy(buf, argsChildren)
							argsChildren = buf
						}
						arguments := newParentNodeInArena(n.ownerArena, argumentListSym, argumentListNamed, argsChildren, nil, 0)
						children := []*Node{function, arguments}
						if n.ownerArena != nil {
							buf := n.ownerArena.allocNodeSlice(len(children))
							copy(buf, children)
							children = buf
						}
						fieldIDs := make([]FieldID, len(children))
						fieldIDs[0] = functionFieldID
						fieldIDs[1] = argumentsFieldID
						if n.ownerArena != nil {
							buf := n.ownerArena.allocFieldIDSlice(len(fieldIDs))
							copy(buf, fieldIDs)
							fieldIDs = buf
						}
						n.symbol = callSym
						n.isNamed = callNamed
						n.children = children
						n.fieldIDs = fieldIDs
						n.fieldSources = make([]uint8, len(children))
						n.fieldSources[0] = fieldSourceDirect
						n.fieldSources[1] = fieldSourceDirect
						n.productionID = 0
						for i, child := range n.children {
							if child == nil {
								continue
							}
							child.parent = n
							child.childIndex = i
						}
					}
				}
			}
		}
		for _, child := range n.children {
			rewrite(child)
		}
	}
	rewrite(root)

	var repair func(*Node)
	repair = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "call_expression" && len(n.children) == 2 {
			function := n.children[0]
			arguments := n.children[1]
			if function != nil && arguments != nil &&
				function.Type(lang) == "parenthesized_expression" &&
				arguments.Type(lang) == "argument_list" &&
				len(function.children) >= 3 {
				var ident *Node
				for _, child := range function.children {
					if child != nil && child.Type(lang) == "identifier" {
						ident = child
						break
					}
				}
				if ident != nil {
					name := canonicalCTypeName(ident.Text(source))
					if _, ok := localTypes[name]; ok {
						typeIdent := newLeafNodeInArena(n.ownerArena, typeIdentifierSym, typeIdentifierNamed, ident.startByte, ident.endByte, ident.startPoint, ident.endPoint)
						typeDescriptor := newParentNodeInArena(n.ownerArena, typeDescriptorSym, typeDescriptorNamed, []*Node{typeIdent}, nil, 0)
						var valueNode *Node
						for _, child := range arguments.children {
							if child != nil && child.isNamed {
								valueNode = child
								break
							}
						}
						if valueNode != nil {
							children := []*Node{function.children[0], typeDescriptor, function.children[len(function.children)-1], valueNode}
							if n.ownerArena != nil {
								buf := n.ownerArena.allocNodeSlice(len(children))
								copy(buf, children)
								children = buf
							}
							fieldIDs := make([]FieldID, len(children))
							fieldIDs[1] = typeFieldID
							fieldIDs[3] = valueFieldID
							if n.ownerArena != nil {
								buf := n.ownerArena.allocFieldIDSlice(len(fieldIDs))
								copy(buf, fieldIDs)
								fieldIDs = buf
							}
							n.symbol = castSym
							n.isNamed = castNamed
							n.children = children
							n.fieldIDs = fieldIDs
							n.fieldSources = make([]uint8, len(children))
							n.fieldSources[1] = fieldSourceDirect
							n.fieldSources[3] = fieldSourceDirect
							n.productionID = 0
							for i, child := range n.children {
								if child == nil {
									continue
								}
								child.parent = n
								child.childIndex = i
							}
						}
					}
				}
			}
		}
		for _, child := range n.children {
			repair(child)
		}
	}
	repair(root)
}

func normalizeCPointerAssignmentPrecedence(root *Node, lang *Language) {
	if root == nil || lang == nil {
		return
	}
	if lang.Name != "c" && lang.Name != "cpp" {
		return
	}

	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		for i, child := range n.children {
			walk(child)
			for {
				rewritten := rewriteCPointerAssignmentPrecedence(child, lang)
				if rewritten == nil {
					break
				}
				n.children[i] = rewritten
				rewritten.parent = n
				rewritten.childIndex = i
				child = rewritten
			}
		}
	}
	walk(root)
}

func rewriteCPointerAssignmentPrecedence(node *Node, lang *Language) *Node {
	if node == nil || lang == nil || node.Type(lang) != "pointer_expression" || len(node.children) != 2 {
		return nil
	}
	operator := node.children[0]
	assignment := node.children[1]
	if operator == nil || assignment == nil || operator.Type(lang) != "*" || assignment.Type(lang) != "assignment_expression" || len(assignment.children) != 3 {
		return nil
	}
	left := assignment.children[0]
	assignOp := assignment.children[1]
	right := assignment.children[2]
	if left == nil || assignOp == nil || right == nil || !isCAssignmentOperatorToken(assignOp.Type(lang)) {
		return nil
	}

	rewrittenPointer := cloneNodeInArena(node.ownerArena, node)
	rewrittenPointer.children = cloneNodeSliceInArena(node.ownerArena, []*Node{operator, left})
	populateParentNode(rewrittenPointer, rewrittenPointer.children)

	rewrittenAssign := cloneNodeInArena(node.ownerArena, assignment)
	rewrittenAssign.children = cloneNodeSliceInArena(node.ownerArena, []*Node{rewrittenPointer, assignOp, right})
	populateParentNode(rewrittenAssign, rewrittenAssign.children)
	return rewrittenAssign
}

func isCAssignmentOperatorToken(tok string) bool {
	if tok == "=" {
		return true
	}
	if !strings.HasSuffix(tok, "=") {
		return false
	}
	switch tok {
	case "==", "!=", "<=", ">=", "=>", "===", "!==":
		return false
	default:
		return true
	}
}

func isCBuiltinPrimitiveTypeName(name string) bool {
	switch name {
	case "char", "int", "float", "double", "void", "_Bool", "_Complex", "bool", "__int128",
		"size_t", "ssize_t", "ptrdiff_t", "intptr_t", "uintptr_t",
		"int8_t", "int16_t", "int32_t", "int64_t",
		"uint8_t", "uint16_t", "uint32_t", "uint64_t",
		"wchar_t", "char16_t", "char32_t":
		return true
	default:
		return false
	}
}

func canonicalCTypeName(name string) string {
	name = strings.TrimSpace(name)
	start, end := 0, len(name)
	for start < end && !isCTypeNameChar(name[start]) {
		start++
	}
	for end > start && !isCTypeNameChar(name[end-1]) {
		end--
	}
	return name[start:end]
}

func isCTypeNameChar(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

func collectCLocalTypeNames(root *Node, source []byte, lang *Language) map[string]struct{} {
	localTypes := make(map[string]struct{})
	if root == nil || lang == nil || lang.Name != "c" {
		return localTypes
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "type_definition" {
			for _, child := range n.children {
				if child == nil || child.Type(lang) != "type_identifier" {
					continue
				}
				if name := canonicalCTypeName(child.Text(source)); name != "" {
					localTypes[name] = struct{}{}
				}
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
	return localTypes
}

func normalizeGoSourceFileRoot(root *Node, source []byte, p *Parser) {
	if root == nil || p == nil || p.language == nil || p.language.Name != "go" || root.Type(p.language) != "ERROR" {
		return
	}
	lang := p.language
	sym, ok := symbolByName(lang, "source_file")
	if !ok {
		return
	}
	if !rootLooksLikeGoTopLevel(root, lang) {
		recoverGoRootTopLevelChunks(root, source, p)
	}
	if !rootLooksLikeGoTopLevel(root, lang) {
		return
	}
	root.symbol = sym
	root.isNamed = int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	root.hasError = false
	for _, child := range root.children {
		if child != nil && (child.IsError() || child.HasError()) {
			root.hasError = true
			break
		}
	}
	if root.endByte < uint32(len(source)) && bytesAreTrivia(source[root.endByte:]) {
		extendNodeEndTo(root, uint32(len(source)), source)
	}
}

func rootLooksLikeGoTopLevel(root *Node, lang *Language) bool {
	if root == nil || lang == nil || len(root.children) == 0 {
		return false
	}
	sawTopLevel := false
	for _, child := range root.children {
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "package_clause",
			"import_declaration",
			"function_declaration",
			"method_declaration",
			"const_declaration",
			"type_declaration",
			"var_declaration",
			"comment":
			sawTopLevel = true
		default:
			return false
		}
	}
	return sawTopLevel
}

func recoverGoRootTopLevelChunks(root *Node, source []byte, p *Parser) {
	if root == nil || p == nil || p.language == nil || p.skipRecoveryReparse || len(source) == 0 || len(root.children) == 0 {
		return
	}
	firstBad := firstGoNonTopLevelChildIndex(root, p.language)
	if firstBad <= 0 {
		return
	}
	start := goRootRecoveryStartByte(root.children[firstBad], source)
	if int(start) >= len(source) {
		return
	}
	recovered, ok := goReparsedTopLevelChunks(source, start, p, root.ownerArena)
	if !ok {
		return
	}
	newChildren := make([]*Node, 0, firstBad+len(recovered))
	newChildren = append(newChildren, root.children[:firstBad]...)
	newChildren = append(newChildren, recovered...)
	if !goChildrenLookLikeTopLevel(newChildren, p.language) {
		return
	}
	if arena := root.ownerArena; arena != nil {
		buf := arena.allocNodeSlice(len(newChildren))
		copy(buf, newChildren)
		root.children = buf
	} else {
		root.children = newChildren
	}
	root.fieldIDs = nil
	root.fieldSources = nil
	populateParentNode(root, root.children)
}

func firstGoNonTopLevelChildIndex(root *Node, lang *Language) int {
	if root == nil || lang == nil {
		return -1
	}
	for i, child := range root.children {
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "package_clause",
			"import_declaration",
			"function_declaration",
			"method_declaration",
			"const_declaration",
			"type_declaration",
			"var_declaration",
			"comment":
			continue
		default:
			return i
		}
	}
	return -1
}

func goChildrenLookLikeTopLevel(children []*Node, lang *Language) bool {
	root := &Node{children: children}
	return rootLooksLikeGoTopLevel(root, lang)
}

func goRootRecoveryStartByte(node *Node, source []byte) uint32 {
	if node == nil {
		return uint32(len(source))
	}
	start := node.startByte
	for start > 0 && source[start-1] != '\n' {
		start--
	}
	return start
}

func goReparsedTopLevelChunks(source []byte, start uint32, p *Parser, arena *nodeArena) ([]*Node, bool) {
	if p == nil || p.language == nil || int(start) >= len(source) {
		return nil, false
	}
	const prefix = "package p\n"
	prefixPoint := advancePointByBytes(Point{}, []byte(prefix))
	chunkStarts := goTopLevelChunkStarts(source, start)
	if len(chunkStarts) == 0 {
		return nil, false
	}
	recovered := make([]*Node, 0, len(chunkStarts))
	for i, chunkStart := range chunkStarts {
		chunkEnd := uint32(len(source))
		if i+1 < len(chunkStarts) {
			chunkEnd = chunkStarts[i+1]
		}
		if chunkStart >= chunkEnd {
			continue
		}
		wrapped := make([]byte, 0, len(prefix)+int(chunkEnd-chunkStart))
		wrapped = append(wrapped, prefix...)
		wrapped = append(wrapped, source[chunkStart:chunkEnd]...)
		tree, err := p.parseForRecovery(wrapped)
		if err != nil || tree == nil || tree.RootNode() == nil {
			if tree != nil {
				tree.Release()
			}
			return nil, false
		}
		if tree.RootNode().HasError() {
			tree.Release()
			recoveredNode, ok := goRecoverWrappedFunctionChunk(source, chunkStart, chunkEnd, p, arena)
			if !ok {
				return nil, false
			}
			recovered = append(recovered, recoveredNode)
			continue
		}
		startPoint := advancePointByBytes(Point{}, source[:chunkStart])
		if startPoint.Row < prefixPoint.Row {
			tree.Release()
			return nil, false
		}
		offsetRoot := tree.RootNodeWithOffset(
			chunkStart-uint32(len(prefix)),
			Point{Row: startPoint.Row - prefixPoint.Row, Column: startPoint.Column},
		)
		tree.Release()
		if offsetRoot == nil {
			return nil, false
		}
		var added int
		for j := 0; j < offsetRoot.NamedChildCount(); j++ {
			child := offsetRoot.NamedChild(j)
			if child == nil || child.Type(p.language) == "package_clause" {
				continue
			}
			recovered = append(recovered, cloneTreeNodesIntoArena(child, arena))
			added++
		}
		if added == 0 {
			return nil, false
		}
	}
	return recovered, len(recovered) > 0
}

func goRecoverWrappedFunctionChunk(source []byte, chunkStart, chunkEnd uint32, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil || p.language == nil || len(source) == 0 || chunkStart >= chunkEnd || int(chunkEnd) > len(source) {
		return nil, false
	}
	const prefix = "package p\n"
	wrapped := make([]byte, 0, len(prefix)+int(chunkEnd-chunkStart))
	wrapped = append(wrapped, prefix...)
	wrapped = append(wrapped, source[chunkStart:chunkEnd]...)
	funcStart := len(prefix)
	openBrace := bytes.IndexByte(wrapped[funcStart:], '{')
	if openBrace < 0 {
		return nil, false
	}
	openBrace += funcStart
	closeBrace := findMatchingBraceByte(wrapped, openBrace, len(wrapped))
	if closeBrace < 0 || closeBrace <= openBrace {
		return nil, false
	}

	skeleton := make([]byte, 0, openBrace+4)
	skeleton = append(skeleton, wrapped[:openBrace]...)
	skeleton = append(skeleton, '{', '}', '\n')
	tree, err := p.parseForRecovery(skeleton)
	if err != nil || tree == nil || tree.RootNode() == nil || tree.RootNode().HasError() {
		if tree != nil {
			tree.Release()
		}
		return nil, false
	}
	defer tree.Release()

	startPoint := advancePointByBytes(Point{}, source[:chunkStart])
	prefixPoint := advancePointByBytes(Point{}, []byte(prefix))
	if startPoint.Row < prefixPoint.Row {
		return nil, false
	}
	offsetRoot := tree.RootNodeWithOffset(
		chunkStart-uint32(len(prefix)),
		Point{Row: startPoint.Row - prefixPoint.Row, Column: startPoint.Column},
	)
	if offsetRoot == nil {
		return nil, false
	}

	fn := goFirstFunctionLikeChild(offsetRoot, p.language)
	if fn == nil || fn.ChildCount() < 4 {
		return nil, false
	}
	openBraceAbs := chunkStart + uint32(openBrace-len(prefix))
	closeBraceAbs := chunkStart + uint32(closeBrace-len(prefix))
	bodyNodes, ok := goRecoverFunctionBodyNodes(source, openBraceAbs+1, closeBraceAbs, p, arena)
	if !ok {
		return nil, false
	}
	recoveredFn := cloneTreeNodesIntoArena(fn, arena)
	block, ok := goBuildRecoveredBlockNode(source, openBraceAbs, closeBraceAbs, bodyNodes, arena, p.language)
	if !ok {
		return nil, false
	}
	recoveredFn.children[len(recoveredFn.children)-1] = block
	block.parent = recoveredFn
	block.childIndex = len(recoveredFn.children) - 1
	populateParentNode(recoveredFn, recoveredFn.children)
	return recoveredFn, true
}

func goRecoverFunctionBodyNodes(source []byte, start, end uint32, p *Parser, arena *nodeArena) ([]*Node, bool) {
	if int(start) >= len(source) || start >= end {
		return nil, false
	}
	ranges := goFunctionStatementRanges(source, start, end)
	if len(ranges) == 0 {
		return nil, true
	}
	out := make([]*Node, 0, len(ranges))
	for _, r := range ranges {
		nodes, ok := goRecoverStatementNodesFromRange(source, r[0], r[1], p, arena)
		if !ok {
			return nil, false
		}
		out = append(out, nodes...)
	}
	return out, true
}

func goRecoverStatementNodesFromRange(source []byte, start, end uint32, p *Parser, arena *nodeArena) ([]*Node, bool) {
	if start >= end {
		return nil, true
	}
	const prefix = "package p\nfunc _() {\n"
	stmt := source[start:end]
	wrapped := make([]byte, 0, len(prefix)+len(stmt)+4)
	wrapped = append(wrapped, prefix...)
	wrapped = append(wrapped, stmt...)
	wrapped = append(wrapped, '\n', '}', '\n')
	tree, err := p.parseForRecovery(wrapped)
	if err == nil && tree != nil && tree.RootNode() != nil {
		startPoint := advancePointByBytes(Point{}, source[:start])
		prefixPoint := advancePointByBytes(Point{}, []byte(prefix))
		if startPoint.Row >= prefixPoint.Row {
			offsetRoot := tree.RootNodeWithOffset(start-uint32(len(prefix)), Point{Row: startPoint.Row - prefixPoint.Row, Column: startPoint.Column})
			if offsetRoot != nil {
				if !offsetRoot.HasError() {
					nodes := goExtractRecoveredStatementNodes(offsetRoot, source, p.language, arena)
					tree.Release()
					if len(nodes) > 0 {
						return nodes, true
					}
				}
				if node := goExtractSingleRecoveredStatement(offsetRoot, source, p.language, arena); node != nil {
					tree.Release()
					return []*Node{node}, true
				}
			}
		}
		tree.Release()
	}
	if node, ok := goRecoverIfStatementFromRange(source, start, end, p, arena); ok {
		return []*Node{node}, true
	}
	return nil, false
}

func goRecoverIfStatementFromRange(source []byte, start, end uint32, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil || p.language == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	trimmedStart := start
	for trimmedStart < end {
		switch source[trimmedStart] {
		case ' ', '\t', '\r', '\n':
			trimmedStart++
		default:
			goto trimmedStartReady
		}
	}
	return nil, false

trimmedStartReady:
	trimmedEnd := end
	for trimmedEnd > trimmedStart {
		switch source[trimmedEnd-1] {
		case ' ', '\t', '\r', '\n':
			trimmedEnd--
		default:
			goto trimmedEndReady
		}
	}
	return nil, false

trimmedEndReady:
	stmt := source[trimmedStart:trimmedEnd]
	if !bytes.HasPrefix(stmt, []byte("if ")) {
		return nil, false
	}
	openBrace := bytes.IndexByte(stmt, '{')
	if openBrace < 0 {
		return nil, false
	}
	closeBrace := findMatchingBraceByte(stmt, openBrace, len(stmt))
	if closeBrace < 0 || closeBrace <= openBrace {
		return nil, false
	}
	openBraceAbs := trimmedStart + uint32(openBrace)
	closeBraceAbs := trimmedStart + uint32(closeBrace)
	condStart := trimmedStart + uint32(len("if "))
	condEnd := openBraceAbs
	for condStart < condEnd {
		switch source[condStart] {
		case ' ', '\t', '\r', '\n':
			condStart++
		default:
			goto condStartReady
		}
	}
	return nil, false

condStartReady:
	for condEnd > condStart {
		switch source[condEnd-1] {
		case ' ', '\t', '\r', '\n':
			condEnd--
		default:
			goto condEndReady
		}
	}
	return nil, false

condEndReady:
	condition, ok := goRecoverExpressionNodeFromRange(source, condStart, condEnd, p, arena)
	if !ok || condition == nil {
		return nil, false
	}
	bodyAbsStart := openBraceAbs + 1
	bodyAbsEnd := closeBraceAbs
	bodyNodes, ok := goRecoverFunctionBodyNodes(source, bodyAbsStart, bodyAbsEnd, p, arena)
	if !ok {
		return nil, false
	}
	block, ok := goBuildRecoveredBlockNode(source, openBraceAbs, closeBraceAbs, bodyNodes, arena, p.language)
	if !ok {
		return nil, false
	}
	ifStmtSym, ok := symbolByName(p.language, "if_statement")
	if !ok {
		return nil, false
	}
	ifTokenSym, ok := symbolByName(p.language, "if")
	if !ok {
		return nil, false
	}
	ifStmtNamed := int(ifStmtSym) < len(p.language.SymbolMetadata) && p.language.SymbolMetadata[ifStmtSym].Named
	ifLeafStart := advancePointByBytes(Point{}, source[:trimmedStart])
	ifLeafEnd := advancePointByBytes(ifLeafStart, source[trimmedStart:trimmedStart+2])
	ifLeaf := newLeafNodeInArena(arena, ifTokenSym, false, trimmedStart, trimmedStart+2, ifLeafStart, ifLeafEnd)
	children := []*Node{ifLeaf, condition, block}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	return newParentNodeInArena(arena, ifStmtSym, ifStmtNamed, children, goSyntheticIfFieldIDs(arena, len(children), p.language), 0), true
}

func goFunctionStatementRanges(source []byte, start, end uint32) [][2]uint32 {
	var ranges [][2]uint32
	chunkStart := uint32(0)
	inChunk := false
	var (
		braceDepth     int
		parenDepth     int
		bracketDepth   int
		inLineComment  bool
		inBlockComment bool
		inString       bool
		inRune         bool
		inRawString    bool
		escape         bool
	)
	flush := func(pos uint32) {
		if !inChunk || pos <= chunkStart {
			inChunk = false
			return
		}
		ranges = append(ranges, [2]uint32{chunkStart, pos})
		inChunk = false
	}
	for i := int(start); i < int(end); i++ {
		b := source[i]
		if !inChunk && (b == ' ' || b == '\t' || b == '\r' || b == '\n') {
			continue
		}
		if !inChunk {
			chunkStart = uint32(i)
			inChunk = true
		}
		if inLineComment {
			if b == '\n' {
				inLineComment = false
				if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
					flush(uint32(i))
				}
			}
			continue
		}
		if inBlockComment {
			if b == '*' && i+1 < int(end) && source[i+1] == '/' {
				inBlockComment = false
				i++
				continue
			}
			continue
		}
		if inString {
			if escape {
				escape = false
				continue
			}
			if b == '\\' {
				escape = true
				continue
			}
			if b == '"' {
				inString = false
			}
			continue
		}
		if inRune {
			if escape {
				escape = false
				continue
			}
			if b == '\\' {
				escape = true
				continue
			}
			if b == '\'' {
				inRune = false
			}
			continue
		}
		if inRawString {
			if b == '`' {
				inRawString = false
			}
			continue
		}
		switch b {
		case '/':
			if i+1 < int(end) && source[i+1] == '/' {
				inLineComment = true
				i++
				continue
			}
			if i+1 < int(end) && source[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
		case '"':
			inString = true
		case '\'':
			inRune = true
		case '`':
			inRawString = true
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '\n':
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				flush(uint32(i))
			}
		}
	}
	if inChunk {
		flush(end)
	}
	return ranges
}

func goFirstFunctionLikeChild(root *Node, lang *Language) *Node {
	if root == nil || lang == nil {
		return nil
	}
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "function_declaration", "method_declaration":
			return child
		}
	}
	return nil
}

func goExtractRecoveredStatementNodes(root *Node, source []byte, lang *Language, arena *nodeArena) []*Node {
	fn := goFirstFunctionLikeChild(root, lang)
	if fn == nil || fn.ChildCount() == 0 {
		return nil
	}
	block := fn.Child(fn.ChildCount() - 1)
	if block == nil || block.Type(lang) != "block" || block.ChildCount() < 2 {
		return nil
	}
	var out []*Node
	for i := 1; i < block.ChildCount()-1; i++ {
		child := block.Child(i)
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "statement_list", "statement_list_repeat1":
			for j := 0; j < child.ChildCount(); j++ {
				grand := child.Child(j)
				if grand != nil {
					if arena != nil {
						cloned := cloneTreeNodesIntoArena(grand, arena)
						recomputeNodePointsFromBytes(cloned, source)
						out = append(out, cloned)
					} else {
						out = append(out, grand)
					}
				}
			}
		default:
			if arena != nil {
				cloned := cloneTreeNodesIntoArena(child, arena)
				recomputeNodePointsFromBytes(cloned, source)
				out = append(out, cloned)
			} else {
				out = append(out, child)
			}
		}
	}
	return out
}

func goExtractSingleRecoveredStatement(root *Node, source []byte, lang *Language, arena *nodeArena) *Node {
	nodes := goExtractRecoveredStatementNodes(root, source, lang, arena)
	if len(nodes) == 1 {
		return nodes[0]
	}
	return nil
}

func goRecoverExpressionNodeFromRange(source []byte, start, end uint32, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil || p.language == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	const prefix = "package p\nvar _ = "
	expr := bytes.TrimSpace(source[start:end])
	if len(expr) == 0 {
		return nil, false
	}
	wrapped := make([]byte, 0, len(prefix)+len(expr)+1)
	wrapped = append(wrapped, prefix...)
	wrapped = append(wrapped, expr...)
	wrapped = append(wrapped, '\n')
	tree, err := p.parseForRecovery(wrapped)
	if err != nil || tree == nil || tree.RootNode() == nil {
		if tree != nil {
			tree.Release()
		}
		return nil, false
	}
	defer tree.Release()
	startPoint := advancePointByBytes(Point{}, source[:start])
	prefixPoint := advancePointByBytes(Point{}, []byte(prefix))
	if startPoint.Row < prefixPoint.Row {
		return nil, false
	}
	offsetRoot := tree.RootNodeWithOffset(start-uint32(len(prefix)), Point{Row: startPoint.Row - prefixPoint.Row, Column: startPoint.Column})
	if offsetRoot == nil || offsetRoot.HasError() {
		return nil, false
	}
	exprNode := goExtractRecoveredVarInitializer(offsetRoot, p.language, arena)
	recomputeNodePointsFromBytes(exprNode, source)
	return exprNode, exprNode != nil
}

func goExtractRecoveredVarInitializer(root *Node, lang *Language, arena *nodeArena) *Node {
	if root == nil || lang == nil {
		return nil
	}
	var walk func(*Node) *Node
	walk = func(n *Node) *Node {
		if n == nil {
			return nil
		}
		if n.Type(lang) == "expression_list" {
			for i := 0; i < n.ChildCount(); i++ {
				child := n.Child(i)
				if child != nil && child.IsNamed() {
					if arena != nil {
						return cloneTreeNodesIntoArena(child, arena)
					}
					return child
				}
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			if out := walk(n.Child(i)); out != nil {
				return out
			}
		}
		return nil
	}
	return walk(root)
}

func goBuildRecoveredBlockNode(source []byte, openBrace, closeBrace uint32, bodyNodes []*Node, arena *nodeArena, lang *Language) (*Node, bool) {
	if lang == nil || int(closeBrace) >= len(source) || openBrace >= closeBrace {
		return nil, false
	}
	blockSym, ok := symbolByName(lang, "block")
	if !ok {
		return nil, false
	}
	blockNamed := int(blockSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[blockSym].Named
	stmtListSym, ok := symbolByName(lang, "statement_list")
	if !ok {
		return nil, false
	}
	stmtListNamed := int(stmtListSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[stmtListSym].Named
	openSym, ok := symbolByName(lang, "{")
	if !ok {
		return nil, false
	}
	closeSym, ok := symbolByName(lang, "}")
	if !ok {
		return nil, false
	}
	openTok := newLeafNodeInArena(arena, openSym, false, openBrace, openBrace+1, advancePointByBytes(Point{}, source[:openBrace]), advancePointByBytes(Point{}, source[:openBrace+1]))
	closeTok := newLeafNodeInArena(arena, closeSym, false, closeBrace, closeBrace+1, advancePointByBytes(Point{}, source[:closeBrace]), advancePointByBytes(Point{}, source[:closeBrace+1]))
	var stmtList *Node
	if len(bodyNodes) > 0 {
		stmtChildren := bodyNodes
		if arena != nil {
			buf := arena.allocNodeSlice(len(bodyNodes))
			copy(buf, bodyNodes)
			stmtChildren = buf
		}
		stmtList = newParentNodeInArena(arena, stmtListSym, stmtListNamed, stmtChildren, nil, 0)
	}
	children := make([]*Node, 0, 3)
	children = append(children, openTok)
	if stmtList != nil {
		children = append(children, stmtList)
	}
	children = append(children, closeTok)
	return newParentNodeInArena(arena, blockSym, blockNamed, children, nil, 0), true
}

func recomputeNodePointsFromBytes(n *Node, source []byte) {
	if n == nil || len(source) == 0 {
		return
	}
	if int(n.startByte) <= len(source) {
		n.startPoint = advancePointByBytes(Point{}, source[:n.startByte])
	}
	if int(n.endByte) <= len(source) {
		n.endPoint = advancePointByBytes(Point{}, source[:n.endByte])
	}
	for _, child := range n.children {
		recomputeNodePointsFromBytes(child, source)
	}
}

func goSyntheticIfFieldIDs(arena *nodeArena, childCount int, lang *Language) []FieldID {
	fieldIDs := make([]FieldID, childCount)
	if arena != nil {
		fieldIDs = arena.allocFieldIDSlice(childCount)
	}
	if fid, ok := lang.FieldByName("condition"); ok && childCount > 1 {
		fieldIDs[1] = fid
	}
	if fid, ok := lang.FieldByName("consequence"); ok && childCount > 2 {
		fieldIDs[2] = fid
	}
	return fieldIDs
}

func goShouldDropSemicolonNode(n *Node, source []byte) bool {
	if n == nil {
		return false
	}
	if n.startByte >= n.endByte || int(n.endByte) > len(source) {
		return true
	}
	text := source[n.startByte:n.endByte]
	if bytes.IndexByte(text, ';') >= 0 {
		return false
	}
	return bytes.IndexByte(text, '\n') >= 0 || bytes.IndexByte(text, '\r') >= 0
}

func normalizeGoCompatibility(root *Node, source []byte, lang *Language) {
	normalizeGoCompatibilityInRanges(root, source, lang, nil)
}

func nodeOverlapsAnyRange(n *Node, ranges []Range) bool {
	if n == nil || len(ranges) == 0 {
		return true
	}
	for _, r := range ranges {
		if !(n.endByte < r.StartByte || r.EndByte < n.startByte) {
			return true
		}
	}
	return false
}

func normalizeGoCompatibilityInRanges(root *Node, source []byte, lang *Language, incrementalRanges []Range) {
	if root == nil || lang == nil || lang.Name != "go" || len(source) == 0 {
		return
	}
	semiSym, ok := symbolByName(lang, ";")
	if !ok {
		return
	}
	expressionCaseSym, ok := symbolByName(lang, "expression_case")
	if !ok {
		return
	}
	defaultCaseSym, ok := symbolByName(lang, "default_case")
	if !ok {
		return
	}
	typeCaseSym, ok := symbolByName(lang, "type_case")
	if !ok {
		return
	}
	communicationCaseSym, ok := symbolByName(lang, "communication_case")
	if !ok {
		return
	}
	statementListSym, ok := symbolByName(lang, "statement_list")
	if !ok {
		return
	}
	statementListRepeatSym, ok := symbolByName(lang, "statement_list_repeat1")
	if !ok {
		return
	}
	semiContainerSyms := make([]Symbol, 0, 8)
	addSemiContainerSym := func(name string) {
		if sym, found := symbolByName(lang, name); found {
			semiContainerSyms = append(semiContainerSyms, sym)
		}
	}
	addSemiContainerSym("source_file")
	addSemiContainerSym("statement_list")
	addSemiContainerSym("statement_list_repeat1")
	addSemiContainerSym("import_declaration")
	addSemiContainerSym("var_declaration")
	addSemiContainerSym("const_declaration")
	addSemiContainerSym("type_declaration")
	addSemiContainerSym("import_spec_list")
	addSemiContainerSym("var_spec_list")
	addSemiContainerSym("const_spec_list")
	addSemiContainerSym("field_declaration_list")
	symbolIn := func(syms []Symbol, want Symbol) bool {
		for _, sym := range syms {
			if sym == want {
				return true
			}
		}
		return false
	}
	isCaseSym := func(sym Symbol) bool {
		switch sym {
		case expressionCaseSym, defaultCaseSym, typeCaseSym, communicationCaseSym:
			return true
		default:
			return false
		}
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if !nodeOverlapsAnyRange(n, incrementalRanges) {
			return
		}
		if len(n.children) > 0 {
			if symbolIn(semiContainerSyms, n.symbol) {
				kept := n.children[:0]
				changed := false
				for _, child := range n.children {
					if child != nil && child.symbol == semiSym && goShouldDropSemicolonNode(child, source) {
						changed = true
						continue
					}
					kept = append(kept, child)
				}
				if changed {
					n.children = kept
					n.fieldIDs = nil
					n.fieldSources = nil
					populateParentNode(n, n.children)
				}
			}
			for i := 0; i+1 < len(n.children); i++ {
				curr := n.children[i]
				next := n.children[i+1]
				if curr == nil || next == nil {
					continue
				}
				if curr.symbol == statementListSym || curr.symbol == statementListRepeatSym {
					if curr.endByte < next.startByte && int(next.startByte) <= len(source) {
						gap := source[curr.endByte:next.startByte]
						if bytesAreTrivia(gap) {
							target := goTrailingNewlineBoundary(curr.endByte, next.startByte, source)
							if target > curr.endByte {
								extendNodeEndTo(curr, target, source)
							}
						}
					}
				}
				if !isCaseSym(curr.symbol) {
					continue
				}
				tail := goTrailingCaseStatementList(curr, statementListSym, statementListRepeatSym)
				if tail == nil {
					continue
				}
				if int(next.startByte) > len(source) {
					continue
				}
				target, hasNewline := goTrailingTriviaBoundaryBefore(next.startByte, source)
				if hasNewline {
					if curr.endByte != target {
						setNodeEndTo(curr, target, source)
					}
					switch {
					case tail.endByte > target:
						setNodeEndTo(tail, target, source)
					case tail.endByte < target && bytesAreTrivia(source[tail.endByte:target]):
						setNodeEndTo(tail, target, source)
					}
					continue
				}
				if curr.endByte > next.startByte {
					setNodeEndTo(curr, next.startByte, source)
				}
				if tail.endByte > next.startByte {
					setNodeEndTo(tail, next.startByte, source)
				}
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func goTrailingNewlineBoundary(start, end uint32, source []byte) uint32 {
	if start >= end || int(end) > len(source) || !bytesAreTrivia(source[start:end]) {
		return start
	}
	gap := source[start:end]
	if newline := bytes.LastIndexByte(gap, '\n'); newline >= 0 {
		return start + uint32(newline+1)
	}
	return start
}

func goTrailingTriviaBoundaryBefore(end uint32, source []byte) (uint32, bool) {
	if end == 0 || int(end) > len(source) {
		return end, false
	}
	start := int(end)
	for start > 0 {
		switch source[start-1] {
		case ' ', '\t', '\r', '\n':
			start--
		default:
			goto gapReady
		}
	}
gapReady:
	gap := source[start:int(end)]
	if newline := bytes.LastIndexByte(gap, '\n'); newline >= 0 {
		return uint32(start + newline + 1), true
	}
	return end, false
}

func goTrailingCaseStatementList(n *Node, statementListSym, statementListRepeatSym Symbol) *Node {
	if n == nil || len(n.children) == 0 {
		return nil
	}
	last := n.children[len(n.children)-1]
	if last == nil {
		return nil
	}
	switch last.symbol {
	case statementListSym, statementListRepeatSym:
		return last
	default:
		return nil
	}
}

func goTopLevelChunkStarts(source []byte, start uint32) []uint32 {
	if int(start) >= len(source) {
		return nil
	}
	var starts []uint32
	var (
		braceDepth     int
		parenDepth     int
		bracketDepth   int
		inLineComment  bool
		inBlockComment bool
		inString       bool
		inRune         bool
		inRawString    bool
		escape         bool
		lineStart      = uint32(0)
		atLineStart    = true
	)
	for i := 0; i < len(source); i++ {
		b := source[i]
		if inLineComment {
			if b == '\n' {
				inLineComment = false
				lineStart = uint32(i + 1)
				atLineStart = true
			}
			continue
		}
		if inBlockComment {
			if b == '*' && i+1 < len(source) && source[i+1] == '/' {
				inBlockComment = false
				i++
				continue
			}
			if b == '\n' {
				lineStart = uint32(i + 1)
				atLineStart = true
			}
			continue
		}
		if inString {
			if escape {
				escape = false
				continue
			}
			if b == '\\' {
				escape = true
				continue
			}
			if b == '"' {
				inString = false
			}
			if b == '\n' {
				lineStart = uint32(i + 1)
				atLineStart = true
			}
			continue
		}
		if inRune {
			if escape {
				escape = false
				continue
			}
			if b == '\\' {
				escape = true
				continue
			}
			if b == '\'' {
				inRune = false
			}
			if b == '\n' {
				lineStart = uint32(i + 1)
				atLineStart = true
			}
			continue
		}
		if inRawString {
			if b == '`' {
				inRawString = false
				continue
			}
			if b == '\n' {
				lineStart = uint32(i + 1)
				atLineStart = true
			}
			continue
		}
		if atLineStart {
			j := i
			for j < len(source) && (source[j] == ' ' || source[j] == '\t' || source[j] == '\r') {
				j++
			}
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 && uint32(j) >= start && goLineStartsTopLevelChunk(source[j:]) {
				starts = append(starts, uint32(j))
			}
			atLineStart = false
		}
		switch b {
		case '/':
			if i+1 < len(source) && source[i+1] == '/' {
				inLineComment = true
				i++
				continue
			}
			if i+1 < len(source) && source[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
		case '"':
			inString = true
		case '\'':
			inRune = true
		case '`':
			inRawString = true
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '\n':
			lineStart = uint32(i + 1)
			atLineStart = true
		}
		_ = lineStart
	}
	return starts
}

func goLineStartsTopLevelChunk(line []byte) bool {
	switch {
	case len(line) == 0:
		return false
	case bytes.HasPrefix(line, []byte("//")),
		bytes.HasPrefix(line, []byte("/*")),
		bytes.HasPrefix(line, []byte("func ")),
		bytes.HasPrefix(line, []byte("var ")),
		bytes.HasPrefix(line, []byte("const ")),
		bytes.HasPrefix(line, []byte("type ")),
		bytes.HasPrefix(line, []byte("import ")):
		return true
	default:
		return false
	}
}

func flattenRootSelfFragments(nodes []*Node, arena *nodeArena, rootSymbol Symbol) []*Node {
	if len(nodes) <= 1 {
		return nodes
	}
	changed := false
	out := make([]*Node, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if node.symbol == rootSymbol && len(node.children) > 0 {
			out = append(out, node.children...)
			changed = true
			continue
		}
		out = append(out, node)
	}
	if !changed {
		return nodes
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(out))
		copy(buf, out)
		return buf
	}
	return out
}

func flattenInvisibleRootChildren(root *Node, arena *nodeArena, lang *Language) *Node {
	if root == nil || lang == nil || len(root.children) == 0 {
		return root
	}
	changed := false
	out := make([]*Node, 0, len(root.children))
	for _, child := range root.children {
		if child == nil {
			continue
		}
		if shouldFlattenInvisibleRootChild(child, lang) {
			for _, grandchild := range child.children {
				if grandchild != nil {
					out = append(out, grandchild)
				}
			}
			changed = true
			continue
		}
		out = append(out, child)
	}
	if !changed {
		return root
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(out))
		copy(buf, out)
		out = buf
	}
	root.children = out
	root.fieldIDs = nil
	root.fieldSources = nil
	return root
}

func shouldFlattenInvisibleRootChild(child *Node, lang *Language) bool {
	if child == nil || child.isExtra || child.isNamed || len(child.children) == 0 {
		return false
	}
	if idx := int(child.symbol); idx < len(lang.SymbolMetadata) {
		return !lang.SymbolMetadata[idx].Visible
	}
	return false
}

func normalizePHPSingletonTypeWrappers(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "php" {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		for i, child := range n.children {
			if child == nil {
				continue
			}
			switch child.Type(lang) {
			case "intersection_type", "union_type":
				if len(child.children) == 1 && child.children[0] != nil && child.children[0].IsNamed() {
					n.children[i] = child.children[0]
					child = n.children[i]
				}
			}
			walk(child)
		}
	}
	walk(root)
}

func normalizeDartSingleTypeArgumentFreeCalls(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "dart" {
		return
	}
	relExprSym, ok := lang.SymbolByName("relational_expression")
	if !ok {
		return
	}
	relOpSym, ok := lang.SymbolByName("relational_operator")
	if !ok {
		return
	}
	parenSym, ok := lang.SymbolByName("parenthesized_expression")
	if !ok {
		return
	}
	relExprNamed := false
	if idx := int(relExprSym); idx < len(lang.SymbolMetadata) {
		relExprNamed = lang.SymbolMetadata[relExprSym].Named
	}
	relOpNamed := false
	if idx := int(relOpSym); idx < len(lang.SymbolMetadata) {
		relOpNamed = lang.SymbolMetadata[relOpSym].Named
	}
	parenNamed := false
	if idx := int(parenSym); idx < len(lang.SymbolMetadata) {
		parenNamed = lang.SymbolMetadata[parenSym].Named
	}

	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		for i := 0; i+1 < len(n.children); i++ {
			if rewriteDartSingleTypeArgumentFreeCall(n, i, lang, relExprSym, relExprNamed, relOpSym, relOpNamed, parenSym, parenNamed) {
				break
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeDartConstructorSignatureKinds(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "dart" {
		return
	}
	constructorSym, ok := lang.SymbolByName("constructor_signature")
	if !ok {
		return
	}
	parametersID, _ := lang.FieldByName("parameters")
	constructorNamed := false
	if idx := int(constructorSym); idx < len(lang.SymbolMetadata) {
		constructorNamed = lang.SymbolMetadata[constructorSym].Named
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "class_definition" {
			className := n.ChildByFieldName("name", lang)
			body := n.ChildByFieldName("body", lang)
			if className != nil && body != nil {
				classText := className.Text(source)
				for _, member := range body.children {
					if member == nil || member.Type(lang) != "method_signature" || len(member.children) != 1 {
						continue
					}
					sig := member.children[0]
					if sig == nil || sig.Type(lang) != "function_signature" || len(sig.children) != 2 {
						continue
					}
					name := sig.children[0]
					params := sig.children[1]
					if name == nil || params == nil || name.Type(lang) != "identifier" || params.Type(lang) != "formal_parameter_list" {
						continue
					}
					if name.Text(source) != classText {
						continue
					}
					sig.symbol = constructorSym
					sig.isNamed = constructorNamed
					if len(sig.fieldIDs) != len(sig.children) {
						ensureNodeFieldStorage(sig, len(sig.children))
					}
					if parametersID != 0 && len(sig.fieldIDs) > 1 {
						sig.fieldIDs[1] = parametersID
						if len(sig.fieldSources) == len(sig.children) {
							sig.fieldSources[1] = fieldSourceDirect
						}
					}
				}
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeDartSwitchExpressionBodyFields(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "dart" {
		return
	}
	bodyID, ok := lang.FieldByName("body")
	if !ok {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "switch_expression" && len(n.children) > 0 {
			ensureNodeFieldStorage(n, len(n.children))
			start := -1
			for i := 0; i < len(n.children); i++ {
				if n.fieldIDs[i] == bodyID {
					start = i
					break
				}
			}
			if start >= 0 {
				for i := start; i < len(n.children); i++ {
					if n.children[i] == nil {
						continue
					}
					n.fieldIDs[i] = bodyID
					if len(n.fieldSources) == len(n.children) {
						n.fieldSources[i] = fieldSourceDirect
					}
				}
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeMakeConditionalConsequenceFields(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "make" {
		return
	}
	consequenceID, ok := lang.FieldByName("consequence")
	if !ok {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		switch n.Type(lang) {
		case "conditional", "elsif_directive", "else_directive":
			ensureNodeFieldStorage(n, len(n.children))
			start, end := -1, -1
			for i := 0; i < len(n.children); i++ {
				if n.fieldIDs[i] != consequenceID {
					continue
				}
				if start < 0 {
					start = i
				}
				end = i
			}
			if start >= 0 && end >= start {
				for start > 0 {
					prev := n.children[start-1]
					if prev == nil || prev.isNamed || prev.isExtra || prev.Type(lang) != "\t" {
						break
					}
					start--
				}
				for i := start; i <= end; i++ {
					if n.children[i] == nil {
						continue
					}
					n.fieldIDs[i] = consequenceID
					if len(n.fieldSources) == len(n.children) {
						n.fieldSources[i] = fieldSourceDirect
					}
				}
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeIniSectionStarts(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "ini" {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "section" {
			for _, child := range n.children {
				if child == nil {
					continue
				}
				if n.startByte < child.startByte {
					n.startByte = child.startByte
					n.startPoint = child.startPoint
				}
				break
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeZigEmptyInitListFields(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "zig" {
		return
	}
	fieldConstantID, ok := lang.FieldByName("field_constant")
	if !ok {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if len(n.fieldIDs) == len(n.children) {
			for i, child := range n.children {
				if child == nil || n.fieldIDs[i] != fieldConstantID || child.Type(lang) != "InitList" {
					continue
				}
				if n.Type(lang) != "SuffixExpr" || len(n.children) != 2 || i != 1 || n.children[0] == nil || n.children[0].Type(lang) != "." {
					continue
				}
				n.fieldIDs[i] = 0
				if len(n.fieldSources) == len(n.children) {
					n.fieldSources[i] = fieldSourceNone
				}
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func ensureNodeFieldStorage(n *Node, childCount int) {
	if n == nil || childCount <= 0 {
		return
	}
	if len(n.fieldIDs) != childCount {
		fieldIDs := make([]FieldID, childCount)
		copy(fieldIDs, n.fieldIDs)
		if n.ownerArena != nil {
			buf := n.ownerArena.allocFieldIDSlice(childCount)
			copy(buf, fieldIDs)
			fieldIDs = buf
		}
		n.fieldIDs = fieldIDs
	}
	if len(n.fieldSources) != childCount {
		fieldSources := make([]uint8, childCount)
		copy(fieldSources, n.fieldSources)
		n.fieldSources = fieldSources
	}
}

func rewriteDartSingleTypeArgumentFreeCall(parent *Node, idx int, lang *Language, relExprSym Symbol, relExprNamed bool, relOpSym Symbol, relOpNamed bool, parenSym Symbol, parenNamed bool) bool {
	if parent == nil || idx < 0 || idx+1 >= len(parent.children) || lang == nil {
		return false
	}
	callee := parent.children[idx]
	selector := parent.children[idx+1]
	if callee == nil || selector == nil || callee.Type(lang) != "identifier" || selector.Type(lang) != "selector" || len(selector.children) != 1 {
		return false
	}
	argPart := selector.children[0]
	if argPart == nil || argPart.Type(lang) != "argument_part" || len(argPart.children) != 2 {
		return false
	}
	typeArgs := argPart.children[0]
	args := argPart.children[1]
	if typeArgs == nil || args == nil || typeArgs.Type(lang) != "type_arguments" || args.Type(lang) != "arguments" {
		return false
	}
	typeIdent, lt, gt, ok := dartSimpleTypeArgumentParts(typeArgs, lang)
	if !ok {
		return false
	}
	if len(args.children) < 2 {
		return false
	}

	arena := parent.ownerArena
	if typeIdent.Type(lang) == "type_identifier" {
		identSym, ok := lang.SymbolByName("identifier")
		if !ok {
			return false
		}
		identNamed := false
		if idx := int(identSym); idx < len(lang.SymbolMetadata) {
			identNamed = lang.SymbolMetadata[identSym].Named
		}
		typeIdent = newLeafNodeInArena(arena, identSym, identNamed, typeIdent.startByte, typeIdent.endByte, typeIdent.startPoint, typeIdent.endPoint)
	}
	lessOp := newParentNodeInArena(arena, relOpSym, relOpNamed, []*Node{lt}, nil, 0)
	left := newParentNodeInArena(arena, relExprSym, relExprNamed, []*Node{callee, lessOp, typeIdent}, nil, 0)
	greaterOp := newParentNodeInArena(arena, relOpSym, relOpNamed, []*Node{gt}, nil, 0)
	parenChildren := dartParenthesizedExpressionChildren(args, lang)
	paren := newParentNodeInArena(arena, parenSym, parenNamed, parenChildren, nil, args.productionID)
	outer := newParentNodeInArena(arena, relExprSym, relExprNamed, []*Node{left, greaterOp, paren}, nil, 0)
	replaceChildRangeWithSingleNode(parent, idx, idx+2, outer)
	return true
}

func dartSimpleTypeArgumentParts(typeArgs *Node, lang *Language) (*Node, *Node, *Node, bool) {
	if typeArgs == nil || lang == nil || typeArgs.Type(lang) != "type_arguments" || len(typeArgs.children) < 3 {
		return nil, nil, nil, false
	}
	lt := typeArgs.children[0]
	gt := typeArgs.children[len(typeArgs.children)-1]
	if lt == nil || gt == nil || lt.Type(lang) != "<" || gt.Type(lang) != ">" {
		return nil, nil, nil, false
	}
	if got := typeArgs.NamedChildCount(); got != 1 {
		return nil, nil, nil, false
	}
	typeIdent := typeArgs.NamedChild(0)
	if typeIdent == nil || typeIdent.Type(lang) != "type_identifier" || nodeContainsNamedType(typeIdent, lang, "type_arguments") {
		return nil, nil, nil, false
	}
	return typeIdent, lt, gt, true
}

func nodeContainsNamedType(root *Node, lang *Language, want string) bool {
	if root == nil || lang == nil {
		return false
	}
	for _, child := range root.children {
		if child == nil {
			continue
		}
		if child.Type(lang) == want {
			return true
		}
		if nodeContainsNamedType(child, lang, want) {
			return true
		}
	}
	return false
}

func replaceChildRangeWithSingleNode(parent *Node, start, end int, replacement *Node) {
	if parent == nil || replacement == nil || start < 0 || start >= end || end > len(parent.children) {
		return
	}
	oldLen := len(parent.children)
	newChildren := make([]*Node, 0, oldLen-(end-start)+1)
	newChildren = append(newChildren, parent.children[:start]...)
	newChildren = append(newChildren, replacement)
	newChildren = append(newChildren, parent.children[end:]...)
	parent.children = newChildren

	if len(parent.fieldIDs) == oldLen {
		newFieldIDs := make([]FieldID, 0, len(newChildren))
		newFieldIDs = append(newFieldIDs, parent.fieldIDs[:start]...)
		mergedField := FieldID(0)
		for i := start; i < end; i++ {
			if parent.fieldIDs[i] != 0 {
				mergedField = parent.fieldIDs[i]
				break
			}
		}
		newFieldIDs = append(newFieldIDs, mergedField)
		newFieldIDs = append(newFieldIDs, parent.fieldIDs[end:]...)
		parent.fieldIDs = newFieldIDs
	}
	if len(parent.fieldSources) == oldLen {
		newFieldSources := make([]uint8, 0, len(newChildren))
		newFieldSources = append(newFieldSources, parent.fieldSources[:start]...)
		mergedSource := uint8(fieldSourceNone)
		for i := start; i < end; i++ {
			if parent.fieldSources[i] != fieldSourceNone {
				mergedSource = parent.fieldSources[i]
				break
			}
		}
		newFieldSources = append(newFieldSources, mergedSource)
		newFieldSources = append(newFieldSources, parent.fieldSources[end:]...)
		parent.fieldSources = newFieldSources
	}
	for i, child := range parent.children {
		if child == nil {
			continue
		}
		child.parent = parent
		child.childIndex = i
	}
}

func dartParenthesizedExpressionChildren(args *Node, lang *Language) []*Node {
	if args == nil || lang == nil {
		return nil
	}
	if len(args.children) != 3 {
		return append([]*Node(nil), args.children...)
	}
	open := args.children[0]
	mid := args.children[1]
	close := args.children[2]
	if open == nil || mid == nil || close == nil {
		return append([]*Node(nil), args.children...)
	}
	if mid.Type(lang) != "argument" || len(mid.children) != 1 || mid.children[0] == nil {
		return append([]*Node(nil), args.children...)
	}
	return []*Node{open, mid.children[0], close}
}

func normalizePHPStaticFunctionFragments(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "php" || len(root.children) == 0 {
		return
	}
	rootType := root.Type(lang)
	if rootType != "program" && rootType != "ERROR" {
		return
	}
	children := root.children
	changed := false
	if children[0] != nil && ((rootType == "program" && children[0].Type(lang) == rootType) || (rootType == "ERROR" && children[0].Type(lang) == "program")) {
		flat := make([]*Node, 0, len(children[0].children)+len(children)-1)
		flat = append(flat, children[0].children...)
		flat = append(flat, children[1:]...)
		children = flat
		changed = true
	}
	arena := root.ownerArena
	out := make([]*Node, 0, len(children))
	seenNonExtra := false
	for i := 0; i < len(children); {
		if repl, consumed, ok := rewritePHPStaticAnonymousHeaderWithTrailingArrowFragments(children[i:], source, lang, arena); ok {
			out = append(out, repl...)
			i += consumed
			changed = true
			for _, n := range repl {
				if phpCountsAsPriorTopLevelNode(n, lang) {
					seenNonExtra = true
				}
			}
			continue
		}
		if repl, consumed, ok := rewritePHPStaticNamedFunctionFragmentsWithTrailingMalformedSibling(children[i:], source, lang, arena, seenNonExtra); ok {
			out = append(out, repl...)
			i += consumed
			changed = true
			for _, n := range repl {
				if phpCountsAsPriorTopLevelNode(n, lang) {
					seenNonExtra = true
				}
			}
			continue
		}
		if repl, consumed, ok := rewritePHPStaticNamedFunctionFragments(children[i:], source, lang, arena, seenNonExtra); ok {
			out = append(out, repl...)
			i += consumed
			changed = true
			for _, n := range repl {
				if phpCountsAsPriorTopLevelNode(n, lang) {
					seenNonExtra = true
				}
			}
			continue
		}
		if repl, consumed, ok := rewritePHPStaticAnonymousFunctionFragments(children[i:], source, lang, arena); ok {
			out = append(out, repl...)
			i += consumed
			changed = true
			for _, n := range repl {
				if phpCountsAsPriorTopLevelNode(n, lang) {
					seenNonExtra = true
				}
			}
			continue
		}
		out = append(out, children[i])
		if phpCountsAsPriorTopLevelNode(children[i], lang) {
			seenNonExtra = true
		}
		i++
	}
	if !changed {
		return
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(out))
		copy(buf, out)
		out = buf
	}
	root.children = out
	root.fieldIDs = nil
	root.fieldSources = nil
	assignPHPTopLevelFragmentFields(root, lang, arena)
	populateParentNode(root, out)
	extendNodeToTrailingWhitespace(root, source)
}

func rewritePHPStaticAnonymousHeaderWithTrailingArrowFragments(nodes []*Node, source []byte, lang *Language, arena *nodeArena) ([]*Node, int, bool) {
	if len(nodes) < 4 {
		return nil, 0, false
	}
	headerErr := nodes[0]
	openBrace := nodes[1]
	body := nodes[2]
	arrowStmt := nodes[3]
	if headerErr == nil || openBrace == nil || body == nil || arrowStmt == nil {
		return nil, 0, false
	}
	if headerErr.Type(lang) != "ERROR" || len(headerErr.children) != 1 || headerErr.children[0] == nil || headerErr.children[0].Type(lang) != "_anonymous_function_header" {
		return nil, 0, false
	}
	header := headerErr.children[0]
	if len(header.children) != 3 || header.children[0] == nil || header.children[1] == nil || header.children[2] == nil {
		return nil, 0, false
	}
	if header.children[0].Type(lang) != "static_modifier" || header.children[1].Type(lang) != "function" || header.children[2].Type(lang) != "formal_parameters" {
		return nil, 0, false
	}
	if openBrace.Type(lang) != "{" || body.Type(lang) != "compound_statement" || len(body.children) < 2 {
		return nil, 0, false
	}
	closeBrace := body.children[0]
	if closeBrace == nil || closeBrace.Type(lang) != "}" {
		return nil, 0, false
	}
	var trailingComment *Node
	var suffixStart uint32
	switch {
	case len(body.children) >= 3 && body.children[1] != nil && body.children[1].Type(lang) == "comment" && body.children[2] != nil:
		trailingComment = body.children[1]
		suffixStart = body.children[2].startByte
	case len(body.children) >= 2 && body.children[1] != nil:
		suffixStart = body.children[1].startByte
	default:
		return nil, 0, false
	}
	if arrowStmt.Type(lang) != "statement" || suffixStart == 0 || int(suffixStart) >= len(source) {
		return nil, 0, false
	}

	closeErrChildren := phpAllocChildren(arena, 1)
	closeErrChildren[0] = closeBrace
	closeErr := newParentNodeInArena(arena, errorSymbol, true, closeErrChildren, nil, 0)
	closeErr.hasError = true
	closeErr.isExtra = true

	prefixLen := 5
	if trailingComment != nil {
		prefixLen++
	}
	prefix := phpAllocChildren(arena, prefixLen)
	prefix[0] = header.children[0]
	prefix[1] = header.children[1]
	prefix[2] = header.children[2]
	prefix[3] = openBrace
	prefix[4] = closeErr
	if trailingComment != nil {
		prefix[5] = trailingComment
	}

	suffix, ok := phpReparsedTopLevelSuffix(source, suffixStart, lang, arena)
	if !ok {
		return nil, 0, false
	}
	combined := phpAllocChildren(arena, len(prefix)+len(suffix))
	copy(combined, prefix)
	copy(combined[len(prefix):], suffix)
	return combined, len(nodes), true
}

func rewritePHPStaticNamedFunctionFragments(nodes []*Node, source []byte, lang *Language, arena *nodeArena, hasPriorNonExtra bool) ([]*Node, int, bool) {
	if len(nodes) < 3 {
		return nil, 0, false
	}
	staticErr := nodes[0]
	header := nodes[1]
	bodyErr := nodes[2]
	if staticErr == nil || header == nil || bodyErr == nil {
		return nil, 0, false
	}
	if staticErr.Type(lang) != "ERROR" || len(staticErr.children) != 1 || staticErr.children[0] == nil || staticErr.children[0].Type(lang) != "static_modifier" {
		return nil, 0, false
	}
	if header.Type(lang) != "_anonymous_function_header" || len(header.children) != 3 {
		return nil, 0, false
	}
	if header.children[0] == nil || header.children[0].Type(lang) != "function" {
		return nil, 0, false
	}
	if header.children[1] == nil || header.children[1].Type(lang) != "ERROR" {
		return nil, 0, false
	}
	if header.children[2] == nil || header.children[2].Type(lang) != "formal_parameters" {
		return nil, 0, false
	}
	body, ok := phpSyntheticCompoundStatementFromError(bodyErr, source, lang, arena)
	if !ok {
		return nil, 0, false
	}
	nameNode, ok := phpSyntheticNamedFunctionName(header.children[1], lang, arena)
	if !ok {
		return nil, 0, false
	}
	args, ok := phpSyntheticArgumentsFromFormals(header.children[2], lang, arena)
	if !ok {
		return nil, 0, false
	}
	callSym, callNamed, ok := phpSymbolMeta(lang, "function_call_expression")
	if !ok {
		return nil, 0, false
	}
	callChildren := phpAllocChildren(arena, 2)
	callChildren[0] = nameNode
	callChildren[1] = args
	call := newParentNodeInArena(arena, callSym, callNamed, callChildren, phpSyntheticFieldIDs(arena, 2, lang, map[int]string{
		0: "function",
		1: "arguments",
	}), 0)

	errChildren := phpAllocChildren(arena, 3)
	errChildren[0] = staticErr.children[0]
	errChildren[1] = header.children[0]
	errChildren[2] = call
	if hasPriorNonExtra {
		errChildren = errChildren[:2]
		errNode := newParentNodeInArena(arena, errorSymbol, true, errChildren, nil, 0)
		errNode.hasError = true
		errNode.isExtra = true

		semiSym, ok := lang.SymbolByName(";")
		if !ok {
			return nil, 0, false
		}
		semi := newLeafNodeInArena(arena, semiSym, false, call.endByte, call.endByte, call.endPoint, call.endPoint)
		semi.hasError = true

		exprSym, exprNamed, ok := phpSymbolMeta(lang, "expression_statement")
		if !ok {
			return nil, 0, false
		}
		exprChildren := phpAllocChildren(arena, 2)
		exprChildren[0] = call
		exprChildren[1] = semi
		expr := newParentNodeInArena(arena, exprSym, exprNamed, exprChildren, nil, 0)

		repl := phpAllocChildren(arena, 3)
		repl[0] = errNode
		repl[1] = expr
		repl[2] = body
		if suffix, ok := phpReparsedTopLevelSuffix(source, body.endByte, lang, arena); ok {
			combined := phpAllocChildren(arena, len(repl)+len(suffix))
			copy(combined, repl)
			copy(combined[len(repl):], suffix)
			return combined, len(nodes), true
		}
		return repl, 3, true
	}

	errNode := newParentNodeInArena(arena, errorSymbol, true, errChildren, nil, 0)
	errNode.hasError = true
	errNode.isExtra = true

	repl := phpAllocChildren(arena, 2)
	repl[0] = errNode
	repl[1] = body
	if suffix, ok := phpReparsedTopLevelSuffix(source, body.endByte, lang, arena); ok {
		combined := phpAllocChildren(arena, len(repl)+len(suffix))
		copy(combined, repl)
		copy(combined[len(repl):], suffix)
		return combined, len(nodes), true
	}
	return repl, 3, true
}

func rewritePHPStaticNamedFunctionFragmentsWithTrailingMalformedSibling(nodes []*Node, source []byte, lang *Language, arena *nodeArena, hasPriorNonExtra bool) ([]*Node, int, bool) {
	if len(nodes) < 3 {
		return nil, 0, false
	}
	staticErr := nodes[0]
	header := nodes[1]
	bodyCarrier := nodes[2]
	if staticErr == nil || header == nil || bodyCarrier == nil {
		return nil, 0, false
	}
	if staticErr.Type(lang) != "ERROR" || len(staticErr.children) != 1 || staticErr.children[0] == nil || staticErr.children[0].Type(lang) != "static_modifier" {
		return nil, 0, false
	}
	if header.Type(lang) != "_anonymous_function_header" || len(header.children) != 3 {
		return nil, 0, false
	}
	if header.children[0] == nil || header.children[0].Type(lang) != "function" {
		return nil, 0, false
	}
	if header.children[1] == nil || header.children[1].Type(lang) != "ERROR" {
		return nil, 0, false
	}
	if header.children[2] == nil || header.children[2].Type(lang) != "formal_parameters" {
		return nil, 0, false
	}
	if bodyCarrier.Type(lang) != "_anonymous_function_header" && bodyCarrier.Type(lang) != "_arrow_function_header" {
		return nil, 0, false
	}
	if len(bodyCarrier.children) == 0 || bodyCarrier.children[0] == nil || bodyCarrier.children[0].Type(lang) != "ERROR" {
		return nil, 0, false
	}
	body, ok := phpSyntheticCompoundStatementFromError(bodyCarrier.children[0], source, lang, arena)
	if !ok {
		return nil, 0, false
	}
	nameNode, ok := phpSyntheticNamedFunctionName(header.children[1], lang, arena)
	if !ok {
		return nil, 0, false
	}
	args, ok := phpSyntheticArgumentsFromFormals(header.children[2], lang, arena)
	if !ok {
		return nil, 0, false
	}
	callSym, callNamed, ok := phpSymbolMeta(lang, "function_call_expression")
	if !ok {
		return nil, 0, false
	}
	callChildren := phpAllocChildren(arena, 2)
	callChildren[0] = nameNode
	callChildren[1] = args
	call := newParentNodeInArena(arena, callSym, callNamed, callChildren, phpSyntheticFieldIDs(arena, 2, lang, map[int]string{
		0: "function",
		1: "arguments",
	}), 0)

	errChildren := phpAllocChildren(arena, 3)
	errChildren[0] = staticErr.children[0]
	errChildren[1] = header.children[0]
	errChildren[2] = call
	var repl []*Node
	if hasPriorNonExtra {
		errChildren = errChildren[:2]
		errNode := newParentNodeInArena(arena, errorSymbol, true, errChildren, nil, 0)
		errNode.hasError = true
		errNode.isExtra = true

		semiSym, ok := lang.SymbolByName(";")
		if !ok {
			return nil, 0, false
		}
		semi := newLeafNodeInArena(arena, semiSym, false, call.endByte, call.endByte, call.endPoint, call.endPoint)
		semi.hasError = true

		exprSym, exprNamed, ok := phpSymbolMeta(lang, "expression_statement")
		if !ok {
			return nil, 0, false
		}
		exprChildren := phpAllocChildren(arena, 2)
		exprChildren[0] = call
		exprChildren[1] = semi
		expr := newParentNodeInArena(arena, exprSym, exprNamed, exprChildren, nil, 0)

		repl = phpAllocChildren(arena, 3)
		repl[0] = errNode
		repl[1] = expr
		repl[2] = body
	} else {
		errNode := newParentNodeInArena(arena, errorSymbol, true, errChildren, nil, 0)
		errNode.hasError = true
		errNode.isExtra = true
		repl = phpAllocChildren(arena, 2)
		repl[0] = errNode
		repl[1] = body
	}
	suffix, ok := phpReparsedTopLevelSuffix(source, body.endByte, lang, arena)
	if !ok {
		return nil, 0, false
	}
	combined := phpAllocChildren(arena, len(repl)+len(suffix))
	copy(combined, repl)
	copy(combined[len(repl):], suffix)
	return combined, len(nodes), true
}

func rewritePHPStaticAnonymousFunctionFragments(nodes []*Node, source []byte, lang *Language, arena *nodeArena) ([]*Node, int, bool) {
	if len(nodes) < 3 {
		return nil, 0, false
	}
	errNode := nodes[0]
	openBrace := nodes[1]
	closeBrace := nodes[2]
	if errNode == nil || openBrace == nil || closeBrace == nil {
		return nil, 0, false
	}
	if errNode.Type(lang) != "ERROR" || len(errNode.children) != 1 || errNode.children[0] == nil || errNode.children[0].Type(lang) != "_anonymous_function_header" {
		return nil, 0, false
	}
	header := errNode.children[0]
	if len(header.children) != 3 || header.children[0] == nil || header.children[1] == nil || header.children[2] == nil {
		return nil, 0, false
	}
	if header.children[0].Type(lang) != "static_modifier" || header.children[1].Type(lang) != "function" || header.children[2].Type(lang) != "formal_parameters" {
		return nil, 0, false
	}
	if openBrace.Type(lang) != "{" || closeBrace.Type(lang) != "}" {
		return nil, 0, false
	}
	compoundSym, compoundNamed, ok := phpSymbolMeta(lang, "compound_statement")
	if !ok {
		return nil, 0, false
	}
	bodyChildren := phpAllocChildren(arena, 2)
	bodyChildren[0] = openBrace
	bodyChildren[1] = closeBrace
	body := newParentNodeInArena(arena, compoundSym, compoundNamed, bodyChildren, nil, 0)

	anonSym, anonNamed, ok := phpSymbolMeta(lang, "anonymous_function")
	if !ok {
		return nil, 0, false
	}
	anonChildren := phpAllocChildren(arena, 4)
	anonChildren[0] = header.children[0]
	anonChildren[1] = header.children[1]
	anonChildren[2] = header.children[2]
	anonChildren[3] = body
	anon := newParentNodeInArena(arena, anonSym, anonNamed, anonChildren, phpSyntheticFieldIDs(arena, 4, lang, map[int]string{
		0: "static_modifier",
		2: "parameters",
		3: "body",
	}), 0)

	extraCount := 0
	for 3+extraCount < len(nodes) {
		next := nodes[3+extraCount]
		if next == nil || !next.isExtra {
			break
		}
		extraCount++
	}

	semiSym, ok := lang.SymbolByName(";")
	if !ok {
		return nil, 0, false
	}
	semiStartByte := closeBrace.endByte
	semiStartPoint := closeBrace.endPoint
	if extraCount > 0 {
		lastExtra := nodes[3+extraCount-1]
		semiStartByte = lastExtra.endByte
		semiStartPoint = lastExtra.endPoint
	}
	semi := newLeafNodeInArena(arena, semiSym, false, semiStartByte, semiStartByte, semiStartPoint, semiStartPoint)
	semi.hasError = true

	exprSym, exprNamed, ok := phpSymbolMeta(lang, "expression_statement")
	if !ok {
		return nil, 0, false
	}
	exprChildren := phpAllocChildren(arena, 2+extraCount)
	exprChildren[0] = anon
	for i := 0; i < extraCount; i++ {
		exprChildren[1+i] = nodes[3+i]
	}
	exprChildren[len(exprChildren)-1] = semi
	expr := newParentNodeInArena(arena, exprSym, exprNamed, exprChildren, nil, 0)

	repl := phpAllocChildren(arena, 1)
	repl[0] = expr
	return repl, 3 + extraCount, true
}

func phpSyntheticNamedFunctionName(errNode *Node, lang *Language, arena *nodeArena) (*Node, bool) {
	if errNode == nil || errNode.startByte >= errNode.endByte {
		return nil, false
	}
	nameSym, nameNamed, ok := phpSymbolMeta(lang, "name")
	if !ok {
		return nil, false
	}
	return newLeafNodeInArena(arena, nameSym, nameNamed, errNode.startByte, errNode.endByte, errNode.startPoint, errNode.endPoint), true
}

func phpSyntheticArgumentsFromFormals(formals *Node, lang *Language, arena *nodeArena) (*Node, bool) {
	if formals == nil || formals.Type(lang) != "formal_parameters" || len(formals.children) != 2 {
		return nil, false
	}
	argsSym, argsNamed, ok := phpSymbolMeta(lang, "arguments")
	if !ok {
		return nil, false
	}
	children := phpAllocChildren(arena, 2)
	children[0] = formals.children[0]
	children[1] = formals.children[1]
	return newParentNodeInArena(arena, argsSym, argsNamed, children, nil, 0), true
}

func phpSyntheticCompoundStatementFromError(errNode *Node, source []byte, lang *Language, arena *nodeArena) (*Node, bool) {
	if errNode == nil || errNode.startByte >= errNode.endByte || int(errNode.endByte) > len(source) {
		return nil, false
	}
	body := source[errNode.startByte:errNode.endByte]
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return nil, false
	}
	compoundSym, compoundNamed, ok := phpSymbolMeta(lang, "compound_statement")
	if !ok {
		return nil, false
	}
	openSym, ok := lang.SymbolByName("{")
	if !ok {
		return nil, false
	}
	closeSym, ok := lang.SymbolByName("}")
	if !ok {
		return nil, false
	}
	openEndByte := errNode.startByte + 1
	openEndPoint := advancePointByBytes(errNode.startPoint, source[errNode.startByte:openEndByte])
	closeStartByte := errNode.endByte - 1
	closeStartPoint := advancePointByBytes(errNode.startPoint, source[errNode.startByte:closeStartByte])
	open := newLeafNodeInArena(arena, openSym, false, errNode.startByte, openEndByte, errNode.startPoint, openEndPoint)
	close := newLeafNodeInArena(arena, closeSym, false, closeStartByte, errNode.endByte, closeStartPoint, errNode.endPoint)
	children := phpAllocChildren(arena, 2)
	children[0] = open
	children[1] = close
	return newParentNodeInArena(arena, compoundSym, compoundNamed, children, nil, 0), true
}

func phpSyntheticFieldIDs(arena *nodeArena, childCount int, lang *Language, byIndex map[int]string) []FieldID {
	fieldIDs := make([]FieldID, childCount)
	if arena != nil {
		fieldIDs = arena.allocFieldIDSlice(childCount)
	}
	for idx, name := range byIndex {
		if idx < 0 || idx >= childCount {
			continue
		}
		if fid, ok := lang.FieldByName(name); ok {
			fieldIDs[idx] = fid
		}
	}
	return fieldIDs
}

func phpAllocChildren(arena *nodeArena, n int) []*Node {
	if arena != nil {
		return arena.allocNodeSlice(n)
	}
	return make([]*Node, n)
}

func phpSymbolMeta(lang *Language, name string) (Symbol, bool, bool) {
	if lang == nil {
		return 0, false, false
	}
	sym, ok := lang.SymbolByName(name)
	if !ok {
		return 0, false, false
	}
	named := false
	if idx := int(sym); idx < len(lang.SymbolMetadata) {
		named = lang.SymbolMetadata[sym].Named
	}
	return sym, named, true
}

func phpCountsAsPriorTopLevelNode(n *Node, lang *Language) bool {
	return n != nil && !n.isExtra && (lang == nil || n.Type(lang) != "php_tag")
}

func assignPHPTopLevelFragmentFields(root *Node, lang *Language, arena *nodeArena) {
	if root == nil || lang == nil || lang.Name != "php" || len(root.children) == 0 {
		return
	}
	var fieldIDs []FieldID
	var fieldSources []uint8
	for i := 0; i+6 < len(root.children); i++ {
		if root.children[i] == nil || root.children[i+1] == nil || root.children[i+2] == nil || root.children[i+3] == nil || root.children[i+4] == nil || root.children[i+6] == nil {
			continue
		}
		if root.children[i].Type(lang) != "static_modifier" ||
			root.children[i+1].Type(lang) != "function" ||
			root.children[i+2].Type(lang) != "formal_parameters" ||
			root.children[i+3].Type(lang) != "{" ||
			root.children[i+4].Type(lang) != "ERROR" ||
			root.children[i+6].Type(lang) != "expression_statement" {
			continue
		}
		if fieldIDs == nil {
			if arena != nil {
				fieldIDs = arena.allocFieldIDSlice(len(root.children))
				fieldSources = make([]uint8, len(root.children))
			} else {
				fieldIDs = make([]FieldID, len(root.children))
				fieldSources = make([]uint8, len(root.children))
			}
		}
		if fid, ok := lang.FieldByName("static_modifier"); ok {
			fieldIDs[i] = fid
			fieldSources[i] = fieldSourceDirect
		}
		if fid, ok := lang.FieldByName("parameters"); ok {
			fieldIDs[i+2] = fid
			fieldSources[i+2] = fieldSourceDirect
		}
	}
	if fieldIDs != nil {
		root.fieldIDs = fieldIDs
		root.fieldSources = fieldSources
	}
}

func phpReparsedTopLevelSuffix(source []byte, start uint32, lang *Language, arena *nodeArena) ([]*Node, bool) {
	if lang == nil || lang.Name != "php" || int(start) >= len(source) {
		return nil, false
	}
	start = phpSkipLeadingLayout(source, start)
	if int(start) >= len(source) {
		return nil, false
	}
	const prefix = "<?php\n"
	wrapped := make([]byte, 0, len(prefix)+len(source)-int(start))
	wrapped = append(wrapped, prefix...)
	wrapped = append(wrapped, source[start:]...)
	tree, err := parseWithSnippetParser(lang, wrapped)
	if err != nil || tree == nil || tree.RootNode() == nil {
		return nil, false
	}
	defer tree.Release()
	startPoint := advancePointByBytes(Point{}, source[:start])
	prefixPoint := advancePointByBytes(Point{}, []byte(prefix))
	if start < uint32(len(prefix)) || startPoint.Row < prefixPoint.Row {
		return nil, false
	}
	offsetRoot := tree.RootNodeWithOffset(
		start-uint32(len(prefix)),
		Point{Row: startPoint.Row - prefixPoint.Row, Column: startPoint.Column},
	)
	if offsetRoot == nil || len(offsetRoot.children) == 0 {
		return nil, false
	}
	out := make([]*Node, 0, len(offsetRoot.children))
	for _, child := range offsetRoot.children {
		if child == nil || child.Type(lang) == "php_tag" {
			continue
		}
		out = append(out, cloneTreeNodesIntoArena(child, arena))
	}
	return out, len(out) > 0
}

func phpSkipLeadingLayout(source []byte, start uint32) uint32 {
	for int(start) < len(source) {
		switch source[start] {
		case ' ', '\t', '\n', '\r':
			start++
		default:
			return start
		}
	}
	return start
}

func bytesContainLineBreak(b []byte) bool {
	for _, c := range b {
		if c == '\n' || c == '\r' {
			return true
		}
	}
	return false
}

func firstNonWhitespaceByte(source []byte) uint32 {
	for i, c := range source {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return uint32(i)
		}
	}
	return 0
}

func normalizeCobolLeadingAreaStart(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || (lang.Name != "cobol" && lang.Name != "COBOL") || len(source) == 0 {
		return
	}
	start := firstNonWhitespaceByte(source)
	if start == 0 {
		// COBOL fixed format: columns 1-6 are sequence numbers (non-whitespace).
		// Detect this pattern and use column 7 (byte 6) as the adjusted start.
		if len(source) >= 7 && (source[6] == ' ' || source[6] == '*' || source[6] == '-' || source[6] == '/') {
			start = 6
		} else {
			return
		}
	}
	startPoint := advancePointByBytes(Point{}, source[:start])
	setNodeStartTo := func(n *Node) {
		if n == nil || n.startByte == start {
			return
		}
		n.startByte = start
		n.startPoint = startPoint
	}
	setNodeStartTo(root)
	if len(root.children) == 0 {
		return
	}
	def := (*Node)(nil)
	for _, child := range root.children {
		if child != nil && !child.IsExtra() && child.Type(lang) == "program_definition" {
			def = child
			break
		}
	}
	if def == nil {
		return
	}
	setNodeStartTo(def)
	if len(def.children) == 0 {
		return
	}
	for _, child := range def.children {
		if child != nil && !child.IsExtra() && child.Type(lang) == "identification_division" {
			setNodeStartTo(child)
			break
		}
	}
}

func normalizeCobolTopLevelDefinitionEnd(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || (lang.Name != "cobol" && lang.Name != "COBOL") || root.Type(lang) != "start" || len(root.children) == 0 {
		return
	}
	def := (*Node)(nil)
	for _, child := range root.children {
		if child != nil && !child.IsExtra() && child.Type(lang) == "program_definition" {
			def = child
			break
		}
	}
	if def == nil {
		return
	}
	end := lastNonTriviaByteEnd(source)
	if end == 0 || end >= def.endByte {
		return
	}
	def.endByte = end
	def.endPoint = advancePointByBytes(Point{}, source[:end])
}

func normalizeCobolDivisionSiblingEnds(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || (lang.Name != "cobol" && lang.Name != "COBOL") || root.Type(lang) != "start" || len(root.children) == 0 {
		return
	}
	def := (*Node)(nil)
	for _, child := range root.children {
		if child != nil && !child.IsExtra() && child.Type(lang) == "program_definition" {
			def = child
			break
		}
	}
	if def == nil {
		return
	}
	for i := 0; i+1 < len(def.children); i++ {
		cur := def.children[i]
		next := def.children[i+1]
		if cur == nil || next == nil || cur.IsExtra() || next.IsExtra() {
			continue
		}
		if !strings.HasSuffix(cur.Type(lang), "_division") {
			continue
		}
		end := lastNonTriviaByteEnd(source[:next.startByte])
		if end == 0 || end <= cur.startByte || end >= cur.endByte {
			continue
		}
		cur.endByte = end
		cur.endPoint = advancePointByBytes(Point{}, source[:end])
	}
}

func normalizeCobolPeriodChildren(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || (lang.Name != "cobol" && lang.Name != "COBOL") {
		return
	}
	normalizeCollapsedNamedLeafChildren(root, lang, "period", ".")
}

// normalizeCollapsedNamedLeafChildren restores collapsed single-anonymous-child
// nodes. When a named node (parentName) wraps a single anonymous token
// (childName) and the collapse logic strips the child, this function
// reconstructs the child so the tree matches C tree-sitter output.
func normalizeCollapsedNamedLeafChildren(root *Node, lang *Language, parentName, childName string) {
	if root == nil || lang == nil {
		return
	}
	parentSym, ok := symbolByName(lang, parentName)
	if !ok {
		return
	}
	childSym, childOk := symbolByName(lang, childName)
	if !childOk {
		return
	}
	childNamed := false
	if int(childSym) < len(lang.SymbolMetadata) {
		childNamed = lang.SymbolMetadata[childSym].Named
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.symbol == parentSym && len(n.children) == 0 {
			child := newLeafNodeInArena(n.ownerArena, childSym, childNamed, n.startByte, n.endByte, n.startPoint, n.endPoint)
			n.children = cloneNodeSliceInArena(n.ownerArena, []*Node{child})
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeNimTopLevelCallEnd(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "nim" || root.Type(lang) != "source_file" || len(root.children) != 1 {
		return
	}
	call := root.children[0]
	if call == nil || call.IsExtra() || call.Type(lang) != "call" {
		return
	}
	end := lastNonTriviaByteEnd(source)
	if end == 0 || end >= call.endByte {
		return
	}
	call.endByte = end
	call.endPoint = advancePointByBytes(Point{}, source[:end])
}

func normalizePascalTopLevelProgramEnd(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "pascal" || root.Type(lang) != "root" || len(root.children) == 0 {
		return
	}
	program := root.children[0]
	if program == nil || program.IsExtra() || program.Type(lang) != "program" {
		return
	}
	end := lastNonTriviaByteEnd(source)
	if end == 0 || end >= program.endByte {
		return
	}
	program.endByte = end
	program.endPoint = advancePointByBytes(Point{}, source[:end])
}

func normalizeCommentTrailingExtraTrivia(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "comment" || root.Type(lang) != "source" {
		return
	}
	trimTrailingExtraTriviaRoot(root, source)
}

func normalizePascalTrailingExtraTrivia(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "pascal" || root.Type(lang) != "root" {
		return
	}
	trimTrailingExtraTriviaRoot(root, source)
}

func normalizeRSTTopLevelSectionEnd(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "rst" || root.Type(lang) != "document" || len(root.children) == 0 {
		return
	}
	trimTrailingExtraTriviaRoot(root, source)
	section := root.children[0]
	if section == nil || section.IsExtra() || section.Type(lang) != "section" {
		return
	}
	end := lastNonTriviaByteEnd(source)
	if end == 0 || end >= section.endByte {
		return
	}
	section.endByte = end
	section.endPoint = advancePointByBytes(Point{}, source[:end])
}

func bytesAreCooklangStepTail(b []byte) bool {
	sawPunctuation := false
	for _, c := range b {
		switch c {
		case '.', '!', '?':
			if sawPunctuation {
				return false
			}
			sawPunctuation = true
		case ' ', '\t', '\n', '\r':
		default:
			return false
		}
	}
	return sawPunctuation
}

func normalizeDModuleDefinitionBounds(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "d" {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "module_def" {
			if first := pythonBlockStartAnchor(n.children, lang); first != nil && n.startByte < first.startByte {
				n.startByte = first.startByte
				n.startPoint = first.startPoint
			}
			if last := pythonBlockEndAnchor(n.children); last != nil && n.endByte > last.endByte {
				n.endByte = last.endByte
				n.endPoint = last.endPoint
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeDSourceFileLeadingTrivia(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "d" || root.Type(lang) != "source_file" || len(root.children) == 0 {
		return
	}
	first := root.children[0]
	if first == nil || root.startByte >= first.startByte || int(first.startByte) > len(source) {
		return
	}
	if !bytesAreTrivia(source[root.startByte:first.startByte]) {
		return
	}
	root.startByte = first.startByte
	root.startPoint = first.startPoint
}

func normalizeDVariableStorageClassWrappers(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "d" {
		return
	}
	storageClassSym, ok := lang.SymbolByName("storage_class")
	if !ok {
		return
	}
	storageClassNamed := false
	if idx := int(storageClassSym); idx < len(lang.SymbolMetadata) {
		storageClassNamed = lang.SymbolMetadata[storageClassSym].Named
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "variable_declaration" {
			for i, child := range n.children {
				if child == nil || child.Type(lang) != "static" {
					continue
				}
				wrapper := newParentNodeInArena(n.ownerArena, storageClassSym, storageClassNamed, []*Node{child}, nil, 0)
				n.children[i] = wrapper
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeDCallExpressionTemplateTypes(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "d" {
		return
	}
	typeSym, ok := lang.SymbolByName("type")
	if !ok {
		return
	}
	typeNamed := false
	if idx := int(typeSym); idx < len(lang.SymbolMetadata) {
		typeNamed = lang.SymbolMetadata[typeSym].Named
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "call_expression" && len(n.children) > 0 {
			child := n.children[0]
			if child != nil && child.Type(lang) == "template_instance" {
				n.children[0] = newParentNodeInArena(n.ownerArena, typeSym, typeNamed, []*Node{child}, nil, 0)
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeDVariableTypeQualifiers(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "d" {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "variable_declaration" && len(n.children) >= 3 {
			for i := 0; i+1 < len(n.children); i++ {
				left := n.children[i]
				right := n.children[i+1]
				if left == nil || right == nil || left.Type(lang) != "storage_class" || right.Type(lang) != "type" {
					continue
				}
				if len(left.children) != 1 || left.children[0] == nil || left.children[0].Type(lang) != "type_ctor" {
					continue
				}
				mergedType := cloneNodeInArena(n.ownerArena, right)
				mergedChildren := make([]*Node, 0, 1+len(right.children))
				mergedChildren = append(mergedChildren, left.children[0])
				mergedChildren = append(mergedChildren, right.children...)
				if n.ownerArena != nil {
					buf := n.ownerArena.allocNodeSlice(len(mergedChildren))
					copy(buf, mergedChildren)
					mergedChildren = buf
				}
				mergedType.children = mergedChildren
				mergedType.startByte = mergedChildren[0].startByte
				mergedType.startPoint = mergedChildren[0].startPoint
				n.children[i+1] = mergedType
				n.children = append(n.children[:i], n.children[i+1:]...)
				break
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeDCallExpressionPropertyTypes(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "d" {
		return
	}
	typeSym, ok := lang.SymbolByName("type")
	if !ok {
		return
	}
	typeNamed := false
	if idx := int(typeSym); idx < len(lang.SymbolMetadata) {
		typeNamed = lang.SymbolMetadata[typeSym].Named
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "call_expression" && len(n.children) > 0 {
			child := n.children[0]
			if parts, ok := flattenDPropertyTypeChain(child, lang); ok {
				if n.ownerArena != nil {
					buf := n.ownerArena.allocNodeSlice(len(parts))
					copy(buf, parts)
					parts = buf
				}
				n.children[0] = newParentNodeInArena(n.ownerArena, typeSym, typeNamed, parts, nil, 0)
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeDCallExpressionSimpleTypeCallees(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "d" {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "call_expression" && len(n.children) > 0 {
			child := n.children[0]
			if child != nil && child.Type(lang) == "type" && len(child.children) == 1 && child.children[0] != nil && child.children[0].Type(lang) == "identifier" {
				n.children[0] = child.children[0]
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeCooklangTrailingStepTail(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "cooklang" || len(source) == 0 {
		return
	}
	if root.Type(lang) != "recipe" || len(root.children) != 1 {
		return
	}
	step := root.children[0]
	if step == nil || step.Type(lang) != "step" || step.endByte >= uint32(len(source)) {
		return
	}
	tail := source[step.endByte:]
	if !bytesAreCooklangStepTail(tail) {
		return
	}
	stepEnd := step.endByte
	for i := int(step.endByte); i < len(source); i++ {
		switch source[i] {
		case '.', '!', '?':
			stepEnd = uint32(i + 1)
		}
	}
	if stepEnd > step.endByte {
		extendNodeEndTo(step, stepEnd, source)
	}
	if root.endByte < uint32(len(source)) {
		extendNodeEndTo(root, uint32(len(source)), source)
	}
}

func lineBreakEndAt(source []byte, start, limit uint32) uint32 {
	if start >= limit || start >= uint32(len(source)) {
		return 0
	}
	switch source[start] {
	case '\n':
		return start + 1
	case '\r':
		if start+1 < limit && start+1 < uint32(len(source)) && source[start+1] == '\n' {
			return start + 2
		}
		return start + 1
	default:
		return 0
	}
}

func normalizeFortranStatementLineBreaks(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "fortran" || len(source) == 0 {
		return
	}
	var walk func(*Node, int)
	walk = func(n *Node, depth int) {
		if n == nil || depth > maxTreeWalkDepth {
			return
		}
		if n.Type(lang) == "program" {
			for i := 0; i+1 < len(n.children); i++ {
				cur := n.children[i]
				next := n.children[i+1]
				if cur == nil || next == nil || cur.endByte >= next.startByte {
					continue
				}
				if cur.Type(lang) != "program_statement" {
					continue
				}
				if end := lineBreakEndAt(source, cur.endByte, next.startByte); end > cur.endByte {
					extendNodeEndTo(cur, end, source)
				}
			}
		}
		for _, child := range n.children {
			walk(child, depth+1)
		}
	}
	walk(root, 0)
}

func normalizeNginxAttributeLineBreaks(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "nginx" || len(source) == 0 {
		return
	}
	var walk func(*Node, int)
	walk = func(n *Node, depth int) {
		if n == nil || depth > maxTreeWalkDepth {
			return
		}
		if n.Type(lang) == "attribute" {
			if end := lineBreakEndAt(source, n.endByte, uint32(len(source))); end > n.endByte {
				extendNodeEndTo(n, end, source)
			}
		}
		for _, child := range n.children {
			walk(child, depth+1)
		}
	}
	walk(root, 0)
}

func normalizeTopLevelTrailingLineBreakSpan(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || len(source) == 0 {
		return
	}
	switch lang.Name {
	case "caddy", "fortran", "pug":
	default:
		return
	}
	if len(root.children) != 1 {
		return
	}
	child := root.children[0]
	if child == nil || child.endByte >= root.endByte || root.endByte > uint32(len(source)) {
		return
	}
	gap := source[child.endByte:root.endByte]
	if !bytesAreTrivia(gap) || !bytesContainLineBreak(gap) {
		return
	}
	extendNodeEndTo(child, root.endByte, source)
}

func normalizeRootEOFNewlineSpan(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || len(source) == 0 || root.endByte >= uint32(len(source)) {
		return
	}
	switch {
	case lang.Name == "go" && root.Type(lang) == "source_file":
	case lang.Name == "scala" && root.Type(lang) == "compilation_unit":
	default:
		return
	}
	gap := source[root.endByte:]
	if !bytesAreTrivia(gap) || !bytesContainLineBreak(gap) {
		return
	}
	extendNodeEndTo(root, uint32(len(source)), source)
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

func normalizeHaskellZeroWidthTokens(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "haskell" || len(root.children) == 0 {
		return
	}
	filtered := root.children[:0]
	for _, child := range root.children {
		if child == nil {
			continue
		}
		if child.Type(lang) == "_token1" && child.startByte == child.endByte {
			continue
		}
		filtered = append(filtered, child)
	}
	root.children = filtered
}

func normalizeHaskellRootImportField(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "haskell" || len(root.children) == 0 {
		return
	}
	if len(lang.FieldNames) == 0 {
		return
	}
	for i, child := range root.children {
		if child == nil {
			continue
		}
		fid := FieldID(0)
		for j, name := range lang.FieldNames {
			if name == child.Type(lang) {
				fid = FieldID(j)
				break
			}
		}
		if fid == 0 {
			continue
		}
		if len(root.fieldIDs) < len(root.children) {
			fieldIDs := make([]FieldID, len(root.children))
			copy(fieldIDs, root.fieldIDs)
			root.fieldIDs = fieldIDs
		}
		if len(root.fieldSources) < len(root.children) {
			fieldSources := make([]uint8, len(root.children))
			copy(fieldSources, root.fieldSources)
			root.fieldSources = fieldSources
		}
		root.fieldIDs[i] = fid
		root.fieldSources[i] = fieldSourceInherited
	}
}

func normalizeHaskellDeclarationsSpan(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "haskell" || len(source) == 0 {
		return
	}
	for _, child := range root.children {
		if child == nil || child.Type(lang) != "declarations" {
			continue
		}
		if child.endByte >= root.endByte || root.endByte > uint32(len(source)) {
			continue
		}
		gap := source[child.endByte:root.endByte]
		if !bytesAreTrivia(gap) {
			continue
		}
		extendNodeEndTo(child, root.endByte, source)
	}
}

func normalizeHaskellLocalBindsStarts(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "haskell" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "let_in" && len(n.children) >= 2 {
			letNode := n.children[0]
			localBinds := n.children[1]
			if letNode != nil && localBinds != nil && letNode.Type(lang) == "let" && localBinds.Type(lang) == "local_binds" && letNode.endByte < localBinds.startByte && localBinds.startByte <= uint32(len(source)) {
				gap := source[letNode.endByte:localBinds.startByte]
				if len(gap) > 0 && bytesAreTrivia(gap) && !bytesContainLineBreak(gap) {
					localBinds.startByte = letNode.endByte
					localBinds.startPoint = letNode.endPoint
				}
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeHaskellQuasiquoteStarts(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "haskell" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "quasiquote" && n.startByte > 0 {
			start := int(n.startByte)
			if source[start-1] == ' ' && start < len(source) && source[start] == '[' {
				n.startByte--
				if n.startPoint.Column > 0 {
					n.startPoint.Column--
				} else if n.startPoint.Row > 0 {
					n.startPoint = advancePointByBytes(Point{}, source[:n.startByte])
				}
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeJavaScriptTopLevelObjectLiterals(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "javascript" || root.Type(lang) != "program" {
		return
	}
	exprSym, exprNamed, ok := javaScriptSymbolMeta(lang, "expression_statement")
	if !ok {
		return
	}
	objectSym, objectNamed, ok := javaScriptSymbolMeta(lang, "object")
	if !ok {
		return
	}
	pairSym, pairNamed, ok := javaScriptSymbolMeta(lang, "pair")
	if !ok {
		return
	}
	propSym, _, ok := javaScriptSymbolMeta(lang, "property_identifier")
	if !ok {
		return
	}
	for i, child := range root.children {
		repl, ok := rewriteJavaScriptTopLevelObjectLiteral(child, lang, root.ownerArena, exprSym, exprNamed, objectSym, objectNamed, pairSym, pairNamed, propSym)
		if ok {
			root.children[i] = repl
		}
	}
}

func normalizeJavaScriptProgramStart(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "javascript" || root.Type(lang) != "program" {
		return
	}
	first, _ := firstAndLastNonNilChild(root.children)
	if first == nil {
		return
	}
	root.startByte = first.startByte
	root.startPoint = first.startPoint
}

func normalizeRubyTopLevelModuleBounds(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "ruby" || root.Type(lang) != "program" || len(source) == 0 {
		return
	}
	end := lastNonTriviaByteEnd(source)
	for _, child := range root.children {
		if child == nil || child.IsExtra() || child.Type(lang) != "module" {
			continue
		}
		if len(child.children) > 0 && child.children[0] != nil && child.startByte < child.children[0].startByte {
			child.startByte = child.children[0].startByte
			child.startPoint = child.children[0].startPoint
		}
		if child.endByte == root.endByte && end > child.startByte && end < child.endByte {
			child.endByte = end
			child.endPoint = advancePointByBytes(Point{}, source[:end])
		}
	}
}

func normalizeRubyThenStarts(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "ruby" {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		switch n.Type(lang) {
		case "elsif", "if", "unless", "when":
			normalizeRubyThenChildStarts(n, lang)
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeRubyThenChildStarts(parent *Node, lang *Language) {
	if parent == nil || lang == nil || len(parent.children) < 2 {
		return
	}
	for i, child := range parent.children {
		if child == nil || child.Type(lang) != "then" || i == 0 {
			continue
		}
		prev := (*Node)(nil)
		for j := i - 1; j >= 0; j-- {
			if parent.children[j] != nil {
				prev = parent.children[j]
				break
			}
		}
		if prev == nil || prev.endByte >= child.startByte {
			continue
		}
		child.startByte = prev.endByte
		child.startPoint = prev.endPoint
	}
}

func normalizeJavaScriptTopLevelExpressionStatementBounds(root *Node, lang *Language) {
	if root == nil || lang == nil || root.Type(lang) != "program" {
		return
	}
	switch lang.Name {
	case "javascript", "typescript", "tsx":
	default:
		return
	}
	for _, child := range root.children {
		if child == nil || child.Type(lang) != "expression_statement" || len(child.children) == 0 {
			continue
		}
		first, last := firstAndLastNonNilChild(child.children)
		if first == nil || last == nil {
			continue
		}
		child.startByte = first.startByte
		child.startPoint = first.startPoint
		child.endByte = last.endByte
		child.endPoint = last.endPoint
	}
}

func normalizeJavaScriptTrailingContinueComments(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "javascript" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		normalizeJavaScriptTrailingContinueCommentSiblings(n, source, lang)
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeJavaScriptTrailingContinueCommentSiblings(parent *Node, source []byte, lang *Language) {
	if parent == nil || len(parent.children) < 3 || parent.Type(lang) != "statement_block" {
		return
	}
	for i := 1; i+1 < len(parent.children); i++ {
		if comment, ok := extractJavaScriptTrailingContinueComment(parent.children[i], source, lang); ok {
			insertJavaScriptStatementBlockComment(parent, i, comment)
			i++
			continue
		}
		stmt := parent.children[i]
		if stmt == nil || stmt.Type(lang) != "if_statement" || len(stmt.children) < 3 {
			continue
		}
		branch := stmt.children[len(stmt.children)-1]
		comment, ok := extractJavaScriptTrailingContinueComment(branch, source, lang)
		if !ok {
			continue
		}
		stmt.endByte = branch.endByte
		stmt.endPoint = branch.endPoint
		insertJavaScriptStatementBlockComment(parent, i, comment)
		i++
	}
}

func extractJavaScriptTrailingContinueComment(node *Node, source []byte, lang *Language) (*Node, bool) {
	if node == nil || lang == nil || node.Type(lang) != "continue_statement" || len(node.children) < 3 {
		return nil, false
	}
	comment := node.children[len(node.children)-1]
	if comment == nil || comment.Type(lang) != "comment" || comment.startByte >= comment.endByte {
		return nil, false
	}
	if int(comment.endByte) > len(source) || !bytes.HasPrefix(source[comment.startByte:comment.endByte], []byte("//")) {
		return nil, false
	}
	prev := node.children[len(node.children)-2]
	if prev == nil || prev.endByte > comment.startByte || bytesContainLineBreak(source[prev.endByte:comment.startByte]) {
		return nil, false
	}
	node.children = node.children[:len(node.children)-1]
	if len(node.fieldIDs) > len(node.children) {
		node.fieldIDs = node.fieldIDs[:len(node.children)]
		if len(node.fieldSources) > len(node.children) {
			node.fieldSources = node.fieldSources[:len(node.children)]
		}
	}
	node.endByte = prev.endByte
	node.endPoint = prev.endPoint
	return comment, true
}

func insertJavaScriptStatementBlockComment(parent *Node, childIdx int, comment *Node) {
	if parent == nil || comment == nil || childIdx < 0 || childIdx >= len(parent.children) {
		return
	}
	parent.children = append(parent.children[:childIdx+1], append([]*Node{comment}, parent.children[childIdx+1:]...)...)
	if len(parent.fieldIDs) > 0 {
		fieldIDs := append([]FieldID(nil), parent.fieldIDs[:childIdx+1]...)
		fieldIDs = append(fieldIDs, 0)
		fieldIDs = append(fieldIDs, parent.fieldIDs[childIdx+1:]...)
		parent.fieldIDs = fieldIDs
		if len(parent.fieldSources) > 0 {
			fieldSources := append([]uint8(nil), parent.fieldSources[:childIdx+1]...)
			fieldSources = append(fieldSources, fieldSourceNone)
			fieldSources = append(fieldSources, parent.fieldSources[childIdx+1:]...)
			parent.fieldSources = fieldSources
		}
	}
	populateParentNode(parent, parent.children)
}

func normalizeJavaScriptTypeScriptOptionalChainLeaves(root *Node, lang *Language) {
	if root == nil || lang == nil {
		return
	}
	switch lang.Name {
	case "javascript", "typescript", "tsx":
	default:
		return
	}

	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "optional_chain" && len(n.children) == 1 {
			child := n.children[0]
			if child != nil && !child.IsNamed() && !child.IsExtra() &&
				child.startByte == n.startByte && child.endByte == n.endByte &&
				child.startPoint == n.startPoint && child.endPoint == n.endPoint {
				n.children = nil
				n.fieldIDs = nil
				n.fieldSources = nil
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeJavaScriptTypeScriptCallPrecedence(root *Node, lang *Language) {
	if root == nil || lang == nil {
		return
	}
	switch lang.Name {
	case "javascript", "typescript", "tsx":
	default:
		return
	}

	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		for i, child := range n.children {
			if rewritten := rewriteJavaScriptTypeScriptCallPrecedence(child, lang); rewritten != nil {
				n.children[i] = rewritten
				rewritten.parent = n
				rewritten.childIndex = i
				child = rewritten
			}
			walk(child)
		}
	}
	walk(root)
}

func rewriteJavaScriptTypeScriptCallPrecedence(node *Node, lang *Language) *Node {
	if node == nil || lang == nil || node.Type(lang) != "call_expression" || len(node.children) != 2 {
		return nil
	}
	function := node.children[0]
	arguments := node.children[1]
	if function == nil || arguments == nil {
		return nil
	}
	return rewriteJavaScriptTypeScriptCallTarget(function, arguments, node, lang)
}

func rewriteJavaScriptTypeScriptCallTarget(target, arguments, callNode *Node, lang *Language) *Node {
	if target == nil || arguments == nil || callNode == nil || lang == nil {
		return nil
	}
	if isJavaScriptTypeScriptCallableShape(target, lang) {
		rewrittenCall := cloneNodeInArena(callNode.ownerArena, callNode)
		rewrittenCall.children = cloneNodeSliceInArena(callNode.ownerArena, []*Node{target, arguments})
		populateParentNode(rewrittenCall, rewrittenCall.children)
		return rewrittenCall
	}

	switch target.Type(lang) {
	case "unary_expression":
		if len(target.children) < 2 {
			return nil
		}
		operandIdx := len(target.children) - 1
		rewrittenOperand := rewriteJavaScriptTypeScriptCallTarget(target.children[operandIdx], arguments, callNode, lang)
		if rewrittenOperand == nil {
			return nil
		}
		rewrittenUnary := cloneNodeInArena(callNode.ownerArena, target)
		unaryChildren := cloneNodeSliceInArena(callNode.ownerArena, target.children)
		unaryChildren[operandIdx] = rewrittenOperand
		rewrittenUnary.children = unaryChildren
		populateParentNode(rewrittenUnary, rewrittenUnary.children)
		return rewrittenUnary
	case "binary_expression":
		operator, rightIdx, ok := javaScriptTypeScriptBinaryOperatorAndRight(target, lang)
		if !ok || rightIdx < 0 || rightIdx >= len(target.children) {
			return nil
		}
		if operator == nil {
			return nil
		}
		if _, ok := javaScriptTypeScriptBinaryOperatorPrecedence(operator.Type(lang)); !ok {
			return nil
		}
		rewrittenRight := rewriteJavaScriptTypeScriptCallTarget(target.children[rightIdx], arguments, callNode, lang)
		if rewrittenRight == nil {
			return nil
		}
		rewrittenBinary := cloneNodeInArena(callNode.ownerArena, target)
		binaryChildren := cloneNodeSliceInArena(callNode.ownerArena, target.children)
		binaryChildren[rightIdx] = rewrittenRight
		rewrittenBinary.children = binaryChildren
		populateParentNode(rewrittenBinary, rewrittenBinary.children)
		return rewrittenBinary
	default:
		return nil
	}
}

func javaScriptTypeScriptBinaryOperatorAndRight(node *Node, lang *Language) (*Node, int, bool) {
	if node == nil || lang == nil || node.Type(lang) != "binary_expression" || len(node.children) < 3 {
		return nil, -1, false
	}
	operatorIdx := -1
	rightIdx := -1
	for i := 0; i < len(node.children); i++ {
		switch node.FieldNameForChild(i, lang) {
		case "operator":
			operatorIdx = i
		case "right":
			rightIdx = i
		}
	}
	if operatorIdx < 0 && len(node.children) >= 2 {
		operatorIdx = 1
	}
	if rightIdx < 0 {
		for i := len(node.children) - 1; i >= 0; i-- {
			child := node.children[i]
			if child == nil || child.isExtra {
				continue
			}
			if i != operatorIdx {
				rightIdx = i
				break
			}
		}
	}
	if operatorIdx < 0 || rightIdx < 0 || operatorIdx >= len(node.children) {
		return nil, -1, false
	}
	return node.children[operatorIdx], rightIdx, true
}

func isJavaScriptTypeScriptCallableShape(node *Node, lang *Language) bool {
	if node == nil || lang == nil {
		return false
	}
	switch node.Type(lang) {
	case "identifier", "member_expression", "subscript_expression", "call_expression", "parenthesized_expression":
		return true
	default:
		return false
	}
}

func cloneNodeSliceInArena(arena *nodeArena, nodes []*Node) []*Node {
	if len(nodes) == 0 {
		return nil
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(nodes))
		copy(buf, nodes)
		return buf
	}
	buf := make([]*Node, len(nodes))
	copy(buf, nodes)
	return buf
}

func normalizeJavaScriptTypeScriptUnaryPrecedence(root *Node, lang *Language) {
	if root == nil || lang == nil {
		return
	}
	switch lang.Name {
	case "javascript", "typescript", "tsx":
	default:
		return
	}

	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		for i, child := range n.children {
			walk(child)
			for {
				rewritten := rewriteJavaScriptTypeScriptUnaryPrecedence(child, lang)
				if rewritten == nil {
					break
				}
				n.children[i] = rewritten
				rewritten.parent = n
				rewritten.childIndex = i
				child = rewritten
			}
		}
	}
	walk(root)
}

func rewriteJavaScriptTypeScriptUnaryPrecedence(node *Node, lang *Language) *Node {
	if node == nil || lang == nil || node.Type(lang) != "unary_expression" || len(node.children) < 2 {
		return nil
	}
	operandIdx := len(node.children) - 1
	operand := node.children[operandIdx]
	if operand == nil || operand.Type(lang) != "binary_expression" || len(operand.children) != 3 {
		return nil
	}
	if _, ok := javaScriptTypeScriptBinaryOperatorPrecedence(operand.children[1].Type(lang)); !ok {
		return nil
	}

	rewrittenUnary := cloneNodeInArena(node.ownerArena, node)
	unaryChildren := cloneNodeSliceInArena(node.ownerArena, node.children)
	unaryChildren[operandIdx] = operand.children[0]
	rewrittenUnary.children = unaryChildren
	populateParentNode(rewrittenUnary, rewrittenUnary.children)

	rewrittenBinary := cloneNodeInArena(node.ownerArena, operand)
	binaryChildren := cloneNodeSliceInArena(node.ownerArena, operand.children)
	binaryChildren[0] = rewrittenUnary
	rewrittenBinary.children = binaryChildren
	populateParentNode(rewrittenBinary, rewrittenBinary.children)
	return rewrittenBinary
}

func normalizeJavaScriptTypeScriptBinaryPrecedence(root *Node, lang *Language) {
	if root == nil || lang == nil {
		return
	}
	switch lang.Name {
	case "javascript", "typescript", "tsx":
	default:
		return
	}

	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		for i, child := range n.children {
			walk(child)
			for {
				rewritten := rewriteJavaScriptTypeScriptBinaryPrecedence(child, lang)
				if rewritten == nil {
					break
				}
				n.children[i] = rewritten
				rewritten.parent = n
				rewritten.childIndex = i
				child = rewritten
			}
		}
	}
	walk(root)
}

func rewriteJavaScriptTypeScriptBinaryPrecedence(node *Node, lang *Language) *Node {
	if node == nil || lang == nil || node.Type(lang) != "binary_expression" || len(node.children) != 3 {
		return nil
	}
	left := node.children[0]
	op := node.children[1]
	right := node.children[2]
	if left == nil || op == nil || right == nil || left.Type(lang) != "binary_expression" || len(left.children) != 3 {
		return nil
	}
	leftOp := left.children[1]
	if leftOp == nil {
		return nil
	}

	parentPrec, ok := javaScriptTypeScriptBinaryOperatorPrecedence(op.Type(lang))
	if !ok {
		return nil
	}
	leftPrec, ok := javaScriptTypeScriptBinaryOperatorPrecedence(leftOp.Type(lang))
	if !ok || parentPrec <= leftPrec {
		return nil
	}

	rotatedInner := cloneNodeInArena(node.ownerArena, node)
	rotatedInner.children = cloneNodeSliceInArena(node.ownerArena, []*Node{left.children[2], op, right})
	populateParentNode(rotatedInner, rotatedInner.children)

	rotatedOuter := cloneNodeInArena(node.ownerArena, left)
	rotatedOuter.children = cloneNodeSliceInArena(node.ownerArena, []*Node{left.children[0], leftOp, rotatedInner})
	populateParentNode(rotatedOuter, rotatedOuter.children)
	return rotatedOuter
}

func javaScriptTypeScriptBinaryOperatorPrecedence(op string) (int, bool) {
	switch op {
	case "??":
		return 1, true
	case "||":
		return 2, true
	case "&&":
		return 3, true
	case "|":
		return 4, true
	case "^":
		return 5, true
	case "&":
		return 6, true
	case "==", "!=", "===", "!==":
		return 7, true
	case "<", "<=", ">", ">=", "instanceof", "in":
		return 8, true
	case "<<", ">>", ">>>":
		return 9, true
	case "+", "-":
		return 10, true
	case "*", "/", "%":
		return 11, true
	case "**":
		return 12, true
	default:
		return 0, false
	}
}

type typeScriptNormalizationContext struct {
	source []byte
	lang   *Language

	canRewriteGenericCalls      bool
	canRewriteInstantiatedCalls bool
	canRewriteAsExpressions     bool
	canClearEnumBodyFields      bool

	callSym                Symbol
	callNamed              bool
	instantiationExprSym   Symbol
	instantiationExprNamed bool
	typeArgsSym            Symbol
	typeArgsNamed          bool
	argsSym                Symbol
	argsNamed              bool
	predefinedTypeSym      Symbol
	predefinedTypeNamed    bool
	asExpressionSym        Symbol
	asExpressionNamed      bool
	functionFieldID        FieldID
	typeArgsFieldID        FieldID
	argumentsFieldID       FieldID
	binaryExpressionSym    Symbol
	assignmentExprSym      Symbol
	assignmentExprNamed    bool
	ternaryExprSym         Symbol
	ternaryExprNamed       bool
	unionTypeSym           Symbol
	unionTypeNamed         bool
	intersectionTypeSym    Symbol
	intersectionTypeNamed  bool
	objectTypeSym          Symbol
	objectTypeNamed        bool
	propertySignatureSym   Symbol
	propertySignatureNamed bool
	typeAnnotationSym      Symbol
	typeAnnotationNamed    bool
	objectSym              Symbol
	pairSym                Symbol
	propertyIdentifierSym  Symbol
	colonSym               Symbol
	greaterThanSym         Symbol
	parenthesizedExprSym   Symbol
	lessThanSym            Symbol
	identifierSym          Symbol
	memberExpressionSym    Symbol
	sequenceExpressionSym  Symbol
	typeIdentifierSym      Symbol
	typeIdentifierNamed    bool
	hasTypeIdentifierSym   bool
	enumBodySym            Symbol
	enumAssignmentSym      Symbol
}

func normalizeTypeScriptCompatibility(root *Node, source []byte, lang *Language) {
	ctx, ok := newTypeScriptNormalizationContext(source, lang)
	if !ok || root == nil {
		return
	}

	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		normalizeTypeScriptIdentifierKeywordAliases(n, &ctx)
		normalizeTypeScriptImportKeywordNamedness(n, &ctx)
		if ctx.canClearEnumBodyFields && n.symbol == ctx.enumBodySym && len(n.fieldIDs) > 0 {
			limit := len(n.children)
			if len(n.fieldIDs) < limit {
				limit = len(n.fieldIDs)
			}
			for i := 0; i < limit; i++ {
				child := n.children[i]
				if child == nil || child.symbol != ctx.enumAssignmentSym {
					continue
				}
				n.fieldIDs[i] = 0
				if len(n.fieldSources) > i {
					n.fieldSources[i] = fieldSourceNone
				}
			}
		}
		for i, child := range n.children {
			for {
				var rewritten *Node
				switch {
				case ctx.canRewriteGenericCalls:
					rewritten = rewriteTypeScriptPredefinedGenericCall(child, &ctx)
				}
				if rewritten == nil && ctx.canRewriteInstantiatedCalls {
					rewritten = rewriteTypeScriptInstantiatedCall(child, &ctx)
				}
				if rewritten == nil && ctx.canRewriteAsExpressions {
					rewritten = rewriteTypeScriptAsExpressionCompatibility(child, &ctx)
				}
				if rewritten == nil {
					break
				}
				n.children[i] = rewritten
				rewritten.parent = n
				rewritten.childIndex = i
				child = rewritten
			}
			walk(child)
		}
	}
	walk(root)
}

func normalizeTypeScriptIdentifierKeywordAliases(node *Node, ctx *typeScriptNormalizationContext) {
	if node == nil || ctx == nil || ctx.lang == nil || node.symbol != ctx.identifierSym || len(node.children) != 1 {
		return
	}
	child := node.children[0]
	if child == nil || child.IsNamed() || child.IsExtra() {
		return
	}
	if child.startByte != node.startByte || child.endByte != node.endByte || child.startPoint != node.startPoint || child.endPoint != node.endPoint {
		return
	}
	node.children = nil
	node.fieldIDs = nil
	node.fieldSources = nil
}

func normalizeTypeScriptImportKeywordNamedness(node *Node, ctx *typeScriptNormalizationContext) {
	if node == nil || ctx == nil || ctx.lang == nil || node.Type(ctx.lang) != "import" {
		return
	}
	node.isNamed = false
}

func normalizeTypeScriptRecoveredNamespaceRoot(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || len(root.children) < 4 {
		return
	}
	if lang.Name != "tsx" && lang.Name != "typescript" {
		return
	}
	rootType := root.Type(lang)
	if rootType != "ERROR" && rootType != "program" {
		return
	}
	stmtBlockSym, ok := lang.SymbolByName("statement_block")
	if !ok {
		return
	}
	internalModuleSym, ok := lang.SymbolByName("internal_module")
	if !ok {
		return
	}
	exprStmtSym, hasExprStmtSym := lang.SymbolByName("expression_statement")
	programSym, hasProgramSym := lang.SymbolByName("program")

	namespaceIdx := -1
	for i, child := range root.children {
		if child == nil || child.isExtra {
			continue
		}
		if child.Type(lang) != "namespace" {
			if child.Type(lang) != "comment" {
				return
			}
			continue
		}
		namespaceIdx = i
		break
	}
	if namespaceIdx < 0 || namespaceIdx+2 >= len(root.children) {
		return
	}
	nameNode := root.children[namespaceIdx+1]
	openBrace := root.children[namespaceIdx+2]
	if nameNode == nil || openBrace == nil || nameNode.Type(lang) != "identifier" || openBrace.Type(lang) != "{" {
		return
	}

	bodyChildren := make([]*Node, 0, len(root.children)-(namespaceIdx+3))
	for i := namespaceIdx + 3; i < len(root.children); i++ {
		child := root.children[i]
		if child == nil {
			continue
		}
		if typeScriptWhitespaceOnlyRecoverySubtree(child, source) {
			continue
		}
		bodyChildren = append(bodyChildren, child)
	}
	if len(bodyChildren) == 0 {
		return
	}
	if root.ownerArena != nil {
		buf := root.ownerArena.allocNodeSlice(len(bodyChildren))
		copy(buf, bodyChildren)
		bodyChildren = buf
	}

	stmtBlockNamed := int(stmtBlockSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[stmtBlockSym].Named
	internalModuleNamed := int(internalModuleSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[internalModuleSym].Named
	block := newParentNodeInArena(root.ownerArena, stmtBlockSym, stmtBlockNamed, bodyChildren, nil, 0)
	block.startByte = openBrace.startByte
	block.startPoint = openBrace.startPoint
	if len(bodyChildren) > 0 {
		last := bodyChildren[len(bodyChildren)-1]
		block.endByte = last.endByte
		block.endPoint = last.endPoint
	}

	moduleChildren := []*Node{nameNode, block}
	if root.ownerArena != nil {
		buf := root.ownerArena.allocNodeSlice(len(moduleChildren))
		copy(buf, moduleChildren)
		moduleChildren = buf
	}
	internalModule := newParentNodeInArena(root.ownerArena, internalModuleSym, internalModuleNamed, moduleChildren, nil, 0)
	internalModule.startByte = root.children[namespaceIdx].startByte
	internalModule.startPoint = root.children[namespaceIdx].startPoint
	internalModule.endByte = block.endByte
	internalModule.endPoint = block.endPoint

	wrapped := internalModule
	if hasExprStmtSym {
		exprStmtNamed := int(exprStmtSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[exprStmtSym].Named
		exprChildren := []*Node{internalModule}
		if root.ownerArena != nil {
			buf := root.ownerArena.allocNodeSlice(1)
			buf[0] = internalModule
			exprChildren = buf
		}
		exprStmt := newParentNodeInArena(root.ownerArena, exprStmtSym, exprStmtNamed, exprChildren, nil, 0)
		exprStmt.startByte = internalModule.startByte
		exprStmt.startPoint = internalModule.startPoint
		exprStmt.endByte = internalModule.endByte
		exprStmt.endPoint = internalModule.endPoint
		wrapped = exprStmt
	}

	newChildren := make([]*Node, 0, namespaceIdx+1)
	for i := 0; i < namespaceIdx; i++ {
		if root.children[i] != nil {
			newChildren = append(newChildren, root.children[i])
		}
	}
	newChildren = append(newChildren, wrapped)
	if root.ownerArena != nil {
		buf := root.ownerArena.allocNodeSlice(len(newChildren))
		copy(buf, newChildren)
		newChildren = buf
	}
	root.children = newChildren
	root.fieldIDs = nil
	root.fieldSources = nil
	if hasProgramSym {
		root.symbol = programSym
		root.isNamed = int(programSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[programSym].Named
	}
	populateParentNode(root, root.children)
}

func typeScriptWhitespaceOnlyRecoverySubtree(node *Node, source []byte) bool {
	if node == nil || (!node.HasError() && node.symbol != errorSymbol) {
		return false
	}
	if int(node.endByte) > len(source) || node.startByte > node.endByte {
		return false
	}
	return bytesAreTrivia(source[node.startByte:node.endByte])
}

func newTypeScriptNormalizationContext(source []byte, lang *Language) (typeScriptNormalizationContext, bool) {
	ctx := typeScriptNormalizationContext{
		source: source,
		lang:   lang,
	}
	if lang == nil {
		return ctx, false
	}
	switch lang.Name {
	case "tsx", "typescript":
	default:
		return ctx, false
	}

	if callSym, ok := lang.SymbolByName("call_expression"); ok {
		if instantiationExprSym, ok := lang.SymbolByName("instantiation_expression"); ok {
			ctx.instantiationExprSym = instantiationExprSym
			ctx.instantiationExprNamed = int(instantiationExprSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[instantiationExprSym].Named
		}
		if typeArgsSym, ok := lang.SymbolByName("type_arguments"); ok {
			if argsSym, ok := lang.SymbolByName("arguments"); ok {
				if predefinedTypeSym, ok := lang.SymbolByName("predefined_type"); ok {
					if binaryExpressionSym, ok := lang.SymbolByName("binary_expression"); ok {
						if greaterThanSym, ok := lang.SymbolByName(">"); ok {
							if parenthesizedExprSym, ok := lang.SymbolByName("parenthesized_expression"); ok {
								if lessThanSym, ok := lang.SymbolByName("<"); ok {
									if identifierSym, ok := lang.SymbolByName("identifier"); ok {
										if memberExpressionSym, ok := lang.SymbolByName("member_expression"); ok {
											if sequenceExpressionSym, ok := lang.SymbolByName("sequence_expression"); ok {
												ctx.canRewriteGenericCalls = true
												ctx.callSym = callSym
												ctx.callNamed = int(callSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[callSym].Named
												ctx.typeArgsSym = typeArgsSym
												ctx.typeArgsNamed = int(typeArgsSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[typeArgsSym].Named
												ctx.argsSym = argsSym
												ctx.argsNamed = int(argsSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[argsSym].Named
												ctx.predefinedTypeSym = predefinedTypeSym
												ctx.predefinedTypeNamed = int(predefinedTypeSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[predefinedTypeSym].Named
												ctx.binaryExpressionSym = binaryExpressionSym
												ctx.greaterThanSym = greaterThanSym
												ctx.parenthesizedExprSym = parenthesizedExprSym
												ctx.lessThanSym = lessThanSym
												ctx.identifierSym = identifierSym
												ctx.memberExpressionSym = memberExpressionSym
												ctx.sequenceExpressionSym = sequenceExpressionSym
												ctx.functionFieldID, _ = lang.FieldByName("function")
												ctx.typeArgsFieldID, _ = lang.FieldByName("type_arguments")
												ctx.argumentsFieldID, _ = lang.FieldByName("arguments")
												ctx.typeIdentifierSym, ctx.hasTypeIdentifierSym = lang.SymbolByName("type_identifier")
												if ctx.hasTypeIdentifierSym {
													ctx.typeIdentifierNamed = int(ctx.typeIdentifierSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[ctx.typeIdentifierSym].Named
												}
												ctx.canRewriteInstantiatedCalls = ctx.instantiationExprSym != 0 && ctx.functionFieldID != 0 && ctx.typeArgsFieldID != 0 && ctx.argumentsFieldID != 0
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	if asExpressionSym, ok := lang.SymbolByName("as_expression"); ok {
		if assignmentExprSym, ok := lang.SymbolByName("assignment_expression"); ok {
			if ternaryExprSym, ok := lang.SymbolByName("ternary_expression"); ok {
				if unionTypeSym, ok := lang.SymbolByName("union_type"); ok {
					if intersectionTypeSym, ok := lang.SymbolByName("intersection_type"); ok {
						ctx.canRewriteAsExpressions = true
						ctx.asExpressionSym = asExpressionSym
						ctx.asExpressionNamed = int(asExpressionSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[asExpressionSym].Named
						ctx.assignmentExprSym = assignmentExprSym
						ctx.assignmentExprNamed = int(assignmentExprSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[assignmentExprSym].Named
						ctx.ternaryExprSym = ternaryExprSym
						ctx.ternaryExprNamed = int(ternaryExprSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[ternaryExprSym].Named
						ctx.unionTypeSym = unionTypeSym
						ctx.unionTypeNamed = int(unionTypeSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[unionTypeSym].Named
						ctx.intersectionTypeSym = intersectionTypeSym
						ctx.intersectionTypeNamed = int(intersectionTypeSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[intersectionTypeSym].Named
						if objectTypeSym, ok := lang.SymbolByName("object_type"); ok {
							if propertySignatureSym, ok := lang.SymbolByName("property_signature"); ok {
								if typeAnnotationSym, ok := lang.SymbolByName("type_annotation"); ok {
									if objectSym, ok := lang.SymbolByName("object"); ok {
										if pairSym, ok := lang.SymbolByName("pair"); ok {
											if propertyIdentifierSym, ok := lang.SymbolByName("property_identifier"); ok {
												if colonSym, ok := lang.SymbolByName(":"); ok {
													ctx.objectTypeSym = objectTypeSym
													ctx.objectTypeNamed = int(objectTypeSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[objectTypeSym].Named
													ctx.propertySignatureSym = propertySignatureSym
													ctx.propertySignatureNamed = int(propertySignatureSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[propertySignatureSym].Named
													ctx.typeAnnotationSym = typeAnnotationSym
													ctx.typeAnnotationNamed = int(typeAnnotationSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[typeAnnotationSym].Named
													ctx.objectSym = objectSym
													ctx.pairSym = pairSym
													ctx.propertyIdentifierSym = propertyIdentifierSym
													ctx.colonSym = colonSym
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	if enumBodySym, ok := lang.SymbolByName("enum_body"); ok {
		if enumAssignmentSym, ok := lang.SymbolByName("enum_assignment"); ok {
			ctx.canClearEnumBodyFields = true
			ctx.enumBodySym = enumBodySym
			ctx.enumAssignmentSym = enumAssignmentSym
		}
	}

	return ctx, ctx.canRewriteGenericCalls || ctx.canRewriteInstantiatedCalls || ctx.canRewriteAsExpressions || ctx.canClearEnumBodyFields
}

func rewriteTypeScriptPredefinedGenericCall(node *Node, ctx *typeScriptNormalizationContext) *Node {
	if node == nil || ctx == nil || ctx.lang == nil || node.symbol != ctx.binaryExpressionSym || len(node.children) != 3 {
		return nil
	}
	left := node.children[0]
	gt := node.children[1]
	paren := node.children[2]
	if left == nil || gt == nil || paren == nil || left.symbol != ctx.binaryExpressionSym || gt.symbol != ctx.greaterThanSym || paren.symbol != ctx.parenthesizedExprSym {
		return nil
	}
	if len(left.children) != 3 || len(paren.children) != 3 {
		return nil
	}
	callee := left.children[0]
	lt := left.children[1]
	typeArg := left.children[2]
	if callee == nil || lt == nil || typeArg == nil || lt.symbol != ctx.lessThanSym {
		return nil
	}
	switch callee.Type(ctx.lang) {
	case "identifier", "member_expression":
	default:
		return nil
	}
	typeArg = normalizeTypeScriptGenericCallTypeArgument(typeArg, ctx)
	if typeArg == nil {
		return nil
	}
	arena := node.ownerArena
	if typeArg.ownerArena != arena {
		typeArg = cloneNodeInArena(arena, typeArg)
	}
	typeArgs := newParentNodeInArena(arena, ctx.typeArgsSym, ctx.typeArgsNamed, []*Node{lt, typeArg, gt}, nil, 0)
	argsChildren := typeScriptGenericCallArgumentChildren(paren, ctx.sequenceExpressionSym)
	if arena != nil && len(argsChildren) > 0 {
		buf := arena.allocNodeSlice(len(argsChildren))
		copy(buf, argsChildren)
		argsChildren = buf
	}
	args := newParentNodeInArena(arena, ctx.argsSym, ctx.argsNamed, argsChildren, nil, paren.productionID)

	callChildren := phpAllocChildren(arena, 3)
	callChildren[0] = callee
	callChildren[1] = typeArgs
	callChildren[2] = args
	var fieldIDs []FieldID
	if ctx.functionFieldID != 0 || ctx.typeArgsFieldID != 0 || ctx.argumentsFieldID != 0 {
		if arena != nil {
			fieldIDs = arena.allocFieldIDSlice(3)
		} else {
			fieldIDs = make([]FieldID, 3)
		}
		fieldIDs[0] = ctx.functionFieldID
		fieldIDs[1] = ctx.typeArgsFieldID
		fieldIDs[2] = ctx.argumentsFieldID
	}
	call := newParentNodeInArena(arena, ctx.callSym, ctx.callNamed, callChildren, fieldIDs, node.productionID)
	call.fieldSources = defaultFieldSourcesInArena(arena, fieldIDs)
	return call
}

func rewriteTypeScriptInstantiatedCall(node *Node, ctx *typeScriptNormalizationContext) *Node {
	if node == nil || ctx == nil || ctx.lang == nil || node.symbol != ctx.callSym || len(node.children) != 2 {
		return nil
	}
	function := node.children[0]
	arguments := node.children[1]
	if function == nil || arguments == nil || function.symbol != ctx.instantiationExprSym || arguments.symbol != ctx.argsSym || len(function.children) != 2 {
		return nil
	}
	callee := function.children[0]
	typeArgs := function.children[1]
	if callee == nil || typeArgs == nil || typeArgs.symbol != ctx.typeArgsSym {
		return nil
	}
	children := phpAllocChildren(node.ownerArena, 3)
	children[0] = callee
	children[1] = typeArgs
	children[2] = arguments
	var fieldIDs []FieldID
	if ctx.functionFieldID != 0 || ctx.typeArgsFieldID != 0 || ctx.argumentsFieldID != 0 {
		if node.ownerArena != nil {
			fieldIDs = node.ownerArena.allocFieldIDSlice(3)
		} else {
			fieldIDs = make([]FieldID, 3)
		}
		fieldIDs[0] = ctx.functionFieldID
		fieldIDs[1] = ctx.typeArgsFieldID
		fieldIDs[2] = ctx.argumentsFieldID
	}
	call := newParentNodeInArena(node.ownerArena, ctx.callSym, ctx.callNamed, children, fieldIDs, node.productionID)
	call.fieldSources = defaultFieldSourcesInArena(node.ownerArena, fieldIDs)
	return call
}

func rewriteTypeScriptAsExpressionCompatibility(node *Node, ctx *typeScriptNormalizationContext) *Node {
	if node == nil || ctx == nil || ctx.lang == nil {
		return nil
	}
	if rewritten := rewriteTypeScriptAsAssignmentOrTernary(node, ctx); rewritten != nil {
		return rewritten
	}
	return rewriteTypeScriptAsTypeChain(node, ctx)
}

func rewriteTypeScriptAsAssignmentOrTernary(node *Node, ctx *typeScriptNormalizationContext) *Node {
	if node == nil || ctx == nil || ctx.lang == nil || node.symbol != ctx.asExpressionSym || len(node.children) < 2 {
		return nil
	}
	valueIdx, typeIdx := 0, len(node.children)-1
	value := node.children[valueIdx]
	if value == nil {
		return nil
	}

	switch value.symbol {
	case ctx.assignmentExprSym:
		if len(value.children) < 2 {
			return nil
		}
		rightIdx := len(value.children) - 1
		rewrittenAs := cloneNodeInArena(node.ownerArena, node)
		asChildren := cloneNodeSliceInArena(node.ownerArena, node.children)
		asChildren[valueIdx] = value.children[rightIdx]
		rewrittenAs.children = asChildren
		populateParentNode(rewrittenAs, rewrittenAs.children)

		rewrittenAssign := cloneNodeInArena(node.ownerArena, value)
		assignChildren := cloneNodeSliceInArena(node.ownerArena, value.children)
		assignChildren[rightIdx] = rewrittenAs
		rewrittenAssign.children = assignChildren
		populateParentNode(rewrittenAssign, rewrittenAssign.children)
		return rewrittenAssign
	case ctx.ternaryExprSym:
		if len(value.children) < 3 {
			return nil
		}
		falseIdx := len(value.children) - 1
		rewrittenAs := cloneNodeInArena(node.ownerArena, node)
		asChildren := cloneNodeSliceInArena(node.ownerArena, node.children)
		asChildren[valueIdx] = value.children[falseIdx]
		rewrittenAs.children = asChildren
		populateParentNode(rewrittenAs, rewrittenAs.children)

		rewrittenTernary := cloneNodeInArena(node.ownerArena, value)
		ternaryChildren := cloneNodeSliceInArena(node.ownerArena, value.children)
		ternaryChildren[falseIdx] = rewrittenAs
		rewrittenTernary.children = ternaryChildren
		populateParentNode(rewrittenTernary, rewrittenTernary.children)
		return rewrittenTernary
	default:
		_ = typeIdx
		return nil
	}
}

func rewriteTypeScriptAsTypeChain(node *Node, ctx *typeScriptNormalizationContext) *Node {
	if node == nil || ctx == nil || ctx.lang == nil || node.symbol != ctx.binaryExpressionSym || len(node.children) != 3 {
		return nil
	}
	baseAs, rewrittenType, ok := collapseTypeScriptAsTypeChain(node, ctx)
	if !ok || baseAs == nil || rewrittenType == nil || len(baseAs.children) < 2 {
		return nil
	}
	rewrittenAs := cloneNodeInArena(node.ownerArena, baseAs)
	asChildren := cloneNodeSliceInArena(node.ownerArena, baseAs.children)
	asChildren[len(asChildren)-1] = rewrittenType
	rewrittenAs.children = asChildren
	populateParentNode(rewrittenAs, rewrittenAs.children)
	return rewrittenAs
}

func collapseTypeScriptAsTypeChain(node *Node, ctx *typeScriptNormalizationContext) (*Node, *Node, bool) {
	if node == nil || ctx == nil || ctx.lang == nil || node.symbol != ctx.binaryExpressionSym || len(node.children) != 3 {
		return nil, nil, false
	}
	left := node.children[0]
	op := node.children[1]
	right := node.children[2]
	if left == nil || op == nil || right == nil {
		return nil, nil, false
	}
	var typeSym Symbol
	var typeNamed bool
	switch op.Type(ctx.lang) {
	case "|":
		typeSym = ctx.unionTypeSym
		typeNamed = ctx.unionTypeNamed
	case "&":
		typeSym = ctx.intersectionTypeSym
		typeNamed = ctx.intersectionTypeNamed
	default:
		return nil, nil, false
	}

	rightType := normalizeTypeScriptTypeExpression(right, ctx)
	if rightType == nil {
		return nil, nil, false
	}

	if left.symbol == ctx.asExpressionSym && len(left.children) >= 2 {
		leftType := normalizeTypeScriptTypeExpression(left.children[len(left.children)-1], ctx)
		if leftType == nil {
			return nil, nil, false
		}
		children := cloneNodeSliceInArena(node.ownerArena, []*Node{leftType, op, rightType})
		return left, newParentNodeInArena(node.ownerArena, typeSym, typeNamed, children, nil, node.productionID), true
	}

	leftAs, leftType, ok := collapseTypeScriptAsTypeChain(left, ctx)
	if !ok || leftAs == nil || leftType == nil {
		return nil, nil, false
	}
	children := cloneNodeSliceInArena(node.ownerArena, []*Node{leftType, op, rightType})
	return leftAs, newParentNodeInArena(node.ownerArena, typeSym, typeNamed, children, nil, node.productionID), true
}

func normalizeTypeScriptTypeExpression(node *Node, ctx *typeScriptNormalizationContext) *Node {
	if node == nil || ctx == nil || ctx.lang == nil {
		return nil
	}
	switch node.Type(ctx.lang) {
	case "type_identifier", "predefined_type", "union_type", "intersection_type", "object_type", "literal_type", "generic_type", "lookup_type", "template_literal_type", "conditional_type", "tuple_type", "array_type", "function_type", "constructor_type", "readonly_type", "type_query", "infer_type", "index_type_query", "nested_type_identifier":
		return node
	case "identifier":
		if ctx.hasTypeIdentifierSym {
			return newLeafNodeInArena(node.ownerArena, ctx.typeIdentifierSym, ctx.typeIdentifierNamed, node.startByte, node.endByte, node.startPoint, node.endPoint)
		}
		return node
	case "binary_expression":
		if len(node.children) != 3 || node.children[1] == nil {
			return nil
		}
		var typeSym Symbol
		var typeNamed bool
		switch node.children[1].Type(ctx.lang) {
		case "|":
			typeSym = ctx.unionTypeSym
			typeNamed = ctx.unionTypeNamed
		case "&":
			typeSym = ctx.intersectionTypeSym
			typeNamed = ctx.intersectionTypeNamed
		default:
			return nil
		}
		leftType := normalizeTypeScriptTypeExpression(node.children[0], ctx)
		rightType := normalizeTypeScriptTypeExpression(node.children[2], ctx)
		if leftType == nil || rightType == nil {
			return nil
		}
		children := cloneNodeSliceInArena(node.ownerArena, []*Node{leftType, node.children[1], rightType})
		return newParentNodeInArena(node.ownerArena, typeSym, typeNamed, children, nil, node.productionID)
	case "object":
		return rewriteTypeScriptObjectExpressionAsType(node, ctx)
	default:
		return nil
	}
}

func rewriteTypeScriptObjectExpressionAsType(node *Node, ctx *typeScriptNormalizationContext) *Node {
	if node == nil || ctx == nil || ctx.lang == nil || node.Type(ctx.lang) != "object" {
		return nil
	}
	children := cloneNodeSliceInArena(node.ownerArena, node.children)
	changed := false
	for i, child := range children {
		if child == nil || child.Type(ctx.lang) != "pair" {
			continue
		}
		propSig := rewriteTypeScriptObjectPairAsPropertySignature(child, ctx)
		if propSig == nil {
			return nil
		}
		children[i] = propSig
		changed = true
	}
	if !changed && len(children) != 2 {
		return nil
	}
	return newParentNodeInArena(node.ownerArena, ctx.objectTypeSym, ctx.objectTypeNamed, children, nil, node.productionID)
}

func rewriteTypeScriptObjectPairAsPropertySignature(node *Node, ctx *typeScriptNormalizationContext) *Node {
	if node == nil || ctx == nil || ctx.lang == nil || node.Type(ctx.lang) != "pair" || len(node.children) < 3 {
		return nil
	}
	key := node.children[0]
	colon := node.children[1]
	value := node.children[len(node.children)-1]
	if key == nil || colon == nil || value == nil || key.Type(ctx.lang) != "property_identifier" || colon.Type(ctx.lang) != ":" {
		return nil
	}
	valueType := normalizeTypeScriptTypeExpression(value, ctx)
	if valueType == nil {
		return nil
	}
	typeAnnChildren := cloneNodeSliceInArena(node.ownerArena, []*Node{colon, valueType})
	typeAnnotation := newParentNodeInArena(node.ownerArena, ctx.typeAnnotationSym, ctx.typeAnnotationNamed, typeAnnChildren, nil, 0)
	propChildren := cloneNodeSliceInArena(node.ownerArena, []*Node{key, typeAnnotation})
	return newParentNodeInArena(node.ownerArena, ctx.propertySignatureSym, ctx.propertySignatureNamed, propChildren, nil, node.productionID)
}

func typeScriptGenericCallArgumentChildren(paren *Node, sequenceExpressionSym Symbol) []*Node {
	if paren == nil {
		return nil
	}
	if len(paren.children) != 3 || paren.children[1] == nil || paren.children[1].symbol != sequenceExpressionSym {
		return append([]*Node(nil), paren.children...)
	}
	seq := paren.children[1]
	out := make([]*Node, 0, len(seq.children)+2)
	out = append(out, paren.children[0])
	out = append(out, seq.children...)
	out = append(out, paren.children[2])
	return out
}

func normalizeTypeScriptGenericCallTypeArgument(node *Node, ctx *typeScriptNormalizationContext) *Node {
	if node == nil || ctx == nil || ctx.lang == nil {
		return nil
	}
	switch node.Type(ctx.lang) {
	case "predefined_type":
		return node
	case "type_identifier":
		if ctx.hasTypeIdentifierSym {
			return node
		}
	case "identifier":
		if typeKeywordSym, ok := typeScriptPredefinedTypeSymbol(ctx.lang, node.Text(ctx.source)); ok {
			typeKeywordNamed := int(typeKeywordSym) < len(ctx.lang.SymbolMetadata) && ctx.lang.SymbolMetadata[typeKeywordSym].Named
			typeLeaf := newLeafNodeInArena(node.ownerArena, typeKeywordSym, typeKeywordNamed, node.startByte, node.endByte, node.startPoint, node.endPoint)
			return newParentNodeInArena(node.ownerArena, ctx.predefinedTypeSym, ctx.predefinedTypeNamed, []*Node{typeLeaf}, nil, 0)
		}
		if ctx.hasTypeIdentifierSym {
			typeIdentifierNamed := int(ctx.typeIdentifierSym) < len(ctx.lang.SymbolMetadata) && ctx.lang.SymbolMetadata[ctx.typeIdentifierSym].Named
			return newLeafNodeInArena(node.ownerArena, ctx.typeIdentifierSym, typeIdentifierNamed, node.startByte, node.endByte, node.startPoint, node.endPoint)
		}
	}
	return nil
}

func typeScriptPredefinedTypeSymbol(lang *Language, text string) (Symbol, bool) {
	if lang == nil {
		return 0, false
	}
	switch text {
	case "any", "bigint", "boolean", "never", "number", "object", "string", "symbol", "undefined", "unknown", "void":
		return lang.SymbolByName(text)
	default:
		return 0, false
	}
}

func rewriteJavaScriptTopLevelObjectLiteral(node *Node, lang *Language, arena *nodeArena, exprSym Symbol, exprNamed bool, objectSym Symbol, objectNamed bool, pairSym Symbol, pairNamed bool, propSym Symbol) (*Node, bool) {
	if node == nil || lang == nil || node.Type(lang) != "statement_block" || len(node.children) != 3 {
		return nil, false
	}
	if node.children[0] == nil || node.children[0].Type(lang) != "{" || node.children[2] == nil || node.children[2].Type(lang) != "}" {
		return nil, false
	}
	label := node.children[1]
	if label == nil || label.Type(lang) != "labeled_statement" || len(label.children) != 3 {
		return nil, false
	}
	key := label.children[0]
	colon := label.children[1]
	valueStmt := label.children[2]
	if key == nil || key.Type(lang) != "statement_identifier" || colon == nil || colon.Type(lang) != ":" || valueStmt == nil || valueStmt.Type(lang) != "expression_statement" || len(valueStmt.children) != 1 || valueStmt.children[0] == nil {
		return nil, false
	}
	pair := newParentNodeInArena(arena, pairSym, pairNamed, []*Node{
		aliasedNodeInArena(arena, lang, key, propSym),
		colon,
		valueStmt.children[0],
	}, nil, 0)
	for fieldIdx, fieldName := range lang.FieldNames {
		switch fieldName {
		case "key":
			ensureNodeFieldStorage(pair, len(pair.children))
			pair.fieldIDs[0] = FieldID(fieldIdx)
			pair.fieldSources[0] = fieldSourceDirect
		case "value":
			ensureNodeFieldStorage(pair, len(pair.children))
			pair.fieldIDs[2] = FieldID(fieldIdx)
			pair.fieldSources[2] = fieldSourceDirect
		}
	}
	object := newParentNodeInArena(arena, objectSym, objectNamed, []*Node{
		node.children[0],
		pair,
		node.children[2],
	}, nil, 0)
	return newParentNodeInArena(arena, exprSym, exprNamed, []*Node{object}, nil, 0), true
}

func normalizeErlangSourceFileForms(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "erlang" || root.Type(lang) != "source_file" {
		return
	}
	formsOnlyID := FieldID(0)
	for i, fieldName := range lang.FieldNames {
		if fieldName == "forms_only" {
			formsOnlyID = FieldID(i)
			break
		}
	}
	if formsOnlyID == 0 || !erlangSourceFileLooksLikeForms(root, lang) {
		return
	}
	ensureNodeFieldStorage(root, len(root.children))
	for i, child := range root.children {
		if child == nil || child.IsExtra() {
			continue
		}
		root.fieldIDs[i] = formsOnlyID
		root.fieldSources[i] = fieldSourceDirect
		normalizeErlangTopLevelFormBounds(child)
	}
}

func erlangSourceFileLooksLikeForms(root *Node, lang *Language) bool {
	sawForm := false
	for _, child := range root.children {
		if child == nil || child.IsExtra() {
			continue
		}
		if !erlangIsTopLevelFormType(child.Type(lang)) {
			return false
		}
		sawForm = true
	}
	return sawForm
}

func erlangIsTopLevelFormType(typ string) bool {
	switch typ {
	case "module_attribute",
		"behaviour_attribute",
		"export_attribute",
		"import_attribute",
		"export_type_attribute",
		"optional_callbacks_attribute",
		"compile_options_attribute",
		"feature_attribute",
		"file_attribute",
		"deprecated_attribute",
		"record_decl",
		"type_alias",
		"nominal",
		"opaque",
		"spec",
		"callback",
		"wild_attribute",
		"fun_decl",
		"pp_include",
		"pp_include_lib",
		"pp_undef",
		"pp_ifdef",
		"pp_ifndef",
		"pp_else",
		"pp_endif",
		"pp_if",
		"pp_elif",
		"pp_define",
		"ssr_definition",
		"shebang":
		return true
	default:
		return false
	}
}

func normalizeErlangTopLevelFormBounds(node *Node) {
	if node == nil || len(node.children) == 0 {
		return
	}
	var first, last *Node
	for _, child := range node.children {
		if child == nil || child.IsExtra() {
			continue
		}
		if first == nil {
			first = child
		}
		last = child
	}
	if first == nil || last == nil {
		return
	}
	node.startByte = first.startByte
	node.startPoint = first.startPoint
	node.endByte = last.endByte
	node.endPoint = last.endPoint
}

func normalizeLuaChunkLocalDeclarationFields(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "lua" || root.Type(lang) != "chunk" || len(source) == 0 {
		return
	}
	localDeclID := FieldID(0)
	for i, fieldName := range lang.FieldNames {
		if fieldName == "local_declaration" {
			localDeclID = FieldID(i)
			break
		}
	}
	if localDeclID == 0 {
		return
	}
	ensureNodeFieldStorage(root, len(root.children))
	for i, child := range root.children {
		if child == nil || child.IsExtra() {
			continue
		}
		switch child.Type(lang) {
		case "function_declaration", "variable_declaration":
		default:
			continue
		}
		if !luaNodeStartsWithLocalKeyword(child, source) {
			continue
		}
		root.fieldIDs[i] = localDeclID
		root.fieldSources[i] = fieldSourceDirect
	}
}

func luaNodeStartsWithLocalKeyword(node *Node, source []byte) bool {
	if node == nil || node.startByte >= uint32(len(source)) {
		return false
	}
	start := int(node.startByte)
	if !bytes.HasPrefix(source[start:], []byte("local")) {
		return false
	}
	after := start + len("local")
	return after >= len(source) || source[after] == ' ' || source[after] == '\t' || source[after] == '\n' || source[after] == '\r'
}

func normalizeSvelteTrailingExtraTrivia(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "svelte" || root.Type(lang) != "document" || len(root.children) == 0 || len(source) == 0 {
		return
	}
	last := root.children[len(root.children)-1]
	if last == nil || last.IsNamed() || !last.IsExtra() || len(last.children) != 0 {
		return
	}
	if last.Type(lang) != "_tag_value_token1" {
		return
	}
	if last.startByte >= last.endByte || last.endByte != root.endByte || int(last.endByte) > len(source) {
		return
	}
	if !bytesAreTrivia(source[last.startByte:last.endByte]) {
		return
	}
	root.children = root.children[:len(root.children)-1]
	if len(root.fieldIDs) > len(root.children) {
		root.fieldIDs = root.fieldIDs[:len(root.children)]
	}
	if len(root.fieldSources) > len(root.children) {
		root.fieldSources = root.fieldSources[:len(root.children)]
	}
}

func trimTrailingExtraTriviaRoot(root *Node, source []byte) {
	if root == nil || len(root.children) == 0 || len(source) == 0 {
		return
	}
	last := root.children[len(root.children)-1]
	if last == nil || !last.IsExtra() || len(last.children) != 0 {
		return
	}
	if last.startByte >= last.endByte || last.endByte != root.endByte || int(last.endByte) > len(source) {
		return
	}
	if !bytesAreTrivia(source[last.startByte:last.endByte]) {
		return
	}
	root.children = root.children[:len(root.children)-1]
	if len(root.fieldIDs) > len(root.children) {
		root.fieldIDs = root.fieldIDs[:len(root.children)]
	}
	if len(root.fieldSources) > len(root.children) {
		root.fieldSources = root.fieldSources[:len(root.children)]
	}
}

func normalizePowerShellProgramShape(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "powershell" || root.Type(lang) != "ERROR" || len(root.children) < 4 || len(source) == 0 {
		return
	}
	programSym, ok := symbolByName(lang, "program")
	if !ok {
		return
	}
	statementListSym, ok := symbolByName(lang, "statement_list")
	if !ok {
		return
	}
	functionStatementSym, ok := symbolByName(lang, "function_statement")
	if !ok {
		return
	}
	functionSym, ok := symbolByName(lang, "function")
	if !ok {
		return
	}
	scriptBlockSym, ok := symbolByName(lang, "script_block")
	if !ok {
		return
	}
	scriptBlockBodySym, ok := symbolByName(lang, "script_block_body")
	if !ok {
		return
	}
	closeBraceSym, ok := symbolByName(lang, "}")
	if !ok {
		return
	}
	pipelineSym, ok := symbolByName(lang, "pipeline")
	if !ok {
		return
	}
	pipelineChainSym, ok := symbolByName(lang, "pipeline_chain")
	if !ok {
		return
	}
	commandSym, ok := symbolByName(lang, "command")
	if !ok {
		return
	}
	commandNameSym, ok := symbolByName(lang, "command_name")
	if !ok {
		return
	}
	commandElementsSym, ok := symbolByName(lang, "command_elements")
	if !ok {
		return
	}
	commandArgSepSym, ok := symbolByName(lang, "command_argument_sep")
	if !ok {
		return
	}
	commandParameterSym, ok := symbolByName(lang, "command_parameter")
	if !ok {
		return
	}
	arrayLiteralSym, ok := symbolByName(lang, "array_literal_expression")
	if !ok {
		return
	}
	unaryExprSym, ok := symbolByName(lang, "unary_expression")
	if !ok {
		return
	}
	variableSym, ok := symbolByName(lang, "variable")
	if !ok {
		return
	}
	stringLiteralSym, ok := symbolByName(lang, "string_literal")
	if !ok {
		return
	}
	expandableStringSym, ok := symbolByName(lang, "expandable_string_literal")
	if !ok {
		return
	}
	genericTokenSym, ok := symbolByName(lang, "generic_token")
	if !ok {
		return
	}
	spaceSym, ok := symbolByName(lang, " ")
	if !ok {
		return
	}

	statementListIdx := -1
	for i, child := range root.children {
		if child != nil && child.Type(lang) == "statement_list" {
			statementListIdx = i
			break
		}
	}
	if statementListIdx < 0 || statementListIdx+3 >= len(root.children) {
		return
	}
	spill := root.children[statementListIdx+1:]
	if !powerShellLooksLikeSpilledFunction(spill, lang) {
		return
	}
	openBrace := spill[2]
	if openBrace == nil {
		return
	}
	closeBracePos := findMatchingBraceByte(source, int(openBrace.startByte), len(source))
	if closeBracePos < 0 {
		return
	}

	functionStatement := buildPowerShellSpilledFunctionStatement(
		root.ownerArena, source, lang, spill, closeBracePos,
		functionStatementSym, functionSym, scriptBlockSym, scriptBlockBodySym, statementListSym, closeBraceSym,
	)
	if functionStatement == nil {
		return
	}
	pipelines := buildPowerShellTrailingPipelines(
		root.ownerArena, source, lang, uint32(closeBracePos+1), root.endByte,
		pipelineSym, pipelineChainSym, commandSym, commandNameSym, commandElementsSym,
		commandArgSepSym, commandParameterSym, arrayLiteralSym, unaryExprSym,
		variableSym, stringLiteralSym, expandableStringSym, genericTokenSym, spaceSym,
	)
	if len(pipelines) == 0 {
		return
	}

	statementList := cloneNodeInArena(root.ownerArena, root.children[statementListIdx])
	children := make([]*Node, 0, len(statementList.children)+1+len(pipelines))
	children = append(children, statementList.children...)
	children = append(children, functionStatement)
	children = append(children, pipelines...)
	if root.ownerArena != nil {
		buf := root.ownerArena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	statementList.children = children
	statementList.fieldIDs = nil
	statementList.fieldSources = nil
	statementList.symbol = statementListSym
	statementList.isNamed = int(statementListSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[statementListSym].Named
	statementList.hasError = true
	extendNodeEndTo(statementList, pipelines[len(pipelines)-1].endByte, source)

	out := make([]*Node, 0, statementListIdx+1)
	out = append(out, root.children[:statementListIdx]...)
	out = append(out, statementList)
	if root.ownerArena != nil {
		buf := root.ownerArena.allocNodeSlice(len(out))
		copy(buf, out)
		out = buf
	}
	root.children = out
	root.fieldIDs = nil
	root.fieldSources = nil
	root.symbol = programSym
	root.isNamed = int(programSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[programSym].Named
	root.hasError = true
}

func powerShellLooksLikeSpilledFunction(nodes []*Node, lang *Language) bool {
	if len(nodes) < 4 || lang == nil {
		return false
	}
	head := nodes[0]
	if head == nil || head.Type(lang) != "ERROR" || len(head.children) != 1 || head.children[0] == nil || head.children[0].Type(lang) != "function" {
		return false
	}
	return nodes[1] != nil && nodes[1].Type(lang) == "function_name" &&
		nodes[2] != nil && nodes[2].Type(lang) == "{"
}

func buildPowerShellSpilledFunctionStatement(arena *nodeArena, source []byte, lang *Language, nodes []*Node, closeBracePos int, functionStatementSym, functionSym, scriptBlockSym, scriptBlockBodySym, statementListSym, closeBraceSym Symbol) *Node {
	if len(nodes) < 4 || nodes[0] == nil || nodes[1] == nil || nodes[2] == nil {
		return nil
	}
	functionLeaf := nodes[0].children[0]
	functionName := nodes[1]
	openBrace := nodes[2]
	scriptEnd := closeBracePos
	for scriptEnd > int(openBrace.endByte) {
		switch source[scriptEnd-1] {
		case ' ', '\t', '\r', '\n':
			scriptEnd--
		default:
			goto trimmed
		}
	}
trimmed:
	scriptChildren := make([]*Node, 0, len(nodes))
	for _, child := range nodes[3:] {
		if child == nil {
			continue
		}
		if int(child.startByte) >= scriptEnd {
			break
		}
		if int(child.endByte) <= scriptEnd {
			scriptChildren = append(scriptChildren, child)
			continue
		}
		truncated := cloneNodeInArena(arena, child)
		truncated.children = nil
		truncated.fieldIDs = nil
		truncated.fieldSources = nil
		truncated.endByte = uint32(scriptEnd)
		truncated.endPoint = advancePointByBytes(truncated.startPoint, source[truncated.startByte:uint32(scriptEnd)])
		scriptChildren = append(scriptChildren, truncated)
		break
	}
	if len(scriptChildren) == 0 {
		return nil
	}
	if len(scriptChildren) > 0 && scriptChildren[0] != nil && scriptChildren[0].Type(lang) == "param_block" {
		structured := make([]*Node, 0, len(scriptChildren))
		structured = append(structured, scriptChildren[0])
		idx := 1
		if idx < len(scriptChildren) && scriptChildren[idx] != nil && scriptChildren[idx].Type(lang) == "_statement_terminator" {
			idx++
		}
		for idx < len(scriptChildren) && scriptChildren[idx] != nil && scriptChildren[idx].Type(lang) == "comment" {
			structured = append(structured, scriptChildren[idx])
			idx++
		}
		if idx < len(scriptChildren) {
			statementListChildren := recoverPowerShellStatementListChildren(arena, source, lang, scriptChildren[idx:], scriptEnd)
			if arena != nil {
				buf := arena.allocNodeSlice(len(statementListChildren))
				copy(buf, statementListChildren)
				statementListChildren = buf
			}
			statementListNamed := int(statementListSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[statementListSym].Named
			stmtList := newParentNodeInArena(arena, statementListSym, statementListNamed, statementListChildren, nil, 0)
			stmtList.hasError = true
			stmtList.endByte = uint32(scriptEnd)
			stmtList.endPoint = advancePointByBytes(stmtList.startPoint, source[stmtList.startByte:uint32(scriptEnd)])
			bodyChildren := []*Node{stmtList}
			if arena != nil {
				buf := arena.allocNodeSlice(1)
				buf[0] = stmtList
				bodyChildren = buf
			}
			scriptBlockBodyNamed := int(scriptBlockBodySym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[scriptBlockBodySym].Named
			body := newParentNodeInArena(arena, scriptBlockBodySym, scriptBlockBodyNamed, bodyChildren, nil, 0)
			body.hasError = true
			for fieldIdx, fieldName := range lang.FieldNames {
				if fieldName != "statement_list" {
					continue
				}
				ensureNodeFieldStorage(body, len(body.children))
				body.fieldIDs[0] = FieldID(fieldIdx)
				body.fieldSources[0] = fieldSourceDirect
				break
			}
			structured = append(structured, body)
		}
		scriptChildren = structured
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(scriptChildren))
		copy(buf, scriptChildren)
		scriptChildren = buf
	}
	scriptBlockNamed := int(scriptBlockSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[scriptBlockSym].Named
	scriptBlock := newParentNodeInArena(arena, scriptBlockSym, scriptBlockNamed, scriptChildren, nil, 0)
	scriptBlock.hasError = true
	for i, child := range scriptBlock.children {
		if child == nil || child.Type(lang) != "script_block_body" {
			continue
		}
		for fieldIdx, fieldName := range lang.FieldNames {
			if fieldName != "script_block_body" {
				continue
			}
			ensureNodeFieldStorage(scriptBlock, len(scriptBlock.children))
			scriptBlock.fieldIDs[i] = FieldID(fieldIdx)
			scriptBlock.fieldSources[i] = fieldSourceDirect
			break
		}
		break
	}
	functionStatementNamed := int(functionStatementSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[functionStatementSym].Named
	closeBraceStart := advancePointByBytes(Point{}, source[:closeBracePos])
	closeBraceLeaf := newLeafNodeInArena(arena, closeBraceSym, false, uint32(closeBracePos), uint32(closeBracePos+1), closeBraceStart, advancePointByBytes(closeBraceStart, source[closeBracePos:closeBracePos+1]))
	children := []*Node{functionLeaf, functionName, openBrace, scriptBlock, closeBraceLeaf}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	fn := newParentNodeInArena(arena, functionStatementSym, functionStatementNamed, children, nil, 0)
	fn.hasError = true
	if functionLeaf.symbol != functionSym {
		functionLeaf = cloneNodeInArena(arena, functionLeaf)
		functionLeaf.symbol = functionSym
		functionLeaf.isNamed = int(functionSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[functionSym].Named
		fn.children[0] = functionLeaf
	}
	extendNodeEndTo(fn, uint32(closeBracePos+1), source)
	return fn
}

func recoverPowerShellStatementListChildren(arena *nodeArena, source []byte, lang *Language, nodes []*Node, end int) []*Node {
	if len(nodes) == 0 || lang == nil || len(source) == 0 {
		return nodes
	}
	flattened := flattenPowerShellStatementListChildren(nodes, lang, nil)
	out := make([]*Node, 0, len(flattened))
	tailStart := -1
	for _, child := range flattened {
		if child == nil {
			continue
		}
		if powerShellIsStatementListChild(child, lang) {
			out = append(out, child)
			continue
		}
		tailStart = int(child.startByte)
		break
	}
	if tailStart < 0 {
		return flattened
	}
	rebuilt := buildPowerShellRecoveredStatements(arena, source, lang, tailStart, end, flattened)
	if len(rebuilt) == 0 {
		return flattened
	}
	out = append(out, rebuilt...)
	return out
}

func flattenPowerShellStatementListChildren(nodes []*Node, lang *Language, out []*Node) []*Node {
	for _, node := range nodes {
		out = flattenPowerShellStatementListChild(node, lang, out)
	}
	return out
}

func flattenPowerShellStatementListChild(node *Node, lang *Language, out []*Node) []*Node {
	if node == nil || lang == nil {
		return out
	}
	switch node.Type(lang) {
	case "_statement":
		if len(node.children) == 1 && node.children[0] != nil {
			return flattenPowerShellStatementListChild(node.children[0], lang, out)
		}
	case "statement_list_repeat1":
		for _, child := range node.children {
			out = flattenPowerShellStatementListChild(child, lang, out)
		}
		return out
	}
	return append(out, node)
}

func powerShellIsStatementListChild(node *Node, lang *Language) bool {
	if node == nil || lang == nil {
		return false
	}
	switch node.Type(lang) {
	case "comment", "pipeline", "if_statement", "try_statement", "flow_control_statement":
		return true
	default:
		return false
	}
}

func buildPowerShellRecoveredStatements(arena *nodeArena, source []byte, lang *Language, start, end int, existing []*Node) []*Node {
	if lang == nil || len(source) == 0 || start >= end {
		return nil
	}
	commentSym, commentNamed, ok := powerShellSymbolMeta(lang, "comment")
	if !ok {
		return nil
	}
	out := make([]*Node, 0, 16)
	i := powerShellSkipTrivia(source, start, end)
	for i < end {
		switch {
		case source[i] == '#':
			lineEnd := powerShellLineEnd(source, i, end)
			startPoint := advancePointByBytes(Point{}, source[:i])
			comment := newLeafNodeInArena(arena, commentSym, commentNamed, uint32(i), uint32(lineEnd), startPoint, advancePointByBytes(startPoint, source[i:lineEnd]))
			comment.isExtra = true
			out = append(out, comment)
			i = powerShellSkipTrivia(source, lineEnd, end)
		case powerShellKeywordAt(source, i, "if"):
			stmt, next := buildPowerShellRecoveredIfStatement(arena, source, lang, i, end, existing)
			if stmt == nil || next <= i {
				return out
			}
			out = append(out, stmt)
			i = powerShellSkipTrivia(source, next, end)
		case powerShellKeywordAt(source, i, "try"):
			stmt, next := buildPowerShellRecoveredTryStatement(arena, source, lang, i, end)
			if stmt == nil || next <= i {
				return out
			}
			out = append(out, stmt)
			i = powerShellSkipTrivia(source, next, end)
		case powerShellKeywordAt(source, i, "throw"):
			lineEnd := powerShellLineEnd(source, i, end)
			if stmt := buildPowerShellRecoveredFlowControlStatement(arena, source, lang, i, lineEnd); stmt != nil {
				out = append(out, stmt)
			}
			i = powerShellSkipTrivia(source, lineEnd, end)
		default:
			lineEnd := powerShellLineEnd(source, i, end)
			if stmt := buildPowerShellRecoveredPipeline(arena, source, lang, i, lineEnd); stmt != nil {
				out = append(out, stmt)
			}
			i = powerShellSkipTrivia(source, lineEnd, end)
		}
	}
	return out
}

func buildPowerShellRecoveredIfStatement(arena *nodeArena, source []byte, lang *Language, start, end int, existing []*Node) (*Node, int) {
	ifStatementSym, ifStatementNamed, ok := powerShellSymbolMeta(lang, "if_statement")
	if !ok {
		return nil, 0
	}
	ifSym, ifNamed, ok := powerShellSymbolMeta(lang, "if")
	if !ok {
		return nil, 0
	}
	openParenSym, _, ok := powerShellSymbolMeta(lang, "(")
	if !ok {
		return nil, 0
	}
	closeParenSym, _, ok := powerShellSymbolMeta(lang, ")")
	if !ok {
		return nil, 0
	}
	elseClauseSym, elseClauseNamed, ok := powerShellSymbolMeta(lang, "else_clause")
	if !ok {
		return nil, 0
	}
	elseSym, elseNamed, ok := powerShellSymbolMeta(lang, "else")
	if !ok {
		return nil, 0
	}
	openParen := powerShellSkipInlineSpace(source, start+len("if"), end)
	if openParen >= end || source[openParen] != '(' {
		return nil, 0
	}
	closeParen := findMatchingDelimitedByte(source, openParen, end, '(', ')')
	if closeParen < 0 {
		return nil, 0
	}
	blockOpen := powerShellSkipTrivia(source, closeParen+1, end)
	if blockOpen >= end || source[blockOpen] != '{' {
		return nil, 0
	}
	blockClose := findMatchingBraceByte(source, blockOpen, end)
	if blockClose < 0 {
		return nil, 0
	}
	condPipeline := powerShellReuseExactNode(existing, lang, "pipeline", uint32(openParen+1), uint32(closeParen))
	reusedCond := condPipeline != nil
	if condPipeline == nil {
		condPipeline = buildPowerShellRecoveredConditionPipeline(arena, source, lang, openParen+1, closeParen)
	}
	if condPipeline == nil {
		return nil, 0
	}
	thenBlock := powerShellReuseExactNode(existing, lang, "statement_block", uint32(blockOpen), uint32(blockClose+1))
	reusedThenBlock := thenBlock != nil
	if thenBlock == nil {
		thenBlock = buildPowerShellRecoveredStatementBlock(arena, source, lang, blockOpen, blockClose)
	}
	if thenBlock == nil {
		return nil, 0
	}
	children := make([]*Node, 0, 6)
	children = append(children,
		newLeafNodeInArena(arena, ifSym, ifNamed, uint32(start), uint32(start+len("if")), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:start+len("if")])),
		newLeafNodeInArena(arena, openParenSym, false, uint32(openParen), uint32(openParen+1), advancePointByBytes(Point{}, source[:openParen]), advancePointByBytes(advancePointByBytes(Point{}, source[:openParen]), source[openParen:openParen+1])),
		condPipeline,
		newLeafNodeInArena(arena, closeParenSym, false, uint32(closeParen), uint32(closeParen+1), advancePointByBytes(Point{}, source[:closeParen]), advancePointByBytes(advancePointByBytes(Point{}, source[:closeParen]), source[closeParen:closeParen+1])),
		thenBlock,
	)
	next := powerShellSkipTrivia(source, blockClose+1, end)
	if powerShellKeywordAt(source, next, "else") {
		elseStart := next
		elseBlockOpen := powerShellSkipTrivia(source, elseStart+len("else"), end)
		if elseBlockOpen >= end || source[elseBlockOpen] != '{' {
			return nil, 0
		}
		elseBlockClose := findMatchingBraceByte(source, elseBlockOpen, end)
		if elseBlockClose < 0 {
			return nil, 0
		}
		elseBlock := buildPowerShellRecoveredStatementBlock(arena, source, lang, elseBlockOpen, elseBlockClose)
		if elseBlock == nil {
			return nil, 0
		}
		elseChildren := []*Node{
			newLeafNodeInArena(arena, elseSym, elseNamed, uint32(elseStart), uint32(elseStart+len("else")), advancePointByBytes(Point{}, source[:elseStart]), advancePointByBytes(advancePointByBytes(Point{}, source[:elseStart]), source[elseStart:elseStart+len("else")])),
			elseBlock,
		}
		if arena != nil {
			buf := arena.allocNodeSlice(len(elseChildren))
			copy(buf, elseChildren)
			elseChildren = buf
		}
		children = append(children, newParentNodeInArena(arena, elseClauseSym, elseClauseNamed, elseChildren, nil, 0))
		next = elseBlockClose + 1
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	stmt := newParentNodeInArena(arena, ifStatementSym, ifStatementNamed, children, nil, 0)
	for fieldIdx, fieldName := range lang.FieldNames {
		switch fieldName {
		case "condition":
			ensureNodeFieldStorage(stmt, len(stmt.children))
			stmt.fieldIDs[2] = FieldID(fieldIdx)
			stmt.fieldSources[2] = fieldSourceDirect
		case "else_clause":
			if len(stmt.children) > 5 && stmt.children[5] != nil && stmt.children[5].Type(lang) == "else_clause" {
				ensureNodeFieldStorage(stmt, len(stmt.children))
				stmt.fieldIDs[5] = FieldID(fieldIdx)
				stmt.fieldSources[5] = fieldSourceDirect
			}
		}
	}
	if reusedCond || reusedThenBlock {
		stmt.hasError = true
	}
	return stmt, next
}

func buildPowerShellRecoveredStatementBlock(arena *nodeArena, source []byte, lang *Language, openBracePos, closeBracePos int) *Node {
	statementBlockSym, statementBlockNamed, ok := powerShellSymbolMeta(lang, "statement_block")
	if !ok {
		return nil
	}
	openBraceSym, _, ok := powerShellSymbolMeta(lang, "{")
	if !ok {
		return nil
	}
	closeBraceSym, _, ok := powerShellSymbolMeta(lang, "}")
	if !ok {
		return nil
	}
	statementListSym, statementListNamed, ok := powerShellSymbolMeta(lang, "statement_list")
	if !ok {
		return nil
	}
	inner := buildPowerShellRecoveredStatements(arena, source, lang, openBracePos+1, closeBracePos, nil)
	blockChildren := make([]*Node, 0, len(inner)+2)
	blockChildren = append(blockChildren, newLeafNodeInArena(arena, openBraceSym, false, uint32(openBracePos), uint32(openBracePos+1), advancePointByBytes(Point{}, source[:openBracePos]), advancePointByBytes(advancePointByBytes(Point{}, source[:openBracePos]), source[openBracePos:openBracePos+1])))
	leadingComments := 0
	for leadingComments < len(inner) && inner[leadingComments] != nil && inner[leadingComments].Type(lang) == "comment" {
		blockChildren = append(blockChildren, inner[leadingComments])
		leadingComments++
	}
	if leadingComments < len(inner) {
		stmtChildren := inner[leadingComments:]
		if arena != nil {
			buf := arena.allocNodeSlice(len(stmtChildren))
			copy(buf, stmtChildren)
			stmtChildren = buf
		}
		blockChildren = append(blockChildren, newParentNodeInArena(arena, statementListSym, statementListNamed, stmtChildren, nil, 0))
	}
	blockChildren = append(blockChildren, newLeafNodeInArena(arena, closeBraceSym, false, uint32(closeBracePos), uint32(closeBracePos+1), advancePointByBytes(Point{}, source[:closeBracePos]), advancePointByBytes(advancePointByBytes(Point{}, source[:closeBracePos]), source[closeBracePos:closeBracePos+1])))
	if arena != nil {
		buf := arena.allocNodeSlice(len(blockChildren))
		copy(buf, blockChildren)
		blockChildren = buf
	}
	block := newParentNodeInArena(arena, statementBlockSym, statementBlockNamed, blockChildren, nil, 0)
	for i, child := range block.children {
		if child == nil || child.Type(lang) != "statement_list" {
			continue
		}
		for fieldIdx, fieldName := range lang.FieldNames {
			if fieldName != "statement_list" {
				continue
			}
			ensureNodeFieldStorage(block, len(block.children))
			block.fieldIDs[i] = FieldID(fieldIdx)
			block.fieldSources[i] = fieldSourceDirect
			break
		}
		break
	}
	return block
}

func buildPowerShellRecoveredTryStatement(arena *nodeArena, source []byte, lang *Language, start, end int) (*Node, int) {
	tryStatementSym, tryStatementNamed, ok := powerShellSymbolMeta(lang, "try_statement")
	if !ok {
		return nil, 0
	}
	trySym, tryNamed, ok := powerShellSymbolMeta(lang, "try")
	if !ok {
		return nil, 0
	}
	catchClausesSym, catchClausesNamed, ok := powerShellSymbolMeta(lang, "catch_clauses")
	if !ok {
		return nil, 0
	}
	blockOpen := powerShellSkipTrivia(source, start+len("try"), end)
	if blockOpen >= end || source[blockOpen] != '{' {
		return nil, 0
	}
	blockClose := findMatchingBraceByte(source, blockOpen, end)
	if blockClose < 0 {
		return nil, 0
	}
	tryBlock := buildPowerShellRecoveredStatementBlock(arena, source, lang, blockOpen, blockClose)
	if tryBlock == nil {
		return nil, 0
	}
	catchStart := powerShellSkipTrivia(source, blockClose+1, end)
	if !powerShellKeywordAt(source, catchStart, "catch") {
		return nil, 0
	}
	catchClause, next := buildPowerShellRecoveredCatchClause(arena, source, lang, catchStart, end)
	if catchClause == nil || next <= catchStart {
		return nil, 0
	}
	catchChildren := []*Node{catchClause}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = catchClause
		catchChildren = buf
	}
	children := []*Node{
		newLeafNodeInArena(arena, trySym, tryNamed, uint32(start), uint32(start+len("try")), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:start+len("try")])),
		tryBlock,
		newParentNodeInArena(arena, catchClausesSym, catchClausesNamed, catchChildren, nil, 0),
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	return newParentNodeInArena(arena, tryStatementSym, tryStatementNamed, children, nil, 0), next
}

func buildPowerShellRecoveredCatchClause(arena *nodeArena, source []byte, lang *Language, start, end int) (*Node, int) {
	catchClauseSym, catchClauseNamed, ok := powerShellSymbolMeta(lang, "catch_clause")
	if !ok {
		return nil, 0
	}
	catchSym, catchNamed, ok := powerShellSymbolMeta(lang, "catch")
	if !ok {
		return nil, 0
	}
	catchTypeListSym, catchTypeListNamed, ok := powerShellSymbolMeta(lang, "catch_type_list")
	if !ok {
		return nil, 0
	}
	typeLiteralSym, typeLiteralNamed, ok := powerShellSymbolMeta(lang, "type_literal")
	if !ok {
		return nil, 0
	}
	openBracketSym, _, ok := powerShellSymbolMeta(lang, "[")
	if !ok {
		return nil, 0
	}
	closeBracketSym, _, ok := powerShellSymbolMeta(lang, "]")
	if !ok {
		return nil, 0
	}
	typeOpen := powerShellSkipInlineSpace(source, start+len("catch"), end)
	if typeOpen >= end || source[typeOpen] != '[' {
		return nil, 0
	}
	typeClose := findMatchingDelimitedByte(source, typeOpen, end, '[', ']')
	if typeClose < 0 {
		return nil, 0
	}
	typeCoreStart, typeCoreEnd := powerShellTrimInlineSpace(source, typeOpen+1, typeClose)
	if typeCoreStart >= typeCoreEnd {
		return nil, 0
	}
	typeSpec := buildPowerShellTypeSpec(arena, source, lang, typeCoreStart, typeCoreEnd)
	if typeSpec == nil {
		return nil, 0
	}
	typeLiteralChildren := []*Node{
		newLeafNodeInArena(arena, openBracketSym, false, uint32(typeOpen), uint32(typeOpen+1), advancePointByBytes(Point{}, source[:typeOpen]), advancePointByBytes(advancePointByBytes(Point{}, source[:typeOpen]), source[typeOpen:typeOpen+1])),
		typeSpec,
		newLeafNodeInArena(arena, closeBracketSym, false, uint32(typeClose), uint32(typeClose+1), advancePointByBytes(Point{}, source[:typeClose]), advancePointByBytes(advancePointByBytes(Point{}, source[:typeClose]), source[typeClose:typeClose+1])),
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(typeLiteralChildren))
		copy(buf, typeLiteralChildren)
		typeLiteralChildren = buf
	}
	typeListChildren := []*Node{newParentNodeInArena(arena, typeLiteralSym, typeLiteralNamed, typeLiteralChildren, nil, 0)}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = typeListChildren[0]
		typeListChildren = buf
	}
	blockOpen := powerShellSkipTrivia(source, typeClose+1, end)
	if blockOpen >= end || source[blockOpen] != '{' {
		return nil, 0
	}
	blockClose := findMatchingBraceByte(source, blockOpen, end)
	if blockClose < 0 {
		return nil, 0
	}
	block := buildPowerShellRecoveredStatementBlock(arena, source, lang, blockOpen, blockClose)
	if block == nil {
		return nil, 0
	}
	children := []*Node{
		newLeafNodeInArena(arena, catchSym, catchNamed, uint32(start), uint32(start+len("catch")), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:start+len("catch")])),
		newParentNodeInArena(arena, catchTypeListSym, catchTypeListNamed, typeListChildren, nil, 0),
		block,
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	return newParentNodeInArena(arena, catchClauseSym, catchClauseNamed, children, nil, 0), blockClose + 1
}

func buildPowerShellRecoveredFlowControlStatement(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	flowControlSym, flowControlNamed, ok := powerShellSymbolMeta(lang, "flow_control_statement")
	if !ok {
		return nil
	}
	throwSym, throwNamed, ok := powerShellSymbolMeta(lang, "throw")
	if !ok {
		return nil
	}
	valueStart := powerShellSkipInlineSpace(source, start+len("throw"), end)
	valueEnd := powerShellTrimInlineSpaceRight(source, valueStart, end)
	if valueStart >= valueEnd {
		return nil
	}
	pipeline := buildPowerShellRecoveredConditionPipeline(arena, source, lang, valueStart, valueEnd)
	if pipeline == nil {
		return nil
	}
	children := []*Node{
		newLeafNodeInArena(arena, throwSym, throwNamed, uint32(start), uint32(start+len("throw")), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:start+len("throw")])),
		pipeline,
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	return newParentNodeInArena(arena, flowControlSym, flowControlNamed, children, nil, 0)
}

func buildPowerShellRecoveredPipeline(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	if lang == nil || start >= end {
		return nil
	}
	if powerShellFindAssignmentByte(source, start, end) >= 0 {
		return buildPowerShellRecoveredAssignmentPipeline(arena, source, lang, start, end)
	}
	pipelineSym, ok := symbolByName(lang, "pipeline")
	if !ok {
		return nil
	}
	pipelineChainSym, ok := symbolByName(lang, "pipeline_chain")
	if !ok {
		return nil
	}
	commandSym, ok := symbolByName(lang, "command")
	if !ok {
		return nil
	}
	commandNameSym, ok := symbolByName(lang, "command_name")
	if !ok {
		return nil
	}
	commandElementsSym, ok := symbolByName(lang, "command_elements")
	if !ok {
		return nil
	}
	commandArgSepSym, ok := symbolByName(lang, "command_argument_sep")
	if !ok {
		return nil
	}
	commandParameterSym, ok := symbolByName(lang, "command_parameter")
	if !ok {
		return nil
	}
	arrayLiteralSym, ok := symbolByName(lang, "array_literal_expression")
	if !ok {
		return nil
	}
	unaryExprSym, ok := symbolByName(lang, "unary_expression")
	if !ok {
		return nil
	}
	variableSym, ok := symbolByName(lang, "variable")
	if !ok {
		return nil
	}
	stringLiteralSym, ok := symbolByName(lang, "string_literal")
	if !ok {
		return nil
	}
	expandableStringSym, ok := symbolByName(lang, "expandable_string_literal")
	if !ok {
		return nil
	}
	genericTokenSym, ok := symbolByName(lang, "generic_token")
	if !ok {
		return nil
	}
	spaceSym, ok := symbolByName(lang, " ")
	if !ok {
		return nil
	}
	return buildPowerShellPipelineFromLine(arena, source, lang, start, end, pipelineSym, pipelineChainSym, commandSym, commandNameSym, commandElementsSym, commandArgSepSym, commandParameterSym, arrayLiteralSym, unaryExprSym, variableSym, stringLiteralSym, expandableStringSym, genericTokenSym, spaceSym)
}

func buildPowerShellRecoveredAssignmentPipeline(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	pipelineSym, pipelineNamed, ok := powerShellSymbolMeta(lang, "pipeline")
	if !ok {
		return nil
	}
	assignmentExprSym, assignmentExprNamed, ok := powerShellSymbolMeta(lang, "assignment_expression")
	if !ok {
		return nil
	}
	assignOpSym, assignOpNamed, ok := powerShellSymbolMeta(lang, "assignement_operator")
	if !ok {
		return nil
	}
	assignLeafSym, assignLeafNamed, ok := powerShellSymbolMeta(lang, "=")
	if !ok {
		assignLeafSym = assignOpSym
		assignLeafNamed = assignOpNamed
	}
	eq := powerShellFindAssignmentByte(source, start, end)
	if eq < 0 {
		return nil
	}
	lhsStart, lhsEnd := powerShellTrimInlineSpace(source, start, eq)
	rhsStart, rhsEnd := powerShellTrimInlineSpace(source, eq+1, end)
	if lhsStart >= lhsEnd || rhsStart >= rhsEnd {
		return nil
	}
	lhs := buildPowerShellLeftAssignmentExpression(arena, source, lang, lhsStart, lhsEnd)
	if lhs == nil {
		return nil
	}
	assignChildren := []*Node{newLeafNodeInArena(arena, assignLeafSym, assignLeafNamed, uint32(eq), uint32(eq+1), advancePointByBytes(Point{}, source[:eq]), advancePointByBytes(advancePointByBytes(Point{}, source[:eq]), source[eq:eq+1]))}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = assignChildren[0]
		assignChildren = buf
	}
	assignOp := newParentNodeInArena(arena, assignOpSym, assignOpNamed, assignChildren, nil, 0)
	rhs := buildPowerShellRecoveredConditionPipeline(arena, source, lang, rhsStart, rhsEnd)
	if rhs == nil {
		rhs = buildPowerShellRecoveredPipeline(arena, source, lang, rhsStart, rhsEnd)
	}
	if rhs == nil {
		return nil
	}
	children := []*Node{lhs, assignOp, rhs}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	assignExpr := newParentNodeInArena(arena, assignmentExprSym, assignmentExprNamed, children, nil, 0)
	for fieldIdx, fieldName := range lang.FieldNames {
		if fieldName != "value" {
			continue
		}
		ensureNodeFieldStorage(assignExpr, len(assignExpr.children))
		assignExpr.fieldIDs[2] = FieldID(fieldIdx)
		assignExpr.fieldSources[2] = fieldSourceDirect
		break
	}
	pipelineChildren := []*Node{assignExpr}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = assignExpr
		pipelineChildren = buf
	}
	return newParentNodeInArena(arena, pipelineSym, pipelineNamed, pipelineChildren, nil, 0)
}

func buildPowerShellLeftAssignmentExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	if _, _, ok := powerShellSymbolMeta(lang, "left_assignment_expression"); !ok {
		return nil
	}
	core := buildPowerShellExpressionCore(arena, source, lang, start, end)
	if core == nil {
		return nil
	}
	return wrapPowerShellExpression(arena, lang, core, "unary_expression", "array_literal_expression", "range_expression", "format_expression", "multiplicative_expression", "additive_expression", "comparison_expression", "bitwise_expression", "logical_expression", "left_assignment_expression")
}

func buildPowerShellRecoveredConditionPipeline(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	pipelineSym, pipelineNamed, ok := powerShellSymbolMeta(lang, "pipeline")
	if !ok {
		return nil
	}
	pipelineChainSym, pipelineChainNamed, ok := powerShellSymbolMeta(lang, "pipeline_chain")
	if !ok {
		return nil
	}
	if powerShellLooksLikeCommandText(source, start, end) {
		if pipeline := buildPowerShellRecoveredPipeline(arena, source, lang, start, end); pipeline != nil {
			return pipeline
		}
	}
	logical := buildPowerShellLogicalExpression(arena, source, lang, start, end)
	if logical == nil {
		return nil
	}
	chainChildren := []*Node{logical}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = logical
		chainChildren = buf
	}
	chain := newParentNodeInArena(arena, pipelineChainSym, pipelineChainNamed, chainChildren, nil, 0)
	pipelineChildren := []*Node{chain}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = chain
		pipelineChildren = buf
	}
	return newParentNodeInArena(arena, pipelineSym, pipelineNamed, pipelineChildren, nil, 0)
}

func buildPowerShellLogicalExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	logicalSym, logicalNamed, ok := powerShellSymbolMeta(lang, "logical_expression")
	if !ok {
		return nil
	}
	bitwise := buildPowerShellBitwiseExpression(arena, source, lang, start, end)
	if bitwise == nil {
		return nil
	}
	children := []*Node{bitwise}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = bitwise
		children = buf
	}
	return newParentNodeInArena(arena, logicalSym, logicalNamed, children, nil, 0)
}

func buildPowerShellBitwiseExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	bitwiseSym, bitwiseNamed, ok := powerShellSymbolMeta(lang, "bitwise_expression")
	if !ok {
		return nil
	}
	comparison := buildPowerShellComparisonExpression(arena, source, lang, start, end)
	if comparison == nil {
		return nil
	}
	children := []*Node{comparison}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = comparison
		children = buf
	}
	return newParentNodeInArena(arena, bitwiseSym, bitwiseNamed, children, nil, 0)
}

func buildPowerShellComparisonExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	comparisonSym, comparisonNamed, ok := powerShellSymbolMeta(lang, "comparison_expression")
	if !ok {
		return nil
	}
	additive := buildPowerShellAdditiveExpression(arena, source, lang, start, end)
	if additive == nil {
		return nil
	}
	children := []*Node{additive}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = additive
		children = buf
	}
	return newParentNodeInArena(arena, comparisonSym, comparisonNamed, children, nil, 0)
}

func buildPowerShellAdditiveExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	additiveSym, additiveNamed, ok := powerShellSymbolMeta(lang, "additive_expression")
	if !ok {
		return nil
	}
	start, end = powerShellTrimInlineSpace(source, start, end)
	if start >= end {
		return nil
	}
	if plus := powerShellFindTopLevelPlus(source, start, end); plus >= 0 {
		left := buildPowerShellAdditiveExpression(arena, source, lang, start, plus)
		right := buildPowerShellMultiplicativeExpression(arena, source, lang, plus+1, end)
		plusSym, plusNamed, ok := powerShellSymbolMeta(lang, "+")
		if !ok || left == nil || right == nil {
			return nil
		}
		children := []*Node{
			left,
			newLeafNodeInArena(arena, plusSym, plusNamed, uint32(plus), uint32(plus+1), advancePointByBytes(Point{}, source[:plus]), advancePointByBytes(advancePointByBytes(Point{}, source[:plus]), source[plus:plus+1])),
			right,
		}
		if arena != nil {
			buf := arena.allocNodeSlice(len(children))
			copy(buf, children)
			children = buf
		}
		return newParentNodeInArena(arena, additiveSym, additiveNamed, children, nil, 0)
	}
	multiplicative := buildPowerShellMultiplicativeExpression(arena, source, lang, start, end)
	if multiplicative == nil {
		return nil
	}
	children := []*Node{multiplicative}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = multiplicative
		children = buf
	}
	return newParentNodeInArena(arena, additiveSym, additiveNamed, children, nil, 0)
}

func buildPowerShellMultiplicativeExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	multiplicativeSym, multiplicativeNamed, ok := powerShellSymbolMeta(lang, "multiplicative_expression")
	if !ok {
		return nil
	}
	format := buildPowerShellFormatExpression(arena, source, lang, start, end)
	if format == nil {
		return nil
	}
	children := []*Node{format}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = format
		children = buf
	}
	return newParentNodeInArena(arena, multiplicativeSym, multiplicativeNamed, children, nil, 0)
}

func buildPowerShellFormatExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	formatSym, formatNamed, ok := powerShellSymbolMeta(lang, "format_expression")
	if !ok {
		return nil
	}
	rng := buildPowerShellRangeExpression(arena, source, lang, start, end)
	if rng == nil {
		return nil
	}
	children := []*Node{rng}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = rng
		children = buf
	}
	return newParentNodeInArena(arena, formatSym, formatNamed, children, nil, 0)
}

func buildPowerShellRangeExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	rangeSym, rangeNamed, ok := powerShellSymbolMeta(lang, "range_expression")
	if !ok {
		return nil
	}
	array := buildPowerShellArrayLiteralExpression(arena, source, lang, start, end)
	if array == nil {
		return nil
	}
	children := []*Node{array}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = array
		children = buf
	}
	return newParentNodeInArena(arena, rangeSym, rangeNamed, children, nil, 0)
}

func buildPowerShellArrayLiteralExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	arraySym, arrayNamed, ok := powerShellSymbolMeta(lang, "array_literal_expression")
	if !ok {
		return nil
	}
	unary := buildPowerShellUnaryExpression(arena, source, lang, start, end)
	if unary == nil {
		return nil
	}
	children := []*Node{unary}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = unary
		children = buf
	}
	return newParentNodeInArena(arena, arraySym, arrayNamed, children, nil, 0)
}

func buildPowerShellUnaryExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	unarySym, unaryNamed, ok := powerShellSymbolMeta(lang, "unary_expression")
	if !ok {
		return nil
	}
	core := buildPowerShellExpressionCore(arena, source, lang, start, end)
	if core == nil {
		return nil
	}
	children := []*Node{core}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = core
		children = buf
	}
	return newParentNodeInArena(arena, unarySym, unaryNamed, children, nil, 0)
}

func buildPowerShellExpressionCore(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	start, end = powerShellTrimInlineSpace(source, start, end)
	if start >= end {
		return nil
	}
	switch source[start] {
	case '!':
		exprUnarySym, exprUnaryNamed, ok := powerShellSymbolMeta(lang, "expression_with_unary_operator")
		if !ok {
			return nil
		}
		bangSym, bangNamed, ok := powerShellSymbolMeta(lang, "!")
		if !ok {
			return nil
		}
		innerStart := powerShellSkipInlineSpace(source, start+1, end)
		innerCore := buildPowerShellExpressionCore(arena, source, lang, innerStart, end)
		if innerCore == nil {
			return nil
		}
		innerUnary := wrapPowerShellExpression(arena, lang, innerCore, "unary_expression")
		if innerUnary == nil {
			return nil
		}
		children := []*Node{
			newLeafNodeInArena(arena, bangSym, bangNamed, uint32(start), uint32(start+1), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:start+1])),
			innerUnary,
		}
		if arena != nil {
			buf := arena.allocNodeSlice(len(children))
			copy(buf, children)
			children = buf
		}
		return newParentNodeInArena(arena, exprUnarySym, exprUnaryNamed, children, nil, 0)
	case '(':
		return buildPowerShellParenthesizedExpression(arena, source, lang, start, end)
	case '"':
		stringLiteralSym, stringLiteralNamed, ok := powerShellSymbolMeta(lang, "string_literal")
		if !ok {
			return nil
		}
		expandable := buildPowerShellExpandableStringLiteral(arena, source, lang, start, end)
		if expandable == nil {
			return nil
		}
		children := []*Node{expandable}
		if arena != nil {
			buf := arena.allocNodeSlice(1)
			buf[0] = expandable
			children = buf
		}
		return newParentNodeInArena(arena, stringLiteralSym, stringLiteralNamed, children, nil, 0)
	case '$':
		variableSym, variableNamed, ok := powerShellSymbolMeta(lang, "variable")
		if !ok {
			return nil
		}
		if bytes.IndexAny(source[start:end], " \t") >= 0 {
			genericSym, genericNamed, ok := powerShellSymbolMeta(lang, "generic_token")
			if !ok {
				return nil
			}
			return newLeafNodeInArena(arena, genericSym, genericNamed, uint32(start), uint32(end), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:end]))
		}
		return newLeafNodeInArena(arena, variableSym, variableNamed, uint32(start), uint32(end), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:end]))
	case '[':
		if bytes.Contains(source[start:end], []byte("::")) {
			if end > start && source[end-1] == ')' {
				if inv := buildPowerShellInvokationExpression(arena, source, lang, start, end); inv != nil {
					return inv
				}
			}
			if member := buildPowerShellMemberAccessExpression(arena, source, lang, start, end); member != nil {
				return member
			}
		}
		genericSym, genericNamed, ok := powerShellSymbolMeta(lang, "generic_token")
		if !ok {
			return nil
		}
		return newLeafNodeInArena(arena, genericSym, genericNamed, uint32(start), uint32(end), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:end]))
	default:
		genericSym, genericNamed, ok := powerShellSymbolMeta(lang, "generic_token")
		if !ok {
			return nil
		}
		return newLeafNodeInArena(arena, genericSym, genericNamed, uint32(start), uint32(end), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:end]))
	}
}

func buildPowerShellParenthesizedExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	parenthesizedSym, parenthesizedNamed, ok := powerShellSymbolMeta(lang, "parenthesized_expression")
	if !ok {
		return nil
	}
	openParenSym, _, ok := powerShellSymbolMeta(lang, "(")
	if !ok {
		return nil
	}
	closeParenSym, _, ok := powerShellSymbolMeta(lang, ")")
	if !ok {
		return nil
	}
	if end-start < 2 || source[start] != '(' || source[end-1] != ')' {
		return nil
	}
	innerStart, innerEnd := powerShellTrimInlineSpace(source, start+1, end-1)
	innerIsCommand := innerStart < innerEnd && powerShellLooksLikeCommandText(source, innerStart, innerEnd)
	var inner *Node
	if innerStart < innerEnd {
		if innerIsCommand {
			inner = buildPowerShellRecoveredPipeline(arena, source, lang, innerStart, innerEnd)
		}
		if inner == nil {
			inner = buildPowerShellRecoveredConditionPipeline(arena, source, lang, innerStart, innerEnd)
		}
	}
	children := make([]*Node, 0, 3)
	children = append(children, newLeafNodeInArena(arena, openParenSym, false, uint32(start), uint32(start+1), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:start+1])))
	if inner != nil {
		children = append(children, inner)
	}
	children = append(children, newLeafNodeInArena(arena, closeParenSym, false, uint32(end-1), uint32(end), advancePointByBytes(Point{}, source[:end-1]), advancePointByBytes(advancePointByBytes(Point{}, source[:end-1]), source[end-1:end])))
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	node := newParentNodeInArena(arena, parenthesizedSym, parenthesizedNamed, children, nil, 0)
	if !innerIsCommand {
		node.hasError = true
	}
	return node
}

func buildPowerShellInvokationExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	invocationSym, invocationNamed, ok := powerShellSymbolMeta(lang, "invokation_expression")
	if !ok {
		return nil
	}
	typeClose := findMatchingDelimitedByte(source, start, end, '[', ']')
	if typeClose < 0 {
		return nil
	}
	memberStart := typeClose + 1
	if memberStart+2 >= end || source[memberStart] != ':' || source[memberStart+1] != ':' {
		return nil
	}
	nameStart := memberStart + 2
	openParen := findMatchingPowerShellToken(source, nameStart, end, '(')
	if openParen < 0 {
		return nil
	}
	closeParen := findMatchingDelimitedByte(source, openParen, end, '(', ')')
	if closeParen != end-1 {
		return nil
	}
	typeLiteral := buildPowerShellTypeLiteral(arena, source, lang, start, typeClose+1)
	memberName := buildPowerShellMemberName(arena, source, lang, nameStart, openParen)
	argumentList := buildPowerShellArgumentList(arena, source, lang, openParen, closeParen+1)
	colonColonSym, colonColonNamed, ok := powerShellSymbolMeta(lang, "::")
	if !ok || typeLiteral == nil || memberName == nil || argumentList == nil {
		return nil
	}
	children := []*Node{
		typeLiteral,
		newLeafNodeInArena(arena, colonColonSym, colonColonNamed, uint32(memberStart), uint32(memberStart+2), advancePointByBytes(Point{}, source[:memberStart]), advancePointByBytes(advancePointByBytes(Point{}, source[:memberStart]), source[memberStart:memberStart+2])),
		memberName,
		argumentList,
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	return newParentNodeInArena(arena, invocationSym, invocationNamed, children, nil, 0)
}

func buildPowerShellMemberAccessExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	memberAccessSym, memberAccessNamed, ok := powerShellSymbolMeta(lang, "member_access")
	if !ok {
		return nil
	}
	typeClose := findMatchingDelimitedByte(source, start, end, '[', ']')
	if typeClose < 0 {
		return nil
	}
	memberStart := typeClose + 1
	if memberStart+2 > end || source[memberStart] != ':' || source[memberStart+1] != ':' {
		return nil
	}
	nameStart := memberStart + 2
	typeLiteral := buildPowerShellTypeLiteral(arena, source, lang, start, typeClose+1)
	memberName := buildPowerShellMemberName(arena, source, lang, nameStart, end)
	colonColonSym, colonColonNamed, ok := powerShellSymbolMeta(lang, "::")
	if !ok || typeLiteral == nil || memberName == nil {
		return nil
	}
	children := []*Node{
		typeLiteral,
		newLeafNodeInArena(arena, colonColonSym, colonColonNamed, uint32(memberStart), uint32(memberStart+2), advancePointByBytes(Point{}, source[:memberStart]), advancePointByBytes(advancePointByBytes(Point{}, source[:memberStart]), source[memberStart:memberStart+2])),
		memberName,
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	return newParentNodeInArena(arena, memberAccessSym, memberAccessNamed, children, nil, 0)
}

func buildPowerShellTypeLiteral(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	typeLiteralSym, typeLiteralNamed, ok := powerShellSymbolMeta(lang, "type_literal")
	if !ok {
		return nil
	}
	openBracketSym, openBracketNamed, ok := powerShellSymbolMeta(lang, "[")
	if !ok {
		return nil
	}
	closeBracketSym, closeBracketNamed, ok := powerShellSymbolMeta(lang, "]")
	if !ok {
		return nil
	}
	typeSpec := buildPowerShellTypeSpec(arena, source, lang, start+1, end-1)
	if typeSpec == nil {
		return nil
	}
	children := make([]*Node, 0, 4)
	children = append(children, newLeafNodeInArena(arena, openBracketSym, openBracketNamed, uint32(start), uint32(start+1), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:start+1])))
	children = append(children, typeSpec)
	if plus := powerShellFindTopLevelPlus(source, start+1, end-1); plus >= 0 {
		plusSym, plusNamed, ok := powerShellSymbolMeta(lang, "+")
		if !ok {
			return nil
		}
		errChildren := []*Node{
			newLeafNodeInArena(arena, plusSym, plusNamed, uint32(plus), uint32(plus+1), advancePointByBytes(Point{}, source[:plus]), advancePointByBytes(advancePointByBytes(Point{}, source[:plus]), source[plus:plus+1])),
		}
		if simpleName := buildPowerShellSimpleName(arena, source, lang, plus+1, end-1); simpleName != nil {
			errChildren = append(errChildren, simpleName)
		}
		if arena != nil {
			buf := arena.allocNodeSlice(len(errChildren))
			copy(buf, errChildren)
			errChildren = buf
		}
		errNode := newParentNodeInArena(arena, errorSymbol, true, errChildren, nil, 0)
		errNode.hasError = true
		errNode.isExtra = true
		children = append(children, errNode)
	}
	children = append(children, newLeafNodeInArena(arena, closeBracketSym, closeBracketNamed, uint32(end-1), uint32(end), advancePointByBytes(Point{}, source[:end-1]), advancePointByBytes(advancePointByBytes(Point{}, source[:end-1]), source[end-1:end])))
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	node := newParentNodeInArena(arena, typeLiteralSym, typeLiteralNamed, children, nil, 0)
	if len(children) == 4 {
		node.hasError = true
	}
	return node
}

func buildPowerShellTypeSpec(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	typeSpecSym, typeSpecNamed, ok := powerShellSymbolMeta(lang, "type_spec")
	if !ok {
		return nil
	}
	if plus := powerShellFindTopLevelPlus(source, start, end); plus >= 0 {
		end = plus
	}
	typeName := buildPowerShellTypeName(arena, source, lang, start, end)
	if typeName == nil {
		return nil
	}
	children := []*Node{typeName}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = typeName
		children = buf
	}
	return newParentNodeInArena(arena, typeSpecSym, typeSpecNamed, children, nil, 0)
}

func buildPowerShellTypeName(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	typeNameSym, typeNameNamed, ok := powerShellSymbolMeta(lang, "type_name")
	if !ok {
		return nil
	}
	typeIdentifierSym, typeIdentifierNamed, ok := powerShellSymbolMeta(lang, "type_identifier")
	if !ok {
		return nil
	}
	if dot := bytes.LastIndexByte(source[start:end], '.'); dot >= 0 {
		dot += start
		left := buildPowerShellTypeName(arena, source, lang, start, dot)
		right := newLeafNodeInArena(arena, typeIdentifierSym, typeIdentifierNamed, uint32(dot+1), uint32(end), advancePointByBytes(Point{}, source[:dot+1]), advancePointByBytes(advancePointByBytes(Point{}, source[:dot+1]), source[dot+1:end]))
		dotSym, dotNamed, ok := powerShellSymbolMeta(lang, ".")
		if !ok || left == nil {
			return nil
		}
		children := []*Node{
			left,
			newLeafNodeInArena(arena, dotSym, dotNamed, uint32(dot), uint32(dot+1), advancePointByBytes(Point{}, source[:dot]), advancePointByBytes(advancePointByBytes(Point{}, source[:dot]), source[dot:dot+1])),
			right,
		}
		if arena != nil {
			buf := arena.allocNodeSlice(len(children))
			copy(buf, children)
			children = buf
		}
		return newParentNodeInArena(arena, typeNameSym, typeNameNamed, children, nil, 0)
	}
	leaf := newLeafNodeInArena(arena, typeIdentifierSym, typeIdentifierNamed, uint32(start), uint32(end), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:end]))
	children := []*Node{leaf}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = leaf
		children = buf
	}
	return newParentNodeInArena(arena, typeNameSym, typeNameNamed, children, nil, 0)
}

func buildPowerShellMemberName(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	memberNameSym, memberNameNamed, ok := powerShellSymbolMeta(lang, "member_name")
	if !ok {
		return nil
	}
	simpleName := buildPowerShellSimpleName(arena, source, lang, start, end)
	if simpleName == nil {
		return nil
	}
	children := []*Node{simpleName}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = simpleName
		children = buf
	}
	return newParentNodeInArena(arena, memberNameSym, memberNameNamed, children, nil, 0)
}

func buildPowerShellSimpleName(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	simpleNameSym, simpleNameNamed, ok := powerShellSymbolMeta(lang, "simple_name")
	if !ok {
		return nil
	}
	leaf := newLeafNodeInArena(arena, simpleNameSym, simpleNameNamed, uint32(start), uint32(end), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:end]))
	return leaf
}

func buildPowerShellArgumentList(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	argumentListSym, argumentListNamed, ok := powerShellSymbolMeta(lang, "argument_list")
	if !ok {
		return nil
	}
	argumentExprListSym, argumentExprListNamed, ok := powerShellSymbolMeta(lang, "argument_expression_list")
	if !ok {
		return nil
	}
	openParenSym, openParenNamed, ok := powerShellSymbolMeta(lang, "(")
	if !ok {
		return nil
	}
	closeParenSym, closeParenNamed, ok := powerShellSymbolMeta(lang, ")")
	if !ok {
		return nil
	}
	argStart, argEnd := powerShellTrimInlineSpace(source, start+1, end-1)
	argument := buildPowerShellArgumentExpression(arena, source, lang, argStart, argEnd)
	if argument == nil {
		return nil
	}
	listChildren := []*Node{argument}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = argument
		listChildren = buf
	}
	argumentListChildren := []*Node{
		newLeafNodeInArena(arena, openParenSym, openParenNamed, uint32(start), uint32(start+1), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:start+1])),
		newParentNodeInArena(arena, argumentExprListSym, argumentExprListNamed, listChildren, nil, 0),
		newLeafNodeInArena(arena, closeParenSym, closeParenNamed, uint32(end-1), uint32(end), advancePointByBytes(Point{}, source[:end-1]), advancePointByBytes(advancePointByBytes(Point{}, source[:end-1]), source[end-1:end])),
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(argumentListChildren))
		copy(buf, argumentListChildren)
		argumentListChildren = buf
	}
	argList := newParentNodeInArena(arena, argumentListSym, argumentListNamed, argumentListChildren, nil, 0)
	for fieldIdx, fieldName := range lang.FieldNames {
		if fieldName != "argument_expression_list" {
			continue
		}
		ensureNodeFieldStorage(argList, len(argList.children))
		argList.fieldIDs[1] = FieldID(fieldIdx)
		argList.fieldSources[1] = fieldSourceDirect
		break
	}
	return argList
}

func buildPowerShellArgumentExpression(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	argumentExprSym, argumentExprNamed, ok := powerShellSymbolMeta(lang, "argument_expression")
	if !ok {
		return nil
	}
	logicalArgSym, logicalArgNamed, ok := powerShellSymbolMeta(lang, "logical_argument_expression")
	if !ok {
		return nil
	}
	bitwiseArgSym, bitwiseArgNamed, ok := powerShellSymbolMeta(lang, "bitwise_argument_expression")
	if !ok {
		return nil
	}
	comparisonArgSym, comparisonArgNamed, ok := powerShellSymbolMeta(lang, "comparison_argument_expression")
	if !ok {
		return nil
	}
	additiveArgSym, additiveArgNamed, ok := powerShellSymbolMeta(lang, "additive_argument_expression")
	if !ok {
		return nil
	}
	multiplicativeArgSym, multiplicativeArgNamed, ok := powerShellSymbolMeta(lang, "multiplicative_argument_expression")
	if !ok {
		return nil
	}
	formatArgSym, formatArgNamed, ok := powerShellSymbolMeta(lang, "format_argument_expression")
	if !ok {
		return nil
	}
	rangeArgSym, rangeArgNamed, ok := powerShellSymbolMeta(lang, "range_argument_expression")
	if !ok {
		return nil
	}
	core := buildPowerShellExpressionCore(arena, source, lang, start, end)
	if core == nil {
		return nil
	}
	unary := wrapPowerShellExpression(arena, lang, core, "unary_expression")
	rangeArg := newParentNodeInArena(arena, rangeArgSym, rangeArgNamed, []*Node{rangeToArenaChild(arena, unary)}, nil, 0)
	formatArg := newParentNodeInArena(arena, formatArgSym, formatArgNamed, []*Node{rangeToArenaChild(arena, rangeArg)}, nil, 0)
	multiplicativeArg := newParentNodeInArena(arena, multiplicativeArgSym, multiplicativeArgNamed, []*Node{rangeToArenaChild(arena, formatArg)}, nil, 0)
	additiveArg := newParentNodeInArena(arena, additiveArgSym, additiveArgNamed, []*Node{rangeToArenaChild(arena, multiplicativeArg)}, nil, 0)
	comparisonArg := newParentNodeInArena(arena, comparisonArgSym, comparisonArgNamed, []*Node{rangeToArenaChild(arena, additiveArg)}, nil, 0)
	bitwiseArg := newParentNodeInArena(arena, bitwiseArgSym, bitwiseArgNamed, []*Node{rangeToArenaChild(arena, comparisonArg)}, nil, 0)
	logicalArg := newParentNodeInArena(arena, logicalArgSym, logicalArgNamed, []*Node{rangeToArenaChild(arena, bitwiseArg)}, nil, 0)
	children := []*Node{logicalArg}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = logicalArg
		children = buf
	}
	return newParentNodeInArena(arena, argumentExprSym, argumentExprNamed, children, nil, 0)
}

func rangeToArenaChild(arena *nodeArena, child *Node) *Node {
	return child
}

func findMatchingPowerShellToken(source []byte, start, end int, target byte) int {
	for i := start; i < end; i++ {
		if source[i] == target {
			return i
		}
	}
	return -1
}

func wrapPowerShellExpression(arena *nodeArena, lang *Language, core *Node, types ...string) *Node {
	if core == nil || lang == nil {
		return nil
	}
	node := core
	for _, typeName := range types {
		sym, named, ok := powerShellSymbolMeta(lang, typeName)
		if !ok {
			return nil
		}
		children := []*Node{node}
		if arena != nil {
			buf := arena.allocNodeSlice(1)
			buf[0] = node
			children = buf
		}
		node = newParentNodeInArena(arena, sym, named, children, nil, 0)
	}
	return node
}

func powerShellReuseExactNode(nodes []*Node, lang *Language, typ string, start, end uint32) *Node {
	for _, node := range nodes {
		if node == nil || node.Type(lang) != typ {
			continue
		}
		if node.startByte == start && node.endByte == end {
			return node
		}
	}
	return nil
}

func powerShellSymbolMeta(lang *Language, name string) (Symbol, bool, bool) {
	if lang == nil {
		return 0, false, false
	}
	sym, ok := symbolByName(lang, name)
	if !ok {
		return 0, false, false
	}
	named := false
	if int(sym) < len(lang.SymbolMetadata) {
		named = lang.SymbolMetadata[sym].Named
	}
	return sym, named, true
}

func powerShellKeywordAt(source []byte, pos int, kw string) bool {
	if pos < 0 || pos+len(kw) > len(source) || !bytes.HasPrefix(source[pos:], []byte(kw)) {
		return false
	}
	if pos > 0 {
		if prev := source[pos-1]; (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') || prev == '_' {
			return false
		}
	}
	if pos+len(kw) < len(source) {
		if next := source[pos+len(kw)]; (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || next == '_' {
			return false
		}
	}
	return true
}

func powerShellSkipTrivia(source []byte, start, end int) int {
	for start < end {
		switch source[start] {
		case ' ', '\t', '\r', '\n':
			start++
		default:
			return start
		}
	}
	return start
}

func powerShellSkipInlineSpace(source []byte, start, end int) int {
	for start < end && (source[start] == ' ' || source[start] == '\t') {
		start++
	}
	return start
}

func powerShellTrimInlineSpace(source []byte, start, end int) (int, int) {
	start = powerShellSkipInlineSpace(source, start, end)
	return start, powerShellTrimInlineSpaceRight(source, start, end)
}

func powerShellTrimInlineSpaceRight(source []byte, start, end int) int {
	for end > start && (source[end-1] == ' ' || source[end-1] == '\t') {
		end--
	}
	return end
}

func powerShellLineEnd(source []byte, start, end int) int {
	for start < end && source[start] != '\n' {
		start++
	}
	return start
}

func powerShellFindAssignmentByte(source []byte, start, end int) int {
	inString := false
	depthParen := 0
	depthBracket := 0
	for i := start; i < end; i++ {
		switch source[i] {
		case '"':
			if !isEscapedQuote(source, uint32(i)) {
				inString = !inString
			}
		case '(':
			if !inString {
				depthParen++
			}
		case ')':
			if !inString && depthParen > 0 {
				depthParen--
			}
		case '[':
			if !inString {
				depthBracket++
			}
		case ']':
			if !inString && depthBracket > 0 {
				depthBracket--
			}
		case '=':
			if !inString && depthParen == 0 && depthBracket == 0 {
				return i
			}
		}
	}
	return -1
}

func powerShellFindTopLevelPlus(source []byte, start, end int) int {
	inString := false
	depthParen := 0
	depthBracket := 0
	for i := start; i < end; i++ {
		switch source[i] {
		case '"':
			if !isEscapedQuote(source, uint32(i)) {
				inString = !inString
			}
		case '(':
			if !inString {
				depthParen++
			}
		case ')':
			if !inString && depthParen > 0 {
				depthParen--
			}
		case '[':
			if !inString {
				depthBracket++
			}
		case ']':
			if !inString && depthBracket > 0 {
				depthBracket--
			}
		case '+':
			if !inString && depthParen == 0 && depthBracket == 0 {
				return i
			}
		}
	}
	return -1
}

func powerShellLooksLikeCommandText(source []byte, start, end int) bool {
	start, end = powerShellTrimInlineSpace(source, start, end)
	if start >= end {
		return false
	}
	switch source[start] {
	case '$', '"', '!', '(':
		return false
	}
	if !((source[start] >= 'a' && source[start] <= 'z') || (source[start] >= 'A' && source[start] <= 'Z') || source[start] == '_') {
		return false
	}
	return bytes.IndexAny(source[start:end], " \t") >= 0
}

func findMatchingDelimitedByte(source []byte, openPos, limit int, open, close byte) int {
	if openPos < 0 || openPos >= len(source) {
		return -1
	}
	if limit > len(source) {
		limit = len(source)
	}
	depth := 0
	inString := false
	for i := openPos; i < limit; i++ {
		switch source[i] {
		case '"':
			if !isEscapedQuote(source, uint32(i)) {
				inString = !inString
			}
		default:
			if inString {
				continue
			}
			if source[i] == open {
				depth++
			} else if source[i] == close {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

func buildPowerShellTrailingPipelines(arena *nodeArena, source []byte, lang *Language, start, end uint32, pipelineSym, pipelineChainSym, commandSym, commandNameSym, commandElementsSym, commandArgSepSym, commandParameterSym, arrayLiteralSym, unaryExprSym, variableSym, stringLiteralSym, expandableStringSym, genericTokenSym, spaceSym Symbol) []*Node {
	out := make([]*Node, 0, 4)
	i := int(start)
	limit := int(end)
	for i < limit {
		for i < limit && (source[i] == ' ' || source[i] == '\t' || source[i] == '\r' || source[i] == '\n') {
			i++
		}
		if i >= limit {
			break
		}
		lineStart := i
		for i < limit && source[i] != '\n' {
			i++
		}
		lineEnd := i
		if pipeline := buildPowerShellPipelineFromLine(arena, source, lang, lineStart, lineEnd, pipelineSym, pipelineChainSym, commandSym, commandNameSym, commandElementsSym, commandArgSepSym, commandParameterSym, arrayLiteralSym, unaryExprSym, variableSym, stringLiteralSym, expandableStringSym, genericTokenSym, spaceSym); pipeline != nil {
			out = append(out, pipeline)
		}
	}
	if arena != nil && len(out) > 0 {
		buf := arena.allocNodeSlice(len(out))
		copy(buf, out)
		out = buf
	}
	return out
}

func buildPowerShellPipelineFromLine(arena *nodeArena, source []byte, lang *Language, start, end int, pipelineSym, pipelineChainSym, commandSym, commandNameSym, commandElementsSym, commandArgSepSym, commandParameterSym, arrayLiteralSym, unaryExprSym, variableSym, stringLiteralSym, expandableStringSym, genericTokenSym, spaceSym Symbol) *Node {
	if start >= end {
		return nil
	}
	commandNameEnd := start
	for commandNameEnd < end && source[commandNameEnd] != ' ' && source[commandNameEnd] != '\t' {
		commandNameEnd++
	}
	if commandNameEnd == start {
		return nil
	}
	commandNameStartPoint := advancePointByBytes(Point{}, source[:start])
	commandNameEndPoint := advancePointByBytes(commandNameStartPoint, source[start:commandNameEnd])
	commandNameNamed := int(commandNameSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[commandNameSym].Named
	commandName := newLeafNodeInArena(arena, commandNameSym, commandNameNamed, uint32(start), uint32(commandNameEnd), commandNameStartPoint, commandNameEndPoint)

	commandChildren := []*Node{commandName}
	elements := buildPowerShellCommandElements(arena, source, lang, commandNameEnd, end, commandElementsSym, commandArgSepSym, commandParameterSym, arrayLiteralSym, unaryExprSym, variableSym, stringLiteralSym, expandableStringSym, genericTokenSym, spaceSym)
	if elements != nil {
		commandChildren = append(commandChildren, elements)
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(commandChildren))
		copy(buf, commandChildren)
		commandChildren = buf
	}
	commandNamed := int(commandSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[commandSym].Named
	command := newParentNodeInArena(arena, commandSym, commandNamed, commandChildren, nil, 0)
	command.endByte = uint32(end)
	command.endPoint = advancePointByBytes(command.startPoint, source[start:end])
	for fieldIdx, fieldName := range lang.FieldNames {
		switch fieldName {
		case "command_name":
			ensureNodeFieldStorage(command, len(command.children))
			command.fieldIDs[0] = FieldID(fieldIdx)
			command.fieldSources[0] = fieldSourceDirect
		case "command_elements":
			if len(command.children) > 1 {
				ensureNodeFieldStorage(command, len(command.children))
				command.fieldIDs[1] = FieldID(fieldIdx)
				command.fieldSources[1] = fieldSourceDirect
			}
		}
	}

	chainChildren := []*Node{command}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = command
		chainChildren = buf
	}
	pipelineChainNamed := int(pipelineChainSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[pipelineChainSym].Named
	chain := newParentNodeInArena(arena, pipelineChainSym, pipelineChainNamed, chainChildren, nil, 0)
	pipelineChildren := []*Node{chain}
	if arena != nil {
		buf := arena.allocNodeSlice(1)
		buf[0] = chain
		pipelineChildren = buf
	}
	pipelineNamed := int(pipelineSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[pipelineSym].Named
	return newParentNodeInArena(arena, pipelineSym, pipelineNamed, pipelineChildren, nil, 0)
}

func buildPowerShellCommandElements(arena *nodeArena, source []byte, lang *Language, start, end int, commandElementsSym, commandArgSepSym, commandParameterSym, arrayLiteralSym, unaryExprSym, variableSym, stringLiteralSym, expandableStringSym, genericTokenSym, spaceSym Symbol) *Node {
	children := make([]*Node, 0, 8)
	i := start
	for i < end {
		sepStart := i
		for i < end && (source[i] == ' ' || source[i] == '\t') {
			i++
		}
		if i == sepStart {
			break
		}
		sepLeafStart := advancePointByBytes(Point{}, source[:sepStart])
		sepLeafEnd := advancePointByBytes(sepLeafStart, source[sepStart:i])
		spaceLeaf := newLeafNodeInArena(arena, spaceSym, false, uint32(sepStart), uint32(i), sepLeafStart, sepLeafEnd)
		sepChildren := []*Node{spaceLeaf}
		if arena != nil {
			buf := arena.allocNodeSlice(1)
			buf[0] = spaceLeaf
			sepChildren = buf
		}
		sepNamed := int(commandArgSepSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[commandArgSepSym].Named
		sep := newParentNodeInArena(arena, commandArgSepSym, sepNamed, sepChildren, nil, 0)
		children = append(children, sep)

		tokenStart := i
		tokenEnd := powerShellTokenEnd(source, i, end)
		if tokenEnd <= tokenStart {
			break
		}
		children = append(children, buildPowerShellCommandElement(arena, source, lang, tokenStart, tokenEnd, commandParameterSym, arrayLiteralSym, unaryExprSym, variableSym, stringLiteralSym, expandableStringSym, genericTokenSym))
		i = tokenEnd
	}
	if len(children) == 0 {
		return nil
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	elementsNamed := int(commandElementsSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[commandElementsSym].Named
	return newParentNodeInArena(arena, commandElementsSym, elementsNamed, children, nil, 0)
}

func buildPowerShellCommandElement(arena *nodeArena, source []byte, lang *Language, start, end int, commandParameterSym, arrayLiteralSym, unaryExprSym, variableSym, stringLiteralSym, expandableStringSym, genericTokenSym Symbol) *Node {
	startPoint := advancePointByBytes(Point{}, source[:start])
	endPoint := advancePointByBytes(startPoint, source[start:end])
	if source[start] == '-' {
		named := int(commandParameterSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[commandParameterSym].Named
		return newLeafNodeInArena(arena, commandParameterSym, named, uint32(start), uint32(end), startPoint, endPoint)
	}
	if source[start] == '$' {
		variable := buildPowerShellVariableMemberAccess(arena, source, lang, start, end)
		if variable == nil {
			variableNamed := int(variableSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[variableSym].Named
			variable = newLeafNodeInArena(arena, variableSym, variableNamed, uint32(start), uint32(end), startPoint, endPoint)
		}
		unaryChildren := []*Node{variable}
		if arena != nil {
			buf := arena.allocNodeSlice(1)
			buf[0] = variable
			unaryChildren = buf
		}
		unaryNamed := int(unaryExprSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[unaryExprSym].Named
		unary := newParentNodeInArena(arena, unaryExprSym, unaryNamed, unaryChildren, nil, 0)
		arrayChildren := []*Node{unary}
		if arena != nil {
			buf := arena.allocNodeSlice(1)
			buf[0] = unary
			arrayChildren = buf
		}
		arrayNamed := int(arrayLiteralSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[arrayLiteralSym].Named
		return newParentNodeInArena(arena, arrayLiteralSym, arrayNamed, arrayChildren, nil, 0)
	}
	if source[start] == '(' && source[end-1] == ')' {
		parenthesized := buildPowerShellParenthesizedExpression(arena, source, lang, start, end)
		if parenthesized != nil {
			unaryChildren := []*Node{parenthesized}
			if arena != nil {
				buf := arena.allocNodeSlice(1)
				buf[0] = parenthesized
				unaryChildren = buf
			}
			unaryNamed := int(unaryExprSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[unaryExprSym].Named
			unary := newParentNodeInArena(arena, unaryExprSym, unaryNamed, unaryChildren, nil, 0)
			arrayChildren := []*Node{unary}
			if arena != nil {
				buf := arena.allocNodeSlice(1)
				buf[0] = unary
				arrayChildren = buf
			}
			arrayNamed := int(arrayLiteralSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[arrayLiteralSym].Named
			return newParentNodeInArena(arena, arrayLiteralSym, arrayNamed, arrayChildren, nil, 0)
		}
	}
	if source[start] == '"' && source[end-1] == '"' {
		expandable := buildPowerShellExpandableStringLiteralFromSymbol(arena, source, lang, start, end, expandableStringSym)
		if expandable == nil {
			return nil
		}
		stringChildren := []*Node{expandable}
		if arena != nil {
			buf := arena.allocNodeSlice(1)
			buf[0] = expandable
			stringChildren = buf
		}
		stringNamed := int(stringLiteralSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[stringLiteralSym].Named
		stringNode := newParentNodeInArena(arena, stringLiteralSym, stringNamed, stringChildren, nil, 0)
		unaryChildren := []*Node{stringNode}
		if arena != nil {
			buf := arena.allocNodeSlice(1)
			buf[0] = stringNode
			unaryChildren = buf
		}
		unaryNamed := int(unaryExprSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[unaryExprSym].Named
		unary := newParentNodeInArena(arena, unaryExprSym, unaryNamed, unaryChildren, nil, 0)
		arrayChildren := []*Node{unary}
		if arena != nil {
			buf := arena.allocNodeSlice(1)
			buf[0] = unary
			arrayChildren = buf
		}
		arrayNamed := int(arrayLiteralSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[arrayLiteralSym].Named
		return newParentNodeInArena(arena, arrayLiteralSym, arrayNamed, arrayChildren, nil, 0)
	}
	genericNamed := int(genericTokenSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[genericTokenSym].Named
	return newLeafNodeInArena(arena, genericTokenSym, genericNamed, uint32(start), uint32(end), startPoint, endPoint)
}

func buildPowerShellVariableMemberAccess(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	memberAccessSym, memberAccessNamed, ok := powerShellSymbolMeta(lang, "member_access")
	if !ok {
		return nil
	}
	variableSym, variableNamed, ok := powerShellSymbolMeta(lang, "variable")
	if !ok {
		return nil
	}
	backslashSym, backslashNamed, ok := powerShellSymbolMeta(lang, "\\")
	if !ok {
		return nil
	}
	dotSym, dotNamed, ok := powerShellSymbolMeta(lang, ".")
	if !ok {
		return nil
	}
	varEnd := start + 1
	for varEnd < end {
		b := source[varEnd]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' {
			varEnd++
			continue
		}
		break
	}
	if varEnd >= end || source[varEnd] != '\\' {
		return nil
	}
	dot := bytes.LastIndexByte(source[varEnd:end], '.')
	if dot < 0 {
		return nil
	}
	dot += varEnd
	variable := newLeafNodeInArena(arena, variableSym, variableNamed, uint32(start), uint32(varEnd), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:varEnd]))
	pathName := buildPowerShellSimpleName(arena, source, lang, varEnd+1, dot)
	memberName := buildPowerShellMemberName(arena, source, lang, dot+1, end)
	if pathName == nil || memberName == nil {
		return nil
	}
	errChildren := []*Node{
		newLeafNodeInArena(arena, backslashSym, backslashNamed, uint32(varEnd), uint32(varEnd+1), advancePointByBytes(Point{}, source[:varEnd]), advancePointByBytes(advancePointByBytes(Point{}, source[:varEnd]), source[varEnd:varEnd+1])),
		pathName,
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(errChildren))
		copy(buf, errChildren)
		errChildren = buf
	}
	errNode := newParentNodeInArena(arena, errorSymbol, true, errChildren, nil, 0)
	errNode.hasError = true
	errNode.isExtra = true
	children := []*Node{
		variable,
		errNode,
		newLeafNodeInArena(arena, dotSym, dotNamed, uint32(dot), uint32(dot+1), advancePointByBytes(Point{}, source[:dot]), advancePointByBytes(advancePointByBytes(Point{}, source[:dot]), source[dot:dot+1])),
		memberName,
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	return newParentNodeInArena(arena, memberAccessSym, memberAccessNamed, children, nil, 0)
}

func buildPowerShellExpandableStringLiteral(arena *nodeArena, source []byte, lang *Language, start, end int) *Node {
	expandableSym, _, ok := powerShellSymbolMeta(lang, "expandable_string_literal")
	if !ok {
		return nil
	}
	return buildPowerShellExpandableStringLiteralFromSymbol(arena, source, lang, start, end, expandableSym)
}

func buildPowerShellExpandableStringLiteralFromSymbol(arena *nodeArena, source []byte, lang *Language, start, end int, expandableSym Symbol) *Node {
	if start >= end {
		return nil
	}
	expandableNamed := int(expandableSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[expandableSym].Named
	variableSym, variableNamed, ok := powerShellSymbolMeta(lang, "variable")
	if !ok {
		return newLeafNodeInArena(arena, expandableSym, expandableNamed, uint32(start), uint32(end), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:end]))
	}
	var children []*Node
	for i := start + 1; i < end-1; i++ {
		if source[i] != '$' {
			continue
		}
		j := i + 1
		for j < end-1 {
			b := source[j]
			if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' {
				j++
				continue
			}
			break
		}
		if j == i+1 {
			continue
		}
		children = append(children, newLeafNodeInArena(arena, variableSym, variableNamed, uint32(i), uint32(j), advancePointByBytes(Point{}, source[:i]), advancePointByBytes(advancePointByBytes(Point{}, source[:i]), source[i:j])))
		i = j - 1
	}
	if len(children) == 0 {
		return newLeafNodeInArena(arena, expandableSym, expandableNamed, uint32(start), uint32(end), advancePointByBytes(Point{}, source[:start]), advancePointByBytes(advancePointByBytes(Point{}, source[:start]), source[start:end]))
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	node := newParentNodeInArena(arena, expandableSym, expandableNamed, children, nil, 0)
	node.startByte = uint32(start)
	node.endByte = uint32(end)
	node.startPoint = advancePointByBytes(Point{}, source[:start])
	node.endPoint = advancePointByBytes(node.startPoint, source[start:end])
	return node
}

func powerShellTokenEnd(source []byte, start, end int) int {
	if start >= end {
		return start
	}
	if source[start] == '"' {
		for i := start + 1; i < end; i++ {
			if source[i] == '"' && !isEscapedQuote(source, uint32(i)) {
				return i + 1
			}
		}
		return end
	}
	if source[start] == '(' {
		if close := findMatchingDelimitedByte(source, start, end, '(', ')'); close >= 0 {
			return close + 1
		}
		return end
	}
	i := start
	for i < end && source[i] != ' ' && source[i] != '\t' {
		i++
	}
	return i
}

func findMatchingBraceByte(source []byte, openPos, limit int) int {
	if openPos < 0 || openPos >= len(source) {
		return -1
	}
	if limit > len(source) {
		limit = len(source)
	}
	depth := 0
	for i := openPos; i < limit; i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func normalizeCSharpTypeConstraintKeywords(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "c_sharp" {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "type_parameter_constraint" && len(n.children) == 1 {
			child := n.children[0]
			if child != nil && child.Type(lang) == "identifier" && len(child.children) == 1 {
				inner := child.children[0]
				if inner != nil && inner.Type(lang) == "notnull" && !inner.isNamed &&
					child.startByte == inner.startByte && child.endByte == inner.endByte {
					n.children[0] = inner
					inner.parent = n
					inner.childIndex = 0
					if len(n.fieldIDs) > 0 {
						n.fieldIDs[0] = 0
					}
					if len(n.fieldSources) > 0 {
						n.fieldSources[0] = fieldSourceNone
					}
				}
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeCSharpSwitchTupleCasePatterns(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "c_sharp" {
		return
	}
	patternSym, ok := lang.SymbolByName("constant_pattern")
	if !ok {
		return
	}
	tupleExprSym, ok := lang.SymbolByName("tuple_expression")
	if !ok {
		return
	}
	named := false
	if idx := int(patternSym); idx < len(lang.SymbolMetadata) {
		named = lang.SymbolMetadata[patternSym].Named
	}
	tupleNamed := false
	if idx := int(tupleExprSym); idx < len(lang.SymbolMetadata) {
		tupleNamed = lang.SymbolMetadata[tupleExprSym].Named
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "switch_section" && len(n.children) > 1 {
			pat := n.children[1]
			if n.children[0] != nil && n.children[0].Type(lang) == "case" &&
				pat != nil && (pat.Type(lang) == "tuple_expression" || pat.Type(lang) == "recursive_pattern") {
				tuple := pat
				if pat.Type(lang) != "tuple_expression" {
					tupleChildren := append([]*Node(nil), pat.children...)
					if n.ownerArena != nil && len(tupleChildren) > 0 {
						buf := n.ownerArena.allocNodeSlice(len(tupleChildren))
						copy(buf, tupleChildren)
						tupleChildren = buf
					}
					tuple = newParentNodeInArena(n.ownerArena, tupleExprSym, tupleNamed, tupleChildren, nil, 0)
				}
				repl := newParentNodeInArena(n.ownerArena, patternSym, named, []*Node{tuple}, nil, 0)
				repl.parent = n
				repl.childIndex = 1
				n.children[1] = repl
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeElixirNestedCallTargetFields(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "elixir" {
		return
	}
	targetID, ok := lang.FieldByName("target")
	if !ok {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "call" && len(n.children) >= 2 {
			first := n.children[0]
			second := n.children[1]
			if first != nil && second != nil &&
				first.Type(lang) == "call" &&
				second.Type(lang) == "arguments" &&
				(len(n.fieldIDs) == 0 || n.fieldIDs[0] == 0) {
				if len(n.fieldIDs) < len(n.children) {
					fieldIDs := make([]FieldID, len(n.children))
					copy(fieldIDs, n.fieldIDs)
					n.fieldIDs = fieldIDs
				}
				n.fieldIDs[0] = targetID
				if len(n.fieldSources) < len(n.children) {
					fieldSources := make([]uint8, len(n.children))
					copy(fieldSources, n.fieldSources)
					n.fieldSources = fieldSources
				}
				n.fieldSources[0] = fieldSourceInherited
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizePerlJoinAssignmentLists(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "perl" {
		return
	}
	listSym, ok := lang.SymbolByName("list_expression")
	if !ok {
		return
	}
	listNamed := false
	if idx := int(listSym); idx < len(lang.SymbolMetadata) {
		listNamed = lang.SymbolMetadata[listSym].Named
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "expression_statement" && len(n.children) == 1 {
			assign := n.children[0]
			if rewritten := rewritePerlJoinAssignmentList(n.ownerArena, assign, source, lang, listSym, listNamed); rewritten != nil {
				n.children[0] = rewritten
				rewritten.parent = n
				rewritten.childIndex = 0
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func rewritePerlJoinAssignmentList(arena *nodeArena, assign *Node, source []byte, lang *Language, listSym Symbol, listNamed bool) *Node {
	if assign == nil || assign.Type(lang) != "assignment_expression" || len(assign.children) != 3 {
		return nil
	}
	call := assign.children[2]
	if call == nil || call.Type(lang) != "ambiguous_function_call_expression" || len(call.children) != 2 {
		return nil
	}
	fn := call.children[0]
	args := call.children[1]
	if fn == nil || args == nil || fn.Text(source) != "join" || args.Type(lang) != "list_expression" || len(args.children) < 3 {
		return nil
	}
	firstArg := args.children[0]
	if firstArg == nil {
		return nil
	}

	callFieldIDs := append([]FieldID(nil), call.fieldIDs...)
	if len(callFieldIDs) > 2 {
		callFieldIDs = callFieldIDs[:2]
	}
	rewrittenCall := newParentNodeInArena(arena, call.symbol, call.isNamed, []*Node{fn, firstArg}, callFieldIDs, call.productionID)
	if len(call.fieldSources) > 0 {
		rewrittenCall.fieldSources = append([]uint8(nil), call.fieldSources...)
		if len(rewrittenCall.fieldSources) > 2 {
			rewrittenCall.fieldSources = rewrittenCall.fieldSources[:2]
		}
	}

	assignFieldIDs := append([]FieldID(nil), assign.fieldIDs...)
	rewrittenAssign := newParentNodeInArena(arena, assign.symbol, assign.isNamed, []*Node{assign.children[0], assign.children[1], rewrittenCall}, assignFieldIDs, assign.productionID)
	if len(assign.fieldSources) > 0 {
		rewrittenAssign.fieldSources = append([]uint8(nil), assign.fieldSources...)
	}

	outerChildren := make([]*Node, 0, len(args.children))
	outerChildren = append(outerChildren, rewrittenAssign)
	outerChildren = append(outerChildren, args.children[1:]...)
	return newParentNodeInArena(arena, listSym, listNamed, outerChildren, nil, args.productionID)
}

func normalizePerlPushExpressionLists(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "perl" {
		return
	}
	listSym, ok := lang.SymbolByName("list_expression")
	if !ok {
		return
	}
	listNamed := false
	if idx := int(listSym); idx < len(lang.SymbolMetadata) {
		listNamed = lang.SymbolMetadata[listSym].Named
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "expression_statement" && len(n.children) == 1 {
			list := n.children[0]
			if rewritten := rewritePerlPushExpressionList(n.ownerArena, list, source, lang, listSym, listNamed); rewritten != nil {
				n.children[0] = rewritten
				rewritten.parent = n
				rewritten.childIndex = 0
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func rewritePerlPushExpressionList(arena *nodeArena, list *Node, source []byte, lang *Language, listSym Symbol, listNamed bool) *Node {
	if list == nil || list.Type(lang) != "list_expression" || len(list.children) < 3 {
		return nil
	}
	call := list.children[0]
	if call == nil || call.Type(lang) != "ambiguous_function_call_expression" || len(call.children) != 2 {
		return nil
	}
	fn := call.children[0]
	firstArg := call.children[1]
	if fn == nil || firstArg == nil || fn.Text(source) != "push" {
		return nil
	}
	argChildren := make([]*Node, 0, len(list.children))
	argChildren = append(argChildren, firstArg)
	argChildren = append(argChildren, list.children[1:]...)
	rewrittenArgs := newParentNodeInArena(arena, listSym, listNamed, argChildren, nil, list.productionID)

	callFieldIDs := append([]FieldID(nil), call.fieldIDs...)
	if len(callFieldIDs) > 2 {
		callFieldIDs = callFieldIDs[:2]
	}
	rewrittenCall := newParentNodeInArena(arena, call.symbol, call.isNamed, []*Node{fn, rewrittenArgs}, callFieldIDs, call.productionID)
	if len(call.fieldSources) > 0 {
		rewrittenCall.fieldSources = append([]uint8(nil), call.fieldSources...)
		if len(rewrittenCall.fieldSources) > 2 {
			rewrittenCall.fieldSources = rewrittenCall.fieldSources[:2]
		}
	}
	return rewrittenCall
}

func normalizePerlReturnExpressionLists(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "perl" {
		return
	}
	listSym, ok := lang.SymbolByName("list_expression")
	if !ok {
		return
	}
	listNamed := false
	if idx := int(listSym); idx < len(lang.SymbolMetadata) {
		listNamed = lang.SymbolMetadata[listSym].Named
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "expression_statement" && len(n.children) == 1 {
			ret := n.children[0]
			if rewritten := rewritePerlReturnExpressionList(n.ownerArena, ret, lang, listSym, listNamed); rewritten != nil {
				n.children[0] = rewritten
				rewritten.parent = n
				rewritten.childIndex = 0
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func rewritePerlReturnExpressionList(arena *nodeArena, ret *Node, lang *Language, listSym Symbol, listNamed bool) *Node {
	if ret == nil || ret.Type(lang) != "return_expression" || len(ret.children) != 2 {
		return nil
	}
	list := ret.children[1]
	if list == nil || list.Type(lang) != "list_expression" || len(list.children) < 3 {
		return nil
	}
	firstItem := list.children[0]
	if firstItem == nil {
		return nil
	}

	retFieldIDs := append([]FieldID(nil), ret.fieldIDs...)
	if len(retFieldIDs) > 2 {
		retFieldIDs = retFieldIDs[:2]
	}
	rewrittenReturn := newParentNodeInArena(arena, ret.symbol, ret.isNamed, []*Node{ret.children[0], firstItem}, retFieldIDs, ret.productionID)
	if len(ret.fieldSources) > 0 {
		rewrittenReturn.fieldSources = append([]uint8(nil), ret.fieldSources...)
		if len(rewrittenReturn.fieldSources) > 2 {
			rewrittenReturn.fieldSources = rewrittenReturn.fieldSources[:2]
		}
	}

	outerChildren := make([]*Node, 0, len(list.children))
	outerChildren = append(outerChildren, rewrittenReturn)
	outerChildren = append(outerChildren, list.children[1:]...)
	return newParentNodeInArena(arena, listSym, listNamed, outerChildren, nil, list.productionID)
}

func normalizeScalaObjectTemplateBodyFragments(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" || len(root.children) < 3 || len(source) == 0 {
		return
	}
	templateBodySym, ok := symbolByName(lang, "template_body")
	if !ok {
		return
	}
	templateBodyNamed := int(templateBodySym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[templateBodySym].Named
	arena := root.ownerArena
	changed := false
	for i := 0; i+2 < len(root.children); i++ {
		obj := root.children[i]
		openBrace := scalaErrorTokenNode(root.children[i+1], "{", lang)
		if !scalaObjectNeedsTemplateBody(obj, lang) || openBrace == nil {
			continue
		}
		closeIdx := scalaFindTemplateBodyClose(root.children, i+2, lang)
		var closeByte uint32
		synthClose := false
		if closeIdx >= 0 {
			if closeNode := scalaErrorTokenNode(root.children[closeIdx], "}", lang); closeNode != nil {
				closeByte = closeNode.endByte
			}
		} else {
			matching := findMatchingBraceByte(source, int(openBrace.startByte), len(source))
			if matching < 0 {
				continue
			}
			closeByte = uint32(matching + 1)
			closeIdx = scalaFindTemplateBodyCloseByByte(root.children, i+2, closeByte)
			if closeIdx < 0 {
				continue
			}
			synthClose = true
		}
		bodyChildren, ok := scalaTemplateBodyFragmentChildren(root.children[i+1:closeIdx+1], arena, lang, source, closeByte, synthClose)
		if !ok {
			continue
		}
		replacementChildren := make([]*Node, 0, len(obj.children)+1)
		replacementChildren = append(replacementChildren, obj.children...)
		replacementChildren = append(replacementChildren, newParentNodeInArena(arena, templateBodySym, templateBodyNamed, bodyChildren, nil, 0))
		replacement := newParentNodeInArena(arena, obj.symbol, obj.isNamed, replacementChildren, obj.fieldIDs, obj.productionID)
		replaceChildRangeWithSingleNode(root, i, closeIdx+1, replacement)
		changed = true
	}
	if changed {
		populateParentNode(root, root.children)
	}
}

func normalizeScalaTemplateBodyObjectFragments(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "template_body" && len(n.children) >= 4 {
			for i := 0; i+2 < len(n.children); i++ {
				objTok := n.children[i]
				ident := n.children[i+1]
				open := n.children[i+2]
				if objTok == nil || ident == nil || open == nil || objTok.Type(lang) != "object" || ident.Type(lang) != "identifier" || open.Type(lang) != "{" {
					continue
				}
				startIdx := i
				if i > 0 {
					prev := n.children[i-1]
					if prev != nil && prev.Type(lang) == "_automatic_semicolon" && prev.startByte == objTok.startByte && prev.endByte == objTok.startByte {
						startIdx = i - 1
					}
				}
				closePos := scalaFindMatchingBraceByteWithTrivia(source, int(open.startByte), n.endByte)
				if closePos < 0 {
					continue
				}
				objectEnd := uint32(closePos + 1)
				recovered, ok := scalaRecoverTopLevelObjectNodeFromRange(source, objTok.startByte, objectEnd, lang, n.ownerArena)
				if !ok || recovered == nil {
					continue
				}
				endIdx := len(n.children)
				for j := startIdx; j < len(n.children); j++ {
					child := n.children[j]
					if child == nil {
						continue
					}
					if child.startByte >= objectEnd {
						endIdx = j
						break
					}
				}
				if endIdx <= startIdx {
					continue
				}
				replaceChildRangeWithSingleNode(n, startIdx, endIdx, recovered)
				scalaRecoverTemplateBodyTailMembers(n, recovered.endByte, source, lang)
				populateParentNode(n, n.children)
				i = startIdx
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

type scalaTemplateMemberKind uint8

const (
	scalaTemplateMemberUnknown scalaTemplateMemberKind = iota
	scalaTemplateMemberPackage
	scalaTemplateMemberClass
	scalaTemplateMemberObject
	scalaTemplateMemberTrait
	scalaTemplateMemberEnum
	scalaTemplateMemberFunction
	scalaTemplateMemberImport
	scalaTemplateMemberVal
	scalaTemplateMemberComment
	scalaTemplateMemberBlockComment
)

type scalaTemplateMemberSpan struct {
	start uint32
	end   uint32
	kind  scalaTemplateMemberKind
}

func normalizeScalaTemplateBodyRecoveredMembers(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "template_body" && n.HasError() {
			scalaRecoverTemplateBodyMembers(n, source, lang)
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeScalaRecoveredObjectTemplateBodies(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if scalaDefinitionTemplateBodyNeedsRecovery(n, lang) {
			for i, child := range n.children {
				if child == nil || child.Type(lang) != "template_body" {
					continue
				}
				rebuilt, ok := scalaRebuildTemplateBodyFromSource(child, source, lang, n.ownerArena)
				if !ok || rebuilt == nil {
					break
				}
				n.children[i] = rebuilt
				rebuilt.parent = n
				rebuilt.childIndex = i
				for cur := n; cur != nil; cur = cur.parent {
					cur.hasError = false
					populateParentNode(cur, cur.children)
				}
				break
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func scalaDefinitionTemplateBodyNeedsRecovery(n *Node, lang *Language) bool {
	if n == nil || lang == nil {
		return false
	}
	switch n.Type(lang) {
	case "object_definition", "class_definition", "trait_definition":
	default:
		return false
	}
	var body *Node
	for _, child := range n.children {
		if child != nil && child.Type(lang) == "template_body" {
			body = child
			break
		}
	}
	if body == nil || len(body.children) < 3 {
		return false
	}
	sawRepeatComment := false
	sawOpenComment := false
	sawBlockComment := false
	for _, child := range body.children {
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "{", "}":
			continue
		case "/*":
			sawOpenComment = true
			continue
		case "block_comment":
			sawBlockComment = true
			continue
		case "block_comment_repeat1":
			sawRepeatComment = true
			continue
		}
	}
	return sawRepeatComment && sawOpenComment && !sawBlockComment
}

func scalaRebuildTemplateBodyFromSource(body *Node, source []byte, lang *Language, arena *nodeArena) (*Node, bool) {
	if body == nil || lang == nil || body.Type(lang) != "template_body" || len(body.children) < 2 {
		return nil, false
	}
	open := body.children[0]
	close := body.children[len(body.children)-1]
	if open == nil || close == nil || open.Type(lang) != "{" || close.Type(lang) != "}" {
		return nil, false
	}
	children := make([]*Node, 0, len(body.children))
	children = append(children, open)
	memberStart := open.endByte
	if comment, ok := scalaBuildTemplateBodyLeadingBlockComment(source, open.endByte, close.startByte, lang, arena); ok && comment != nil {
		children = append(children, comment)
		memberStart = comment.endByte
	}
	spans := scalaTemplateBodyMemberSpans(source, memberStart, close.startByte)
	for _, span := range spans {
		recovered, ok := scalaRecoverTemplateBodyMemberNode(source, span, lang, arena)
		if !ok || recovered == nil {
			continue
		}
		children = append(children, recovered)
	}
	if len(children) < 2 {
		return nil, false
	}
	children = append(children, close)
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	return newParentNodeInArena(arena, body.symbol, body.isNamed, children, nil, body.productionID), true
}

func scalaBuildTemplateBodyLeadingBlockComment(source []byte, start, limit uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if start >= limit || int(limit) > len(source) || lang == nil {
		return nil, false
	}
	pos := int(start)
	endLimit := int(limit)
	for pos < endLimit {
		switch source[pos] {
		case ' ', '\t', '\n', '\r':
			pos++
		default:
			goto triviaDone
		}
	}
triviaDone:
	if pos+1 >= endLimit || source[pos] != '/' || source[pos+1] != '*' {
		return nil, false
	}
	closeRel := bytes.Index(source[pos+2:endLimit], []byte("*/"))
	if closeRel < 0 {
		return nil, false
	}
	closeStart := pos + 2 + closeRel
	closeEnd := closeStart + 2
	closeLeafStart := closeStart
	for closeLeafStart > pos {
		switch source[closeLeafStart-1] {
		case ' ', '\t':
			closeLeafStart--
		default:
			goto closeLeafDone
		}
	}
closeLeafDone:
	commentSym, ok := symbolByName(lang, "block_comment")
	if !ok {
		return nil, false
	}
	openSym, ok := symbolByName(lang, "/*")
	if !ok {
		return nil, false
	}
	closeSym, ok := symbolByName(lang, "*/")
	if !ok {
		return nil, false
	}
	commentNamed := int(commentSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[commentSym].Named
	openNamed := int(openSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[openSym].Named
	closeNamed := int(closeSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[closeSym].Named
	openNode := newLeafNodeInArena(
		arena,
		openSym,
		openNamed,
		uint32(pos),
		uint32(pos+2),
		advancePointByBytes(Point{}, source[:pos]),
		advancePointByBytes(Point{}, source[:pos+2]),
	)
	closeNode := newLeafNodeInArena(
		arena,
		closeSym,
		closeNamed,
		uint32(closeLeafStart),
		uint32(closeEnd),
		advancePointByBytes(Point{}, source[:closeLeafStart]),
		advancePointByBytes(Point{}, source[:closeEnd]),
	)
	children := []*Node{openNode, closeNode}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	comment := newParentNodeInArena(arena, commentSym, commentNamed, children, nil, 0)
	comment.isExtra = true
	return comment, true
}

func normalizeScalaSplitFunctionDefinitions(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "template_body" && n.HasError() {
			scalaRecoverSplitFunctionDefinition(n, source, lang)
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func scalaRecoverSplitFunctionDefinition(body *Node, source []byte, lang *Language) {
	if body == nil || lang == nil || body.Type(lang) != "template_body" || len(body.children) < 4 {
		return
	}
	for i := 0; i+2 < len(body.children); i++ {
		header := body.children[i]
		if header == nil {
			continue
		}
		switch header.Type(lang) {
		case "function_declaration", "_function_declaration":
		default:
			continue
		}
		eqIdx := i + 1
		openIdx := i + 2
		if eqIdx >= len(body.children) || openIdx >= len(body.children) {
			continue
		}
		open := body.children[openIdx]
		if open == nil || open.Type(lang) != "{" {
			continue
		}
		eqLeaf := body.children[eqIdx]
		if eqLeaf == nil {
			continue
		}
		eqToken := eqLeaf
		if eqLeaf.Type(lang) == "ERROR" {
			eqToken = scalaErrorTokenNode(eqLeaf, "=", lang)
		}
		if eqToken == nil || eqToken.Type(lang) != "=" {
			continue
		}
		closePos := scalaFindMatchingBraceByteWithTrivia(source, int(open.startByte), body.endByte)
		if closePos < 0 {
			continue
		}
		recovered, ok := scalaRecoverSplitFunctionDefinitionFromRange(source, header.startByte, uint32(closePos+1), lang, body.ownerArena)
		if !ok || recovered == nil {
			continue
		}
		startIdx, endIdx, ok := scalaTemplateBodyChildRange(body.children, header.startByte, uint32(closePos+1))
		if !ok {
			continue
		}
		replaceChildRangeWithSingleNode(body, startIdx, endIdx, recovered)
		for n := body; n != nil; n = n.parent {
			n.hasError = false
			populateParentNode(n, n.children)
		}
		return
	}
}

func scalaRecoverTemplateBodyMembers(body *Node, source []byte, lang *Language) {
	if body == nil || lang == nil || body.Type(lang) != "template_body" || len(body.children) < 3 {
		return
	}
	open := body.children[0]
	close := body.children[len(body.children)-1]
	if open == nil || close == nil || open.Type(lang) != "{" || close.Type(lang) != "}" {
		return
	}
	spans := scalaTemplateBodyMemberSpans(source, open.endByte, close.startByte)
	if len(spans) == 0 {
		return
	}
	changed := false
	for _, span := range spans {
		recovered, ok := scalaRecoverTemplateBodyMemberNode(source, span, lang, body.ownerArena)
		if !ok || recovered == nil {
			continue
		}
		startIdx, endIdx, ok := scalaTemplateBodyChildRange(body.children, span.start, span.end)
		if !ok {
			continue
		}
		replaceChildRangeWithSingleNode(body, startIdx, endIdx, recovered)
		changed = true
	}
	if !changed {
		return
	}
	for n := body; n != nil; n = n.parent {
		n.hasError = false
		populateParentNode(n, n.children)
	}
}

func scalaTemplateBodyChildRange(children []*Node, start, end uint32) (int, int, bool) {
	startIdx := -1
	endIdx := -1
	for i, child := range children {
		if child == nil {
			continue
		}
		if startIdx < 0 && (child.startByte >= start || child.endByte > start) {
			startIdx = i
		}
		if startIdx >= 0 && child.startByte >= end {
			endIdx = i
			break
		}
	}
	if startIdx < 0 {
		return 0, 0, false
	}
	if endIdx < 0 {
		endIdx = len(children)
	}
	if endIdx <= startIdx {
		return 0, 0, false
	}
	return startIdx, endIdx, true
}

func scalaRecoverTemplateBodyMemberNode(source []byte, span scalaTemplateMemberSpan, lang *Language, arena *nodeArena) (*Node, bool) {
	if span.end <= span.start || int(span.end) > len(source) {
		return nil, false
	}
	switch span.kind {
	case scalaTemplateMemberClass:
		return scalaRecoverTopLevelClassNodeFromRange(source, span.start, span.end, lang, arena)
	case scalaTemplateMemberObject:
		return scalaRecoverTopLevelObjectNodeFromRange(source, span.start, span.end, lang, arena)
	case scalaTemplateMemberFunction:
		return scalaRecoverTopLevelFunctionNodeFromRange(source, span.start, span.end, lang, arena)
	case scalaTemplateMemberImport:
		return scalaRecoverTopLevelNamedNodeFromRange(source, span.start, span.end, lang, arena, "import_declaration")
	case scalaTemplateMemberVal:
		return scalaRecoverTopLevelNamedNodeFromRange(source, span.start, span.end, lang, arena, "val_definition")
	case scalaTemplateMemberComment:
		return scalaRecoverTopLevelNamedNodeFromRange(source, span.start, span.end, lang, arena, "comment")
	case scalaTemplateMemberBlockComment:
		if comment, ok := scalaBuildTemplateBodyLeadingBlockComment(source, span.start, span.end, lang, arena); ok && comment != nil {
			return comment, true
		}
		return scalaRecoverTopLevelNamedNodeFromRange(source, span.start, span.end, lang, arena, "block_comment")
	default:
		return nil, false
	}
}

func scalaRecoverTemplateBodyTailMembers(body *Node, start uint32, source []byte, lang *Language) {
	if body == nil || lang == nil || body.Type(lang) != "template_body" || len(body.children) < 2 {
		return
	}
	closeIdx := len(body.children) - 1
	close := body.children[closeIdx]
	if close == nil || close.Type(lang) != "}" || start >= close.startByte {
		return
	}
	for i := 0; i < closeIdx; i++ {
		child := body.children[i]
		if child != nil && child.startByte >= start && !child.IsExtra() {
			return
		}
	}
	spans := scalaTemplateBodyMemberSpans(source, start, close.startByte)
	if len(spans) == 0 {
		return
	}
	recovered := make([]*Node, 0, len(spans))
	for _, span := range spans {
		node, ok := scalaRecoverTemplateBodyMemberNode(source, span, lang, body.ownerArena)
		if !ok || node == nil {
			continue
		}
		recovered = append(recovered, node)
	}
	if len(recovered) == 0 {
		return
	}
	newChildren := make([]*Node, 0, len(body.children)+len(recovered))
	newChildren = append(newChildren, body.children[:closeIdx]...)
	newChildren = append(newChildren, recovered...)
	newChildren = append(newChildren, body.children[closeIdx:]...)
	body.children = newChildren
	if len(body.fieldIDs) > 0 {
		fieldIDs := make([]FieldID, 0, len(body.children))
		fieldIDs = append(fieldIDs, body.fieldIDs[:closeIdx]...)
		for range recovered {
			fieldIDs = append(fieldIDs, 0)
		}
		fieldIDs = append(fieldIDs, body.fieldIDs[closeIdx:]...)
		body.fieldIDs = fieldIDs
	}
	if len(body.fieldSources) > 0 {
		fieldSources := make([]uint8, 0, len(body.children))
		fieldSources = append(fieldSources, body.fieldSources[:closeIdx]...)
		for range recovered {
			fieldSources = append(fieldSources, fieldSourceNone)
		}
		fieldSources = append(fieldSources, body.fieldSources[closeIdx:]...)
		body.fieldSources = fieldSources
	}
	for i, child := range body.children {
		if child == nil {
			continue
		}
		child.parent = body
		child.childIndex = i
	}
}

func scalaTemplateBodyMemberSpans(source []byte, bodyStart, bodyEnd uint32) []scalaTemplateMemberSpan {
	if bodyStart >= bodyEnd || int(bodyEnd) > len(source) {
		return nil
	}
	var spans []scalaTemplateMemberSpan
	pos := int(bodyStart)
	limit := int(bodyEnd)
	for pos < limit {
		start, kind, ok := scalaFindNextTemplateBodyMemberStart(source, pos, limit)
		if !ok {
			break
		}
		end := scalaFindTemplateBodyMemberEnd(source, start, limit)
		if end <= start {
			pos = start + 1
			continue
		}
		spans = append(spans, scalaTemplateMemberSpan{
			start: uint32(start),
			end:   uint32(end),
			kind:  kind,
		})
		pos = end
	}
	return spans
}

func scalaFindNextTemplateBodyMemberStart(source []byte, pos, limit int) (int, scalaTemplateMemberKind, bool) {
	braceDepth := 0
	parenDepth := 0
	bracketDepth := 0
	inLineComment := false
	inBlockComment := false
	var stringQuote byte
	tripleQuote := false
	lineStart := true
	for i := pos; i < limit; i++ {
		ch := source[i]
		next := byte(0)
		if i+1 < limit {
			next = source[i+1]
		}
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				lineStart = true
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
				continue
			}
			if ch == '\n' {
				lineStart = true
			}
			continue
		}
		if stringQuote != 0 {
			if tripleQuote {
				if i+2 < limit && source[i] == stringQuote && source[i+1] == stringQuote && source[i+2] == stringQuote {
					stringQuote = 0
					tripleQuote = false
					i += 2
				}
				continue
			}
			if ch == '\\' {
				i++
				continue
			}
			if ch == stringQuote {
				stringQuote = 0
			}
			continue
		}
		if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 && ch == '/' {
			switch next {
			case '/':
				return i, scalaTemplateMemberComment, true
			case '*':
				return i, scalaTemplateMemberBlockComment, true
			}
		}
		if lineStart {
			j := skipHorizontalTrivia(source, i, limit)
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				if kind, ok := scalaTemplateMemberKindAt(source, j, limit); ok {
					return j, kind, true
				}
			}
			lineStart = false
		}
		switch {
		case ch == '/' && next == '/':
			inLineComment = true
			i++
			continue
		case ch == '/' && next == '*':
			inBlockComment = true
			i++
			continue
		case ch == '"' || ch == '\'':
			if i+2 < limit && source[i+1] == ch && source[i+2] == ch {
				stringQuote = ch
				tripleQuote = true
				i += 2
				continue
			}
			stringQuote = ch
			tripleQuote = false
			continue
		case ch == '{':
			braceDepth++
		case ch == '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case ch == '(':
			parenDepth++
		case ch == ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case ch == '[':
			bracketDepth++
		case ch == ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		}
		if ch == '\n' {
			lineStart = true
		}
	}
	return 0, scalaTemplateMemberUnknown, false
}

func scalaFindTemplateBodyMemberEnd(source []byte, start, limit int) int {
	if start+1 < limit && source[start] == '/' {
		switch source[start+1] {
		case '/':
			end := start + 2
			for end < limit && source[end] != '\n' && source[end] != '\r' {
				end++
			}
			return trimTrailingHorizontalAndVerticalTrivia(source, start, end)
		case '*':
			end := start + 2
			for end+1 < limit {
				if source[end] == '*' && source[end+1] == '/' {
					end += 2
					return trimTrailingHorizontalAndVerticalTrivia(source, start, end)
				}
				end++
			}
			return trimTrailingHorizontalAndVerticalTrivia(source, start, limit)
		}
	}
	braceDepth := 0
	parenDepth := 0
	bracketDepth := 0
	inLineComment := false
	inBlockComment := false
	var stringQuote byte
	tripleQuote := false
	lineStart := false
	for i := start + 1; i < limit; i++ {
		ch := source[i]
		next := byte(0)
		if i+1 < limit {
			next = source[i+1]
		}
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				lineStart = true
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
				continue
			}
			if ch == '\n' {
				lineStart = true
			}
			continue
		}
		if stringQuote != 0 {
			if tripleQuote {
				if i+2 < limit && source[i] == stringQuote && source[i+1] == stringQuote && source[i+2] == stringQuote {
					stringQuote = 0
					tripleQuote = false
					i += 2
				}
				continue
			}
			if ch == '\\' {
				i++
				continue
			}
			if ch == stringQuote {
				stringQuote = 0
			}
			continue
		}
		if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 && ch == '/' && (next == '/' || next == '*') {
			return trimTrailingHorizontalAndVerticalTrivia(source, start, i)
		}
		if lineStart {
			j := skipHorizontalTrivia(source, i, limit)
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				switch {
				case j < limit && source[j] == '}':
					return j
				case j+1 < limit && source[j] == '/' && (source[j+1] == '/' || source[j+1] == '*'):
					return trimTrailingHorizontalAndVerticalTrivia(source, start, i)
				default:
					if _, ok := scalaTemplateMemberKindAt(source, j, limit); ok {
						return trimTrailingHorizontalAndVerticalTrivia(source, start, i)
					}
				}
			}
			lineStart = false
		}
		switch {
		case ch == '/' && next == '/':
			inLineComment = true
			i++
			continue
		case ch == '/' && next == '*':
			inBlockComment = true
			i++
			continue
		case ch == '"' || ch == '\'':
			if i+2 < limit && source[i+1] == ch && source[i+2] == ch {
				stringQuote = ch
				tripleQuote = true
				i += 2
				continue
			}
			stringQuote = ch
			tripleQuote = false
			continue
		case ch == '{':
			braceDepth++
		case ch == '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case ch == '(':
			parenDepth++
		case ch == ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case ch == '[':
			bracketDepth++
		case ch == ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		}
		if ch == '\n' {
			lineStart = true
		}
	}
	return trimTrailingHorizontalAndVerticalTrivia(source, start, limit)
}

func scalaTemplateMemberKindAt(source []byte, pos, limit int) (scalaTemplateMemberKind, bool) {
	if pos >= limit {
		return scalaTemplateMemberUnknown, false
	}
	switch {
	case bytes.HasPrefix(source[pos:limit], []byte("private lazy val ")):
		return scalaTemplateMemberVal, true
	case bytes.HasPrefix(source[pos:limit], []byte("lazy val ")):
		return scalaTemplateMemberVal, true
	case bytes.HasPrefix(source[pos:limit], []byte("private val ")):
		return scalaTemplateMemberVal, true
	case bytes.HasPrefix(source[pos:limit], []byte("override val ")):
		return scalaTemplateMemberVal, true
	case bytes.HasPrefix(source[pos:limit], []byte("val ")):
		return scalaTemplateMemberVal, true
	case bytes.HasPrefix(source[pos:limit], []byte("implicit class ")):
		return scalaTemplateMemberClass, true
	case bytes.HasPrefix(source[pos:limit], []byte("final class ")):
		return scalaTemplateMemberClass, true
	case bytes.HasPrefix(source[pos:limit], []byte("class ")):
		return scalaTemplateMemberClass, true
	case bytes.HasPrefix(source[pos:limit], []byte("object ")):
		return scalaTemplateMemberObject, true
	case bytes.HasPrefix(source[pos:limit], []byte("import ")):
		return scalaTemplateMemberImport, true
	case pos < limit && source[pos] == '@':
		return scalaTemplateMemberFunction, true
	case bytes.HasPrefix(source[pos:limit], []byte("private def ")):
		return scalaTemplateMemberFunction, true
	case bytes.HasPrefix(source[pos:limit], []byte("override def ")):
		return scalaTemplateMemberFunction, true
	case bytes.HasPrefix(source[pos:limit], []byte("def ")):
		return scalaTemplateMemberFunction, true
	default:
		return scalaTemplateMemberUnknown, false
	}
}

func skipHorizontalTrivia(source []byte, pos, limit int) int {
	for pos < limit {
		switch source[pos] {
		case ' ', '\t':
			pos++
		default:
			return pos
		}
	}
	return pos
}

func trimTrailingHorizontalAndVerticalTrivia(source []byte, start, end int) int {
	if end > len(source) {
		end = len(source)
	}
	for end > start {
		switch source[end-1] {
		case ' ', '\t', '\n', '\r', '\f':
			end--
		default:
			return end
		}
	}
	return end
}

type scalaStatementSpan struct {
	start uint32
	end   uint32
}

func scalaRecoverSplitFunctionDefinitionFromRange(source []byte, fnStart, fnEnd uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || int(fnStart) >= len(source) || fnEnd <= fnStart || int(fnEnd) > len(source) {
		return nil, false
	}
	tree, err := parseWithSnippetParser(lang, source[fnStart:fnEnd])
	if err != nil || tree == nil || tree.RootNode() == nil {
		return nil, false
	}
	defer tree.Release()
	startPoint := advancePointByBytes(Point{}, source[:fnStart])
	offsetRoot := tree.RootNodeWithOffset(fnStart, startPoint)
	if offsetRoot == nil || offsetRoot.ChildCount() < 3 {
		return nil, false
	}
	header := offsetRoot.Child(0)
	eqLeaf := offsetRoot.Child(1)
	open := offsetRoot.Child(2)
	if header == nil || open == nil || open.Type(lang) != "{" {
		return nil, false
	}
	switch header.Type(lang) {
	case "function_declaration", "_function_declaration":
	default:
		return nil, false
	}
	if eqLeaf == nil || eqLeaf.Type(lang) == "ERROR" {
		if eqLeaf == nil {
			return nil, false
		}
		eqLeaf = scalaErrorTokenNode(eqLeaf, "=", lang)
	}
	if eqLeaf == nil || eqLeaf.Type(lang) != "=" {
		return nil, false
	}
	closePos := scalaFindMatchingBraceByteWithTrivia(source, int(open.startByte), fnEnd)
	if closePos < 0 {
		return nil, false
	}
	block, ok := scalaRecoverFunctionBlockFromRange(source, open.startByte, uint32(closePos+1), lang, arena)
	if !ok || block == nil {
		return nil, false
	}
	functionSym, ok := symbolByName(lang, "function_definition")
	if !ok {
		return nil, false
	}
	functionNamed := int(functionSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[functionSym].Named
	children := make([]*Node, 0, len(header.children)+2)
	for _, child := range header.children {
		if child == nil {
			continue
		}
		children = append(children, cloneTreeNodesIntoArena(child, arena))
	}
	children = append(children, cloneTreeNodesIntoArena(eqLeaf, arena))
	children = append(children, block)
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	return newParentNodeInArena(arena, functionSym, functionNamed, children, nil, 0), true
}

func scalaRecoverFunctionBlockFromRange(source []byte, blockStart, blockEnd uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || blockEnd <= blockStart || int(blockEnd) > len(source) {
		return nil, false
	}
	blockSym, ok := symbolByName(lang, "block")
	if !ok {
		return nil, false
	}
	blockNamed := int(blockSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[blockSym].Named
	openSym, ok := symbolByName(lang, "{")
	if !ok {
		return nil, false
	}
	openNamed := int(openSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[openSym].Named
	closeSym, ok := symbolByName(lang, "}")
	if !ok {
		return nil, false
	}
	closeNamed := int(closeSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[closeSym].Named
	open := newLeafNodeInArena(arena, openSym, openNamed, blockStart, blockStart+1, advancePointByBytes(Point{}, source[:blockStart]), advancePointByBytes(Point{}, source[:blockStart+1]))
	close := newLeafNodeInArena(arena, closeSym, closeNamed, blockEnd-1, blockEnd, advancePointByBytes(Point{}, source[:blockEnd-1]), advancePointByBytes(Point{}, source[:blockEnd]))
	statementSpans := scalaBlockStatementSpans(source, blockStart+1, blockEnd-1)
	if len(statementSpans) == 0 {
		return nil, false
	}
	children := make([]*Node, 0, len(statementSpans)+2)
	children = append(children, open)
	for _, span := range statementSpans {
		stmt, ok := scalaRecoverBlockStatementNode(source, span.start, span.end, lang, arena)
		if !ok || stmt == nil {
			return nil, false
		}
		children = append(children, stmt)
	}
	children = append(children, close)
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	return newParentNodeInArena(arena, blockSym, blockNamed, children, nil, 0), true
}

func scalaBlockStatementSpans(source []byte, blockStart, blockEnd uint32) []scalaStatementSpan {
	if blockStart >= blockEnd || int(blockEnd) > len(source) {
		return nil
	}
	var spans []scalaStatementSpan
	pos := int(blockStart)
	limit := int(blockEnd)
	for pos < limit {
		start, ok := scalaFindNextBlockStatementStart(source, pos, limit)
		if !ok {
			break
		}
		end := scalaFindNextBlockStatementBoundary(source, start, limit)
		if end <= start {
			pos = start + 1
			continue
		}
		spans = append(spans, scalaStatementSpan{start: uint32(start), end: uint32(end)})
		pos = end
	}
	return spans
}

func scalaFindNextBlockStatementStart(source []byte, pos, limit int) (int, bool) {
	lineStart := true
	for i := pos; i < limit; i++ {
		if lineStart {
			j := skipHorizontalTrivia(source, i, limit)
			if j < limit && source[j] != '\n' && source[j] != '\r' && source[j] != '}' {
				return j, true
			}
			lineStart = false
		}
		if source[i] == '\n' {
			lineStart = true
		}
	}
	return 0, false
}

func scalaFindNextBlockStatementBoundary(source []byte, start, limit int) int {
	braceDepth := 0
	parenDepth := 0
	bracketDepth := 0
	inLineComment := false
	inBlockComment := false
	var stringQuote byte
	tripleQuote := false
	lineStart := false
	for i := start + 1; i < limit; i++ {
		ch := source[i]
		next := byte(0)
		if i+1 < limit {
			next = source[i+1]
		}
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				lineStart = true
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
				continue
			}
			if ch == '\n' {
				lineStart = true
			}
			continue
		}
		if stringQuote != 0 {
			if tripleQuote {
				if i+2 < limit && source[i] == stringQuote && source[i+1] == stringQuote && source[i+2] == stringQuote {
					stringQuote = 0
					tripleQuote = false
					i += 2
				}
				continue
			}
			if ch == '\\' {
				i++
				continue
			}
			if ch == stringQuote {
				stringQuote = 0
			}
			continue
		}
		if lineStart {
			j := skipHorizontalTrivia(source, i, limit)
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 && j < limit {
				switch source[j] {
				case '}', '\n', '\r':
					return trimTrailingHorizontalAndVerticalTrivia(source, start, i)
				}
				return trimTrailingHorizontalAndVerticalTrivia(source, start, i)
			}
			lineStart = false
		}
		switch {
		case ch == '/' && next == '/':
			inLineComment = true
			i++
			continue
		case ch == '/' && next == '*':
			inBlockComment = true
			i++
			continue
		case ch == '"' || ch == '\'':
			if i+2 < limit && source[i+1] == ch && source[i+2] == ch {
				stringQuote = ch
				tripleQuote = true
				i += 2
				continue
			}
			stringQuote = ch
			tripleQuote = false
			continue
		case ch == '{':
			braceDepth++
		case ch == '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case ch == '(':
			parenDepth++
		case ch == ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case ch == '[':
			bracketDepth++
		case ch == ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		}
		if ch == '\n' {
			lineStart = true
		}
	}
	return trimTrailingHorizontalAndVerticalTrivia(source, start, limit)
}

func scalaRecoverBlockStatementNode(source []byte, start, end uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if end <= start || int(end) > len(source) {
		return nil, false
	}
	tree, err := parseWithSnippetParser(lang, source[start:end])
	if err == nil && tree != nil && tree.RootNode() != nil {
		defer tree.Release()
		startPoint := advancePointByBytes(Point{}, source[:start])
		offsetRoot := tree.RootNodeWithOffset(start, startPoint)
		if offsetRoot != nil {
			for i := 0; i < offsetRoot.ChildCount(); i++ {
				child := offsetRoot.Child(i)
				if child == nil || child.HasError() {
					continue
				}
				switch child.Type(lang) {
				case "val_definition", "call_expression":
					return cloneTreeNodesIntoArena(child, arena), true
				}
			}
		}
	}
	if bytes.HasPrefix(source[start:end], []byte("val ")) {
		return scalaRecoverValDefinitionIfExpressionFromRange(source, start, end, lang, arena)
	}
	return nil, false
}

func scalaRecoverValDefinitionIfExpressionFromRange(source []byte, start, end uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if end <= start || int(end) > len(source) || lang == nil {
		return nil, false
	}
	valSym, ok := symbolByName(lang, "val")
	if !ok {
		return nil, false
	}
	identifierSym, ok := symbolByName(lang, "identifier")
	if !ok {
		return nil, false
	}
	eqSym, ok := symbolByName(lang, "=")
	if !ok {
		return nil, false
	}
	valDefSym, ok := symbolByName(lang, "val_definition")
	if !ok {
		return nil, false
	}
	ifExprSym, ok := symbolByName(lang, "if_expression")
	if !ok {
		return nil, false
	}
	ifSym, ok := symbolByName(lang, "if")
	if !ok {
		return nil, false
	}
	elseSym, ok := symbolByName(lang, "else")
	if !ok {
		return nil, false
	}
	valNamed := int(valSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[valSym].Named
	identifierNamed := int(identifierSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[identifierSym].Named
	eqNamed := int(eqSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[eqSym].Named
	valDefNamed := int(valDefSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[valDefSym].Named
	ifExprNamed := int(ifExprSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[ifExprSym].Named
	ifNamed := int(ifSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[ifSym].Named
	elseNamed := int(elseSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[elseSym].Named

	ifPos := bytes.Index(source[start:end], []byte("if "))
	elsePos := bytes.Index(source[start:end], []byte(" else "))
	if ifPos < 0 || elsePos < 0 {
		return nil, false
	}
	ifPos += int(start)
	elsePos += int(start) + 1
	condStart := ifPos + len("if ")
	condEnd := scalaFindMatchingParenByteWithTrivia(source, condStart, int(end))
	if condEnd < condStart {
		return nil, false
	}
	consequenceStart := skipHorizontalTrivia(source, condEnd+1, int(end))
	if consequenceStart >= elsePos {
		return nil, false
	}
	alternativeStart := skipHorizontalTrivia(source, elsePos+len("else"), int(end))
	if alternativeStart >= int(end) {
		return nil, false
	}
	condition, ok := scalaRecoverSingleExpressionNode(source, uint32(condStart), uint32(condEnd+1), lang, arena, "parenthesized_expression")
	if !ok {
		return nil, false
	}
	consequence, ok := scalaRecoverSingleExpressionNode(source, uint32(consequenceStart), uint32(elsePos), lang, arena, "infix_expression")
	if !ok {
		return nil, false
	}
	alternative, ok := scalaRecoverSingleExpressionNode(source, uint32(alternativeStart), end, lang, arena, "identifier")
	if !ok {
		return nil, false
	}
	valLeaf := newLeafNodeInArena(arena, valSym, valNamed, start, start+3, advancePointByBytes(Point{}, source[:start]), advancePointByBytes(Point{}, source[:start+3]))
	nameStart := start + 4
	nameEnd := nameStart + 3
	nameLeaf := newLeafNodeInArena(arena, identifierSym, identifierNamed, nameStart, nameEnd, advancePointByBytes(Point{}, source[:nameStart]), advancePointByBytes(Point{}, source[:nameEnd]))
	eqStart := start + 8
	eqLeaf := newLeafNodeInArena(arena, eqSym, eqNamed, eqStart, eqStart+1, advancePointByBytes(Point{}, source[:eqStart]), advancePointByBytes(Point{}, source[:eqStart+1]))
	ifLeaf := newLeafNodeInArena(arena, ifSym, ifNamed, uint32(ifPos), uint32(ifPos+2), advancePointByBytes(Point{}, source[:ifPos]), advancePointByBytes(Point{}, source[:ifPos+2]))
	elseLeaf := newLeafNodeInArena(arena, elseSym, elseNamed, uint32(elsePos), uint32(elsePos+4), advancePointByBytes(Point{}, source[:elsePos]), advancePointByBytes(Point{}, source[:elsePos+4]))
	ifChildren := []*Node{ifLeaf, condition, consequence, elseLeaf, alternative}
	if arena != nil {
		buf := arena.allocNodeSlice(len(ifChildren))
		copy(buf, ifChildren)
		ifChildren = buf
	}
	ifNode := newParentNodeInArena(arena, ifExprSym, ifExprNamed, ifChildren, nil, 0)
	valChildren := []*Node{valLeaf, nameLeaf, eqLeaf, ifNode}
	if arena != nil {
		buf := arena.allocNodeSlice(len(valChildren))
		copy(buf, valChildren)
		valChildren = buf
	}
	return newParentNodeInArena(arena, valDefSym, valDefNamed, valChildren, nil, 0), true
}

func scalaRecoverSingleExpressionNode(source []byte, start, end uint32, lang *Language, arena *nodeArena, want string) (*Node, bool) {
	if end <= start || int(end) > len(source) {
		return nil, false
	}
	tree, err := parseWithSnippetParser(lang, source[start:end])
	if err != nil || tree == nil || tree.RootNode() == nil {
		return nil, false
	}
	defer tree.Release()
	startPoint := advancePointByBytes(Point{}, source[:start])
	offsetRoot := tree.RootNodeWithOffset(start, startPoint)
	if offsetRoot == nil {
		return nil, false
	}
	for i := 0; i < offsetRoot.ChildCount(); i++ {
		child := offsetRoot.Child(i)
		if child == nil || child.HasError() {
			continue
		}
		if child.Type(lang) == want {
			return cloneTreeNodesIntoArena(child, arena), true
		}
	}
	if want == "identifier" {
		sym, ok := symbolByName(lang, "identifier")
		if !ok {
			return nil, false
		}
		named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
		return newLeafNodeInArena(arena, sym, named, start, end, advancePointByBytes(Point{}, source[:start]), advancePointByBytes(Point{}, source[:end])), true
	}
	return nil, false
}

func scalaRecoverLeadingAnnotations(source []byte, start, fnStart, fnEnd uint32, lang *Language, arena *nodeArena) []*Node {
	if lang == nil || fnStart <= start || fnEnd <= fnStart || int(fnEnd) > len(source) {
		return nil
	}
	pos := int(start)
	limit := int(fnStart)
	for pos < limit {
		switch source[pos] {
		case ' ', '\t', '\n', '\r':
			pos++
		default:
			goto found
		}
	}
found:
	if pos >= limit || source[pos] != '@' {
		return nil
	}
	tree, err := parseWithSnippetParser(lang, source[pos:fnEnd])
	if err != nil || tree == nil || tree.RootNode() == nil {
		return nil
	}
	defer tree.Release()
	startPoint := advancePointByBytes(Point{}, source[:pos])
	offsetRoot := tree.RootNodeWithOffset(uint32(pos), startPoint)
	if offsetRoot == nil {
		return nil
	}
	for i := 0; i < offsetRoot.ChildCount(); i++ {
		child := offsetRoot.Child(i)
		if child == nil || child.Type(lang) != "function_definition" || child.HasError() {
			continue
		}
		var annotations []*Node
		for _, fnChild := range child.children {
			if fnChild == nil || fnChild.Type(lang) != "annotation" {
				break
			}
			annotations = append(annotations, cloneTreeNodesIntoArena(fnChild, arena))
		}
		if len(annotations) > 0 {
			return annotations
		}
	}
	return nil
}

func scalaFindMatchingBraceByteWithTrivia(source []byte, openPos int, limit uint32) int {
	return scalaFindMatchingDelimiterByteWithTrivia(source, openPos, int(limit), '{', '}')
}

func scalaFindMatchingParenByteWithTrivia(source []byte, openPos int, limit int) int {
	return scalaFindMatchingDelimiterByteWithTrivia(source, openPos, limit, '(', ')')
}

func scalaFindMatchingDelimiterByteWithTrivia(source []byte, openPos, limit int, openDelim, closeDelim byte) int {
	if openPos < 0 || openPos >= len(source) {
		return -1
	}
	if limit > len(source) {
		limit = len(source)
	}
	depth := 0
	inLineComment := false
	inBlockComment := false
	var stringQuote byte
	tripleQuote := false
	for i := openPos; i < limit; i++ {
		ch := source[i]
		next := byte(0)
		if i+1 < limit {
			next = source[i+1]
		}
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if stringQuote != 0 {
			if tripleQuote {
				if i+2 < limit && source[i] == stringQuote && source[i+1] == stringQuote && source[i+2] == stringQuote {
					stringQuote = 0
					tripleQuote = false
					i += 2
				}
				continue
			}
			if ch == '\\' {
				i++
				continue
			}
			if ch == stringQuote {
				stringQuote = 0
			}
			continue
		}
		switch {
		case ch == '/' && next == '/':
			inLineComment = true
			i++
			continue
		case ch == '/' && next == '*':
			inBlockComment = true
			i++
			continue
		case ch == '"' || ch == '\'':
			if i+2 < limit && source[i+1] == ch && source[i+2] == ch {
				stringQuote = ch
				tripleQuote = true
				i += 2
				continue
			}
			stringQuote = ch
			tripleQuote = false
			continue
		case ch == openDelim:
			depth++
		case ch == closeDelim:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func normalizeScalaTopLevelClassFragments(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" || root.Type(lang) != "ERROR" || len(root.children) == 0 || len(source) == 0 {
		return
	}
	for _, child := range root.children {
		if child != nil && child.Type(lang) == "class_definition" {
			return
		}
	}
	lastObjectEnd := uint32(0)
	for _, child := range root.children {
		if child != nil && child.Type(lang) == "object_definition" && child.endByte > lastObjectEnd {
			lastObjectEnd = child.endByte
		}
	}
	if lastObjectEnd == 0 || int(lastObjectEnd) >= len(source) {
		return
	}
	classStartRel := bytes.Index(source[lastObjectEnd:], []byte("\nfinal class "))
	if classStartRel < 0 {
		classStartRel = bytes.Index(source[lastObjectEnd:], []byte("\nclass "))
		if classStartRel < 0 {
			return
		}
	}
	classStart := int(lastObjectEnd) + classStartRel + 1
	classNode, ok := scalaRecoverTopLevelClassNode(source, uint32(classStart), lang, root.ownerArena)
	if !ok || classNode == nil {
		return
	}
	startIdx := len(root.children)
	for i, child := range root.children {
		if child != nil && child.startByte >= uint32(classStart) {
			startIdx = i
			break
		}
	}
	if startIdx >= len(root.children) {
		return
	}
	replaceChildRangeWithSingleNode(root, startIdx, len(root.children), classNode)
	populateParentNode(root, root.children)
}

func scalaObjectNeedsTemplateBody(node *Node, lang *Language) bool {
	if node == nil || lang == nil || node.Type(lang) != "object_definition" || len(node.children) != 2 {
		return false
	}
	return node.children[0] != nil && node.children[0].Type(lang) == "object" &&
		node.children[1] != nil && node.children[1].Type(lang) == "identifier"
}

func scalaSingleTokenError(node *Node, token string, lang *Language) bool {
	return scalaErrorTokenNode(node, token, lang) != nil
}

func scalaErrorTokenNode(node *Node, token string, lang *Language) *Node {
	if node == nil || lang == nil || node.Type(lang) != "ERROR" || len(node.children) != 1 || node.children[0] == nil {
		return nil
	}
	if node.children[0].Type(lang) == token {
		return node.children[0]
	}
	return nil
}

func scalaFindTemplateBodyClose(nodes []*Node, start int, lang *Language) int {
	for i := start; i < len(nodes); i++ {
		if scalaSingleTokenError(nodes[i], "}", lang) {
			return i
		}
	}
	return -1
}

func scalaFindTemplateBodyCloseByByte(nodes []*Node, start int, closeByte uint32) int {
	last := -1
	for i := start; i < len(nodes); i++ {
		n := nodes[i]
		if n == nil {
			continue
		}
		if n.startByte >= closeByte {
			break
		}
		last = i
		if n.endByte >= closeByte {
			return i
		}
	}
	return last
}

func scalaTemplateBodyFragmentChildren(nodes []*Node, arena *nodeArena, lang *Language, source []byte, closeByte uint32, synthClose bool) ([]*Node, bool) {
	out := make([]*Node, 0, len(nodes))
	var appendNode func(*Node)
	appendNode = func(n *Node) {
		if n == nil {
			return
		}
		switch n.Type(lang) {
		case "_indent", "_outdent":
			return
		case "_block_repeat1":
			for _, child := range n.children {
				appendNode(child)
			}
			return
		case "ERROR":
			if len(n.children) == 1 && n.children[0] != nil {
				switch n.children[0].Type(lang) {
				case "{", "}":
					out = append(out, n.children[0])
					return
				}
			}
		}
		out = append(out, n)
	}
	for _, node := range nodes {
		appendNode(node)
	}
	if len(out) == 0 || out[0] == nil || out[0].Type(lang) != "{" {
		return nil, false
	}
	if synthClose && (len(out) == 1 || out[len(out)-1] == nil || out[len(out)-1].Type(lang) != "}") {
		closeSym, ok := symbolByName(lang, "}")
		if !ok {
			return nil, false
		}
		closeNamed := int(closeSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[closeSym].Named
		start := closeByte - 1
		if int(closeByte) > len(source) || start >= closeByte {
			return nil, false
		}
		close := newLeafNodeInArena(
			arena,
			closeSym,
			closeNamed,
			start,
			closeByte,
			advancePointByBytes(Point{}, source[:start]),
			advancePointByBytes(Point{}, source[:closeByte]),
		)
		out = append(out, close)
	}
	if len(out) < 2 || out[len(out)-1] == nil || out[len(out)-1].Type(lang) != "}" {
		return nil, false
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(out))
		copy(buf, out)
		out = buf
	}
	return out, true
}

func scalaRecoverTopLevelClassNode(source []byte, classStart uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || int(classStart) >= len(source) {
		return nil, false
	}
	openRel := bytes.IndexByte(source[classStart:], '{')
	if openRel < 0 {
		return nil, false
	}
	openBrace := int(classStart) + openRel
	closeBrace := findMatchingBraceByte(source, openBrace, len(source))
	if closeBrace < 0 || closeBrace <= openBrace {
		return nil, false
	}
	return scalaRecoverTopLevelClassNodeFromRange(source, classStart, uint32(closeBrace+1), lang, arena)
}

func scalaRecoverTopLevelClassNodeFromRange(source []byte, classStart, classEnd uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || int(classStart) >= len(source) || classEnd <= classStart || int(classEnd) > len(source) {
		return nil, false
	}
	tree, err := parseWithSnippetParser(lang, source[classStart:classEnd])
	if err != nil || tree == nil || tree.RootNode() == nil {
		return nil, false
	}
	defer tree.Release()
	startPoint := advancePointByBytes(Point{}, source[:classStart])
	offsetRoot := tree.RootNodeWithOffset(classStart, startPoint)
	if offsetRoot == nil {
		return nil, false
	}
	for i := 0; i < offsetRoot.ChildCount(); i++ {
		child := offsetRoot.Child(i)
		if child == nil || child.Type(lang) != "class_definition" || child.HasError() {
			continue
		}
		recovered := cloneTreeNodesIntoArena(child, arena)
		if recovered.endByte < classEnd && bytesAreTrivia(source[recovered.endByte:classEnd]) {
			extendNodeEndTo(recovered, classEnd, source)
		}
		if recovered.endByte == classEnd {
			return recovered, true
		}
	}
	classSym, ok := symbolByName(lang, "class_definition")
	if !ok {
		return nil, false
	}
	classNamed := int(classSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[classSym].Named
	templateBodySym, ok := symbolByName(lang, "template_body")
	if !ok {
		return nil, false
	}
	templateBodyNamed := int(templateBodySym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[templateBodySym].Named
	headerIdx := -1
	classIdx := -1
	constructorIdx := -1
	openIdx := -1
	extendsIdx := -1
	for i := 0; i < offsetRoot.ChildCount(); i++ {
		child := offsetRoot.Child(i)
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "class_definition":
			if headerIdx < 0 {
				headerIdx = i
			}
		case "class":
			classIdx = i
		case "_class_constructor":
			if classIdx >= 0 && constructorIdx < 0 {
				constructorIdx = i
			}
		case "extends_clause":
			if constructorIdx >= 0 && extendsIdx < 0 {
				extendsIdx = i
			}
		case "{":
			if constructorIdx >= 0 || headerIdx >= 0 {
				openIdx = i
				i = offsetRoot.ChildCount()
			}
		}
		if openIdx < 0 && headerIdx >= 0 {
			if brace := scalaErrorTokenNode(child, "{", lang); brace != nil {
				openIdx = i
				i = offsetRoot.ChildCount()
			}
		}
	}
	if headerIdx >= 0 && openIdx >= 0 {
		header := offsetRoot.Child(headerIdx)
		closeIdx := scalaFindTemplateBodyCloseByByte(offsetRoot.children, openIdx+1, classEnd)
		if closeIdx < openIdx {
			closeIdx = len(offsetRoot.children) - 1
		}
		bodyChildren, ok := scalaTemplateBodyFragmentChildren(offsetRoot.children[openIdx:closeIdx+1], arena, lang, source, classEnd, true)
		if !ok {
			return nil, false
		}
		templateBody := newParentNodeInArena(arena, templateBodySym, templateBodyNamed, bodyChildren, nil, 0)
		children := make([]*Node, 0, len(header.children)+1)
		for _, child := range header.children {
			if child == nil {
				continue
			}
			children = append(children, cloneTreeNodesIntoArena(child, arena))
		}
		children = append(children, templateBody)
		if arena != nil {
			buf := arena.allocNodeSlice(len(children))
			copy(buf, children)
			children = buf
		}
		recovered := newParentNodeInArena(arena, classSym, classNamed, children, nil, header.productionID)
		if recovered.endByte < classEnd && bytesAreTrivia(source[recovered.endByte:classEnd]) {
			extendNodeEndTo(recovered, classEnd, source)
		}
		return recovered, true
	}
	if classIdx < 0 || constructorIdx < 0 || openIdx < 0 {
		return nil, false
	}
	constructor := offsetRoot.Child(constructorIdx)
	if constructor == nil || constructor.ChildCount() < 2 {
		return nil, false
	}
	nameNode := constructor.Child(0)
	paramsNode := constructor.Child(1)
	if nameNode == nil || paramsNode == nil || nameNode.Type(lang) != "identifier" || paramsNode.Type(lang) != "class_parameters" {
		return nil, false
	}
	closeByte := classEnd
	closeIdx := scalaFindTemplateBodyCloseByByte(offsetRoot.children, openIdx+1, closeByte)
	if closeIdx < openIdx {
		closeIdx = len(offsetRoot.children) - 1
	}
	synthClose := true
	if closeIdx >= 0 && closeIdx < len(offsetRoot.children) {
		if closeNode := scalaErrorTokenNode(offsetRoot.children[closeIdx], "}", lang); closeNode != nil && closeNode.endByte == closeByte {
			synthClose = false
		} else if offsetRoot.children[closeIdx] != nil && offsetRoot.children[closeIdx].Type(lang) == "}" && offsetRoot.children[closeIdx].endByte == closeByte {
			synthClose = false
		}
	}
	bodyChildren, ok := scalaTemplateBodyFragmentChildren(offsetRoot.children[openIdx:closeIdx+1], arena, lang, source, closeByte, synthClose)
	if !ok {
		return nil, false
	}
	templateBody := newParentNodeInArena(arena, templateBodySym, templateBodyNamed, bodyChildren, nil, 0)
	children := make([]*Node, 0, 6)
	if classIdx > 0 {
		if modifiers := offsetRoot.Child(classIdx - 1); modifiers != nil && modifiers.Type(lang) == "modifiers" {
			children = append(children, cloneTreeNodesIntoArena(modifiers, arena))
		}
	}
	children = append(children, cloneTreeNodesIntoArena(offsetRoot.Child(classIdx), arena))
	children = append(children, cloneTreeNodesIntoArena(nameNode, arena))
	children = append(children, cloneTreeNodesIntoArena(paramsNode, arena))
	if extendsIdx >= 0 {
		if extendsClause := offsetRoot.Child(extendsIdx); extendsClause != nil && extendsClause.Type(lang) == "extends_clause" {
			children = append(children, cloneTreeNodesIntoArena(extendsClause, arena))
		}
	}
	children = append(children, templateBody)
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	recovered := newParentNodeInArena(arena, classSym, classNamed, children, nil, 0)
	if recovered.endByte < classEnd && bytesAreTrivia(source[recovered.endByte:classEnd]) {
		extendNodeEndTo(recovered, classEnd, source)
	}
	return recovered, true
}

func scalaRecoverTopLevelObjectNodeFromRange(source []byte, objectStart, objectEnd uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || int(objectStart) >= len(source) || objectEnd <= objectStart || int(objectEnd) > len(source) {
		return nil, false
	}
	tree, err := parseWithSnippetParser(lang, source[objectStart:objectEnd])
	if err != nil || tree == nil || tree.RootNode() == nil {
		return nil, false
	}
	defer tree.Release()
	startPoint := advancePointByBytes(Point{}, source[:objectStart])
	offsetRoot := tree.RootNodeWithOffset(objectStart, startPoint)
	if offsetRoot == nil {
		return nil, false
	}
	for i := 0; i < offsetRoot.ChildCount(); i++ {
		child := offsetRoot.Child(i)
		if child == nil || child.Type(lang) != "object_definition" || child.HasError() {
			continue
		}
		recovered := cloneTreeNodesIntoArena(child, arena)
		if recovered.endByte < objectEnd && bytesAreTrivia(source[recovered.endByte:objectEnd]) {
			extendNodeEndTo(recovered, objectEnd, source)
		}
		if recovered.endByte == objectEnd {
			return recovered, true
		}
	}
	objectSym, ok := symbolByName(lang, "object_definition")
	if !ok {
		return nil, false
	}
	objectNamed := int(objectSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[objectSym].Named
	templateBodySym, ok := symbolByName(lang, "template_body")
	if !ok {
		return nil, false
	}
	templateBodyNamed := int(templateBodySym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[templateBodySym].Named
	objectIdx := -1
	identifierIdx := -1
	openIdx := -1
	for i := 0; i < offsetRoot.ChildCount(); i++ {
		child := offsetRoot.Child(i)
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "object":
			if objectIdx < 0 {
				objectIdx = i
			}
		case "identifier":
			if objectIdx >= 0 && identifierIdx < 0 {
				identifierIdx = i
			}
		case "{":
			if identifierIdx >= 0 {
				openIdx = i
				i = offsetRoot.ChildCount()
			}
		}
	}
	if objectIdx < 0 || identifierIdx < 0 || openIdx < 0 {
		return nil, false
	}
	closeIdx := scalaFindTemplateBodyCloseByByte(offsetRoot.children, openIdx+1, objectEnd)
	if closeIdx < openIdx {
		closeIdx = len(offsetRoot.children) - 1
	}
	synthClose := true
	if closeIdx >= 0 && closeIdx < len(offsetRoot.children) {
		if closeNode := scalaErrorTokenNode(offsetRoot.children[closeIdx], "}", lang); closeNode != nil && closeNode.endByte == objectEnd {
			synthClose = false
		} else if offsetRoot.children[closeIdx] != nil && offsetRoot.children[closeIdx].Type(lang) == "}" && offsetRoot.children[closeIdx].endByte == objectEnd {
			synthClose = false
		}
	}
	bodyChildren, ok := scalaTemplateBodyFragmentChildren(offsetRoot.children[openIdx:closeIdx+1], arena, lang, source, objectEnd, synthClose)
	if !ok {
		return nil, false
	}
	templateBody := newParentNodeInArena(arena, templateBodySym, templateBodyNamed, bodyChildren, nil, 0)
	children := []*Node{
		cloneTreeNodesIntoArena(offsetRoot.Child(objectIdx), arena),
		cloneTreeNodesIntoArena(offsetRoot.Child(identifierIdx), arena),
		templateBody,
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	recovered := newParentNodeInArena(arena, objectSym, objectNamed, children, nil, 0)
	if recovered.endByte < objectEnd && bytesAreTrivia(source[recovered.endByte:objectEnd]) {
		extendNodeEndTo(recovered, objectEnd, source)
	}
	return recovered, true
}

func scalaRecoverTopLevelNamedNodeFromRange(source []byte, start, end uint32, lang *Language, arena *nodeArena, want string) (*Node, bool) {
	if lang == nil || int(start) >= len(source) || end <= start || int(end) > len(source) {
		return nil, false
	}
	tree, err := parseWithSnippetParser(lang, source[start:end])
	if err != nil || tree == nil || tree.RootNode() == nil {
		return nil, false
	}
	defer tree.Release()
	startPoint := advancePointByBytes(Point{}, source[:start])
	offsetRoot := tree.RootNodeWithOffset(start, startPoint)
	if offsetRoot == nil {
		return nil, false
	}
	for i := 0; i < offsetRoot.ChildCount(); i++ {
		child := offsetRoot.Child(i)
		if child == nil || child.Type(lang) != want || child.HasError() {
			continue
		}
		recovered := cloneTreeNodesIntoArena(child, arena)
		if recovered.endByte < end && bytesAreTrivia(source[recovered.endByte:end]) {
			extendNodeEndTo(recovered, end, source)
		}
		if recovered.endByte == end {
			return recovered, true
		}
	}
	return nil, false
}

func scalaRecoverTopLevelFunctionNodeFromRange(source []byte, fnStart, fnEnd uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || int(fnStart) >= len(source) || fnEnd <= fnStart || int(fnEnd) > len(source) {
		return nil, false
	}
	tree, err := parseWithSnippetParser(lang, source[fnStart:fnEnd])
	if err != nil || tree == nil || tree.RootNode() == nil {
		return nil, false
	}
	defer tree.Release()
	startPoint := advancePointByBytes(Point{}, source[:fnStart])
	offsetRoot := tree.RootNodeWithOffset(fnStart, startPoint)
	if offsetRoot == nil {
		return nil, false
	}
	for i := 0; i < offsetRoot.ChildCount(); i++ {
		child := offsetRoot.Child(i)
		if child == nil || child.Type(lang) != "function_definition" {
			continue
		}
		recovered := cloneTreeNodesIntoArena(child, arena)
		if recovered.endByte < fnEnd && bytesAreTrivia(source[recovered.endByte:fnEnd]) {
			extendNodeEndTo(recovered, fnEnd, source)
		}
		return recovered, true
	}
	return nil, false
}

func normalizeScalaCompilationUnitRoot(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" || root.Type(lang) != "ERROR" {
		return
	}
	sym, ok := symbolByName(lang, "compilation_unit")
	if !ok {
		return
	}
	if children, ok := scalaRebuildCompilationUnitChildren(source, lang, root.ownerArena); ok {
		root.children = children
		root.fieldIDs = nil
		root.fieldSources = nil
		root.symbol = sym
		root.isNamed = int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
		populateParentNode(root, root.children)
		root.hasError = false
		for _, child := range root.children {
			if child != nil && (child.IsError() || child.HasError()) {
				root.hasError = true
				break
			}
		}
		if !root.hasError {
			return
		}
	}
	if !rootLooksLikeScalaCompilationUnit(root, lang) {
		return
	}
	root.symbol = sym
	root.isNamed = int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	root.hasError = false
	for _, child := range root.children {
		if child != nil && (child.IsError() || child.HasError()) {
			root.hasError = true
			break
		}
	}
}

func scalaRebuildCompilationUnitChildren(source []byte, lang *Language, arena *nodeArena) ([]*Node, bool) {
	if lang == nil || len(source) == 0 {
		return nil, false
	}
	spans := scalaCompilationUnitSpans(source)
	if len(spans) == 0 {
		return nil, false
	}
	sawPackageOrImport := false
	sawDefinition := false
	for _, span := range spans {
		switch span.kind {
		case scalaTemplateMemberPackage, scalaTemplateMemberImport:
			sawPackageOrImport = true
		case scalaTemplateMemberClass, scalaTemplateMemberObject, scalaTemplateMemberTrait, scalaTemplateMemberEnum:
			sawDefinition = true
		}
	}
	if !sawPackageOrImport || !sawDefinition {
		return nil, false
	}
	children := make([]*Node, 0, len(spans))
	for _, span := range spans {
		node, ok := scalaRecoverCompilationUnitMemberNode(source, span, lang, arena)
		if !ok || node == nil {
			switch span.kind {
			case scalaTemplateMemberComment, scalaTemplateMemberBlockComment:
				continue
			default:
				return nil, false
			}
		}
		children = append(children, node)
	}
	if len(children) == 0 {
		return nil, false
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	return children, true
}

func scalaCompilationUnitSpans(source []byte) []scalaTemplateMemberSpan {
	var spans []scalaTemplateMemberSpan
	pos := 0
	limit := len(source)
	for pos < limit {
		start, kind, ok := scalaFindNextCompilationUnitMemberStart(source, pos, limit)
		if !ok {
			break
		}
		end := scalaFindCompilationUnitMemberEnd(source, start, limit, kind)
		if end <= start {
			pos = start + 1
			continue
		}
		spans = append(spans, scalaTemplateMemberSpan{
			start: uint32(start),
			end:   uint32(end),
			kind:  kind,
		})
		pos = end
	}
	return spans
}

func scalaFindNextCompilationUnitMemberStart(source []byte, pos, limit int) (int, scalaTemplateMemberKind, bool) {
	braceDepth := 0
	parenDepth := 0
	bracketDepth := 0
	inLineComment := false
	inBlockComment := false
	var stringQuote byte
	tripleQuote := false
	lineStart := true
	for i := pos; i < limit; i++ {
		ch := source[i]
		next := byte(0)
		if i+1 < limit {
			next = source[i+1]
		}
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				lineStart = true
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
				continue
			}
			if ch == '\n' {
				lineStart = true
			}
			continue
		}
		if stringQuote != 0 {
			if tripleQuote {
				if i+2 < limit && source[i] == stringQuote && source[i+1] == stringQuote && source[i+2] == stringQuote {
					stringQuote = 0
					tripleQuote = false
					i += 2
				}
				continue
			}
			if ch == '\\' {
				i++
				continue
			}
			if ch == stringQuote {
				stringQuote = 0
			}
			continue
		}
		if lineStart {
			j := skipHorizontalTrivia(source, i, limit)
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				switch {
				case j+1 < limit && source[j] == '/' && source[j+1] == '/':
					return j, scalaTemplateMemberComment, true
				case j+1 < limit && source[j] == '/' && source[j+1] == '*':
					return j, scalaTemplateMemberBlockComment, true
				default:
					if kind, ok := scalaCompilationUnitKindAt(source, j, limit); ok {
						return j, kind, true
					}
				}
			}
			lineStart = false
		}
		switch {
		case ch == '/' && next == '/':
			inLineComment = true
			i++
			continue
		case ch == '/' && next == '*':
			inBlockComment = true
			i++
			continue
		case ch == '"' || ch == '\'':
			if i+2 < limit && source[i+1] == ch && source[i+2] == ch {
				stringQuote = ch
				tripleQuote = true
				i += 2
				continue
			}
			stringQuote = ch
			tripleQuote = false
			continue
		case ch == '{':
			braceDepth++
		case ch == '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case ch == '(':
			parenDepth++
		case ch == ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case ch == '[':
			bracketDepth++
		case ch == ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		}
		if ch == '\n' {
			lineStart = true
		}
	}
	return 0, scalaTemplateMemberUnknown, false
}

func scalaCompilationUnitKindAt(source []byte, pos, limit int) (scalaTemplateMemberKind, bool) {
	if pos >= limit {
		return scalaTemplateMemberUnknown, false
	}
	switch {
	case bytes.HasPrefix(source[pos:limit], []byte("package ")):
		return scalaTemplateMemberPackage, true
	case bytes.HasPrefix(source[pos:limit], []byte("import ")):
		return scalaTemplateMemberImport, true
	case bytes.HasPrefix(source[pos:limit], []byte("final class ")):
		return scalaTemplateMemberClass, true
	case bytes.HasPrefix(source[pos:limit], []byte("implicit class ")):
		return scalaTemplateMemberClass, true
	case bytes.HasPrefix(source[pos:limit], []byte("class ")):
		return scalaTemplateMemberClass, true
	case bytes.HasPrefix(source[pos:limit], []byte("object ")):
		return scalaTemplateMemberObject, true
	case bytes.HasPrefix(source[pos:limit], []byte("trait ")):
		return scalaTemplateMemberTrait, true
	case bytes.HasPrefix(source[pos:limit], []byte("enum ")):
		return scalaTemplateMemberEnum, true
	default:
		return scalaTemplateMemberUnknown, false
	}
}

func scalaFindCompilationUnitMemberEnd(source []byte, start, limit int, kind scalaTemplateMemberKind) int {
	switch kind {
	case scalaTemplateMemberComment:
		end := start
		for end < limit && source[end] != '\n' && source[end] != '\r' {
			end++
		}
		return trimTrailingHorizontalAndVerticalTrivia(source, start, end)
	case scalaTemplateMemberBlockComment:
		end := start + 2
		for end+1 < limit {
			if source[end] == '*' && source[end+1] == '/' {
				end += 2
				return trimTrailingHorizontalAndVerticalTrivia(source, start, end)
			}
			end++
		}
		return trimTrailingHorizontalAndVerticalTrivia(source, start, limit)
	case scalaTemplateMemberPackage, scalaTemplateMemberImport:
		end := start
		for end < limit && source[end] != '\n' && source[end] != '\r' {
			end++
		}
		return trimTrailingHorizontalAndVerticalTrivia(source, start, end)
	case scalaTemplateMemberObject, scalaTemplateMemberClass, scalaTemplateMemberTrait, scalaTemplateMemberEnum:
		openRel := bytes.IndexByte(source[start:limit], '{')
		if openRel < 0 {
			end := start
			for end < limit && source[end] != '\n' && source[end] != '\r' {
				end++
			}
			return trimTrailingHorizontalAndVerticalTrivia(source, start, end)
		}
		openPos := start + openRel
		if closePos := scalaFindMatchingBraceByteWithTrivia(source, openPos, uint32(limit)); closePos >= 0 {
			return closePos + 1
		}
		return trimTrailingHorizontalAndVerticalTrivia(source, start, limit)
	default:
		return 0
	}
}

func scalaRecoverCompilationUnitMemberNode(source []byte, span scalaTemplateMemberSpan, lang *Language, arena *nodeArena) (*Node, bool) {
	switch span.kind {
	case scalaTemplateMemberPackage:
		return scalaRecoverTopLevelNamedNodeFromRange(source, span.start, span.end, lang, arena, "package_clause")
	case scalaTemplateMemberImport:
		return scalaRecoverTopLevelNamedNodeFromRange(source, span.start, span.end, lang, arena, "import_declaration")
	case scalaTemplateMemberObject:
		return scalaRecoverTopLevelObjectNodeFromRange(source, span.start, span.end, lang, arena)
	case scalaTemplateMemberClass:
		return scalaRecoverTopLevelClassNodeFromRange(source, span.start, span.end, lang, arena)
	case scalaTemplateMemberTrait:
		return scalaRecoverTopLevelNamedNodeFromRange(source, span.start, span.end, lang, arena, "trait_definition")
	case scalaTemplateMemberEnum:
		return scalaRecoverTopLevelNamedNodeFromRange(source, span.start, span.end, lang, arena, "enum_definition")
	case scalaTemplateMemberComment:
		return scalaRecoverTopLevelNamedNodeFromRange(source, span.start, span.end, lang, arena, "comment")
	case scalaTemplateMemberBlockComment:
		return scalaRecoverTopLevelNamedNodeFromRange(source, span.start, span.end, lang, arena, "block_comment")
	default:
		return nil, false
	}
}

func normalizeScalaImportPathFields(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" {
		return
	}
	pathID, ok := lang.FieldByName("path")
	if !ok || pathID == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "import_declaration" && len(n.children) > 0 {
			for i, child := range n.children {
				if child == nil || child.Type(lang) != "." {
					continue
				}
				prevHasPath := i > 0 && i-1 < len(n.fieldIDs) && n.fieldIDs[i-1] == pathID
				nextHasPath := i+1 < len(n.children) && i+1 < len(n.fieldIDs) && n.fieldIDs[i+1] == pathID
				if !prevHasPath || !nextHasPath {
					continue
				}
				ensureNodeFieldStorage(n, len(n.children))
				n.fieldIDs[i] = pathID
				n.fieldSources[i] = fieldSourceDirect
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeScalaDefinitionFields(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" {
		return
	}
	nameID, _ := lang.FieldByName("name")
	classParamsID, _ := lang.FieldByName("class_parameters")
	extendID, _ := lang.FieldByName("extend")
	parametersID, _ := lang.FieldByName("parameters")
	patternID, _ := lang.FieldByName("pattern")
	valueID, _ := lang.FieldByName("value")
	typeID, _ := lang.FieldByName("type")
	returnTypeID, _ := lang.FieldByName("return_type")
	bodyID, ok := lang.FieldByName("body")
	if !ok {
		return
	}
	conditionID, _ := lang.FieldByName("condition")
	consequenceID, _ := lang.FieldByName("consequence")
	alternativeID, _ := lang.FieldByName("alternative")
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		switch n.Type(lang) {
		case "object_definition", "class_definition", "trait_definition", "enum_definition":
			for i, child := range n.children {
				if child == nil {
					continue
				}
				var want FieldID
				switch n.Type(lang) {
				case "object_definition", "trait_definition":
					switch child.Type(lang) {
					case "identifier":
						want = nameID
					case "extends_clause":
						want = extendID
					case "template_body":
						want = bodyID
					}
				case "class_definition":
					switch child.Type(lang) {
					case "identifier":
						want = nameID
					case "class_parameters":
						want = classParamsID
					case "extends_clause":
						want = extendID
					case "template_body":
						want = bodyID
					}
				case "enum_definition":
					switch child.Type(lang) {
					case "identifier":
						want = nameID
					case "enum_body":
						want = bodyID
					}
				}
				if want == 0 {
					continue
				}
				ensureNodeFieldStorage(n, len(n.children))
				if n.fieldIDs[i] == 0 {
					n.fieldIDs[i] = want
					n.fieldSources[i] = fieldSourceDirect
				}
			}
		case "function_definition":
			for i, child := range n.children {
				if child == nil {
					continue
				}
				var want FieldID
				switch {
				case child.Type(lang) == "identifier":
					want = nameID
				case child.Type(lang) == "parameters":
					want = parametersID
				case i > 0 && n.children[i-1] != nil && n.children[i-1].Type(lang) == ":" && child.isNamed:
					want = returnTypeID
				case i > 0 && n.children[i-1] != nil && (n.children[i-1].Type(lang) == "=" || n.children[i-1].Type(lang) == "=>") && child.isNamed:
					want = bodyID
				}
				if want == 0 {
					continue
				}
				ensureNodeFieldStorage(n, len(n.children))
				if n.fieldIDs[i] == 0 {
					n.fieldIDs[i] = want
					n.fieldSources[i] = fieldSourceDirect
				}
			}
		case "val_definition", "var_definition":
			patternAssigned := false
			typePending := false
			valuePending := false
			for i, child := range n.children {
				if child == nil {
					continue
				}
				switch child.Type(lang) {
				case ":":
					typePending = true
					continue
				case "=":
					valuePending = true
					typePending = false
					continue
				case "modifiers":
					continue
				}
				if !child.isNamed {
					continue
				}
				var want FieldID
				switch {
				case valuePending:
					want = valueID
					valuePending = false
				case typePending:
					want = typeID
					typePending = false
				case !patternAssigned:
					want = patternID
					patternAssigned = true
				}
				if want == 0 {
					continue
				}
				ensureNodeFieldStorage(n, len(n.children))
				if n.fieldIDs[i] == 0 {
					n.fieldIDs[i] = want
					n.fieldSources[i] = fieldSourceDirect
				}
			}
		case "if_expression":
			conditionAssigned := false
			consequenceAssigned := false
			afterElse := false
			for i, child := range n.children {
				if child == nil {
					continue
				}
				if child.Type(lang) == "else" {
					afterElse = true
					continue
				}
				if !child.isNamed {
					continue
				}
				var want FieldID
				switch {
				case !conditionAssigned:
					want = conditionID
					conditionAssigned = true
				case !afterElse && !consequenceAssigned:
					want = consequenceID
					consequenceAssigned = true
				case afterElse:
					want = alternativeID
				}
				if want == 0 {
					continue
				}
				ensureNodeFieldStorage(n, len(n.children))
				if n.fieldIDs[i] == 0 {
					n.fieldIDs[i] = want
					n.fieldSources[i] = fieldSourceDirect
				}
			}
		case "case_block":
			for i := 0; i+1 < len(n.children); i++ {
				curr := n.children[i]
				if curr == nil || curr.Type(lang) != "case_clause" {
					continue
				}
				next := scalaNextCaseClauseBoundaryNode(n.children, i, lang)
				if next == nil {
					continue
				}
				if curr.endByte >= next.startByte {
					continue
				}
				gap := source[curr.endByte:next.startByte]
				if !bytesAreTrivia(gap) || !bytesContainLineBreak(gap) {
					continue
				}
				extendNodeEndTo(curr, next.startByte, source)
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeScalaTemplateBodyFunctionAnnotations(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "template_body" {
			for i, child := range n.children {
				if child == nil || child.Type(lang) != "function_definition" || len(child.children) == 0 {
					continue
				}
				if child.children[0] != nil && child.children[0].Type(lang) == "annotation" {
					continue
				}
				gapStart := n.startByte
				if i > 0 && n.children[i-1] != nil {
					gapStart = n.children[i-1].endByte
				}
				annotations := scalaRecoverLeadingAnnotations(source, gapStart, child.startByte, child.endByte, lang, child.ownerArena)
				if len(annotations) == 0 {
					continue
				}
				newChildren := make([]*Node, 0, len(annotations)+len(child.children))
				newChildren = append(newChildren, annotations...)
				newChildren = append(newChildren, child.children...)
				if child.ownerArena != nil {
					buf := child.ownerArena.allocNodeSlice(len(newChildren))
					copy(buf, newChildren)
					newChildren = buf
				}
				child.children = newChildren
				if len(child.fieldIDs) > 0 {
					fieldIDs := make([]FieldID, 0, len(child.children))
					for range annotations {
						fieldIDs = append(fieldIDs, 0)
					}
					fieldIDs = append(fieldIDs, child.fieldIDs...)
					child.fieldIDs = fieldIDs
				}
				if len(child.fieldSources) > 0 {
					fieldSources := make([]uint8, 0, len(child.children))
					for range annotations {
						fieldSources = append(fieldSources, fieldSourceNone)
					}
					fieldSources = append(fieldSources, child.fieldSources...)
					child.fieldSources = fieldSources
				}
				populateParentNode(child, child.children)
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeScalaCaseClauseEnds(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "case_block" {
			for i := 0; i+1 < len(n.children); i++ {
				curr := n.children[i]
				if curr == nil || curr.Type(lang) != "case_clause" {
					continue
				}
				next := scalaNextCaseClauseBoundaryNode(n.children, i, lang)
				if next == nil {
					continue
				}
				if curr.endByte >= next.startByte || int(next.startByte) > len(source) {
					continue
				}
				gap := source[curr.endByte:next.startByte]
				if !bytesAreTrivia(gap) || !bytesContainLineBreak(gap) {
					continue
				}
				extendNodeEndTo(curr, next.startByte, source)
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func normalizeScalaTemplateBodyFunctionEnds(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "template_body" {
			for i := 0; i+1 < len(n.children); i++ {
				curr := n.children[i]
				next := n.children[i+1]
				if curr == nil || next == nil || curr.Type(lang) != "function_definition" || next.IsExtra() {
					continue
				}
				if len(curr.children) == 0 {
					continue
				}
				last := curr.children[len(curr.children)-1]
				if last == nil || last.Type(lang) != "indented_block" {
					continue
				}
				if curr.endByte >= next.startByte || int(next.startByte) > len(source) {
					continue
				}
				gap := source[curr.endByte:next.startByte]
				if !bytesAreTrivia(gap) || !bytesContainLineBreak(gap) {
					continue
				}
				extendNodeEndTo(last, next.startByte, source)
				extendNodeEndTo(curr, next.startByte, source)
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func scalaNextCaseClauseBoundaryNode(children []*Node, start int, lang *Language) *Node {
	for i := start + 1; i < len(children); i++ {
		child := children[i]
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "_automatic_semicolon":
			continue
		}
		return child
	}
	return nil
}

func rootLooksLikeScalaCompilationUnit(root *Node, lang *Language) bool {
	if root == nil || lang == nil || len(root.children) == 0 {
		return false
	}
	sawTopLevel := false
	for _, child := range root.children {
		if child == nil {
			continue
		}
		switch child.Type(lang) {
		case "comment",
			"block_comment",
			"package_clause",
			"import_declaration",
			"object_definition",
			"class_definition",
			"trait_definition",
			"enum_definition",
			"function_definition",
			"type_definition",
			"val_definition",
			"var_definition",
			"given_definition":
			sawTopLevel = true
		default:
			return false
		}
	}
	return sawTopLevel
}

func normalizeScalaTrailingCommentOwnership(root *Node, source []byte, lang *Language) {
	if root == nil || len(source) == 0 || lang == nil || lang.Name != "scala" {
		return
	}
	var walk func(*Node, int)
	walk = func(n *Node, depth int) {
		if n == nil || depth > maxTreeWalkDepth {
			return
		}
		normalizeScalaTrailingCommentSiblings(n, source, lang)
		for _, child := range n.children {
			walk(child, depth+1)
		}
	}
	walk(root, 0)
}

func normalizeScalaTrailingCommentSiblings(parent *Node, source []byte, lang *Language) {
	if parent == nil || len(parent.children) < 3 {
		return
	}
	for i := 1; i+1 < len(parent.children); {
		firstComment := parent.children[i]
		if !isScalaCommentNode(firstComment, lang) {
			i++
			continue
		}
		prev := parent.children[i-1]
		body := scalaTrailingCommentTarget(prev, lang)
		if body == nil || body.endByte != firstComment.startByte {
			i++
			continue
		}
		j := i
		for j < len(parent.children) && isScalaCommentNode(parent.children[j], lang) {
			j++
		}
		if j >= len(parent.children) {
			i++
			continue
		}
		next := parent.children[j]
		if next == nil || next.isExtra {
			i++
			continue
		}
		lastComment := parent.children[j-1]

		targetEndByte := lastComment.endByte
		targetEndPoint := lastComment.endPoint
		if lastComment.endByte <= uint32(len(source)) && next.startByte >= lastComment.endByte && next.startByte <= uint32(len(source)) {
			gap := source[lastComment.endByte:next.startByte]
			if bytesAreTrivia(gap) {
				targetEndByte = next.startByte
				targetEndPoint = advancePointByBytes(lastComment.endPoint, gap)
			}
		}

		added := parent.children[i:j]
		rebuiltChildren := make([]*Node, 0, len(body.children)+len(added))
		rebuiltChildren = append(rebuiltChildren, body.children...)
		rebuiltChildren = append(rebuiltChildren, added...)
		body.children = rebuiltChildren

		if len(body.fieldIDs) > 0 {
			rebuiltFieldIDs := make([]FieldID, 0, len(body.fieldIDs)+len(added))
			rebuiltFieldIDs = append(rebuiltFieldIDs, body.fieldIDs...)
			for range added {
				rebuiltFieldIDs = append(rebuiltFieldIDs, 0)
			}
			body.fieldIDs = rebuiltFieldIDs
		}
		if len(body.fieldSources) > 0 {
			rebuiltFieldSources := make([]uint8, 0, len(body.fieldSources)+len(added))
			rebuiltFieldSources = append(rebuiltFieldSources, body.fieldSources...)
			for range added {
				rebuiltFieldSources = append(rebuiltFieldSources, 0)
			}
			body.fieldSources = rebuiltFieldSources
		}
		if targetEndByte > body.endByte {
			body.endByte = targetEndByte
			body.endPoint = targetEndPoint
		}
		if targetEndByte > prev.endByte {
			prev.endByte = targetEndByte
			prev.endPoint = targetEndPoint
		}

		parent.children = append(parent.children[:i], parent.children[j:]...)
		if len(parent.fieldIDs) > 0 {
			parent.fieldIDs = append(parent.fieldIDs[:i], parent.fieldIDs[j:]...)
			if len(parent.fieldSources) > 0 {
				parent.fieldSources = append(parent.fieldSources[:i], parent.fieldSources[j:]...)
			}
		}
	}
}

func isScalaCommentNode(n *Node, lang *Language) bool {
	if n == nil {
		return false
	}
	switch n.Type(lang) {
	case "comment", "block_comment":
		return true
	default:
		return false
	}
}

func scalaTrailingCommentTarget(prev *Node, lang *Language) *Node {
	if prev == nil || lang == nil || len(prev.children) == 0 {
		return nil
	}
	last := prev.children[len(prev.children)-1]
	if last == nil {
		return nil
	}
	switch prev.Type(lang) {
	case "function_definition":
		if last.Type(lang) == "indented_block" {
			return last
		}
	case "trait_definition", "object_definition", "class_definition":
		if last.Type(lang) == "template_body" {
			return last
		}
	case "enum_definition":
		if last.Type(lang) == "enum_body" {
			return last
		}
	}
	return nil
}

func normalizeScalaFunctionModifierFields(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" {
		return
	}
	returnTypeID, ok := lang.FieldByName("return_type")
	if !ok {
		return
	}
	var walk func(*Node, int)
	walk = func(n *Node, depth int) {
		if n == nil || depth > maxTreeWalkDepth {
			return
		}
		if n.Type(lang) == "function_definition" {
			for i, child := range n.children {
				if child == nil || child.Type(lang) != "modifiers" {
					continue
				}
				if i < len(n.fieldIDs) && n.fieldIDs[i] == returnTypeID {
					n.fieldIDs[i] = 0
					if i < len(n.fieldSources) {
						n.fieldSources[i] = fieldSourceNone
					}
				}
			}
		}
		for _, child := range n.children {
			walk(child, depth+1)
		}
	}
	walk(root, 0)
}

func normalizeScalaInterpolatedStringTail(root *Node, source []byte, lang *Language) {
	if root == nil || len(source) == 0 || lang == nil || lang.Name != "scala" {
		return
	}
	var walk func(*Node, int)
	walk = func(n *Node, depth int) {
		if n == nil || depth > maxTreeWalkDepth {
			return
		}
		if n.Type(lang) == "interpolated_string_expression" && len(n.children) >= 2 {
			inner := n.children[1]
			if inner != nil && inner.Type(lang) == "interpolated_string" {
				normalizeScalaSingleLineInterpolatedStringTail(n, inner, source)
			}
		}
		if n.Type(lang) == "field_expression" && len(n.children) >= 2 {
			left := n.children[0]
			right := n.children[1]
			if left != nil && right != nil &&
				left.Type(lang) == "interpolated_string_expression" &&
				right.Type(lang) == "." &&
				left.endByte < right.startByte &&
				right.startByte <= uint32(len(source)) &&
				scalaInterpolatedStringTail(source[left.endByte:right.startByte]) {
				extendNodeEndTo(left, right.startByte, source)
				if len(left.children) >= 2 {
					inner := left.children[1]
					if inner != nil && inner.Type(lang) == "interpolated_string" {
						extendNodeEndTo(inner, right.startByte, source)
					}
				}
			}
		}
		for _, child := range n.children {
			walk(child, depth+1)
		}
		if n.Type(lang) == "infix_expression" && len(n.children) > 0 {
			last := n.children[len(n.children)-1]
			if last != nil && last.Type(lang) == "interpolated_string_expression" && n.endByte < last.endByte {
				extendNodeEndTo(n, last.endByte, source)
			}
		}
	}
	walk(root, 0)
}

func normalizeScalaSingleLineInterpolatedStringTail(expr *Node, inner *Node, source []byte) {
	if expr == nil || inner == nil || inner.startByte >= uint32(len(source)) {
		return
	}
	if source[inner.startByte] != '"' {
		return
	}
	if inner.startByte+2 < uint32(len(source)) &&
		source[inner.startByte+1] == '"' &&
		source[inner.startByte+2] == '"' {
		return
	}
	end, ok := scanScalaSingleLineStringTail(source, inner.endByte)
	if !ok || end <= inner.endByte {
		return
	}
	extendNodeEndTo(inner, end, source)
	extendNodeEndTo(expr, end, source)
}

func scalaInterpolatedStringTail(gap []byte) bool {
	if len(gap) == 0 {
		return false
	}
	hasQuote := false
	for _, c := range gap {
		switch c {
		case ' ', '\t', '\n', '\r', '\f', '|', '}', '"':
			if c == '"' {
				hasQuote = true
			}
		default:
			return false
		}
	}
	return hasQuote
}

func scanScalaSingleLineStringTail(source []byte, start uint32) (uint32, bool) {
	if start >= uint32(len(source)) {
		return 0, false
	}
	for i := start; i < uint32(len(source)); i++ {
		switch source[i] {
		case '\n', '\r':
			return 0, false
		case '"':
			if !isEscapedQuote(source, i) {
				return i + 1, true
			}
		}
	}
	return 0, false
}

func isEscapedQuote(source []byte, idx uint32) bool {
	if idx == 0 || idx > uint32(len(source)) {
		return false
	}
	backslashes := 0
	for i := int(idx) - 1; i >= 0 && source[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func extendNodeEndTo(n *Node, end uint32, source []byte) {
	if n == nil || end <= n.endByte || end > uint32(len(source)) {
		return
	}
	gap := source[n.endByte:end]
	n.endByte = end
	n.endPoint = advancePointByBytes(n.endPoint, gap)
}

func setNodeEndTo(n *Node, end uint32, source []byte) {
	if n == nil || end > uint32(len(source)) || end < n.startByte || end == n.endByte {
		return
	}
	if end > n.endByte {
		extendNodeEndTo(n, end, source)
		return
	}
	n.endByte = end
	n.endPoint = advancePointByBytes(Point{}, source[:end])
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

func collapsePythonRootFragments(nodes []*Node, arena *nodeArena, lang *Language) []*Node {
	if len(nodes) == 0 || lang == nil || lang.Name != "python" {
		return nodes
	}
	nodes = dropZeroWidthUnnamedTail(nodes, lang)
	for {
		next, changed := collapsePythonClassFragments(nodes, arena, lang)
		if changed {
			nodes = next
			nodes = dropZeroWidthUnnamedTail(nodes, lang)
			continue
		}
		next, changed = collapsePythonFunctionFragments(nodes, arena, lang)
		if changed {
			nodes = next
			nodes = dropZeroWidthUnnamedTail(nodes, lang)
			continue
		}
		next, changed = collapsePythonTerminalIfSuffix(nodes, arena, lang)
		if changed {
			nodes = next
			nodes = dropZeroWidthUnnamedTail(nodes, lang)
			continue
		}
		return normalizePythonModuleChildren(nodes, arena, lang)
	}
}

func collapsePythonClassFragments(nodes []*Node, arena *nodeArena, lang *Language) ([]*Node, bool) {
	if len(nodes) < 5 {
		return nodes, false
	}
	classDefSym, ok := symbolByName(lang, "class_definition")
	if !ok {
		return nodes, false
	}
	blockSym, ok := symbolByName(lang, "block")
	if !ok {
		return nodes, false
	}
	for i := 0; i < len(nodes)-4; i++ {
		j := i
		classNode := nodes[j]
		nameNode := nodes[j+1]
		if classNode == nil || nameNode == nil {
			continue
		}
		if classNode.Type(lang) != "class" || nameNode.Type(lang) != "identifier" {
			continue
		}
		var argNode *Node
		if j+5 < len(nodes) && nodes[j+2] != nil && nodes[j+2].Type(lang) == "argument_list" {
			argNode = nodes[j+2]
			j++
		}
		colonNode := nodes[j+2]
		indentNode := nodes[j+3]
		bodyNode := nodes[j+4]
		if colonNode == nil || indentNode == nil || bodyNode == nil {
			continue
		}
		if colonNode.Type(lang) != ":" || indentNode.Type(lang) != "_indent" {
			continue
		}

		bodyStart := j + 4
		bodyEnd := bodyStart + 1
		var bodyChildren []*Node
		if bodyNode.Type(lang) == "module_repeat1" {
			bodyChildren = flattenPythonModuleRepeat(bodyNode, nil, lang)
		} else {
			var ok bool
			bodyChildren, bodyEnd, ok = pythonCollectIndentedSuite(nodes, bodyStart, classNode.startPoint.Column)
			if !ok {
				continue
			}
			bodyChildren = collapsePythonRootFragments(bodyChildren, arena, lang)
		}
		if len(bodyChildren) == 0 {
			continue
		}
		if arena != nil {
			buf := arena.allocNodeSlice(len(bodyChildren))
			copy(buf, bodyChildren)
			bodyChildren = buf
		}
		blockNode := newParentNodeInArena(arena, blockSym, true, bodyChildren, nil, 0)
		if repairedBlock, changed := repairPythonBlock(blockNode, arena, lang, true); changed {
			blockNode = repairedBlock
		} else {
			blockNode.hasError = false
		}

		classChildren := make([]*Node, 0, 5)
		classChildren = append(classChildren, classNode, nameNode)
		if argNode != nil {
			classChildren = append(classChildren, argNode)
		}
		classChildren = append(classChildren, colonNode, blockNode)
		if arena != nil {
			buf := arena.allocNodeSlice(len(classChildren))
			copy(buf, classChildren)
			classChildren = buf
		}
		classFieldIDs := pythonSyntheticClassFieldIDs(arena, len(classChildren), argNode != nil, lang)
		classDef := newParentNodeInArena(arena, classDefSym, true, classChildren, classFieldIDs, 0)
		classDef.hasError = false

		out := make([]*Node, 0, len(nodes)-(bodyEnd-i)+1)
		out = append(out, nodes[:i]...)
		out = append(out, classDef)
		out = append(out, nodes[bodyEnd:]...)
		if arena != nil {
			buf := arena.allocNodeSlice(len(out))
			copy(buf, out)
			out = buf
		}
		return out, true
	}
	return nodes, false
}

func collapsePythonFunctionFragments(nodes []*Node, arena *nodeArena, lang *Language) ([]*Node, bool) {
	if len(nodes) < 6 || lang == nil || lang.Name != "python" {
		return nodes, false
	}
	functionDefSym, ok := symbolByName(lang, "function_definition")
	if !ok {
		return nodes, false
	}
	blockSym, ok := symbolByName(lang, "block")
	if !ok {
		return nodes, false
	}
	for i := 0; i < len(nodes)-5; i++ {
		defNode := nodes[i]
		nameNode := nodes[i+1]
		paramsNode := nodes[i+2]
		colonNode := nodes[i+3]
		indentNode := nodes[i+4]
		if defNode == nil || nameNode == nil || paramsNode == nil || colonNode == nil || indentNode == nil {
			continue
		}
		if defNode.Type(lang) != "def" || nameNode.Type(lang) != "identifier" || paramsNode.Type(lang) != "parameters" {
			continue
		}
		if colonNode.Type(lang) != ":" || indentNode.Type(lang) != "_indent" {
			continue
		}
		bodyChildren, bodyEnd, ok := pythonCollectIndentedSuite(nodes, i+5, defNode.startPoint.Column)
		if !ok {
			continue
		}
		bodyChildren = collapsePythonRootFragments(bodyChildren, arena, lang)
		if len(bodyChildren) == 0 {
			continue
		}
		if arena != nil {
			buf := arena.allocNodeSlice(len(bodyChildren))
			copy(buf, bodyChildren)
			bodyChildren = buf
		}
		blockNode := newParentNodeInArena(arena, blockSym, true, bodyChildren, nil, 0)
		if repairedBlock, changed := repairPythonBlock(blockNode, arena, lang, false); changed {
			blockNode = repairedBlock
		} else {
			blockNode.hasError = false
		}
		fnChildren := []*Node{defNode, nameNode, paramsNode, colonNode, blockNode}
		if arena != nil {
			buf := arena.allocNodeSlice(len(fnChildren))
			copy(buf, fnChildren)
			fnChildren = buf
		}
		fn := newParentNodeInArena(arena, functionDefSym, true, fnChildren, pythonSyntheticFunctionFieldIDs(arena, len(fnChildren), lang), 0)
		fn.hasError = false

		out := make([]*Node, 0, len(nodes)-(bodyEnd-i)+1)
		out = append(out, nodes[:i]...)
		out = append(out, fn)
		out = append(out, nodes[bodyEnd:]...)
		if arena != nil {
			buf := arena.allocNodeSlice(len(out))
			copy(buf, out)
			out = buf
		}
		return out, true
	}
	return nodes, false
}
func collapsePythonTerminalIfSuffix(nodes []*Node, arena *nodeArena, lang *Language) ([]*Node, bool) {
	if len(nodes) < 6 {
		return nodes, false
	}
	ifSym, ok := symbolByName(lang, "if_statement")
	if !ok {
		return nodes, false
	}
	blockSym, ok := symbolByName(lang, "block")
	if !ok {
		return nodes, false
	}
	n := len(nodes)
	ifNode := nodes[n-6]
	condNode := nodes[n-5]
	colonNode := nodes[n-4]
	indentNode := nodes[n-3]
	bodyNode := nodes[n-2]
	dedentNode := nodes[n-1]
	if ifNode == nil || condNode == nil || colonNode == nil || indentNode == nil || bodyNode == nil || dedentNode == nil {
		return nodes, false
	}
	if ifNode.Type(lang) != "if" || colonNode.Type(lang) != ":" || indentNode.Type(lang) != "_indent" || bodyNode.Type(lang) != "_simple_statements" || dedentNode.Type(lang) != "_dedent" {
		return nodes, false
	}
	if !condNode.IsNamed() {
		return nodes, false
	}

	blockChildren := []*Node{indentNode, bodyNode, dedentNode}
	if arena != nil {
		buf := arena.allocNodeSlice(len(blockChildren))
		copy(buf, blockChildren)
		blockChildren = buf
	}
	blockNode := newParentNodeInArena(arena, blockSym, true, blockChildren, nil, 0)
	blockNode.hasError = false

	ifChildren := []*Node{ifNode, condNode, colonNode, blockNode}
	if arena != nil {
		buf := arena.allocNodeSlice(len(ifChildren))
		copy(buf, ifChildren)
		ifChildren = buf
	}
	ifFieldIDs := pythonSyntheticIfFieldIDs(arena, len(ifChildren), lang)
	ifStmt := newParentNodeInArena(arena, ifSym, true, ifChildren, ifFieldIDs, 0)
	ifStmt.hasError = false

	out := make([]*Node, 0, n-5)
	out = append(out, nodes[:n-6]...)
	out = append(out, ifStmt)
	if arena != nil {
		buf := arena.allocNodeSlice(len(out))
		copy(buf, out)
		return buf, true
	}
	return out, true
}

func flattenPythonModuleRepeat(node *Node, out []*Node, lang *Language) []*Node {
	if node == nil {
		return out
	}
	if node.Type(lang) == "module_repeat1" {
		for _, child := range node.children {
			out = flattenPythonModuleRepeat(child, out, lang)
		}
		return out
	}
	if node.IsNamed() {
		out = append(out, node)
	}
	return out
}

func pythonCollectIndentedSuite(nodes []*Node, start int, baseColumn uint32) ([]*Node, int, bool) {
	if start >= len(nodes) {
		return nil, start, false
	}
	end := start
	for end < len(nodes) {
		cur := nodes[end]
		if cur == nil {
			end++
			continue
		}
		if cur.startPoint.Column <= baseColumn {
			break
		}
		end++
	}
	if end == start {
		return nil, start, false
	}
	return nodes[start:end], end, true
}

func normalizePythonModuleChildren(nodes []*Node, arena *nodeArena, lang *Language) []*Node {
	if len(nodes) == 0 || lang == nil || lang.Name != "python" {
		return nodes
	}
	out := make([]*Node, 0, len(nodes))
	changed := false
	for _, node := range nodes {
		if node == nil {
			continue
		}
		normalized, nodeChanged := normalizePythonModuleNode(node, lang)
		if nodeChanged {
			out = append(out, normalized)
			changed = true
			continue
		}
		out = append(out, node)
	}
	if !changed {
		return nodes
	}
	if arena != nil {
		buf := arena.allocNodeSlice(len(out))
		copy(buf, out)
		return buf
	}
	return out
}

func normalizePythonModuleNode(node *Node, lang *Language) (*Node, bool) {
	changed := false
	for node != nil {
		if node.Type(lang) == "_simple_statements" && len(node.children) == 1 {
			child := node.children[0]
			if child != nil && child.IsNamed() {
				node = child
				changed = true
				continue
			}
		}
		if node.Type(lang) == "expression_statement" && len(node.children) == 1 {
			child := node.children[0]
			if child != nil && child.IsNamed() {
				node = child
				changed = true
				continue
			}
		}
		if (node.Type(lang) == "expression" || node.Type(lang) == "primary_expression") && len(node.children) == 1 {
			child := node.children[0]
			if child != nil && child.IsNamed() {
				node = child
				changed = true
				continue
			}
		}
		break
	}
	return node, changed
}

func repairPythonRootNode(root *Node, arena *nodeArena, lang *Language) *Node {
	if root == nil || lang == nil || lang.Name != "python" || root.Type(lang) != "module" {
		return root
	}
	children := collapsePythonRootFragments(root.children, arena, lang)
	changed := len(children) != len(root.children)
	if !changed {
		for i := range children {
			if children[i] != root.children[i] {
				changed = true
				break
			}
		}
	}

	repaired := make([]*Node, 0, len(children))
	for _, child := range children {
		fixed := repairPythonTopLevelNode(child, arena, lang)
		if fixed != child {
			changed = true
		}
		repaired = append(repaired, fixed)
	}

	if !changed {
		if root.hasError && pythonModuleChildrenLookComplete(repaired, lang) {
			cloned := cloneNodeInArena(arena, root)
			cloned.hasError = false
			return cloned
		}
		return root
	}

	cloned := cloneNodeInArena(arena, root)
	if arena != nil {
		buf := arena.allocNodeSlice(len(repaired))
		copy(buf, repaired)
		repaired = buf
	}
	cloned.children = repaired
	cloned.fieldIDs = nil
	cloned.fieldSources = nil
	if pythonModuleChildrenLookComplete(repaired, lang) {
		cloned.hasError = false
	}
	return cloned
}

func repairPythonTopLevelNode(node *Node, arena *nodeArena, lang *Language) *Node {
	if node == nil || lang == nil || lang.Name != "python" {
		return node
	}
	return repairPythonNode(node, arena, lang)
}

func repairPythonNode(node *Node, arena *nodeArena, lang *Language) *Node {
	if node == nil || lang == nil || lang.Name != "python" {
		return node
	}
	normalized, changed := normalizePythonModuleNode(node, lang)
	if changed {
		node = normalized
	}
	switch node.Type(lang) {
	case "class_definition":
		return repairPythonClassDefinition(node, arena, lang)
	case "function_definition":
		return repairPythonFunctionDefinition(node, arena, lang)
	case "if_statement":
		return repairPythonIfStatement(node, arena, lang)
	case "block":
		repaired, _ := repairPythonBlock(node, arena, lang, false)
		return repaired
	default:
		return node
	}
}

func repairPythonClassDefinition(node *Node, arena *nodeArena, lang *Language) *Node {
	if node == nil || node.Type(lang) != "class_definition" || len(node.children) == 0 {
		return node
	}
	bodyIndex := -1
	for i, child := range node.children {
		if child != nil && child.Type(lang) == "block" {
			bodyIndex = i
		}
	}
	if bodyIndex < 0 {
		return node
	}
	body := node.children[bodyIndex]
	repairedBody, changed := repairPythonBlock(body, arena, lang, true)
	if !changed {
		return node
	}

	cloned := cloneNodeInArena(arena, node)
	children := make([]*Node, len(node.children))
	copy(children, node.children)
	children[bodyIndex] = repairedBody
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	cloned.children = children
	if repairedBody != nil {
		cloned.endByte = repairedBody.endByte
		cloned.endPoint = repairedBody.endPoint
	}
	return cloned
}

func repairPythonFunctionDefinition(node *Node, arena *nodeArena, lang *Language) *Node {
	if node == nil || node.Type(lang) != "function_definition" || len(node.children) == 0 {
		return node
	}
	bodyIndex := -1
	for i, child := range node.children {
		if child != nil && child.Type(lang) == "block" {
			bodyIndex = i
		}
	}
	if bodyIndex < 0 {
		return node
	}
	body := node.children[bodyIndex]
	repairedBody, changed := repairPythonBlock(body, arena, lang, false)
	if !changed {
		return node
	}

	cloned := cloneNodeInArena(arena, node)
	children := make([]*Node, len(node.children))
	copy(children, node.children)
	children[bodyIndex] = repairedBody
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	cloned.children = children
	if repairedBody != nil {
		cloned.endByte = repairedBody.endByte
		cloned.endPoint = repairedBody.endPoint
	}
	return cloned
}

func repairPythonIfStatement(node *Node, arena *nodeArena, lang *Language) *Node {
	if node == nil || node.Type(lang) != "if_statement" || len(node.children) == 0 {
		return node
	}
	children := make([]*Node, len(node.children))
	changed := false
	for i, child := range node.children {
		repaired := repairPythonNode(child, arena, lang)
		if repaired != child {
			changed = true
		}
		children[i] = repaired
	}
	if !changed {
		return node
	}

	cloned := cloneNodeInArena(arena, node)
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	cloned.children = children
	last := children[len(children)-1]
	if last != nil {
		cloned.endByte = last.endByte
		cloned.endPoint = last.endPoint
	}
	return cloned
}

func repairPythonBlock(node *Node, arena *nodeArena, lang *Language, allowHoist bool) (*Node, bool) {
	if node == nil || node.Type(lang) != "block" {
		return node, false
	}
	pending := append([]*Node(nil), node.children...)
	out := make([]*Node, 0, len(node.children))
	changed := false

	for len(pending) > 0 {
		cur := pending[0]
		pending = pending[1:]
		if cur == nil {
			continue
		}
		norm, normChanged := normalizePythonModuleNode(cur, lang)
		if normChanged {
			changed = true
		}
		cur = norm
		if cur != nil {
			switch cur.Type(lang) {
			case "_indent", "_dedent":
				changed = true
				continue
			case "_simple_statements":
				flat := flattenPythonSimpleStatements(cur, nil, lang)
				if len(flat) > 0 {
					changed = true
					pending = append(append([]*Node{}, flat...), pending...)
					continue
				}
			}
		}

		if allowHoist && cur != nil && cur.Type(lang) == "function_definition" {
			repairedFn, hoisted, split := splitPythonOvernestedFunction(cur, arena, lang)
			if split {
				changed = true
				repairedFn = repairPythonNode(repairedFn, arena, lang)
				out = append(out, repairedFn)
				if len(hoisted) > 0 {
					pending = append(append([]*Node{}, hoisted...), pending...)
				}
				continue
			}
		}

		repaired := repairPythonNode(cur, arena, lang)
		if repaired != cur {
			changed = true
		}
		out = append(out, repaired)
	}

	if !changed {
		firstNamed := pythonBlockStartAnchor(out, lang)
		lastSpan := pythonBlockEndAnchor(out)
		if firstNamed == nil || lastSpan == nil {
			return node, false
		}
		if node.startByte == firstNamed.startByte &&
			node.startPoint == firstNamed.startPoint &&
			node.endByte == lastSpan.endByte &&
			node.endPoint == lastSpan.endPoint {
			return node, false
		}
		changed = true
	}

	cloned := cloneNodeInArena(arena, node)
	if arena != nil {
		buf := arena.allocNodeSlice(len(out))
		copy(buf, out)
		out = buf
	}
	cloned.children = out
	cloned.fieldIDs = nil
	cloned.fieldSources = nil
	firstNamed := pythonBlockStartAnchor(out, lang)
	lastSpan := pythonBlockEndAnchor(out)
	if firstNamed != nil {
		cloned.startByte = firstNamed.startByte
		cloned.startPoint = firstNamed.startPoint
	}
	if lastSpan != nil {
		cloned.endByte = lastSpan.endByte
		cloned.endPoint = lastSpan.endPoint
	}
	return cloned, true
}
func pythonBlockStartAnchor(children []*Node, lang *Language) *Node {
	for _, child := range children {
		if child == nil {
			continue
		}
		typ := child.Type(lang)
		if typ == "_indent" || typ == "_dedent" {
			continue
		}
		if child.endByte > child.startByte || child.IsNamed() {
			return child
		}
	}
	return nil
}

func pythonBlockEndAnchor(children []*Node) *Node {
	for i := len(children) - 1; i >= 0; i-- {
		child := children[i]
		if child != nil && child.endByte > child.startByte {
			return child
		}
	}
	return nil
}

func flattenDPropertyTypeChain(n *Node, lang *Language) ([]*Node, bool) {
	if n == nil || lang == nil {
		return nil, false
	}
	switch n.Type(lang) {
	case "identifier":
		return []*Node{n}, true
	case "property_expression":
		if len(n.children) != 3 || n.children[1] == nil || n.children[2] == nil {
			return nil, false
		}
		if n.children[1].Type(lang) != "." || n.children[2].Type(lang) != "identifier" {
			return nil, false
		}
		left, ok := flattenDPropertyTypeChain(n.children[0], lang)
		if !ok {
			return nil, false
		}
		out := make([]*Node, 0, len(left)+2)
		out = append(out, left...)
		out = append(out, n.children[1], n.children[2])
		return out, true
	default:
		return nil, false
	}
}

func splitPythonOvernestedFunction(node *Node, arena *nodeArena, lang *Language) (*Node, []*Node, bool) {
	if node == nil || node.Type(lang) != "function_definition" {
		return node, nil, false
	}
	bodyIndex := -1
	for i, child := range node.children {
		if child != nil && child.Type(lang) == "block" {
			bodyIndex = i
		}
	}
	if bodyIndex < 0 {
		return node, nil, false
	}
	body := node.children[bodyIndex]
	if body == nil || len(body.children) == 0 {
		return node, nil, false
	}
	fnColumn := node.startPoint.Column
	hoistStart := -1
	for i, child := range body.children {
		if child == nil || !child.IsNamed() {
			continue
		}
		if child.startPoint.Column <= fnColumn {
			hoistStart = i
			break
		}
	}
	if hoistStart <= 0 {
		return node, nil, false
	}

	kept := append([]*Node(nil), body.children[:hoistStart]...)
	hoisted := append([]*Node(nil), body.children[hoistStart:]...)
	if len(kept) == 0 {
		return node, nil, false
	}

	newBody := cloneNodeInArena(arena, body)
	if arena != nil {
		buf := arena.allocNodeSlice(len(kept))
		copy(buf, kept)
		kept = buf
	}
	newBody.children = kept
	newBody.fieldIDs = nil
	newBody.fieldSources = nil
	lastKept := kept[len(kept)-1]
	newBody.endByte = lastKept.endByte
	newBody.endPoint = lastKept.endPoint

	newFn := cloneNodeInArena(arena, node)
	fnChildren := make([]*Node, len(node.children))
	copy(fnChildren, node.children)
	fnChildren[bodyIndex] = newBody
	if arena != nil {
		buf := arena.allocNodeSlice(len(fnChildren))
		copy(buf, fnChildren)
		fnChildren = buf
	}
	newFn.children = fnChildren
	newFn.endByte = newBody.endByte
	newFn.endPoint = newBody.endPoint
	return newFn, hoisted, true
}

func flattenPythonSimpleStatements(node *Node, out []*Node, lang *Language) []*Node {
	if node == nil {
		return out
	}
	switch node.Type(lang) {
	case "_simple_statements", "_simple_statements_repeat1":
		for _, child := range node.children {
			out = flattenPythonSimpleStatements(child, out, lang)
		}
		return out
	case "expression_statement":
		if len(node.children) == 1 && node.children[0] != nil && node.children[0].IsNamed() {
			return append(out, node.children[0])
		}
	}
	if node.IsNamed() || (lang != nil && node.Type(lang) == ";") {
		return append(out, node)
	}
	return out
}

func normalizePythonStringContinuationEscapes(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "python" || len(source) == 0 {
		return
	}
	escapeSym, ok := symbolByName(lang, "escape_sequence")
	if !ok {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "string_content" && n.startByte < n.endByte && int(n.endByte) <= len(source) {
			n.children = addPythonContinuationEscapes(n, source, escapeSym)
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func addPythonContinuationEscapes(node *Node, source []byte, escapeSym Symbol) []*Node {
	if node == nil || node.startByte >= node.endByte || int(node.endByte) > len(source) {
		return node.children
	}
	children := node.children
	changed := false
	for i := int(node.startByte); i+1 < int(node.endByte); i++ {
		if source[i] != '\\' {
			continue
		}
		end := i + 2
		if source[i+1] == '\r' && end < int(node.endByte) && source[end] == '\n' {
			end++
		} else if source[i+1] != '\n' {
			continue
		}
		found := false
		for _, child := range children {
			if child != nil && child.startByte == uint32(i) && child.endByte == uint32(end) && child.symbol == escapeSym {
				found = true
				break
			}
		}
		if found {
			i = end - 1
			continue
		}
		startPoint := advancePointByBytes(Point{}, source[:i])
		esc := newLeafNodeInArena(node.ownerArena, escapeSym, true, uint32(i), uint32(end), startPoint, advancePointByBytes(startPoint, source[i:end]))
		insertAt := len(children)
		for idx, child := range children {
			if child == nil || child.startByte > uint32(i) {
				insertAt = idx
				break
			}
		}
		next := make([]*Node, 0, len(children)+1)
		next = append(next, children[:insertAt]...)
		next = append(next, esc)
		next = append(next, children[insertAt:]...)
		if node.ownerArena != nil {
			buf := node.ownerArena.allocNodeSlice(len(next))
			copy(buf, next)
			next = buf
		}
		children = next
		changed = true
		i = end - 1
	}
	if !changed {
		return node.children
	}
	return children
}

func pythonSyntheticClassFieldIDs(arena *nodeArena, childCount int, hasArgList bool, lang *Language) []FieldID {
	fieldIDs := make([]FieldID, childCount)
	if arena != nil {
		fieldIDs = arena.allocFieldIDSlice(childCount)
	}
	if fid, ok := lang.FieldByName("name"); ok && childCount > 1 {
		fieldIDs[1] = fid
	}
	if hasArgList {
		if fid, ok := lang.FieldByName("superclasses"); ok && childCount > 2 {
			fieldIDs[2] = fid
		}
		if fid, ok := lang.FieldByName("body"); ok && childCount > 4 {
			fieldIDs[4] = fid
		}
		return fieldIDs
	}
	if fid, ok := lang.FieldByName("body"); ok && childCount > 3 {
		fieldIDs[3] = fid
	}
	return fieldIDs
}

func pythonSyntheticFunctionFieldIDs(arena *nodeArena, childCount int, lang *Language) []FieldID {
	fieldIDs := make([]FieldID, childCount)
	if arena != nil {
		fieldIDs = arena.allocFieldIDSlice(childCount)
	}
	if fid, ok := lang.FieldByName("name"); ok && childCount > 1 {
		fieldIDs[1] = fid
	}
	if fid, ok := lang.FieldByName("parameters"); ok && childCount > 2 {
		fieldIDs[2] = fid
	}
	if fid, ok := lang.FieldByName("body"); ok && childCount > 4 {
		fieldIDs[4] = fid
	}
	return fieldIDs
}
func pythonSyntheticIfFieldIDs(arena *nodeArena, childCount int, lang *Language) []FieldID {
	fieldIDs := make([]FieldID, childCount)
	if arena != nil {
		fieldIDs = arena.allocFieldIDSlice(childCount)
	}
	if fid, ok := lang.FieldByName("condition"); ok && childCount > 1 {
		fieldIDs[1] = fid
	}
	if fid, ok := lang.FieldByName("consequence"); ok && childCount > 3 {
		fieldIDs[3] = fid
	}
	return fieldIDs
}

func pythonModuleChildrenLookComplete(nodes []*Node, lang *Language) bool {
	if len(nodes) == 0 {
		return false
	}
	seen := 0
	for _, n := range nodes {
		if n == nil || n.isExtra {
			continue
		}
		if n.IsNamed() {
			seen++
			continue
		}
		switch n.Type(lang) {
		case "_simple_statements":
			seen++
		default:
			return false
		}
	}
	return seen > 0
}

func dartProgramChildrenLookComplete(nodes []*Node, lang *Language) bool {
	if len(nodes) == 0 || lang == nil || lang.Name != "dart" {
		return false
	}
	seen := 0
	for _, n := range nodes {
		if n == nil || n.isExtra {
			continue
		}
		if n.IsNamed() {
			seen++
			continue
		}
		switch n.Type(lang) {
		case ";":
			seen++
		default:
			return false
		}
	}
	return seen > 0
}

func dropZeroWidthUnnamedTail(nodes []*Node, lang *Language) []*Node {
	for len(nodes) > 0 {
		last := nodes[len(nodes)-1]
		if last == nil {
			nodes = nodes[:len(nodes)-1]
			continue
		}
		if last.IsNamed() || last.startByte != last.endByte || len(last.children) > 0 {
			break
		}
		if lang != nil && last.Type(lang) != "" {
			break
		}
		nodes = nodes[:len(nodes)-1]
	}
	return nodes
}

func javaScriptSymbolMeta(lang *Language, name string) (Symbol, bool, bool) {
	if lang == nil {
		return 0, false, false
	}
	sym, ok := symbolByName(lang, name)
	if !ok {
		return 0, false, false
	}
	named := false
	if int(sym) < len(lang.SymbolMetadata) {
		named = lang.SymbolMetadata[sym].Named
	}
	return sym, named, true
}

func symbolByName(lang *Language, name string) (Symbol, bool) {
	if lang == nil {
		return 0, false
	}
	for i, symName := range lang.SymbolNames {
		if symName == name {
			return Symbol(i), true
		}
	}
	return 0, false
}
