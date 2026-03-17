package grammargen

import (
	"strings"
	"testing"
)

func TestTranspileDingoLet(t *testing.T) {
	source := []byte(`package main

func main() {
	let x = 42
	_ = x
}
`)
	goCode, err := TranspileDingo(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "x := 42") {
		t.Error("expected x := 42")
	}
	if strings.Contains(goCode, "let") {
		t.Error("should not contain 'let'")
	}
}

func TestTranspileDingoEnum(t *testing.T) {
	source := []byte(`package main

enum Color {
	Red,
	Green,
	Blue(int),
}
`)
	goCode, err := TranspileDingo(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "type Color struct") {
		t.Error("expected Color struct")
	}
	if !strings.Contains(goCode, "ColorRed") {
		t.Error("expected ColorRed constant")
	}
	if !strings.Contains(goCode, "func Blue(") {
		t.Error("expected Blue constructor")
	}
}

func TestTranspileDingoNullCoalesce(t *testing.T) {
	source := []byte(`package main

func f() {
	x := val ?? "default"
	_ = x
}
`)
	goCode, err := TranspileDingo(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "!= nil") {
		t.Error("expected nil check")
	}
}
