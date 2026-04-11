package gotreesitter

import (
	"fmt"
	"strings"
)

// InjectionResult holds parse results for a multi-language document.
type InjectionResult struct {
	// Tree is the parent language's parse tree.
	Tree *Tree
	// Injections contains child language parse results, ordered by position.
	Injections []Injection
}

// Injection is a single embedded language region.
type Injection struct {
	// Language is the detected language name (e.g., "javascript").
	Language string
	// Tree is the parse tree for this region, or nil if the language
	// was not registered.
	Tree *Tree
	// Ranges are the source ranges this tree covers.
	Ranges []Range
	// Node is the parent tree node that triggered the injection.
	Node *Node
}

// InjectionParser parses documents with embedded languages.
//
// InjectionParser is not safe for concurrent use. It caches child parsers and
// mutates shared maps during parse operations.
type InjectionParser struct {
	// languages maps language name -> Language.
	languages map[string]*Language
	// injectionQueries maps parent language name -> compiled injection query.
	injectionQueries map[string]*Query
	// parsers caches Parser instances per language for reuse.
	parsers map[string]*Parser
	// maxDepth limits nested injection recursion. Zero means use default.
	maxDepth int
	// prevResult holds the previous parse result for reuse.
	prevResult *InjectionResult
}

// NewInjectionParser creates an InjectionParser.
func NewInjectionParser() *InjectionParser {
	return &InjectionParser{
		languages:        make(map[string]*Language),
		injectionQueries: make(map[string]*Query),
		parsers:          make(map[string]*Parser),
	}
}

// RegisterLanguage adds a language that can be used as parent or child.
func (ip *InjectionParser) RegisterLanguage(name string, lang *Language) {
	ip.languages[name] = lang
}

// RegisterInjectionQuery sets the injection query for a parent language.
// The query should use @injection.content and #set! injection.language
// conventions. It is compiled against the registered parent language.
func (ip *InjectionParser) RegisterInjectionQuery(parentLang string, query string) error {
	lang, ok := ip.languages[parentLang]
	if !ok {
		return fmt.Errorf("injection: parent language %q not registered", parentLang)
	}
	q, err := NewQuery(query, lang)
	if err != nil {
		return fmt.Errorf("injection: compiling query for %q: %w", parentLang, err)
	}
	ip.injectionQueries[parentLang] = q
	return nil
}

// releaseResult releases all parse trees held by r. Safe to call with nil.
func releaseResult(r *InjectionResult) {
	if r == nil {
		return
	}
	r.Tree.Release()
	for _, inj := range r.Injections {
		if inj.Tree != nil {
			inj.Tree.Release()
		}
	}
}

// Parse parses source as parentLang, then recursively parses injected regions.
func (ip *InjectionParser) Parse(source []byte, parentLang string) (*InjectionResult, error) {
	// Release previous result to allow arena reuse.
	releaseResult(ip.prevResult)
	ip.prevResult = nil

	lang, ok := ip.languages[parentLang]
	if !ok {
		return nil, fmt.Errorf("injection: language %q not registered", parentLang)
	}

	parser := ip.getParser(parentLang, lang)
	tree, err := parser.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("injection: parsing %q: %w", parentLang, err)
	}

	injections, err := ip.findAndParseInjections(source, parentLang, tree, 0)
	if err != nil {
		return nil, err
	}

	ip.prevResult = &InjectionResult{
		Tree:       tree,
		Injections: injections,
	}

	return ip.prevResult, nil
}

