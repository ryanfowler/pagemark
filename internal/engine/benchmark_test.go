package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchmarkDocument prevents the compiler from discarding extraction results.
var benchmarkDocument *Document

// BenchmarkExtractNeutralTree exposes accidental subtree scans and class-token
// work across ordinary nested containers.
func BenchmarkExtractNeutralTree(b *testing.B) {
	var source strings.Builder
	source.WriteString(`<html><head><title>Neutral document</title></head><body><main><article><h1>Neutral document</h1>`)
	for range 500 {
		source.WriteString(`<div class="layout column content"><p>This ordinary paragraph contains useful neutral prose without navigation, visibility, consent, filter, or script-notice markers.</p></div>`)
	}
	source.WriteString(`</article></main></body></html>`)
	input := []byte(source.String())
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	for b.Loop() {
		doc, err := ExtractBytes(input, "https://example.com/article", WithPageType(PageTypeArticle))
		if err != nil {
			b.Fatal(err)
		}
		benchmarkDocument = doc
	}
}

// BenchmarkExtractRealWorld measures the complete public extraction path,
// including HTML parsing. The frozen corpus covers articles, documentation,
// discussions, products, listings, services, and generic pages.
func BenchmarkExtractRealWorld(b *testing.B) {
	const directory = "../../testdata/real-world"
	manifestData, err := os.ReadFile(filepath.Join(directory, "fixtures.json"))
	if err != nil {
		b.Fatal(err)
	}
	var manifest realWorldManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		b.Fatalf("decode fixture manifest: %v", err)
	}

	for _, fixture := range manifest.Fixtures {
		fixture := fixture
		source, err := os.ReadFile(filepath.Join(directory, fixture.File))
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fixture.Name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(source)))
			b.ResetTimer()
			for b.Loop() {
				doc, err := ExtractBytes(source, fixture.URL)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkDocument = doc
			}
		})
	}
}
