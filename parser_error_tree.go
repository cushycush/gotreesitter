package gotreesitter

import "unicode/utf8"

func parseErrorTree(source []byte, lang *Language) *Tree {
	end := Point{}
	for i := 0; i < len(source); {
		if source[i] == '\n' {
			end.Row++
			end.Column = 0
			i++
			continue
		}
		_, size := utf8.DecodeRune(source[i:])
		if size <= 0 {
			size = 1
		}
		i += size
		end.Column++
	}

	root := NewLeafNode(errorSymbol, false, 0, uint32(len(source)), Point{}, end)
	root.hasError = true
	return NewTree(root, source, lang)
}

func isWhitespaceOnlySource(source []byte) bool {
	for i := 0; i < len(source); i++ {
		switch source[i] {
		case ' ', '\t', '\n', '\r', '\f':
		default:
			return false
		}
	}
	return true
}

// extendRootToChildSpans ensures a non-error root node's byte range covers
// all of its children. When reductions produce a root whose raw span is empty
// (e.g. all children are zero-width external scanner tokens), the root endByte
// stays at 0 while children may have real spans from folded extras. C
// tree-sitter's padding representation avoids this because size accumulates
// through reduce; the Go runtime uses absolute offsets that must be explicit.
func extendRootToChildSpans(root *Node, source []byte) {
	if root == nil || root.hasError || len(source) == 0 {
		return
	}
	// Only fix empty-span roots whose children have content.
	if root.endByte > root.startByte {
		return
	}
	maxEnd := root.endByte
	var maxEndPt Point
	for _, c := range root.children {
		if c.endByte > maxEnd {
			maxEnd = c.endByte
			maxEndPt = c.endPoint
		}
	}
	if maxEnd > root.endByte {
		root.endByte = maxEnd
		root.endPoint = maxEndPt
	}
}

func extendNodeToTrailingWhitespace(n *Node, source []byte) {
	if n == nil {
		return
	}
	sourceEnd := uint32(len(source))
	if n.endByte >= sourceEnd {
		return
	}
	tail := source[n.endByte:sourceEnd]
	for i := 0; i < len(tail); i++ {
		switch tail[i] {
		case ' ', '\t', '\n', '\r', '\f':
		default:
			return
		}
	}

	pt := n.endPoint
	for i := 0; i < len(tail); {
		if tail[i] == '\n' {
			pt.Row++
			pt.Column = 0
			i++
			continue
		}
		_, size := utf8.DecodeRune(tail[i:])
		if size <= 0 {
			size = 1
		}
		i += size
		pt.Column++
	}

	n.endByte = sourceEnd
	n.endPoint = pt
}