// ParseIncremental re-parses after edits, reusing unchanged child trees.
func (ip *InjectionParser) ParseIncremental(source []byte, parentLang string,
	oldResult *InjectionResult) (*InjectionResult, error) {

	// Detach prevResult now; release it after parsing so that oldResult.Tree
	// (which may be the same object) remains valid throughout the parse.
	prev := ip.prevResult
	ip.prevResult = nil
	defer releaseResult(prev)

	lang, ok := ip.languages[parentLang]
	if !ok {
		return nil, fmt.Errorf("injection: language %q not registered", parentLang)
	}

	parser := ip.getParser(parentLang, lang)
	newTree, err := parser.ParseIncremental(source, oldResult.Tree)
	if err != nil {
		return nil, fmt.Errorf("injection: incremental parsing %q: %w", parentLang, err)
	}

	// Determine which ranges changed between old and new parent trees.
	changedRanges := DiffChangedRanges(oldResult.Tree, newTree)

	// Re-detect injections from the new parent tree.
	newDetected, err := ip.detectInjections(source, parentLang, newTree)
	if err != nil {
		return nil, err
	}

	// For each detected injection, check if it overlaps a changed range.
	// If not, try to reuse the old child tree.
	var injections []Injection
	for _, det := range newDetected {
		if det.Language == "" {
			injections = append(injections, det)
			continue
		}

		childLang, hasLang := ip.languages[det.Language]
		if !hasLang {
			injections = append(injections, det)
			continue
		}

		// Check if this injection's ranges overlap any changed range.
		changed := false
		for _, cr := range changedRanges {
			for _, r := range det.Ranges {
				if r.StartByte < cr.EndByte && r.EndByte > cr.StartByte {
					changed = true
					break
				}
			}
			if changed {
				break
			}
		}

		if !changed {
			// Try to reuse old child tree.
			if oldChild := ip.findOldInjection(oldResult, det.Language, det.Ranges); oldChild != nil {
				det.Tree = oldChild
				injections = append(injections, det)
				continue
			}
		}

		// Parse (or reparse) this injection region.
		childParser := ip.getParser(det.Language, childLang)
		childParser.SetIncludedRanges(det.Ranges)
		childTree, err := childParser.Parse(source)
		if err != nil {
			// If child parse fails, record injection without tree.
			injections = append(injections, det)
			continue
		}
		det.Tree = childTree
		injections = append(injections, det)
	}

	ip.prevResult = &InjectionResult{
		Tree:       newTree,
		Injections: injections,
	}

	return ip.prevResult, nil
}

// defaultMaxInjectionDepth limits recursion to prevent infinite loops.
const defaultMaxInjectionDepth = 10

// SetMaxDepth overrides the nested injection recursion limit.
// Depth values <= 0 restore the default limit.
func (ip *InjectionParser) SetMaxDepth(depth int) {
	if ip == nil {
		return
	}
	if depth <= 0 {
		ip.maxDepth = 0
		return
	}
	ip.maxDepth = depth
}

func (ip *InjectionParser) effectiveMaxDepth() int {
	if ip == nil || ip.maxDepth <= 0 {
		return defaultMaxInjectionDepth
	}
	return ip.maxDepth
}

// findAndParseInjections detects injections in tree and parses them, recursing.
func (ip *InjectionParser) findAndParseInjections(source []byte, parentLang string,
	tree *Tree, depth int) ([]Injection, error) {

	if depth >= ip.effectiveMaxDepth() {
		return nil, nil
	}

	detected, err := ip.detectInjections(source, parentLang, tree)
	if err != nil {
		return nil, err
	}

	result := make([]Injection, 0, len(detected))
	for _, det := range detected {
		if det.Language == "" {
			result = append(result, det)
			continue
		}

		childLang, ok := ip.languages[det.Language]
		if !ok {
			// Language not registered — record injection without tree.
			result = append(result, det)
			continue
		}

		childParser := ip.getParser(det.Language, childLang)

		// For single-range injections (the common case), parse only the range
		// bytes via ParseIncremental(rangeBytes, nil). This lets the parser use
		// an incremental-class arena (16 KB slab vs 2 MB for full parse), which
		// is orders of magnitude cheaper when there are many small injections.
		// Rebase the resulting tree back into document coordinates before
		// exposing it so callers still see the same byte/point space as the
		// included-range path. Multi-range injections fall back to the
		// full-source path with SetIncludedRanges because the lexer needs
		// non-contiguous byte ranges.
		var childTree *Tree
		if len(det.Ranges) == 1 {
			r := det.Ranges[0]
			if r.StartByte <= r.EndByte && int(r.EndByte) <= len(source) {
				rangeSource := source[r.StartByte:r.EndByte]
				childTree, err = childParser.ParseIncremental(rangeSource, nil)
				if err == nil && childTree != nil && !childTree.ParseStoppedEarly() {
					rebaseInjectionTree(childTree, source, r)
				} else {
					childParser.SetIncludedRanges(det.Ranges)
					childTree, err = childParser.Parse(source)
				}
			} else {
				childParser.SetIncludedRanges(det.Ranges)
				childTree, err = childParser.Parse(source)
			}
		} else {
			childParser.SetIncludedRanges(det.Ranges)
			childTree, err = childParser.Parse(source)
		}
		if err != nil {
			result = append(result, det)
			continue
		}
		det.Tree = childTree

		// Recurse: check if this child language has injection queries too.
		if _, hasQuery := ip.injectionQueries[det.Language]; hasQuery {
			nested, err := ip.findAndParseInjections(source, det.Language, childTree, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, det)
			result = append(result, nested...)
		} else {
			result = append(result, det)
		}
	}

	return result, nil
}

