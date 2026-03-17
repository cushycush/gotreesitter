package grammarlsp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.lsp.dev/jsonrpc2"
)

func (p *Proxy) handleInitialize(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var result json.RawMessage
	_, err := p.gopls.Call(ctx, "initialize", req.Params(), &result)
	if err != nil {
		return reply(ctx, nil, err)
	}
	// Patch: advertise full document sync (we need full text for transpilation)
	var initResult map[string]interface{}
	json.Unmarshal(result, &initResult)
	if caps, ok := initResult["capabilities"].(map[string]interface{}); ok {
		caps["textDocumentSync"] = 1 // Full sync
	}
	patched, _ := json.Marshal(initResult)
	return reply(ctx, json.RawMessage(patched), nil)
}

func (p *Proxy) handleDidOpen(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	uri, text, err := extractDidOpenParams(req.Params())
	if err != nil || !p.docs.IsManaged(uri) {
		return p.forward(ctx, reply, req)
	}

	if err := p.docs.Open(uri, text); err != nil {
		p.logger.Printf("transpile error on open %s: %v", uri, err)
		return p.forward(ctx, reply, req)
	}

	doc, _ := p.docs.Get(uri)
	shadowParams := rewriteDidOpen(uri, doc.ShadowPath, doc.GoCode)
	return p.gopls.Notify(ctx, "textDocument/didOpen", shadowParams)
}

func (p *Proxy) handleDidChange(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	uri, text, err := extractDidChangeText(req.Params())
	if err != nil || !p.docs.IsManaged(uri) {
		return p.forward(ctx, reply, req)
	}

	if err := p.docs.Update(uri, text); err != nil {
		p.logger.Printf("transpile error on change %s: %v", uri, err)
		return nil
	}

	doc, _ := p.docs.Get(uri)
	shadowParams := rewriteDidChange(uri, doc.ShadowPath, doc.GoCode, doc.Version)
	return p.gopls.Notify(ctx, "textDocument/didChange", shadowParams)
}

func (p *Proxy) handleDidClose(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	uri, err := extractURI(req.Params())
	if err != nil || !p.docs.IsManaged(uri) {
		return p.forward(ctx, reply, req)
	}

	doc, ok := p.docs.Get(uri)
	if ok {
		closeParams := rewriteURI(req.Params(), "file://"+doc.ShadowPath)
		p.gopls.Notify(ctx, "textDocument/didClose", closeParams)
	}
	p.docs.Close(uri)
	return nil
}

func (p *Proxy) handleCompletion(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	uri, err := extractURI(req.Params())
	if err != nil || !p.docs.IsManaged(uri) {
		return p.forward(ctx, reply, req)
	}

	doc, ok := p.docs.Get(uri)
	if !ok {
		return p.forward(ctx, reply, req)
	}

	// Map position from DSL to shadow Go
	mappedParams := p.mapRequestPosition(req.Params(), doc)

	// Get completions from gopls
	var result json.RawMessage
	_, err = p.gopls.Call(ctx, "textDocument/completion", mappedParams, &result)
	if err != nil {
		return reply(ctx, nil, err)
	}

	// Add extension-specific completions
	if doc.Extension.Completions != nil {
		line, col := extractPosition(req.Params())
		completionCtx := CompletionContext{
			Line: line, Column: col,
			Source: []byte(doc.Source),
		}
		extItems := doc.Extension.Completions(completionCtx)
		result = mergeCompletions(result, extItems)
	}

	return reply(ctx, result, nil)
}

