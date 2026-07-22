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

func TestStructuralArticleTailsAreExcluded(t *testing.T) {
	html := `<html><head><meta property="og:type" content="article"></head><body><main>
<div class="topic-tags"><a href="/topics/ai">Artificial Intelligence</a><a href="/topics/software">Software Engineering</a><a href="/topics/ml">Machine Learning</a><a href="/topics/programming">Programming</a></div>
<article><h1>Measured result</h1><p>The investigation explains the measured result, the evidence supporting it, and the limitations that affect its interpretation.</p><p>The conclusion records what changed and why the evidence supports that conclusion.</p><section class="footnotes"><h2>Notes</h2><ol><li id="note-1">The archived measurement includes the original calibration details.</li></ol></section></article>
<section class="newsletter-panel"><h2>Be the first to see the latest research</h2><p>News and updates in your inbox.</p><form action="/newsletter/signup"><label>Instagram <input name="website"></label><span>This field is for validation purposes and should be left unchanged.</span><label>Email <input type="email"></label><button>Subscribe</button></form><p>By submitting your email you agree to our Terms of Use and Privacy Policy.</p></section>
<section><h2>Get a home loan that helps you win</h2><a href="/loans">Get started</a></section>
<section class="next-step"><h2>Ready for the next step?</h2><a href="/account/start">Get started</a></section>
<section class="briefing-club"><h2>Research Briefing Club</h2><form action="/join"><label>Email <input type="email"></label><label>Organization <input type="text"></label><button>Join</button></form><p>By joining, you agree to our Privacy Policy.</p></section>
<section class="research-alerts"><h2>Research Alerts Club</h2><form action="/join"><label>Email <input type="email"></label><label>Website <input type="text"></label><span>This field is for validation purposes and should be left unchanged.</span><button>Join</button></form></section>
<section><h2>Recommended for you</h2><div><h3><a href="/one">Life with hazard ratios</a></h3><time>May 2</time></div><div><h3><a href="/two">The trouble with vitamin studies</a></h3><time>May 3</time></div><div><h3><a href="/three">Choosing the right painkiller</a></h3><time>May 4</time></div></section>
<div class="site-links"><a href="/help">Help</a><a href="/about">About</a><a href="/careers">Careers</a><a href="/privacy">Privacy</a><a href="/rules">Rules</a><a href="/terms">Terms</a></div>
</main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/research/result")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Measured result", "evidence supporting it", "The archived measurement"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing article content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Artificial Intelligence", "Be the first", "validation purposes", "Privacy Policy", "Get a home loan", "Ready for the next step", "Research Briefing Club", "Research Alerts Club", "Life with hazard ratios", "Choosing the right painkiller", "Careers", "Rules"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included peripheral content %q: %s", unwanted, doc.Text)
		}
	}
}

func TestSubstantiveRecommendationsFormsAndSourcesSurvive(t *testing.T) {
	tests := []struct {
		name, tail string
		want       []string
		inside     bool
	}{
		{
			name:   "linked recommendations in article",
			tail:   `<section><h2>Recommendations</h2><div><h3><a href="/monitoring">Monitor monthly</a></h3><p>Clinicians should review adverse effects at every visit and record how the patient responds over time.</p></div><div><h3><a href="/dosing">Adjust the dose</a></h3><p>The dose should change when monthly measurements show that the current treatment is no longer appropriate.</p></div></section>`,
			want:   []string{"Recommendations", "Monitor monthly", "review adverse effects", "Adjust the dose"},
			inside: true,
		},
		{
			name:   "linked related work in article",
			tail:   `<section><h2>Related work</h2><div><h3><a href="/papers/a">Earlier method</a></h3><p>The earlier method established the baseline algorithm and documented where its accuracy declined.</p></div><div><h3><a href="/papers/b">Recent extension</a></h3><p>The recent extension improves that algorithm while retaining its original convergence guarantees.</p></div></section>`,
			want:   []string{"Related work", "Earlier method", "baseline algorithm", "Recent extension"},
			inside: true,
		},
		{
			name:   "contact form example in article",
			tail:   `<section class="contact-form-example"><h2>Contact form implementation</h2><p>This example validates an email address, records the message, and explains how an application can present errors without losing user input.</p><form action="/examples/contact"><label>Email <input type="email"></label><label>Message <textarea></textarea></label><label>Website <input type="text"></label><span>This field is for validation purposes and should be left unchanged.</span><button type="submit">Send message</button></form><p>The example links to the Privacy Policy because production contact forms must explain how submitted messages are processed.</p></section>`,
			want:   []string{"Contact form implementation", "records the message", "production contact forms"},
			inside: true,
		},
		{
			name: "post-article calculator",
			tail: `<section class="risk-calculator"><h2>Risk calculator</h2><p>This worksheet demonstrates how the variables in the analysis combine to produce the reported estimate for an individual case.</p><form><label>Baseline value <input type="number"></label><label>Adjustment <input type="number"></label><button type="button">Calculate result</button></form></section>`,
			want: []string{"Risk calculator", "worksheet demonstrates", "reported estimate"},
		},
		{
			name: "mortgage calculator",
			tail: `<section class="mortgage-calculator"><h2>Mortgage repayment calculator</h2><p>This calculator applies the repayment formula described in the article and shows how principal, term, and interest affect the monthly amount.</p><form><label>Principal <input type="number"></label><label>Interest rate <input type="number"></label><button type="button">Calculate repayment</button></form></section>`,
			want: []string{"Mortgage repayment calculator", "repayment formula", "monthly amount"},
		},
		{
			name: "linked sources",
			tail: `<section class="sources"><h2>Sources and evidence</h2><div><h3><a href="/study/one">Longitudinal cohort study</a></h3><p>The first study supplies the baseline measurements used in the analysis.</p></div><div><h3><a href="/study/two">Controlled comparison</a></h3><p>The second study tests the result against a matched comparison group.</p></div><div><h3><a href="/study/three">Independent replication</a></h3><p>The third study reports an independent replication using newer observations.</p></div></section>`,
			want: []string{"Sources and evidence", "Longitudinal cohort study", "Independent replication"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := `<html><head><meta property="og:type" content="article"><title>Clinical evidence review</title></head><body><main><article><h1>Clinical evidence review</h1><p>The review evaluates the available measurements, explains the analytical method, and reports the practical implications of the observed result.</p><p>A second paragraph documents important limitations and establishes the context needed to interpret the material that follows.</p>`
			if tt.inside {
				html += tt.tail + `</article>`
			} else {
				html += `</article>` + tt.tail
			}
			html += `</main></body></html>`
			doc, err := ExtractBytes([]byte(html), "https://example.com/reviews/evidence")
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(doc.Text, want) {
					t.Errorf("missing substantive content %q: %s", want, doc.Text)
				}
			}
		})
	}
}

func TestNoteToEditorsPrecedesExcludedRegistrationPanel(t *testing.T) {
	html := `<html><head><meta property="og:type" content="article"></head><body><main><article><h1>New flight programme</h1><p>The programme will evaluate a new wing configuration during a series of instrumented flights.</p><section><h2>Note to editors</h2><p>The test aircraft will remain based at the company flight facility throughout the programme.</p></section></article><section class="registration"><h2>Stay up to date with our latest news</h2><p>Create an account for company announcements.</p><form action="/register"><label>Email <input type="email"></label><button>Register</button></form></section></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/news/flight-programme")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Note to editors", "test aircraft"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing editorial note %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Stay up to date", "Create an account", "Register"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included registration panel %q: %s", unwanted, doc.Text)
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

