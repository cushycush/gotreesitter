package grammargen

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestDiagFortranKeywordReachability(t *testing.T) {
	if os.Getenv("DIAG_FORTRAN_KEYWORD_REACHABILITY") != "1" {
		t.Skip("set DIAG_FORTRAN_KEYWORD_REACHABILITY=1")
	}

	gram := loadFortranDiagGrammar(t)
	ng, err := Normalize(gram)
	if err != nil {
		t.Fatalf("normalize fortran: %v", err)
	}
	tables, lrCtx, err := buildLRTablesWithProvenance(ng)
	if err != nil {
		t.Fatalf("build lr tables: %v", err)
	}
	if _, err := resolveConflictsWithDiag(tables, ng, lrCtx.provenance); err != nil {
		t.Fatalf("resolve conflicts: %v", err)
	}

	tokenCount := ng.TokenCount()
	immediateTokens := make(map[int]bool)
	for _, term := range ng.Terminals {
		if term.Immediate {
			immediateTokens[term.SymbolID] = true
		}
	}
	keywordSet := make(map[int]bool, len(ng.KeywordSymbols))
	for _, sym := range ng.KeywordSymbols {
		keywordSet[sym] = true
	}
	followTokens := buildFollowTokensFunc(tables, tokenCount)
	lexModes, stateToMode, afterWSModes := computeLexModes(
		tables.StateCount,
		tokenCount,
		func(state, sym int) bool {
			if acts, ok := tables.ActionTable[state]; ok {
				return len(acts[sym]) > 0
			}
			return false
		},
		computeStringPrefixExtensions(ng.Terminals),
		ng.ExtraSymbols,
		tables.ExtraChainStateStart,
		immediateTokens,
		ng.ExternalSymbols,
		ng.WordSymbolID,
		keywordSet,
		terminalPatternSymSet(ng),
		followTokens,
		patternImmediateTokenSet(ng),
	)

	t.Logf("summary: productions=%d states=%d lex_modes=%d after_ws_modes=%d keyword_symbols=%d keyword_entries=%d word=%s",
		len(ng.Productions),
		tables.StateCount,
		len(lexModes),
		len(afterWSModes),
		len(ng.KeywordSymbols),
		len(ng.KeywordEntries),
		diagSymbolName(ng, ng.WordSymbolID),
	)

	interestingNames := parseDiagCSVNames(os.Getenv("DIAG_FORTRAN_STATE_NAMES"))
	if len(interestingNames) == 0 {
		interestingNames = []string{
			"use_statement",
			"included_items",
			"_generic_procedure",
			"defined_io_procedure",
			"defined_io_generic_spec",
			"generic_spec",
		}
	}
	stateFilter := parseDiagCSVInts(t, os.Getenv("DIAG_FORTRAN_STATE_IDS"))
	targetNames := parseDiagCSVNames(os.Getenv("DIAG_FORTRAN_TARGET_SYMBOLS"))
	if len(targetNames) == 0 {
		targetNames = []string{"write", "formatted", "assignment", "identifier"}
	}
	targetSymbolIDs := parseDiagCSVInts(t, os.Getenv("DIAG_FORTRAN_TARGET_SYMBOL_IDS"))
	stateLimit := 40
	if raw := strings.TrimSpace(os.Getenv("DIAG_FORTRAN_STATE_LIMIT")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("parse DIAG_FORTRAN_STATE_LIMIT: %v", err)
		}
		stateLimit = v
	}

	for _, name := range interestingNames {
		t.Logf("interesting symbol %q ids=%s", name, diagFormatSymbolIDs(ng, keywordSet, diagFindAllSymbols(ng, name)))
	}
	for _, name := range targetNames {
		t.Logf("target symbol %q ids=%s", name, diagFormatSymbolIDs(ng, keywordSet, diagFindAllSymbols(ng, name)))
	}

	targetKeys := append([]string(nil), targetNames...)
	targetIDs := make(map[string][]int, len(targetNames)+len(targetSymbolIDs))
	targetSymSet := make(map[int]bool)
	for _, name := range targetNames {
		targetIDs[name] = diagFindAllSymbols(ng, name)
		for _, id := range targetIDs[name] {
			targetSymSet[id] = true
		}
	}
	if len(targetSymbolIDs) > 0 {
		sortedIDs := make([]int, 0, len(targetSymbolIDs))
		for id := range targetSymbolIDs {
			sortedIDs = append(sortedIDs, id)
		}
		sort.Ints(sortedIDs)
		for _, id := range sortedIDs {
			label := fmt.Sprintf("sym:%s", diagSymbolName(ng, id))
			targetKeys = append(targetKeys, label)
			targetIDs[label] = []int{id}
			targetSymSet[id] = true
			t.Logf("target symbol id %d => %s", id, diagSymbolName(ng, id))
		}
	}
	rawTargetActions := make(map[int]map[int][]lrAction)
	for state, acts := range tables.ActionTable {
		for sym, entry := range acts {
			if !targetSymSet[sym] || len(entry) == 0 {
				continue
			}
			stateActs := rawTargetActions[state]
			if stateActs == nil {
				stateActs = make(map[int][]lrAction)
				rawTargetActions[state] = stateActs
			}
			stateActs[sym] = append([]lrAction(nil), entry...)
		}
	}

	stats := make(map[string]*fortranStateTokenStats, len(targetKeys))
	for _, name := range targetKeys {
		stats[name] = &fortranStateTokenStats{}
	}
	wordModeStates := 0
	matchedStates := 0
	loggedStates := 0

	for state := 0; state < len(lrCtx.itemSets); state++ {
		stateMatchesNames := diagStateMentionsNames(ng, &lrCtx.itemSets[state], interestingNames)
		stateMatchesID := len(stateFilter) > 0 && stateFilter[state]
		if !stateMatchesNames && !stateMatchesID {
			continue
		}
		matchedStates++

		modeIdx := 0
		if state < len(stateToMode) {
			modeIdx = stateToMode[state]
		}
		mode := lexModes[modeIdx]
		followSet := make(map[int]bool)
		for _, sym := range followTokens(state) {
			followSet[sym] = true
		}
		if mode.validSymbols[ng.WordSymbolID] {
			wordModeStates++
		}

		matchedTarget := false
		var targetSummaries []string
		for _, name := range targetKeys {
			presence := diagFortranStateTargetPresence(ng, tables, rawTargetActions[state], state, mode.validSymbols, followSet, keywordSet, targetIDs[name])
			if presence.direct {
				stats[name].directStates++
			}
			if presence.follow {
				stats[name].followStates++
			}
			if presence.lex {
				stats[name].lexStates++
			}
			if presence.direct || presence.follow || presence.lex {
				matchedTarget = true
			}
			targetSummaries = append(targetSummaries, fmt.Sprintf("%s{%s}", name, presence.String()))
		}
		if !matchedTarget && !stateMatchesID {
			continue
		}
		if !stateMatchesID && loggedStates >= stateLimit {
			continue
		}
		loggedStates++

		t.Logf("state=%d merged=%v merges=%d mode=%d word_in_mode=%v names=%v targets=%s",
			state,
			lrCtx.provenance != nil && lrCtx.provenance.isMerged(state),
			diagMergeCount(lrCtx, state),
			modeIdx,
			mode.validSymbols[ng.WordSymbolID],
			diagStateMatchedNames(ng, &lrCtx.itemSets[state], interestingNames),
			strings.Join(targetSummaries, " "),
		)
		items := diagStateInterestingItems(ng, &lrCtx.itemSets[state], interestingNames, 8)
		if len(items) == 0 && stateMatchesID {
			items = diagStateItems(ng, &lrCtx.itemSets[state], 8)
		}
		for _, item := range items {
			t.Logf("  item %s", item)
		}
	}

	t.Logf("summary: interesting_state_count=%d logged_states=%d word_mode_states=%d", matchedStates, loggedStates, wordModeStates)
	for _, name := range targetKeys {
		s := stats[name]
		t.Logf("summary: target=%s direct_states=%d follow_states=%d lex_states=%d", name, s.directStates, s.followStates, s.lexStates)
	}
}

