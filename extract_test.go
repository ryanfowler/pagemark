package pagemark

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestExtractStructuresAndSafety(t *testing.T) {
	html := `<!doctype html><html lang="en"><head><title>API Guide</title><base href="https://example.com/docs/"><meta name="author" content="Ada"><link rel="canonical" href="guide"></head><body>
<header><nav><a href="/">Home</a></nav></header><main><h1>API Guide</h1><p>Use <strong>this API</strong> safely.</p><pre>go test
` + "```" + `</pre><ul><li>First</li><li>Second</li></ul><table><tr><th>Name</th><th>Value</th></tr><tr><td>Mode</td><td>Fast</td></tr></table><p><a href="next?utm_source=x">Next page</a> <a href="javascript:alert(1)">bad</a></p></main><footer>Copyright</footer></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/start", WithPageType(PageTypeDocumentation), WithDiagnostics(true), WithURLPolicy(URLPolicy{Schemes: []string{"https"}, MaxLength: 1000, StripTracking: true}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# API Guide", "**this API** safely", "````\ngo test\n```\n````", "- First", "| Name | Value |", "https://example.com/docs/next"} {
		if !strings.Contains(doc.Markdown, want) {
			t.Errorf("Markdown does not contain %q:\n%s", want, doc.Markdown)
		}
	}
	for _, bad := range []string{"Home", "Copyright", "javascript:", "utm_source", "<table"} {
		if strings.Contains(doc.Markdown, bad) {
			t.Errorf("Markdown contains %q:\n%s", bad, doc.Markdown)
		}
	}
	if doc.Title != "API Guide" || doc.Author != "Ada" || doc.CanonicalURL != "https://example.com/docs/guide" {
		t.Fatalf("bad metadata: %#v", doc)
	}
	if doc.Diagnostics == nil || len(doc.Diagnostics.RejectedLinks) == 0 {
		t.Fatal("missing rejected-link diagnostic")
	}
}

func TestPageWidePreformattedArchiveRetainsLinks(t *testing.T) {
	source, err := os.ReadFile("testdata/preformatted-archive.html")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ExtractBytes(source, "https://example.com/security/archive/")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeListing && doc.PageType != PageTypeGeneric {
		t.Fatalf("page type = %q, want listing or generic", doc.PageType)
	}
	for _, want := range []string{"[CVE-2025-1001: Correct bounds validation in parser](https://example.com/records/CVE-2025-1001)", "2025-04-03", "[next page](https://example.com/security/archive/?page=2)"} {
		if !strings.Contains(doc.Markdown, want) {
			t.Errorf("missing archive content %q:\n%s", want, doc.Markdown)
		}
	}
	if strings.Contains(doc.Markdown, "```") || strings.Contains(doc.Markdown, "javascript:") {
		t.Fatalf("archive was fenced or retained an unsafe URL:\n%s", doc.Markdown)
	}
}

func TestLargeArticlePreCodeRemainsFenced(t *testing.T) {
	source, err := os.ReadFile("testdata/article-large-code.html")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ExtractBytes(source, "https://example.com/articles/record-parser")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("page type = %q, want article", doc.PageType)
	}
	wantCode := "```go\npackage parser\n\nfunc Parse(input []byte) (Record, error) {"
	if !strings.Contains(doc.Markdown, wantCode) || !strings.Contains(doc.Markdown, "\treturn Record{}, ErrShortInput") {
		t.Fatalf("large code sample was changed or unfenced:\n%s", doc.Markdown)
	}
}

func TestListingLineEvidenceRequiresActualDates(t *testing.T) {
	text := "release-2025-alpha\nCVE-2025-1001\nsource-2024-main\n2025-04-03 record\n3 Apr 2025 record\nApril 2025 archive"
	nonempty, dated := listingLineEvidence(text)
	if nonempty != 6 || dated != 3 {
		t.Fatalf("nonempty=%d dated=%d, want 6 and 3", nonempty, dated)
	}
}

func TestLinkedYearIdentifiersInArticleRemainCode(t *testing.T) {
	html := `<html><head><title>Indexing release-2025 identifiers</title><meta property="og:type" content="article"></head><body><article><h1>Indexing release identifiers</h1><pre><code>` +
		"lookup(<a href=\"/symbols/release-2025-alpha\">release-2025-alpha</a>)\n" +
		"lookup(<a href=\"/symbols/CVE-2025-1001\">CVE-2025-1001</a>)\n" +
		"lookup(<a href=\"/symbols/source-2024-main\">source-2024-main</a>)\n" +
		"lookup(<a href=\"/symbols/version-2023-next\">version-2023-next</a>)\n" +
		`lookup(<a href="/symbols/build-2022-debug">build-2022-debug</a>)` +
		`</code></pre></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("page type = %q, want article", doc.PageType)
	}
	if !strings.Contains(doc.Markdown, "```\nlookup(release-2025-alpha)\nlookup(CVE-2025-1001)") || strings.Contains(doc.Markdown, "](") {
		t.Fatalf("linked identifier sample was reinterpreted as an archive:\n%s", doc.Markdown)
	}
}

func TestExplicitArticleKeepsLinkedDateLiteralsAsCode(t *testing.T) {
	html := `<html><head><title>Release lookup example</title></head><body><main><pre><code>` +
		"releases[\"2025-04-04\"] = <a href=\"/symbols/alpha\">alphaHandler</a>\n" +
		"releases[\"2025-04-03\"] = <a href=\"/symbols/beta\">betaHandler</a>\n" +
		"releases[\"2025-04-02\"] = <a href=\"/symbols/gamma\">gammaHandler</a>\n" +
		"releases[\"2025-04-01\"] = <a href=\"/symbols/delta\">deltaHandler</a>" +
		`</code></pre></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/examples/releases", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("page type = %q, want article", doc.PageType)
	}
	want := "```\nreleases[\"2025-04-04\"] = alphaHandler\nreleases[\"2025-04-03\"] = betaHandler"
	if !strings.Contains(doc.Markdown, want) || strings.Contains(doc.Markdown, "](") {
		t.Fatalf("explicit article code was reinterpreted as an archive:\n%s", doc.Markdown)
	}
}

func TestExtractTreatsInputAsUTF8(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name:   "HTML without charset metadata",
			source: `<!doctype html><html><body><main><h1>The Psychology of Software Teams</h1><p>You’ll read “the team’s guide” ↩</p></main></body></html>`,
		},
		{
			name:   "XHTML",
			source: `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><body><main><h1>The Psychology of Software Teams</h1><p>You’ll read “the team’s guide” ↩</p></main></body></html>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc, err := Extract(strings.NewReader(test.source), "https://example.com/teams", WithPageType(PageTypeArticle))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"You’ll", "“the team’s guide”", "↩"} {
				if !strings.Contains(doc.Markdown, want) {
					t.Errorf("Markdown does not contain %q:\n%s", want, doc.Markdown)
				}
			}
			for _, mojibake := range []string{"â€™", "â€œ", "â†©"} {
				if strings.Contains(doc.Markdown, mojibake) {
					t.Errorf("Markdown contains mojibake %q:\n%s", mojibake, doc.Markdown)
				}
			}
		})
	}
}

func TestJellyfinDiscussionPostBodies(t *testing.T) {
	source, err := os.ReadFile("testdata/jellyfin-forum-thread.html")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ExtractBytes(source, "https://forum.jellyfin.org/t-project-leadership-changes", WithDiagnostics(true))
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeDiscussion {
		t.Fatalf("page type = %q, want discussion", doc.PageType)
	}
	for _, want := range []string{
		"Hello everyone. Effective yesterday",
		"For me personally, it was just time for a change",
		"I truly hope Jellyfin outlives me",
		"Thank you for everything!",
		"Thank you very much Joshua, Anthony and Andrew",
		"one of my favourite self-hosted projects",
	} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing post body %q:\n%s", want, doc.Markdown)
		}
	}
	for _, unwanted := range []string{
		"0 Vote(s) - 0 Average",
		"Forum Jump:",
		"Private Messages",
		"Rate this post",
		"Quote this post",
		"You must log in to reply.",
	} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included discussion control %q:\n%s", unwanted, doc.Markdown)
		}
	}
	postBodies := 0
	for _, block := range doc.Diagnostics.Blocks {
		for _, reason := range block.Reasons {
			if reason == "discussion post body" && block.Selected {
				postBodies++
			}
		}
	}
	if postBodies != 4 {
		t.Errorf("selected %d discussion post bodies, want 4", postBodies)
	}
}

func TestMessageWrapperRetainsDiscussionBody(t *testing.T) {
	html := `<main><div class="thread"><div class="message"><div class="message-content">Actual forum reply from a participant.</div></div></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/forum/thread/1")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeDiscussion {
		t.Fatalf("page type = %q, want discussion", doc.PageType)
	}
	if !strings.Contains(doc.Text, "Actual forum reply from a participant.") {
		t.Fatalf("missing wrapped message content: %s", doc.Markdown)
	}
}

