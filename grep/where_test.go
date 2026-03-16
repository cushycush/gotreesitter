package grep

import (
	"testing"
)

// --------------------------------------------------------------------------
// CompileWhere — basic constraint parsing
// --------------------------------------------------------------------------

func TestCompileWhere_EmptyClause(t *testing.T) {
	filter, err := CompileWhere("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty where clause should match everything.
	r := &Result{Captures: map[string]Capture{}}
	if !filter(r, nil, nil) {
		t.Error("empty where clause should match everything")
	}
}

func TestCompileWhere_Contains(t *testing.T) {
	filter, err := CompileWhere(`contains($NAME, "Test")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should match when capture contains "Test".
	r := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("TestFunction")},
		},
	}
	if !filter(r, nil, nil) {
		t.Error("expected filter to match TestFunction")
	}

	// Should not match when capture does not contain "Test".
	r2 := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("myFunction")},
		},
	}
	if filter(r2, nil, nil) {
		t.Error("expected filter to NOT match myFunction")
	}
}

func TestCompileWhere_NotContains(t *testing.T) {
	filter, err := CompileWhere(`not contains($NAME, "init")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should match when capture does NOT contain "init".
	r := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("myFunction")},
		},
	}
	if !filter(r, nil, nil) {
		t.Error("expected filter to match myFunction (does not contain init)")
	}

	// Should not match when capture contains "init".
	r2 := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("initData")},
		},
	}
	if filter(r2, nil, nil) {
		t.Error("expected filter to NOT match initData")
	}
}

func TestCompileWhere_Matches(t *testing.T) {
	filter, err := CompileWhere(`matches($NAME, "^Test")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should match when capture starts with "Test".
	r := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("TestSomething")},
		},
	}
	if !filter(r, nil, nil) {
		t.Error("expected filter to match TestSomething")
	}

	// Should not match when capture does not start with "Test".
	r2 := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("myTest")},
		},
	}
	if filter(r2, nil, nil) {
		t.Error("expected filter to NOT match myTest (does not start with Test)")
	}
}

func TestCompileWhere_NotMatches(t *testing.T) {
	filter, err := CompileWhere(`not matches($NAME, "^Test")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should match when capture does NOT start with "Test".
	r := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("myFunc")},
		},
	}
	if !filter(r, nil, nil) {
		t.Error("expected filter to match myFunc")
	}

	// Should not match when capture starts with "Test".
	r2 := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("TestFunc")},
		},
	}
	if filter(r2, nil, nil) {
		t.Error("expected filter to NOT match TestFunc")
	}
}

func TestCompileWhere_MatchesWithQuotes(t *testing.T) {
	// Regex with surrounding quotes should be stripped.
	filter, err := CompileWhere(`matches($NAME, "^get[A-Z]")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("getUser")},
		},
	}
	if !filter(r, nil, nil) {
		t.Error("expected filter to match getUser")
	}
}

func TestCompileWhere_MultipleClauses(t *testing.T) {
	// Two constraints joined by semicolon — both must pass.
	filter, err := CompileWhere(`contains($NAME, "Func"); not contains($NAME, "Test")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Passes both: contains "Func" and does not contain "Test".
	r := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("myFunc")},
		},
	}
	if !filter(r, nil, nil) {
		t.Error("expected filter to match myFunc")
	}

	// Fails second: contains "Test".
	r2 := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("TestFunc")},
		},
	}
	if filter(r2, nil, nil) {
		t.Error("expected filter to NOT match TestFunc (contains Test)")
	}

	// Fails first: does not contain "Func".
	r3 := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("myHelper")},
		},
	}
	if filter(r3, nil, nil) {
		t.Error("expected filter to NOT match myHelper (does not contain Func)")
	}
}

func TestCompileWhere_MissingCapture(t *testing.T) {
	filter, err := CompileWhere(`contains($MISSING, "hello")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// When capture is not present, text is empty, so contains returns false.
	r := &Result{
		Captures: map[string]Capture{
			"NAME": {Name: "NAME", Text: []byte("test")},
		},
	}
	if filter(r, nil, nil) {
		t.Error("expected filter to NOT match when capture is missing")
	}
}

// --------------------------------------------------------------------------
// CompileWhere — error cases
// --------------------------------------------------------------------------

func TestCompileWhere_InvalidRegex(t *testing.T) {
	_, err := CompileWhere(`matches($NAME, "[invalid")`)
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestCompileWhere_UnknownConstraint(t *testing.T) {
	_, err := CompileWhere(`startsWith($NAME, "test")`)
	if err == nil {
		t.Fatal("expected error for unknown constraint")
	}
}

func TestCompileWhere_MissingArgs(t *testing.T) {
	_, err := CompileWhere(`contains($NAME)`)
	if err == nil {
		t.Fatal("expected error for missing argument")
	}
}

// --------------------------------------------------------------------------
// CompileWhere — integration with Match (Go source)
// --------------------------------------------------------------------------

func TestCompileWhere_IntegrationGoFunctions(t *testing.T) {
	lang := testLang(t, "go")
	source := []byte(`package main

func TestFoo() {}
func TestBar() {}
func helperFunc() {}
func initSetup() {}
`)

	results, err := Match(lang, `func $NAME()`, source)
	if err != nil {
		t.Fatalf("match error: %v", err)
	}

	// Filter to only Test* functions.
	filter, err := CompileWhere(`matches($NAME, "^Test")`)
	if err != nil {
		t.Fatalf("compile where error: %v", err)
	}

	var filtered []Result
	for i := range results {
		if filter(&results[i], source, lang) {
			filtered = append(filtered, results[i])
		}
	}

	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered results, got %d", len(filtered))
	}

	names := make(map[string]bool)
	for _, r := range filtered {
		names[string(r.Captures["NAME"].Text)] = true
	}
	if !names["TestFoo"] {
		t.Error("expected TestFoo in filtered results")
	}
	if !names["TestBar"] {
		t.Error("expected TestBar in filtered results")
	}
}