type fortranStateTargetPresence struct {
	direct bool
	follow bool
	lex    bool
	ids    []int
	acts   []string
	raw    []string
}

func (p fortranStateTargetPresence) String() string {
	acts := "-"
	if len(p.acts) > 0 {
		acts = strings.Join(p.acts, "|")
	}
	raw := "-"
	if len(p.raw) > 0 {
		raw = strings.Join(p.raw, "|")
	}
	return fmt.Sprintf("ids=%v direct=%v follow=%v lex=%v raw=%s acts=%s", p.ids, p.direct, p.follow, p.lex, raw, acts)
}

type fortranStateTokenStats struct {
	directStates int
	followStates int
	lexStates    int
}

func diagFortranStateTargetPresence(ng *NormalizedGrammar, tables *LRTables, rawStateActs map[int][]lrAction, state int, modeSyms map[int]bool, followSet map[int]bool, keywordSet map[int]bool, ids []int) fortranStateTargetPresence {
	p := fortranStateTargetPresence{ids: append([]int(nil), ids...)}
	for _, id := range ids {
		if raw := rawStateActs[id]; len(raw) > 0 {
			p.raw = append(p.raw, fmt.Sprintf("%s=%s", diagSymbolName(ng, id), diagFormatActions(ng, raw)))
		}
		if acts, ok := tables.ActionTable[state][id]; ok && len(acts) > 0 {
			p.direct = true
			p.acts = append(p.acts, fmt.Sprintf("%s=%s", diagSymbolName(ng, id), diagFormatActions(ng, acts)))
		}
		if followSet[id] {
			p.follow = true
		}
		if modeSyms[id] || (keywordSet[id] && modeSyms[ng.WordSymbolID]) {
			p.lex = true
		}
	}
	return p
}