func TestDistributedServiceAndHiddenContent(t *testing.T) {
	html := `<html><body><header><p>Site menu words</p></header><section><h1>Cloud Service</h1><p>Build and ship applications quickly.</p></section><section><h2>Features</h2><p>Reliable storage and simple deployment.</p></section><div hidden><p>secret</p></div><div class="cookie-banner"><p>Accept all cookies now.</p></div><section><h2>FAQ</h2><p>Cancel at any time.</p></section></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/service", WithPageType(PageTypeService))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Cloud Service", "Build and ship", "Features", "Reliable storage", "FAQ", "Cancel at any time"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing %q: %s", want, doc.Text)
		}
	}
	for _, bad := range []string{"Site menu", "secret", "Accept all cookies"} {
		if strings.Contains(doc.Text, bad) {
			t.Errorf("included %q: %s", bad, doc.Text)
		}
	}
}

func TestHiddenDescendantsNeverAppear(t *testing.T) {
	html := `<main><h1>Visibility</h1><p>Visible <span hidden>INLINE_SECRET</span><span aria-hidden="true">ARIA_SECRET</span><span inert>INERT_SECRET</span><span style="display: none">DISPLAY_SECRET</span><span style="visibility: hidden">VISIBILITY_SECRET</span> text.</p><ul><li>Shown</li><li hidden>LIST_SECRET</li><li>Item <span hidden>LIST_INLINE_SECRET</span>end</li></ul><table><tr><th>Name</th><th>Value</th></tr><tr><td>Shown</td><td><span hidden>TABLE_SECRET</span>Safe</td></tr><tr hidden><td>ROW_SECRET</td><td>Bad</td></tr></table></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com", WithPageType(PageTypeDocumentation), WithDiagnostics(true))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Visible", "Shown", "Safe"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing %q: %s", want, doc.Text)
		}
	}
	for _, secret := range []string{"INLINE_SECRET", "ARIA_SECRET", "INERT_SECRET", "DISPLAY_SECRET", "VISIBILITY_SECRET", "LIST_SECRET", "LIST_INLINE_SECRET", "TABLE_SECRET", "ROW_SECRET"} {
		if strings.Contains(doc.Markdown, secret) || strings.Contains(doc.Text, secret) {
			t.Errorf("hidden value %q appeared: %s", secret, doc.Markdown)
		}
	}
}

func TestNestedAndMultiParagraphList(t *testing.T) {
	html := `<main><h1>Steps</h1><ul><li>Parent<p>More detail.</p><ul><li>Child</li></ul></li></ul></main>`
	doc, err := ExtractBytes([]byte(html), "", WithPageType(PageTypeDocumentation))
	if err != nil {
		t.Fatal(err)
	}
	want := "- Parent\n  More detail.\n  - Child"
	if !strings.Contains(doc.Markdown, want) {
		t.Fatalf("want %q in:\n%s", want, doc.Markdown)
	}
	if strings.Count(doc.Text, "Child") != 1 {
		t.Fatalf("nested item was duplicated: %q", doc.Text)
	}
}

func TestAuxiliarySectionsAndCallsToActionAreRemoved(t *testing.T) {
	html := `<main><article><h1>City budget approved</h1><p>The council approved the annual budget after a detailed public debate.</p><p>Residents can read more about the adopted transport plan in the report.</p></article><aside><h2>On this page</h2><ul><li><a href="#budget">Budget</a></li><li><a href="#transport">Transport</a></li></ul></aside><section><h2>More news</h2><article><h3>Unrelated sports result</h3><p>A summary from another story.</p><a href="/sports">Read more</a></article></section><div class="story-card"><h2>Budget documents</h2><p>The resolution and voting record are available.</p><a href="/documents">Read more</a></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/news/budget", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"City budget approved", "council approved", "read more about", "Budget documents", "resolution and voting record"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing relevant content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"On this page", "More news", "Unrelated sports result", "another story", "Read more"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included auxiliary content %q: %s", unwanted, doc.Text)
		}
	}
}

func TestTrailingNewsletterWrapperIsExcluded(t *testing.T) {
	source, err := os.ReadFile("testdata/article-with-newsletter.html")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ExtractBytes(source, "https://example.com/blog/newsletter-delivery")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"A newsletter implementation should validate each email address",
		"Newsletter form implementation",
		"This example explains how to subscribe users safely",
	} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("article discussion %q was removed: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Stay updated", "Subscribe to get updates", "low volume mailing list"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included subscription component %q: %s", unwanted, doc.Text)
		}
	}
}

func TestArticleAuxiliaryLabelsAreHardExcluded(t *testing.T) {
	html := `<main><article><h1>Primary analysis</h1><p>The analysis explains the important result with enough detail for readers.</p><p>Readers can read more about the underlying method in this sentence.</p></article><section><h2>Related posts</h2><article><h3>Other result</h3><p>A substantial summary of a different post that must not overcome boilerplate penalties.</p></article></section><section><h2>Read more</h2><p>A long promotional description for an unrelated report.</p></section><aside aria-label="Share"><p>Share this story on several social networks.</p></aside><section><h2>More by Ada Writer</h2><p>Updates, podcasts, and interviews from the same author.</p></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/analysis", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Primary analysis", "important result", "read more about the underlying method"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing article content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Related posts", "Other result", "substantial summary", "Read more", "promotional description", "Share this story", "More by Ada Writer", "podcasts"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included auxiliary content %q: %s", unwanted, doc.Text)
		}
	}
}

func TestTrailingArticleCardGridIsHardExcluded(t *testing.T) {
	html := `<main><article><h1>Primary report</h1><p>The report contains the complete findings and detailed conclusions for the reader, including the evidence, methodology, limitations, and practical consequences of the result.</p></article><section class="promotions"><div class="article-card"><h2>Unrelated article one</h2><p>This card has substantial prose about a different subject and should not leak.</p></div><div class="article-card"><h2>Unrelated article two</h2><p>This second card also contains enough prose to receive a positive content score.</p></div></section></main>`
	// Do not force the page type: card tokens can make this otherwise look like
	// a listing, but the structurally trailing grid must still be excluded.
	doc, err := ExtractBytes([]byte(html), "https://example.com/report")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("got page type %q, want article", doc.PageType)
	}
	for _, want := range []string{"Primary report", "complete findings"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing article content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Unrelated article one", "different subject", "Unrelated article two", "positive content score"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included trailing card content %q: %s", unwanted, doc.Text)
		}
	}
}

func TestTrailingSocialCardAndSelfPreviewAreExcluded(t *testing.T) {
	html := `<html><head><title>Safer account recovery</title><meta property="og:type" content="article"><meta name="description" content="A practical account recovery design that avoids locking legitimate users out."><link rel="canonical" href="https://example.com/articles/account-recovery"></head><body><main><article><h1>Safer account recovery</h1><p>The recovery flow starts with a short-lived challenge and records each attempt for later review.</p><blockquote class="bsky-post"><p>A security engineer wrote: recovery mechanisms deserve the same careful threat modeling as sign-in.</p><a href="https://bsky.app/profile/example/post/quoted">View quoted post</a></blockquote><p>This quotation is part of the analysis, and the article then explains how independent verification limits abuse.</p></article><aside class="disclosure"><p>Disclosure: the author advised a team that uses a similar recovery design.</p></aside><aside class="social-profile"><nav><a href="/feed.xml">Feed</a><a href="https://bsky.app/profile/example">Bluesky profile</a></nav><div class="bsky-card"><p>This copied post says the new account recovery article is now available.</p><a class="preview-card" href="https://example.com/articles/account-recovery/"><strong>Safer account recovery</strong><span>A practical account recovery design that avoids locking legitimate users out.</span></a></div></aside></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/read?source=feed")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"short-lived challenge", "security engineer wrote", "careful threat modeling", "independent verification", "Disclosure: the author advised"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing article content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Feed", "Bluesky profile", "copied post", "new account recovery article", "locking legitimate users out"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included trailing social content %q: %s", unwanted, doc.Text)
		}
	}
}

func TestSubstantiveTrailingSocialSectionSurvives(t *testing.T) {
	html := `<html><head><title>Designing public spaces</title><meta property="og:type" content="article"></head><body><main><article><h1>Designing public spaces</h1><p>The design process began with observations of how residents use the square throughout the day.</p><p>Those observations informed the proposed seating, lighting, and pedestrian routes.</p></article><section class="social-impact"><h2>Social impact</h2><p>The finished square gave neighborhood groups a dependable meeting place and made community events easier to organize.</p><p>Local accessibility advocates also documented how the unobstructed routes improved independent travel.</p></section></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/articles/public-spaces")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Social impact", "dependable meeting place", "improved independent travel"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing substantive trailing section %q: %s", want, doc.Text)
		}
	}
}

func TestDocumentationSocialFollowWidgetIsExcluded(t *testing.T) {
	html := `<html><head><title>Deployment reference</title></head><body><main class="documentation"><h1>Deployment reference</h1><p>Create a deployment by selecting an environment and providing an immutable revision identifier.</p><p>Verify the resulting status before directing production traffic to the new revision.</p><div class="social"><p>Follow us on social media for product announcements and company updates.</p></div></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/docs/deployments", WithPageType(PageTypeDocumentation))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Deployment reference", "immutable revision identifier", "Verify the resulting status"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing documentation content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Follow us", "social media", "company updates"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included social-follow widget %q: %s", unwanted, doc.Text)
		}
	}
}

func TestSelfPreviewPreservesIdentityQueryParameters(t *testing.T) {
	html := `<html><head><title>First query article</title><meta property="og:type" content="article"><link rel="canonical" href="https://example.com/post?id=1"></head><body><main><article><h1>First query article</h1><p>The primary article contains enough detail to establish its semantic content region.</p><p>Its second paragraph explains why query parameters identify distinct posts on this site.</p></article><aside class="preview-card"><a href="/post?id=2"><strong>Second query article</strong></a><p>This preview belongs to a different article and must not be mistaken for a self-preview.</p></aside><aside class="preview-card"><a href="/post?id=1&amp;utm_source=social"><strong>First query article</strong></a><p>This duplicate self-preview should be removed even when its link includes tracking.</p></aside></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/post?id=1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"belongs to a different article"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing distinct query-linked preview %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"duplicate self-preview", "link includes tracking"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included canonical self-preview %q: %s", unwanted, doc.Text)
		}
	}
}

