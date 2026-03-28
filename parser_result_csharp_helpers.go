package gotreesitter

import "bytes"

func csharpFindTopLevelOperator(source []byte, start, end uint32, op string) (uint32, bool) {
	if start >= end || op == "" {
		return 0, false
	}
	parens := 0
	braces := 0
	brackets := 0
	inString := false
	escape := false
	opLen := uint32(len(op))
	for i := start; i+opLen <= end; i++ {
		b := source[i]
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
		switch b {
		case '"':
			inString = true
			continue
		case '(':
			parens++
			continue
		case ')':
			if parens > 0 {
				parens--
			}
			continue
		case '{':
			braces++
			continue
		case '}':
			if braces > 0 {
				braces--
			}
			continue
		case '[':
			brackets++
			continue
		case ']':
			if brackets > 0 {
				brackets--
			}
			continue
		}
		if parens == 0 && braces == 0 && brackets == 0 && string(source[i:i+opLen]) == op {
			return i, true
		}
	}
	return 0, false
}

func csharpFindLastTopLevelOperator(source []byte, start, end uint32, op string) (uint32, bool) {
	if start >= end || op == "" {
		return 0, false
	}
	parens := 0
	braces := 0
	brackets := 0
	inString := false
	escape := false
	opLen := uint32(len(op))
	last := uint32(0)
	found := false
	for i := start; i+opLen <= end; i++ {
		b := source[i]
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
		switch b {
		case '"':
			inString = true
			continue
		case '(':
			parens++
			continue
		case ')':
			if parens > 0 {
				parens--
			}
			continue
		case '{':
			braces++
			continue
		case '}':
			if braces > 0 {
				braces--
			}
			continue
		case '[':
			brackets++
			continue
		case ']':
			if brackets > 0 {
				brackets--
			}
			continue
		}
		if parens == 0 && braces == 0 && brackets == 0 && string(source[i:i+opLen]) == op {
			last = i
			found = true
		}
	}
	return last, found
}

func csharpFindConditionalColon(source []byte, start, end uint32) (uint32, bool) {
	parens := 0
	braces := 0
	brackets := 0
	inString := false
	escape := false
	nestedTernary := 0
	for i := start; i < end; i++ {
		b := source[i]
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
		switch b {
		case '"':
			inString = true
		case '(':
			parens++
		case ')':
			if parens > 0 {
				parens--
			}
		case '{':
			braces++
		case '}':
			if braces > 0 {
				braces--
			}
		case '[':
			brackets++
		case ']':
			if brackets > 0 {
				brackets--
			}
		case '?':
			if parens == 0 && braces == 0 && brackets == 0 {
				nestedTernary++
			}
		case ':':
			if parens == 0 && braces == 0 && brackets == 0 {
				if nestedTernary == 0 {
					return i, true
				}
				nestedTernary--
			}
		}
	}
	return 0, false
}

func csharpFindTopLevelAssignment(source []byte, start, end uint32) (uint32, bool) {
	pos, ok := csharpFindTopLevelOperator(source, start, end, "=")
	if !ok {
		return 0, false
	}
	if pos > start && source[pos-1] == '=' {
		return 0, false
	}
	if pos+1 < end && (source[pos+1] == '=' || source[pos+1] == '>') {
		return 0, false
	}
	return pos, true
}

func csharpFindInvocationOpenParen(source []byte, start, end uint32) (uint32, bool) {
	if end <= start || source[end-1] != ')' {
		return 0, false
	}
	depth := 0
	inString := false
	escape := false
	for i := end; i > start; i-- {
		b := source[i-1]
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
		switch b {
		case '"':
			inString = true
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return i - 1, true
			}
		}
	}
	return 0, false
}