// detectInjections runs the injection query for parentLang against tree
// and returns detected injection regions (without parsing them).
func (ip *InjectionParser) detectInjections(source []byte, parentLang string,
	tree *Tree) ([]Injection, error) {

	q, ok := ip.injectionQueries[parentLang]
	if !ok {
		return nil, nil
	}

	lang := ip.languages[parentLang]
	root := tree.RootNode()
	if root == nil {
		return nil, nil
	}

	cursor := q.Exec(root, lang, source)

	// Collect injection regions grouped by language.
	type injectionEntry struct {
		language string
		ranges   []Range
		node     *Node
	}
	var entries []injectionEntry

	for {
		match, ok := cursor.NextMatch()
		if !ok {
			break
		}

		var contentNode *Node
		var langName string

		// Check for static language from #set! injection.language.
		if vals := match.SetValues(q, "injection.language"); len(vals) > 0 {
			langName = vals[0]
		}

		for _, cap := range match.Captures {
			switch cap.Name {
			case "injection.content":
				contentNode = cap.Node
			case "injection.language":
				// Dynamic language detection: node text is the language name.
				if langName == "" && cap.Node != nil {
					langName = strings.TrimSpace(cap.Node.Text(source))
				}
			}
		}

		if contentNode == nil {
			continue
		}

		entries = append(entries, injectionEntry{
			language: langName,
			ranges:   []Range{contentNode.Range()},
			node:     contentNode,
		})
	}

	// Convert entries to Injection structs.
	injections := make([]Injection, len(entries))
	for i, e := range entries {
		injections[i] = Injection{
			Language: e.language,
			Ranges:   e.ranges,
			Node:     e.node,
		}
	}

	return injections, nil
}

// findOldInjection searches oldResult for a matching injection by language and ranges.
func (ip *InjectionParser) findOldInjection(oldResult *InjectionResult, lang string, ranges []Range) *Tree {
	for _, old := range oldResult.Injections {
		if old.Language != lang || old.Tree == nil || len(old.Ranges) != len(ranges) {
			continue
		}
		match := true
		for i, r := range old.Ranges {
			if r != ranges[i] {
				match = false
				break
			}
		}
		if match {
			return old.Tree
		}
	}
	return nil
}

func rebaseInjectionTree(tree *Tree, source []byte, span Range) {
	if tree == nil {
		return
	}
	if tree.root != nil {
		if !shiftNodeBytes(tree.root, int64(span.StartByte)) {
			tree.root = tree.RootNodeWithOffset(span.StartByte, span.StartPoint)
		} else {
			shiftNodePoints(tree.root, span.StartPoint)
		}
	}
	tree.source = source

	rt := tree.ParseRuntime()
	rt.SourceLen = uint32(len(source))
	rt.ExpectedEOFByte = addUint32Delta(rt.ExpectedEOFByte, int64(span.StartByte))
	if rt.LastTokenEndByte != 0 {
		rt.LastTokenEndByte = addUint32Delta(rt.LastTokenEndByte, int64(span.StartByte))
	}
	if tree.root != nil {
		rt.RootEndByte = tree.root.EndByte()
	} else {
		rt.RootEndByte = span.StartByte
	}
	rt.Truncated = rt.RootEndByte < rt.ExpectedEOFByte
	tree.setParseRuntime(rt)
}

func shiftNodePoints(root *Node, offset Point) {
	if root == nil || offset == (Point{}) {
		return
	}
	baseRow := root.startPoint.Row
	stack := []*Node{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		n := stack[last]
		stack = stack[:last]

		startRow := n.startPoint.Row
		n.startPoint.Row = addUint32Delta(n.startPoint.Row, int64(offset.Row))
		if offset.Row == 0 || startRow == baseRow {
			n.startPoint.Column = addUint32Delta(n.startPoint.Column, int64(offset.Column))
		}

		endRow := n.endPoint.Row
		n.endPoint.Row = addUint32Delta(n.endPoint.Row, int64(offset.Row))
		if offset.Row == 0 || endRow == baseRow {
			n.endPoint.Column = addUint32Delta(n.endPoint.Column, int64(offset.Column))
		}

		stack = append(stack, n.children...)
	}
}

// getParser returns a cached Parser for the language, creating one if needed.
func (ip *InjectionParser) getParser(name string, lang *Language) *Parser {
	if p, ok := ip.parsers[name]; ok {
		// Reset included ranges for fresh use.
		p.SetIncludedRanges(nil)
		return p
	}
	p := NewParser(lang)
	ip.parsers[name] = p
	return p
}