func TestLongNarrativeOutweighsEmbeddedRecordSections(t *testing.T) {
	longParagraphs := `<p>The project began with a careful review of the existing system, the constraints faced by its users, and the evidence needed to choose a durable direction for the work.</p>` +
		`<p>The next phase tested those assumptions in production and documented how each change affected reliability, maintenance effort, and the experience of the people using it.</p>` +
		`<p>That investigation also revealed several tradeoffs which only became clear after the team compared the early prototype with the behavior of the complete implementation.</p>` +
		`<p>The article now turns to the practical consequences, explaining why the chosen approach works and where future improvements can build on the foundation already in place.</p>` +
		`<p>Readers should understand the sequence because no single measurement tells the whole story; the conclusion follows from all of the observations considered together over time.</p>` +
		`<p>Finally, the author describes what comes next and how the lessons from this work apply to teams facing similar technical and organizational decisions.</p>`
	tests := []struct {
		name, supporting string
	}{
		{"metrics cards", strings.Repeat(`<div class="metric-card"><strong>42%</strong><span>Measured improvement over the previous release</span></div>`, 8)},
		{"testimonials", strings.Repeat(`<div class="testimonial-card"><blockquote>The new workflow made our daily work clearer and more dependable.</blockquote></div>`, 8)},
		{"sponsorship tiers", strings.Repeat(`<div class="sponsor-card price"><h3>Supporting sponsor — $500</h3><p>Recognition and project updates for organizations funding the work.</p></div>`, 8)},
		{"comparison table", `<table><thead><tr><th>Approach</th><th>Result</th></tr></thead><tbody><tr><td>Earlier design</td><td>Manual coordination</td></tr><tr><td>New design</td><td>Automatic coordination</td></tr></tbody></table>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := `<html><head><title>A complete account of the project</title></head><body><main><h1>A complete account of the project</h1>` + longParagraphs + `<h2>Evidence and options</h2>` + tt.supporting + `<h2>Conclusion</h2><p>The evidence supports continuing the project while preserving the principles established throughout the preceding analysis.</p></main></body></html>`
			doc, err := ExtractBytes([]byte(html), "https://example.com/blog/complete-project-account")
			if err != nil {
				t.Fatal(err)
			}
			if doc.PageType != PageTypeArticle {
				t.Fatalf("page type = %q (score %.3f), want article", doc.PageType, doc.PageTypeScore)
			}
		})
	}
}

func TestRepeatedRecordsDominateGenuineListings(t *testing.T) {
	tests := []struct {
		name, url, intro, records string
	}{
		{
			name:    "product listing with descriptive intro",
			url:     "https://example.com/catalog/tools",
			intro:   `<p>This catalog explains the available workshop tools and helps buyers choose equipment appropriate for different materials and project sizes.</p>`,
			records: strings.Repeat(`<article class="product-card"><h2>Workshop tool</h2><p>A distinct product with specifications, availability, and ordering details.</p></article>`, 7),
		},
		{
			name:    "article index",
			url:     "https://example.com/news",
			intro:   `<p>Reporting and analysis from our writers, updated throughout the week.</p>`,
			records: strings.Repeat(`<article class="story-card"><h2>Current report</h2><p>A summary of a separate report with enough detail to help readers choose it.</p></article>`, 7),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := `<main><h1>Current collection</h1>` + tt.intro + `<div class="results">` + tt.records + `</div></main>`
			doc, err := ExtractBytes([]byte(html), tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if doc.PageType != PageTypeListing && doc.PageType != PageTypeCollection {
				t.Fatalf("page type = %q, want listing or collection", doc.PageType)
			}
		})
	}
}

func TestNeutralRecordsUnderResultsWrapperInferListing(t *testing.T) {
	html := `<main><h1>Search results</h1><div class="results">` +
		`<div class="filters"><label>Category <select><option>All</option></select></label></div>` +
		`<div class="grid">` +
		`<article><h2>First match</h2><p>The first matching page has a useful descriptive summary.</p></article>` +
		`<article><h2>Second match</h2><p>The second matching page has another descriptive summary.</p></article>` +
		`<article><h2>Third match</h2><p>The third matching page completes the result set.</p></article>` +
		`</div></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/search?q=match", WithMaxRepeatedItems(2))
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeListing {
		t.Fatalf("page type = %q, want listing", doc.PageType)
	}
	for _, want := range []string{"First match", "Second match"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing retained result %q: %s", want, doc.Text)
		}
	}
	if strings.Contains(doc.Text, "Third match") {
		t.Errorf("neutral result records did not use repeated-item handling: %s", doc.Text)
	}
}

func TestArticleWrapperInferenceDoesNotCacheAuthorProfileAsRelevant(t *testing.T) {
	html := `<html><head><meta property="og:type" content="article"><title>Primary investigation</title></head><body>` +
		`<main class="results"><article><h1>Primary investigation</h1>` +
		`<p>The investigation begins with a detailed account of the evidence and the circumstances surrounding the observed behavior.</p>` +
		`<p>Further analysis explains the cause, the verification process, and the change that resolved the problem reliably.</p>` +
		`</article><section class="author-profile"><h2>About the author</h2><p>This trailing biography is publication furniture rather than article content.</p></section></main>` +
		`</body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/reports/investigation")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("page type = %q, want article", doc.PageType)
	}
	for _, want := range []string{"Primary investigation", "verification process"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing article content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"About the author", "trailing biography"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("cached pre-inference relevance leaked %q: %s", unwanted, doc.Text)
		}
	}
}

func TestPaginationItemsDoNotOverrideGenericListingRecords(t *testing.T) {
	html := `<main><h1>Search results</h1><div class="results">` +
		`<div><h2>First result</h2><p>The first generic result has a useful descriptive summary.</p></div>` +
		`<div><h2>Second result</h2><p>The second generic result has another descriptive summary.</p></div>` +
		`<div><h2>Third result</h2><p>The third generic result completes the result set.</p></div>` +
		`<nav aria-label="Pagination"><ul><li><a href="?page=1">1</a></li><li><a href="?page=2">2</a></li></ul></nav>` +
		`</div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/search?q=result", WithMaxRepeatedItems(2))
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeListing {
		t.Fatalf("page type = %q, want listing", doc.PageType)
	}
	for _, want := range []string{"First result", "Second result"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing retained result %q: %s", want, doc.Text)
		}
	}
	if strings.Contains(doc.Text, "Third result") {
		t.Errorf("pagination items displaced generic result records: %s", doc.Text)
	}
}

