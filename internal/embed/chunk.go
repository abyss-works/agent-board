// Package embed implements the search by meaning: the client of an OpenAI
// compatible embeddings API, the chunking of source text, the in-memory vector
// index and the runner that keeps the stored embeddings current.
package embed

import "strings"

// MaxChunkRunes is the longest chunk of a source text, in runes.
const MaxChunkRunes = 900

// Chunk splits text into consecutive pieces of at most MaxChunkRunes runes,
// trims them and drops the empty ones. Text without content has no chunks.
func Chunk(text string) []string {
	r := []rune(text)
	var out []string
	for len(r) > 0 {
		n := min(len(r), MaxChunkRunes)
		if c := strings.TrimSpace(string(r[:n])); c != "" {
			out = append(out, c)
		}
		r = r[n:]
	}
	return out
}
