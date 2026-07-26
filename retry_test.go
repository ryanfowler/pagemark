package pagemark

import (
	"strconv"
	"strings"
	"testing"
)

func TestRelaxedArticleLabelsRecoverProse(t *testing.T) {
	html := `<html><head><meta property="article:published_time" content="2025-01-02"><title>Recovered story</title></head><body>
	<div class="newsletter-layout"><p>This is the first substantial paragraph of the report, with enough ordinary prose to establish the local editorial body.</p>
	<p>This is the second substantial paragraph, continuing the report without links, controls, promotions, or other page furniture.</p></div></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/story", WithPageType(PageTypeArticle), WithDiagnostics(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "first substantial paragraph") || !strings.Contains(doc.Text, "second substantial paragraph") {
		t.Fatalf("article prose was not recovered: %q", doc.Text)
	}
	if doc.Diagnostics == nil || doc.Diagnostics.Fallback != "relaxed-labels" {
		t.Fatalf("fallback = %#v, want relaxed-labels", doc.Diagnostics)
	}
	if len(doc.Warnings) == 0 || doc.Warnings[0].Code != "relaxed-article-extraction" {
		t.Fatalf("warnings = %#v", doc.Warnings)
	}
}

func TestRelaxedArticleLabelsRecoverLongEmptyPrimary(t *testing.T) {
	var source strings.Builder
	source.WriteString(`<html><head><meta property="article:published_time" content="2025-01-02"><title>Long recovered story</title></head><body><div class="newsletter-layout">`)
	for i := 1; i <= 13; i++ {
		source.WriteString("<p>Unique paragraph number ")
		source.WriteString(strconv.Itoa(i))
		source.WriteString(" contains substantial, low-link-density editorial prose that belongs to this long article and should be recovered.</p>")
	}
	source.WriteString(`</div></body></html>`)

	doc, err := ExtractBytes([]byte(source.String()), "https://example.com/long-story", WithPageType(PageTypeArticle), WithDiagnostics(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "Unique paragraph number 1 ") || !strings.Contains(doc.Text, "Unique paragraph number 13 ") {
		t.Fatalf("long relaxed article was truncated or not recovered: %q", doc.Text)
	}
	if doc.Diagnostics == nil || doc.Diagnostics.Fallback != "relaxed-labels" {
		t.Fatalf("fallback = %#v, want relaxed-labels", doc.Diagnostics)
	}
}

func TestRelaxedArticleThresholdIsProseOnly(t *testing.T) {
	html := `<html><head><meta property="article:published_time" content="2025-01-02"><title>Brief report</title></head><body><div class="article-body">
	<p>One concise paragraph narrowly misses strict scoring.</p><p>Another concise paragraph supplies the rest of the report.</p>
	<nav><p>This deliberately long navigation label looks rather like prose but must always remain excluded from article output.</p></nav>
	</div></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/brief", WithPageType(PageTypeArticle), WithFavorPrecision(true), WithDiagnostics(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "One concise paragraph") || strings.Contains(doc.Text, "navigation label") {
		t.Fatalf("unexpected output: %q (diagnostics: %#v)", doc.Text, doc.Diagnostics)
	}
	if doc.Diagnostics.Fallback != "relaxed-threshold" {
		t.Fatalf("fallback = %q, want relaxed-threshold", doc.Diagnostics.Fallback)
	}
}

func TestArticleRetryNeverRestoresAuxiliaryRegions(t *testing.T) {
	html := `<html><head><meta property="article:published_time" content="2025-01-02"><title>Safe story</title></head><body>
	<div class="newsletter-layout"><p>The first substantial editorial paragraph contains useful reporting and establishes the body of this article.</p>
	<div class="advertisement"><p>Advertisement prose that must never be restored even when it is exceptionally long and fluent.</p></div>
	<p>The second substantial editorial paragraph continues after the advertisement and belongs in the extracted result.</p></div>
	<section class="newsletter"><h2>Subscribe</h2><form><input type="email"><button>Subscribe</button></form><p>Newsletter subscription prose must not be recovered into the article.</p></section>
	<section class="comments"><h2>Comments</h2><article><p>Comment prose must not be recovered into the article extraction.</p></article></section></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/safe", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Advertisement prose", "Newsletter subscription prose", "Comment prose"} {
		if strings.Contains(doc.Text, forbidden) {
			t.Fatalf("recovered forbidden %q in %q", forbidden, doc.Text)
		}
	}
	if !strings.Contains(doc.Text, "second substantial editorial") {
		t.Fatalf("missing article continuation: %q", doc.Text)
	}
}

func TestArticleRetriesDoNotApplyToExplicitNonArticleType(t *testing.T) {
	html := `<html><head><meta property="article:published_time" content="2025-01-02"><title>Not an article</title></head><body>
	<pre>const retained = "documentation"</pre><div class="newsletter-layout"><p>This substantial paragraph would be eligible under relaxed article labels if retries were incorrectly enabled.</p>
	<p>This second substantial paragraph supplies enough local prose evidence for an article-specific retry.</p></div></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/docs", WithPageType(PageTypeDocumentation), WithDiagnostics(true))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Diagnostics != nil && strings.HasPrefix(doc.Diagnostics.Fallback, "relaxed-") {
		t.Fatalf("non-article used retry: %q", doc.Diagnostics.Fallback)
	}
}

func TestRelaxedArticleDeterministicWithoutDiagnostics(t *testing.T) {
	html := []byte(`<html><head><meta property="article:published_time" content="2025-01-02"><title>Repeatable</title></head><body><div class="newsletter-layout"><p>This first substantial article paragraph is deterministic and contains useful editorial prose for readers.</p><p>This second substantial article paragraph is deterministic and completes the report for readers.</p></div></body></html>`)
	var want string
	for i := 0; i < 5; i++ {
		doc, err := ExtractBytes(html, "https://example.com/repeat", WithPageType(PageTypeArticle))
		if err != nil {
			t.Fatal(err)
		}
		if doc.Diagnostics != nil {
			t.Fatal("diagnostics allocated while disabled")
		}
		if i == 0 {
			want = doc.Markdown
		} else if doc.Markdown != want {
			t.Fatalf("run %d was nondeterministic: %q != %q", i, doc.Markdown, want)
		}
	}
}
