package gotreesitter

func csharpRecoverTopLevelStatementFromRange(source []byte, start, end uint32, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil || p.language == nil || arena == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	if stmt, ok := csharpRecoverTopLevelLocalDeclarationStatementFromRange(source, start, end, p, arena); ok {
		return stmt, true
	}
	if stmt, ok := csharpRecoverTopLevelLocalFunctionStatementFromRange(source, start, end, p, arena); ok {
		return stmt, true
	}
	if stmt, ok := csharpRecoverWrappedStatementNodeFromRange(source, start, end, p, arena); ok {
		return csharpWrapRecoveredStatementAsGlobal(arena, p.language, stmt)
	}
	return nil, false
}

func csharpRecoverWrappedStatementNodeFromRange(source []byte, start, end uint32, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil || p.language == nil || arena == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	start, end = csharpTrimSpaceBounds(source, start, end)
	if start >= end {
		return nil, false
	}
	const prefix = "class __Q { void __M() { "
	const suffix = " } }\n"
	wrapped := make([]byte, 0, len(prefix)+int(end-start)+len(suffix))
	wrapped = append(wrapped, prefix...)
	wrapped = append(wrapped, source[start:end]...)
	wrapped = append(wrapped, suffix...)
	tree, err := p.parseForRecovery(wrapped)
	if err != nil || tree == nil || tree.RootNode() == nil {
		if tree != nil {
			tree.Release()
		}
		return nil, false
	}
	defer tree.Release()
	stmt := csharpExtractRecoveredWrappedMethodStatement(tree.RootNode(), p.language, arena)
	if stmt == nil {
		return csharpRecoverSimpleTypePatternSwitchStatementFromRange(source, start, end, p, arena)
	}
	if !shiftNodeBytes(stmt, int64(start)-int64(len(prefix))) {
		return nil, false
	}
	recomputeNodePointsFromBytes(stmt, source)
	return stmt, true
}

func csharpRecoverTopLevelLocalDeclarationStatementFromRange(source []byte, start, end uint32, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil || p.language == nil || arena == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	start, end = csharpTrimSpaceBounds(source, start, end)
	if start >= end || source[end-1] != ';' {
		return nil, false
	}
	stmtEnd := end - 1
	eqPos, ok := csharpFindTopLevelAssignment(source, start, stmtEnd)
	if !ok {
		return nil, false
	}
	valueStart := csharpSkipSpaceBytes(source, eqPos+1)
	valueEnd := csharpTrimRightSpaceBytes(source, stmtEnd)
	if valueStart >= valueEnd {
		return nil, false
	}
	const prefix = "class __Q { void __M() { "
	const suffix = " } }\n"
	wrapped := make([]byte, 0, len(prefix)+int(end-start)+len(suffix))
	wrapped = append(wrapped, prefix...)
	wrapped = append(wrapped, source[start:end]...)
	wrapped = append(wrapped, suffix...)
	valueStartInWrapped := uint32(len(prefix)) + (valueStart - start)
	valueEndInWrapped := uint32(len(prefix)) + (valueEnd - start)
	for i := valueStartInWrapped; i < valueEndInWrapped; i++ {
		wrapped[i] = ' '
	}
	wrapped[valueStartInWrapped] = '0'
	tree, err := p.parseForRecovery(wrapped)
	if err != nil || tree == nil || tree.RootNode() == nil {
		if tree != nil {
			tree.Release()
		}
		return nil, false
	}
	defer tree.Release()
	stmt := csharpExtractRecoveredWrappedMethodStatement(tree.RootNode(), p.language, arena)
	if stmt == nil || stmt.Type(p.language) != "local_declaration_statement" {
		return nil, false
	}
	if !shiftNodeBytes(stmt, int64(start)-int64(len(prefix))) {
		return nil, false
	}
	expr, ok := csharpRecoverExpressionNodeFromRange(source, valueStart, valueEnd, p, arena)
	if !ok {
		return nil, false
	}
	if !csharpReplaceRecoveredVariableInitializer(stmt, p.language, expr) {
		return nil, false
	}
	recomputeNodePointsFromBytes(stmt, source)
	return csharpWrapRecoveredStatementAsGlobal(arena, p.language, stmt)
}

