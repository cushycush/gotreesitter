package gotreesitter

import "bytes"

func normalizeCSharpQueryExpressions(root *Node, source []byte, p *Parser) {
	if root == nil || p == nil || p.language == nil || p.language.Name != "c_sharp" || len(source) == 0 {
		return
	}
	if root.ownerArena == nil {
		return
	}
	if !root.HasError() && root.EndByte() >= uint32(len(source)) {
		return
	}
	if recovered, ok := csharpRecoverQueryAssignmentsRoot(source, p, root.ownerArena); ok {
		*root = *recovered
		root.parent = nil
		root.childIndex = -1
		return
	}
	spec, ok := csharpFindSimpleJoinQuerySpec(source)
	if !ok {
		return
	}
	recovered, ok := csharpRecoverQuerySkeletonRoot(source, p, root.ownerArena, spec)
	if !ok {
		return
	}
	*root = *recovered
	root.parent = nil
	root.childIndex = -1
}

type csharpQueryClauseKind uint8

const (
	csharpQueryFromClause csharpQueryClauseKind = iota
	csharpQueryWhereClause
	csharpQueryOrderByClause
	csharpQueryLetClause
	csharpQueryJoinClause
	csharpQueryGroupClause
	csharpQuerySelectClause
)

type csharpQueryClauseSpec struct {
	kind    csharpQueryClauseKind
	start   uint32
	end     uint32
	keyword [2]uint32
	name    [2]uint32
	sep1    [2]uint32
	sep2    [2]uint32
	sep3    [2]uint32
	value1  [2]uint32
	value2  [2]uint32
	value3  [2]uint32
	extra   [2]uint32
}

type csharpQueryAssignmentSpec struct {
	queryStart uint32
	queryEnd   uint32
	semiPos    uint32
	clauses    []csharpQueryClauseSpec
}

func csharpRecoverQueryAssignmentsRoot(source []byte, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil || p.language == nil || arena == nil {
		return nil, false
	}
	specs, ok := csharpFindQueryAssignmentSpecs(source)
	if !ok || len(specs) == 0 {
		return nil, false
	}
	skeleton := append([]byte(nil), source...)
	for _, spec := range specs {
		for i := spec.queryStart; i < spec.queryEnd; i++ {
			skeleton[i] = ' '
		}
		if spec.queryStart < uint32(len(skeleton)) {
			skeleton[spec.queryStart] = '0'
		}
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
	for _, spec := range specs {
		queryExpr, ok := csharpBuildRecoveredQueryExpression(arena, source, p, spec)
		if !ok {
			return nil, false
		}
		if !csharpReplaceRecoveredQueryExpression(cloned, p.language, spec.queryStart, spec.queryEnd, queryExpr) {
			return nil, false
		}
	}
	return cloned, true
}

func csharpFindQueryAssignmentSpecs(source []byte) ([]csharpQueryAssignmentSpec, bool) {
	var specs []csharpQueryAssignmentSpec
	cursor := uint32(0)
	for cursor < uint32(len(source)) {
		eqRel := bytes.IndexByte(source[cursor:], '=')
		if eqRel < 0 {
			break
		}
		eqPos := cursor + uint32(eqRel)
		queryStart := csharpSkipSpaceBytes(source, eqPos+1)
		if !csharpHasKeywordAt(source, queryStart, "from") {
			cursor = eqPos + 1
			continue
		}
		spec, ok := csharpParseQueryAssignmentSpec(source, queryStart)
		if !ok {
			cursor = eqPos + 1
			continue
		}
		specs = append(specs, spec)
		cursor = spec.semiPos + 1
	}
	return specs, len(specs) > 0
}

func csharpParseQueryAssignmentSpec(source []byte, queryStart uint32) (csharpQueryAssignmentSpec, bool) {
	var spec csharpQueryAssignmentSpec
	spec.queryStart = queryStart
	semiRel := bytes.IndexByte(source[queryStart:], ';')
	if semiRel < 0 {
		return spec, false
	}
	spec.semiPos = queryStart + uint32(semiRel)
	spec.queryEnd = csharpTrimRightSpaceBytes(source, spec.semiPos)
	pos := queryStart
	for pos < spec.queryEnd {
		kw, kwPos, ok := csharpFindNextQueryKeyword(source, pos)
		if !ok || kwPos != pos {
			return spec, false
		}
		var clause csharpQueryClauseSpec
		var next uint32
		switch kw {
		case "from":
			clause, next, ok = csharpParseFromQueryClause(source, pos, spec.queryEnd)
		case "where":
			clause, next, ok = csharpParseWhereQueryClause(source, pos, spec.queryEnd)
		case "orderby":
			clause, next, ok = csharpParseOrderByQueryClause(source, pos, spec.queryEnd)
		case "let":
			clause, next, ok = csharpParseLetQueryClause(source, pos, spec.queryEnd)
		case "join":
			clause, next, ok = csharpParseJoinQueryClause(source, pos, spec.queryEnd)
		case "group":
			clause, next, ok = csharpParseGroupQueryClause(source, pos, spec.queryEnd)
		case "select":
			clause, next, ok = csharpParseSelectQueryClause(source, pos, spec.queryEnd)
		default:
			return spec, false
		}
		if !ok {
			return spec, false
		}
		spec.clauses = append(spec.clauses, clause)
		pos = csharpSkipSpaceBytes(source, next)
		if clause.kind == csharpQuerySelectClause {
			if pos != spec.queryEnd {
				return spec, false
			}
			break
		}
	}
	return spec, len(spec.clauses) >= 2
}

func csharpParseFromQueryClause(source []byte, start, queryEnd uint32) (csharpQueryClauseSpec, uint32, bool) {
	var clause csharpQueryClauseSpec
	clause.kind = csharpQueryFromClause
	clause.start = start
	clause.keyword = [2]uint32{start, start + 4}
	var ok bool
	if clause.name[0], clause.name[1], ok = csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, clause.keyword[1])); !ok {
		return clause, 0, false
	}
	clause.sep1[0] = csharpSkipSpaceBytes(source, clause.name[1])
	clause.sep1[1] = clause.sep1[0] + 2
	if !csharpHasKeywordAt(source, clause.sep1[0], "in") {
		return clause, 0, false
	}
	exprStart := csharpSkipSpaceBytes(source, clause.sep1[1])
	nextKeyword, nextPos, ok := csharpFindNextQueryKeyword(source, exprStart)
	if !ok || nextPos > queryEnd || nextKeyword == "into" {
		nextPos = queryEnd
	}
	clause.value1 = [2]uint32{exprStart, csharpTrimRightSpaceBytes(source, nextPos)}
	clause.end = clause.value1[1]
	return clause, nextPos, clause.value1[0] < clause.value1[1]
}