func BenchmarkTrailingNeutralContainers(b *testing.B) {
	html := `<html><head><meta property="og:type" content="article"></head><body><main><article><h1>Large article</h1><p>The primary article provides enough prose for extraction before a large number of neutral layout containers.</p></article>` +
		strings.Repeat(`<div><p>Neutral trailing layout content remains inexpensive to classify.</p></div>`, 8000) + `</main></body></html>`
	input := []byte(html)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ExtractBytes(input, "https://example.com/articles/large"); err != nil {
			b.Fatal(err)
		}
	}
}

func TestStrongArticleSignalsOutweighRelatedContentCards(t *testing.T) {
	html := `<html><head><title>GitHub suddenly rejected my SSH key</title><meta property="og:type" content="article"><link rel="canonical" href="https://example.com/blog/github-rejected-ssh-key"></head><body><main><aside class="author-profile"><h2>Ada Example</h2><p>A software engineer writing about developer tools.</p></aside><article itemscope itemtype="https://schema.org/CreativeWork"><h1 itemprop="headline">GitHub suddenly rejected my SSH key</h1><p>Yesterday a working SSH key began failing without warning, even though the local configuration and repository permissions had not changed.</p><p>I compared the key fingerprint with the account settings, inspected the verbose client output, and found that the server was rejecting an outdated registration.</p><p>Removing that registration and uploading the current public key restored access. The diagnostic steps made the cause clear and avoided replacing unrelated credentials.</p></article><section class="related-content"><h2>You May Also Enjoy</h2><div class="story-card"><h3>Rotating deployment credentials</h3><p>This separate tutorial explains how another credential can be replaced safely.</p></div><div class="story-card"><h3>Debugging network access</h3><p>This unrelated excerpt discusses network diagnostics for remote services.</p></div><div class="story-card"><h3>Managing repository settings</h3><p>This recommendation describes settings on a different page.</p></div><div class="story-card"><h3>Understanding key formats</h3><p>This final related excerpt belongs to a fourth separate article.</p></div></section></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/read?id=42")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("page type = %q, want article", doc.PageType)
	}
	if !strings.HasPrefix(doc.Text, "GitHub suddenly rejected my SSH key") {
		t.Fatalf("article title was not first: %q", doc.Text)
	}
	for _, want := range []string{"working SSH key", "verbose client output", "restored access"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing article content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Ada Example", "You May Also Enjoy", "Rotating deployment credentials", "network diagnostics", "This recommendation", "final related excerpt"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included auxiliary content %q: %s", unwanted, doc.Text)
		}
	}
}

func TestJSONLDItemListEntriesDoNotMakePageAnArticle(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{"@type":"ItemList","itemListElement":[{"@type":"NewsArticle","headline":"First nested story","datePublished":"2025-01-01"},{"@type":"NewsArticle","headline":"Second nested story","datePublished":"2025-01-02"}]}</script></head><body><main><h1>Latest news</h1><ul><li><a href="/one">First nested story</a></li><li><a href="/two">Second nested story</a></li></ul></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/news")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeListing {
		t.Fatalf("page type = %q, want listing", doc.PageType)
	}
}

func TestMicrodataItemListEntriesDoNotMakePageAnArticle(t *testing.T) {
	html := `<main itemscope itemtype="https://schema.org/ItemList"><h1>News archive</h1><article itemprop="itemListElement" itemscope itemtype="https://schema.org/NewsArticle"><h2 itemprop="headline">First archived story</h2><p>A summary of the first archived story.</p></article><article itemprop="itemListElement" itemscope itemtype="https://schema.org/NewsArticle"><h2 itemprop="headline">Second archived story</h2><p>A summary of the second archived story.</p></article></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/news/archive")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeListing {
		t.Fatalf("page type = %q, want listing", doc.PageType)
	}
}

func TestSiblingMicrodataArticlesAreListingRecords(t *testing.T) {
	html := `<main><h1>Regional news archive</h1><div><article itemscope itemtype="https://schema.org/NewsArticle"><h2 itemprop="headline">Northern update</h2><p>The first regional report summarizes events in the north.</p></article><article itemscope itemtype="https://schema.org/NewsArticle"><h2 itemprop="headline">Southern update</h2><p>The second regional report summarizes events in the south.</p></article><article itemscope itemtype="https://schema.org/NewsArticle"><h2 itemprop="headline">Western update</h2><p>The third regional report summarizes events in the west.</p></article></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/news/archive")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeListing {
		t.Fatalf("page type = %q, want listing", doc.PageType)
	}
}

func TestPrimaryMicrodataArticleOutweighsSingleTeaser(t *testing.T) {
	html := `<main><article itemscope itemtype="https://schema.org/Article"><h1 itemprop="headline">Primary investigation</h1><p>The investigation begins with a detailed account of the observed behavior and the evidence collected during the initial review.</p><p>Further analysis explains the underlying cause, the tests used to confirm it, and the change that resolved the problem.</p></article><div class="story-card" itemscope itemtype="https://schema.org/NewsArticle"><h2 itemprop="headline">Unrelated news update</h2><p>This teaser excerpt belongs to another page and must not become primary content.</p></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/read/primary")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("page type = %q, want article", doc.PageType)
	}
	for _, want := range []string{"Primary investigation", "observed behavior", "underlying cause"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing primary article content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Unrelated news update", "teaser excerpt"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included teaser content %q: %s", unwanted, doc.Text)
		}
	}
}

func TestDivMicrodataArticleCanDominateSemanticTeaser(t *testing.T) {
	html := `<main><div itemscope itemtype="https://schema.org/Article"><h1 itemprop="headline">Primary report in a generic container</h1><p>The primary report provides a substantial description of the investigation, its evidence, and the circumstances that led to the final conclusion.</p><p>The follow-up analysis documents the verification process and explains why the resulting change solved the original problem reliably.</p></div><article class="story-card" itemscope itemtype="https://schema.org/NewsArticle"><h2 itemprop="headline">Separate teaser story</h2><p>This unrelated summary belongs to a linked story rather than the primary report.</p></article></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/reports/primary")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("page type = %q, want article", doc.PageType)
	}
	for _, want := range []string{"Primary report in a generic container", "substantial description", "verification process"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing primary article content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Separate teaser story", "unrelated summary"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included teaser content %q: %s", unwanted, doc.Text)
		}
	}
}

func TestWrappedSiblingMicrodataArticlesAreListingRecords(t *testing.T) {
	html := `<main><h1>Technology news archive</h1><ul><li><article itemscope itemtype="https://schema.org/NewsArticle"><h2 itemprop="headline">Hardware update</h2><p>The first archive entry summarizes a hardware announcement.</p></article></li><li><article itemscope itemtype="https://schema.org/NewsArticle"><h2 itemprop="headline">Software update</h2><p>The second archive entry summarizes a software announcement.</p></article></li><li><article itemscope itemtype="https://schema.org/NewsArticle"><h2 itemprop="headline">Network update</h2><p>The third archive entry summarizes a network announcement.</p></article></li></ul></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/technology/archive")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeListing {
		t.Fatalf("page type = %q, want listing", doc.PageType)
	}
	for _, want := range []string{"Hardware update", "Software update", "Network update"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing listing entry %q: %s", want, doc.Text)
		}
	}
}

func TestCyclicJSONLDMainEntityReferencesTerminate(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{"@graph":[{"@id":"#a","@type":"WebPage","mainEntity":{"@id":"#b"}},{"@id":"#b","@type":"Article","headline":"Cyclic article","mainEntity":{"@id":"#a"}}]}</script></head><body><main><h1>Cyclic article</h1><p>The page remains extractable despite malformed cyclic structured data.</p></main></body></html>`
	if _, err := ExtractBytes([]byte(html), "https://example.com/cycle"); err != nil {
		t.Fatal(err)
	}
}

func TestJSONLDIDResolutionPrefersCompleteEntity(t *testing.T) {
	article := `{"@id":"#article","@type":"Article","headline":"Resolved article"}`
	page := `{"@id":"#page","@type":"WebPage","mainEntity":{"@id":"#article"}}`
	for _, graph := range []string{article + `,` + page, page + `,` + article} {
		html := `<html><head><script type="application/ld+json">{"mainEntity":{"@id":"#article"},"@graph":[` + graph + `]}</script></head><body><main><p>The complete linked article entity supplies reliable page metadata.</p></main></body></html>`
		doc, err := ExtractBytes([]byte(html), "https://example.com/resolved")
		if err != nil {
			t.Fatal(err)
		}
		if doc.PageType != PageTypeArticle {
			t.Fatalf("page type = %q, want article", doc.PageType)
		}
		if doc.Title != "Resolved article" {
			t.Fatalf("title = %q, want resolved article headline", doc.Title)
		}
	}
}

func TestJSONLDIDResolutionMergesEqualPartialEntities(t *testing.T) {
	name := `{"@id":"#article","name":"Partial article"}`
	typeOf := `{"@id":"#article","@type":"Article"}`
	for _, graph := range []string{name + `,` + typeOf, typeOf + `,` + name} {
		html := `<html><head><script type="application/ld+json">{"mainEntity":{"@id":"#article"},"@graph":[` + graph + `]}</script></head><body><main><h1>Partial article</h1><p>The split JSON-LD entity still identifies this page consistently.</p></main></body></html>`
		doc, err := ExtractBytes([]byte(html), "https://example.com/partial")
		if err != nil {
			t.Fatal(err)
		}
		if doc.PageType != PageTypeArticle {
			t.Fatalf("page type = %q, want article", doc.PageType)
		}
	}
}

func TestCreativeWorkAloneIsNotAnArticleSignal(t *testing.T) {
	html := `<main itemscope itemtype="https://schema.org/CreativeWork"><h1>Acme drawing application</h1><p>A downloadable program for creating diagrams and illustrations.</p></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/software/acme")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType == PageTypeArticle {
		t.Fatalf("page type = %q, CreativeWork alone must not imply article", doc.PageType)
	}
}

