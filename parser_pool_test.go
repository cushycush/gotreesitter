package gotreesitter

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestParserPoolParseConcurrent(t *testing.T) {
	lang := buildArithmeticLanguage()
	pool := NewParserPool(lang)
	src := []byte("1 + 2 + 3 + 4")

	const workers = 16
	const iters = 64

	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				tree, err := pool.Parse(src)
				if err != nil {
					errs <- err
					return
				}
				if tree == nil || tree.RootNode() == nil {
					errs <- fmt.Errorf("nil parse tree")
					return
				}
				if tree.RootNode().HasError() {
					errs <- fmt.Errorf("unexpected parse error: %s", tree.RootNode().SExpr(lang))
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent parse failed: %v", err)
	}
}

func TestParserPoolParseWithTokenSource(t *testing.T) {
	lang := buildArithmeticLanguage()
	pool := NewParserPool(lang)
	src := []byte("1+2+3")

	seedParser := NewParser(lang)
	lexer := NewLexer(lang.LexStates, src)
	ts := acquireDFATokenSource(lexer, lang, seedParser.lookupActionIndex, seedParser.hasKeywordState)

	tree, err := pool.ParseWithTokenSource(src, ts)
	if err != nil {
		t.Fatalf("ParseWithTokenSource failed: %v", err)
	}
	if tree == nil || tree.RootNode() == nil {
		t.Fatal("ParseWithTokenSource returned nil tree")
	}
	if tree.RootNode().HasError() {
		t.Fatalf("expected parse without errors, got: %s", tree.RootNode().SExpr(lang))
	}
}

func TestParserPoolAppliesLoggerOption(t *testing.T) {
	lang := buildArithmeticLanguage()
	var logCount atomic.Int64
	pool := NewParserPool(
		lang,
		WithParserPoolLogger(func(kind ParserLogType, msg string) {
			logCount.Add(1)
		}),
	)

	if _, err := pool.Parse([]byte("1+2")); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if logCount.Load() == 0 {
		t.Fatal("expected parser logger to receive at least one log")
	}
}