func (p *Proxy) handleHover(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	uri, err := extractURI(req.Params())
	if err != nil || !p.docs.IsManaged(uri) {
		return p.forward(ctx, reply, req)
	}

	doc, ok := p.docs.Get(uri)
	if !ok {
		return p.forward(ctx, reply, req)
	}

	// Try extension hover first
	if doc.Extension.Hover != nil {
		line, col := extractPosition(req.Params())
		hoverText := doc.Extension.Hover(HoverContext{
			Line: line, Column: col,
			Source: []byte(doc.Source),
		})
		if hoverText != "" {
			hoverResult, _ := json.Marshal(map[string]interface{}{
				"contents": map[string]string{
					"kind":  "markdown",
					"value": hoverText,
				},
			})
			return reply(ctx, json.RawMessage(hoverResult), nil)
		}
	}

	// Fall back to gopls
	mappedParams := p.mapRequestPosition(req.Params(), doc)
	var result json.RawMessage
	_, err = p.gopls.Call(ctx, "textDocument/hover", mappedParams, &result)
	if err != nil {
		return reply(ctx, nil, err)
	}
	return reply(ctx, result, nil)
}

func (p *Proxy) handleDefinition(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	uri, err := extractURI(req.Params())
	if err != nil || !p.docs.IsManaged(uri) {
		return p.forward(ctx, reply, req)
	}

	doc, ok := p.docs.Get(uri)
	if !ok {
		return p.forward(ctx, reply, req)
	}

	mappedParams := p.mapRequestPosition(req.Params(), doc)
	var result json.RawMessage
	_, err = p.gopls.Call(ctx, "textDocument/definition", mappedParams, &result)
	if err != nil {
		return reply(ctx, nil, err)
	}

	// Map response positions back from shadow to DSL
	result = p.mapResponsePositions(result, doc)
	return reply(ctx, result, nil)
}

func (p *Proxy) handleDiagnostics(ctx context.Context, req jsonrpc2.Request) error {
	// Map diagnostics from shadow file back to DSL file
	var params struct {
		URI         string            `json:"uri"`
		Diagnostics []json.RawMessage `json:"diagnostics"`
	}
	json.Unmarshal(req.Params(), &params)

	// Find if this shadow URI belongs to a managed document
	for _, doc := range p.allDocs() {
		if strings.Contains(params.URI, doc.ShadowPath) || params.URI == "file://"+doc.ShadowPath {
			// Rewrite URI to original DSL file
			params.URI = doc.URI
			// Map diagnostic positions back
			for i, rawDiag := range params.Diagnostics {
				params.Diagnostics[i] = p.mapDiagnosticPosition(rawDiag, doc)
			}
			mapped, _ := json.Marshal(params)
			return p.client.Notify(ctx, "textDocument/publishDiagnostics", json.RawMessage(mapped))
		}
	}

	// Not a managed file — forward as-is
	return p.client.Notify(ctx, req.Method(), req.Params())
}

// allDocs returns all tracked documents (for diagnostic URI matching).
func (p *Proxy) allDocs() []*Document {
	p.docs.mu.RLock()
	defer p.docs.mu.RUnlock()
	docs := make([]*Document, 0, len(p.docs.docs))
	for _, d := range p.docs.docs {
		docs = append(docs, d)
	}
	return docs
}

// --- Helper functions ---

func extractURI(params json.RawMessage) (string, error) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", err
	}
	return p.TextDocument.URI, nil
}

func extractDidOpenParams(params json.RawMessage) (uri, text string, err error) {
	var p struct {
		TextDocument struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", "", err
	}
	return p.TextDocument.URI, p.TextDocument.Text, nil
}

func extractDidChangeText(params json.RawMessage) (uri, text string, err error) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", "", err
	}
	if len(p.ContentChanges) == 0 {
		return p.TextDocument.URI, "", fmt.Errorf("no changes")
	}
	return p.TextDocument.URI, p.ContentChanges[len(p.ContentChanges)-1].Text, nil
}

