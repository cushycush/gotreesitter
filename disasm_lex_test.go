package gotreesitter_test

import (
	"fmt"
	"testing"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func TestDisasmLexColon(t *testing.T) {
	entries := grammars.AllLanguages()
	var entry grammars.LangEntry
	for _, e := range entries {
		if e.Name == "disassembly" {
			entry = e
			break
		}
	}
	lang := entry.Language()

	// Try lexing ":" at every lex state
	for ls := uint32(0); ls < uint32(len(lang.LexStates)); ls++ {
		lexer := gotreesitter.NewLexer(lang.LexStates, []byte(":"))
		tok := lexer.Next(ls)
		if tok.Symbol != 0 {
			fmt.Printf("  colon: lexState=%d → sym %d %q text=%q\n", ls, tok.Symbol, lang.SymbolNames[tok.Symbol], tok.Text)
		}
	}

	// Check _new_line
	fmt.Println("\n--- newline ---")
	for ls := uint32(0); ls < uint32(len(lang.LexStates)); ls++ {
		lexer := gotreesitter.NewLexer(lang.LexStates, []byte("\n"))
		tok := lexer.Next(ls)
		if tok.Symbol != 0 {
			fmt.Printf("  newline: lexState=%d → sym %d %q text=%q\n", ls, tok.Symbol, lang.SymbolNames[tok.Symbol], tok.Text)
		}
	}
	
	fmt.Printf("\ntotal lex states: %d\n", len(lang.LexStates))
	fmt.Printf("LexModes count: %d\n", len(lang.LexModes))
}