func csharpRecoverTopLevelLocalFunctionStatementFromRange(source []byte, start, end uint32, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil || p.language == nil || arena == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	start, end = csharpTrimSpaceBounds(source, start, end)
	if start >= end || source[end-1] != '}' {
		return nil, false
	}
	openRel := 0
	for openRel < int(end-start) && source[start+uint32(openRel)] != '{' {
		openRel++
	}
	if start+uint32(openRel) >= end || source[start+uint32(openRel)] != '{' {
		return nil, false
	}
	openBrace := start + uint32(openRel)
	closeBrace := findMatchingBraceByte(source, int(openBrace), int(end))
	if closeBrace < 0 || uint32(closeBrace+1) != end {
		return nil, false
	}
	const prefix = "class __Q { "
	const suffix = " }\n"
	wrapped := make([]byte, 0, len(prefix)+int(end-start)+len(suffix))
	wrapped = append(wrapped, prefix...)
	wrapped = append(wrapped, source[start:end]...)
	wrapped = append(wrapped, suffix...)
	bodyStart := uint32(len(prefix)) + (openBrace - start)
	bodyEnd := uint32(len(prefix)) + (uint32(closeBrace) - start)
	for i := bodyStart + 1; i < bodyEnd; i++ {
		wrapped[i] = ' '
	}
	tree, err := p.parseForRecovery(wrapped)
	if err != nil || tree == nil || tree.RootNode() == nil {
		if tree != nil {
			tree.Release()
		}
		return nil, false
	}
	defer tree.Release()
	fn := csharpExtractRecoveredWrappedClassMethod(tree.RootNode(), p.language, arena)
	if fn == nil {
		return nil, false
	}
	if !shiftNodeBytes(fn, int64(start)-int64(len(prefix))) {
		return nil, false
	}
	statements, ok := csharpRecoverMethodBlockStatementsFromRange(source, openBrace+1, uint32(closeBrace), p, arena)
	if !ok {
		return nil, false
	}
	block, ok := csharpBuildRecoveredMethodBlockNode(source, p.language, arena, openBrace, uint32(closeBrace), statements)
	if !ok {
		return nil, false
	}
	if !csharpReplaceMethodBlock(fn, p.language, block) {
		return nil, false
	}
	if !csharpConvertMethodToLocalFunctionStatement(fn, p.language) {
		return nil, false
	}
	recomputeNodePointsFromBytes(fn, source)
	return csharpWrapRecoveredStatementAsGlobal(arena, p.language, fn)
}

func csharpExtractRecoveredWrappedMethodStatement(root *Node, lang *Language, arena *nodeArena) *Node {
	if root == nil || lang == nil {
		return nil
	}
	method := csharpFindFirstNamedDescendantOfType(root, lang, "method_declaration")
	if method == nil {
		return nil
	}
	block := csharpFindFirstNamedDescendantOfType(method, lang, "block")
	if block == nil {
		return nil
	}
	var candidate *Node
	for _, child := range block.children {
		if child == nil || !child.IsNamed() || !csharpIsRecoveredMethodBlockStatement(child, lang) {
			continue
		}
		if candidate != nil {
			return nil
		}
		if arena != nil {
			candidate = cloneTreeNodesIntoArena(child, arena)
		} else {
			candidate = child
		}
	}
	return candidate
}

