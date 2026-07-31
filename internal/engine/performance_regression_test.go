package engine

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/net/html"
)

func TestNormalizedTextAtLeastMatchesMaterializedText(t *testing.T) {
	sources := []string{
		`<p>plain text</p>`,
		`<p>  split <em> across </em> nodes  </p>`,
		`<p>visible <span hidden>ignored text</span> café</p>`,
		`<p><span> </span><span>alpha</span><span> </span><span>beta</span></p>`,
	}
	for _, source := range sources {
		root, err := html.Parse(strings.NewReader(source))
		if err != nil {
			t.Fatal(err)
		}
		length := utf8.RuneCountInString(normalizeText(nodeText(root)))
		for limit := 0; limit <= length+2; limit++ {
			if got, want := normalizedTextAtLeast(root, limit), length >= limit; got != want {
				t.Fatalf("normalizedTextAtLeast(%q, %d) = %v, want %v (length %d)",
					source, limit, got, want, length)
			}
		}
	}
}

func TestScriptRequirementNoticeStreamsNormalizedText(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name:   "phrases split across inline nodes",
			source: `<div id="notice">Please enable <strong>JavaScript</strong> to run this <em>app</em>.</div>`,
			want:   true,
		},
		{
			name:   "unicode case and whitespace",
			source: "<div id=\"notice\">ENABLE\u00a0<span>JAVASCRIPT</span> for this WEB APPLICATION.</div>",
			want:   true,
		},
		{
			name:   "hidden phrase ignored",
			source: `<div id="notice">Please enable JavaScript.<span hidden>This web application requires JavaScript.</span></div>`,
			want:   false,
		},
		{
			name:   "hidden subtree excluded from length",
			source: `<div id="notice">Enable JavaScript to run this app.<span hidden>` + strings.Repeat("ignored ", 1000) + `</span></div>`,
			want:   true,
		},
		{
			name:   "semantic content retained",
			source: `<main><div id="notice">Enable JavaScript to run this app.</div></main>`,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := html.Parse(strings.NewReader(tt.source))
			if err != nil {
				t.Fatal(err)
			}
			var notice *html.Node
			walk(root, func(n *html.Node) bool {
				if n.Type == html.ElementNode && attrValue(n, "id") == "notice" {
					notice = n
					return false
				}
				return notice == nil
			})
			if notice == nil {
				t.Fatal("notice element not found")
			}
			if got := isScriptRequirementNotice(notice); got != tt.want {
				t.Fatalf("isScriptRequirementNotice() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScriptRequirementNoticeStopsAtNormalizedRuneLimit(t *testing.T) {
	const evidence = "enable javascript requires javascript"
	for _, tt := range []struct {
		length int
		want   bool
	}{
		{length: 300, want: true},
		{length: 301, want: false},
	} {
		text := evidence + strings.Repeat("é", tt.length-utf8.RuneCountInString(evidence))
		notice := &html.Node{Type: html.ElementNode, Data: "div"}
		notice.AppendChild(&html.Node{Type: html.TextNode, Data: text})
		// This tail must not be visited after the normalized text reaches 301
		// runes. It also guards against byte-based length counting.
		tail := &html.Node{Type: html.ElementNode, Data: "span"}
		tail.AppendChild(&html.Node{Type: html.TextNode, Data: strings.Repeat("tail", 1000)})
		notice.AppendChild(tail)

		// At 300 runes, the separator before the visible tail is rune 301. The
		// scan can reject the notice without visiting the large tail text.
		if isScriptRequirementNotice(notice) {
			t.Fatal("notice with visible tail was not rejected")
		}
		// Hide the tail for the exact boundary assertion.
		tail.Attr = []html.Attribute{{Key: "hidden"}}
		got := isScriptRequirementNotice(notice)
		if got != tt.want {
			t.Fatalf("%d normalized runes: isScriptRequirementNotice() = %v, want %v", tt.length, got, tt.want)
		}
	}
}

func BenchmarkScriptRequirementNoticeStreaming(b *testing.B) {
	root, err := html.Parse(strings.NewReader(`<div id="notice">Please enable <strong>JavaScript</strong> to run this <em>app</em>.</div>`))
	if err != nil {
		b.Fatal(err)
	}
	var notice *html.Node
	walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && attrValue(n, "id") == "notice" {
			notice = n
			return false
		}
		return notice == nil
	})
	b.ReportAllocs()
	for b.Loop() {
		if !isScriptRequirementNotice(notice) {
			b.Fatal("notice was not recognized")
		}
	}
}

func TestBlockSubtreeEvidenceMatchesIndependentScans(t *testing.T) {
	root, err := html.Parse(strings.NewReader(`<main><p>outside <a href="/one"> one  <em>two</em> </a><button>act</button><span hidden><a href="/hidden">hidden</a><input></span><a href="/two"><span>three</span><span>four</span></a></p></main>`))
	if err != nil {
		t.Fatal(err)
	}
	links, controlCount := blockSubtreeEvidence(root)
	if want := linkTextLength(root); links != want {
		t.Fatalf("linked text length = %d, want %d", links, want)
	}
	if want := controls(root); controlCount != want {
		t.Fatalf("controls = %d, want %d", controlCount, want)
	}
	text, shapeLinks, shapeControls := subtreeShapeEvidence(root)
	if want := utf8.RuneCountInString(normalizeText(nodeText(root))); text != want {
		t.Fatalf("text length = %d, want %d", text, want)
	}
	if shapeLinks != links || shapeControls != controlCount {
		t.Fatalf("shape evidence = (%d, %d), want (%d, %d)", shapeLinks, shapeControls, links, controlCount)
	}
}

