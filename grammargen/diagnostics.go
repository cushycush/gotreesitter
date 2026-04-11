package grammargen

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/odvcencio/gotreesitter"
)

// buildFollowTokensFunc returns a function that, given a parser state,
// returns the terminal symbols valid in states reachable after a reduce.
// This expands lex modes so that keywords like "AS" in dockerfile can be
// recognized even when parsing inside a production like image_name where
// "AS" isn't directly valid but becomes valid after reducing.
func buildFollowTokensFunc(tables *LRTables, tokenCount int) func(int) []int {
	if tables == nil {
		return nil
	}
	// Pre-build reverse GOTO index: lhsSym → list of GOTO target states.
	// This avoids the O(stateCount) scan per reduce action that made
	// computeLexModes unusable for large grammars (C# 121K states, TS 42K).
	type gotoTarget struct{ targetState int }
	gotoIndex := make(map[int][]gotoTarget) // lhsSym → targets
	for state := 0; state < tables.StateCount; state++ {
		acts, ok := tables.ActionTable[state]
		if !ok {
			continue
		}
		for sym, actions := range acts {
			for _, act := range actions {
				if act.kind == lrShift && sym >= tokenCount {
					// This is a GOTO entry (nonterminal shift)
					gotoIndex[sym] = append(gotoIndex[sym], gotoTarget{int(act.state)})
				}
			}
		}
	}

	// Pre-build terminal sets per state for fast lookup
	stateTerminals := make(map[int][]int) // state → terminal syms
	for state := 0; state < tables.StateCount; state++ {
		acts, ok := tables.ActionTable[state]
		if !ok {
			continue
		}
		var terms []int
		for sym := range acts {
			if sym > 0 && sym < tokenCount {
				terms = append(terms, sym)
			}
		}
		if len(terms) > 0 {
			stateTerminals[state] = terms
		}
	}

	cache := make(map[int][]int)
	return func(state int) []int {
		if cached, ok := cache[state]; ok {
			return cached
		}
		seen := make(map[int]bool)
		acts, ok := tables.ActionTable[state]
		if !ok {
			cache[state] = nil
			return nil
		}
		for _, actions := range acts {
			for _, act := range actions {
				if act.kind != lrReduce {
					continue
				}
				lhsSym := int(act.lhsSym)
				if lhsSym <= 0 {
					continue
				}
				// Use pre-built GOTO index instead of scanning all states
				for _, gt := range gotoIndex[lhsSym] {
					for _, sym := range stateTerminals[gt.targetState] {
						seen[sym] = true
					}
				}
			}
		}
		result := make([]int, 0, len(seen))
		for sym := range seen {
			result = append(result, sym)
		}
		cache[state] = result
		return result
	}
}

func useForcedBroadLexFallback() bool {
	return os.Getenv("GTS_GRAMMARGEN_FORCE_BROAD_LEX") == "1"
}

// ConflictKind describes the type of LR conflict.
type ConflictKind int

const (
	ShiftReduce ConflictKind = iota
	ReduceReduce
)

// ConflictDiag describes a conflict encountered during LR table construction.
type ConflictDiag struct {
	Kind          ConflictKind
	State         int
	LookaheadSym  int
	Actions       []lrAction // the conflicting actions
	Resolution    string     // how it was resolved (or "GLR" if kept)
	IsMergedState bool       // was this state produced by LALR merging?
	MergeCount    int        // how many merge origins this state has
}

func (d *ConflictDiag) String(ng *NormalizedGrammar) string {
	var b strings.Builder
	symName := func(id int) string {
		if id >= 0 && id < len(ng.Symbols) {
			return ng.Symbols[id].Name
		}
		return fmt.Sprintf("sym_%d", id)
	}
	prodStr := func(prodIdx int) string {
		if prodIdx < 0 || prodIdx >= len(ng.Productions) {
			return fmt.Sprintf("prod_%d", prodIdx)
		}
		p := &ng.Productions[prodIdx]
		var rhs []string
		for _, s := range p.RHS {
			rhs = append(rhs, symName(s))
		}
		return fmt.Sprintf("%s → %s", symName(p.LHS), strings.Join(rhs, " "))
	}

	switch d.Kind {
	case ShiftReduce:
		fmt.Fprintf(&b, "Shift/reduce conflict in state %d on %q:\n",
			d.State, symName(d.LookaheadSym))
		for _, a := range d.Actions {
			switch a.kind {
			case lrShift:
				fmt.Fprintf(&b, "  Shift → state %d (prec %d)\n", a.state, a.prec)
			case lrReduce:
				p := &ng.Productions[int(a.prodIdx)]
				assocStr := ""
				switch p.Assoc {
				case AssocLeft:
					assocStr = ", left-associative"
				case AssocRight:
					assocStr = ", right-associative"
				}
				fmt.Fprintf(&b, "  Reduce: %s (prec %d%s)\n", prodStr(int(a.prodIdx)), p.Prec, assocStr)
			}
		}
	case ReduceReduce:
		fmt.Fprintf(&b, "Reduce/reduce conflict in state %d on %q:\n",
			d.State, symName(d.LookaheadSym))
		for _, a := range d.Actions {
			p := &ng.Productions[int(a.prodIdx)]
			fmt.Fprintf(&b, "  Reduce: %s (prec %d)\n", prodStr(int(a.prodIdx)), p.Prec)
		}
	}
	fmt.Fprintf(&b, "  Resolution: %s", d.Resolution)
	return b.String()
}