func csharpRecoverSimpleTypePatternSwitchStatementFromRange(source []byte, start, end uint32, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil || p.language == nil || arena == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	start, end = csharpTrimSpaceBounds(source, start, end)
	if start >= end || !csharpHasKeywordAt(source, start, "switch") {
		return nil, false
	}
	openParen := csharpSkipSpaceBytes(source, start+uint32(len("switch")))
	if openParen >= end || source[openParen] != '(' {
		return nil, false
	}
	closeParen, ok := csharpFindMatchingParenByte(source, openParen, end)
	if !ok {
		return nil, false
	}
	exprStart, exprEnd := csharpTrimSpaceBounds(source, openParen+1, closeParen)
	expr, ok := csharpRecoverExpressionNodeFromRange(source, exprStart, exprEnd, p, arena)
	if !ok {
		return nil, false
	}
	openBrace := csharpSkipSpaceBytes(source, closeParen+1)
	if openBrace >= end || source[openBrace] != '{' {
		return nil, false
	}
	closeBrace := findMatchingBraceByte(source, int(openBrace), int(end))
	if closeBrace < 0 || uint32(closeBrace+1) != end {
		return nil, false
	}
	bodyStart, bodyEnd := csharpTrimSpaceBounds(source, openBrace+1, uint32(closeBrace))
	if bodyStart >= bodyEnd || !csharpHasKeywordAt(source, bodyStart, "case") {
		return nil, false
	}
	patternStart := csharpSkipSpaceBytes(source, bodyStart+uint32(len("case")))
	whenPos, ok := csharpFindKeywordAfter(source, patternStart, bodyEnd, "when")
	if !ok {
		return nil, false
	}
	patternEnd := csharpTrimRightSpaceBytes(source, whenPos)
	colonPos, ok := csharpFindTopLevelOperator(source, whenPos+uint32(len("when")), bodyEnd, ":")
	if !ok {
		return nil, false
	}
	conditionStart, conditionEnd := csharpTrimSpaceBounds(source, whenPos+uint32(len("when")), colonPos)
	if conditionStart >= conditionEnd {
		return nil, false
	}
	stmtStart := csharpSkipSpaceBytes(source, colonPos+1)
	if !csharpHasKeywordAt(source, stmtStart, "break") {
		return nil, false
	}
	semiPos, ok := csharpFindTopLevelOperator(source, stmtStart, bodyEnd, ";")
	if !ok {
		return nil, false
	}
	pattern, ok := csharpBuildSimpleTypePatternNode(source, patternStart, patternEnd, p.language, arena)
	if !ok {
		return nil, false
	}
	condition, ok := csharpBuildLessThanBinaryExpressionNode(source, conditionStart, conditionEnd, p, arena)
	if !ok {
		return nil, false
	}
	whenClause, ok := csharpBuildWhenClauseNode(source, whenPos, colonPos, condition, p.language, arena)
	if !ok {
		return nil, false
	}
	breakStmt, ok := csharpBuildBreakStatementNode(source, stmtStart, semiPos+1, p.language, arena)
	if !ok {
		return nil, false
	}
	section, ok := csharpBuildSwitchSectionNode(source, bodyStart, colonPos, p.language, arena, pattern, whenClause, breakStmt)
	if !ok {
		return nil, false
	}
	body, ok := csharpBuildSwitchBodyNode(source, openBrace, uint32(closeBrace), p.language, arena, []*Node{section})
	if !ok {
		return nil, false
	}
	return csharpBuildSwitchStatementNode(source, start, openParen, closeParen, openBrace, p.language, arena, expr, body)
}

