package grammarlsp

import (
	"encoding/json"
	"testing"
)

func TestNewProxy(t *testing.T) {
	ext := Extension{
		Name:          "test",
		FileExtension: ".test",
		Transpile:     func(s []byte) (string, error) { return string(s), nil },
	}
	p := NewProxy(Config{
		GoplsPath:  "gopls",
		ShadowDir:  t.TempDir(),
		Extensions: []Extension{ext},
	})
	if p == nil {
		t.Fatal("NewProxy returned nil")
	}
	if p.docs == nil {
		t.Fatal("docs not initialized")
	}
	if !p.docs.IsManaged("file:///x.test") {
		t.Error("should manage .test files")
	}
	if p.docs.IsManaged("file:///x.go") {
		t.Error("should not manage .go files")
	}
}

func TestExtractHelpers(t *testing.T) {
	// Test extractURI
	params := []byte(`{"textDocument":{"uri":"file:///test/hello.dmj"}}`)
	uri, err := extractURI(params)
	if err != nil {
		t.Fatalf("extractURI: %v", err)
	}
	if uri != "file:///test/hello.dmj" {
		t.Errorf("got %s", uri)
	}

	// Test extractDidOpenParams
	openParams := []byte(`{"textDocument":{"uri":"file:///x.dmj","text":"package main\n"}}`)
	openURI, openText, err := extractDidOpenParams(openParams)
	if err != nil {
		t.Fatalf("extractDidOpenParams: %v", err)
	}
	if openURI != "file:///x.dmj" {
		t.Errorf("uri: %s", openURI)
	}
	if openText != "package main\n" {
		t.Errorf("text: %s", openText)
	}

	// Test extractPosition
	posParams := []byte(`{"textDocument":{"uri":"file:///x.dmj"},"position":{"line":5,"character":10}}`)
	line, col := extractPosition(posParams)
	if line != 5 || col != 10 {
		t.Errorf("position: %d:%d", line, col)
	}
}

func TestRewriteHelpers(t *testing.T) {
	result := rewriteDidOpen("file:///x.dmj", "/tmp/shadow/x.go", "package main\n")
	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	td := parsed["textDocument"].(map[string]interface{})
	if td["uri"] != "file:///tmp/shadow/x.go" {
		t.Errorf("uri: %v", td["uri"])
	}
	if td["text"] != "package main\n" {
		t.Errorf("text: %v", td["text"])
	}
}
