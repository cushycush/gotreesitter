package grammars

type grammarBlob struct {
	data    []byte
	release func()
}

func (b grammarBlob) close() {
	if b.release != nil {
		b.release()
	}
}

// BlobByName returns the raw compressed grammar blob for the named language
// (e.g. "go", "python"). Returns nil if the language blob is not found.
// The returned bytes are the gzip+gob encoded grammar data suitable for
// serving to browser-side WASM modules that decode grammars on demand.
func BlobByName(name string) []byte {
	// Resolve aliases and normalize case the same way DetectLanguageByName does.
	entry := DetectLanguageByName(name)
	if entry == nil || entry.GrammarSource != GrammarSourceTS2GoBlob {
		return nil
	}
	blob, err := readGrammarBlob(entry.Name + ".bin")
	if err != nil {
		return nil
	}
	// Copy the data so the caller owns it and we can release the blob.
	data := make([]byte, len(blob.data))
	copy(data, blob.data)
	blob.close()
	return data
}
