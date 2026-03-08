package grammargen

import "testing"

func TestMergeProvenanceRecordsMerge(t *testing.T) {
	prov := newMergeProvenance()
	prov.recordFresh(5)
	if prov.isMerged(5) {
		t.Fatal("fresh state should not be merged")
	}

	prov.recordMerge(5, mergeOrigin{
		kernelHash:  0xABCD,
		sourceState: -1,
	})
	if !prov.isMerged(5) {
		t.Fatal("state with merge should report isMerged=true")
	}

	origins := prov.origins(5)
	if len(origins) != 1 {
		t.Fatalf("expected 1 origin, got %d", len(origins))
	}
	if origins[0].kernelHash != 0xABCD {
		t.Fatalf("expected kernelHash 0xABCD, got %x", origins[0].kernelHash)
	}
}

func TestMergeProvenanceMultipleMerges(t *testing.T) {
	prov := newMergeProvenance()
	prov.recordFresh(10)
	prov.recordMerge(10, mergeOrigin{kernelHash: 0x1111})
	prov.recordMerge(10, mergeOrigin{kernelHash: 0x2222})
	prov.recordMerge(10, mergeOrigin{kernelHash: 0x3333})

	origins := prov.origins(10)
	if len(origins) != 3 {
		t.Fatalf("expected 3 origins, got %d", len(origins))
	}
}

func TestMergeProvenanceLookaheadContributors(t *testing.T) {
	prov := newMergeProvenance()
	prov.recordFresh(7)

	prov.recordLookaheadContributor(7, 3, 42)
	prov.recordLookaheadContributor(7, 3, 55)

	contribs := prov.lookaheadContributors(7, 3)
	if len(contribs) != 2 {
		t.Fatalf("expected 2 contributors, got %d", len(contribs))
	}
}

func TestMergeProvenanceMergedStateCount(t *testing.T) {
	prov := newMergeProvenance()
	prov.recordFresh(0)
	prov.recordFresh(1)
	prov.recordFresh(2)
	prov.recordMerge(1, mergeOrigin{kernelHash: 0xAAAA})
	prov.recordMerge(2, mergeOrigin{kernelHash: 0xBBBB})

	if prov.mergedStateCount() != 2 {
		t.Fatalf("expected 2 merged states, got %d", prov.mergedStateCount())
	}
}

func TestMergeProvenanceNoContributors(t *testing.T) {
	prov := newMergeProvenance()
	contribs := prov.lookaheadContributors(99, 42)
	if len(contribs) != 0 {
		t.Fatalf("expected 0 contributors for unknown state, got %d", len(contribs))
	}
}