func csharpSplitTopLevelByComma(source []byte, start, end uint32) [][2]uint32 {
	var spans [][2]uint32
	itemStart := start
	parens := 0
	braces := 0
	brackets := 0
	inString := false
	escape := false
	for i := start; i < end; i++ {
		b := source[i]
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
		switch b {
		case '"':
			inString = true
		case '(':
			parens++
		case ')':
			if parens > 0 {
				parens--
			}
		case '{':
			braces++
		case '}':
			if braces > 0 {
				braces--
			}
		case '[':
			brackets++
		case ']':
			if brackets > 0 {
				brackets--
			}
		case ',':
			if parens == 0 && braces == 0 && brackets == 0 {
				spans = append(spans, [2]uint32{itemStart, i})
				itemStart = i + 1
			}
		}
	}
	if itemStart <= end {
		spans = append(spans, [2]uint32{itemStart, end})
	}
	return spans
}

func csharpFindCommaBetween(source []byte, start, end uint32) uint32 {
	for i := start; i < end && i < uint32(len(source)); i++ {
		if source[i] == ',' {
			return i
		}
	}
	return 0
}

func csharpIsIntegerLiteral(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, ch := range b {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func csharpExtractRecoveredVariableInitializer(root *Node, lang *Language, arena *nodeArena) *Node {
	if root == nil || lang == nil {
		return nil
	}
	var walk func(*Node) *Node
	walk = func(n *Node) *Node {
		if n == nil {
			return nil
		}
		if n.Type(lang) == "variable_declarator" && len(n.children) >= 3 {
			value := n.children[len(n.children)-1]
			if value != nil {
				if arena != nil {
					return cloneTreeNodesIntoArena(value, arena)
				}
				return value
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			if got := walk(n.Child(i)); got != nil {
				return got
			}
		}
		return nil
	}
	return walk(root)
}

func csharpRecoverQuerySkeletonRoot(source []byte, p *Parser, arena *nodeArena, spec csharpSimpleJoinQuerySpec) (*Node, bool) {
	if p == nil || p.language == nil || arena == nil {
		return nil, false
	}
	skeleton := append([]byte(nil), source...)
	for i := spec.queryStart; i < spec.queryEnd; i++ {
		skeleton[i] = ' '
	}
	if spec.queryStart < uint32(len(skeleton)) {
		skeleton[spec.queryStart] = '0'
	}
	tree, err := p.parseForRecovery(skeleton)
	if err != nil || tree == nil || tree.RootNode() == nil {
		if tree != nil {
			tree.Release()
		}
		return nil, false
	}
	defer tree.Release()
	rt := tree.ParseRuntime()
	recoveredRoot := tree.RootNode()
	if rt.StopReason != ParseStopAccepted || rt.Truncated || rt.TokenSourceEOFEarly || recoveredRoot.HasError() {
		return nil, false
	}
	cloned := cloneTreeNodesIntoArena(recoveredRoot, arena)
	if cloned == nil {
		return nil, false
	}
	queryExpr, ok := csharpBuildSimpleJoinQueryExpression(arena, source, p.language, spec)
	if !ok {
		return nil, false
	}
	if !csharpReplaceRecoveredQueryExpression(cloned, p.language, spec.queryStart, spec.queryEnd, queryExpr) {
		return nil, false
	}
	return cloned, true
}

func csharpFindSimpleJoinQuerySpec(source []byte) (csharpSimpleJoinQuerySpec, bool) {
	var spec csharpSimpleJoinQuerySpec
	if len(source) == 0 {
		return spec, false
	}
	eq := bytes.IndexByte(source, '=')
	if eq < 0 {
		return spec, false
	}
	spec.queryStart = csharpSkipSpaceBytes(source, uint32(eq+1))
	spec.fromStart = spec.queryStart
	if !csharpHasKeywordAt(source, spec.fromStart, "from") {
		return spec, false
	}
	spec.fromEnd = spec.fromStart + 4
	var ok bool
	if spec.rangeName[0], spec.rangeName[1], ok = csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, spec.fromEnd)); !ok {
		return spec, false
	}
	spec.in1Start = csharpSkipSpaceBytes(source, spec.rangeName[1])
	if !csharpHasKeywordAt(source, spec.in1Start, "in") {
		return spec, false
	}
	spec.in1End = spec.in1Start + 2
	if spec.source1[0], spec.source1[1], ok = csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, spec.in1End)); !ok {
		return spec, false
	}
	spec.joinStart = csharpSkipSpaceBytes(source, spec.source1[1])
	if !csharpHasKeywordAt(source, spec.joinStart, "join") {
		return spec, false
	}
	spec.joinEnd = spec.joinStart + 4
	if spec.joinName[0], spec.joinName[1], ok = csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, spec.joinEnd)); !ok {
		return spec, false
	}
	spec.in2Start = csharpSkipSpaceBytes(source, spec.joinName[1])
	if !csharpHasKeywordAt(source, spec.in2Start, "in") {
		return spec, false
	}
	spec.in2End = spec.in2Start + 2
	if spec.source2[0], spec.source2[1], ok = csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, spec.in2End)); !ok {
		return spec, false
	}
	spec.onStart = csharpSkipSpaceBytes(source, spec.source2[1])
	if !csharpHasKeywordAt(source, spec.onStart, "on") {
		return spec, false
	}
	spec.onEnd = spec.onStart + 2
	if spec.leftObj[0], spec.leftObj[1], ok = csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, spec.onEnd)); !ok {
		return spec, false
	}
	spec.leftDotPos = spec.leftObj[1]
	if spec.leftDotPos >= uint32(len(source)) || source[spec.leftDotPos] != '.' {
		return spec, false
	}
	if spec.leftProp[0], spec.leftProp[1], ok = csharpScanIdentifierAt(source, spec.leftDotPos+1); !ok {
		return spec, false
	}
	spec.equalsStart = csharpSkipSpaceBytes(source, spec.leftProp[1])
	if !csharpHasKeywordAt(source, spec.equalsStart, "equals") {
		return spec, false
	}
	spec.equalsEnd = spec.equalsStart + 6
	if spec.rightObj[0], spec.rightObj[1], ok = csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, spec.equalsEnd)); !ok {
		return spec, false
	}
	spec.rightDotPos = spec.rightObj[1]
	if spec.rightDotPos >= uint32(len(source)) || source[spec.rightDotPos] != '.' {
		return spec, false
	}
	if spec.rightProp[0], spec.rightProp[1], ok = csharpScanIdentifierAt(source, spec.rightDotPos+1); !ok {
		return spec, false
	}
	spec.selectStart = csharpSkipSpaceBytes(source, spec.rightProp[1])
	if !csharpHasKeywordAt(source, spec.selectStart, "select") {
		return spec, false
	}
	spec.selectEnd = spec.selectStart + 6
	if spec.selectName[0], spec.selectName[1], ok = csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, spec.selectEnd)); !ok {
		return spec, false
	}
	spec.queryEnd = spec.selectName[1]
	spec.semiPos = csharpSkipSpaceBytes(source, spec.queryEnd)
	if spec.semiPos >= uint32(len(source)) || source[spec.semiPos] != ';' {
		return spec, false
	}
	return spec, true
}

