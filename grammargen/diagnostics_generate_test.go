package grammargen

import (
	"context"
	"testing"
)

func TestGenerateWithReportCtxSkipsDiagnosticsWhenNotRequested(t *testing.T) {
	report, err := generateWithReportCtx(context.Background(), CalcGrammar(), reportBuildOptions{
		includeLanguage: true,
	})
	if err != nil {
		t.Fatalf("generateWithReportCtx: %v", err)
	}
	if report.Language == nil {
		t.Fatal("report.Language is nil")
	}
	if len(report.Conflicts) != 0 {
		t.Fatalf("report.Conflicts = %d, want 0", len(report.Conflicts))
	}
	if len(report.SplitCandidates) != 0 {
		t.Fatalf("report.SplitCandidates = %d, want 0", len(report.SplitCandidates))
	}
	if report.SplitResult != nil {
		t.Fatalf("report.SplitResult = %#v, want nil", report.SplitResult)
	}
	if len(report.Blob) != 0 {
		t.Fatalf("report.Blob len = %d, want 0", len(report.Blob))
	}
}