// GenerateReport holds the result of grammar generation with diagnostics.
type GenerateReport struct {
	Language        *gotreesitter.Language
	Blob            []byte
	Conflicts       []ConflictDiag
	SplitCandidates []splitCandidate
	SplitResult     *splitReport
	Warnings        []string
	SymbolCount     int
	StateCount      int
	TokenCount      int
}

// dumpSymbolsByID prints the symbol name for each comma-separated ID in
// GTS_GRAMMARGEN_DIAG_SYMBOL_IDS. Useful for cross-referencing numeric sym
// values in parser runtime traces against their generation-time names.
func dumpSymbolsByID(ng *NormalizedGrammar) {
	ids := parseDiagConflictStates(os.Getenv("GTS_GRAMMARGEN_DIAG_SYMBOL_IDS"))
	if len(ids) == 0 {
		return
	}
	for id := range ids {
		if id >= 0 && id < len(ng.Symbols) {
			info := &ng.Symbols[id]
			fmt.Printf("diag-sym: id=%d name=%q kind=%d visible=%v named=%v\n",
				id, info.Name, info.Kind, info.Visible, info.Named)
		} else {
			fmt.Printf("diag-sym: id=%d (out of range; symbolCount=%d)\n", id, len(ng.Symbols))
		}
	}
}

// dumpKernelItemsForState prints the raw core (kernel+closure) items for a
// single LR item set. Used by the build loop when the state ID matches
// GTS_GRAMMARGEN_DIAG_KERNEL_STATES. Each core item is printed as
// "prodIdx dot lookaheads" and the production is annotated with the dot
// position using `.` as a marker in the RHS.
func dumpKernelItemsForState(stateIdx int, itemSet *lrItemSet, ng *NormalizedGrammar) {
	symName := func(id int) string {
		if id >= 0 && id < len(ng.Symbols) {
			return ng.Symbols[id].Name
		}
		return fmt.Sprintf("sym_%d", id)
	}
	fmt.Printf("diag-kernel-items: state=%d cores=%d\n", stateIdx, len(itemSet.cores))
	for i, ce := range itemSet.cores {
		if int(ce.prodIdx) < 0 || int(ce.prodIdx) >= len(ng.Productions) {
			fmt.Printf("  [%d] prodIdx=%d (out of range)\n", i, int(ce.prodIdx))
			continue
		}
		p := &ng.Productions[int(ce.prodIdx)]
		parts := make([]string, 0, len(p.RHS)+1)
		for j, s := range p.RHS {
			if j == int(ce.dot) {
				parts = append(parts, ".")
			}
			parts = append(parts, symName(s))
		}
		if int(ce.dot) >= len(p.RHS) {
			parts = append(parts, ".")
		}
		var lookaheadNames []string
		ce.lookaheads.forEach(func(la int) {
			if len(lookaheadNames) < 16 {
				lookaheadNames = append(lookaheadNames, symName(la))
			}
		})
		laStr := strings.Join(lookaheadNames, ",")
		if len(lookaheadNames) == 16 {
			laStr += ",..."
		}
		fmt.Printf("  [%d] prodIdx=%d dot=%d  %s → %s  la={%s}\n",
			i, int(ce.prodIdx), int(ce.dot), symName(p.LHS), strings.Join(parts, " "), laStr)
	}
}