func csharpParseWhereQueryClause(source []byte, start, queryEnd uint32) (csharpQueryClauseSpec, uint32, bool) {
	var clause csharpQueryClauseSpec
	clause.kind = csharpQueryWhereClause
	clause.start = start
	clause.keyword = [2]uint32{start, start + 5}
	exprStart := csharpSkipSpaceBytes(source, clause.keyword[1])
	nextKeyword, nextPos, ok := csharpFindNextQueryKeyword(source, exprStart)
	if !ok || nextPos > queryEnd || nextKeyword == "into" {
		nextPos = queryEnd
	}
	clause.value1 = [2]uint32{exprStart, csharpTrimRightSpaceBytes(source, nextPos)}
	clause.end = clause.value1[1]
	return clause, nextPos, clause.value1[0] < clause.value1[1]
}

func csharpParseOrderByQueryClause(source []byte, start, queryEnd uint32) (csharpQueryClauseSpec, uint32, bool) {
	var clause csharpQueryClauseSpec
	clause.kind = csharpQueryOrderByClause
	clause.start = start
	clause.keyword = [2]uint32{start, start + 7}
	exprStart := csharpSkipSpaceBytes(source, clause.keyword[1])
	nextKeyword, nextPos, ok := csharpFindNextQueryKeyword(source, exprStart)
	if !ok || nextPos > queryEnd || nextKeyword == "into" {
		nextPos = queryEnd
	}
	clauseEnd := csharpTrimRightSpaceBytes(source, nextPos)
	if dirStart, dirEnd, ok := csharpFindTrailingDirection(source, exprStart, clauseEnd); ok {
		clause.extra = [2]uint32{dirStart, dirEnd}
		clauseEnd = csharpTrimRightSpaceBytes(source, dirStart)
	}
	clause.value1 = [2]uint32{exprStart, clauseEnd}
	clause.end = clause.extra[1]
	if clause.end == 0 {
		clause.end = clause.value1[1]
	}
	return clause, nextPos, clause.value1[0] < clause.value1[1]
}

func csharpParseLetQueryClause(source []byte, start, queryEnd uint32) (csharpQueryClauseSpec, uint32, bool) {
	var clause csharpQueryClauseSpec
	clause.kind = csharpQueryLetClause
	clause.start = start
	clause.keyword = [2]uint32{start, start + 3}
	var ok bool
	if clause.name[0], clause.name[1], ok = csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, clause.keyword[1])); !ok {
		return clause, 0, false
	}
	sep := csharpSkipSpaceBytes(source, clause.name[1])
	if sep >= uint32(len(source)) || source[sep] != '=' {
		return clause, 0, false
	}
	clause.sep1 = [2]uint32{sep, sep + 1}
	exprStart := csharpSkipSpaceBytes(source, clause.sep1[1])
	nextKeyword, nextPos, ok := csharpFindNextQueryKeyword(source, exprStart)
	if !ok || nextPos > queryEnd || nextKeyword == "into" {
		nextPos = queryEnd
	}
	clause.value1 = [2]uint32{exprStart, csharpTrimRightSpaceBytes(source, nextPos)}
	clause.end = clause.value1[1]
	return clause, nextPos, clause.value1[0] < clause.value1[1]
}

func csharpParseJoinQueryClause(source []byte, start, queryEnd uint32) (csharpQueryClauseSpec, uint32, bool) {
	var clause csharpQueryClauseSpec
	clause.kind = csharpQueryJoinClause
	clause.start = start
	clause.keyword = [2]uint32{start, start + 4}
	var ok bool
	if clause.name[0], clause.name[1], ok = csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, clause.keyword[1])); !ok {
		return clause, 0, false
	}
	clause.sep1[0] = csharpSkipSpaceBytes(source, clause.name[1])
	clause.sep1[1] = clause.sep1[0] + 2
	if !csharpHasKeywordAt(source, clause.sep1[0], "in") {
		return clause, 0, false
	}
	sourceStart := csharpSkipSpaceBytes(source, clause.sep1[1])
	onPos, ok := csharpFindKeywordAfter(source, sourceStart, queryEnd, "on")
	if !ok {
		return clause, 0, false
	}
	clause.value1 = [2]uint32{sourceStart, csharpTrimRightSpaceBytes(source, onPos)}
	clause.sep2 = [2]uint32{onPos, onPos + 2}
	leftStart := csharpSkipSpaceBytes(source, clause.sep2[1])
	equalsPos, ok := csharpFindKeywordAfter(source, leftStart, queryEnd, "equals")
	if !ok {
		return clause, 0, false
	}
	clause.value2 = [2]uint32{leftStart, csharpTrimRightSpaceBytes(source, equalsPos)}
	clause.sep3 = [2]uint32{equalsPos, equalsPos + 6}
	rightStart := csharpSkipSpaceBytes(source, clause.sep3[1])
	nextKeyword, nextPos, ok := csharpFindNextQueryKeyword(source, rightStart)
	if !ok || nextPos > queryEnd || nextKeyword == "into" {
		nextPos = queryEnd
	}
	clause.value3 = [2]uint32{rightStart, csharpTrimRightSpaceBytes(source, nextPos)}
	clause.end = clause.value3[1]
	return clause, nextPos, clause.value1[0] < clause.value1[1] && clause.value2[0] < clause.value2[1] && clause.value3[0] < clause.value3[1]
}