func TestNestedMetadataListsDoNotOverrideGenericListingRecords(t *testing.T) {
	html := `<main><h1>Project results</h1><div class="results">` +
		`<div><h2>First project</h2><p>The first project is a web service.</p><ul><li>Go</li><li>Web</li></ul></div>` +
		`<div><h2>Second project</h2><p>The second project is a command line tool.</p><ul><li>Rust</li><li>CLI</li></ul></div>` +
		`<div><h2>Third project</h2><p>The third project exposes an application interface.</p><ul><li>Java</li><li>API</li></ul></div>` +
		`</div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/search/projects", WithMaxRepeatedItems(2))
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeListing {
		t.Fatalf("page type = %q, want listing", doc.PageType)
	}
	for _, want := range []string{"First project", "Second project"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing retained result %q: %s", want, doc.Text)
		}
	}
	if strings.Contains(doc.Text, "Third project") {
		t.Errorf("nested metadata lists displaced generic result records: %s", doc.Text)
	}
}

func TestDominantProductWrapperInfersProductOnNeutralURL(t *testing.T) {
	html := `<main class="product"><h1>Adjustable standing desk</h1><p>This height-adjustable desk has a solid hardwood surface and a quiet electric frame suitable for a home office.</p><h2>Dimensions and finish</h2><p>The desktop is available in three widths, with a durable finish and a programmable controller included.</p></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/detail/adjustable-desk")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeProduct {
		t.Fatalf("page type = %q, want product", doc.PageType)
	}
}

func TestExplicitPageTypeOverridesNarrativeInference(t *testing.T) {
	html := `<main><h1>Long analysis</h1><p>This long analysis explains the complete history and the evidence behind the final decision in a conventional article structure.</p><p>A second substantial paragraph continues the narrative and records the practical consequences for readers.</p></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/blog/analysis", WithPageType(PageTypeListing))
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeListing || doc.PageTypeScore != 1 {
		t.Fatalf("page type = %q score %.3f, want explicit listing with score 1", doc.PageType, doc.PageTypeScore)
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

func TestNumberedSectionHeadingFormats(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"1. Cut is undoable", true},
		{"1) Cut is undoable", true},
		{"1: Cut is undoable", true},
		{"1 - Cut is undoable", true},
		{"1 – Cut is undoable", true},
		{"1 Heading without punctuation", false},
		{"7 Ways to Improve Reliability", false},
		{"5 Things to Know", false},
		{"12. A later section", true},
		{"100: Appendix section", true},
		{"2024: A year in review", false},
		{"2024 Predictions", false},
		{"10 Things I Learned", false},
		{"1Password for teams", false},
		{"1-800 numbers explained", false},
		{"Chapter 1", false},
	}
	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			if got := isNumberedSectionHeading(tc.text); got != tc.want {
				t.Fatalf("isNumberedSectionHeading(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
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

func TestArticleMetadataTitleWinsOverInternalH1(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Intel Starts Shipping High-NA EUV Silicon"><meta property="og:type" content="article"></head><body><article><p>The opening explains why this manufacturing milestone matters and provides enough selected prose to establish the article body.</p><h1>What the machine actually is</h1><p>This section explains the machine architecture and the practical implications of the new process in substantial detail.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Intel Starts Shipping High-NA EUV Silicon\n") {
		t.Fatalf("metadata title did not precede the internal heading:\n%s", doc.Markdown)
	}
	if strings.Count(doc.Text, "Intel Starts Shipping High-NA EUV Silicon") != 1 || !strings.Contains(doc.Markdown, "## What the machine actually is") {
		t.Fatalf("title was duplicated or internal h1 was not demoted:\n%s", doc.Markdown)
	}
}

func TestArticleMetadataTitleWinsOverNumberedInternalH1(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Introducing Ghost Cut - or why Cut &amp; Paste is broken everywhere"></head><body><article><h1>1. Cut is undoable</h1><p>This first section contains substantial explanatory prose about editor behavior and why the operation cannot be modeled correctly.</p><p>The article continues with implementation details and examples that establish a complete primary prose region.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/blog/ghost-cut", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Introducing Ghost Cut - or why Cut \\& Paste is broken everywhere\n") || !strings.Contains(doc.Markdown, "## 1\\. Cut is undoable") {
		t.Fatalf("numbered section replaced the metadata title:\n%s", doc.Markdown)
	}
}

func TestSingleDigitListStyleArticleTitleOverridesStaleMetadata(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Stale Metadata"></head><body><article><h1>7 Ways to Improve Reliability</h1><p>This article provides substantial explanatory prose about practical reliability improvements and their operational impact.</p><p>A second substantive paragraph confirms that the leading list-style heading labels the selected article body.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# 7 Ways to Improve Reliability\n") || strings.Contains(doc.Text, "Stale Metadata") {
		t.Fatalf("single-digit list-style title was treated as a section:\n%s", doc.Markdown)
	}
}

func TestHiddenPublicationMetadataDoesNotInvalidateHeadline(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Stale Metadata Title"></head><body><article><time hidden datetime="2020-01-01">January 1, 2020</time><h1>Current Visible Headline</h1><p>This article contains substantial explanatory prose that clearly belongs to the current visible source headline.</p><p>A second substantive paragraph confirms the selected article structure and its legitimate leading heading.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Current Visible Headline\n") || strings.Contains(doc.Text, "Stale Metadata Title") {
		t.Fatalf("hidden publication metadata invalidated visible headline:\n%s", doc.Markdown)
	}
}

func TestArticleMetadataTitleWinsWhenDatePrecedesInternalH1(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Perlin's Noise Algorithm"></head><body><article><time datetime="2024-02-01">February 1, 2024</time><h1>What is Noise?</h1><p>This section introduces noise functions with substantial explanatory prose and examples for the tutorial reader.</p><p>The tutorial continues with gradients and interpolation details that establish a complete article body.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/tutorial/perlin", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Perlin's Noise Algorithm\n") || !strings.Contains(doc.Markdown, "## What is Noise?") {
		t.Fatalf("post-date section heading replaced metadata title:\n%s", doc.Markdown)
	}
}

func TestArticleMetadataEquivalentH2IsPromoted(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Perlin's Noise Algorithm"></head><body><article><h2>Perlin's Noise Algorithm</h2><p>This tutorial introduces coherent noise with enough explanatory prose to establish the selected article body.</p><p>Further details describe interpolation, gradients, and implementation choices for the complete algorithm.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/tutorial/perlin", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Perlin's Noise Algorithm\n") || strings.Count(doc.Text, "Perlin's Noise Algorithm") != 1 {
		t.Fatalf("matching h2 was not promoted exactly once:\n%s", doc.Markdown)
	}
}

func TestMarkedSourceHeadlineOverridesConflictingBrowserTitle(t *testing.T) {
	html := `<html><head><title>Incorrect Browser Title</title></head><body><article><h1 itemprop="headline">Correct Body Headline</h1><p>This report contains substantial primary prose that clearly belongs to the explicitly marked source headline.</p><p>Additional reporting confirms the article structure and keeps the selected body substantial.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/reports/correct", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Correct Body Headline\n") || strings.Contains(doc.Text, "Incorrect Browser Title") {
		t.Fatalf("marked source headline did not override browser metadata:\n%s", doc.Markdown)
	}
}