func csharpReplaceRecoveredQueryExpression(root *Node, lang *Language, queryStart, queryEnd uint32, queryExpr *Node) bool {
	if root == nil || lang == nil || queryExpr == nil {
		return false
	}
	var walk func(*Node) bool
	walk = func(n *Node) bool {
		if n == nil {
			return false
		}
		if n.Type(lang) == "variable_declarator" && len(n.children) >= 3 {
			expr := n.children[len(n.children)-1]
			if expr != nil && expr.startByte <= queryStart && expr.endByte > queryStart && n.startByte <= queryStart {
				n.children[len(n.children)-1] = queryExpr
				queryExpr.parent = n
				queryExpr.childIndex = len(n.children) - 1
				for cur := n; cur != nil; cur = cur.parent {
					populateParentNode(cur, cur.children)
				}
				return true
			}
		}
		for _, child := range n.children {
			if walk(child) {
				n.hasError = false
				return true
			}
		}
		return false
	}
	return walk(root)
}

func csharpBuildSimpleJoinQueryExpression(arena *nodeArena, source []byte, lang *Language, spec csharpSimpleJoinQuerySpec) (*Node, bool) {
	if arena == nil || lang == nil {
		return nil, false
	}
	queryExprSym, ok := symbolByName(lang, "query_expression")
	if !ok {
		return nil, false
	}
	fromClauseSym, ok := symbolByName(lang, "from_clause")
	if !ok {
		return nil, false
	}
	joinClauseSym, ok := symbolByName(lang, "join_clause")
	if !ok {
		return nil, false
	}
	selectClauseSym, ok := symbolByName(lang, "select_clause")
	if !ok {
		return nil, false
	}
	memberAccessSym, ok := symbolByName(lang, "member_access_expression")
	if !ok {
		return nil, false
	}
	identifierSym, ok := symbolByName(lang, "identifier")
	if !ok {
		return nil, false
	}
	fromSym, ok := symbolByName(lang, "from")
	if !ok {
		return nil, false
	}
	inSym, ok := symbolByName(lang, "in")
	if !ok {
		return nil, false
	}
	joinSym, ok := symbolByName(lang, "join")
	if !ok {
		return nil, false
	}
	onSym, ok := symbolByName(lang, "on")
	if !ok {
		return nil, false
	}
	equalsSym, ok := symbolByName(lang, "equals")
	if !ok {
		return nil, false
	}
	selectSym, ok := symbolByName(lang, "select")
	if !ok {
		return nil, false
	}
	dotSym, ok := symbolByName(lang, ".")
	if !ok {
		return nil, false
	}
	nameFieldID, hasNameField := lang.FieldByName("name")
	expressionFieldID, hasExpressionField := lang.FieldByName("expression")
	if !hasNameField || !hasExpressionField {
		return nil, false
	}
	identifierNamed := int(identifierSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[identifierSym].Named
	memberAccessNamed := int(memberAccessSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[memberAccessSym].Named
	fromClauseNamed := int(fromClauseSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[fromClauseSym].Named
	joinClauseNamed := int(joinClauseSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[joinClauseSym].Named
	selectClauseNamed := int(selectClauseSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[selectClauseSym].Named
	queryExprNamed := int(queryExprSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[queryExprSym].Named

	ident := func(span [2]uint32) *Node {
		return newLeafNodeInArena(
			arena,
			identifierSym,
			identifierNamed,
			span[0],
			span[1],
			advancePointByBytes(Point{}, source[:span[0]]),
			advancePointByBytes(Point{}, source[:span[1]]),
		)
	}
	leaf := func(sym Symbol, start, end uint32) *Node {
		named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
		return newLeafNodeInArena(
			arena,
			sym,
			named,
			start,
			end,
			advancePointByBytes(Point{}, source[:start]),
			advancePointByBytes(Point{}, source[:end]),
		)
	}
	memberAccess := func(obj, prop [2]uint32, dotPos uint32) *Node {
		children := []*Node{
			ident(obj),
			leaf(dotSym, dotPos, dotPos+1),
			ident(prop),
		}
		fieldIDs := csharpFieldIDsInArena(arena, []FieldID{expressionFieldID, 0, nameFieldID})
		return newParentNodeInArena(arena, memberAccessSym, memberAccessNamed, children, fieldIDs, 0)
	}

	fromChildren := []*Node{
		leaf(fromSym, spec.fromStart, spec.fromEnd),
		ident(spec.rangeName),
		leaf(inSym, spec.in1Start, spec.in1End),
		ident(spec.source1),
	}
	fromFields := csharpFieldIDsInArena(arena, []FieldID{0, nameFieldID, 0, 0})
	fromClause := newParentNodeInArena(arena, fromClauseSym, fromClauseNamed, fromChildren, fromFields, 0)

	joinClause := newParentNodeInArena(arena, joinClauseSym, joinClauseNamed, []*Node{
		leaf(joinSym, spec.joinStart, spec.joinEnd),
		ident(spec.joinName),
		leaf(inSym, spec.in2Start, spec.in2End),
		ident(spec.source2),
		leaf(onSym, spec.onStart, spec.onEnd),
		memberAccess(spec.leftObj, spec.leftProp, spec.leftDotPos),
		leaf(equalsSym, spec.equalsStart, spec.equalsEnd),
		memberAccess(spec.rightObj, spec.rightProp, spec.rightDotPos),
	}, nil, 0)

	selectClause := newParentNodeInArena(arena, selectClauseSym, selectClauseNamed, []*Node{
		leaf(selectSym, spec.selectStart, spec.selectEnd),
		ident(spec.selectName),
	}, nil, 0)

	queryExpr := newParentNodeInArena(arena, queryExprSym, queryExprNamed, []*Node{
		fromClause,
		joinClause,
		selectClause,
	}, nil, 0)
	return queryExpr, true
}

func csharpFieldIDsInArena(arena *nodeArena, ids []FieldID) []FieldID {
	if len(ids) == 0 {
		return nil
	}
	if arena == nil {
		out := make([]FieldID, len(ids))
		copy(out, ids)
		return out
	}
	out := arena.allocFieldIDSlice(len(ids))
	copy(out, ids)
	return out
}

func csharpHasKeywordAt(source []byte, start uint32, kw string) bool {
	if int(start)+len(kw) > len(source) {
		return false
	}
	return string(source[start:uint32(int(start)+len(kw))]) == kw
}

func csharpSkipSpaceBytes(source []byte, start uint32) uint32 {
	i := start
	for i < uint32(len(source)) {
		switch source[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

func csharpTrimRightSpaceBytes(source []byte, end uint32) uint32 {
	for end > 0 {
		switch source[end-1] {
		case ' ', '\t', '\n', '\r':
			end--
		default:
			return end
		}
	}
	return end
}

func csharpTrimSpaceBounds(source []byte, start, end uint32) (uint32, uint32) {
	start = csharpSkipSpaceBytes(source, start)
	end = csharpTrimRightSpaceBytes(source, end)
	if start > end {
		return end, end
	}
	return start, end
}

func csharpScanIdentifierAt(source []byte, start uint32) (uint32, uint32, bool) {
	if start >= uint32(len(source)) {
		return 0, 0, false
	}
	b := source[start]
	if !csharpIdentifierStartByte(b) {
		return 0, 0, false
	}
	end := start + 1
	for end < uint32(len(source)) && csharpIdentifierContinueByte(source[end]) {
		end++
	}
	return start, end, true
}

func csharpBuildIdentifierNodeFromSource(source []byte, start, end uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	ident, ok := csharpBuildLeafNodeByName(arena, source, lang, "identifier", start, end)
	if !ok || lang == nil || int(end) > len(source) || start >= end {
		return ident, ok
	}
	keyword := string(source[start:end])
	keywordSym, ok := symbolByName(lang, keyword)
	if !ok || int(keywordSym) >= len(lang.SymbolMetadata) || lang.SymbolMetadata[keywordSym].Named {
		return ident, true
	}
	keywordLeaf, ok := csharpBuildLeafNodeByName(arena, source, lang, keyword, start, end)
	if !ok {
		return ident, true
	}
	identSym, ok := symbolByName(lang, "identifier")
	if !ok {
		return ident, true
	}
	identNamed := int(identSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[identSym].Named
	children := []*Node{keywordLeaf}
	if arena != nil {
		buf := arena.allocNodeSlice(len(children))
		copy(buf, children)
		children = buf
	}
	node := newParentNodeInArena(arena, identSym, identNamed, children, nil, 0)
	node.hasError = false
	return node, true
}

func csharpIdentifierStartByte(b byte) bool {
	return b == '_' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

func csharpIdentifierContinueByte(b byte) bool {
	return csharpIdentifierStartByte(b) || b >= '0' && b <= '9'
}