func csharpParseGroupQueryClause(source []byte, start, queryEnd uint32) (csharpQueryClauseSpec, uint32, bool) {
	var clause csharpQueryClauseSpec
	clause.kind = csharpQueryGroupClause
	clause.start = start
	clause.keyword = [2]uint32{start, start + 5}
	groupExprStart := csharpSkipSpaceBytes(source, clause.keyword[1])
	byPos, ok := csharpFindKeywordAfter(source, groupExprStart, queryEnd, "by")
	if !ok {
		return clause, 0, false
	}
	clause.value1 = [2]uint32{groupExprStart, csharpTrimRightSpaceBytes(source, byPos)}
	clause.sep1 = [2]uint32{byPos, byPos + 2}
	keyExprStart := csharpSkipSpaceBytes(source, clause.sep1[1])
	nextKeyword, nextPos, ok := csharpFindNextQueryKeyword(source, keyExprStart)
	if !ok || nextPos > queryEnd {
		nextPos = queryEnd
	}
	if nextKeyword == "into" {
		clause.value2 = [2]uint32{keyExprStart, csharpTrimRightSpaceBytes(source, nextPos)}
		clause.sep2 = [2]uint32{nextPos, nextPos + 4}
		var okIdent bool
		if clause.name[0], clause.name[1], okIdent = csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, clause.sep2[1])); !okIdent {
			return clause, 0, false
		}
		clause.end = clause.name[1]
		nextKeyword, nextPos, ok = csharpFindNextQueryKeyword(source, csharpSkipSpaceBytes(source, clause.end))
		if !ok || nextKeyword != "select" {
			return clause, 0, false
		}
		return clause, nextPos, clause.value1[0] < clause.value1[1] && clause.value2[0] < clause.value2[1]
	}
	clause.value2 = [2]uint32{keyExprStart, csharpTrimRightSpaceBytes(source, nextPos)}
	clause.end = clause.value2[1]
	return clause, nextPos, clause.value1[0] < clause.value1[1] && clause.value2[0] < clause.value2[1]
}

func csharpParseSelectQueryClause(source []byte, start, queryEnd uint32) (csharpQueryClauseSpec, uint32, bool) {
	var clause csharpQueryClauseSpec
	clause.kind = csharpQuerySelectClause
	clause.start = start
	clause.keyword = [2]uint32{start, start + 6}
	exprStart := csharpSkipSpaceBytes(source, clause.keyword[1])
	clause.value1 = [2]uint32{exprStart, csharpTrimRightSpaceBytes(source, queryEnd)}
	clause.end = clause.value1[1]
	return clause, queryEnd, clause.value1[0] < clause.value1[1]
}

func csharpFindNextQueryKeyword(source []byte, start uint32) (string, uint32, bool) {
	keywords := []string{"from", "where", "orderby", "let", "join", "group", "into", "select"}
	for i := start; i < uint32(len(source)); i++ {
		for _, kw := range keywords {
			if csharpHasKeywordBoundaryAt(source, i, kw) {
				return kw, i, true
			}
		}
	}
	return "", 0, false
}

func csharpFindKeywordAfter(source []byte, start, limit uint32, kw string) (uint32, bool) {
	if limit > uint32(len(source)) {
		limit = uint32(len(source))
	}
	for i := start; i < limit; i++ {
		if csharpHasKeywordBoundaryAt(source, i, kw) {
			return i, true
		}
	}
	return 0, false
}

func csharpHasKeywordBoundaryAt(source []byte, start uint32, kw string) bool {
	if !csharpHasKeywordAt(source, start, kw) {
		return false
	}
	if start > 0 && csharpIdentifierContinueByte(source[start-1]) {
		return false
	}
	end := start + uint32(len(kw))
	if end < uint32(len(source)) && csharpIdentifierContinueByte(source[end]) {
		return false
	}
	return true
}

func csharpFindTrailingDirection(source []byte, start, end uint32) (uint32, uint32, bool) {
	end = csharpTrimRightSpaceBytes(source, end)
	for _, kw := range []string{"ascending", "descending"} {
		if end < uint32(len(kw)) {
			continue
		}
		dirStart := end - uint32(len(kw))
		if dirStart < start {
			continue
		}
		if csharpHasKeywordBoundaryAt(source, dirStart, kw) {
			return dirStart, end, true
		}
	}
	return 0, 0, false
}