// dumpProductionsBySubstr prints every production whose LHS symbol name
// contains any of the substrings in GTS_GRAMMARGEN_DIAG_DUMP_PRODUCTION_LHS.
// Runs once per call site (e.g. from resolveConflicts top). Useful for
// understanding what alternatives an auxiliary nonterminal was expanded to.
func dumpProductionsBySubstr(ng *NormalizedGrammar) {
	substrs := parseDiagConflictSubstrs(os.Getenv("GTS_GRAMMARGEN_DIAG_DUMP_PRODUCTION_LHS"))
	if len(substrs) == 0 {
		return
	}
	symName := func(id int) string {
		if id >= 0 && id < len(ng.Symbols) {
			return ng.Symbols[id].Name
		}
		return fmt.Sprintf("sym_%d", id)
	}
	seen := make(map[string]bool)
	for idx := range ng.Productions {
		p := &ng.Productions[idx]
		if p.LHS < 0 || p.LHS >= len(ng.Symbols) {
			continue
		}
		name := strings.ToLower(ng.Symbols[p.LHS].Name)
		matched := false
		for _, s := range substrs {
			if strings.Contains(name, s) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		key := ng.Symbols[p.LHS].Name
		if !seen[key] {
			seen[key] = true
			fmt.Printf("diag-production-lhs: %s\n", key)
		}
		rhs := make([]string, len(p.RHS))
		for i, s := range p.RHS {
			rhs[i] = symName(s)
		}
		fmt.Printf("  [prodIdx=%d prec=%d assoc=%d] %s → %s\n",
			idx, p.Prec, p.Assoc, key, strings.Join(rhs, " "))
	}
}

// parseDiagConflictStates parses a comma-separated list of state IDs from
// the GTS_GRAMMARGEN_DIAG_CONFLICT_STATES env var into a set.
func parseDiagConflictStates(raw string) map[int]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := make(map[int]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(part, "%d", &n); err == nil {
			out[n] = true
		}
	}
	return out
}

// dumpConflictStateIfRequestedSingle prints the raw action table for a
// state when any of these match:
//   - state ID in GTS_GRAMMARGEN_DIAG_CONFLICT_STATES (comma-separated ids)
//   - any conflict lookahead symbol name contains a substring in
//     GTS_GRAMMARGEN_DIAG_CONFLICT_LOOKAHEAD_SUBSTR
//   - any conflict action's production LHS (reduce) or shift LHS contains a
//     substring in GTS_GRAMMARGEN_DIAG_CONFLICT_PRODUCTION_SUBSTR
//
// This is the per-state entry point used by resolveConflictsForState, which
// runs inline during LR table construction. Filters are AND-combined with
// state IDs (state IDs are ORed against the substring matches).
func dumpConflictStateIfRequestedSingle(state int, actions map[int][]lrAction, ng *NormalizedGrammar) {
	dumpStates := parseDiagConflictStates(os.Getenv("GTS_GRAMMARGEN_DIAG_CONFLICT_STATES"))
	laSubstrs := parseDiagConflictSubstrs(os.Getenv("GTS_GRAMMARGEN_DIAG_CONFLICT_LOOKAHEAD_SUBSTR"))
	prodSubstrs := parseDiagConflictSubstrs(os.Getenv("GTS_GRAMMARGEN_DIAG_CONFLICT_PRODUCTION_SUBSTR"))
	if !dumpStates[state] && len(laSubstrs) == 0 && len(prodSubstrs) == 0 {
		return
	}
	matched := dumpStates[state]
	if !matched && len(laSubstrs) > 0 {
		for sym, acts := range actions {
			if len(acts) < 2 {
				continue
			}
			if sym >= 0 && sym < len(ng.Symbols) {
				name := strings.ToLower(ng.Symbols[sym].Name)
				for _, s := range laSubstrs {
					if strings.Contains(name, s) {
						matched = true
						break
					}
				}
			}
			if matched {
				break
			}
		}
	}
	if !matched && len(prodSubstrs) > 0 {
		for _, acts := range actions {
			if len(acts) < 2 {
				continue
			}
			for _, a := range acts {
				var name string
				switch a.kind {
				case lrShift:
					if int(a.lhsSym) >= 0 && int(a.lhsSym) < len(ng.Symbols) {
						name = strings.ToLower(ng.Symbols[int(a.lhsSym)].Name)
					}
				case lrReduce:
					p := &ng.Productions[int(a.prodIdx)]
					if p.LHS >= 0 && p.LHS < len(ng.Symbols) {
						name = strings.ToLower(ng.Symbols[p.LHS].Name)
					}
				}
				for _, s := range prodSubstrs {
					if strings.Contains(name, s) {
						matched = true
						break
					}
				}
				if matched {
					break
				}
			}
			if matched {
				break
			}
		}
	}
	if !matched {
		return
	}
	symName, prodStr := makeSymPrintersForDiag(ng)
	fmt.Printf("diag-conflict-state: state=%d (per-state)\n", state)
	printConflictStateActions(actions, symName, prodStr, ng)
}

