package grammargen

import (
	"fmt"
	"os"
	"sort"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// LexModeMismatch describes a parser state whose lex mode cannot produce
// some terminal that the action table expects as a shift target.
type LexModeMismatch struct {
	State        int      // parser state
	LexState     uint32   // DFA lex state index
	MissingSyms  []int    // terminal symbol IDs missing from lex mode
	MissingNames []string // human-readable names for missing symbols
	TotalShifts  int      // total shift actions in the state (for context)
}

// VerifyLexModeCompleteness walks every parser state in the generated
// Language and checks whether every terminal with a shift action in the
// action table is reachable from the state's lex mode DFA. It accounts
// for external scanner tokens (which are not in the DFA) and keyword
// promotion (via KeywordCaptureToken + KeywordLexStates).
//
// This exposes `computeLexModes` incompleteness bugs where a parser state
// has valid shifts for tokens the DFA can't actually produce in that
// state — causing the parser to get stuck at parse time.
//
// When GOT_DEBUG_LEXMODE is set, a summary is printed to stderr. The
// function always returns the full mismatch list for programmatic use.
func VerifyLexModeCompleteness(lang *gotreesitter.Language) []LexModeMismatch {
	if lang == nil {
		return nil
	}

	externalSet := make(map[int]bool, len(lang.ExternalSymbols))
	for _, s := range lang.ExternalSymbols {
		externalSet[int(s)] = true
	}

	// Extras are produced by the DFA but may have dedicated lex modes;
	// they're also always available so aren't "missing".
	// They'll be handled via the reachability check (the DFA will
	// include them when appropriate).

	// Build keyword-reachable set: tokens the keyword DFA can promote to.
	keywordSet := make(map[int]bool)
	if len(lang.KeywordLexStates) > 0 {
		visited := make(map[int32]bool)
		queue := []int32{0}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if visited[cur] || cur < 0 || int(cur) >= len(lang.KeywordLexStates) {
				continue
			}
			visited[cur] = true
			st := lang.KeywordLexStates[cur]
			if st.AcceptToken > 0 {
				keywordSet[int(st.AcceptToken)] = true
			}
			for _, tr := range st.Transitions {
				queue = append(queue, int32(tr.NextState))
			}
			if st.Default >= 0 {
				queue = append(queue, int32(st.Default))
			}
		}
	}
	kwCapture := int(lang.KeywordCaptureToken)

	// Cache per-lex-state reachable accept tokens.
	reachCache := make(map[uint32]map[int]bool)
	lexReach := func(startState uint32) map[int]bool {
		if cached, ok := reachCache[startState]; ok {
			return cached
		}
		reached := make(map[int]bool)
		visited := make(map[int32]bool)
		queue := []int32{int32(startState)}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if visited[cur] || cur < 0 || int(cur) >= len(lang.LexStates) {
				continue
			}
			visited[cur] = true
			st := lang.LexStates[cur]
			if st.AcceptToken > 0 {
				reached[int(st.AcceptToken)] = true
			}
			for _, tr := range st.Transitions {
				queue = append(queue, int32(tr.NextState))
			}
			if st.Default >= 0 {
				queue = append(queue, int32(st.Default))
			}
			if st.EOF >= 0 {
				queue = append(queue, int32(st.EOF))
			}
		}
		reachCache[startState] = reached
		return reached
	}

	totalStates := len(lang.LexModes)
	var mismatches []LexModeMismatch
	for state := 0; state < totalStates; state++ {
		lm := lang.LexModes[state]
		mainReach := lexReach(lm.LexState)
		var afterWSReach map[int]bool
		if lm.AfterWhitespaceLexState != 0 && lm.AfterWhitespaceLexState != lm.LexState {
			afterWSReach = lexReach(lm.AfterWhitespaceLexState)
		}

		totalShifts := 0
		var missing []int
		for sym := 1; sym < len(lang.SymbolNames); sym++ {
			if uint32(sym) >= lang.TokenCount {
				break
			}
			if externalSet[sym] {
				continue
			}
			actIdx := lookupActionIndexForLanguage(lang, gotreesitter.StateID(state), gotreesitter.Symbol(sym))
			if actIdx == 0 {
				continue
			}
			if int(actIdx) >= len(lang.ParseActions) {
				continue
			}
			hasShift := false
			for _, a := range lang.ParseActions[actIdx].Actions {
				if a.Type == gotreesitter.ParseActionShift {
					hasShift = true
					break
				}
			}
			if !hasShift {
				continue
			}
			totalShifts++

			if mainReach[sym] {
				continue
			}
			if afterWSReach != nil && afterWSReach[sym] {
				continue
			}
			// Keyword promotion via the identifier capture token.
			if kwCapture > 0 && mainReach[kwCapture] && keywordSet[sym] {
				continue
			}
			if kwCapture > 0 && afterWSReach != nil && afterWSReach[kwCapture] && keywordSet[sym] {
				continue
			}
			missing = append(missing, sym)
		}
		if len(missing) > 0 {
			sort.Ints(missing)
			names := make([]string, 0, len(missing))
			for _, sym := range missing {
				if sym < len(lang.SymbolNames) {
					names = append(names, lang.SymbolNames[sym])
				}
			}
			mismatches = append(mismatches, LexModeMismatch{
				State:        state,
				LexState:     lm.LexState,
				MissingSyms:  missing,
				MissingNames: names,
				TotalShifts:  totalShifts,
			})
		}
	}

	if os.Getenv("GOT_DEBUG_LEXMODE") == "1" {
		fmt.Fprintf(os.Stderr, "[lexmode-verify] %s: %d/%d states have mismatched lex modes\n",
			lang.Name, len(mismatches), totalStates)
		// Summarize by missing count.
		bucketCounts := map[int]int{}
		for _, m := range mismatches {
			bucketCounts[len(m.MissingSyms)]++
		}
		buckets := make([]int, 0, len(bucketCounts))
		for k := range bucketCounts {
			buckets = append(buckets, k)
		}
		sort.Ints(buckets)
		for _, k := range buckets {
			fmt.Fprintf(os.Stderr, "[lexmode-verify]   missing=%d: %d states\n", k, bucketCounts[k])
		}
		// Print top-5 worst offenders.
		sort.Slice(mismatches, func(i, j int) bool {
			return len(mismatches[i].MissingSyms) > len(mismatches[j].MissingSyms)
		})
		for i, m := range mismatches {
			if i >= 5 {
				break
			}
			sample := m.MissingNames
			if len(sample) > 10 {
				sample = append(sample[:10:10], "...")
			}
			fmt.Fprintf(os.Stderr, "[lexmode-verify]   state=%d lexState=%d missing=%d shifts=%d first=%v\n",
				m.State, m.LexState, len(m.MissingSyms), m.TotalShifts, sample)
		}
	}
	return mismatches
}