func csharpExtractRecoveredWrappedClassMethod(root *Node, lang *Language, arena *nodeArena) *Node {
	if root == nil || lang == nil {
		return nil
	}
	classDecl := csharpFindFirstNamedDescendantOfType(root, lang, "class_declaration")
	if classDecl == nil {
		return nil
	}
	var candidate *Node
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil || candidate != nil {
			return
		}
		if n.IsNamed() && n.Type(lang) == "method_declaration" {
			if arena != nil {
				candidate = cloneTreeNodesIntoArena(n, arena)
			} else {
				candidate = n
			}
			return
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(classDecl)
	return candidate
}

func csharpRecoverMethodBlockStatementsFromRange(source []byte, start, end uint32, p *Parser, arena *nodeArena) ([]*Node, bool) {
	if p == nil || p.language == nil || arena == nil || start > end || int(end) > len(source) {
		return nil, false
	}
	if start == end {
		return nil, true
	}
	if bytesAreTrivia(source[start:end]) {
		return nil, true
	}
	relSpans := csharpTopLevelChunkSpans(source[start:end])
	if len(relSpans) == 0 {
		return nil, false
	}
	out := make([]*Node, 0, len(relSpans))
	for _, rel := range relSpans {
		spanStart := start + rel[0]
		spanEnd := start + rel[1]
		for _, part := range csharpSplitLeadingTopLevelCommentSpans(source, spanStart, spanEnd) {
			if comment, ok := csharpRecoverTopLevelCommentNodeFromRange(source, part[0], part[1], p.language, arena); ok {
				out = append(out, comment)
				continue
			}
			stmt, ok := csharpRecoverWrappedStatementNodeFromRange(source, part[0], part[1], p, arena)
			if !ok {
				return nil, false
			}
			out = append(out, stmt)
		}
	}
	return out, true
}

func csharpFindFirstNamedDescendantOfType(root *Node, lang *Language, want string) *Node {
	if root == nil || lang == nil {
		return nil
	}
	if root.IsNamed() && root.Type(lang) == want {
		return root
	}
	for _, child := range root.children {
		if got := csharpFindFirstNamedDescendantOfType(child, lang, want); got != nil {
			return got
		}
	}
	return nil
}

func csharpWrapRecoveredStatementAsGlobal(arena *nodeArena, lang *Language, stmt *Node) (*Node, bool) {
	if lang == nil || stmt == nil {
		return nil, false
	}
	sym, ok := symbolByName(lang, "global_statement")
	if !ok {
		return nil, false
	}
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	children := []*Node{stmt}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	global := newParentNodeInArena(arena, sym, named, children, nil, 0)
	global.hasError = false
	return global, true
}

func csharpBuildSimpleTypePatternNode(source []byte, start, end uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	var typeNode *Node
	var ok bool
	if typeNode, ok = csharpBuildLeafNodeByName(arena, source, lang, "predefined_type", start, end); !ok {
		typeNode, ok = csharpBuildIdentifierNodeFromSource(source, start, end, lang, arena)
		if !ok {
			return nil, false
		}
	}
	sym, ok := symbolByName(lang, "type_pattern")
	if !ok {
		return nil, false
	}
	typeID, _ := lang.FieldByName("type")
	fields := csharpFieldIDsInArena(arena, []FieldID{typeID})
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, []*Node{typeNode}, fields, 0), true
}

func csharpBuildLessThanBinaryExpressionNode(source []byte, start, end uint32, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil || p.language == nil || arena == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	opPos, ok := csharpFindTopLevelOperator(source, start, end, "<")
	if !ok {
		return nil, false
	}
	return csharpBuildBinaryExpressionNode(arena, source, p.language, start, opPos, opPos+1, end)
}

func csharpBuildWhenClauseNode(source []byte, whenPos, colonPos uint32, condition *Node, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || condition == nil || whenPos >= colonPos || int(colonPos) > len(source) {
		return nil, false
	}
	sym, ok := symbolByName(lang, "when_clause")
	if !ok {
		return nil, false
	}
	whenTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "when", whenPos, whenPos+uint32(len("when")))
	if !ok {
		return nil, false
	}
	valueID, _ := lang.FieldByName("value")
	fields := csharpFieldIDsInArena(arena, []FieldID{0, valueID})
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, []*Node{whenTok, condition}, fields, 0), true
}

func csharpBuildBreakStatementNode(source []byte, start, end uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	sym, ok := symbolByName(lang, "break_statement")
	if !ok {
		return nil, false
	}
	breakTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "break", start, start+uint32(len("break")))
	if !ok {
		return nil, false
	}
	semiTok, ok := csharpBuildLeafNodeByName(arena, source, lang, ";", end-1, end)
	if !ok {
		return nil, false
	}
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, []*Node{breakTok, semiTok}, nil, 0), true
}

func csharpBuildSwitchSectionNode(source []byte, casePos, colonPos uint32, lang *Language, arena *nodeArena, pattern, whenClause, stmt *Node) (*Node, bool) {
	if lang == nil || pattern == nil || whenClause == nil || stmt == nil || casePos >= colonPos || int(colonPos) > len(source) {
		return nil, false
	}
	sym, ok := symbolByName(lang, "switch_section")
	if !ok {
		return nil, false
	}
	caseTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "case", casePos, casePos+uint32(len("case")))
	if !ok {
		return nil, false
	}
	colonTok, ok := csharpBuildLeafNodeByName(arena, source, lang, ":", colonPos, colonPos+1)
	if !ok {
		return nil, false
	}
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, []*Node{caseTok, pattern, whenClause, colonTok, stmt}, nil, 0), true
}