func extractPosition(params json.RawMessage) (line, col int) {
	var p struct {
		Position struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"position"`
	}
	json.Unmarshal(params, &p)
	return p.Position.Line, p.Position.Character
}

func rewriteDidOpen(origURI, shadowPath, goCode string) json.RawMessage {
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        "file://" + shadowPath,
			"languageId": "go",
			"version":    1,
			"text":       goCode,
		},
	}
	b, _ := json.Marshal(params)
	return b
}

func rewriteDidChange(origURI, shadowPath, goCode string, version int) json.RawMessage {
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":     "file://" + shadowPath,
			"version": version,
		},
		"contentChanges": []map[string]string{
			{"text": goCode},
		},
	}
	b, _ := json.Marshal(params)
	return b
}

func rewriteURI(params json.RawMessage, newURI string) json.RawMessage {
	var p map[string]interface{}
	json.Unmarshal(params, &p)
	if td, ok := p["textDocument"].(map[string]interface{}); ok {
		td["uri"] = newURI
	}
	b, _ := json.Marshal(p)
	return b
}

func (p *Proxy) mapRequestPosition(params json.RawMessage, doc *Document) json.RawMessage {
	var raw map[string]interface{}
	json.Unmarshal(params, &raw)

	// Rewrite URI to shadow
	if td, ok := raw["textDocument"].(map[string]interface{}); ok {
		td["uri"] = "file://" + doc.ShadowPath
	}

	// Map position
	if pos, ok := raw["position"].(map[string]interface{}); ok {
		line := int(pos["line"].(float64))
		col := int(pos["character"].(float64))
		mapped := doc.Map.ToDst(Position{Line: line, Col: col})
		pos["line"] = mapped.Line
		pos["character"] = mapped.Col
	}

	b, _ := json.Marshal(raw)
	return b
}

func (p *Proxy) mapResponsePositions(result json.RawMessage, doc *Document) json.RawMessage {
	// Try to map Location[] or Location responses
	var locations []struct {
		URI   string `json:"uri"`
		Range struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
			End struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"end"`
		} `json:"range"`
	}
	if json.Unmarshal(result, &locations) == nil && len(locations) > 0 {
		for i := range locations {
			if strings.Contains(locations[i].URI, doc.ShadowPath) {
				locations[i].URI = doc.URI
				start := doc.Map.ToSrc(Position{Line: locations[i].Range.Start.Line, Col: locations[i].Range.Start.Character})
				end := doc.Map.ToSrc(Position{Line: locations[i].Range.End.Line, Col: locations[i].Range.End.Character})
				locations[i].Range.Start.Line = start.Line
				locations[i].Range.Start.Character = start.Col
				locations[i].Range.End.Line = end.Line
				locations[i].Range.End.Character = end.Col
			}
		}
		b, _ := json.Marshal(locations)
		return b
	}
	return result
}

func (p *Proxy) mapDiagnosticPosition(rawDiag json.RawMessage, doc *Document) json.RawMessage {
	var diag map[string]interface{}
	json.Unmarshal(rawDiag, &diag)
	if r, ok := diag["range"].(map[string]interface{}); ok {
		if start, ok := r["start"].(map[string]interface{}); ok {
			line := int(start["line"].(float64))
			col := int(start["character"].(float64))
			mapped := doc.Map.ToSrc(Position{Line: line, Col: col})
			start["line"] = float64(mapped.Line)
			start["character"] = float64(mapped.Col)
		}
		if end, ok := r["end"].(map[string]interface{}); ok {
			line := int(end["line"].(float64))
			col := int(end["character"].(float64))
			mapped := doc.Map.ToSrc(Position{Line: line, Col: col})
			end["line"] = float64(mapped.Line)
			end["character"] = float64(mapped.Col)
		}
	}
	b, _ := json.Marshal(diag)
	return b
}

func mergeCompletions(goplsResult json.RawMessage, extItems []CompletionItem) json.RawMessage {
	if len(extItems) == 0 {
		return goplsResult
	}
	var result struct {
		IsIncomplete bool                     `json:"isIncomplete"`
		Items        []map[string]interface{} `json:"items"`
	}
	json.Unmarshal(goplsResult, &result)

	for _, item := range extItems {
		result.Items = append(result.Items, map[string]interface{}{
			"label":      item.Label,
			"kind":       item.Kind,
			"detail":     item.Detail,
			"insertText": item.InsertText,
		})
	}
	b, _ := json.Marshal(result)
	return b
}
