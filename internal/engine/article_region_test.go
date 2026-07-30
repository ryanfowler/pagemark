package engine

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestArticleRegionReconstructsSiblingSectionsInOrder(t *testing.T) {
	// SelectionPrecision leaves the two short story segments unselected by primary
	// block scoring. Their shared publisher class lets the article-region
	// fallback recover them, while the similarly adjacent auxiliary regions stay
	// excluded.
	source := `<html><head><title>Field report</title><meta property="og:type" content="article"></head><body><div class="story-shell">
<div class="story-segment"><h1>Field report</h1><p>The introduction explains the detailed investigation, its setting, and enough unique evidence to anchor the complete authored report for readers.</p></div>
<div class="story-segment">By Ada Reporter.</div>
<div class="advertisement"><p>Buy the amazing distraction today.</p></div>
<div class="story-segment"><p>The body result matters.</p></div>
<section class="related-articles"><h2>Related articles</h2><div class="card"><a href="/one">Unrelated story one</a></div><div class="card"><a href="/two">Unrelated story two</a></div></section>
<div class="story-segment"><p>The final conclusion matters.</p></div>
</div></body></html>`
	doc, err := ExtractBytes([]byte(source), "https://example.com/field-report", WithPageType(PageTypeArticle), WithSelectionMode(SelectionPrecision), WithDiagnostics(true))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Diagnostics == nil || doc.Diagnostics.Fallback != "article-region" {
		t.Fatalf("fixture did not exercise reconstruction: diagnostics=%#v", doc.Diagnostics)
	}
	for _, want := range []string{"The introduction", "By Ada Reporter", "The body result", "The final conclusion"} {
		if !strings.Contains(doc.Markdown, want) {
			t.Errorf("missing %q:\n%s", want, doc.Markdown)
		}
	}
	for _, unwanted := range []string{"amazing distraction", "Unrelated story one", "Unrelated story two"} {
		if strings.Contains(doc.Markdown, unwanted) {
			t.Fatalf("auxiliary sibling %q was reconstructed:\n%s", unwanted, doc.Markdown)
		}
	}
	intro := strings.Index(doc.Markdown, "The introduction")
	body := strings.Index(doc.Markdown, "The body result")
	conclusion := strings.Index(doc.Markdown, "The final conclusion")
	if !(intro >= 0 && intro < body && body < conclusion) {
		t.Fatalf("article sections are out of document order:\n%s", doc.Markdown)
	}
	if doc.Title != "Field report" || strings.Contains(doc.Markdown, "# Field report") {
		t.Fatalf("title was not separated: title=%q markdown=%s", doc.Title, doc.Markdown)
	}
	if strings.Count(doc.Markdown, "The body result") != 1 {
		t.Fatalf("selected ancestor duplicated a selected block:\n%s", doc.Markdown)
	}
}

func TestArticleRegionJoinsThreeIndependentCandidates(t *testing.T) {
	source := `<html><head><meta property="og:type" content="article"><title>Joined report</title></head><body><div class="shell">
<div><h1>Joined report</h1><p>The opening finding gives useful context here.</p></div>
<div><p>The middle finding supplies important evidence.</p></div>
<div><p>The closing finding states the practical result.</p></div>
</div></body></html>`
	doc, err := ExtractBytes([]byte(source), "https://example.com/joined", WithPageType(PageTypeArticle), WithSelectionMode(SelectionPrecision), WithDiagnostics(true))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Diagnostics == nil || doc.Diagnostics.Fallback != "article-region" {
		t.Fatalf("fixture did not exercise shared-region reconstruction: %#v", doc.Diagnostics)
	}
	for _, want := range []string{"opening finding", "middle finding", "closing finding"} {
		if !strings.Contains(doc.Markdown, want) {
			t.Errorf("missing joined candidate %q:\n%s", want, doc.Markdown)
		}
	}
}

