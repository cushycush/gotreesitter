package gotreesitter

// cursorFrame tracks a node and the child index within its parent.
// childIndex is -1 for the cursor root (no parent context).
type cursorFrame struct {
	node       *Node
	childIndex int
}

// TreeCursor provides stateful, O(1) tree navigation.
// It maintains a stack of (node, childIndex) frames enabling efficient
// parent, child, and sibling movement without scanning.
//
// The cursor holds pointers to Nodes. If the underlying Tree is released,
// the cursor becomes invalid (matches C tree-sitter semantics).
type TreeCursor struct {
	stack []cursorFrame
	tree  *Tree
}

// NewTreeCursor creates a cursor starting at the given node.
// The optional tree reference enables field name resolution and text extraction.
func NewTreeCursor(node *Node, tree *Tree) *TreeCursor {
	return &TreeCursor{
		stack: []cursorFrame{{node: node, childIndex: -1}},
		tree:  tree,
	}
}

// NewTreeCursorFromTree creates a cursor starting at the tree's root node.
func NewTreeCursorFromTree(tree *Tree) *TreeCursor {
	return NewTreeCursor(tree.RootNode(), tree)
}

// CurrentNode returns the node the cursor is currently pointing to.
func (c *TreeCursor) CurrentNode() *Node {
	return c.stack[len(c.stack)-1].node
}

// Depth returns the cursor's current depth (0 at the root).
func (c *TreeCursor) Depth() int {
	return len(c.stack) - 1
}

// GotoFirstChild moves the cursor to the first child of the current node.
// Returns false if the current node has no children.
func (c *TreeCursor) GotoFirstChild() bool {
	node := c.CurrentNode()
	if len(node.children) == 0 {
		return false
	}
	c.stack = append(c.stack, cursorFrame{node: node.children[0], childIndex: 0})
	return true
}

// GotoLastChild moves the cursor to the last child of the current node.
// Returns false if the current node has no children.
func (c *TreeCursor) GotoLastChild() bool {
	node := c.CurrentNode()
	n := len(node.children)
	if n == 0 {
		return false
	}
	c.stack = append(c.stack, cursorFrame{node: node.children[n-1], childIndex: n - 1})
	return true
}

// GotoNextSibling moves the cursor to the next sibling.
// Returns false if the cursor is at the root or the last sibling.
func (c *TreeCursor) GotoNextSibling() bool {
	if len(c.stack) < 2 {
		return false
	}
	frame := &c.stack[len(c.stack)-1]
	parentNode := c.stack[len(c.stack)-2].node
	next := frame.childIndex + 1
	if next >= len(parentNode.children) {
		return false
	}
	frame.childIndex = next
	frame.node = parentNode.children[next]
	return true
}

// GotoPrevSibling moves the cursor to the previous sibling.
// Returns false if the cursor is at the root or the first sibling.
func (c *TreeCursor) GotoPrevSibling() bool {
	if len(c.stack) < 2 {
		return false
	}
	frame := &c.stack[len(c.stack)-1]
	parentNode := c.stack[len(c.stack)-2].node
	prev := frame.childIndex - 1
	if prev < 0 {
		return false
	}
	frame.childIndex = prev
	frame.node = parentNode.children[prev]
	return true
}

// GotoParent moves the cursor to the parent of the current node.
// Returns false if the cursor is at the root.
func (c *TreeCursor) GotoParent() bool {
	if len(c.stack) < 2 {
		return false
	}
	c.stack = c.stack[:len(c.stack)-1]
	return true
}

// CurrentFieldID returns the field ID of the current node within its parent.
// Returns 0 if the cursor is at the root or the node has no field assignment.
func (c *TreeCursor) CurrentFieldID() FieldID {
	if len(c.stack) < 2 {
		return 0
	}
	frame := c.stack[len(c.stack)-1]
	parentNode := c.stack[len(c.stack)-2].node
	if frame.childIndex < len(parentNode.fieldIDs) {
		return parentNode.fieldIDs[frame.childIndex]
	}
	return 0
}

// CurrentFieldName returns the field name of the current node within its parent.
// Returns "" if no tree is associated, the cursor is at the root, or
// the node has no field assignment.
func (c *TreeCursor) CurrentFieldName() string {
	fid := c.CurrentFieldID()
	if fid == 0 || c.tree == nil {
		return ""
	}
	lang := c.tree.Language()
	if lang == nil || int(fid) >= len(lang.FieldNames) {
		return ""
	}
	return lang.FieldNames[fid]
}

// GotoChildByFieldID moves the cursor to the first child with the given field ID.
// Returns false if no child has that field.
func (c *TreeCursor) GotoChildByFieldID(fid FieldID) bool {
	node := c.CurrentNode()
	for i, id := range node.fieldIDs {
		if id == fid && i < len(node.children) {
			c.stack = append(c.stack, cursorFrame{node: node.children[i], childIndex: i})
			return true
		}
	}
	return false
}

