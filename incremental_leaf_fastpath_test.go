package gotreesitter

import (
	"reflect"
	"testing"
)

func TestSnapshotTokenSourceStateUnsupportedType(t *testing.T) {
	ts := &stubTokenSource{}
	restore, ok := snapshotTokenSourceState(ts)
	if ok {
		restore()
		t.Fatal("snapshotTokenSourceState unexpected success for non-wrapped DFA token source")
	}
}

func TestSnapshotTokenSourceStateRestoresDFATokenSource(t *testing.T) {
	original := &dfaTokenSource{
		state:                      42,
		glrStates:                  []StateID{1, 2},
		lexer:                      &Lexer{pos: 13, row: 7, col: 4},
		externalValid:              []bool{true, false},
		extZeroTried:               []bool{false, true},
		externalTokenStart:         []byte{0x11, 0x22},
		externalTokenEnd:           []byte{0x33},
		externalSnapshot:           []byte{0x44, 0x55},
		externalRetrySnap:          []byte{0x66},
		externalCompare:            []byte{0x77},
		externalLexer:              ExternalLexer{startPoint: Point{Row: 3, Column: 4}, endPoint: Point{Row: 3, Column: 5}},
		externalRetryLexer:         ExternalLexer{startPoint: Point{Row: 7, Column: 8}, endPoint: Point{Row: 7, Column: 9}},
		lastExternalTokenStartByte: 88,
		lastExternalTokenEndByte:   99,
		lastExternalTokenValid:     true,
		extZeroPos:                 11,
		extZeroState:               12,
		zeroWidthPos:               21,
		zeroWidthCount:             34,
	}
	restore, ok := snapshotTokenSourceState(original)
	if !ok {
		t.Fatal("snapshotTokenSourceState returned false for *dfaTokenSource")
	}

	original.state = 77
	original.glrStates = append(original.glrStates[:0], 9, 8, 7)
	original.lexer = &Lexer{pos: 99, row: 10, col: 11}
	original.externalValid = []bool{false, false, true}
	original.extZeroTried = []bool{true}
	original.externalTokenStart = []byte{0xee}
	original.externalTokenEnd = nil
	original.externalSnapshot = []byte{0xaa}
	original.externalRetrySnap = nil
	original.externalCompare = []byte{0xbb, 0xcc}
	original.externalLexer = ExternalLexer{startPoint: Point{Row: 9, Column: 9}, endPoint: Point{Row: 10, Column: 10}}
	original.externalRetryLexer = ExternalLexer{startPoint: Point{Row: 5, Column: 6}, endPoint: Point{Row: 6, Column: 7}}
	original.lastExternalTokenStartByte = 101
	original.lastExternalTokenEndByte = 202
	original.lastExternalTokenValid = false
	original.extZeroPos = 121
	original.extZeroState = 131
	original.zeroWidthPos = 141
	original.zeroWidthCount = 151

	restore()

	if original.state != 42 {
		t.Fatalf("state restored to %d, want %d", original.state, 42)
	}
	if !reflect.DeepEqual(original.glrStates, []StateID{1, 2}) {
		t.Fatalf("glrStates = %v, want %v", original.glrStates, []StateID{1, 2})
	}
	if original.lexer.pos != 13 || original.lexer.row != 7 || original.lexer.col != 4 {
		t.Fatalf("lexer = %#v, want pos=13 row=7 col=4", original.lexer)
	}
	if !reflect.DeepEqual(original.externalValid, []bool{true, false}) {
		t.Fatalf("externalValid = %v, want %v", original.externalValid, []bool{true, false})
	}
	if !reflect.DeepEqual(original.extZeroTried, []bool{false, true}) {
		t.Fatalf("extZeroTried = %v, want %v", original.extZeroTried, []bool{false, true})
	}
	if !reflect.DeepEqual(original.externalTokenStart, []byte{0x11, 0x22}) {
		t.Fatalf("externalTokenStart = %v, want %v", original.externalTokenStart, []byte{0x11, 0x22})
	}
	if !reflect.DeepEqual(original.externalTokenEnd, []byte{0x33}) {
		t.Fatalf("externalTokenEnd = %v, want %v", original.externalTokenEnd, []byte{0x33})
	}
	if !reflect.DeepEqual(original.externalSnapshot, []byte{0x44, 0x55}) {
		t.Fatalf("externalSnapshot = %v, want %v", original.externalSnapshot, []byte{0x44, 0x55})
	}
	if !reflect.DeepEqual(original.externalRetrySnap, []byte{0x66}) {
		t.Fatalf("externalRetrySnap = %v, want %v", original.externalRetrySnap, []byte{0x66})
	}
	if !reflect.DeepEqual(original.externalCompare, []byte{0x77}) {
		t.Fatalf("externalCompare = %v, want %v", original.externalCompare, []byte{0x77})
	}
	if got, want := original.externalLexer.startPoint, (Point{Row: 3, Column: 4}); got != want {
		t.Fatalf("externalLexer.startPoint = %v, want %v", got, want)
	}
	if got, want := original.externalRetryLexer.startPoint, (Point{Row: 7, Column: 8}); got != want {
		t.Fatalf("externalRetryLexer.startPoint = %v, want %v", got, want)
	}
	if original.lastExternalTokenStartByte != 88 || original.lastExternalTokenEndByte != 99 {
		t.Fatalf("external token bytes = [%d, %d], want [88, 99]", original.lastExternalTokenStartByte, original.lastExternalTokenEndByte)
	}
	if !original.lastExternalTokenValid {
		t.Fatal("lastExternalTokenValid false, want true")
	}
	if original.extZeroPos != 11 || original.extZeroState != 12 {
		t.Fatalf("extZeroPos = %d extZeroState = %d, want 11/12", original.extZeroPos, original.extZeroState)
	}
	if original.zeroWidthPos != 21 || original.zeroWidthCount != 34 {
		t.Fatalf("zeroWidthPos = %d zeroWidthCount = %d, want 21/34", original.zeroWidthPos, original.zeroWidthCount)
	}
}

func TestSnapshotTokenSourceStateSupportsIncludedRangeWrapper(t *testing.T) {
	underlying := &dfaTokenSource{
		state: 50,
		lexer: &Lexer{pos: 3},
	}
	parsed := &includedRangeTokenSource{
		base:   underlying,
		ranges: []Range{{StartByte: 1, EndByte: 2}},
		idx:    4,
	}

	restore, ok := snapshotTokenSourceState(parsed)
	if !ok {
		t.Fatal("snapshotTokenSourceState returned false for included range token source")
	}

	underlying.state = 51
	underlying.lexer = &Lexer{pos: 9}
	parsed.idx = 9

	restore()

	if parsed.idx != 4 {
		t.Fatalf("included range idx = %d, want %d", parsed.idx, 4)
	}
	if underlying.state != 50 {
		t.Fatalf("underlying state = %d, want %d", underlying.state, 50)
	}
	if underlying.lexer.pos != 3 {
		t.Fatalf("underlying lexer.pos = %d, want %d", underlying.lexer.pos, 3)
	}
}