func TestGenericMediumBrowserTitleUsesBodyHeadline(t *testing.T) {
	html := `<html><head><title>Medium</title></head><body><article><h1>The Correct Body Headline</h1><p>This article contains enough useful explanatory prose to establish that its visible heading is the actual title.</p><p>A second substantial paragraph confirms the selected article region without relying on generic browser chrome.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://medium.com/example/correct", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# The Correct Body Headline\n") || strings.Contains(doc.Markdown, "# Medium") {
		t.Fatalf("generic browser title replaced body headline:\n%s", doc.Markdown)
	}
}

func TestArticleSiteSuffixRestoresNormalizedTitle(t *testing.T) {
	html := `<html><head><title>Article title - Site Name</title><meta property="og:site_name" content="Site Name"></head><body><article><p>This article has substantial opening prose but deliberately omits a visible source headline from the selected body.</p><p>A second paragraph ensures there is enough article content for normalized metadata title restoration.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Article title\n") || strings.Contains(doc.Markdown, "Site Name") || strings.Count(doc.Text, "Article title") != 1 {
		t.Fatalf("site suffix was not stripped from restored title:\n%s", doc.Markdown)
	}
}

func TestAlreadySelectedArticleTitleRemainsOnce(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Already Selected Title"></head><body><article><h1>Already Selected Title</h1><p>This article contains enough explanatory prose to retain its already selected source title in the output.</p><p>More substantive prose ensures title recovery does not need to synthesize another heading.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Already Selected Title\n") || strings.Count(doc.Text, "Already Selected Title") != 1 {
		t.Fatalf("already selected title was duplicated:\n%s", doc.Markdown)
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
	if !strings.HasPrefix(doc.Markdown, "# Actual Story\n") {
		t.Fatalf("document-specific metadata fallback was not used:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Markdown, "Example Site") {
		t.Fatalf("browser title site suffix was retained:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Text, "Publisher Home") {
		t.Fatalf("site masthead was selected in addition to metadata: %q", doc.Text)
	}
}

func TestArticleSocialTitleOverridesAdjacentBrowserTitleMasthead(t *testing.T) {
	html := `<html><head><title>Example Site</title><meta property="og:title" content="Actual Story"></head><body><h1>Example Site</h1><article><p>The actual story body contains substantial reporting about the event and enough detail to establish the selected primary prose.</p><p>A second paragraph confirms that this is one article whose social title should override the adjacent site masthead.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Actual Story\n") {
		t.Fatalf("preferred social title was not restored:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Text, "Example Site") {
		t.Fatalf("browser-title masthead overrode the social title: %q", doc.Text)
	}
}

func TestArticleSocialTitleBeforeBrowserTitleOverridesAdjacentMasthead(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Actual Story"><title>Example Site</title></head><body><h1>Example Site</h1><article><p>The actual story body contains substantial reporting about the event and enough detail to establish the selected primary prose.</p><p>A second paragraph confirms that metadata order cannot allow the adjacent site masthead to override the social title.</p></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Actual Story\n") {
		t.Fatalf("preferred social title was not restored when it preceded title:\n%s", doc.Markdown)
	}
	if strings.Contains(doc.Text, "Example Site") {
		t.Fatalf("later browser-title masthead overrode the social title: %q", doc.Text)
	}
}

func TestArticleMetadataTitleRetainsLeadingSectionHeading(t *testing.T) {
	html := `<html><head><title>Actual Story</title><meta property="og:title" content="Actual Story"></head><body><main><h2>Introduction</h2><p>This substantial opening paragraph introduces the article subject and establishes the selected primary prose region.</p><p>This second substantial paragraph continues the introduction with useful context for the rest of the article.</p></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/article", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Actual Story\n\n## Introduction\n") {
		t.Fatalf("legitimate leading section heading was not retained:\n%s", doc.Markdown)
	}
	if strings.Count(doc.Text, "Actual Story") != 1 || strings.Count(doc.Text, "Introduction") != 1 {
		t.Fatalf("title or section heading was duplicated: %q", doc.Text)
	}
}

func TestExcludedEquivalentHeadingDoesNotBlockTitleRestoration(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Actual Story"></head><body><main><h1 class="thread-tools">Actual Story</h1><div class="content"><p>This substantial opening paragraph explains the actual story with enough detail to establish one dominant prose region.</p><p>This second substantial paragraph continues the same document after the excluded discussion control heading.</p></div></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/story", WithPageType(PageTypeDiscussion))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# Actual Story\n") || strings.Count(doc.Text, "Actual Story") != 1 {
		t.Fatalf("excluded equivalent heading blocked restoration:\n%s", doc.Markdown)
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

func TestGenericDominantProseRestoresMetadataTitle(t *testing.T) {
	html := `<html><head><title>A Useful Guide — Example Site</title><meta property="og:title" content="A Useful Guide"></head><body><div class="content"><p>This is a long opening paragraph with enough prose to explain the useful subject clearly and establish a primary document region.</p><p>This is a second substantial paragraph that develops the guide with practical details for readers who need the information.</p></div></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/guides/useful", WithPageType(PageTypeGeneric))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.Markdown, "# A Useful Guide\n") || strings.Count(doc.Text, "A Useful Guide") != 1 {
		t.Fatalf("generic prose title was not restored exactly once:\n%s", doc.Markdown)
	}
}

func TestGenericDominantProseDoesNotRestoreSiteOnlyTitle(t *testing.T) {
	html := `<html><head><title>Example Site</title></head><body><main><p>This substantial opening paragraph contains enough prose to resemble a document while describing the publication generally.</p><p>This second substantial paragraph ensures output shape alone cannot turn a generic site name into a document heading.</p></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/about", WithPageType(PageTypeGeneric))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.Markdown, "# Example Site") {
		t.Fatalf("generic site title was synthesized:\n%s", doc.Markdown)
	}
}

func TestGenericDominantProseDoesNotDuplicateEquivalentHeading(t *testing.T) {
	html := `<html><head><meta property="og:title" content="A Useful Guide"></head><body><main><h1>A Useful Guide</h1><p>This substantial opening paragraph explains the subject and gives the selected output a clear primary prose region.</p><p>This second substantial paragraph supplies further useful details without requiring metadata title restoration.</p></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/guides/useful", WithPageType(PageTypeGeneric))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(doc.Text, "A Useful Guide") != 1 {
		t.Fatalf("surviving generic heading was duplicated: %q", doc.Text)
	}
}

func TestForcedGenericListingDoesNotRestoreMetadataTitle(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Useful Resources"></head><body><main>` +
		`<div class="card"><h2><a href="/one">First record</a></h2><p>The first linked record has a substantial descriptive summary for readers browsing the collection.</p></div>` +
		`<div class="card"><h2><a href="/two">Second record</a></h2><p>The second linked record has a substantial descriptive summary for readers browsing the collection.</p></div>` +
		`<div class="card"><h2><a href="/three">Third record</a></h2><p>The third linked record has a substantial descriptive summary for readers browsing the collection.</p></div>` +
		`<div class="card"><h2><a href="/four">Fourth record</a></h2><p>The fourth linked record has a substantial descriptive summary for readers browsing the collection.</p></div></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/resources", WithPageType(PageTypeGeneric))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.Markdown, "# Useful Resources") {
		t.Fatalf("listing metadata title was synthesized:\n%s", doc.Markdown)
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

func TestTrailingOrganizationProfileIsExcluded(t *testing.T) {
	html := `<main><article><h1>Product release notes</h1><p>Substantial article content explains the changes included in this release.</p><h2>Stay tuned</h2><p>Final article conclusion describes what readers can expect next.</p></article><section class="company-info"><h2>About Us</h2><p>Example Corp is a global company building products for customers around the world.</p></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/blog/release", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Product release notes", "Stay tuned", "readers can expect next"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing article content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"About Us", "global company", "around the world"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included organization boilerplate %q: %s", unwanted, doc.Text)
		}
	}
}