func csharpBuildRecoveredQueryExpression(arena *nodeArena, source []byte, p *Parser, spec csharpQueryAssignmentSpec) (*Node, bool) {
	if arena == nil || p == nil || p.language == nil || len(spec.clauses) == 0 {
		return nil, false
	}
	queryExprSym, ok := symbolByName(p.language, "query_expression")
	if !ok {
		return nil, false
	}
	queryExprNamed := int(queryExprSym) < len(p.language.SymbolMetadata) && p.language.SymbolMetadata[queryExprSym].Named
	children := make([]*Node, 0, len(spec.clauses)+2)
	for _, clause := range spec.clauses {
		node, extra, ok := csharpBuildRecoveredQueryClause(arena, source, p, clause)
		if !ok {
			return nil, false
		}
		children = append(children, node)
		if len(extra) > 0 {
			children = append(children, extra...)
		}
	}
	return newParentNodeInArena(arena, queryExprSym, queryExprNamed, children, nil, 0), true
}

func csharpBuildRecoveredQueryClause(arena *nodeArena, source []byte, p *Parser, clause csharpQueryClauseSpec) (*Node, []*Node, bool) {
	if arena == nil || p == nil || p.language == nil {
		return nil, nil, false
	}
	lang := p.language
	identifierSym, ok := symbolByName(lang, "identifier")
	if !ok {
		return nil, nil, false
	}
	identifierNamed := int(identifierSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[identifierSym].Named
	leafByName := func(name string, span [2]uint32) (*Node, bool) {
		sym, ok := symbolByName(lang, name)
		if !ok || span[0] >= span[1] {
			return nil, false
		}
		named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
		return newLeafNodeInArena(arena, sym, named, span[0], span[1], advancePointByBytes(Point{}, source[:span[0]]), advancePointByBytes(Point{}, source[:span[1]])), true
	}
	ident := func(span [2]uint32) *Node {
		return newLeafNodeInArena(arena, identifierSym, identifierNamed, span[0], span[1], advancePointByBytes(Point{}, source[:span[0]]), advancePointByBytes(Point{}, source[:span[1]]))
	}
	expr := func(span [2]uint32) (*Node, bool) {
		return csharpRecoverExpressionNodeFromRange(source, span[0], span[1], p, arena)
	}
	switch clause.kind {
	case csharpQueryFromClause:
		fromClauseSym, ok := symbolByName(lang, "from_clause")
		if !ok {
			return nil, nil, false
		}
		fromClauseNamed := int(fromClauseSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[fromClauseSym].Named
		nameFieldID, ok := lang.FieldByName("name")
		if !ok {
			return nil, nil, false
		}
		fromTok, ok := leafByName("from", clause.keyword)
		if !ok {
			return nil, nil, false
		}
		inTok, ok := leafByName("in", clause.sep1)
		if !ok {
			return nil, nil, false
		}
		sourceNode, ok := expr(clause.value1)
		if !ok {
			return nil, nil, false
		}
		children := []*Node{fromTok, ident(clause.name), inTok, sourceNode}
		fields := csharpFieldIDsInArena(arena, []FieldID{0, nameFieldID, 0, 0})
		return newParentNodeInArena(arena, fromClauseSym, fromClauseNamed, children, fields, 0), nil, true
	case csharpQueryWhereClause:
		whereClauseSym, ok := symbolByName(lang, "where_clause")
		if !ok {
			return nil, nil, false
		}
		whereClauseNamed := int(whereClauseSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[whereClauseSym].Named
		whereTok, ok := leafByName("where", clause.keyword)
		if !ok {
			return nil, nil, false
		}
		value, ok := expr(clause.value1)
		if !ok {
			return nil, nil, false
		}
		return newParentNodeInArena(arena, whereClauseSym, whereClauseNamed, []*Node{whereTok, value}, nil, 0), nil, true
	case csharpQueryOrderByClause:
		orderByClauseSym, ok := symbolByName(lang, "order_by_clause")
		if !ok {
			return nil, nil, false
		}
		orderByClauseNamed := int(orderByClauseSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[orderByClauseSym].Named
		orderTok, ok := leafByName("orderby", clause.keyword)
		if !ok {
			return nil, nil, false
		}
		value, ok := expr(clause.value1)
		if !ok {
			return nil, nil, false
		}
		children := []*Node{orderTok, value}
		if clause.extra[0] < clause.extra[1] {
			dirName := string(source[clause.extra[0]:clause.extra[1]])
			dirTok, ok := leafByName(dirName, clause.extra)
			if !ok {
				return nil, nil, false
			}
			children = append(children, dirTok)
		}
		return newParentNodeInArena(arena, orderByClauseSym, orderByClauseNamed, children, nil, 0), nil, true
	case csharpQueryLetClause:
		letClauseSym, ok := symbolByName(lang, "let_clause")
		if !ok {
			return nil, nil, false
		}
		letClauseNamed := int(letClauseSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[letClauseSym].Named
		letTok, ok := leafByName("let", clause.keyword)
		if !ok {
			return nil, nil, false
		}
		eqTok, ok := leafByName("=", clause.sep1)
		if !ok {
			return nil, nil, false
		}
		value, ok := expr(clause.value1)
		if !ok {
			return nil, nil, false
		}
		return newParentNodeInArena(arena, letClauseSym, letClauseNamed, []*Node{letTok, ident(clause.name), eqTok, value}, nil, 0), nil, true
	case csharpQueryJoinClause:
		joinClauseSym, ok := symbolByName(lang, "join_clause")
		if !ok {
			return nil, nil, false
		}
		joinClauseNamed := int(joinClauseSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[joinClauseSym].Named
		joinTok, ok := leafByName("join", clause.keyword)
		if !ok {
			return nil, nil, false
		}
		inTok, ok := leafByName("in", clause.sep1)
		if !ok {
			return nil, nil, false
		}
		onTok, ok := leafByName("on", clause.sep2)
		if !ok {
			return nil, nil, false
		}
		equalsTok, ok := leafByName("equals", clause.sep3)
		if !ok {
			return nil, nil, false
		}
		sourceNode, ok := expr(clause.value1)
		if !ok {
			return nil, nil, false
		}
		leftNode, ok := expr(clause.value2)
		if !ok {
			return nil, nil, false
		}
		rightNode, ok := expr(clause.value3)
		if !ok {
			return nil, nil, false
		}
		children := []*Node{joinTok, ident(clause.name), inTok, sourceNode, onTok, leftNode, equalsTok, rightNode}
		return newParentNodeInArena(arena, joinClauseSym, joinClauseNamed, children, nil, 0), nil, true
	case csharpQueryGroupClause:
		groupClauseSym, ok := symbolByName(lang, "group_clause")
		if !ok {
			return nil, nil, false
		}
		groupClauseNamed := int(groupClauseSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[groupClauseSym].Named
		groupTok, ok := leafByName("group", clause.keyword)
		if !ok {
			return nil, nil, false
		}
		byTok, ok := leafByName("by", clause.sep1)
		if !ok {
			return nil, nil, false
		}
		groupExpr, ok := expr(clause.value1)
		if !ok {
			return nil, nil, false
		}
		keyExpr, ok := expr(clause.value2)
		if !ok {
			return nil, nil, false
		}
		groupClause := newParentNodeInArena(arena, groupClauseSym, groupClauseNamed, []*Node{groupTok, groupExpr, byTok, keyExpr}, nil, 0)
		var extra []*Node
		if clause.sep2[0] < clause.sep2[1] && clause.name[0] < clause.name[1] {
			intoTok, ok := leafByName("into", clause.sep2)
			if !ok {
				return nil, nil, false
			}
			extra = []*Node{intoTok, ident(clause.name)}
		}
		return groupClause, extra, true
	case csharpQuerySelectClause:
		selectClauseSym, ok := symbolByName(lang, "select_clause")
		if !ok {
			return nil, nil, false
		}
		selectClauseNamed := int(selectClauseSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[selectClauseSym].Named
		selectTok, ok := leafByName("select", clause.keyword)
		if !ok {
			return nil, nil, false
		}
		value, ok := expr(clause.value1)
		if !ok {
			return nil, nil, false
		}
		return newParentNodeInArena(arena, selectClauseSym, selectClauseNamed, []*Node{selectTok, value}, nil, 0), nil, true
	default:
		return nil, nil, false
	}
}

func csharpRecoverExpressionNodeFromRange(source []byte, start, end uint32, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil {
		return nil, false
	}
	if node, ok := csharpRecoverSimpleQueryAtomNodeFromRange(source, start, end, p.language, arena); ok {
		return node, true
	}
	if node, ok := csharpRecoverExpressionNodeFromRangeWithWrapper(source, start, end, p, arena); ok {
		return node, true
	}
	return csharpRecoverQueryExpressionNodeFromRange(source, start, end, p.language, arena)
}

func csharpRecoverExpressionNodeFromRangeWithWrapper(source []byte, start, end uint32, p *Parser, arena *nodeArena) (*Node, bool) {
	if p == nil || p.language == nil || arena == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	start, end = csharpTrimSpaceBounds(source, start, end)
	if start >= end {
		return nil, false
	}
	const prefix = "class __Q { void __M() { var __q = "
	const suffix = "; } }\n"
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
	node := csharpExtractRecoveredVariableInitializer(tree.RootNode(), p.language, arena)
	if node == nil {
		return nil, false
	}
	if !shiftNodeBytes(node, int64(start)-int64(len(prefix))) {
		return nil, false
	}
	recomputeNodePointsFromBytes(node, source)
	return node, node != nil
}

func csharpRecoverQueryExpressionNodeFromRange(source []byte, start, end uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || arena == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	start, end = csharpTrimSpaceBounds(source, start, end)
	if start >= end {
		return nil, false
	}
	if qPos, ok := csharpFindTopLevelOperator(source, start, end, "?"); ok {
		colonPos, ok := csharpFindConditionalColon(source, qPos+1, end)
		if !ok {
			return nil, false
		}
		condition, ok := csharpRecoverQueryExpressionNodeFromRange(source, start, qPos, lang, arena)
		if !ok {
			return nil, false
		}
		consequence, ok := csharpRecoverQueryExpressionNodeFromRange(source, qPos+1, colonPos, lang, arena)
		if !ok {
			return nil, false
		}
		alternative, ok := csharpRecoverQueryExpressionNodeFromRange(source, colonPos+1, end, lang, arena)
		if !ok {
			return nil, false
		}
		return csharpBuildConditionalExpressionNode(arena, source, lang, condition, qPos, consequence, colonPos, alternative)
	}
	if csharpHasKeywordAt(source, start, "new") {
		if node, ok := csharpBuildAnonymousObjectCreationNode(arena, source, lang, start, end); ok {
			return node, true
		}
	}
	if source[start] == '(' && source[end-1] == ')' {
		if node, ok := csharpBuildTupleExpressionNode(arena, source, lang, start, end); ok {
			return node, true
		}
	}
	if arrowPos, ok := csharpFindTopLevelOperator(source, start, end, "=>"); ok {
		return csharpBuildLambdaExpressionNode(arena, source, lang, start, arrowPos, end)
	}
	if opPos, ok := csharpFindTopLevelOperator(source, start, end, "=="); ok {
		return csharpBuildBinaryExpressionNode(arena, source, lang, start, opPos, opPos+2, end)
	}
	if opPos, ok := csharpFindTopLevelOperator(source, start, end, "*"); ok {
		return csharpBuildBinaryExpressionNode(arena, source, lang, start, opPos, opPos+1, end)
	}
	if opPos, ok := csharpFindTopLevelAssignment(source, start, end); ok {
		return csharpBuildAssignmentExpressionNode(arena, source, lang, start, opPos, end)
	}
	if end > start && source[end-1] == ')' {
		if node, ok := csharpBuildInvocationExpressionNode(arena, source, lang, start, end); ok {
			return node, true
		}
	}
	if dotPos, ok := csharpFindLastTopLevelOperator(source, start, end, "."); ok {
		return csharpBuildMemberAccessExpressionNode(arena, source, lang, start, dotPos, end)
	}
	if source[start] == '"' && source[end-1] == '"' && end-start >= 2 {
		return csharpBuildStringLiteralNode(arena, source, lang, start, end)
	}
	if csharpIsIntegerLiteral(source[start:end]) {
		return csharpBuildLeafNodeByName(arena, source, lang, "integer_literal", start, end)
	}
	if identStart, identEnd, ok := csharpScanIdentifierAt(source, start); ok && identStart == start && identEnd == end {
		return csharpBuildIdentifierNodeFromSource(source, start, end, lang, arena)
	}
	return nil, false
}

func csharpBuildTupleExpressionNode(arena *nodeArena, source []byte, lang *Language, start, end uint32) (*Node, bool) {
	if lang == nil || start >= end || int(end) > len(source) || source[start] != '(' || source[end-1] != ')' {
		return nil, false
	}
	items := csharpSplitTopLevelByComma(source, start+1, end-1)
	if len(items) < 2 {
		return nil, false
	}
	sym, ok := symbolByName(lang, "tuple_expression")
	if !ok {
		return nil, false
	}
	argSym, ok := symbolByName(lang, "argument")
	if !ok {
		return nil, false
	}
	argNamed := int(argSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[argSym].Named
	openTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "(", start, start+1)
	if !ok {
		return nil, false
	}
	closeTok, ok := csharpBuildLeafNodeByName(arena, source, lang, ")", end-1, end)
	if !ok {
		return nil, false
	}
	children := []*Node{openTok}
	for i, span := range items {
		itemStart, itemEnd := csharpTrimSpaceBounds(source, span[0], span[1])
		if itemStart >= itemEnd {
			return nil, false
		}
		value, ok := csharpRecoverQueryExpressionNodeFromRange(source, itemStart, itemEnd, lang, arena)
		if !ok {
			return nil, false
		}
		children = append(children, newParentNodeInArena(arena, argSym, argNamed, []*Node{value}, nil, 0))
		if i < len(items)-1 {
			commaPos := csharpFindCommaBetween(source, span[1], items[i+1][0])
			if commaPos == 0 && source[span[1]] != ',' {
				return nil, false
			}
			if commaPos == 0 {
				commaPos = span[1]
			}
			commaTok, ok := csharpBuildLeafNodeByName(arena, source, lang, ",", commaPos, commaPos+1)
			if !ok {
				return nil, false
			}
			children = append(children, commaTok)
		}
	}
	children = append(children, closeTok)
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, children, nil, 0), true
}

func csharpRecoverSimpleQueryAtomNodeFromRange(source []byte, start, end uint32, lang *Language, arena *nodeArena) (*Node, bool) {
	if lang == nil || arena == nil || start >= end || int(end) > len(source) {
		return nil, false
	}
	start, end = csharpTrimSpaceBounds(source, start, end)
	if start >= end {
		return nil, false
	}
	if source[start] == '"' && source[end-1] == '"' && end-start >= 2 {
		return csharpBuildStringLiteralNode(arena, source, lang, start, end)
	}
	if csharpIsIntegerLiteral(source[start:end]) {
		return csharpBuildLeafNodeByName(arena, source, lang, "integer_literal", start, end)
	}
	if identStart, identEnd, ok := csharpScanIdentifierAt(source, start); ok && identStart == start && identEnd == end {
		return csharpBuildIdentifierNodeFromSource(source, start, end, lang, arena)
	}
	if dotPos, ok := csharpFindLastTopLevelOperator(source, start, end, "."); ok {
		return csharpBuildMemberAccessExpressionNode(arena, source, lang, start, dotPos, end)
	}
	return nil, false
}

func csharpBuildConditionalExpressionNode(arena *nodeArena, source []byte, lang *Language, condition *Node, qPos uint32, consequence *Node, colonPos uint32, alternative *Node) (*Node, bool) {
	sym, ok := symbolByName(lang, "conditional_expression")
	if !ok {
		return nil, false
	}
	conditionID, _ := lang.FieldByName("condition")
	consequenceID, _ := lang.FieldByName("consequence")
	alternativeID, _ := lang.FieldByName("alternative")
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	children := []*Node{condition, consequence, alternative}
	fieldIDs := []FieldID{conditionID, consequenceID, alternativeID}
	if qTok, qOK := csharpBuildLeafNodeByName(arena, source, lang, "?", qPos, qPos+1); qOK {
		if colonTok, colonOK := csharpBuildLeafNodeByName(arena, source, lang, ":", colonPos, colonPos+1); colonOK {
			children = []*Node{condition, qTok, consequence, colonTok, alternative}
			fieldIDs = []FieldID{conditionID, 0, consequenceID, 0, alternativeID}
		}
	}
	fields := csharpFieldIDsInArena(arena, fieldIDs)
	return newParentNodeInArena(arena, sym, named, children, fields, 0), true
}

func csharpBuildBinaryExpressionNode(arena *nodeArena, source []byte, lang *Language, start, opStart, opEnd, end uint32) (*Node, bool) {
	left, ok := csharpRecoverQueryExpressionNodeFromRange(source, start, opStart, lang, arena)
	if !ok {
		return nil, false
	}
	right, ok := csharpRecoverQueryExpressionNodeFromRange(source, opEnd, end, lang, arena)
	if !ok {
		return nil, false
	}
	sym, ok := symbolByName(lang, "binary_expression")
	if !ok {
		return nil, false
	}
	opTok, ok := csharpBuildLeafNodeByName(arena, source, lang, string(source[opStart:opEnd]), opStart, opEnd)
	if !ok {
		return nil, false
	}
	leftID, _ := lang.FieldByName("left")
	operatorID, _ := lang.FieldByName("operator")
	rightID, _ := lang.FieldByName("right")
	fields := csharpFieldIDsInArena(arena, []FieldID{leftID, operatorID, rightID})
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, []*Node{left, opTok, right}, fields, 0), true
}

func csharpBuildAssignmentExpressionNode(arena *nodeArena, source []byte, lang *Language, start, opPos, end uint32) (*Node, bool) {
	left, ok := csharpRecoverQueryExpressionNodeFromRange(source, start, opPos, lang, arena)
	if !ok {
		return nil, false
	}
	right, ok := csharpRecoverQueryExpressionNodeFromRange(source, opPos+1, end, lang, arena)
	if !ok {
		return nil, false
	}
	sym, ok := symbolByName(lang, "assignment_expression")
	if !ok {
		return nil, false
	}
	eqTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "=", opPos, opPos+1)
	if !ok {
		return nil, false
	}
	leftID, _ := lang.FieldByName("left")
	operatorID, _ := lang.FieldByName("operator")
	rightID, _ := lang.FieldByName("right")
	fields := csharpFieldIDsInArena(arena, []FieldID{leftID, operatorID, rightID})
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, []*Node{left, eqTok, right}, fields, 0), true
}

func csharpBuildInvocationExpressionNode(arena *nodeArena, source []byte, lang *Language, start, end uint32) (*Node, bool) {
	openPos, ok := csharpFindInvocationOpenParen(source, start, end)
	if !ok {
		return nil, false
	}
	function, ok := csharpRecoverQueryExpressionNodeFromRange(source, start, openPos, lang, arena)
	if !ok {
		return nil, false
	}
	args, ok := csharpBuildArgumentListNode(arena, source, lang, openPos, end)
	if !ok {
		return nil, false
	}
	sym, ok := symbolByName(lang, "invocation_expression")
	if !ok {
		return nil, false
	}
	functionID, _ := lang.FieldByName("function")
	argumentsID, _ := lang.FieldByName("arguments")
	fields := csharpFieldIDsInArena(arena, []FieldID{functionID, argumentsID})
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, []*Node{function, args}, fields, 0), true
}

func csharpBuildArgumentListNode(arena *nodeArena, source []byte, lang *Language, openPos, end uint32) (*Node, bool) {
	sym, ok := symbolByName(lang, "argument_list")
	if !ok {
		return nil, false
	}
	openTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "(", openPos, openPos+1)
	if !ok {
		return nil, false
	}
	closeTok, ok := csharpBuildLeafNodeByName(arena, source, lang, ")", end-1, end)
	if !ok {
		return nil, false
	}
	children := []*Node{openTok}
	items := csharpSplitTopLevelByComma(source, openPos+1, end-1)
	argSym, ok := symbolByName(lang, "argument")
	if !ok {
		return nil, false
	}
	argNamed := int(argSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[argSym].Named
	commaSym, _ := symbolByName(lang, ",")
	for i, span := range items {
		itemStart, itemEnd := csharpTrimSpaceBounds(source, span[0], span[1])
		if itemStart >= itemEnd {
			continue
		}
		value, ok := csharpRecoverQueryExpressionNodeFromRange(source, itemStart, itemEnd, lang, arena)
		if !ok {
			return nil, false
		}
		children = append(children, newParentNodeInArena(arena, argSym, argNamed, []*Node{value}, nil, 0))
		if i < len(items)-1 {
			commaPos := csharpFindCommaBetween(source, span[1], items[i+1][0])
			if commaPos == 0 && source[span[1]] != ',' {
				return nil, false
			}
			if commaPos == 0 {
				commaPos = span[1]
			}
			commaTok, ok := csharpBuildLeafNodeByName(arena, source, lang, ",", commaPos, commaPos+1)
			if !ok && commaSym != 0 {
				return nil, false
			}
			children = append(children, commaTok)
		}
	}
	children = append(children, closeTok)
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, children, nil, 0), true
}

func csharpBuildMemberAccessExpressionNode(arena *nodeArena, source []byte, lang *Language, start, dotPos, end uint32) (*Node, bool) {
	left, ok := csharpRecoverQueryExpressionNodeFromRange(source, start, dotPos, lang, arena)
	if !ok {
		return nil, false
	}
	rightStart, rightEnd, ok := csharpScanIdentifierAt(source, csharpSkipSpaceBytes(source, dotPos+1))
	if !ok || rightEnd != end {
		return nil, false
	}
	sym, ok := symbolByName(lang, "member_access_expression")
	if !ok {
		return nil, false
	}
	dotTok, ok := csharpBuildLeafNodeByName(arena, source, lang, ".", dotPos, dotPos+1)
	if !ok {
		return nil, false
	}
	nameNode, ok := csharpBuildIdentifierNodeFromSource(source, rightStart, rightEnd, lang, arena)
	if !ok {
		return nil, false
	}
	expressionID, _ := lang.FieldByName("expression")
	nameID, _ := lang.FieldByName("name")
	fields := csharpFieldIDsInArena(arena, []FieldID{expressionID, 0, nameID})
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, []*Node{left, dotTok, nameNode}, fields, 0), true
}

func csharpBuildAnonymousObjectCreationNode(arena *nodeArena, source []byte, lang *Language, start, end uint32) (*Node, bool) {
	openPos := csharpSkipSpaceBytes(source, start+3)
	if openPos >= end || end == 0 || source[openPos] != '{' || source[end-1] != '}' {
		return nil, false
	}
	sym, ok := symbolByName(lang, "anonymous_object_creation_expression")
	if !ok {
		return nil, false
	}
	newTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "new", start, start+3)
	if !ok {
		return nil, false
	}
	openTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "{", openPos, openPos+1)
	if !ok {
		return nil, false
	}
	closeTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "}", end-1, end)
	if !ok {
		return nil, false
	}
	children := []*Node{newTok, openTok}
	items := csharpSplitTopLevelByComma(source, openPos+1, end-1)
	for i, span := range items {
		itemStart, itemEnd := csharpTrimSpaceBounds(source, span[0], span[1])
		if itemStart >= itemEnd {
			continue
		}
		if eqPos, ok := csharpFindTopLevelAssignment(source, itemStart, itemEnd); ok {
			nameStart, nameEnd, ok := csharpScanIdentifierAt(source, itemStart)
			if !ok {
				return nil, false
			}
			nameNode, ok := csharpBuildIdentifierNodeFromSource(source, nameStart, nameEnd, lang, arena)
			if !ok {
				return nil, false
			}
			eqTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "=", eqPos, eqPos+1)
			if !ok {
				return nil, false
			}
			valueNode, ok := csharpRecoverQueryExpressionNodeFromRange(source, eqPos+1, itemEnd, lang, arena)
			if !ok {
				return nil, false
			}
			children = append(children, nameNode, eqTok, valueNode)
		} else {
			valueNode, ok := csharpRecoverQueryExpressionNodeFromRange(source, itemStart, itemEnd, lang, arena)
			if !ok {
				return nil, false
			}
			children = append(children, valueNode)
		}
		if i < len(items)-1 {
			commaPos := csharpFindCommaBetween(source, span[1], items[i+1][0])
			if commaPos == 0 && source[span[1]] != ',' {
				return nil, false
			}
			if commaPos == 0 {
				commaPos = span[1]
			}
			commaTok, ok := csharpBuildLeafNodeByName(arena, source, lang, ",", commaPos, commaPos+1)
			if !ok {
				return nil, false
			}
			children = append(children, commaTok)
		}
	}
	children = append(children, closeTok)
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, children, nil, 0), true
}