// parseDiagConflictSubstrs parses comma-separated substrings, lowercased.
func parseDiagConflictSubstrs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// makeSymPrintersForDiag builds symbol and production formatter closures.
func makeSymPrintersForDiag(ng *NormalizedGrammar) (func(int) string, func(int) string) {
	symName := func(id int) string {
		if id >= 0 && id < len(ng.Symbols) {
			return ng.Symbols[id].Name
		}
		return fmt.Sprintf("sym_%d", id)
	}
	prodStr := func(prodIdx int) string {
		if prodIdx < 0 || prodIdx >= len(ng.Productions) {
			return fmt.Sprintf("prod_%d", prodIdx)
		}
		p := &ng.Productions[prodIdx]
		rhs := make([]string, len(p.RHS))
		for i, s := range p.RHS {
			rhs[i] = symName(s)
		}
		return fmt.Sprintf("%s → %s", symName(p.LHS), strings.Join(rhs, " "))
	}
	return symName, prodStr
}

// printConflictStateActions prints action entries for a state. By default
// only multi-action (conflict) entries are shown; set
// GTS_GRAMMARGEN_DIAG_CONFLICT_ALL=1 to print singleton actions too.
func printConflictStateActions(actions map[int][]lrAction, symName func(int) string, prodStr func(int) string, ng *NormalizedGrammar) {
	showSingletons := os.Getenv("GTS_GRAMMARGEN_DIAG_CONFLICT_ALL") == "1"
	syms := make([]int, 0, len(actions))
	for sym := range actions {
		syms = append(syms, sym)
	}
	sort.Ints(syms)
	for _, sym := range syms {
		acts := actions[sym]
		if len(acts) < 2 && !showSingletons {
			continue
		}
		fmt.Printf("  sym=%d(%s) actions=%d:\n", sym, symName(sym), len(acts))
		for i, a := range acts {
			switch a.kind {
			case lrShift:
				lhsName := symName(int(a.lhsSym))
				extraLhs := ""
				if len(a.lhsSyms) > 0 {
					names := make([]string, len(a.lhsSyms))
					for j, s := range a.lhsSyms {
						names[j] = symName(int(s))
					}
					extraLhs = " lhsSyms=[" + strings.Join(names, ",") + "]"
				}
				fmt.Printf("    [%d] SHIFT → state %d (prec=%d, assoc=%d, lhs=%s%s)\n",
					i, int(a.state), a.prec, a.assoc, lhsName, extraLhs)
			case lrReduce:
				prod := &ng.Productions[int(a.prodIdx)]
				fmt.Printf("    [%d] REDUCE %s (prodIdx=%d, prec=%d, assoc=%d)\n",
					i, prodStr(int(a.prodIdx)), int(a.prodIdx), prod.Prec, prod.Assoc)
			}
		}
	}
}

// dumpConflictStatesIfRequested prints the raw, pre-resolution action table
// (with full symbol names, production RHS, precedence, and LHS info) for any
// states listed in GTS_GRAMMARGEN_DIAG_CONFLICT_STATES. It's called before
// conflict resolution so the reader sees the competing actions before
// precedence/associativity collapses them. No-op if the env var is unset.
func dumpConflictStatesIfRequested(tables *LRTables, ng *NormalizedGrammar, prov *mergeProvenance) {
	dumpStates := parseDiagConflictStates(os.Getenv("GTS_GRAMMARGEN_DIAG_CONFLICT_STATES"))
	if len(dumpStates) == 0 {
		return
	}
	symName := func(id int) string {
		if id >= 0 && id < len(ng.Symbols) {
			return ng.Symbols[id].Name
		}
		return fmt.Sprintf("sym_%d", id)
	}
	prodStr := func(prodIdx int) string {
		if prodIdx < 0 || prodIdx >= len(ng.Productions) {
			return fmt.Sprintf("prod_%d", prodIdx)
		}
		p := &ng.Productions[prodIdx]
		rhs := make([]string, len(p.RHS))
		for i, s := range p.RHS {
			rhs[i] = symName(s)
		}
		return fmt.Sprintf("%s → %s", symName(p.LHS), strings.Join(rhs, " "))
	}
	for state := range dumpStates {
		actions, ok := tables.ActionTable[state]
		if !ok {
			fmt.Printf("diag-conflict-state: state=%d NOT FOUND in action table\n", state)
			continue
		}
		mergeInfo := "canonical"
		if prov != nil && prov.isMerged(state) {
			mergeInfo = fmt.Sprintf("merged(origins=%d)", len(prov.origins(state)))
		}
		fmt.Printf("diag-conflict-state: state=%d %s\n", state, mergeInfo)
		syms := make([]int, 0, len(actions))
		for sym := range actions {
			syms = append(syms, sym)
		}
		sort.Ints(syms)
		for _, sym := range syms {
			acts := actions[sym]
			// Print all syms with >=1 action (not just conflicts — the dump
			// happens BEFORE resolution, but for conflict investigation we
			// want to see competing alternatives AND singletons for the
			// same state so the reader understands the context).
			if len(acts) < 2 {
				continue
			}
			fmt.Printf("  sym=%d(%s) actions=%d:\n", sym, symName(sym), len(acts))
			for i, a := range acts {
				switch a.kind {
				case lrShift:
					lhsName := symName(int(a.lhsSym))
					extraLhs := ""
					if len(a.lhsSyms) > 0 {
						names := make([]string, len(a.lhsSyms))
						for j, s := range a.lhsSyms {
							names[j] = symName(int(s))
						}
						extraLhs = " lhsSyms=[" + strings.Join(names, ",") + "]"
					}
					fmt.Printf("    [%d] SHIFT → state %d (prec=%d, assoc=%d, lhs=%s%s)\n",
						i, int(a.state), a.prec, a.assoc, lhsName, extraLhs)
				case lrReduce:
					prod := &ng.Productions[int(a.prodIdx)]
					fmt.Printf("    [%d] REDUCE %s (prodIdx=%d, prec=%d, assoc=%d)\n",
						i, prodStr(int(a.prodIdx)), int(a.prodIdx), prod.Prec, prod.Assoc)
				}
			}
		}
	}
}