func TestTrailingOrganizationProfileInsideArticleIsExcluded(t *testing.T) {
	html := `<html><head><meta property="og:site_name" content="Example Corp"><meta property="og:type" content="article"></head><body><article><h1>Product release notes</h1><div><p>Substantial article content explains the changes included in this release.</p></div><div><h2>Stay tuned</h2><p>Final article conclusion describes what readers can expect next.</p></div><div><h2>About Us</h2><p>Example Corp is the software company building a complete suite of useful products.</p></div></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/blog/release")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "Final article conclusion") {
		t.Fatalf("article conclusion was lost: %s", doc.Text)
	}
	if strings.Contains(doc.Text, "About Us") || strings.Contains(doc.Text, "software company") {
		t.Fatalf("included organization boilerplate: %s", doc.Text)
	}
}

func TestTrailingOrganizationProfileBeforeArticleFooterIsExcluded(t *testing.T) {
	html := `<html><head><meta property="og:type" content="article"></head><body><article><h1>Product release notes</h1><p>The release notes explain the completed work, the design decisions behind it, and all implementation details readers need.</p><p>The final article paragraph documents verification results, remaining limitations, and the conclusions reached by the engineering team.</p><section class="company"><h2>About Us</h2><p>Example Corp is a global company building products for customers around the world.</p></section><footer><p>Written by Jane Doe.</p></footer></article></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/blog/release", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "final article paragraph") {
		t.Fatalf("article conclusion was lost: %s", doc.Text)
	}
	for _, unwanted := range []string{"About Us", "global company", "Written by Jane Doe"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included trailing auxiliary content %q: %s", unwanted, doc.Text)
		}
	}
}

func TestTrailingOrganizationProfileUsesSchemaAndOrganizationLink(t *testing.T) {
	for _, tc := range []struct {
		name, href string
	}{
		{"about path", "/about"},
		{"LinkedIn profile", "https://www.linkedin.com/company/example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html := `<main><article><h1>Product update</h1><p>The article explains the product update, the design decisions behind it, and the implementation details readers need to understand the release.</p><p>A second substantial paragraph documents the verification process, observed results, remaining limitations, and the final conclusions reached by the team.</p></article><section itemscope itemtype="https://schema.org/Organization"><h2>About Us</h2><p>Learn more about our work.</p><a href="` + tc.href + `">Our profile</a></section></main>`
			doc, err := ExtractBytes([]byte(html), "https://example.com/blog/update", WithPageType(PageTypeArticle))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(doc.Text, "About Us") || strings.Contains(doc.Text, "Learn more about our work") {
				t.Fatalf("included organization profile supported by %s: %s", tc.name, doc.Text)
			}
		})
	}
}

func TestSiteIdentityRequiresWordBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, head, pageURL, prose string
	}{
		{
			name:    "hostname inside readers",
			pageURL: "https://read.com/article",
			prose:   "This section explains why Readers Guild is a company case study for the article.",
		},
		{
			name:    "metadata inside pressure",
			head:    `<meta property="og:site_name" content="Press">`,
			pageURL: "https://example.com/article",
			prose:   "This section explains why Pressure Labs is a company case study for the article.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html := `<html><head>` + tc.head + `</head><body><main><article><h1>Writing company case studies</h1><p>The article examines evidence and methods for preparing an accurate case study.</p></article><section><h2>About Us</h2><p>` + tc.prose + `</p></section></main></body></html>`
			doc, err := ExtractBytes([]byte(html), tc.pageURL, WithPageType(PageTypeArticle))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(doc.Text, "About Us") || !strings.Contains(doc.Text, tc.prose) {
				t.Fatalf("legitimate section was removed by a substring identity match: %s", doc.Text)
			}
		})
	}
}

func TestLegitimateArticleAboutUsSectionIsRetained(t *testing.T) {
	html := `<article><h1>Why websites need an About Us page</h1><p>The introduction explains how company websites communicate with their audiences.</p><section><h2>About Us</h2><p>This section analyzes how the phrase is used and why its wording matters.</p></section><section><h2>Conclusion</h2><p>The final argument summarizes the analysis for website authors.</p></section></article>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/articles/about-pages", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"About Us", "phrase is used", "Conclusion", "final argument"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing legitimate article content %q: %s", want, doc.Text)
		}
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

func TestEmptyCommentsControlsDoNotMakeArticleDiscussion(t *testing.T) {
	html := `<main><article><h1>Resurrecting a small tablet</h1><p>The first substantial paragraph explains the history of the device and why restoring it was worth the effort.</p><p>The second substantial paragraph describes the software changes, testing process, and results observed after installation.</p><p>The final substantial paragraph records the remaining limitations while preserving the central conclusion of the article.</p></article><div class="comments"><div class="comments-headline"><span class="dossier-label">thread</span><div class="comments-headline-main"><span class="comments-title">discussion</span><button class="comments-toggle">open thread</button></div></div><div class="comments-panel" hidden></div></div></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/posts/resurrecting-tablet")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("page type = %q, want article", doc.PageType)
	}
	if !strings.Contains(doc.Text, "software changes") {
		t.Fatalf("article body missing: %s", doc.Text)
	}
	for _, unwanted := range []string{"thread", "discussion", "open thread"} {
		if strings.Contains(strings.ToLower(doc.Text), unwanted) {
			t.Errorf("comments control %q was retained: %s", unwanted, doc.Text)
		}
	}
}

