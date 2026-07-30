package pagemark

import (
	"errors"
	"math"
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
		{
			name: "precision",
			mode: SelectionPrecision,
			want: "```\nanchor content stays\n```",
		},
		{
			name: "recall",
			mode: SelectionRecall,
			want: "```\nanchor content stays\n```\n\nThis medium optional paragraph has enough characters to cross the balanced threshold.\n\nBrief optional note.",
		},
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

func TestLimitsSemanticsAndPrecedence(t *testing.T) {
	defaults, err := applyOptions([]Option{WithLimits(Limits{})})
	if err != nil {
		t.Fatal(err)
	}
	want := defaultOptions()
	if defaults.maxInput != want.maxInput || defaults.maxElements != want.maxElements ||
		defaults.maxDepth != want.maxDepth || defaults.maxOutput != want.maxOutput ||
		defaults.maxLinks != want.maxLinks || defaults.maxImages != want.maxImages ||
		defaults.maxTableCells != want.maxTableCells || defaults.maxRepeated != want.maxRepeated {
		t.Fatalf("zero limits = %#v, want defaults %#v", defaults, want)
	}

	unlimited, err := applyOptions([]Option{WithLimits(Limits{
		InputBytes: -1, Elements: -1, Depth: -1, OutputBytes: -1,
		Links: -1, Images: -1, TableCells: -1, RepeatedItems: -1,
	})})
	if err != nil {
		t.Fatal(err)
	}
	if unlimited.maxInput != 0 || unlimited.maxElements != 0 || unlimited.maxDepth != 0 ||
		unlimited.maxOutput != 0 || unlimited.maxLinks != math.MaxInt ||
		unlimited.maxImages != math.MaxInt || unlimited.maxTableCells != math.MaxInt ||
		unlimited.maxRepeated != 0 {
		t.Fatalf("unlimited values were not normalized: %#v", unlimited)
	}

	later, err := applyOptions([]Option{
		WithLimits(Limits{OutputBytes: 1 << 20}),
		WithMaxOutputBytes(2 << 20),
	})
	if err != nil {
		t.Fatal(err)
	}
	if later.maxOutput != 2<<20 {
		t.Fatalf("later output limit = %d, want %d", later.maxOutput, 2<<20)
	}
}

func TestInvalidLimitValues(t *testing.T) {
	tests := []Limits{
		{InputBytes: -2}, {Elements: -2}, {Depth: -2}, {OutputBytes: -2},
		{Links: -2}, {Images: -2}, {TableCells: -2}, {RepeatedItems: -2},
	}
	for _, limits := range tests {
		_, err := ExtractBytes([]byte(`<p>content</p>`), "", WithLimits(limits))
		if !errors.Is(err, ErrInvalidOption) {
			t.Errorf("WithLimits(%#v) error = %v", limits, err)
		}
	}
	for _, option := range []Option{WithMaxInputBytes(-2), WithMaxOutputBytes(-2)} {
		_, err := ExtractBytes([]byte(`<p>content</p>`), "", option)
		if !errors.Is(err, ErrInvalidOption) {
			t.Errorf("convenience option error = %v", err)
		}
	}
}

func TestUnlimitedContentLimitsDoNotDisableFeatures(t *testing.T) {
	source := []byte(`<main><h1>Resources</h1><p>Useful resources are listed here for readers.</p><p><a href="/guide">Read the guide</a></p><img src="/diagram.png" alt="System diagram" width="800" height="600"><table><tr><th>Name</th><th>Value</th></tr><tr><td>Mode</td><td>Safe</td></tr></table><p>This conclusion provides enough substantive content for extraction.</p></main>`)
	doc, err := ExtractBytes(source, "https://example.com/start", WithPageType(PageTypeDocumentation), WithLimits(Limits{Links: -1, Images: -1, TableCells: -1}))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Links) == 0 || len(doc.Images) == 0 || !strings.Contains(doc.Markdown, "| Name | Value |") {
		t.Fatalf("unlimited limits disabled a feature: links=%#v images=%#v\n%s", doc.Links, doc.Images, doc.Markdown)
	}

	disabled, err := ExtractBytes(source, "https://example.com/start", WithPageType(PageTypeDocumentation),
		WithLimits(Limits{Links: -1, Images: -1, TableCells: -1}),
		WithIncludeLinks(false), WithIncludeImages(false), WithIncludeTables(false))
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled.Links) != 0 || len(disabled.Images) != 0 || strings.Contains(disabled.Markdown, "| Name | Value |") {
		t.Fatalf("feature toggles were ignored: links=%#v images=%#v\n%s", disabled.Links, disabled.Images, disabled.Markdown)
	}
}