// GotoChildByFieldName moves the cursor to the first child with the given field name.
// Returns false if the tree has no language, the field name is unknown, or
// no child has that field.
func (c *TreeCursor) GotoChildByFieldName(name string) bool {
	if c.tree == nil {
		return false
	}
	lang := c.tree.Language()
	if lang == nil {
		return false
	}
	fid, ok := lang.FieldByName(name)
	if !ok || fid == 0 {
		return false
	}
	return c.GotoChildByFieldID(fid)
}

// GotoFirstNamedChild moves the cursor to the first named child of the
// current node, skipping anonymous nodes. Returns false if no named child exists.
func (c *TreeCursor) GotoFirstNamedChild() bool {
	node := c.CurrentNode()
	for i, child := range node.children {
		if child.isNamed {
			c.stack = append(c.stack, cursorFrame{node: child, childIndex: i})
			return true
		}
	}
	return false
}

// GotoLastNamedChild moves the cursor to the last named child of the
// current node, skipping anonymous nodes. Returns false if no named child exists.
func (c *TreeCursor) GotoLastNamedChild() bool {
	node := c.CurrentNode()
	for i := len(node.children) - 1; i >= 0; i-- {
		if node.children[i].isNamed {
			c.stack = append(c.stack, cursorFrame{node: node.children[i], childIndex: i})
			return true
		}
	}
	return false
}

// GotoNextNamedSibling moves the cursor to the next named sibling,
// skipping anonymous nodes. Returns false if no named sibling follows.
func (c *TreeCursor) GotoNextNamedSibling() bool {
	if len(c.stack) < 2 {
		return false
	}
	frame := &c.stack[len(c.stack)-1]
	parentNode := c.stack[len(c.stack)-2].node
	for i := frame.childIndex + 1; i < len(parentNode.children); i++ {
		if parentNode.children[i].isNamed {
			frame.childIndex = i
			frame.node = parentNode.children[i]
			return true
		}
	}
	return false
}

// GotoPrevNamedSibling moves the cursor to the previous named sibling,
// skipping anonymous nodes. Returns false if no named sibling precedes.
func (c *TreeCursor) GotoPrevNamedSibling() bool {
	if len(c.stack) < 2 {
		return false
	}
	frame := &c.stack[len(c.stack)-1]
	parentNode := c.stack[len(c.stack)-2].node
	for i := frame.childIndex - 1; i >= 0; i-- {
		if parentNode.children[i].isNamed {
			frame.childIndex = i
			frame.node = parentNode.children[i]
			return true
		}
	}
	return false
}

// GotoFirstChildForByte moves the cursor to the first child whose byte range
// contains targetByte (i.e., the first child where endByte > targetByte).
// Returns false if the current node has no children or targetByte is past all children.
func (c *TreeCursor) GotoFirstChildForByte(targetByte uint32) bool {
	node := c.CurrentNode()
	for i, child := range node.children {
		if child.endByte > targetByte {
			c.stack = append(c.stack, cursorFrame{node: child, childIndex: i})
			return true
		}
	}
	return false
}

// GotoFirstChildForPoint moves the cursor to the first child whose point range
// contains targetPoint (i.e., the first child where endPoint > targetPoint).
// Returns false if the current node has no children or targetPoint is past all children.
func (c *TreeCursor) GotoFirstChildForPoint(targetPoint Point) bool {
	node := c.CurrentNode()
	for i, child := range node.children {
		ep := child.endPoint
		if ep.Row > targetPoint.Row || (ep.Row == targetPoint.Row && ep.Column > targetPoint.Column) {
			c.stack = append(c.stack, cursorFrame{node: child, childIndex: i})
			return true
		}
	}
	return false
}

// Reset resets the cursor to a new root node, clearing the navigation stack.
func (c *TreeCursor) Reset(node *Node) {
	c.stack = c.stack[:1]
	c.stack[0] = cursorFrame{node: node, childIndex: -1}
}

// ResetTree resets the cursor to the root of a new tree.
func (c *TreeCursor) ResetTree(tree *Tree) {
	c.tree = tree
	c.Reset(tree.RootNode())
}

// Copy returns an independent copy of the cursor. The copy shares the same
// tree reference but has its own navigation stack.
func (c *TreeCursor) Copy() *TreeCursor {
	newStack := make([]cursorFrame, len(c.stack))
	copy(newStack, c.stack)
	return &TreeCursor{
		stack: newStack,
		tree:  c.tree,
	}
}

// CurrentNodeType returns the type name of the current node.
// Requires a tree with a language to be associated.
func (c *TreeCursor) CurrentNodeType() string {
	if c.tree == nil {
		return ""
	}
	lang := c.tree.Language()
	if lang == nil {
		return ""
	}
	return c.CurrentNode().Type(lang)
}

// CurrentNodeText returns the source text of the current node.
// Requires a tree with source to be associated.
func (c *TreeCursor) CurrentNodeText() string {
	if c.tree == nil {
		return ""
	}
	return c.CurrentNode().Text(c.tree.Source())
}

// CurrentNodeIsNamed returns whether the current node is a named node.
func (c *TreeCursor) CurrentNodeIsNamed() bool {
	return c.CurrentNode().IsNamed()
}