// resolveConflictsWithDiag is like resolveConflicts but collects diagnostics.
func resolveConflictsWithDiag(tables *LRTables, ng *NormalizedGrammar, prov *mergeProvenance) ([]ConflictDiag, error) {
	var diags []ConflictDiag

	dumpConflictStatesIfRequested(tables, ng, prov)

	// Sort states and syms for deterministic conflict resolution order.
	states := make([]int, 0, len(tables.ActionTable))
	for state := range tables.ActionTable {
		states = append(states, state)
	}
	sort.Ints(states)

	for _, state := range states {
		actions := tables.ActionTable[state]
		syms := make([]int, 0, len(actions))
		for sym := range actions {
			syms = append(syms, sym)
		}
		sort.Ints(syms)
		for _, sym := range syms {
			acts := actions[sym]
			if len(acts) <= 1 {
				continue
			}

			diag := ConflictDiag{
				State:        state,
				LookaheadSym: sym,
				Actions:      append([]lrAction{}, acts...),
			}

			if prov != nil {
				diag.IsMergedState = prov.isMerged(state)
				diag.MergeCount = len(prov.origins(state))
			}

			// Classify conflict.
			hasShift, hasReduce := false, false
			for _, a := range acts {
				if a.kind == lrShift {
					hasShift = true
				}
				if a.kind == lrReduce {
					hasReduce = true
				}
			}
			if hasShift && hasReduce {
				diag.Kind = ShiftReduce
			} else {
				diag.Kind = ReduceReduce
			}

			resolved, err := resolveActionConflict(sym, acts, ng)
			if err != nil {
				return diags, fmt.Errorf("state %d, symbol %d: %w", state, sym, err)
			}
			tables.ActionTable[state][sym] = resolved

			// Determine resolution description.
			switch {
			case len(resolved) > 1:
				diag.Resolution = "GLR (multiple actions kept)"
			case len(resolved) == 1 && resolved[0].kind == lrShift:
				diag.Resolution = "shift wins"
				if hasReduce {
					for _, a := range acts {
						if a.kind == lrReduce {
							p := &ng.Productions[a.prodIdx]
							if p.Prec > 0 || resolved[0].prec > 0 {
								diag.Resolution = fmt.Sprintf("shift wins (prec %d > %d)", resolved[0].prec, p.Prec)
							} else if p.Assoc == AssocRight {
								diag.Resolution = "shift wins (right-associative)"
							} else {
								diag.Resolution = "shift wins (default yacc behavior)"
							}
							break
						}
					}
				}
			case len(resolved) == 1 && resolved[0].kind == lrReduce:
				prod := &ng.Productions[resolved[0].prodIdx]
				if prod.Assoc == AssocLeft {
					diag.Resolution = "reduce wins (left-associative)"
				} else {
					diag.Resolution = fmt.Sprintf("reduce wins (prec %d)", prod.Prec)
				}
			case len(resolved) == 0:
				diag.Resolution = "error (non-associative)"
			}

			diags = append(diags, diag)
		}
	}
	return diags, nil
}

