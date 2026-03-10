package gotreesitter

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
		candidate = repairPythonRootNode(candidate, arena, p.language)
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
		normalizeKnownSpanAttribution(realRoot, source, p.language)
		if returnRealRoot {
			if shouldWireParentLinks {
				wireParentLinksWithScratch(realRoot, linkScratch)
			}
			return newTreeWithArenas(realRoot, source, p.language, arena, getBorrowed())
		}
	}

	rootChildren := nodes
	rootSymbol := nodes[len(nodes)-1].symbol
	rootHasError := false
	for _, n := range nodes {
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
	if rootHasError && !(p != nil && p.language != nil && p.language.Name == "python" && hasExpectedRoot && pythonModuleChildrenLookComplete(nodes, p.language)) {
		root.hasError = true
	}
	root = repairPythonRootNode(root, arena, p.language)
	extendNodeToTrailingWhitespace(root, source)
	p.normalizeRootSourceStart(root, source)
	normalizeKnownSpanAttribution(root, source, p.language)
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

// normalizeKnownSpanAttribution applies narrow compatibility fixes where
// C tree-sitter attributes trailing trivia to a grouped node but this runtime
// currently drops it during child normalization.
func normalizeKnownSpanAttribution(root *Node, source []byte, lang *Language) {
	normalizeCobolLeadingAreaStart(root, source, lang)
	normalizeCooklangTrailingStepTail(root, source, lang)
	normalizeDartConstructorSignatureKinds(root, source, lang)
	normalizeDartSingleTypeArgumentFreeCalls(root, lang)
	normalizeDartSwitchExpressionBodyFields(root, lang)
	normalizeDModuleDefinitionBounds(root, lang)
	normalizeDCallExpressionTemplateTypes(root, lang)
	normalizeDCallExpressionPropertyTypes(root, lang)
	normalizeDCallExpressionSimpleTypeCallees(root, lang)
	normalizeDVariableTypeQualifiers(root, lang)
	normalizeDVariableStorageClassWrappers(root, lang)
	normalizeFortranStatementLineBreaks(root, source, lang)
	normalizeHaskellImportsSpan(root, source, lang)
	normalizeHaskellZeroWidthTokens(root, lang)
	normalizeHaskellRootImportField(root, lang)
	normalizeHaskellDeclarationsSpan(root, source, lang)
	normalizeIniSectionStarts(root, lang)
	normalizeMakeConditionalConsequenceFields(root, lang)
	normalizeNginxAttributeLineBreaks(root, source, lang)
	normalizeTopLevelTrailingLineBreakSpan(root, source, lang)
	normalizeCSharpTypeConstraintKeywords(root, lang)
	normalizeCSharpSwitchTupleCasePatterns(root, lang)
	normalizeElixirNestedCallTargetFields(root, lang)
	normalizePHPSingletonTypeWrappers(root, lang)
	normalizePHPStaticFunctionFragments(root, source, lang)
	normalizePerlJoinAssignmentLists(root, source, lang)
	normalizePerlPushExpressionLists(root, source, lang)
	normalizePerlReturnExpressionLists(root, lang)
	normalizeScalaTrailingCommentOwnership(root, source, lang)
	normalizeScalaFunctionModifierFields(root, lang)
	normalizeScalaInterpolatedStringTail(root, source, lang)
	normalizeZigEmptyInitListFields(root, lang)
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
	parser := NewParser(lang)
	tree, err := parser.Parse(wrapped)
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
	if root == nil || lang == nil || lang.Name != "cobol" || len(source) == 0 {
		return
	}
	start := firstNonWhitespaceByte(source)
	if start == 0 {
		return
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
	if len(root.children) != 1 {
		return
	}
	def := root.children[0]
	if def == nil || def.Type(lang) != "program_definition" {
		return
	}
	setNodeStartTo(def)
	if len(def.children) != 1 {
		return
	}
	div := def.children[0]
	if div == nil || div.Type(lang) != "identification_division" {
		return
	}
	setNodeStartTo(div)
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
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
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
			walk(child)
		}
	}
	walk(root)
}

func normalizeNginxAttributeLineBreaks(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "nginx" || len(source) == 0 {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "attribute" {
			if end := lineBreakEndAt(source, n.endByte, uint32(len(source))); end > n.endByte {
				extendNodeEndTo(n, end, source)
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
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
	named := false
	if idx := int(patternSym); idx < len(lang.SymbolMetadata) {
		named = lang.SymbolMetadata[patternSym].Named
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "switch_section" && len(n.children) > 1 {
			pat := n.children[1]
			if n.children[0] != nil && n.children[0].Type(lang) == "case" &&
				pat != nil && pat.Type(lang) == "tuple_expression" {
				repl := newParentNodeInArena(n.ownerArena, patternSym, named, []*Node{pat}, nil, 0)
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

func normalizeScalaTrailingCommentOwnership(root *Node, source []byte, lang *Language) {
	if root == nil || len(source) == 0 || lang == nil || lang.Name != "scala" {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		normalizeScalaTrailingCommentSiblings(n, source, lang)
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
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
		body := scalaIndentedCommentTarget(prev, lang)
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

func scalaIndentedCommentTarget(prev *Node, lang *Language) *Node {
	if prev == nil || lang == nil || prev.Type(lang) != "function_definition" || len(prev.children) == 0 {
		return nil
	}
	last := prev.children[len(prev.children)-1]
	if last == nil || last.Type(lang) != "indented_block" {
		return nil
	}
	return last
}

func normalizeScalaFunctionModifierFields(root *Node, lang *Language) {
	if root == nil || lang == nil || lang.Name != "scala" {
		return
	}
	returnTypeID, ok := lang.FieldByName("return_type")
	if !ok {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
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
			walk(child)
		}
	}
	walk(root)
}

func normalizeScalaInterpolatedStringTail(root *Node, source []byte, lang *Language) {
	if root == nil || len(source) == 0 || lang == nil || lang.Name != "scala" {
		return
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
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
			walk(child)
		}
		if n.Type(lang) == "infix_expression" && len(n.children) > 0 {
			last := n.children[len(n.children)-1]
			if last != nil && last.Type(lang) == "interpolated_string_expression" && n.endByte < last.endByte {
				extendNodeEndTo(n, last.endByte, source)
			}
		}
	}
	walk(root)
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
		if colonNode.Type(lang) != ":" || indentNode.Type(lang) != "_indent" || bodyNode.Type(lang) != "module_repeat1" {
			continue
		}

		bodyChildren := flattenPythonModuleRepeat(bodyNode, nil, lang)
		if len(bodyChildren) == 0 {
			continue
		}
		if arena != nil {
			buf := arena.allocNodeSlice(len(bodyChildren))
			copy(buf, bodyChildren)
			bodyChildren = buf
		}
		blockNode := newParentNodeInArena(arena, blockSym, true, bodyChildren, nil, 0)
		blockNode.hasError = false

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

		out := make([]*Node, 0, len(nodes)-(j+5-i)+1)
		out = append(out, nodes[:i]...)
		out = append(out, classDef)
		out = append(out, nodes[j+5:]...)
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
