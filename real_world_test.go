package pagemark

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type realWorldManifest struct {
	SchemaVersion int                `json:"schema_version"`
	Fixtures      []realWorldFixture `json:"fixtures"`
}

type realWorldFixture struct {
	Name       string   `json:"name"`
	File       string   `json:"file"`
	URL        string   `json:"url"`
	CapturedAt string   `json:"captured_at"`
	License    string   `json:"license"`
	SHA256     string   `json:"sha256"`
	PageType   PageType `json:"page_type"`
	MinQuality float64  `json:"min_quality"`
	Required   []string `json:"required"`
	Forbidden  []string `json:"forbidden"`
}

func TestRealWorldFixtures(t *testing.T) {
	const directory = "testdata/real-world"
	manifestData, err := os.ReadFile(filepath.Join(directory, "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest realWorldManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode fixture manifest: %v", err)
	}
	if manifest.SchemaVersion != 1 {
		t.Fatalf("fixture schema version = %d, want 1", manifest.SchemaVersion)
	}
	if len(manifest.Fixtures) == 0 {
		t.Fatal("fixture manifest is empty")
	}

	for _, fixture := range manifest.Fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			if fixture.CapturedAt == "" || fixture.License == "" {
				t.Fatal("fixture provenance must include captured_at and license")
			}
			source, err := os.ReadFile(filepath.Join(directory, fixture.File))
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(source)
			if got := hex.EncodeToString(digest[:]); got != fixture.SHA256 {
				t.Fatalf("fixture checksum = %s, want %s; update expectations and provenance deliberately", got, fixture.SHA256)
			}

			doc, err := ExtractBytes(source, fixture.URL)
			if err != nil {
				t.Fatal(err)
			}
			if doc.PageType != fixture.PageType {
				t.Errorf("page type = %q, want %q", doc.PageType, fixture.PageType)
			}
			if doc.Quality < fixture.MinQuality {
				t.Errorf("quality = %.2f, want at least %.2f", doc.Quality, fixture.MinQuality)
			}
			for _, required := range fixture.Required {
				if !strings.Contains(doc.Markdown, required) {
					t.Errorf("missing required Markdown %q\n%s", required, excerpt(doc.Markdown))
				}
			}
			for _, forbidden := range fixture.Forbidden {
				if strings.Contains(doc.Markdown, forbidden) {
					t.Errorf("included forbidden Markdown %q\n%s", forbidden, excerpt(doc.Markdown))
				}
			}
		})
	}
}

func excerpt(markdown string) string {
	const max = 2000
	if len(markdown) <= max {
		return markdown
	}
	return fmt.Sprintf("%s\n... (%d bytes total)", markdown[:max], len(markdown))
}