// Validate checks the grammar for common issues and returns warnings.
func Validate(g *Grammar) []string {
	var warnings []string

	if len(g.RuleOrder) == 0 {
		warnings = append(warnings, "grammar has no rules defined")
		return warnings
	}

	// Check for undefined symbol references.
	defined := make(map[string]bool)
	for _, name := range g.RuleOrder {
		defined[name] = true
	}
	// External symbols are also valid references.
	for _, ext := range g.Externals {
		if ext.Kind == RuleSymbol && ext.Value != "" {
			defined[ext.Value] = true
		}
	}
	for _, name := range g.RuleOrder {
		refs := collectSymbolRefs(g.Rules[name])
		for _, ref := range refs {
			if !defined[ref] {
				warnings = append(warnings, fmt.Sprintf("rule %q references undefined symbol %q", name, ref))
			}
		}
	}

	// Check for unreachable rules (not reachable from start symbol).
	reachable := make(map[string]bool)
	var walk func(name string)
	walk = func(name string) {
		if reachable[name] {
			return
		}
		reachable[name] = true
		if rule, ok := g.Rules[name]; ok {
			for _, ref := range collectSymbolRefs(rule) {
				walk(ref)
			}
		}
	}
	walk(g.RuleOrder[0]) // start from start symbol
	// Extras and externals can reference rules too.
	for _, extra := range g.Extras {
		for _, ref := range collectSymbolRefs(extra) {
			walk(ref)
		}
	}
	for _, ext := range g.Externals {
		for _, ref := range collectSymbolRefs(ext) {
			walk(ref)
		}
	}
	for _, name := range g.RuleOrder {
		if !reachable[name] {
			warnings = append(warnings, fmt.Sprintf("rule %q is unreachable from start symbol %q", name, g.RuleOrder[0]))
		}
	}

	// Check for empty choice alternatives.
	for _, name := range g.RuleOrder {
		checkEmptyChoice(g.Rules[name], name, &warnings)
	}

	// Check conflicts reference existing rules.
	for i, group := range g.Conflicts {
		for _, sym := range group {
			if !defined[sym] {
				warnings = append(warnings, fmt.Sprintf("conflict group %d references undefined rule %q", i, sym))
			}
		}
	}

	// Check supertypes reference existing rules.
	for _, st := range g.Supertypes {
		if !defined[st] {
			warnings = append(warnings, fmt.Sprintf("supertype %q is not a defined rule", st))
		}
	}

	// Check word token is defined.
	if g.Word != "" && !defined[g.Word] {
		warnings = append(warnings, fmt.Sprintf("word token %q is not a defined rule", g.Word))
	}

	return warnings
}

// collectSymbolRefs returns all symbol references in a rule tree.
func collectSymbolRefs(r *Rule) []string {
	if r == nil {
		return nil
	}
	var refs []string
	if r.Kind == RuleSymbol {
		refs = append(refs, r.Value)
	}
	for _, child := range r.Children {
		refs = append(refs, collectSymbolRefs(child)...)
	}
	return refs
}

// checkEmptyChoice warns about choice rules with blank alternatives
// that might indicate a mistake (usually Optional should be used instead).
func checkEmptyChoice(r *Rule, ruleName string, warnings *[]string) {
	if r == nil {
		return
	}
	for _, child := range r.Children {
		checkEmptyChoice(child, ruleName, warnings)
	}
}

// RunTests generates the grammar and runs all embedded test cases.
// Returns nil if all tests pass, or an error describing failures.
func RunTests(g *Grammar) error {
	if len(g.Tests) == 0 {
		return nil
	}

	lang, err := GenerateLanguage(g)
	if err != nil {
		return fmt.Errorf("generate failed: %w", err)
	}

	var failures []string
	for _, tc := range g.Tests {
		parser := gotreesitter.NewParser(lang)
		tree, err := parser.Parse([]byte(tc.Input))
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: parse error: %v", tc.Name, err))
			continue
		}

		sexp := tree.RootNode().SExpr(lang)
		hasError := strings.Contains(sexp, "ERROR")

		if tc.ExpectError {
			if !hasError {
				failures = append(failures, fmt.Sprintf("%s: expected ERROR nodes but got: %s", tc.Name, sexp))
			}
			continue
		}

		if hasError {
			failures = append(failures, fmt.Sprintf("%s: unexpected ERROR in tree: %s", tc.Name, sexp))
			continue
		}

		if tc.Expected != "" && sexp != tc.Expected {
			failures = append(failures, fmt.Sprintf("%s: tree mismatch\n  got:      %s\n  expected: %s", tc.Name, sexp, tc.Expected))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("%d test(s) failed:\n%s", len(failures), strings.Join(failures, "\n"))
	}
	return nil
}

type reportBuildOptions struct {
	includeDiagnostics bool
	includeLanguage    bool
	includeBlob        bool
}

func generateWithReport(g *Grammar, opts reportBuildOptions) (*GenerateReport, error) {
	return generateWithReportCtx(context.Background(), g, opts)
}