func TestInferenceUsesPrimaryRatherThanAuxiliaryDiscussionEvidence(t *testing.T) {
	articleParagraphs := `<p>The opening section provides a detailed account of the subject, its history, and the practical constraints that shaped the work over several months.</p><p>The next section explains the decisions in enough depth for readers to understand the alternatives and the reasons those alternatives were rejected.</p><p>Testing uncovered several subtle problems, so the team repeated each measurement and documented the results before drawing any conclusions.</p><p>The final section connects those results to the original goal and records the remaining limitations for future work and independent verification.</p><p>An appendix adds implementation details and examples that make the central explanation useful without relying on any reader responses below.</p>`
	tests := []struct {
		name, source, pageURL string
		want                  PageType
		retained, omitted     []string
	}{
		{
			name: "long article with empty comments controls", pageURL: "https://example.com/articles/measurements",
			source: `<main><article><h1>Careful measurements</h1>` + articleParagraphs + `</article><aside class="comments discussion"><button>Open discussion</button><form hidden><textarea class="reply"></textarea></form></aside></main>`,
			want:   PageTypeArticle, retained: []string{"opening section", "final section"}, omitted: []string{"Open discussion"},
		},
		{
			name: "long article with one reader comment", pageURL: "https://example.com/articles/measurements",
			source: `<main><article><h1>Careful measurements</h1>` + articleParagraphs + `</article><aside class="comments"><article class="comment message"><p>A reader suggests one additional measurement that could be useful in a later experiment.</p></article></aside></main>`,
			want:   PageTypeArticle, retained: []string{"opening section"}, omitted: []string{"reader suggests"},
		},
		{
			name: "long article with several comments", pageURL: "https://example.com/articles/measurements",
			source: `<main><article><h1>Careful measurements</h1>` + articleParagraphs + `</article><section class="comments"><article class="comment"><p>Ada proposes repeating the test with a second device to compare the observed results.</p></article><article class="comment"><p>Ben asks whether the same setup was used for every measurement reported in the article.</p></article><article class="comment"><p>Chen confirms the procedure and contributes another independent result from a similar device.</p></article></section></main>`,
			want:   PageTypeArticle, retained: []string{"implementation details"}, omitted: []string{"Ada proposes", "Ben asks", "Chen confirms"},
		},
		{
			name: "forum topic with replies", pageURL: "https://example.com/forum/topic/42",
			source: `<main class="forum topic"><article class="post message"><p>The initial post describes a reproducible configuration failure and lists the relevant settings.</p></article><article class="reply message"><p>The first reply identifies the incorrect setting and explains the required replacement value.</p></article><article class="reply message"><p>The second reply confirms the fix and provides a command that verifies the new configuration.</p></article></main>`,
			want:   PageTypeDiscussion, retained: []string{"initial post", "first reply", "second reply"},
		},
		{
			name: "question and answers", pageURL: "https://example.com/help/42",
			source: `<html><head><script type="application/ld+json">{"@context":"https://schema.org","@type":"QAPage"}</script></head><body><main><div class="question"><p>How can the worker be configured to finish every queued task before shutdown?</p></div><div class="answer"><p>Set the graceful timeout before starting the worker, then verify the value in its status output.</p></div><div class="answer"><p>You can also send the drain signal and wait until the queue count reaches zero.</p></div></main></body></html>`,
			want:   PageTypeDiscussion, retained: []string{"How can the worker", "graceful timeout", "drain signal"},
		},
		{
			name: "faq questions are not discussion records", pageURL: "https://example.com/faq",
			source: `<main><h1>Frequently asked questions</h1><div class="question"><h2>Where is account data stored?</h2><p>Account data is stored in the selected region and remains there until the account is deleted.</p></div><div class="question"><h2>How can an account be exported?</h2><p>Use the export command in settings to download a complete archive in a portable format.</p></div></main>`,
			want:   PageTypeGeneric, retained: []string{"Where is account data stored?", "export command"},
		},
		{
			name: "survey questions are not discussion records", pageURL: "https://example.com/research/survey",
			source: `<main><h1>Reader survey</h1><div class="question"><p>Which features are most useful in your daily work, and why do they improve the process?</p></div><div class="question"><p>What changes would make the application easier for a new member of your team to adopt?</p></div></main>`,
			want:   PageTypeGeneric, retained: []string{"Which features", "What changes"},
		},
		{
			name: "hidden discussion template", pageURL: "https://example.com/about",
			source: `<div hidden><main class="forum thread"><article class="message"><p>A hidden template message must never provide evidence about the visible page.</p></article></main></div><section><h1>Project overview</h1><p>This visible generic page describes the project goals and the resources available to visitors.</p></section>`,
			want:   PageTypeGeneric, retained: []string{"visible generic page"}, omitted: []string{"hidden template"},
		},
		{
			name: "generic discussion button", pageURL: "https://example.com/about",
			source: `<main><h1>Project overview</h1><p>This page gives a concise overview of the project and points visitors toward the available resources.</p><button class="reply discussion">Start discussion</button></main>`,
			want:   PageTypeGeneric, retained: []string{"concise overview"}, omitted: []string{"Start discussion"},
		},
		{
			name: "message previews in sidebar", pageURL: "https://example.com/about",
			source: `<main><h1>Project overview</h1><p>This primary region explains the project purpose and provides the information visitors need.</p><div class="discussion sidebar"><div class="message"><p>A preview message asks a detailed question about an unrelated community topic.</p></div><div class="message"><p>Another preview message gives a substantive answer and links to the full conversation.</p></div></div></main>`,
			want:   PageTypeGeneric, retained: []string{"primary region"},
		},
		{
			name: "blog post class", pageURL: "https://example.com/read/42",
			source: `<main><div class="post"><h1>Building a dependable worker</h1>` + articleParagraphs + `</div></main>`,
			want:   PageTypeArticle, retained: []string{"opening section", "independent verification"},
		},
		{
			name: "documentation about message threads", pageURL: "https://example.com/guide/messages",
			source: `<main class="documentation"><h1>Messaging guide</h1><p>This guide explains the messaging API and the lifecycle of a delivery.</p><section class="message-threads"><h2>Message threads</h2><p>A message thread groups related events under one stable identifier for later retrieval.</p><p>Applications should store that identifier and pass it to each subsequent API request.</p></section><p>The reference section documents error handling, pagination, authentication, and retry behavior.</p></main>`,
			want:   PageTypeDocumentation, retained: []string{"Message threads", "stable identifier", "error handling"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ExtractBytes([]byte(tc.source), tc.pageURL)
			if err != nil {
				t.Fatal(err)
			}
			if doc.PageType != tc.want {
				t.Fatalf("page type = %q, want %q; markdown: %s", doc.PageType, tc.want, doc.Markdown)
			}
			for _, want := range tc.retained {
				if !strings.Contains(doc.Markdown, want) {
					t.Errorf("missing retained text %q: %s", want, doc.Markdown)
				}
			}
			for _, unwanted := range tc.omitted {
				if strings.Contains(doc.Markdown, unwanted) {
					t.Errorf("retained auxiliary text %q: %s", unwanted, doc.Markdown)
				}
			}
		})
	}
}

func TestRepeatedSubstantiveMessagesAreInferredAsDiscussion(t *testing.T) {
	html := `<main class="discussion thread"><article class="post message"><p>The initial question explains the failing behavior and includes enough detail to reproduce it reliably.</p></article><article class="reply message"><p>The first answer recommends changing the worker setting before trying the operation again.</p></article><article class="reply message"><p>The second answer adds a concrete verification step and explains the expected result.</p></article></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/conversation/42")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeDiscussion {
		t.Fatalf("page type = %q, want discussion", doc.PageType)
	}
	for _, want := range []string{"initial question", "first answer", "second answer"} {
		if !strings.Contains(strings.ToLower(doc.Text), want) {
			t.Errorf("message %q missing: %s", want, doc.Text)
		}
	}
}

func TestNeutralSemanticRecordsInThreadAreInferredAsDiscussion(t *testing.T) {
	html := `<main class="discussion thread"><article><p>The question describes the unexpected behavior and gives enough detail for another reader to reproduce it.</p></article><article><p>The first answer identifies the relevant setting and explains when the new value takes effect.</p></article><article><p>The second answer provides a verification command and describes the successful output.</p></article></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/conversation/neutral")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageType != PageTypeDiscussion {
		t.Fatalf("page type = %q, want discussion", doc.PageType)
	}
	for _, want := range []string{"question describes", "first answer", "second answer"} {
		if !strings.Contains(strings.ToLower(doc.Text), want) {
			t.Errorf("message %q missing: %s", want, doc.Text)
		}
	}
}

