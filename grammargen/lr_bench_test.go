package grammargen

import (
	"fmt"
	"testing"
	"time"
)

// BenchmarkLRTableGeneration benchmarks LR table construction on the built-in
// grammars of various sizes.
func BenchmarkLRTableGeneration(b *testing.B) {
	grammars := []struct {
		name string
		fn   func() *Grammar
	}{
		{"json", JSONGrammar},
		{"calc", CalcGrammar},
		{"ini", INIGrammar},
		{"lox", LoxGrammar},
		{"mustache", MustacheGrammar},
	}

	for _, g := range grammars {
		b.Run(g.name, func(b *testing.B) {
			gram := g.fn()
			ng, err := Normalize(gram)
			if err != nil {
				b.Fatalf("normalize: %v", err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := buildLRTables(ng)
				if err != nil {
					b.Fatalf("buildLRTables: %v", err)
				}
			}
		})
	}
}

// TestLRGenerationScaling measures generation time for grammars of known
// complexity. This is not a benchmark (it always runs) but reports timing
// for manual inspection.
func TestLRGenerationScaling(t *testing.T) {
	grammars := []struct {
		name string
		fn   func() *Grammar
	}{
		{"json", JSONGrammar},
		{"calc", CalcGrammar},
		{"ini", INIGrammar},
		{"lox", LoxGrammar},
		{"mustache", MustacheGrammar},
	}

	for _, g := range grammars {
		t.Run(g.name, func(t *testing.T) {
			gram := g.fn()
			ng, err := Normalize(gram)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}

			start := time.Now()
			tables, err := buildLRTables(ng)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("buildLRTables: %v", err)
			}

			t.Logf("%-12s: %d prods, %d symbols (%d tokens), %d states, %v",
				g.name, len(ng.Productions), len(ng.Symbols),
				ng.TokenCount(), tables.StateCount, elapsed)
		})
	}
}

// synthGrammar creates a synthetic grammar with the given number of rules and
// terminals, used for scalability testing.
func synthGrammar(numRules, numTerminals int) *Grammar {
	g := NewGrammar("synth")
	rules := make(map[string]*Rule)

	// Create terminal patterns.
	for i := 0; i < numTerminals; i++ {
		name := fmt.Sprintf("t%d", i)
		rules[name] = Pat(fmt.Sprintf("[a-z]%d", i))
	}

	// Create rules that use the terminals.
	for i := 0; i < numRules; i++ {
		name := fmt.Sprintf("rule_%d", i)
		// Each rule is a choice between several sequences of terminals.
		numAlts := 3
		if numAlts > numTerminals {
			numAlts = numTerminals
		}
		alts := make([]*Rule, numAlts)
		for j := 0; j < numAlts; j++ {
			tIdx := (i*numAlts + j) % numTerminals
			alts[j] = Seq(Sym(fmt.Sprintf("t%d", tIdx)), Sym(fmt.Sprintf("t%d", (tIdx+1)%numTerminals)))
		}
		rules[name] = Choice(alts...)
	}

	// Source rule uses all the other rules.
	srcAlts := make([]*Rule, numRules)
	for i := 0; i < numRules; i++ {
		srcAlts[i] = Sym(fmt.Sprintf("rule_%d", i))
	}
	rules["source"] = Repeat(Choice(srcAlts...))

	g.Rules = rules
	g.RuleOrder = make([]string, 0, len(rules))
	g.RuleOrder = append(g.RuleOrder, "source")
	for i := 0; i < numRules; i++ {
		g.RuleOrder = append(g.RuleOrder, fmt.Sprintf("rule_%d", i))
	}
	for i := 0; i < numTerminals; i++ {
		g.RuleOrder = append(g.RuleOrder, fmt.Sprintf("t%d", i))
	}

	return g
}

func BenchmarkLRTableScaling(b *testing.B) {
	sizes := []struct {
		rules, terminals int
	}{
		{10, 10},
		{50, 30},
		{100, 50},
		{200, 80},
	}

	for _, s := range sizes {
		name := fmt.Sprintf("r%d_t%d", s.rules, s.terminals)
		b.Run(name, func(b *testing.B) {
			gram := synthGrammar(s.rules, s.terminals)
			ng, err := Normalize(gram)
			if err != nil {
				b.Fatalf("normalize: %v", err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := buildLRTables(ng)
				if err != nil {
					b.Fatalf("buildLRTables: %v", err)
				}
			}
		})
	}
}