// GenerateWithReport compiles a grammar and returns a full diagnostic report.
func GenerateWithReport(g *Grammar) (*GenerateReport, error) {
	return generateWithReport(g, reportBuildOptions{
		includeDiagnostics: true,
		includeLanguage:    true,
		includeBlob:        true,
	})
}

// generateWithReportCtx is like generateWithReport but threads a context
// through LR table construction for cancellation support. When the context
// is cancelled, the LR builder aborts promptly and returns an error.
func generateWithReportCtx(bgCtx context.Context, g *Grammar, opts reportBuildOptions) (*GenerateReport, error) {
	report := &GenerateReport{}

	report.Warnings = Validate(g)

	ng, err := Normalize(g)
	if err != nil {
		return nil, fmt.Errorf("normalize: %w", err)
	}
	dumpProductionsBySubstr(ng)
	dumpSymbolsByID(ng)

	needDiagnostics := opts.includeDiagnostics || g.EnableLRSplitting
	tables, lrCtx, err := buildLRTablesInternal(bgCtx, ng, needDiagnostics)
	if err != nil {
		return nil, fmt.Errorf("build LR tables: %w", err)
	}
	prov := lrCtx.provenance

	if needDiagnostics {
		diags, err := resolveConflictsWithDiag(tables, ng, prov)
		if err != nil {
			return nil, fmt.Errorf("resolve conflicts: %w", err)
		}
		if opts.includeDiagnostics {
			report.Conflicts = diags
		}

		var splitCandidates []splitCandidate
		if opts.includeDiagnostics || g.EnableLRSplitting {
			splitCandidates = newSplitOracle(diags, prov, tables, ng).candidates()
			if opts.includeDiagnostics {
				report.SplitCandidates = splitCandidates
			}
		}

		if len(splitCandidates) > 0 && g.EnableLRSplitting {
			glrBefore := 0
			for _, d := range diags {
				if d.Resolution == "GLR (multiple actions kept)" {
					glrBefore++
				}
			}

			extTokenCandidates := 0
			for _, c := range splitCandidates {
				if c.reason == "hidden external token in merged LALR state" {
					extTokenCandidates++
				}
			}

			sr := &splitReport{CandidatesFound: len(splitCandidates)}
			sr.ConflictsBefore = len(diags)
			statesBefore := tables.StateCount
			splitCount, splitErr := localLR1Rebuild(tables, ng, lrCtx, splitCandidates, 200)
			sr.StatesSplit = splitCount
			sr.NewStatesAdded = tables.StateCount - statesBefore
			sr.Error = splitErr

			diagsAfter, _ := resolveConflictsWithDiag(tables, ng, prov)
			sr.ConflictsAfter = len(diagsAfter)

			glrAfter := 0
			for _, d := range diagsAfter {
				if d.Resolution == "GLR (multiple actions kept)" {
					glrAfter++
				}
			}
			sr.GLRBefore = glrBefore
			sr.GLRAfter = glrAfter

			if os.Getenv("GTS_GRAMMARGEN_DIAG_LR_SPLIT") == "1" {
				fmt.Printf("lr-split: grammar=%s candidates=%d states_split=%d new_states=%d conflicts=%d→%d glr=%d→%d err=%v\n",
					g.Name, len(splitCandidates), splitCount, tables.StateCount-statesBefore,
					len(diags), len(diagsAfter), glrBefore, glrAfter, splitErr)
			}

			keepSplit := glrAfter < glrBefore || len(diagsAfter) < len(diags) ||
				(extTokenCandidates > 0 && splitCount > 0)

			if !keepSplit {
				tables, err = buildLRTables(ng)
				if err != nil {
					return nil, fmt.Errorf("rebuild LR tables after split rollback: %w", err)
				}
				if err := resolveConflicts(tables, ng); err != nil {
					return nil, fmt.Errorf("resolve conflicts after split rollback: %w", err)
				}
				sr.StatesSplit = 0
				sr.NewStatesAdded = 0
				sr.ConflictsAfter = sr.ConflictsBefore
				sr.Error = fmt.Errorf("rollback: conflicts %d -> %d, GLR conflicts %d -> %d (not reduced)",
					len(diags), len(diagsAfter), glrBefore, glrAfter)
			} else if opts.includeDiagnostics {
				report.Conflicts = diagsAfter
				report.SplitCandidates = newSplitOracle(diagsAfter, prov, tables, ng).candidates()
			}
			if opts.includeDiagnostics {
				report.SplitResult = sr
			}
		}
	} else {
		if err := resolveConflicts(tables, ng); err != nil {
			return nil, fmt.Errorf("resolve conflicts: %w", err)
		}
	}

	addNonterminalExtraChains(tables, ng, lrCtx)

	lrCtx.releaseScratch()
	prov = nil
	lrCtx = nil

	report.SymbolCount = len(ng.Symbols)
	report.StateCount = tables.StateCount + 1
	report.TokenCount = ng.TokenCount()

	if !opts.includeLanguage {
		return report, nil
	}

	tokenCount := ng.TokenCount()
	immediateTokens := make(map[int]bool)
	for _, t := range ng.Terminals {
		if t.Immediate {
			immediateTokens[t.SymbolID] = true
		}
	}

	keywordSet := make(map[int]bool, len(ng.KeywordSymbols))
	for _, ks := range ng.KeywordSymbols {
		keywordSet[ks] = true
	}
	stringPrefixExtensions := computeStringPrefixExtensions(ng.Terminals)
	termPatSyms := terminalPatternSymSet(ng)

	var lexModes []lexModeSpec
	var stateToMode []int
	var afterWSModes []afterWSModeEntry
	if useForcedBroadLexFallback() {
		// Escape hatch only. The broad DFA is much faster to build for huge
		// grammars, but it is not parser-correct for languages that rely on
		// stateful contextual lexing such as C# and COBOL.
		allSyms := make(map[int]bool)
		for _, t := range ng.Terminals {
			allSyms[t.SymbolID] = true
		}
		for _, e := range ng.ExtraSymbols {
			if e > 0 && e < tokenCount {
				allSyms[e] = true
			}
		}
		lexModes = []lexModeSpec{{validSymbols: allSyms, skipWhitespace: true}}
		stateToMode = make([]int, tables.StateCount)
	} else {
		lexModes, stateToMode, afterWSModes = computeLexModes(
			tables.StateCount,
			tokenCount,
			func(state, sym int) bool {
				if acts, ok := tables.ActionTable[state]; ok {
					if entry, ok := acts[sym]; ok && len(entry) > 0 {
						return true
					}
				}
				return false
			},
			stringPrefixExtensions,
			ng.ExtraSymbols,
			tables.ExtraChainStateStart,
			immediateTokens,
			ng.ExternalSymbols,
			ng.WordSymbolID,
			keywordSet,
			termPatSyms,
			buildFollowTokensFunc(tables, tokenCount),
			patternImmediateTokenSet(ng),
		)
	}

	skipExtras := computeSkipExtras(ng)
	lexStates, lexModeOffsets, err := buildLexDFA(bgCtx, ng.Terminals, ng.ExtraSymbols, skipExtras, lexModes)
	if err != nil {
		return nil, fmt.Errorf("build lex DFA: %w", err)
	}

	var keywordLexStates []gotreesitter.LexState
	if len(ng.KeywordEntries) > 0 {
		kls, _, err := buildLexDFA(bgCtx, ng.KeywordEntries, nil, nil, []lexModeSpec{{
			validSymbols:   allSymbolsSet(ng.KeywordEntries),
			skipWhitespace: false,
		}})
		if err != nil {
			return nil, fmt.Errorf("build keyword DFA: %w", err)
		}
		keywordLexStates = kls
	}

	lang, err := assemble(ng, tables, lexStates, stateToMode, lexModeOffsets)
	if err != nil {
		return nil, fmt.Errorf("assemble: %w", err)
	}
	lang.Name = g.Name

	// Set after-whitespace lex states for states that need IMMTOKEN exclusion.
	for _, entry := range afterWSModes {
		if entry.stateIdx < len(lang.LexModes) && entry.modeIdx < len(lexModeOffsets) {
			lang.LexModes[entry.stateIdx].AfterWhitespaceLexState = uint32(lexModeOffsets[entry.modeIdx])
		}
	}

	if len(keywordLexStates) > 0 {
		lang.KeywordLexStates = keywordLexStates
		lang.KeywordCaptureToken = gotreesitter.Symbol(ng.WordSymbolID)
	}

	report.Language = lang
	report.SymbolCount = int(lang.SymbolCount)
	report.StateCount = int(lang.StateCount)
	report.TokenCount = int(lang.TokenCount)

	if !opts.includeBlob {
		return report, nil
	}

	blob, err := encodeLanguageBlob(lang)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	report.Blob = blob

	return report, nil
}

// generateDiagnosticsReport runs the report pipeline but skips lex/assemble/blob
// work. It is intended for large-grammar diagnostic/perf tests that only need
// conflicts, split metadata, warnings, and final table counts.
func generateDiagnosticsReport(g *Grammar) (*GenerateReport, error) {
	return generateWithReport(g, reportBuildOptions{includeDiagnostics: true})
}