func csharpBuildSwitchBodyNode(source []byte, openBrace, closeBrace uint32, lang *Language, arena *nodeArena, sections []*Node) (*Node, bool) {
	if lang == nil || openBrace >= closeBrace || int(closeBrace+1) > len(source) {
		return nil, false
	}
	sym, ok := symbolByName(lang, "switch_body")
	if !ok {
		return nil, false
	}
	openTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "{", openBrace, openBrace+1)
	if !ok {
		return nil, false
	}
	closeTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "}", closeBrace, closeBrace+1)
	if !ok {
		return nil, false
	}
	children := make([]*Node, 0, len(sections)+2)
	children = append(children, openTok)
	children = append(children, sections...)
	children = append(children, closeTok)
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, children, nil, 0), true
}

func csharpBuildSwitchStatementNode(source []byte, switchPos, openParen, closeParen, openBrace uint32, lang *Language, arena *nodeArena, expr, body *Node) (*Node, bool) {
	if lang == nil || expr == nil || body == nil || switchPos >= openParen || openParen >= closeParen || int(closeParen+1) > len(source) {
		return nil, false
	}
	sym, ok := symbolByName(lang, "switch_statement")
	if !ok {
		return nil, false
	}
	switchTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "switch", switchPos, switchPos+uint32(len("switch")))
	if !ok {
		return nil, false
	}
	openTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "(", openParen, openParen+1)
	if !ok {
		return nil, false
	}
	closeTok, ok := csharpBuildLeafNodeByName(arena, source, lang, ")", closeParen, closeParen+1)
	if !ok {
		return nil, false
	}
	expressionID, _ := lang.FieldByName("expression")
	fields := csharpFieldIDsInArena(arena, []FieldID{0, 0, expressionID, 0, 0})
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, []*Node{switchTok, openTok, expr, closeTok, body}, fields, 0), true
}

func csharpReplaceMethodBlock(method *Node, lang *Language, block *Node) bool {
	if method == nil || lang == nil || block == nil || method.Type(lang) != "method_declaration" {
		return false
	}
	for i := len(method.children) - 1; i >= 0; i-- {
		if method.children[i] == nil || method.children[i].Type(lang) != "block" {
			continue
		}
		method.children[i] = block
		block.parent = method
		block.childIndex = i
		method.hasError = false
		populateParentNode(method, method.children)
		return true
	}
	return false
}

func csharpReplaceRecoveredVariableInitializer(root *Node, lang *Language, expr *Node) bool {
	if root == nil || lang == nil || expr == nil {
		return false
	}
	var replace func(*Node) bool
	replace = func(n *Node) bool {
		if n == nil {
			return false
		}
		if n.Type(lang) == "variable_declarator" && len(n.children) >= 3 {
			idx := len(n.children) - 1
			n.children[idx] = expr
			expr.parent = n
			expr.childIndex = idx
			n.hasError = false
			populateParentNode(n, n.children)
			csharpExtendNodeEndIfNeeded(n, expr.endByte)
			if n.parent != nil {
				csharpExtendNodeEndIfNeeded(n.parent, expr.endByte)
			}
			return true
		}
		for _, child := range n.children {
			if replace(child) {
				return true
			}
		}
		return false
	}
	return replace(root)
}

func csharpExtendNodeEndIfNeeded(n *Node, end uint32) {
	for cur := n; cur != nil; cur = cur.parent {
		if cur.endByte >= end {
			continue
		}
		cur.endByte = end
	}
}

func csharpConvertMethodToLocalFunctionStatement(n *Node, lang *Language) bool {
	if n == nil || lang == nil || n.Type(lang) != "method_declaration" {
		return false
	}
	sym, ok := symbolByName(lang, "local_function_statement")
	if !ok {
		return false
	}
	n.symbol = sym
	n.isNamed = int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	n.productionID = 0
	n.hasError = false
	populateParentNode(n, n.children)
	return true
}
