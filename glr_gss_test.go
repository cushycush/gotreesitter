package gotreesitter

import "testing"

func TestGSSStackPushCloneAndTruncate(t *testing.T) {
	var scratch gssScratch
	base := newGSSStack(1, &scratch)
	if base.len() != 1 {
		t.Fatalf("base len = %d, want 1", base.len())
	}

	clone := base.clone()
	base.push(2, nil, &scratch)
	base.push(3, nil, &scratch)

	if base.len() != 3 {
		t.Fatalf("base len after pushes = %d, want 3", base.len())
	}
	if clone.len() != 1 {
		t.Fatalf("clone len changed = %d, want 1", clone.len())
	}
	if base.top().state != 3 {
		t.Fatalf("base top state = %d, want 3", base.top().state)
	}

	ok := base.truncate(2)
	if !ok {
		t.Fatal("truncate(2) = false, want true")
	}
	if got := base.top().state; got != 2 {
		t.Fatalf("top after truncate = %d, want 2", got)
	}
}

func TestGSSStackMaterializeAndByteOffset(t *testing.T) {
	var scratch gssScratch
	n1 := &Node{endByte: 5}
	n2 := &Node{endByte: 9}

	var s gssStack
	s.push(1, nil, &scratch)
	s.push(2, n1, &scratch)
	s.push(3, nil, &scratch)
	s.push(4, n2, &scratch)

	got := s.materialize(nil)
	if len(got) != 4 {
		t.Fatalf("materialized len = %d, want 4", len(got))
	}
	if got[0].state != 1 || got[1].state != 2 || got[2].state != 3 || got[3].state != 4 {
		t.Fatalf("unexpected materialized states: %+v", got)
	}

	if off := s.byteOffset(); off != 9 {
		t.Fatalf("byteOffset = %d, want 9", off)
	}

	s.truncate(3)
	if off := s.byteOffset(); off != 5 {
		t.Fatalf("byteOffset after truncate = %d, want 5", off)
	}
}

func TestGLRStackToGSS(t *testing.T) {
	var gScratch gssScratch
	var entryScratch glrEntryScratch
	s := newGLRStackWithScratch(1, &entryScratch)
	s.push(2, nil, &entryScratch, &gScratch)
	s.push(3, nil, &entryScratch, &gScratch)

	gs := s.toGSS(&gScratch)
	mat := gs.materialize(nil)
	want := s.ensureEntries(&entryScratch)
	if len(mat) != len(want) {
		t.Fatalf("materialized len = %d, want %d", len(mat), len(want))
	}
	for i := range mat {
		if mat[i].state != want[i].state {
			t.Fatalf("state[%d] = %d, want %d", i, mat[i].state, want[i].state)
		}
	}
}

func TestGSSStackMaterializePanicsOnCorruptDepth(t *testing.T) {
	head := &gssNode{entry: stackEntry{state: 2}, depth: 3}
	head.prev = &gssNode{entry: stackEntry{state: 1}, depth: 1}
	s := gssStack{head: head}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on corrupt GSS depth metadata")
		}
	}()
	_ = s.materialize(nil)
}