func TestDirectCommentsProseIsKeptInExplicitDiscussion(t *testing.T) {
	html := `<section class="comments"><p>This is a real reader response with substantive advice about resolving the reported problem.</p></section>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/conversation/direct", WithPageType(PageTypeDiscussion))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "real reader response") {
		t.Fatalf("direct comment prose missing: %s", doc.Text)
	}
}

func TestEmptyCommentParagraphPromptsAreFiltered(t *testing.T) {
	for _, prompt := range []string{"No comments yet.", "Sign in to join the discussion."} {
		t.Run(prompt, func(t *testing.T) {
			html := `<main><p>The primary page content remains useful without any reader responses.</p><section class="comments"><p>` + prompt + `</p><button>Open discussion</button></section></main>`
			doc, err := ExtractBytes([]byte(html), "https://example.com/page")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(doc.Text, "primary page content") {
				t.Fatalf("primary content missing: %s", doc.Text)
			}
			if strings.Contains(doc.Text, prompt) {
				t.Errorf("comment prompt was retained: %s", doc.Text)
			}
		})
	}
}

func TestOnlyEmptyCommentPromptProducesNoContent(t *testing.T) {
	html := `<section class="comments"><p>No comments yet.</p><button>Open discussion</button></section>`
	_, err := ExtractBytes([]byte(html), "https://example.com/page")
	if !errors.Is(err, ErrNoContent) {
		t.Fatalf("got %v, want ErrNoContent", err)
	}
}

func TestCommentCallToActionWidgetIsFiltered(t *testing.T) {
	html := `<main><p>The primary page content remains useful without any reader responses.</p><section class="comments"><h2>Join the conversation</h2><p>Share your thoughts with other readers.</p><button>Sign in</button></section></main>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/page")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "primary page content") {
		t.Fatalf("primary content missing: %s", doc.Text)
	}
	for _, unwanted := range []string{"Join the conversation", "Share your thoughts", "Sign in"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("comment call to action %q was retained: %s", unwanted, doc.Text)
		}
	}
}

func TestDirectBreakSeparatedCommentsProseIsKept(t *testing.T) {
	html := `<section class="comments">A substantive reader response explains the solution.<br>A second line provides the verification steps.</section>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/conversation/direct", WithPageType(PageTypeDiscussion))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"substantive reader response", "second line"} {
		if !strings.Contains(strings.ToLower(doc.Text), want) {
			t.Errorf("direct comment text %q missing: %s", want, doc.Text)
		}
	}
}

func TestLongPromptLikeCommentIsKept(t *testing.T) {
	html := `<section class="comments"><article class="comment"><p>Share your feedback with the maintainers before deploying this change, because the current configuration can lose queued work.</p></article></section>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/conversation/direct", WithPageType(PageTypeDiscussion))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "current configuration can lose queued work") {
		t.Fatalf("long comment was discarded: %s", doc.Text)
	}
}

func TestShortPromptLikeMarkedCommentIsKept(t *testing.T) {
	for _, tag := range []string{"article", "li", "div", "section"} {
		t.Run(tag, func(t *testing.T) {
			record := `<` + tag + ` class="comment"><p>Share your feedback publicly; private reports are being ignored.</p></` + tag + `>`
			if tag == "li" {
				record = `<ul>` + record + `</ul>`
			}
			html := `<section class="comments">` + record + `</section>`
			doc, err := ExtractBytes([]byte(html), "https://example.com/conversation/direct", WithPageType(PageTypeDiscussion))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(doc.Text, "private reports are being ignored") {
				t.Fatalf("short marked %s comment was discarded: %s", tag, doc.Text)
			}
		})
	}
}

