package pagemark

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func TestBlockSubtreeEvidenceMatchesIndependentScans(t *testing.T) {
	parsed, err := html.Parse(strings.NewReader(`<main><p>outside <a href="/one"> one  <em>two</em> </a><button>act</button><span hidden><a href="/hidden">hidden</a><input></span><a href="/two"><span>three</span><span>four</span></a></p></main>`))
	if err != nil {
		t.Fatal(err)
	}

	manual := &html.Node{Type: html.ElementNode, Data: "DIV"}
	link := &html.Node{Type: html.ElementNode, Data: "A"}
	link.AppendChild(&html.Node{Type: html.TextNode, Data: "  mixed"})
	span := &html.Node{Type: html.ElementNode, Data: "SPAN"}
	span.AppendChild(&html.Node{Type: html.TextNode, Data: "case  "})
	link.AppendChild(span)
	manual.AppendChild(link)
	manual.AppendChild(&html.Node{Type: html.ElementNode, Data: "BuTtOn"})
	hidden := &html.Node{Type: html.ElementNode, Data: "DIV", Attr: []html.Attribute{{Key: "HiDdEn"}}}
	hidden.AppendChild(&html.Node{Type: html.ElementNode, Data: "InPuT"})
	manual.AppendChild(hidden)

	for name, root := range map[string]*html.Node{"parsed": parsed, "manual mixed-case": manual} {
		t.Run(name, func(t *testing.T) {
			links, controlCount := blockSubtreeEvidence(root)
			if want := linkTextLength(root); links != want {
				t.Fatalf("linked text length = %d, want %d", links, want)
			}
			if want := controls(root); controlCount != want {
				t.Fatalf("controls = %d, want %d", controlCount, want)
			}
		})
	}
}

func TestExtractNodeAcceptsMixedCaseManualTree(t *testing.T) {
	document := &html.Node{Type: html.DocumentNode}
	htmlNode := &html.Node{Type: html.ElementNode, Data: "HTML"}
	body := &html.Node{Type: html.ElementNode, Data: "BODY"}
	main := &html.Node{Type: html.ElementNode, Data: "MAIN"}
	article := &html.Node{Type: html.ElementNode, Data: "ARTICLE"}
	heading := &html.Node{Type: html.ElementNode, Data: "H1"}
	heading.AppendChild(&html.Node{Type: html.TextNode, Data: "Manual tree title"})
	paragraph := &html.Node{Type: html.ElementNode, Data: "P"}
	paragraph.AppendChild(&html.Node{Type: html.TextNode, Data: "This manually constructed mixed-case article contains enough substantive prose to be selected without changing its source DOM."})
	article.AppendChild(heading)
	article.AppendChild(paragraph)
	main.AppendChild(article)
	body.AppendChild(main)
	htmlNode.AppendChild(body)
	document.AppendChild(htmlNode)

	doc, err := ExtractNode(document, "https://example.com/manual", WithPageType(PageTypeArticle))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "manually constructed mixed-case article") {
		t.Fatalf("mixed-case manual tree content was not extracted: %q", doc.Text)
	}
}

func TestAtomizedManualTreeRetainsMixedCaseAttributeSupport(t *testing.T) {
	n := &html.Node{
		Type:     html.ElementNode,
		Data:     "DIV",
		DataAtom: atom.Div,
		Attr: []html.Attribute{
			{Key: "ID", Val: "Article"},
			{Key: "ClAsS", Val: "Post Content"},
			{Key: "RoLe", Val: "MAIN"},
		},
	}

	if got := attrValue(n, "class"); got != "Post Content" {
		t.Fatalf("mixed-case attribute value = %q, want %q", got, "Post Content")
	}
	if !elementContainsAny(n, "article") {
		t.Fatal("mixed-case token attribute was not recognized")
	}
	if got := elementTokens(n); got != "article post content main" {
		t.Fatalf("element tokens = %q, want %q", got, "article post content main")
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
	if got := a.nodeStates[heading].irrelevantAncestor; got != 1 {
		t.Fatalf("heading ancestor result was not cached: got %d, want 1", got)
	}

	// Title restoration performs this kind of late override after probing the
	// selected content. Descendant caches must observe the changed parent.
	a.overrideIrrelevant(parent, true)
	if !a.hasIrrelevantAncestor(heading) {
		t.Fatal("nested heading retained stale relevant-ancestor result after override")
	}
}