func TestStandaloneRelatedResultsRemainAListing(t *testing.T) {
	html := `<main><h1>Related results</h1><section class="related-results"><div class="story-card"><h2>Result one</h2><p>The first matching record has useful details.</p></div><div class="story-card"><h2>Result two</h2><p>The second matching record has useful details.</p></div><div class="story-card"><h2>Result three</h2><p>The third matching record has useful details.</p></div></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/search/related")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeListing {
		t.Fatalf("page type = %q, want listing", doc.PageType)
	}
	for _, want := range []string{"Result one", "Result two", "Result three"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing primary listing record %q: %s", want, doc.Text)
		}
	}
}

func TestInferredListingKeepsCardsAfterFeaturedArticle(t *testing.T) {
	html := `<main><article class="featured"><h1>Featured story</h1><p>The featured story introduces this news index with a detailed account long enough to stand on its own while directing readers toward the rest of the current coverage.</p></article><section class="results"><article class="story-card"><h2>Story one</h2><p>The first story has useful listing details.</p></article><article class="story-card"><h2>Story two</h2><p>The second story has useful listing details.</p></article></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/news")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeListing {
		t.Fatalf("got page type %q, want listing", doc.PageType)
	}
	for _, want := range []string{"Featured story", "Story one", "first story", "Story two", "second story"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing inferred listing content %q: %s", want, doc.Text)
		}
	}
}

func TestListingKeepsCardsAfterFeaturedArticle(t *testing.T) {
	html := `<main><article class="featured"><h1>Featured product</h1><p>The featured product introduces this catalog and explains the collection in enough detail for shoppers to understand the available selection and its purpose.</p></article><section class="results"><article class="product-card"><h2>Product one</h2><p>The first product has useful listing details.</p></article><article class="product-card"><h2>Product two</h2><p>The second product has useful listing details.</p></article></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/products", WithPageType(PageTypeListing))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Featured product", "Product one", "first product", "Product two", "second product"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing listing content %q: %s", want, doc.Text)
		}
	}
}

func TestNonArticleShareAndReadMoreSectionsAreRetained(t *testing.T) {
	html := `<main><h1>Web API reference</h1><section><h2>Share</h2><p>The Share interface sends data to a user-selected destination and returns a promise.</p></section><section><h2>Read more</h2><p>The Read more component expands truncated documentation without navigating away.</p></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/docs/share", WithPageType(PageTypeDocumentation))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Share", "sends data", "Read more", "expands truncated documentation"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing non-article subject content %q: %s", want, doc.Text)
		}
	}
}

func TestTitleEquivalentDecorations(t *testing.T) {
	tests := []struct {
		name, heading, title, site string
		want                       bool
	}{
		{"site suffix with tilde", "Article title", "Article title ~ Site Name", "Site Name", true},
		{"site suffix with pipe", "Article title", "Article title | Site Name", "Site Name", true},
		{"site prefix with em dash", "Article title", "Site Name — Article title", "Site Name", true},
		{"year and site suffix", "Article title", "Article title in 2026 ~ Site Name", "Site Name", true},
		{"different subtitle", "Article title", "Article title | A different story", "Site Name", false},
		{"different title", "Article title", "Article title with additional details", "Site Name", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := titleEquivalent(tc.heading, tc.title, tc.site); got != tc.want {
				t.Fatalf("titleEquivalent(%q, %q, %q) = %v, want %v", tc.heading, tc.title, tc.site, got, tc.want)
			}
		})
	}
}

func TestArticleDecoratedBrowserTitleDoesNotDuplicateH1(t *testing.T) {
	html := `<html><head><title>95 reasons for having your own website in 2026 ~ Bell Kiosk</title><meta property="og:site_name" content="Bell Kiosk"><meta property="og:type" content="article"></head><body><article><h1>95 reasons for having your own website</h1><p>Having an independent website gives its owner a durable place to publish useful work and communicate directly with readers.</p><p>The article continues with practical reasons and examples that make the primary prose substantial enough for extraction.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://bellkiosk.website/blog/reasons-to-website.html", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(doc.Markdown, "# 95 reasons for having your own website") != 1 {
		t.Fatalf("decorated browser title duplicated the visible heading:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Markdown, "in 2026 ~ Bell Kiosk") {
		t.Fatalf("decorated browser title was emitted instead of the visible heading:\n%s", doc.Markdown)
	}
}

func TestArticleHeadingWinsWhenSiteNameDiffersFromTitleBranding(t *testing.T) {
	html := `<html><head><title>Article title | NYTimes.com</title><meta property="og:site_name" content="The New York Times"><meta property="og:type" content="article"></head><body><article><h1>Article title</h1><p>This selected article paragraph contains enough substantive reporting to establish that the enclosed heading labels the primary prose.</p><p>Further article content ensures the source heading and body are retained by extraction.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(doc.Text, "Article title") != 1 {
		t.Fatalf("differently branded browser title duplicated the article heading: %q", doc.Text)
	}
	if strings.Contains(doc.Text, "NYTimes.com") {
		t.Fatalf("browser title was emitted instead of the structural article heading: %q", doc.Text)
	}
}

func TestArticleKeepsAdjacentH1BelowScoreThreshold(t *testing.T) {
	html := `<html><head><title>Agent swarms and the new model economics | Cursor</title></head><body><header><h1>Agent swarms and the new model economics</h1></header><article><p>There are important changes in the cost of coordinating many capable software agents.</p><p>The article body remains selected because it contains useful explanatory prose.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Agent swarms and the new model economics\n") {
		t.Fatalf("article title was not restored before its body:\n%s", doc.Markdown)
	}
	if strings.Count(doc.Text, "Agent swarms and the new model economics") != 1 {
		t.Fatalf("article title was duplicated: %q", doc.Text)
	}
}

func TestSemanticArticleHeadingOverridesConflictingMetadata(t *testing.T) {
	html := `<html><head><title>–end-of-options | Example Site</title><meta property="og:title" content="–end-of-options"></head><body><article><h1 itemprop="name headline">--end-of-options</h1><p>This article explains how command line parsers recognize an explicit end marker and why the exact pair of ASCII hyphens matters.</p><p>The remaining discussion describes practical examples and compatibility considerations for programs that pass options through to another command.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# \\--end-of-options\n") {
		t.Fatalf("semantic source heading did not override conflicting metadata:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Markdown, "# –end-of-options") {
		t.Fatalf("conflicting metadata title was synthesized:\n%s", doc.Markdown)
	}
	if strings.Count(doc.Text, "--end-of-options") != 1 {
		t.Fatalf("source heading was duplicated: %q", doc.Text)
	}
}

