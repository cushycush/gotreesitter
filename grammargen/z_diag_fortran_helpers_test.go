package grammargen

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func loadFortranDiagGrammar(t *testing.T) *Grammar {
	t.Helper()

	pg := lookupParityGrammarByName("fortran")
	if pg == nil {
		t.Fatal("fortran not found")
	}
	gram, err := importParityGrammarSource(*pg)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	applyFortranDiagEnvOptions(t, gram)
	return gram
}

func applyFortranDiagEnvOptions(t *testing.T, gram *Grammar) {
	t.Helper()

	if os.Getenv("DIAG_FORTRAN_NO_INLINE") == "1" {
		gram.Inline = nil
		t.Log("WARNING: all inline disabled")
	} else if os.Getenv("DIAG_FORTRAN_PARTIAL_INLINE") == "1" {
		var filtered []string
		for _, name := range gram.Inline {
			if name != "_statement" {
				filtered = append(filtered, name)
			}
		}
		gram.Inline = filtered
		t.Logf("WARNING: partial inline, kept: %v", gram.Inline)
	}
	if raw := os.Getenv("DIAG_FORTRAN_SEQ_HELPER_THRESHOLD"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("parse DIAG_FORTRAN_SEQ_HELPER_THRESHOLD: %v", err)
		}
		gram.SeqChoiceHelperThreshold = v
		t.Logf("WARNING: seq-choice helper threshold=%d", v)
	}
	if names := parseDiagCSVNames(os.Getenv("DIAG_FORTRAN_SEQ_HELPER_EXCLUDE")); len(names) > 0 {
		gram.SeqChoiceHelperExclude = append(gram.SeqChoiceHelperExclude, names...)
		t.Logf("WARNING: seq-choice helper exclude=%v", gram.SeqChoiceHelperExclude)
	}
	if names := parseDiagCSVNames(os.Getenv("DIAG_FORTRAN_SEQ_HELPER_FORCE")); len(names) > 0 {
		gram.SeqChoiceHelperForce = append(gram.SeqChoiceHelperForce, names...)
		t.Logf("WARNING: seq-choice helper force=%v", gram.SeqChoiceHelperForce)
	}
	if raw := os.Getenv("DIAG_FORTRAN_CHOICE_LIFT_THRESHOLD"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("parse DIAG_FORTRAN_CHOICE_LIFT_THRESHOLD: %v", err)
		}
		gram.ChoiceLiftThreshold = v
		t.Logf("WARNING: choice-lift threshold=%d", v)
	}
	if names := parseDiagCSVNames(os.Getenv("DIAG_FORTRAN_CHOICE_LIFT_FORCE")); len(names) > 0 {
		gram.ChoiceLiftForce = append(gram.ChoiceLiftForce, names...)
		t.Logf("WARNING: choice-lift force=%v", gram.ChoiceLiftForce)
	}
	if os.Getenv("DIAG_FORTRAN_ENABLE_LR_SPLITTING") == "1" {
		gram.EnableLRSplitting = true
		t.Log("WARNING: LR splitting enabled")
	}
	if os.Getenv("DIAG_FORTRAN_BINARY_REPEAT") == "1" {
		gram.BinaryRepeatMode = true
		t.Log("WARNING: binary repeat mode enabled")
	}
}

func parseDiagCSVNames(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var names []string
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}

func parseDiagCSVSet(raw string) map[string]bool {
	names := parseDiagCSVNames(raw)
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func parseDiagCSVInts(t *testing.T, raw string) map[int]bool {
	t.Helper()

	names := parseDiagCSVNames(raw)
	if len(names) == 0 {
		return nil
	}
	set := make(map[int]bool, len(names))
	for _, name := range names {
		v, err := strconv.Atoi(name)
		if err != nil {
			t.Fatalf("parse int value %q: %v", name, err)
		}
		set[v] = true
	}
	return set
}
