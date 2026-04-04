package gotreesitter

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
)

// LoadLanguage deserializes a compressed grammar blob into a Language.
// Blobs are produced by grammargen's GenerateLanguage or the grammar
// build toolchain. This is the only function needed at runtime to load
// pre-compiled grammars — no grammargen import required.
func LoadLanguage(data []byte) (*Language, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gzr.Close()

	// Pre-size the decompression buffer using the ISIZE field in the last 4
	// bytes of the gzip trailer. This avoids io.ReadAll's repeated doublings.
	// ISIZE is uncompressed size mod 2^32; for grammar blobs (well under 4 GB)
	// it is exact. Fall back to io.ReadAll if the hint is implausible.
	var raw []byte
	if len(data) >= 4 {
		isize := binary.LittleEndian.Uint32(data[len(data)-4:])
		if isize > 0 && isize < 256*1024*1024 { // sanity cap at 256 MB
			raw = make([]byte, 0, isize)
			var buf [32 * 1024]byte
			for {
				n, readErr := gzr.Read(buf[:])
				if n > 0 {
					raw = append(raw, buf[:n]...)
				}
				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					return nil, fmt.Errorf("read gzip: %w", readErr)
				}
			}
		}
	}
	if raw == nil {
		raw, err = io.ReadAll(gzr)
		if err != nil {
			return nil, fmt.Errorf("read gzip: %w", err)
		}
	}

	var lang Language
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&lang); err != nil {
		return nil, fmt.Errorf("decode language: %w", err)
	}

	return &lang, nil
}
