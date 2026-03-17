package grammarlsp

// Extension defines what a grammar extension provides to the LSP proxy.
// Implement this interface and register it with the proxy to get
// full IDE support for your grammar's file extension.
type Extension struct {
	// Name identifies this extension (e.g., "danmuji", "dingo")
	Name string

	// FileExtension is the file extension this handles (e.g., ".dmj", ".dingo")
	FileExtension string

	// Transpile converts source in the custom language to valid Go code.
	// This is the only required function.
	Transpile func(source []byte) (string, error)

	// Completions returns completion items for the given context. Optional.
	Completions func(ctx CompletionContext) []CompletionItem

	// Hover returns hover documentation for the given context. Optional.
	Hover func(ctx HoverContext) string

	// Diagnostics returns additional diagnostics beyond what gopls provides. Optional.
	Diagnostics func(source []byte) []Diagnostic
}

// CompletionContext provides context for completion requests.
type CompletionContext struct {
	Line   int
	Column int
	Prefix string // text before cursor on current line
	Source []byte // full document source
}

// CompletionItem is a single completion suggestion.
type CompletionItem struct {
	Label      string
	Kind       int    // LSP CompletionItemKind: 14=keyword, 7=class, 3=function
	Detail     string // short description
	InsertText string // text to insert (can be snippet)
}

// HoverContext provides context for hover requests.
type HoverContext struct {
	Line   int
	Column int
	Word   string // word under cursor
	Source []byte // full document source
}

// Diagnostic is an additional diagnostic from the extension.
type Diagnostic struct {
	Line     int
	Column   int
	EndLine  int
	EndCol   int
	Message  string
	Severity int // 1=error, 2=warning, 3=info, 4=hint
}
