package pagemark

import (
	"strings"
	"testing"
)

func TestImagesEnabledByDefaultAndCanBeDisabled(t *testing.T) {
	if !defaultOptions().includeImages {
		t.Fatal("default options disable images")
	}

	source := []byte(`<main><h1>Field report</h1><p>This field report explains the observed result in enough detail to establish the primary content.</p><img src="/photos/result.jpg" alt="Observed field result" width="1200" height="800"><p>The concluding analysis confirms the observation and records the outcome for future readers.</p></main>`)
	const image = `![Observed field result](https://example.com/photos/result.jpg)`

	withImages, err := ExtractBytes(source, "https://example.com/reports/field")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withImages.Markdown, image) {
		t.Fatalf("default output is missing image Markdown:\n%s", withImages.Markdown)
	}
	if len(withImages.Images) != 1 || withImages.Images[0].Alt != "Observed field result" || withImages.Images[0].URL != "https://example.com/photos/result.jpg" {
		t.Fatalf("default images = %#v", withImages.Images)
	}

	withoutImages, err := ExtractBytes(source, "https://example.com/reports/field", WithIncludeImages(false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(withoutImages.Markdown, "result.jpg") || len(withoutImages.Images) != 0 {
		t.Fatalf("WithIncludeImages(false) retained images: %#v\n%s", withoutImages.Images, withoutImages.Markdown)
	}
}
