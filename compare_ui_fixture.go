package gotreesitter

// CompareEntityFixture is a tiny declaration-level test surface for Orchard compare UI.
type CompareEntityFixture struct {
	Language string
	QueryLen int
}

// BuildCompareEntityFixture constructs a deterministic fixture object for UI diff tests.
func BuildCompareEntityFixture(language string, query string) CompareEntityFixture {
	return CompareEntityFixture{
		Language: language,
		QueryLen: len(query),
	}
}
