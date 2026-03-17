package grammargen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranspileDanmujiSimple(t *testing.T) {
	source := []byte(`package myservice_test

import "testing"

unit "arithmetic" {
	given "two numbers" {
		a := 2
		b := 3
		when "added" {
			result := a + b
			then "equals their sum" {
				expect result == 5
			}
		}
	}
}
`)

	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	// Structural checks
	if !strings.Contains(goCode, "func TestArithmetic(t *testing.T)") {
		t.Error("expected TestArithmetic function")
	}
	if !strings.Contains(goCode, "t.Run(") {
		t.Error("expected t.Run calls for given/when/then")
	}
	if !strings.Contains(goCode, "assert.Equal") {
		t.Error("expected testify assert.Equal assertion")
	}
	if !strings.Contains(goCode, `"github.com/stretchr/testify/assert"`) {
		t.Error("expected testify assert import")
	}
}

func TestTranspileDanmujiCompileAndRun(t *testing.T) {
	source := []byte(`package main_test

import "testing"

unit "basic" {
	given "a value" {
		x := 42
		then "it equals 42" {
			expect x == 42
		}
		then "it is not zero" {
			expect x != 0
		}
	}
}
`)

	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	// Write to temp dir as a test file
	tmpDir := t.TempDir()

	// Need a go.mod for the test
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "main_test.go"), []byte(goCode), 0644)

	// Add testify dependency and tidy
	goGet := exec.Command("go", "get", "github.com/stretchr/testify@latest")
	goGet.Dir = tmpDir
	if out, err := goGet.CombinedOutput(); err != nil {
		t.Fatalf("go get testify failed: %v\n%s", err, out)
	}
	goTidy := exec.Command("go", "mod", "tidy")
	goTidy.Dir = tmpDir
	if out, err := goTidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy failed: %v\n%s", err, out)
	}

	// Run go test
	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", string(out))
	if err != nil {
		t.Fatalf("go test failed: %v\n%s", err, out)
	}

	if !strings.Contains(string(out), "PASS") {
		t.Error("expected PASS in test output")
	}
}

func TestTranspileDanmujiFailingTest(t *testing.T) {
	source := []byte(`package main_test

import "testing"

unit "failing" {
	then "should fail" {
		expect 1 == 2
	}
}
`)

	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "main_test.go"), []byte(goCode), 0644)

	// Add testify dependency and tidy
	goGet := exec.Command("go", "get", "github.com/stretchr/testify@latest")
	goGet.Dir = tmpDir
	if getOut, getErr := goGet.CombinedOutput(); getErr != nil {
		t.Fatalf("go get testify failed: %v\n%s", getErr, getOut)
	}
	goTidy := exec.Command("go", "mod", "tidy")
	goTidy.Dir = tmpDir
	if tidyOut, tidyErr := goTidy.CombinedOutput(); tidyErr != nil {
		t.Fatalf("go mod tidy failed: %v\n%s", tidyErr, tidyOut)
	}

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = tmpDir
	out, _ := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", string(out))

	// This test SHOULD fail — that proves our assertions work
	if !strings.Contains(string(out), "FAIL") {
		t.Error("expected FAIL in output — the danmuji test asserts 1==2")
	}
}

func TestTranspileDanmujiMock(t *testing.T) {
	source := []byte(`package main_test

import "testing"

unit "with mock" {
	mock Repo {
		Save(name string) -> error = nil
	}
	repo := &mockRepo{}
	_ = repo.Save("alice")
	then "save was called" {
		expect repo.SaveCalls == 1
	}
}
`)

	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "main_test.go"), []byte(goCode), 0644)

	// Add testify dependency and tidy
	goGet := exec.Command("go", "get", "github.com/stretchr/testify@latest")
	goGet.Dir = tmpDir
	if getOut, getErr := goGet.CombinedOutput(); getErr != nil {
		t.Fatalf("go get testify failed: %v\n%s", getErr, getOut)
	}
	goTidy := exec.Command("go", "mod", "tidy")
	goTidy.Dir = tmpDir
	if tidyOut, tidyErr := goTidy.CombinedOutput(); tidyErr != nil {
		t.Fatalf("go mod tidy failed: %v\n%s", tidyErr, tidyOut)
	}

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", string(out))
	if err != nil {
		t.Fatalf("go test failed: %v\n%s", err, out)
	}
}

func TestTranspileDanmujiNeeds(t *testing.T) {
	source := []byte(`package myservice_test

import "testing"

integration "with database" {
	needs postgres db {
		port = 5432
	}
	then "database is ready" {
		expect db != nil
	}
}
`)

	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	// Structural checks
	if !strings.Contains(goCode, "postgres.Run") {
		t.Error("expected postgres.Run in generated code")
	}
	if !strings.Contains(goCode, "require.NoError") {
		t.Error("expected require.NoError in generated code")
	}
	if !strings.Contains(goCode, "t.Cleanup") {
		t.Error("expected t.Cleanup for container teardown")
	}
	if !strings.Contains(goCode, `"github.com/stretchr/testify/require"`) {
		t.Error("expected testify require import")
	}
}