func TestAbsoluteSchemaHeadlineOverridesConflictingMetadata(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attribute string
	}{
		{"absolute itemprop", `itemprop="https://schema.org/headline"`},
		{"absolute property", `property="http://schema.org/headline"`},
		{"fragment property", `property="https://schema.org/CreativeWork#headline"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html := `<html><head><title>Incorrect metadata title | Example Site</title><meta property="og:title" content="Incorrect metadata title"></head><body><article><h1 ` + tc.attribute + `>Correct source title</h1><p>This substantive article paragraph establishes that the absolute Schema.org headline labels the selected primary prose.</p><p>Additional explanatory prose ensures that metadata remains only a fallback rather than replacing the visible source heading.</p></article></body></html>`
			doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(doc.Markdown, "# Correct source title\n") {
				t.Fatalf("absolute Schema.org headline did not override metadata:\n%s", doc.Markdown)
			}
			if strings.Contains(doc.Text, "Incorrect metadata title") || strings.Count(doc.Text, "Correct source title") != 1 {
				t.Fatalf("metadata was synthesized or source heading was duplicated: %q", doc.Text)
			}
		})
	}
}

func TestSemanticMastheadOutsideArticleDoesNotOverrideMetadata(t *testing.T) {
	html := `<html><head><title>Actual Story | Example Site</title><meta property="og:title" content="Actual Story"></head><body><header><h1 itemprop="headline">Publisher Home</h1></header><article><p>The actual story body contains substantial reporting about the event, the evidence gathered by investigators, and the conclusions they reached.</p><p>A second paragraph provides enough selected prose to establish the article region without relying on the unrelated site masthead.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Actual Story | Example Site\n") {
		t.Fatalf("metadata fallback was not used:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Text, "Publisher Home") {
		t.Fatalf("site masthead was selected in addition to metadata: %q", doc.Text)
	}
}

func TestArticleDoesNotRestoreAdjacentSiteMasthead(t *testing.T) {
	html := `<html><head><title>Actual Story</title></head><body><header><h1>Example News</h1></header><article><p>The actual story body contains useful reporting and explanatory prose.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Actual Story\n") {
		t.Fatalf("metadata title was not used:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Text, "Example News") {
		t.Fatalf("adjacent site masthead was restored as an article title: %q", doc.Text)
	}
}

func TestArticleSitePrefixedTitleDoesNotDuplicateH1(t *testing.T) {
	html := `<html><head><title>Cursor | Agent swarms and the new model economics</title></head><body><header><h1>Agent swarms and the new model economics</h1></header><article><p>There are important changes in the cost of coordinating many capable software agents.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(doc.Text, "Agent swarms and the new model economics") != 1 {
		t.Fatalf("site-prefixed browser title duplicated the source h1: %q", doc.Text)
	}
	if strings.Contains(doc.Text, "Cursor |") {
		t.Fatalf("site-prefixed metadata title was unnecessarily injected: %q", doc.Text)
	}
}

func TestArticlePrependsMetadataTitleWhenHeadingIsMissing(t *testing.T) {
	html := `<html><head><title>How to pack ternary numbers in 8-bit bytes</title></head><body><article><p>There are 3 possible values for each ternary digit in the packed representation.</p><p>The remaining article explains the encoding and decoding procedure in detail.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# How to pack ternary numbers in 8-bit bytes\n") {
		t.Fatalf("metadata title was not prepended:\n%s", doc.Markdown)
	}
}

func TestOversizedMetadataTitleDoesNotHideArticleBody(t *testing.T) {
	html := `<html><head><title>` + strings.Repeat("A very long title ", 10) + `</title></head><body><article><p>Short valid article body.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle), WithMaxOutputBytes(30))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "Short valid article body.") {
		t.Fatalf("oversized synthesized title hid the article body: %q", doc.Markdown)
	}
}

func TestRestoredSourceTitleDoesNotConsumeBodyBudget(t *testing.T) {
	html := `<html><head><title>Short source title</title></head><body><header><h1>Short source title</h1></header><article><p>Body fits by itself.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle), WithMaxOutputBytes(30))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "Body fits by itself.") {
		t.Fatalf("restored source title consumed the article budget: %q", doc.Markdown)
	}
	if strings.Contains(doc.Text, "Short source title") {
		t.Fatalf("source title should have been omitted to retain body content: %q", doc.Markdown)
	}
}

func TestFittingMetadataTitleDoesNotConsumeBodyBudget(t *testing.T) {
	html := `<html><head><title>123456789012345678901</title></head><body><article><p>Short valid body.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle), WithMaxOutputBytes(30))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "Short valid body.") {
		t.Fatalf("fitting synthesized title consumed the article budget: %q", doc.Markdown)
	}
	if strings.Contains(doc.Text, "123456789012345678901") {
		t.Fatalf("synthetic title should have been omitted to retain body content: %q", doc.Markdown)
	}
}

func TestSyntheticTitleAndSectionHeadingDoNotDisplaceProse(t *testing.T) {
	html := `<html><head><title>Small metadata title</title></head><body><article><h2>Part</h2><p>Prose survives alone.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle), WithMaxOutputBytes(32))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "Prose survives alone.") {
		t.Fatalf("headings displaced all substantive article content: %q", doc.Markdown)
	}
	if strings.Contains(doc.Text, "Small metadata title") {
		t.Fatalf("synthetic title should have been omitted to retain prose: %q", doc.Markdown)
	}
}

func TestArticleDoesNotDuplicateSurvivingTitleEquivalentHeading(t *testing.T) {
	html := `<html><head><title>Refactoring English</title></head><body><article><h2>Refactoring English</h2><p>This article has enough useful prose to remain in the selected output.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(doc.Text, "Refactoring English") != 1 {
		t.Fatalf("surviving title-equivalent heading was duplicated: %q", doc.Text)
	}
}

func TestArticlePromotesMetadataEquivalentH2OverSiteH1(t *testing.T) {
	html := `<html><head><meta property="og:title" content="A Specific Article Title"><meta property="og:type" content="article"></head><body><h1>Blog</h1><main><h2 class="post-title">A Specific Article Title</h2><div class="post-meta">July 21, 2026</div><div class="post-content"><p>A substantial opening paragraph explains the subject with enough detail to identify the primary article prose.</p><p>More article prose remains present after the correct headline has been recovered.</p></div></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/blog/specific-article")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# A Specific Article Title\n") {
		t.Fatalf("h2 article headline was not promoted:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Text, "Blog") {
		t.Fatalf("site masthead was retained in article output:\n%s", doc.Markdown)
	}
	for _, want := range []string{"substantial opening paragraph", "More article prose"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing body content %q: %s", want, doc.Text)
		}
	}
}

func TestStructurallyMarkedH2OverridesConflictingMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, marker string
	}{
		{"schema headline", `itemprop="headline"`},
		{"article title class", `class="article-title"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html := `<html><head><meta property="og:title" content="Incorrect Metadata Title"><meta property="og:type" content="article"></head><body><main><h2 ` + tc.marker + `>Correct Structural Headline</h2><p>This substantial opening paragraph establishes that the marked heading labels the selected primary article prose.</p><p>More article content confirms the source headline through its close structural relationship with the body.</p></main></body></html>`
			doc, err := ExtractBytes([]byte(html), "https://example.com/blog/structural-headline")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(doc.Markdown, "# Correct Structural Headline\n") {
				t.Fatalf("structurally marked h2 did not override metadata:\n%s", doc.Markdown)
			}
			if strings.Contains(doc.Text, "Incorrect Metadata Title") {
				t.Fatalf("conflicting metadata title was emitted: %q", doc.Text)
			}
			if strings.Count(doc.Text, "Correct Structural Headline") != 1 {
				t.Fatalf("source headline was duplicated: %q", doc.Text)
			}
		})
	}
}

func TestArticleDoesNotPromoteUnrelatedDistantH2(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Actual Article Title"><meta property="og:type" content="article"></head><body><main><header><h2>Unrelated Feature</h2></header><div>Short layout label one</div><div>Short layout label two</div><div>Short layout label three</div><article><p>The actual article opens with substantial prose that clearly establishes the primary content region for extraction.</p><p>Its second paragraph remains available and provides more relevant details for the reader.</p></article></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/blog/actual-article")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Actual Article Title\n") {
		t.Fatalf("distant unrelated h2 replaced metadata title:\n%s", doc.Markdown)
	}
	if strings.Contains(strings.SplitN(doc.Markdown, "\n\n", 2)[0], "Unrelated Feature") {
		t.Fatalf("unrelated h2 was used as title:\n%s", doc.Markdown)
	}
	if !strings.Contains(doc.Text, "actual article opens") {
		t.Fatalf("article body was lost: %s", doc.Text)
	}
}

func TestNestedAuxiliaryRegionDoesNotExcludeSharedLayout(t *testing.T) {
	html := `<main><div class="layout"><aside><h2>On this page</h2><ul><li>Overview</li></ul></aside><article><h1>Actual article</h1><p>The article contains the relevant details that readers need.</p></article></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Actual article", "relevant details"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"On this page", "Overview"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included %q: %s", unwanted, doc.Text)
		}
	}
}

func TestDivSidebarDoesNotExcludeSharedLayout(t *testing.T) {
	html := `<main><div class="layout"><div class="sidebar"><h2>On this page</h2><ul><li>Overview</li></ul></div><article><h1>Actual article</h1><p>The article remains relevant when a div-based sidebar comes first.</p></article></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Actual article", "article remains relevant"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"On this page", "Overview"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included %q: %s", unwanted, doc.Text)
		}
	}
}

func TestSiblingHeaderDoesNotExcludeSharedLayout(t *testing.T) {
	html := `<main><div class="layout"><header><h2>On this page</h2></header><article><h1>Actual article</h1><p>The article remains relevant when an auxiliary header comes first.</p></article></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Actual article", "article remains relevant"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing %q: %s", want, doc.Text)
		}
	}
	if strings.Contains(doc.Text, "On this page") {
		t.Errorf("included auxiliary header: %s", doc.Text)
	}
}

func TestSubjectNamedLikeBoilerplateIsRetained(t *testing.T) {
	html := `<main><h1>Authentication reference</h1><section id="login"><h2>Login API</h2><p>The login endpoint exchanges user credentials for an access token.</p></section><section class="related"><h2>Related records</h2><p>The related records field connects an account to its organization.</p></section><section id="advertisement"><h2>Advertisement model</h2><p>The advertisement model documents campaign properties and status values.</p></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/docs/auth", WithPageType(PageTypeDocumentation))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Login API", "login endpoint", "Related records", "related records field", "Advertisement model", "campaign properties"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing subject content %q: %s", want, doc.Text)
		}
	}
}

