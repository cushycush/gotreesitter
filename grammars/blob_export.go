package grammars

// BlobByName returns the raw compressed grammar blob bytes for the named
// language, or nil if the language is unknown or does not have a ts2go blob.
// The returned bytes are gzip-compressed gob data — the same format stored
// in the embedded grammar_blobs/*.bin files.
//
// The name is normalized the same way as DetectLanguageByName: lowercased,
// trimmed, and resolved through linguist aliases (e.g. "Go", "golang",
// "C++", "cpp" all work).
//
// This is useful for serving grammar blobs over HTTP so that a browser-side
// parser can load grammars on demand.
func BlobByName(name string) []byte {
	entry := DetectLanguageByName(name)
	if entry == nil {
		return nil
	}
	// Only ts2go blobs have embedded .bin files.
	if entry.GrammarSource != GrammarSourceTS2GoBlob {
		return nil
	}
	blobName := entry.Name + ".bin"
	blob, err := readGrammarBlob(blobName)
	if err != nil {
		return nil
	}
	defer blob.close()

	// Return a copy so the caller owns the bytes and any mmap can be released.
	out := make([]byte, len(blob.data))
	copy(out, blob.data)
	return out
}