func csharpBuildLambdaExpressionNode(arena *nodeArena, source []byte, lang *Language, start, arrowPos, end uint32) (*Node, bool) {
	paramStart, paramEnd, ok := csharpScanIdentifierAt(source, start)
	if !ok {
		return nil, false
	}
	paramSym, ok := symbolByName(lang, "implicit_parameter")
	if !ok {
		return nil, false
	}
	paramNamed := int(paramSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[paramSym].Named
	paramNode := newLeafNodeInArena(arena, paramSym, paramNamed, paramStart, paramEnd, advancePointByBytes(Point{}, source[:paramStart]), advancePointByBytes(Point{}, source[:paramEnd]))
	bodyNode, ok := csharpRecoverQueryExpressionNodeFromRange(source, arrowPos+2, end, lang, arena)
	if !ok {
		return nil, false
	}
	sym, ok := symbolByName(lang, "lambda_expression")
	if !ok {
		return nil, false
	}
	arrowTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "=>", arrowPos, arrowPos+2)
	if !ok {
		return nil, false
	}
	parametersID, _ := lang.FieldByName("parameters")
	bodyID, _ := lang.FieldByName("body")
	fields := csharpFieldIDsInArena(arena, []FieldID{parametersID, 0, bodyID})
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, []*Node{paramNode, arrowTok, bodyNode}, fields, 0), true
}