func TestStructuralNamesUsedAsDocumentationSubjectsAreRetained(t *testing.T) {
	html := `<main><h1>Component reference</h1><section id="pagination"><h2>Pagination</h2><p>The pagination component divides large result sets into separate pages.</p></section><section id="toolbar"><h2>Toolbar</h2><p>The toolbar component groups commands used to edit a document.</p></section><section id="breadcrumb"><h2>Breadcrumb</h2><p>The breadcrumb component displays the current location within a hierarchy.</p></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/docs/components", WithPageType(PageTypeDocumentation))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Pagination", "divides large result sets", "Toolbar", "groups commands", "Breadcrumb", "current location"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing component documentation %q: %s", want, doc.Text)
		}
	}
}

func TestInteractiveToolbarDocumentationIsRetained(t *testing.T) {
	html := `<main><h1>Component reference</h1><section id="toolbar"><h2>Toolbar</h2><p>The toolbar groups editing commands for the document.</p><div role="toolbar"><button>Bold</button><button>Italic</button></div><p>Use arrow keys to move between commands.</p></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/docs/toolbar", WithPageType(PageTypeDocumentation))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Toolbar", "groups editing commands", "Use arrow keys"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing interactive documentation %q: %s", want, doc.Text)
		}
	}
}

func TestExcludedCallsToActionInStructuredFallbacks(t *testing.T) {
	html := `<main><h1>Reference</h1><dl><dt>Option</dt><dd>Useful explanation <a href="/details">Read more</a></dd></dl><table><tr><td>Useful table value <a href="/table-details">Read more</a></td></tr></table></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/docs/reference", WithPageType(PageTypeDocumentation), WithIncludeTables(false))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Useful explanation", "Useful table value"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing %q: %s", want, doc.Text)
		}
	}
	if strings.Contains(doc.Text, "Read more") {
		t.Errorf("included excluded call to action: %s", doc.Text)
	}
}

func TestTOCOnlySemanticFallbackProducesNoContent(t *testing.T) {
	html := `<main><div id="toc"><h2>Contents</h2><ul><li>Install</li><li>Configure</li></ul></div></main>`
	_, err := ExtractBytes([]byte(html), "https://example.com/docs/empty", WithPageType(PageTypeDocumentation))
	if !errors.Is(err, ErrNoContent) {
		t.Fatalf("got %v, want ErrNoContent", err)
	}
}

func TestWrappedAuxiliaryHeadingExcludesSection(t *testing.T) {
	html := `<main><article><h1>Primary report</h1><p>The report contains the relevant findings and conclusions.</p></article><section><div class="section-title"><h2>More news</h2></div><div class="cards"><h3>Unrelated update</h3><p>This card describes a different news story.</p></div></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/report", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Primary report", "relevant findings"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"More news", "Unrelated update", "different news story"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included %q: %s", unwanted, doc.Text)
		}
	}
}

func TestTokenLabeledAuxiliaryRegionIsRemoved(t *testing.T) {
	html := `<main><h1>Installation</h1><p>Install the package with the command below.</p><div id="toc"><h2>Contents</h2><ul><li>Install</li><li>Configure</li></ul></div><div role="complementary"><h2>Related guides</h2><p>Upgrade an older system.</p></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/docs/install", WithPageType(PageTypeDocumentation))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "Install the package") {
		t.Fatal(doc.Text)
	}
	for _, unwanted := range []string{"Contents", "Configure", "Related guides", "Upgrade an older"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included %q: %s", unwanted, doc.Text)
		}
	}
}

func TestCodeHeavyPublishedPostIsInferredAsArticle(t *testing.T) {
	html := `<html><head><title>Building a small compiler</title><meta property="article:published_time" content="2025-01-10"><link rel="canonical" href="https://example.com/blog/small-compiler"></head><body><article><h1>Building a small compiler</h1><p>We started by defining the language and explaining the constraints that shaped its implementation.</p><pre>type Token struct { Kind int }</pre><p>The lexer keeps source locations so later stages can produce useful diagnostics for readers.</p><pre>func lex(source string) []Token</pre><p>Parsing then turns the token stream into a compact syntax tree while preserving error context.</p><pre>func parse(tokens []Token) Node</pre><p>The evaluator walks that tree and records values in a deliberately small lexical environment.</p><pre>func eval(node Node) Value</pre><p>Several implementation details changed after testing the compiler against larger real programs.</p><pre>go test ./...</pre><p>The final design remains understandable, and these tradeoffs are useful beyond this particular project.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/read?id=42")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("page type = %q, want article", doc.PageType)
	}
}

func TestAnnotatedPublishedPostIsNotInferredAsDiscussion(t *testing.T) {
	html := `<html><head><title>Incident review and lessons learned</title><meta name="datePublished" content="2025-02-12"><link rel="canonical" href="https://example.com/articles/incident-review"></head><body><article><h1>Incident review and lessons learned</h1><p>The team reconstructed the incident from logs and interviews, then compared that timeline with the expected behavior.</p><div class="comment"><p>The founder noted that this decision was reasonable given the information available at the time.</p></div><p>The first failure increased load gradually, which kept the underlying problem hidden during the initial response.</p><div class="comment"><p>An engineer added context about the alert threshold and why it had originally been selected.</p></div><p>Once the impact was understood, responders reduced traffic and restored the affected data from a verified copy.</p><div class="comment"><p>The founder clarified that recovery speed mattered less than preserving a complete and auditable record.</p></div><p>The follow-up work now focuses on simpler operating procedures and earlier warnings for the same failure mode.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/archive/42")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("page type = %q, want article", doc.PageType)
	}
}

func TestTimestampedCommentsAreInferredAsDiscussion(t *testing.T) {
	html := `<main><h1>Configuration question</h1><div class="comment"><h2>Ada</h2><time datetime="2025-03-01T10:00:00Z">March 1</time><p>I tried the default configuration, but the worker still stops before processing the queued item.</p></div><div class="comment"><h2>Ben</h2><time datetime="2025-03-01T10:05:00Z">March 1</time><p>Set the worker limit before starting the process, then retry the same queued item.</p></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/conversation/42")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeDiscussion {
		t.Fatalf("page type = %q, want discussion", doc.PageType)
	}
}

func TestProseHeavyDocumentationContainerIsInferredAsDocumentation(t *testing.T) {
	html := `<html><head><title>Working with environments</title><link rel="canonical" href="https://example.com/guide/environments"></head><body><main class="documentation"><h1>Working with environments</h1><p>An environment groups the settings and resources used when an application runs. Keeping these values together makes deployments repeatable and gives operators one place to inspect changes before applying them.</p><p>Create an environment before adding a deployment target. Choose a stable name that describes its purpose, because the same identifier appears in command output, audit records, and configuration files shared by the team.</p><p>Variables can be defined for the whole environment or overridden for one target. General values provide useful defaults, while narrow overrides let a deployment connect to resources that exist only in its region.</p><p>Review pending changes before activation. The preview includes additions, removals, and replacements, allowing an operator to catch an incorrect value without modifying a running application or interrupting current work.</p><p>After activation, verify the reported revision and run a health check from each target. If verification fails, restore the previous revision and inspect the event record before attempting another update.</p><p>Access should be granted through team roles rather than shared credentials. This keeps the audit history useful and allows permissions to be removed promptly when responsibilities change.</p></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/guide/environments")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeDocumentation {
		t.Fatalf("page type = %q, want documentation", doc.PageType)
	}
}

func TestArticleCommentRegionIsProfileSpecific(t *testing.T) {
	source, err := os.ReadFile("testdata/article-with-comments.html")
	if err != nil {
		t.Fatal(err)
	}

	articleOptions := [][]Option{nil, {WithPageType(PageTypeArticle)}}
	for _, opts := range articleOptions {
		doc, err := ExtractBytes(source, "https://example.com/2026/07/20/careful-measurements/", opts...)
		if err != nil {
			t.Fatal(err)
		}
		if doc.PageType != PageTypeArticle {
			t.Fatalf("page type = %q, want article", doc.PageType)
		}
		if !strings.Contains(doc.Text, "central conclusion of the article") {
			t.Errorf("article prose missing: %s", doc.Text)
		}
		for _, unwanted := range []string{"Responses", "Ada's comment", "Ben's comment", "Reply", "Like", "Leave a comment", "Post comment"} {
			if strings.Contains(doc.Text, unwanted) {
				t.Errorf("article included %q: %s", unwanted, doc.Text)
			}
		}
	}

	discussion, err := ExtractBytes(source, "https://example.com/2026/07/20/careful-measurements/", WithPageType(PageTypeDiscussion))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Ada's comment", "Ben's comment"} {
		if !strings.Contains(discussion.Text, want) {
			t.Errorf("discussion missing %q: %s", want, discussion.Text)
		}
	}
}

func TestWordPressArticleBodyIsNotExcludedWithSiblingComments(t *testing.T) {
	html := `<html><head><meta property="og:type" content="article"><meta property="og:title" content="Example article"></head><body><div id="content" role="main"><div id="post-123" class="post type-post hentry"><h2 class="entry-title">Example article</h2><div class="entry-content"><p>This is a substantial opening paragraph that clearly introduces the article and its central subject.</p><p>This is another substantial article paragraph that develops the subject with useful supporting detail.</p></div></div><div id="comments"><h3>19 Responses to Example article</h3><ol class="commentlist"><li class="comment"><p>A reader response that should not appear in the extracted article.</p></li><li class="comment"><p>Another reader response that should also be excluded from the article.</p></li></ol></div></div></body></html>`

	for _, tc := range []struct {
		name string
		opts []Option
	}{
		{name: "inferred"},
		{name: "explicit", opts: []Option{WithPageType(PageTypeArticle)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ExtractBytes([]byte(html), "https://example.com/2026/07/20/example-article/", tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			if doc.PageType != PageTypeArticle {
				t.Fatalf("page type = %q, want article", doc.PageType)
			}
			for _, want := range []string{"Example article", "substantial opening paragraph"} {
				if !strings.Contains(doc.Text, want) {
					t.Errorf("article content %q missing: %s", want, doc.Text)
				}
			}
			for _, unwanted := range []string{"A reader response", "Another reader response", "19 Responses"} {
				if strings.Contains(doc.Text, unwanted) {
					t.Errorf("reader comments included %q: %s", unwanted, doc.Text)
				}
			}
		})
	}
}

func TestShortRepeatedCommentsAreRemovedFromArticle(t *testing.T) {
	html := `<main><article><h1>A complete field report</h1><p>The report records the observations, explains the method used to verify them, and presents the conclusion supported by the collected evidence.</p></article><div class="feedback"><div class="comment"><p>Thanks!</p></div><div class="comment"><p>Well written.</p></div></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/articles/field-report", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "The report records the observations") {
		t.Fatalf("article prose missing: %s", doc.Text)
	}
	for _, unwanted := range []string{"Thanks!", "Well written."} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("short comment %q was retained: %s", unwanted, doc.Text)
		}
	}
}

