package gotreesitter

func normalizeCSharpInvocationStatements(root *Node, source []byte, lang *Language) {
	if root == nil || lang == nil || lang.Name != "c_sharp" || len(source) == 0 {
		return
	}
	exprStmtSym, ok := lang.SymbolByName("expression_statement")
	if !ok {
		return
	}
	invocationSym, ok := lang.SymbolByName("invocation_expression")
	if !ok {
		return
	}
	memberAccessSym, ok := lang.SymbolByName("member_access_expression")
	if !ok {
		return
	}
	argumentListSym, ok := lang.SymbolByName("argument_list")
	if !ok {
		return
	}
	argumentSym, ok := lang.SymbolByName("argument")
	if !ok {
		return
	}
	functionFieldID, hasFunctionField := lang.FieldByName("function")
	argumentsFieldID, hasArgumentsField := lang.FieldByName("arguments")
	expressionFieldID, hasExpressionField := lang.FieldByName("expression")
	nameFieldID, hasNameField := lang.FieldByName("name")
	if !hasFunctionField || !hasArgumentsField || !hasExpressionField || !hasNameField {
		return
	}
	exprStmtNamed := int(exprStmtSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[exprStmtSym].Named
	invocationNamed := int(invocationSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[invocationSym].Named
	memberAccessNamed := int(memberAccessSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[memberAccessSym].Named
	argumentListNamed := int(argumentListSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[argumentListSym].Named
	argumentNamed := int(argumentSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[argumentSym].Named

	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "local_declaration_statement" && len(n.children) == 2 {
			decl := n.children[0]
			semi := n.children[1]
			if decl != nil && semi != nil && semi.Type(lang) == ";" &&
				decl.Type(lang) == "variable_declaration" && len(decl.children) == 2 {
				target := decl.children[0]
				varDecl := decl.children[1]
				if target != nil && varDecl != nil &&
					target.Type(lang) == "qualified_name" &&
					varDecl.Type(lang) == "variable_declarator" &&
					len(varDecl.children) == 1 &&
					varDecl.children[0] != nil &&
					varDecl.children[0].Type(lang) == "tuple_pattern" {
					function := csharpRewriteQualifiedNameAsMemberAccess(target, lang, memberAccessSym, memberAccessNamed, expressionFieldID, nameFieldID)
					if arguments, ok := csharpBuildArgumentListFromTuplePattern(varDecl.children[0], lang, argumentListSym, argumentListNamed, argumentSym, argumentNamed); ok {
						invocationFields := []FieldID{functionFieldID, argumentsFieldID}
						if n.ownerArena != nil {
							buf := n.ownerArena.allocFieldIDSlice(len(invocationFields))
							copy(buf, invocationFields)
							invocationFields = buf
						}
						invocation := newParentNodeInArena(n.ownerArena, invocationSym, invocationNamed, []*Node{function, arguments}, invocationFields, 0)
						invocation.fieldSources = defaultFieldSourcesInArena(n.ownerArena, invocationFields)
						n.symbol = exprStmtSym
						n.isNamed = exprStmtNamed
						n.children = []*Node{invocation, semi}
						if n.ownerArena != nil {
							buf := n.ownerArena.allocNodeSlice(len(n.children))
							copy(buf, n.children)
							n.children = buf
						}
						n.fieldIDs = nil
						n.fieldSources = nil
						n.productionID = 0
						n.hasError = false
						populateParentNode(n, n.children)
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

func csharpRecoverTopLevelInvocationStatementFromRange(source []byte, start, end uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || arena == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	start, end = csharpTrimSpaceBounds(source, start, end)
	if start >= end || source[end-1] != ';' {
		return nil, false
	}
	exprEnd := csharpTrimRightSpaceBytes(source, end-1)
	if exprEnd <= start {
		return nil, false
	}
	invocation, ok := csharpBuildInvocationExpressionNode(arena, source, lang, start, exprEnd)
	if !ok || invocation == nil {
		return nil, false
	}
	if invocation.Type(lang) != "invocation_expression" || len(invocation.children) == 0 || invocation.children[0] == nil {
		return nil, false
	}
	semiTok, ok := csharpBuildLeafNodeByName(arena, source, lang, ";", end-1, end)
	if !ok {
		return nil, false
	}
	exprStmtSym, ok := symbolByName(lang, "expression_statement")
	if !ok {
		return nil, false
	}
	globalSym, ok := symbolByName(lang, "global_statement")
	if !ok {
		return nil, false
	}
	exprStmtNamed := int(exprStmtSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[exprStmtSym].Named
	globalNamed := int(globalSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[globalSym].Named
	expressionFieldID, _ := lang.FieldByName("expression")
	exprChildren := []*Node{invocation, semiTok}
	if arena != nil {
		buf := arena.allocNodeSlice(len(exprChildren))
		copy(buf, exprChildren)
		exprChildren = buf
	}
	exprFieldIDs := csharpFieldIDsInArena(arena, []FieldID{expressionFieldID, 0})
	exprStmt := newParentNodeInArena(arena, exprStmtSym, exprStmtNamed, exprChildren, exprFieldIDs, 0)
	exprStmt.hasError = false
	globalChildren := []*Node{exprStmt}
	if arena != nil {
		buf := arena.allocNodeSlice(len(globalChildren))
		copy(buf, globalChildren)
		globalChildren = buf
	}
	global := newParentNodeInArena(arena, globalSym, globalNamed, globalChildren, nil, 0)
	global.hasError = false
	return global, true
}

func csharpRewriteQualifiedNameAsMemberAccess(node *Node, lang *Language, memberAccessSym Symbol, memberAccessNamed bool, expressionFieldID, nameFieldID FieldID) *Node {
	if node == nil || lang == nil || node.Type(lang) != "qualified_name" {
		return node
	}
	childCount := len(node.children)
	fieldIDs := make([]FieldID, childCount)
	if node.ownerArena != nil && childCount > 0 {
		buf := node.ownerArena.allocFieldIDSlice(childCount)
		copy(buf, fieldIDs)
		fieldIDs = buf
	}
	if childCount > 0 {
		fieldIDs[0] = expressionFieldID
	}
	if childCount > 2 {
		fieldIDs[2] = nameFieldID
	}
	node.symbol = memberAccessSym
	node.isNamed = memberAccessNamed
	node.fieldIDs = fieldIDs
	node.fieldSources = defaultFieldSourcesInArena(node.ownerArena, fieldIDs)
	node.productionID = 0
	node.hasError = false
	populateParentNode(node, node.children)
	return node
}

func csharpBuildArgumentListFromTuplePattern(tuple *Node, lang *Language, argumentListSym Symbol, argumentListNamed bool, argumentSym Symbol, argumentNamed bool) (*Node, bool) {
	if tuple == nil || lang == nil || tuple.Type(lang) != "tuple_pattern" || len(tuple.children) < 3 {
		return nil, false
	}
	children := make([]*Node, 0, len(tuple.children))
	children = append(children, tuple.children[0])
	for i := 1; i < len(tuple.children)-1; i++ {
		child := tuple.children[i]
		if child == nil {
			continue
		}
		if child.IsNamed() {
			argChildren := []*Node{child}
			if tuple.ownerArena != nil {
				buf := tuple.ownerArena.allocNodeSlice(len(argChildren))
				copy(buf, argChildren)
				argChildren = buf
			}
			arg := newParentNodeInArena(tuple.ownerArena, argumentSym, argumentNamed, argChildren, nil, 0)
			arg.hasError = false
			children = append(children, arg)
			continue
		}
		children = append(children, child)
	}
	children = append(children, tuple.children[len(tuple.children)-1])
	if tuple.ownerArena != nil {
		buf := tuple.ownerArena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	args := newParentNodeInArena(tuple.ownerArena, argumentListSym, argumentListNamed, children, nil, 0)
	args.hasError = false
	return args, true
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
	positionalSym, ok := lang.SymbolByName("positional_pattern_clause")
	if !ok {
		return
	}
	subpatternSym, ok := lang.SymbolByName("subpattern")
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
	positionalNamed := false
	if idx := int(positionalSym); idx < len(lang.SymbolMetadata) {
		positionalNamed = lang.SymbolMetadata[positionalSym].Named
	}
	subpatternNamed := false
	if idx := int(subpatternSym); idx < len(lang.SymbolMetadata) {
		subpatternNamed = lang.SymbolMetadata[subpatternSym].Named
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type(lang) == "switch_section" && len(n.children) > 1 {
			pat := n.children[1]
			if n.children[0] != nil && n.children[0].Type(lang) == "case" &&
				pat != nil && csharpShouldWrapSwitchCaseConstantPattern(pat, lang) {
				repl := newParentNodeInArena(n.ownerArena, patternSym, named, []*Node{pat}, nil, 0)
				repl.parent = n
				repl.childIndex = 1
				n.children[1] = repl
				pat = repl
			}
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
				pat = repl
			}
			if pat != nil && pat.Type(lang) == "constant_pattern" && len(pat.children) == 1 && pat.children[0] != nil {
				csharpRewriteSwitchTupleLiteralPatternArguments(pat.children[0], lang, positionalSym, positionalNamed, subpatternSym, subpatternNamed, patternSym, named)
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(root)
}

func csharpShouldWrapSwitchCaseConstantPattern(n *Node, lang *Language) bool {
	if n == nil || lang == nil {
		return false
	}
	switch n.Type(lang) {
	case "integer_literal", "real_literal", "string_literal", "character_literal", "null_literal", "boolean_literal", "identifier", "member_access_expression":
		return true
	default:
		return false
	}
}

func csharpRewriteSwitchTupleLiteralPatternArguments(tuple *Node, lang *Language, positionalSym Symbol, positionalNamed bool, subpatternSym Symbol, subpatternNamed bool, constantPatternSym Symbol, constantPatternNamed bool) bool {
	if tuple == nil || lang == nil || tuple.Type(lang) != "tuple_expression" || len(tuple.children) < 3 {
		return false
	}
	for _, child := range tuple.children {
		if child != nil && child.IsNamed() && child.Type(lang) == "positional_pattern_clause" {
			return false
		}
	}
	clauseChildren := make([]*Node, 0, len(tuple.children)-2)
	for _, child := range tuple.children[1 : len(tuple.children)-1] {
		if child == nil {
			continue
		}
		if !child.IsNamed() {
			clauseChildren = append(clauseChildren, child)
			continue
		}
		if child.Type(lang) != "argument" || len(child.children) != 1 || child.children[0] == nil {
			return false
		}
		patternChild := child.children[0]
		if patternChild.Type(lang) != "discard" && patternChild.Type(lang) != "constant_pattern" {
			patternChildren := []*Node{patternChild}
			if tuple.ownerArena != nil {
				buf := tuple.ownerArena.allocNodeSlice(len(patternChildren))
				copy(buf, patternChildren)
				patternChildren = buf
			}
			patternChild = newParentNodeInArena(tuple.ownerArena, constantPatternSym, constantPatternNamed, patternChildren, nil, 0)
			patternChild.hasError = false
		}
		subChildren := []*Node{patternChild}
		if tuple.ownerArena != nil {
			buf := tuple.ownerArena.allocNodeSlice(len(subChildren))
			copy(buf, subChildren)
			subChildren = buf
		}
		sub := newParentNodeInArena(tuple.ownerArena, subpatternSym, subpatternNamed, subChildren, nil, 0)
		sub.hasError = false
		clauseChildren = append(clauseChildren, sub)
	}
	if len(clauseChildren) == 0 {
		return false
	}
	if tuple.ownerArena != nil {
		buf := tuple.ownerArena.allocNodeSlice(len(clauseChildren))
		copy(buf, clauseChildren)
		clauseChildren = buf
	}
	clause := newParentNodeInArena(tuple.ownerArena, positionalSym, positionalNamed, clauseChildren, nil, 0)
	clause.hasError = false
	children := []*Node{tuple.children[0], clause, tuple.children[len(tuple.children)-1]}
	if tuple.ownerArena != nil {
		buf := tuple.ownerArena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	tuple.children = children
	tuple.fieldIDs = nil
	tuple.fieldSources = nil
	tuple.productionID = 0
	tuple.hasError = false
	populateParentNode(tuple, tuple.children)
	return true
}