func TestTranspileDanmujiExec(t *testing.T) {
	source := []byte(`package exec_test

import "testing"

unit "shell commands" {
	exec "echo test" {
		run "echo hello"
		expect exit_code == 0
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "exec.Command") {
		t.Error("expected exec.Command in output")
	}
}

func TestTranspileDanmujiLoad(t *testing.T) {
	source := []byte(`package load_test

import "testing"

load "api throughput" {
	rate 10
	duration 5
	target get "http://localhost:8080/health"
	then "healthy" {
		expect true
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "vegeta.Rate") {
		t.Error("expected vegeta.Rate in output")
	}
	if !strings.Contains(goCode, "vegeta.NewAttacker") {
		t.Error("expected vegeta.NewAttacker in output")
	}
	if !strings.Contains(goCode, "func TestLoadApiThroughput") {
		t.Error("expected TestLoadApiThroughput function")
	}
}

func TestTranspileDanmujiExecCompile(t *testing.T) {
	source := []byte(`package exec_test

import "testing"

unit "echo" {
	exec "hello" {
		run "echo hello"
		expect exit_code == 0
		expect stdout contains "hello"
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "exec_test.go"), []byte(goCode), 0644)

	getCmd := exec.Command("go", "get", "github.com/stretchr/testify@latest")
	getCmd.Dir = tmpDir
	if out, err := getCmd.CombinedOutput(); err != nil {
		t.Fatalf("go get testify failed: %v\n%s", err, out)
	}
	tidyCmd := exec.Command("go", "mod", "tidy")
	tidyCmd.Dir = tmpDir
	if out, err := tidyCmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy failed: %v\n%s", err, out)
	}

	runCmd := exec.Command("go", "test", "-v", "./...")
	runCmd.Dir = tmpDir
	out, err := runCmd.CombinedOutput()
	t.Logf("go test output:\n%s", string(out))
	if err != nil {
		t.Fatalf("go test failed: %v\n%s", err, out)
	}
}

func TestTranspileDanmujiBenchmark(t *testing.T) {
	source, err := os.ReadFile("testdata/benchmark.dmj")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "func BenchmarkStringConcat(b *testing.B)") {
		t.Error("expected BenchmarkStringConcat function")
	}
	if !strings.Contains(goCode, "b.ReportAllocs()") {
		t.Error("expected b.ReportAllocs()")
	}
	if !strings.Contains(goCode, "b.ResetTimer()") {
		t.Error("expected b.ResetTimer()")
	}
	if !strings.Contains(goCode, "for i := 0; i < b.N; i++") {
		t.Error("expected b.N loop")
	}
}

func TestTranspileDanmujiBenchmarkCompile(t *testing.T) {
	source := []byte(`package bench_test

import "testing"

benchmark "addition" {
	measure {
		x := 1 + 2
		_ = x
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "bench_test.go"), []byte(goCode), 0644)

	cmd := exec.Command("go", "test", "-bench=.", "-benchtime=1x", "-v", "./...")
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", string(out))
	if err != nil {
		t.Fatalf("go test failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "BenchmarkAddition") {
		t.Error("expected BenchmarkAddition in output")
	}
}

func TestTranspileDanmujiProfile(t *testing.T) {
	source := []byte(`package prof_test

import "testing"

unit "goroutine check" {
	profile routines {}
	then "no leaks" {
		expect true
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "runtime.NumGoroutine") {
		t.Error("expected runtime.NumGoroutine in output")
	}
	if !strings.Contains(goCode, "_goroutinesBefore") {
		t.Error("expected _goroutinesBefore variable")
	}
	if !strings.Contains(goCode, "defer func()") {
		t.Error("expected deferred goroutine check")
	}
}

func TestTranspileDanmujiFake(t *testing.T) {
	source := []byte(`package fake_test

import "testing"

unit "with fake" {
	fake Store {
		Get(key string) -> string {
			return "cached"
		}
	}
	s := &fakeStore{}
	then "returns cached" {
		expect s.Get("x") == "cached"
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "fakeStore") {
		t.Error("expected fakeStore struct in output")
	}
	if !strings.Contains(goCode, "type fakeStore struct{}") {
		t.Error("expected fakeStore struct definition")
	}
}

func TestTranspileDanmujiTable(t *testing.T) {
	source := []byte(`package table_test

import "testing"

unit "table driven" {
	table sums {
		| 1 | 2 | 3 |
		| 4 | 5 | 9 |
	}
	each row in sums {
		expect true
	}
}
`)
	goCode, err := TranspileDanmuji(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Transpiled Go:\n%s", goCode)

	if !strings.Contains(goCode, "range sums") {
		t.Error("expected range iteration over table")
	}
	if !strings.Contains(goCode, "sumsRow") {
		t.Error("expected sumsRow struct type")
	}
}
