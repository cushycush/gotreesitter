package gotreesitter

const (
	defaultGSSNodeSlabCap = 4 * 1024
	maxRetainedGSSNodes   = 256 * 1024
)

type gssNode struct {
	entry stackEntry
	prev  *gssNode
	depth int
}

// gssStack is a shared-prefix stack foundation for future GLR work.
// Cloning is O(1): clones share the same head pointer until diverging pushes.
type gssStack struct {
	head *gssNode
}

type gssScratch struct {
	slabs      []gssNodeSlab
	slabCursor int
}

type gssNodeSlab struct {
	data []gssNode
	used int
}

func newGSSStack(initial StateID, scratch *gssScratch) gssStack {
	return buildGSSStack([]stackEntry{{state: initial}}, scratch)
}

func buildGSSStack(entries []stackEntry, scratch *gssScratch) gssStack {
	var s gssStack
	for i := range entries {
		s.push(entries[i].state, entries[i].node, scratch)
	}
	return s
}

func (s gssStack) clone() gssStack {
	return s
}

func (s gssStack) len() int {
	if s.head == nil {
		return 0
	}
	return s.head.depth
}

func (s gssStack) top() stackEntry {
	if s.head == nil {
		return stackEntry{}
	}
	return s.head.entry
}

func (s gssStack) byteOffset() uint32 {
	for n := s.head; n != nil; n = n.prev {
		if n.entry.node != nil {
			return n.entry.node.endByte
		}
	}
	return 0
}

func (s *gssStack) push(state StateID, node *Node, scratch *gssScratch) {
	entry := stackEntry{state: state, node: node}
	var depth int
	if s.head != nil {
		depth = s.head.depth + 1
	} else {
		depth = 1
	}
	n := scratch.allocNode(entry, s.head, depth)
	s.head = n
}

func (s *gssStack) truncate(depth int) bool {
	if depth <= 0 {
		s.head = nil
		return true
	}
	if s.head == nil {
		return depth == 0
	}
	if depth > s.head.depth {
		return false
	}
	keep := s.head
	for keep != nil && keep.depth > depth {
		keep = keep.prev
	}
	if keep == nil || keep.depth != depth {
		return false
	}
	s.head = keep
	return true
}

func (s gssStack) materialize(dst []stackEntry) []stackEntry {
	n := s.len()
	if n == 0 {
		return dst[:0]
	}
	if cap(dst) < n {
		dst = make([]stackEntry, n)
	} else {
		dst = dst[:n]
	}
	i := n - 1
	for node := s.head; node != nil && i >= 0; node = node.prev {
		dst[i] = node.entry
		i--
	}
	if i >= 0 {
		// Corrupt depth metadata; return the traversed suffix.
		return dst[i+1:]
	}
	return dst
}

func (s *gssScratch) allocNode(entry stackEntry, prev *gssNode, depth int) *gssNode {
	if s == nil {
		return &gssNode{entry: entry, prev: prev, depth: depth}
	}
	if len(s.slabs) == 0 {
		s.slabs = append(s.slabs, gssNodeSlab{data: make([]gssNode, defaultGSSNodeSlabCap)})
		s.slabCursor = 0
	}
	if s.slabCursor < 0 || s.slabCursor >= len(s.slabs) {
		s.slabCursor = 0
	}
	for i := s.slabCursor; ; i++ {
		if i >= len(s.slabs) {
			lastCap := defaultGSSNodeSlabCap
			if len(s.slabs) > 0 {
				lastCap = len(s.slabs[len(s.slabs)-1].data)
			}
			capacity := lastCap * 2
			if capacity < defaultGSSNodeSlabCap {
				capacity = defaultGSSNodeSlabCap
			}
			s.slabs = append(s.slabs, gssNodeSlab{data: make([]gssNode, capacity)})
		}
		slab := &s.slabs[i]
		if slab.used >= len(slab.data) {
			continue
		}
		idx := slab.used
		slab.used++
		s.slabCursor = i
		n := &slab.data[idx]
		n.entry = entry
		n.prev = prev
		n.depth = depth
		return n
	}
}

func (s *gssScratch) reset() {
	if len(s.slabs) == 0 {
		return
	}
	total := 0
	for i := range s.slabs {
		total += len(s.slabs[i].data)
	}
	if total > maxRetainedGSSNodes {
		keepFrom := len(s.slabs) - 1
		retained := len(s.slabs[keepFrom].data)
		for keepFrom > 0 {
			next := retained + len(s.slabs[keepFrom-1].data)
			if next > maxRetainedGSSNodes {
				break
			}
			keepFrom--
			retained = next
		}
		if keepFrom > 0 {
			copy(s.slabs, s.slabs[keepFrom:])
			s.slabs = s.slabs[:len(s.slabs)-keepFrom]
		}
	}
	for i := range s.slabs {
		slab := &s.slabs[i]
		clear(slab.data[:slab.used])
		slab.used = 0
	}
	s.slabCursor = 0
}

func (s *glrStack) toGSS(scratch *gssScratch) gssStack {
	return buildGSSStack(s.entries, scratch)
}