func TestResponsesTokenDoesNotRemoveArticleSection(t *testing.T) {
	html := `<main><article><h1>Survey findings</h1><p>The survey compared results across several groups and checked each aggregate against the complete response data.</p><section id="responses"><h2>Responses by age group</h2><p>Primary survey results show that participation increased in every age group included in the study.</p></section></article></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/articles/survey-findings", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Responses by age group", "Primary survey results"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("article section content %q was removed: %s", want, doc.Text)
		}
	}
}

func TestReplyControlsDoNotMakeArticleSectionACommentRegion(t *testing.T) {
	html := `<main><article><h1>Interactive annotations</h1><p>The introduction explains how readers can inspect annotations while preserving the complete argument presented by the author.</p><section><h2>Reviewing the evidence</h2><p>This section contains primary article prose that must remain even though its interactive annotations provide reply actions.</p><a class="reply" href="#first">Reply to first annotation</a><a class="reply" href="#second">Reply to second annotation</a></section></article></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/articles/interactive-annotations", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "This section contains primary article prose") {
		t.Fatalf("article section was removed: %s", doc.Text)
	}
}

func TestDiscussionKeepsComments(t *testing.T) {
	html := `<main><h1>How?</h1><article class="post"><p>The question has useful detail.</p></article><article class="comment"><h2>Ada</h2><p>Use the documented method.</p><button>Reply</button></article><article class="comment"><h2>Bob</h2><p>This answer adds an example.</p></article></main>`
	d, e := ExtractBytes([]byte(html), "https://example.com/forum/1", WithPageType(PageTypeDiscussion))
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"The question", "Use the documented", "This answer"} {
		if !strings.Contains(d.Text, s) {
			t.Errorf("missing %q", s)
		}
	}
	if strings.Contains(d.Text, "Reply") {
		t.Error("included control")
	}
}

func TestMalformedHTTPDestinationIsPlainText(t *testing.T) {
	doc, err := ExtractBytes([]byte(`<main><p><a href="http:opaque">Label</a> and useful text.</p></main>`), "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.Markdown, "](http:") || !strings.Contains(doc.Text, "Label") {
		t.Fatal(doc.Markdown)
	}
}

func TestURLControlCharactersAreRejected(t *testing.T) {
	html := `<main><p><a href="java&#10;script:bad">scheme</a> <a href="https://exa&#9;mple.com/path">host</a> <a href="https://example.com/a&#10;b">path</a> <a href="https://example.com/?a=one&#13;two">query</a> <a href="/safe">safe</a></p></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/base", WithDiagnostics(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Links) != 1 || doc.Links[0].URL != "https://example.com/safe" {
		t.Fatalf("unsafe links were retained: %#v", doc.Links)
	}
	if len(doc.Diagnostics.RejectedLinks) < 4 {
		t.Fatalf("missing rejected links: %#v", doc.Diagnostics.RejectedLinks)
	}
	for _, label := range []string{"scheme", "host", "path", "query", "safe"} {
		if !strings.Contains(doc.Text, label) {
			t.Errorf("missing plain label %q", label)
		}
	}
}

func TestMaxRepeatedDoesNotTruncateProseSiblings(t *testing.T) {
	var paragraphs strings.Builder
	for i := 0; i < 8; i++ {
		paragraphs.WriteString(`<p>Prose paragraph with enough useful content number `)
		paragraphs.WriteByte(byte('0' + i))
		paragraphs.WriteString(`.</p>`)
	}
	html := `<main><h1>Long article</h1>` + paragraphs.String() + `<h2>What comes next?</h2><p>A final prose paragraph follows the section heading.</p></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle), WithMaxRepeatedItems(3))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "number 7") || !strings.Contains(doc.Text, "What comes next?") {
		t.Fatalf("prose was truncated: %s", doc.Text)
	}
	for _, warning := range doc.Warnings {
		if warning.Code == "repeated-items-truncated" {
			t.Fatalf("unexpected repetition warning: %#v", warning)
		}
	}
	if doc.Stats.SelectedBlocks != 11 {
		t.Fatalf("selected blocks = %d, want 11", doc.Stats.SelectedBlocks)
	}
}

func TestMaxRepeatedLimitsListingRecordsAndWarns(t *testing.T) {
	html := `<main><h1>Results</h1><div class="item"><p>First listed record.</p></div><div class="item"><p>Second listed record.</p></div><div class="item"><p>Third listed record.</p></div><div class="item"><p>Fourth listed record.</p></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/results", WithPageType(PageTypeListing), WithMaxRepeatedItems(2))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "First listed") || !strings.Contains(doc.Text, "Second listed") || strings.Contains(doc.Text, "Third listed") {
		t.Fatalf("unexpected listing output: %s", doc.Text)
	}
	if doc.Stats.SelectedBlocks != 3 { // heading and two emitted records
		t.Fatalf("selected blocks = %d, want 3", doc.Stats.SelectedBlocks)
	}
	if len(doc.Warnings) != 1 || doc.Warnings[0].Code != "repeated-items-truncated" {
		t.Fatalf("missing repetition warning: %#v", doc.Warnings)
	}
}

func TestMaxRepeatedLimitsRecordsInsideListsAndTables(t *testing.T) {
	tests := []struct {
		name, body, kept, dropped string
	}{
		{
			name:    "list items",
			body:    `<ul><li class="item">First list record</li><li class="item">Second list record</li><li class="item">Third list record</li></ul>`,
			kept:    "Second list record",
			dropped: "Third list record",
		},
		{
			name:    "table rows",
			body:    `<table><tr><th>Name</th><th>Value</th></tr><tr class="result"><td>First row</td><td>One</td></tr><tr class="result"><td>Second row</td><td>Two</td></tr><tr class="result"><td>Third row</td><td>Three</td></tr></table>`,
			kept:    "Second row",
			dropped: "Third row",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			html := `<main><h1>Results</h1>` + test.body + `</main>`
			doc, err := ExtractBytes([]byte(html), "https://example.com/results", WithPageType(PageTypeListing), WithMaxRepeatedItems(2))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(doc.Text, test.kept) || strings.Contains(doc.Text, test.dropped) {
				t.Fatalf("unexpected output: %s", doc.Text)
			}
			if len(doc.Warnings) != 1 || doc.Warnings[0].Code != "repeated-items-truncated" {
				t.Fatalf("missing repetition warning: %#v", doc.Warnings)
			}
			if doc.Stats.SelectedBlocks != 2 { // heading and list/table block
				t.Fatalf("selected blocks = %d, want 2", doc.Stats.SelectedBlocks)
			}
		})
	}
}

func TestImageOnlyArticleBlocks(t *testing.T) {
	html := `<main><article>
		<h1>How the system works</h1>
		<p>This article explains the system architecture and the path each request takes through its components.</p>
		<p><img src="images/architecture.png" alt="Request flow through the system" width="800" height="450"></p>
		<p>The diagram above connects the first stage to the processing stage described in the following section.</p>
		<figure><img src="/images/result.png" alt="Result of the processing pipeline"><figcaption>The completed processing pipeline.</figcaption></figure>
		<p><img src="/images/divider.png" alt=""></p>
		<aside class="author-profile"><img src="/people/author.jpg" alt="Jane Doe" width="128" height="128"><p>About the author</p></aside>
	</article></main>`

	doc, err := ExtractBytes([]byte(html), "https://example.com/posts/entry/", WithPageType(PageTypeArticle), WithIncludeImages(true))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`![Request flow through the system](https://example.com/posts/entry/images/architecture.png)`,
		`![Result of the processing pipeline](https://example.com/images/result.png)`,
	} {
		if !strings.Contains(doc.Markdown, want) {
			t.Fatalf("missing content image %q:\n%s", want, doc.Markdown)
		}
	}
	if len(doc.Images) != 2 {
		t.Fatalf("images = %#v, want two content images", doc.Images)
	}
	if doc.Images[0].Alt != "Request flow through the system" || doc.Images[0].URL != "https://example.com/posts/entry/images/architecture.png" ||
		doc.Images[1].Alt != "Result of the processing pipeline" || doc.Images[1].URL != "https://example.com/images/result.png" {
		t.Fatalf("unexpected images: %#v", doc.Images)
	}
	for _, unwanted := range []string{"author.jpg", "divider.png", "Jane Doe"} {
		if strings.Contains(doc.Markdown, unwanted) {
			t.Fatalf("auxiliary image %q survived:\n%s", unwanted, doc.Markdown)
		}
	}

	without, err := ExtractBytes([]byte(html), "https://example.com/posts/entry/", WithPageType(PageTypeArticle), WithIncludeImages(false))
	if err != nil {
		t.Fatal(err)
	}
	if len(without.Images) != 0 || strings.Contains(without.Markdown, "![") || strings.Contains(without.Markdown, "architecture.png") || strings.Contains(without.Markdown, "result.png") {
		t.Fatalf("images survived WithIncludeImages(false): %#v\n%s", without.Images, without.Markdown)
	}
}

