package engine

import (
	"errors"
	"strings"
	"sync"
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

func TestSelectionModeValidation(t *testing.T) {
	for _, mode := range []SelectionMode{SelectionBalanced, SelectionPrecision, SelectionRecall} {
		if _, err := ExtractBytes([]byte(`<main><p>Useful content for a valid selection mode.</p></main>`), "", WithSelectionMode(mode)); err != nil {
			t.Fatalf("mode %d: %v", mode, err)
		}
	}
	_, err := ExtractBytes([]byte(`<p>content</p>`), "", WithSelectionMode(SelectionMode(255)))
	if !errors.Is(err, ErrInvalidOption) || !strings.Contains(err.Error(), "selection mode") {
		t.Fatalf("invalid mode error = %v", err)
	}
}

func TestExplicitBalancedSelectionMatchesDefault(t *testing.T) {
	source := []byte(`<article><h1>Selection behavior</h1><p>This representative article paragraph contains enough authored prose to exercise normal selection.</p><aside><p>Related navigation and auxiliary material.</p></aside><p>The final paragraph records the result for readers.</p></article>`)
	defaultDoc, err := ExtractBytes(source, "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	balancedDoc, err := ExtractBytes(source, "https://example.com/article", WithPageType(PageTypeArticle), WithSelectionMode(SelectionBalanced))
	if err != nil {
		t.Fatal(err)
	}
	if balancedDoc.Markdown != defaultDoc.Markdown || balancedDoc.Text != defaultDoc.Text {
		t.Fatalf("balanced mode differs from default:\ndefault=%q\nbalanced=%q", defaultDoc.Markdown, balancedDoc.Markdown)
	}
}

func TestSelectionModeGoldenOutputs(t *testing.T) {
	source := []byte(`<body><pre>anchor content stays</pre><p>This medium optional paragraph has enough characters to cross the balanced threshold.</p><p>Brief optional note.</p></body>`)
	tests := []struct {
		name string
		mode SelectionMode
		want string
	}{
		{name: "precision", mode: SelectionPrecision, want: "```\nanchor content stays\n```"},
		{name: "recall", mode: SelectionRecall, want: "```\nanchor content stays\n```\n\nThis medium optional paragraph has enough characters to cross the balanced threshold.\n\nBrief optional note."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc, err := ExtractBytes(source, "https://example.com/modes", WithPageType(PageTypeGeneric), WithSelectionMode(test.mode))
			if err != nil {
				t.Fatal(err)
			}
			if doc.Markdown != test.want {
				t.Fatalf("%s Markdown changed:\n got: %q\nwant: %q", test.name, doc.Markdown, test.want)
			}
		})
	}
}

func TestPageTypeValidation(t *testing.T) {
	_, err := ExtractBytes([]byte(`<p>content</p>`), "", WithPageType(PageType("not-a-page-type")))
	if !errors.Is(err, ErrInvalidOption) || !strings.Contains(err.Error(), "page type") {
		t.Fatalf("invalid page type error = %v", err)
	}
}

func TestInputAndOutputOptions(t *testing.T) {
	source := []byte(`<main><p>The first complete paragraph is useful and should remain in the output.</p><p>The second complete paragraph proves that unlimited output is not truncated.</p></main>`)
	if _, err := ExtractBytes([]byte(strings.Repeat("x", 20)), "", WithMaxInputBytes(10)); !errors.Is(err, ErrLimit) {
		t.Fatalf("input limit error = %v", err)
	}
	limited, err := ExtractBytes(source, "", WithMaxOutputBytes(80))
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Warnings) == 0 || limited.Warnings[0].Code != WarningOutputTruncated {
		t.Fatalf("small output limit did not truncate: %#v", limited.Warnings)
	}
	doc, err := ExtractBytes(source, "", WithMaxOutputBytes(-1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "second complete paragraph") {
		t.Fatalf("unlimited output was truncated: %q", doc.Text)
	}
}

func TestInvalidInputAndOutputLimits(t *testing.T) {
	for _, option := range []Option{WithMaxInputBytes(-2), WithMaxOutputBytes(-2)} {
		_, err := ExtractBytes([]byte(`<p>content</p>`), "", option)
		if !errors.Is(err, ErrInvalidOption) {
			t.Errorf("invalid limit error = %v", err)
		}
	}
}

func TestInternalDOMLimits(t *testing.T) {
	source := []byte(`<main><section><div><p>Deep content remains available.</p></div></section></main>`)
	if _, err := ExtractBytes(source, "", func(o *options) { o.maxElements = 2 }); !errors.Is(err, ErrLimit) {
		t.Fatalf("element limit error = %v", err)
	}
	if _, err := ExtractBytes(source, "", func(o *options) { o.maxDepth = 2 }); !errors.Is(err, ErrLimit) {
		t.Fatalf("depth limit error = %v", err)
	}
}

func TestURLPolicyValidationAndOwnership(t *testing.T) {
	schemes := []string{"HTTPS", "https"}
	option := WithURLPolicy(URLPolicy{AllowedSchemes: schemes, MaxLength: -1})
	schemes[0] = "http"

	o, err := applyOptions([]Option{option})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.urlPolicy.AllowedSchemes) != 1 || o.urlPolicy.AllowedSchemes[0] != "https" || o.urlPolicy.MaxLength != 0 {
		t.Fatalf("normalized policy = %#v", o.urlPolicy)
	}
	first := DefaultURLPolicy()
	second := DefaultURLPolicy()
	first.AllowedSchemes[0] = "mailto"
	if second.AllowedSchemes[0] != "http" {
		t.Fatalf("DefaultURLPolicy shares scheme storage: %#v", second.AllowedSchemes)
	}

	for _, policy := range []URLPolicy{
		{AllowedSchemes: []string{""}},
		{AllowedSchemes: []string{"1http"}},
		{AllowedSchemes: []string{"http:"}},
		{AllowedSchemes: []string{"white space"}},
		{AllowedSchemes: []string{"éxample"}},
		{AllowedSchemes: []string{"https"}, MaxLength: -2},
	} {
		_, err := ExtractBytes([]byte(`<p>content</p>`), "", WithURLPolicy(policy))
		if !errors.Is(err, ErrInvalidOption) {
			t.Errorf("policy %#v error = %v", policy, err)
		}
	}
}

func TestURLPolicyOptionConcurrentReuse(t *testing.T) {
	option := WithURLPolicy(URLPolicy{AllowedSchemes: []string{"https"}, MaxLength: 1024})
	source := []byte(`<main><p>Read <a href="/guide">the complete guide</a> for useful details about this system.</p></main>`)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doc, err := ExtractBytes(source, "https://example.com/start", option)
			if err != nil {
				t.Errorf("concurrent extraction: %v", err)
				return
			}
			if len(doc.Links) != 1 {
				t.Errorf("links = %#v", doc.Links)
			}
		}()
	}
	wg.Wait()
}