func diagFormatSymbolIDs(ng *NormalizedGrammar, keywordSet map[int]bool, ids []int) string {
	if len(ids) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if id < 0 || id >= len(ng.Symbols) {
			parts = append(parts, fmt.Sprintf("sym%d", id))
			continue
		}
		sym := ng.Symbols[id]
		parts = append(parts, fmt.Sprintf("%s kind=%d visible=%v named=%v keyword=%v", diagSymbolName(ng, id), sym.Kind, sym.Visible, sym.Named, keywordSet[id]))
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func diagStateMatchedNames(ng *NormalizedGrammar, set *lrItemSet, names []string) []string {
	matched := make(map[string]bool)
	for _, ce := range set.cores {
		prod := &ng.Productions[ce.prodIdx]
		if prod.LHS >= 0 && prod.LHS < len(ng.Symbols) {
			name := ng.Symbols[prod.LHS].Name
			for _, candidate := range names {
				if name == candidate {
					matched[candidate] = true
				}
			}
		}
		for _, sym := range prod.RHS {
			if sym < 0 || sym >= len(ng.Symbols) {
				continue
			}
			name := ng.Symbols[sym].Name
			for _, candidate := range names {
				if name == candidate {
					matched[candidate] = true
				}
			}
		}
	}
	out := make([]string, 0, len(matched))
	for name := range matched {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func diagStateInterestingItems(ng *NormalizedGrammar, set *lrItemSet, names []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	items := make([]string, 0, limit)
	for _, ce := range set.cores {
		prod := &ng.Productions[ce.prodIdx]
		if !diagProductionMentionsNames(ng, prod, names) {
			continue
		}
		item := diagFormatProd(ng, int(ce.prodIdx), int(ce.dot))
		items = append(items, item)
		if len(items) >= limit {
			break
		}
	}
	return items
}

func diagStateItems(ng *NormalizedGrammar, set *lrItemSet, limit int) []string {
	if limit <= 0 {
		return nil
	}
	items := make([]string, 0, limit)
	for _, ce := range set.cores {
		items = append(items, diagFormatProd(ng, int(ce.prodIdx), int(ce.dot)))
		if len(items) >= limit {
			break
		}
	}
	return items
}