func TestUnwrappedArticleImageIsSelected(t *testing.T) {
	html := `<article>
		<p>Introductory prose explains the system before presenting its architecture and major components.</p>
		<img src="/diagram.png" alt="System architecture" width="900" height="500">
		<p>Following prose explains how requests move through the architecture shown above.</p>
	</article>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle), WithIncludeImages(true))
	if err != nil {
		t.Fatal(err)
	}
	want := `![System architecture](https://example.com/diagram.png)`
	if !strings.Contains(doc.Markdown, want) {
		t.Fatalf("missing direct article image %q:\n%s", want, doc.Markdown)
	}
	if len(doc.Images) != 1 || doc.Images[0].Alt != "System architecture" || doc.Images[0].URL != "https://example.com/diagram.png" {
		t.Fatalf("unexpected images: %#v", doc.Images)
	}
}

func TestAccessibleSVGInternalsRemainOpaque(t *testing.T) {
	internal := strings.Repeat("INTERNAL CHART METADATA SHOULD NOT AFFECT EXTRACTION ", 100)
	html := `<article>
		<p>Introductory prose establishes the article context before the diagram.</p>
		<svg role="img" aria-label="Request lifecycle"><text>` + internal + `</text><a href="/internal-link"><text>hidden link</text></a></svg>
		<p>Following prose explains the request lifecycle after the diagram.</p>
	</article>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle), WithIncludeImages(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Markdown, "Diagram: Request lifecycle") {
		t.Fatalf("missing accessible SVG label:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Markdown, "INTERNAL") || strings.Contains(doc.Text, "INTERNAL") || strings.Contains(doc.Markdown, "internal-link") {
		t.Fatalf("SVG internals affected extraction:\n%s\ntext=%q", doc.Markdown, doc.Text)
	}
	baseline, err := ExtractBytes([]byte(strings.Replace(html, internal, "", 1)), "https://example.com/article", WithPageType(PageTypeArticle), WithIncludeImages(true))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Quality != baseline.Quality || doc.PageType != baseline.PageType {
		t.Fatalf("SVG internals changed analysis: quality %v vs %v, type %v vs %v", doc.Quality, baseline.Quality, doc.PageType, baseline.PageType)
	}
}

func TestEmptyAuxiliarySectionHeadingIsPruned(t *testing.T) {
	html := `<main><h1>SSH tunnel guide</h1><p>This substantive introduction explains how to configure and use an SSH tunnel safely.</p><h2>Installation</h2><div><p>Run the installer.</p></div><section class="webmentions"><h2>Web mentions</h2><div id="webmentions"></div></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/guide", WithPageType(PageTypeDocumentation))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.Markdown, "Web mentions") || strings.Contains(doc.Text, "Web mentions") {
		t.Fatalf("empty section heading survived:\n%s", doc.Markdown)
	}
	if !strings.Contains(doc.Markdown, "## Installation\n\nRun the installer.") {
		t.Fatalf("heading with nested content was removed:\n%s", doc.Markdown)
	}
	for _, section := range doc.Sections {
		if section.Heading == "Web mentions" {
			t.Fatalf("pruned heading survived in Document.Sections: %#v", doc.Sections)
		}
	}
}

func TestTruncationKeepsViewsConsistent(t *testing.T) {
	html := `<main><h1>Title</h1><p><a href="/kept">Kept link</a> <img src="/kept.png" alt="Kept image"> short text.</p><p><a href="/omitted">Omitted link</a> <img src="/omitted.png" alt="Omitted image"> ` + strings.Repeat("long ", 30) + `</p></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com", WithPageType(PageTypeDocumentation), WithIncludeImages(true), WithMaxOutputBytes(180))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Markdown, "Kept link") || strings.Contains(doc.Markdown, "Omitted") {
		t.Fatalf("unexpected output:\n%s", doc.Markdown)
	}
	if len(doc.Links) != 1 || strings.Contains(doc.Links[0].URL, "omitted") {
		t.Fatalf("links do not match output: %#v", doc.Links)
	}
	if len(doc.Images) != 1 || strings.Contains(doc.Images[0].URL, "omitted") {
		t.Fatalf("images do not match output: %#v", doc.Images)
	}
	for _, section := range doc.Sections {
		if strings.Contains(section.Heading, "Omitted") || strings.Contains(section.Text, "Omitted") {
			t.Fatalf("sections do not match output: %#v", doc.Sections)
		}
	}
	if strings.Contains(doc.Text, "Omitted") {
		t.Fatalf("text does not match output: %q", doc.Text)
	}
}

func TestLimitsAndInvalidURL(t *testing.T) {
	_, e := ExtractBytes([]byte(strings.Repeat("x", 20)), "", WithMaxInputBytes(10))
	var le *LimitError
	if !errors.As(e, &le) {
		t.Fatalf("got %v", e)
	}
	_, e = ExtractBytes([]byte(`<p>text</p>`), "/relative")
	if !errors.Is(e, ErrInvalidURL) {
		t.Fatalf("got %v", e)
	}
	_, e = ExtractBytes([]byte(`<div><div><p>deep text here</p></div></div>`), "", WithMaxDepth(2))
	if !errors.Is(e, ErrLimit) {
		t.Fatalf("got %v", e)
	}
}

func TestOutputLimitIsUTF8Safe(t *testing.T) {
	html := `<main><h1>Title</h1><p>` + strings.Repeat("界", 100) + `</p><p>later block</p></main>`
	d, e := ExtractBytes([]byte(html), "", WithMaxOutputBytes(40))
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Markdown) > 40 {
		t.Fatalf("output has %d bytes", len(d.Markdown))
	}
	if !strings.Contains(d.Markdown, "Title") {
		t.Fatal(d.Markdown)
	}
	if len(d.Warnings) == 0 {
		t.Fatal("missing truncation warning")
	}
}

func TestMetadataFallback(t *testing.T) {
	html := `<html><head><title>Client App</title><meta name="description" content="Content supplied for clients without script execution."></head><body><div id="app"></div></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/app", WithDiagnostics(true))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Diagnostics.Fallback != "metadata" || !strings.Contains(doc.Text, "Content supplied") {
		t.Fatalf("%#v", doc)
	}
}

func TestJSONLDMetadata(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{"@type":"Article","author":{"name":"Grace"},"datePublished":"2024-01-02"}</script></head><body><main><h1>Long article title</h1><p>This is enough useful article text for extraction and metadata verification.</p></main></body></html>`
	d, e := ExtractBytes([]byte(html), "")
	if e != nil {
		t.Fatal(e)
	}
	if d.Author != "Grace" || d.PublishedTime != "2024-01-02" || d.PageType != PageTypeArticle {
		t.Fatalf("%#v", d)
	}
}

func TestConcurrentExtraction(t *testing.T) {
	src := []byte(`<main><h1>Title</h1><p>A useful paragraph has enough content.</p></main>`)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, e := ExtractBytes(src, "")
			if e != nil || d.Text == "" {
				t.Errorf("result=%v error=%v", d, e)
			}
		}()
	}
	wg.Wait()
}

func FuzzExtract(f *testing.F) {
	f.Add([]byte(`<main><p>Hello world with useful text.</p></main>`))
	f.Add([]byte(`<p><a href="java&#x0a;script:x">bad</a></p>`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 100000 {
			t.Skip()
		}
		d, e := ExtractBytes(b, "https://example.com", WithMaxInputBytes(100000), WithMaxElements(5000), WithMaxDepth(100), WithMaxOutputBytes(10000))
		if e == nil {
			if len(d.Markdown) > 10000 {
				t.Fatal("output limit")
			}
			low := strings.ToLower(d.Markdown)
			if strings.Contains(low, "](javascript:") || strings.Contains(low, "](data:") || strings.Contains(low, "\n<script") {
				t.Fatal("unsafe output")
			}
		}
	})
}

func BenchmarkExtract(b *testing.B) {
	src := []byte(`<main><h1>Guide</h1><p>This guide contains useful content for a benchmark.</p><pre>go test ./...</pre></main>`)
	b.ReportAllocs()
	for b.Loop() {
		if _, e := ExtractBytes(src, "https://example.com/docs"); e != nil {
			b.Fatal(e)
		}
	}
}
