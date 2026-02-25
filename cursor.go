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