func TestUnlimitedLimitsThroughPublicAPI(t *testing.T) {
	t.Run("input reader and bytes", func(t *testing.T) {
		source := []byte(`<main><p>Useful content remains available after disabling the small input limit.</p></main>`)
		for _, extract := range []struct {
			name string
			call func(...Option) (*Document, error)
		}{
			{"reader", func(opts ...Option) (*Document, error) {
				return Extract(strings.NewReader(string(source)), "", opts...)
			}},
			{"bytes", func(opts ...Option) (*Document, error) {
				return ExtractBytes(source, "", opts...)
			}},
		} {
			t.Run(extract.name, func(t *testing.T) {
				if _, err := extract.call(WithMaxInputBytes(10)); !errors.Is(err, ErrLimit) {
					t.Fatalf("small limit error = %v", err)
				}
				doc, err := extract.call(WithMaxInputBytes(-1))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(doc.Text, "Useful content") {
					t.Fatalf("unlimited input lost content: %q", doc.Text)
				}
			})
		}
	})

	t.Run("elements", func(t *testing.T) {
		source := []byte(`<main><section><div><p>Element-limited content remains available.</p></div></section></main>`)
		if _, err := ExtractBytes(source, "", WithLimits(Limits{Elements: 2})); !errors.Is(err, ErrLimit) {
			t.Fatalf("small element limit error = %v", err)
		}
		doc, err := ExtractBytes(source, "", WithLimits(Limits{Elements: -1}))
		if err != nil || !strings.Contains(doc.Text, "Element-limited content") {
			t.Fatalf("unlimited elements: doc=%#v err=%v", doc, err)
		}
	})

	t.Run("depth", func(t *testing.T) {
		source := []byte(`<main><section><div><p>Deep content remains available.</p></div></section></main>`)
		if _, err := ExtractBytes(source, "", WithLimits(Limits{Depth: 2})); !errors.Is(err, ErrLimit) {
			t.Fatalf("small depth limit error = %v", err)
		}
		doc, err := ExtractBytes(source, "", WithLimits(Limits{Depth: -1}))
		if err != nil || !strings.Contains(doc.Text, "Deep content") {
			t.Fatalf("unlimited depth: doc=%#v err=%v", doc, err)
		}
	})

	t.Run("output", func(t *testing.T) {
		source := []byte(`<main><p>The first complete paragraph is useful and should remain in the output.</p><p>The second complete paragraph proves that unlimited output is not truncated.</p></main>`)
		limited, err := ExtractBytes(source, "", WithLimits(Limits{OutputBytes: 80}))
		if err != nil {
			t.Fatal(err)
		}
		if len(limited.Warnings) == 0 || limited.Warnings[0].Code != WarningOutputTruncated {
			t.Fatalf("small output limit did not truncate: %#v", limited.Warnings)
		}
		doc, err := ExtractBytes(source, "", WithLimits(Limits{OutputBytes: -1}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc.Text, "second complete paragraph") {
			t.Fatalf("unlimited output was truncated: %q", doc.Text)
		}
		for _, warning := range doc.Warnings {
			if warning.Code == WarningOutputTruncated {
				t.Fatalf("unlimited output warning: %#v", warning)
			}
		}
	})

	t.Run("repeated items", func(t *testing.T) {
		source := []byte(`<main><h1>Results</h1><div class="item"><p>First listed record.</p></div><div class="item"><p>Second listed record.</p></div><div class="item"><p>Third listed record.</p></div></main>`)
		limited, err := ExtractBytes(source, "https://example.com/results", WithPageType(PageTypeListing), WithLimits(Limits{RepeatedItems: 1}))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(limited.Text, "Third listed") {
			t.Fatalf("small repeated limit retained every record: %q", limited.Text)
		}
		doc, err := ExtractBytes(source, "https://example.com/results", WithPageType(PageTypeListing), WithLimits(Limits{RepeatedItems: -1}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc.Text, "Third listed") {
			t.Fatalf("unlimited repeated items were truncated: %q", doc.Text)
		}
		for _, warning := range doc.Warnings {
			if warning.Code == WarningRepeatedItemsTruncated {
				t.Fatalf("unlimited repeated-item warning: %#v", warning)
			}
		}
	})
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
