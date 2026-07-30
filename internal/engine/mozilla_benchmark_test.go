package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkExtractMozillaReadability exercises representative article-only
// compatibility cases. It is secondary to BenchmarkExtractRealWorld.
func BenchmarkExtractMozillaReadability(b *testing.B) {
	fixtures := []struct {
		name     string
		fixture  string
		recovery bool
	}{
		{name: "short-article", fixture: "001"},
		{name: "long-article", fixture: "google-sre-book-1"},
		{name: "malformed-publisher-html", fixture: "comment-inside-script-parsing"},
		{name: "lazy-images", fixture: "lazy-image-1"},
		{name: "tables", fixture: "keep-tabular-data"},
		{name: "article-region-recovery", fixture: "metadata-content-missing", recovery: true},
	}
	if _, err := os.Stat(mozillaCorpusDir); err != nil {
		b.Skip("Mozilla Readability corpus is not initialized; run: git submodule update --init --recursive")
	}
	for _, fixture := range fixtures {
		source, err := os.ReadFile(filepath.Join(mozillaCorpusDir, fixture.fixture, "source.html"))
		if err != nil {
			b.Fatal(err)
		}
		if fixture.recovery {
			doc, err := ExtractBytes(source, mozillaSyntheticURL, WithPageType(PageTypeArticle), WithDiagnostics(true))
			if err != nil {
				b.Fatal(err)
			}
			if doc.Diagnostics == nil || doc.Diagnostics.Fallback != "article-region" {
				b.Fatalf("fixture %s no longer exercises article-region recovery", fixture.fixture)
			}
		}
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(source)))
			b.ResetTimer()
			for b.Loop() {
				doc, err := ExtractBytes(source, mozillaSyntheticURL, WithPageType(PageTypeArticle))
				if err != nil {
					b.Fatal(err)
				}
				benchmarkDocument = doc
			}
		})
	}
}
