package grammarlsp

import "testing"

func TestSourceMapIdentity(t *testing.T) {
	sm := NewSourceMap()
	sm.AddLineMapping(0, 0)
	sm.AddLineMapping(5, 5)
	sm.Build()

	dst := sm.ToDst(Position{Line: 3, Col: 10})
	if dst.Line != 3 || dst.Col != 10 {
		t.Errorf("identity map: got %+v", dst)
	}
	src := sm.ToSrc(Position{Line: 3, Col: 10})
	if src.Line != 3 || src.Col != 10 {
		t.Errorf("reverse: got %+v", src)
	}
}

func TestSourceMapOffset(t *testing.T) {
	sm := NewSourceMap()
	// Source lines 0-3 -> dest lines 0-3 (1:1)
	for i := 0; i < 4; i++ {
		sm.AddLineMapping(i, i)
	}
	// Source line 4 -> dest line 4 (enum expansion: 4 src lines -> 15 dest lines)
	sm.AddLineMapping(4, 4)
	// Source line 8 -> dest line 19
	sm.AddLineMapping(8, 19)
	sm.Build()

	// Dest line 12 (inside expansion) -> source line 4
	src := sm.ToSrc(Position{Line: 12, Col: 0})
	if src.Line != 4 {
		t.Errorf("expansion map: expected src 4, got %d", src.Line)
	}

	// Source line 9 -> dest line 20
	dst := sm.ToDst(Position{Line: 9, Col: 0})
	if dst.Line != 20 {
		t.Errorf("post-expansion: expected dst 20, got %d", dst.Line)
	}
}

func TestDocumentManagerOpenClose(t *testing.T) {
	ext := Extension{
		Name:          "test",
		FileExtension: ".test",
		Transpile: func(source []byte) (string, error) {
			return string(source), nil // passthrough
		},
	}
	dm := NewDocumentManager(t.TempDir(), []Extension{ext})

	uri := "file:///test/hello.test"
	err := dm.Open(uri, "package main\n")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	doc, ok := dm.Get(uri)
	if !ok {
		t.Fatal("not found")
	}
	if doc.Source != "package main\n" {
		t.Error("wrong source")
	}

	dm.Close(uri)
	if _, ok := dm.Get(uri); ok {
		t.Error("still exists after close")
	}
}

func TestDocumentManagerRoutesByExtension(t *testing.T) {
	ext1 := Extension{Name: "a", FileExtension: ".aaa", Transpile: func(s []byte) (string, error) { return "aaa", nil }}
	ext2 := Extension{Name: "b", FileExtension: ".bbb", Transpile: func(s []byte) (string, error) { return "bbb", nil }}
	dm := NewDocumentManager(t.TempDir(), []Extension{ext1, ext2})

	if !dm.IsManaged("file:///x.aaa") {
		t.Error("should manage .aaa")
	}
	if !dm.IsManaged("file:///x.bbb") {
		t.Error("should manage .bbb")
	}
	if dm.IsManaged("file:///x.go") {
		t.Error("should not manage .go")
	}

	dm.Open("file:///x.aaa", "src")
	doc, _ := dm.Get("file:///x.aaa")
	if doc.GoCode != "aaa" {
		t.Errorf("expected aaa, got %s", doc.GoCode)
	}
}
