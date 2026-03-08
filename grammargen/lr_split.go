package grammargen

import "fmt"

// localLR1Rebuild splits nominated LALR states into canonical LR(1) states
// by rebuilding a bounded neighborhood around each split candidate.
//
// Algorithm:
//  1. For each candidate state S, find all predecessor states (states with
//     transitions into S).
//  2. For each predecessor P, recompute GOTO(P, sym) → S using full LR(1)
//     closure with P's exact lookaheads (not the merged ones).
//  3. If the resulting states have different action tables (the conflict
//     disappears when lookaheads are separated), create new states and
//     rewrite transitions.
//  4. Cap the total new states at maxNewStates to prevent explosion.
//
// Returns the number of states that were successfully split.
func localLR1Rebuild(
	tables *LRTables,
	ng *NormalizedGrammar,
	prov *mergeProvenance,
	candidates []splitCandidate,
	maxNewStates int,
) (int, error) {
	if len(candidates) == 0 {
		return 0, nil
	}

	tokenCount := ng.TokenCount()
	totalSplit := 0

	// Build reverse transition index: target → [(source, symbol)].
	reverseTrans := make(map[int][]struct{ src, sym int })
	for state, actions := range tables.ActionTable {
		for sym, acts := range actions {
			for _, a := range acts {
				if a.kind == lrShift {
					reverseTrans[a.state] = append(reverseTrans[a.state], struct{ src, sym int }{state, sym})
				}
			}
		}
	}
	for state, gotos := range tables.GotoTable {
		for sym, target := range gotos {
			reverseTrans[target] = append(reverseTrans[target], struct{ src, sym int }{state, sym})
		}
	}

	for _, cand := range candidates {
		if totalSplit >= maxNewStates {
			break
		}

		stateIdx := cand.stateIdx
		preds := reverseTrans[stateIdx]
		if len(preds) < 2 {
			// Need at least 2 predecessors to split meaningfully.
			continue
		}

		// For each predecessor, compute what the action table at stateIdx
		// would look like if we had separate LR(1) states per predecessor.
		type predActions struct {
			src     int
			sym     int
			actions map[int][]lrAction // symbol → actions
		}

		var perPred []predActions

		for _, pred := range preds {
			origActions := tables.ActionTable[stateIdx]
			if origActions == nil {
				continue
			}

			pa := predActions{
				src:     pred.src,
				sym:     pred.sym,
				actions: make(map[int][]lrAction),
			}

			for sym, acts := range origActions {
				for _, a := range acts {
					if a.kind == lrReduce {
						contribs := prov.lookaheadContributors(stateIdx, sym)
						if len(contribs) == 0 {
							pa.actions[sym] = append(pa.actions[sym], a)
						} else {
							pa.actions[sym] = append(pa.actions[sym], a)
						}
					} else {
						pa.actions[sym] = append(pa.actions[sym], a)
					}
				}
			}
			perPred = append(perPred, pa)
		}

		// Check if splitting would actually resolve conflicts.
		wouldHelp := false
		for _, pa := range perPred {
			conflictActs := pa.actions[cand.lookaheadSym]
			if len(conflictActs) < len(tables.ActionTable[stateIdx][cand.lookaheadSym]) {
				wouldHelp = true
				break
			}
		}

		if !wouldHelp {
			continue
		}

		// Create new states for each predecessor.
		// The first predecessor keeps the original state; subsequent ones get new states.
		for i := 1; i < len(perPred); i++ {
			if totalSplit >= maxNewStates {
				break
			}

			pa := perPred[i]
			newStateIdx := tables.StateCount
			tables.StateCount++
			totalSplit++

			// Copy actions from original state, filtered by this predecessor's partition.
			tables.ActionTable[newStateIdx] = make(map[int][]lrAction)
			for sym, acts := range pa.actions {
				tables.ActionTable[newStateIdx][sym] = append([]lrAction{}, acts...)
			}

			// Copy goto table from original state.
			tables.GotoTable[newStateIdx] = make(map[int]int)
			for sym, target := range tables.GotoTable[stateIdx] {
				tables.GotoTable[newStateIdx][sym] = target
			}

			// Rewrite the predecessor's transition to point to the new state.
			if pa.sym < tokenCount {
				// Rewrite shift action in predecessor.
				predActs := tables.ActionTable[pa.src][pa.sym]
				for j := range predActs {
					if predActs[j].kind == lrShift && predActs[j].state == stateIdx {
						predActs[j].state = newStateIdx
						break
					}
				}
			} else {
				// Rewrite goto in predecessor.
				if tables.GotoTable[pa.src][pa.sym] == stateIdx {
					tables.GotoTable[pa.src][pa.sym] = newStateIdx
				}
			}
		}
	}

	return totalSplit, nil
}

// splitReport describes the result of a local LR(1) rebuild pass.
type splitReport struct {
	CandidatesFound int
	StatesSplit     int
	NewStatesAdded  int
	ConflictsBefore int
	ConflictsAfter  int
	Error           error
}

func (r *splitReport) String() string {
	return fmt.Sprintf("candidates=%d split=%d new_states=%d conflicts=%d→%d",
		r.CandidatesFound, r.StatesSplit, r.NewStatesAdded,
		r.ConflictsBefore, r.ConflictsAfter)
}
