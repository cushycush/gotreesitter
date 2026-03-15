package gotreesitter

import "time"

func (p *Parser) tryTokenInvariantLeafEdit(source []byte, oldTree *Tree, ts TokenSource, timing *incrementalParseTiming) (*Tree, bool) {
	if p == nil || oldTree == nil || oldTree.RootNode() == nil || oldTree.language != p.language {
		return nil, false
	}
	if len(oldTree.edits) != 1 || languageUsesExternalScannerCheckpoints(p.language) {
		return nil, false
	}
	edit := oldTree.edits[0]
	if edit.NewEndByte-edit.StartByte != edit.OldEndByte-edit.StartByte {
		return nil, false
	}
	if edit.NewEndPoint != edit.OldEndPoint || edit.OldEndByte <= edit.StartByte {
		return nil, false
	}
	if len(source) != len(oldTree.source) {
		return nil, false
	}
	root := oldTree.RootNode()
	leaf := root.DescendantForByteRange(edit.StartByte, edit.OldEndByte)
	if leaf == nil || leaf.ChildCount() != 0 || leaf.hasError || leaf.isMissing || leaf.isExtra {
		return nil, false
	}
	stateful, ok := ts.(parserStateTokenSource)
	if !ok {
		return nil, false
	}
	start := time.Time{}
	if timing != nil {
		start = time.Now()
	}
	stateful.SetParserState(leaf.preGotoState)
	stateful.SetGLRStates(nil)
	var tok Token
	if skipper, ok := ts.(PointSkippableTokenSource); ok {
		tok = skipper.SkipToByteWithPoint(leaf.startByte, leaf.startPoint)
	} else if skipper, ok := ts.(ByteSkippableTokenSource); ok {
		tok = skipper.SkipToByte(leaf.startByte)
	} else {
		return nil, false
	}
	if tok.Symbol != leaf.symbol || tok.StartByte != leaf.startByte || tok.EndByte != leaf.endByte {
		return nil, false
	}
	tree := reuseTreeWithNewSource(oldTree, source)
	if tree == nil || tree.root == nil {
		return nil, false
	}
	tree.setParseRuntime(ParseRuntime{
		StopReason:       ParseStopAccepted,
		SourceLen:        uint32(len(source)),
		TokensConsumed:   1,
		LastTokenEndByte: tok.EndByte,
		LastTokenSymbol:  tok.Symbol,
		ExpectedEOFByte:  uint32(len(source)),
		RootEndByte:      tree.root.EndByte(),
		MaxStacksSeen:    1,
	})
	if timing != nil {
		timing.reuseNanos += time.Since(start).Nanoseconds()
		timing.reusedSubtrees++
		timing.reusedBytes += uint64(len(source))
		timing.maxStacksSeen = 1
		timing.stopReason = ParseStopAccepted
		timing.tokensConsumed = 1
		timing.lastTokenEndByte = tok.EndByte
		timing.expectedEOFByte = uint32(len(source))
		timing.singleStackIterations = 1
		timing.singleStackTokens = 1
	}
	return tree, true
}

func reuseTreeWithNewSource(oldTree *Tree, source []byte) *Tree {
	if oldTree == nil || oldTree.root == nil {
		return nil
	}
	borrowed := make([]*nodeArena, 0, len(oldTree.borrowedArena)+1)
	if oldTree.arena != nil {
		oldTree.arena.Retain()
		borrowed = append(borrowed, oldTree.arena)
	}
	for _, a := range oldTree.borrowedArena {
		if a == nil {
			continue
		}
		a.Retain()
		borrowed = append(borrowed, a)
	}
	clearDirtyFlags(oldTree.root)
	return &Tree{
		root:          oldTree.root,
		source:        source,
		language:      oldTree.language,
		borrowedArena: uniqueArenas(borrowed, nil),
	}
}

func clearDirtyFlags(root *Node) {
	if root == nil {
		return
	}
	stack := []*Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n.dirty = false
		for i := len(n.children) - 1; i >= 0; i-- {
			if child := n.children[i]; child != nil {
				stack = append(stack, child)
			}
		}
	}
}