func TestOverrideIrrelevantInvalidatesDescendantCache(t *testing.T) {
	root := &html.Node{Type: html.DocumentNode}
	parent := &html.Node{Type: html.ElementNode, Data: "div"}
	heading := &html.Node{Type: html.ElementNode, Data: "h2"}
	heading.AppendChild(&html.Node{Type: html.TextNode, Data: "Promoted title"})
	parent.AppendChild(heading)
	root.AppendChild(parent)

	a := &analysis{root: root, nodeStates: make(map[*html.Node]nodeState)}
	if a.hasIrrelevantAncestor(heading) {
		t.Fatal("neutral nested heading was unexpectedly irrelevant")
	}
	if got, known := a.nodeStates[heading].irrelevantAncestor.value(); !known || got {
		t.Fatalf("heading ancestor result was not cached as false: got %t, known %t", got, known)
	}

	// Title restoration performs this kind of late override after probing the
	// selected content. Descendant caches must observe the changed parent.
	a.overrideIrrelevant(parent, true)
	if !a.hasIrrelevantAncestor(heading) {
		t.Fatal("nested heading retained stale relevant-ancestor result after override")
	}
}

func TestSubscriptionTextEvidenceStreamsAcrossElements(t *testing.T) {
	tests := []struct {
		name                   string
		source                 string
		cta, consent, honeypot bool
	}{
		{
			name:     "split phrases",
			source:   `<section>Sign<em>up</em><p>Read our privacy<a>policy</a>.</p><span>Leave this field<b>unchanged</b></span></section>`,
			cta:      true,
			consent:  true,
			honeypot: true,
		},
		{
			name:    "punctuation separates words",
			source:  `<section>Terms-of-use apply</section>`,
			consent: true,
		},
		{
			name:   "unicode lowercase maps to ASCII",
			source: `<section>MAİLING LIST</section>`,
			cta:    true,
		},
		{
			name:    "unicode simple fold maps to ASCII",
			source:  `<section>termſ of use</section>`,
			consent: true,
		},
		{
			name:   "word sequence does not match inside word",
			source: `<section>A proprietarypolicy document</section>`,
		},
		{
			name:   "hidden evidence ignored",
			source: `<section><span hidden>Subscribe privacy policy do not fill</span>Article text</section>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := html.Parse(strings.NewReader(tt.source))
			if err != nil {
				t.Fatal(err)
			}
			cta, consent, honeypot := subscriptionTextEvidence(root)
			if cta != tt.cta || consent != tt.consent || honeypot != tt.honeypot {
				t.Fatalf("evidence = (%v, %v, %v), want (%v, %v, %v)",
					cta, consent, honeypot, tt.cta, tt.consent, tt.honeypot)
			}
		})
	}
}

func TestMarketingInteractionsStreamsNormalizedLabels(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   int
	}{
		{"long leading space", `<a>` + strings.Repeat(" ", 100) + `Get started</a>`, 1},
		{"long trailing space", `<a>Learn more` + strings.Repeat(" ", 100) + `</a>`, 1},
		{"exact label split across elements", `<a>Contact<strong>us</strong></a>`, 1},
		{"exact label with extra text", `<a>Learn more about this</a>`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := html.Parse(strings.NewReader(tt.source))
			if err != nil {
				t.Fatal(err)
			}
			interactions, links := marketingInteractions(root)
			if interactions != tt.want || links != 1 {
				t.Fatalf("marketing interactions, links = (%d, %d), want (%d, 1)",
					interactions, links, tt.want)
			}
		})
	}
}

func TestNormalizeTextFastMatchesNormalizeText(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"single word", "hello"},
		{"already normal", "this is normal text"},
		{"leading space", " leading space"},
		{"trailing space", "trailing space "},
		{"double space", "double  space"},
		{"tab between", "tab\tbetween"},
		{"newline between", "line1\nline2"},
		{"non-ascii", "caf\u00e9 au lait"},
		{"mixed case", "Mixed Case Text"},
		{"single char", "x"},
		{"punctuation", "hello, world!"},
		{"multiple spaces", "a    b"},
		{"only spaces", "   "},
		{"tab at start", "\tleading tab"},
		{"cr between", "line1\r\nline2"},
		{"form feed", "a\fb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fastResult, fastOK := normalizeTextFast(tt.input)
			normalResult := normalizeText(tt.input)
			if fastOK && fastResult != normalResult {
				t.Fatalf("normalizeTextFast(%q) = (%q, true) but normalizeText(%q) = %q",
					tt.input, fastResult, tt.input, normalResult)
			}
			_ = normalResult
		})
	}
}

func TestHasBoilerplateTokenAttributes(t *testing.T) {
	tests := []struct {
		name string
		html string
		want bool
	}{
		{"no attributes", `<div>content</div>`, false},
		{"social class", `<div class="social-media">content</div>`, true},
		{"social id", `<div id="social-follow">content</div>`, true},
		{"social in compound", `<div class="social-impact">content</div>`, false},
		{"follow in class", `<div class="social-follow-links">content</div>`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := html.Parse(strings.NewReader(tt.html))
			if err != nil {
				t.Fatal(err)
			}
			var div *html.Node
			var find func(*html.Node)
			find = func(n *html.Node) {
				if div != nil {
					return
				}
				if n.Type == html.ElementNode && n.Data == "div" {
					div = n
					return
				}
				for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
					find(ch)
				}
			}
			find(root)
			if div == nil {
				t.Fatal("div element not found")
			}
			if got := hasBoilerplateTokenAttributes(div); got != tt.want {
				t.Errorf("hasBoilerplateTokenAttributes() = %v, want %v", got, tt.want)
			}
		})
	}
}