func csharpBuildStringLiteralNode(arena *nodeArena, source []byte, lang *Language, start, end uint32) (*Node, bool) {
	sym, ok := symbolByName(lang, "string_literal")
	if !ok {
		return nil, false
	}
	openTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "\"", start, start+1)
	if !ok {
		return nil, false
	}
	contentSym, ok := symbolByName(lang, "string_literal_content")
	if !ok {
		return nil, false
	}
	contentNamed := int(contentSym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[contentSym].Named
	content := newLeafNodeInArena(arena, contentSym, contentNamed, start+1, end-1, advancePointByBytes(Point{}, source[:start+1]), advancePointByBytes(Point{}, source[:end-1]))
	closeTok, ok := csharpBuildLeafNodeByName(arena, source, lang, "\"", end-1, end)
	if !ok {
		return nil, false
	}
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newParentNodeInArena(arena, sym, named, []*Node{openTok, content, closeTok}, nil, 0), true
}

func csharpBuildLeafNodeByName(arena *nodeArena, source []byte, lang *Language, name string, start, end uint32) (*Node, bool) {
	sym, ok := symbolByName(lang, name)
	if !ok {
		return nil, false
	}
	named := int(sym) < len(lang.SymbolMetadata) && lang.SymbolMetadata[sym].Named
	return newLeafNodeInArena(arena, sym, named, start, end, advancePointByBytes(Point{}, source[:start]), advancePointByBytes(Point{}, source[:end])), true
}
