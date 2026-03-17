package grammarlsp

import "sort"

// Position is a 0-indexed line/column pair.
type Position struct {
	Line, Col int
}

// Mapping links a source position to a destination position.
type Mapping struct {
	SrcLine, SrcCol int
	DstLine, DstCol int
}

// SourceMap provides bidirectional position mapping between DSL source
// and generated Go code. Two sorted indices for O(log n) lookup.
type SourceMap struct {
	mappings []Mapping
	byDst    []int // indices into mappings, sorted by DstLine/DstCol
}

func NewSourceMap() *SourceMap {
	return &SourceMap{}
}

func (sm *SourceMap) AddMapping(m Mapping) {
	sm.mappings = append(sm.mappings, m)
}

func (sm *SourceMap) AddLineMapping(srcLine, dstLine int) {
	sm.AddMapping(Mapping{SrcLine: srcLine, DstLine: dstLine})
}

func (sm *SourceMap) Build() {
	sort.Slice(sm.mappings, func(i, j int) bool {
		if sm.mappings[i].SrcLine != sm.mappings[j].SrcLine {
			return sm.mappings[i].SrcLine < sm.mappings[j].SrcLine
		}
		return sm.mappings[i].SrcCol < sm.mappings[j].SrcCol
	})
	sm.byDst = make([]int, len(sm.mappings))
	for i := range sm.byDst {
		sm.byDst[i] = i
	}
	sort.Slice(sm.byDst, func(i, j int) bool {
		a, b := sm.mappings[sm.byDst[i]], sm.mappings[sm.byDst[j]]
		if a.DstLine != b.DstLine {
			return a.DstLine < b.DstLine
		}
		return a.DstCol < b.DstCol
	})
}

func (sm *SourceMap) ToDst(pos Position) Position {
	if len(sm.mappings) == 0 {
		return pos
	}
	idx := sort.Search(len(sm.mappings), func(i int) bool {
		return sm.mappings[i].SrcLine > pos.Line ||
			(sm.mappings[i].SrcLine == pos.Line && sm.mappings[i].SrcCol > pos.Col)
	}) - 1
	if idx < 0 {
		idx = 0
	}
	m := sm.mappings[idx]
	lineDelta := pos.Line - m.SrcLine
	colDelta := pos.Col
	if lineDelta == 0 {
		colDelta = pos.Col - m.SrcCol
	}
	return Position{Line: m.DstLine + lineDelta, Col: m.DstCol + colDelta}
}

func (sm *SourceMap) ToSrc(pos Position) Position {
	if len(sm.byDst) == 0 {
		return pos
	}
	idx := sort.Search(len(sm.byDst), func(i int) bool {
		m := sm.mappings[sm.byDst[i]]
		return m.DstLine > pos.Line ||
			(m.DstLine == pos.Line && m.DstCol > pos.Col)
	}) - 1
	if idx < 0 {
		idx = 0
	}
	m := sm.mappings[sm.byDst[idx]]
	lineDelta := pos.Line - m.DstLine
	// Clamp lineDelta to the source line span for this segment.
	// In expansion zones (many dst lines for few src lines), map back
	// to the anchor source line rather than overshooting.
	if idx+1 < len(sm.byDst) {
		next := sm.mappings[sm.byDst[idx+1]]
		srcSpan := next.SrcLine - m.SrcLine
		dstSpan := next.DstLine - m.DstLine
		if srcSpan < 0 {
			srcSpan = 0
		}
		if dstSpan > srcSpan {
			// Expansion zone: many dst lines generated from fewer src lines.
			// Map back to the anchor source line (no delta) because there is
			// no meaningful line-by-line correspondence in generated code.
			lineDelta = 0
		} else if lineDelta > srcSpan && srcSpan > 0 {
			lineDelta = srcSpan
		}
	}
	colDelta := pos.Col
	if lineDelta == 0 {
		colDelta = pos.Col - m.DstCol
	}
	return Position{Line: m.SrcLine + lineDelta, Col: m.SrcCol + colDelta}
}

// BuildFromDiff builds a source map by comparing source and destination line-by-line.
func BuildFromDiff(srcLines, dstLines []string) *SourceMap {
	sm := NewSourceMap()
	srcIdx, dstIdx := 0, 0
	for srcIdx < len(srcLines) && dstIdx < len(dstLines) {
		sm.AddLineMapping(srcIdx, dstIdx)
		srcIdx++
		dstIdx++
	}
	sm.Build()
	return sm
}