func TestLongDirectStatusLikeCommentIsKept(t *testing.T) {
	html := `<section class="comments"><p>Comments are disabled after upgrading to version two; how can I enable them again for all users?</p></section>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/conversation/direct", WithPageType(PageTypeDiscussion))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "how can I enable them again") {
		t.Fatalf("long status-like comment was discarded: %s", doc.Text)
	}
}

func TestPromptLikeMarkedCommentWithReplyButtonIsKept(t *testing.T) {
	html := `<section class="comments"><article class="comment"><p>Share your feedback publicly; private reports are being ignored.</p><button class="reply">Reply</button></article></section>`
	doc, err := ExtractBytes([]byte(html), "https://example.com/conversation/direct", WithPageType(PageTypeDiscussion))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "private reports are being ignored") {
		t.Fatalf("marked comment with reply button was discarded: %s", doc.Text)
	}
	if strings.Contains(doc.Text, "Reply") {
		t.Fatalf("reply control was retained: %s", doc.Text)
	}
}

func TestEmptyCommentsDoNotDisableReadabilityFallback(t *testing.T) {
	prose := strings.Repeat("A", 101)
	extract := func(extra string) *Document {
		t.Helper()
		html := `<html><body><article><p>` + prose + `</p></article>` + extra + `</body></html>`
		doc, err := ExtractBytes([]byte(html), "https://example.com/articles/short")
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	withoutWidget := extract("")
	withWidget := extract(`<section class="comments"><p>No comments yet.</p><button>Open discussion</button></section>`)
	if withoutWidget.Quality < .58 || withWidget.Quality != withoutWidget.Quality {
		t.Fatalf("quality without widget = %v, with widget = %v", withoutWidget.Quality, withWidget.Quality)
	}
	if strings.Contains(withWidget.Text, "No comments yet") {
		t.Fatalf("readability restored empty comments: %s", withWidget.Text)
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

func TestArticleContinuityBridgesInlineAdvertisement(t *testing.T) {
	html := `<html><head><meta property="og:type" content="article"><title>Article title</title></head><body>
<article><h1>Article title</h1>
<p>Long introduction explains the subject with enough detail to establish the article body clearly.</p>
<p>More article prose develops the evidence and gives readers important context for what follows.</p>
<aside class="advertisement"><p>Sponsored product copy that must never appear.</p><a rel="sponsored" href="/buy">Buy now</a></aside>
<p>Important continuation explains the result after the interruption in substantive detail.</p>
<p>Article conclusion summarizes the findings and their consequences for the reader.</p>
</article>
<section class="author-profile"><h2>About the author</h2><p>Generic biography that is outside the article body.</p></section>
<section class="related-stories"><h2>Related stories</h2><div class="story-card"><p>Unrelated card one.</p></div><div class="story-card"><p>Unrelated card two.</p></div></section>
</body></html>`
	doc, err := Extract(strings.NewReader(html), "https://example.com/article")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Long introduction", "More article prose", "Important continuation", "Article conclusion"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing article prose %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Sponsored product", "Buy now", "Generic biography", "Unrelated card"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included auxiliary content %q: %s", unwanted, doc.Text)
		}
	}
}

func TestGenericProseContinuityBridgesInlineAdvertisement(t *testing.T) {
	html := `<main><div class="entry-content"><h1>Article title</h1>
<p>The opening paragraph establishes a substantial prose region and introduces the central subject.</p>
<p>A second paragraph supplies enough supporting detail to confirm the document body.</p>
<div class="advertisement"><a rel="sponsored" href="/offer">Advertisement offer</a></div>
<p>The important continuation remains part of the same prose region after the interruption.</p>
<p>The final paragraph records the conclusion in source order for every reader.</p>
</div></main>`
	doc, err := Extract(strings.NewReader(html), "https://example.com/story")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"opening paragraph", "second paragraph", "important continuation", "final paragraph"} {
		if !strings.Contains(strings.ToLower(doc.Text), want) {
			t.Errorf("missing generic prose %q: %s", want, doc.Text)
		}
	}
	if strings.Contains(doc.Text, "Advertisement offer") {
		t.Fatalf("included inline advertisement: %s", doc.Text)
	}
}

func TestAffiliateWidgetDoesNotHideOrReclassifyContentContainer(t *testing.T) {
	html := `<main><div class="entry-content"><h1>Independent field report</h1>
<p>The opening report describes the observation, its background, and the evidence gathered during a careful investigation.</p>
<p>A second editorial paragraph explains the method and gives readers enough context to understand the reported result.</p>
<div class="affiliate-product">
<h2 class="product-title">Portable field recorder</h2>
<div class="product-feature">Long battery life for field work.</div>
<div class="product-price">Special price today.</div>
<ul class="product-features"><li>Compact product design.</li><li>Includes a carrying case.</li></ul>
<a rel="sponsored" href="/buy">Buy this product</a>
</div>
<p>The report continues after the commercial interruption with analysis of the collected evidence.</p>
<p>The concluding editorial paragraph summarizes the finding and identifies the remaining uncertainty.</p>
</div></main>`
	doc, err := Extract(strings.NewReader(html), "https://example.com/field-report")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"opening report", "second editorial paragraph", "report continues", "concluding editorial paragraph"} {
		if !strings.Contains(strings.ToLower(doc.Text), want) {
			t.Errorf("missing editorial content %q: %s", want, doc.Text)
		}
	}
	for _, unwanted := range []string{"Portable field recorder", "Special price", "Buy this product"} {
		if strings.Contains(doc.Text, unwanted) {
			t.Errorf("included affiliate widget content %q: %s", unwanted, doc.Text)
		}
	}
	if doc.PageType == PageTypeProduct || doc.PageType == PageTypeCollection || doc.PageType == PageTypeListing {
		t.Fatalf("affiliate widget changed page type to %s", doc.PageType)
	}
}

func TestMalformedParagraphContinuesAfterBlockAdvertisement(t *testing.T) {
	html := `<article><h1>Article title</h1><p>A substantial introduction establishes the article body before malformed publisher markup.
<div class="advertisement"><a rel="sponsored" href="/offer">Advertisement offer</a></div>
Important continuation text remains useful article prose even though the HTML parser places it directly in the content container.<br>
The article conclusion also remains available after the inline advertisement.</p></article>`
	doc, err := Extract(strings.NewReader(html), "https://example.com/malformed", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"substantial introduction", "Important continuation text", "article conclusion"} {
		if !strings.Contains(doc.Text, want) {
			t.Errorf("missing malformed-flow prose %q: %s", want, doc.Text)
		}
	}
	if strings.Contains(doc.Text, "Advertisement offer") {
		t.Fatalf("included malformed inline advertisement: %s", doc.Text)
	}
}

func TestArticleContinuityAcrossStructuralNodes(t *testing.T) {
	anchorOne := "The selected opening analysis establishes the dominant article body with detailed technical evidence for readers. " + strings.Repeat("It also supplies enough explanatory context to make this an independently strong paragraph. ", 4)
	anchorTwo := "The selected closing analysis continues the same article body with detailed technical evidence for readers. " + strings.Repeat("It also supplies enough explanatory context to make this an independently strong paragraph. ", 4)
	figure := `<figure><img src="https://example.com/diagram.jpg" width="800" height="600" alt="Technical diagram"><figcaption>Technical diagram caption</figcaption></figure>`

	tests := []struct {
		name, middle, want string
		unwanted           string
		images             bool
	}{
		{
			name:   "figure before rejected paragraph",
			middle: figure + `<p>The process changed after that result.</p>`,
			want:   "The process changed after that result.",
		},
		{
			name: "share toolbar and image",
			middle: `<div class="share-toolbar" role="toolbar"><button>Share</button><button>Copy link</button></div>` +
				`<p>The next experiment confirmed the change.</p><img src="https://example.com/result.jpg" width="900" height="600" alt="Experiment result">`,
			want:     "The next experiment confirmed the change.",
			unwanted: "Copy link",
			images:   true,
		},
		{
			name:     "advertisement paragraph remains excluded",
			middle:   `<aside class="advertisement"><p>This sponsored card describes an unrelated product that readers should buy immediately.</p></aside>`,
			unwanted: "sponsored card",
		},
		{
			name:   "several consecutive figures",
			middle: figure + figure + figure + figure + `<p>The measurements still agreed.</p>` + figure + figure + figure,
			want:   "The measurements still agreed.",
		},
		{
			name:   "short transition",
			middle: `<p>That changed in 2024.</p>`,
			want:   "That changed in 2024.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := `<html><head><meta property="og:type" content="article"><title>Continuity report</title></head><body><article class="newsletter"><h1>Continuity report</h1><p>` + anchorOne + `</p>` + tt.middle + `<p>` + anchorTwo + `</p></article></body></html>`
			doc, err := ExtractBytes([]byte(html), "https://example.com/report", WithIncludeImages(tt.images), WithDiagnostics(true))
			if err != nil {
				t.Fatal(err)
			}
			if tt.want != "" && !strings.Contains(doc.Text, tt.want) {
				t.Fatalf("missing bridged paragraph %q:\n%s", tt.want, doc.Text)
			}
			if tt.unwanted != "" && strings.Contains(doc.Text, tt.unwanted) {
				t.Fatalf("included auxiliary text %q:\n%s", tt.unwanted, doc.Text)
			}
			if strings.Index(doc.Text, anchorOne[:40]) > strings.Index(doc.Text, anchorTwo[:40]) {
				t.Fatalf("article blocks were emitted out of DOM order:\n%s", doc.Text)
			}
			if tt.want != "" {
				for _, block := range doc.Diagnostics.Blocks {
					if strings.Contains(block.Text, tt.want) && (!block.Selected || block.Score < 1) {
						t.Fatalf("diagnostic does not report restored block: %+v", block)
					}
				}
			}
		})
	}
}