func TestNearArticleCandidatesArePairwiseNonOverlapping(t *testing.T) {
	root, err := html.Parse(strings.NewReader(`<body><div id="left"></div><div id="branch"><div id="nested"></div></div></body>`))
	if err != nil {
		t.Fatal(err)
	}
	nodes := make(map[string]*html.Node)
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if id := attrValue(n, "id"); id != "" {
			nodes[id] = n
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	candidates := []*articleRegionEvidence{
		{node: nodes["left"], score: 10, proseChars: 100},
		{node: nodes["branch"], score: 9, proseChars: 100},
		{node: nodes["nested"], score: 8, proseChars: 100},
	}
	near := nonOverlappingNearCandidates(candidates, regionRank(candidates[0]))
	if len(near) != 2 || near[0].node != nodes["left"] || near[1].node != nodes["branch"] {
		t.Fatalf("nested evidence counted as an independent region: %#v", near)
	}

	paragraph := &html.Node{Type: html.ElementNode, Data: "p"}
	paragraph.AppendChild(&html.Node{Type: html.TextNode, Data: "One eligible prose block must only contribute once."})
	nodes["nested"].AppendChild(paragraph)
	a := &analysis{
		root:       root,
		pageType:   PageTypeArticle,
		nodeStates: make(map[*html.Node]nodeState),
		blocks:     []block{{node: paragraph, kind: "p", text: nodeText(paragraph)}},
	}
	overlapping := []*articleRegionEvidence{{node: nodes["branch"]}, {node: nodes["nested"]}}
	if got, want := a.uniqueRegionProseChars(overlapping), len([]rune(nodeText(paragraph))); got != want {
		t.Fatalf("overlapping candidates counted prose more than once: got %d, want %d", got, want)
	}
}

func TestArticleRegionRejectsAdjacentAuxiliaryCohorts(t *testing.T) {
	source := `<html><head><meta property="og:type" content="article"><title>Careful analysis</title></head><body><main>
<article><h1>Careful analysis</h1><p>The main analysis gives a substantial explanation of the evidence and the method used to reach the reported result.</p><p>A second paragraph checks the limitations and gives readers enough context to interpret the result correctly.</p></article>
<section class="related-articles"><h2>Related articles</h2><div class="card"><a href="/one"><h3>Other story one</h3><p>An unrelated linked summary for readers.</p></a></div><div class="card"><a href="/two"><h3>Other story two</h3><p>Another unrelated linked summary for readers.</p></a></div></section>
<section id="comments"><h2>Comments</h2><p>A reader comment contains long sentence-like prose but is not part of the authored analysis and must stay out.</p><button>Reply</button></section>
<section class="newsletter-signup"><h2>Weekly newsletter</h2><p>Get every new report delivered directly to your inbox each week.</p><form><input type="email"><button>Subscribe</button></form></section>
</main></body></html>`
	doc, err := ExtractBytes([]byte(source), "https://example.com/analysis", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"Other story one", "Other story two", "reader comment", "Weekly newsletter", "delivered directly"} {
		if strings.Contains(doc.Markdown, unwanted) {
			t.Errorf("included auxiliary sibling %q:\n%s", unwanted, doc.Markdown)
		}
	}
	if !strings.Contains(doc.Markdown, "The main analysis") {
		t.Fatalf("primary article was lost:\n%s", doc.Markdown)
	}
}

func TestArticleRegionDoesNotTurnCardCohortIntoArticle(t *testing.T) {
	source := `<html><head><title>Story index</title></head><body><main><div class="cards">
<article class="card"><h2><a href="/a">First candidate</a></h2><p><a href="/a">A linked preview describing the first unrelated story in this collection.</a></p></article>
<article class="card"><h2><a href="/b">Second candidate</a></h2><p><a href="/b">A linked preview describing the second unrelated story in this collection.</a></p></article>
<article class="card"><h2><a href="/c">Third candidate</a></h2><p><a href="/c">A linked preview describing the third unrelated story in this collection.</a></p></article>
</div></main></body></html>`
	doc, err := ExtractBytes([]byte(source), "https://example.com/stories", WithPageType(PageTypeArticle))
	if err == nil && doc != nil {
		count := 0
		for _, title := range []string{"First candidate", "Second candidate", "Third candidate"} {
			if strings.Contains(doc.Markdown, title) {
				count++
			}
		}
		if count == 3 {
			t.Fatalf("near-tied cards were reconstructed as one article:\n%s", doc.Markdown)
		}
	}
}

func TestArticleRegionExtractionDoesNotMutateDOM(t *testing.T) {
	root, err := html.Parse(strings.NewReader(`<html><head><meta property="og:type" content="article"></head><body><div><div class="story-segment"><p>This first sibling contains a sufficiently detailed article introduction that anchors reconstruction for readers.</p></div><div class="story-segment"><p>This conclusion matters.</p></div></div></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	var before bytes.Buffer
	if err := html.Render(&before, root); err != nil {
		t.Fatal(err)
	}
	doc, err := ExtractNode(root, "https://example.com/immutable", WithPageType(PageTypeArticle), WithSelectionMode(SelectionPrecision), WithDiagnostics(true))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Diagnostics == nil || doc.Diagnostics.Fallback != "article-region" {
		t.Fatalf("fixture did not exercise reconstruction: %#v", doc.Diagnostics)
	}
	var after bytes.Buffer
	if err := html.Render(&after, root); err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatal("article-region extraction mutated the source DOM")
	}
}
