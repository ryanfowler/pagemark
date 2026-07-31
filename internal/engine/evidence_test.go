package engine

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestEvidenceIndexSeparatesLocalAndDescendantShape(t *testing.T) {
	root, err := html.Parse(strings.NewReader(`
		<html><body><section id="region">
			<div class="post-body"><p>Visible prose</p></div>
			<form action="/subscribe"><input type="email"><button>Join</button></form>
			<div hidden><p>Hidden prose</p><button>Hidden control</button></div>
		</section></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	index, err := buildEvidence(root, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	region := elementByID(root, "region")
	postBody := firstElementWithClass(root, "post-body")
	if region == nil || postBody == nil {
		t.Fatal("fixture nodes not found")
	}

	if !index.has(region, evidenceDiscussionBodyDescendant) || !index.has(region, evidenceBlockDescendant) {
		t.Fatal("region did not inherit descendant shape")
	}
	if index.has(postBody, evidenceDiscussionBodyDescendant) {
		t.Fatal("a node must not count itself as its own discussion-body descendant")
	}
	if !index.has(region, evidenceForm) || !index.has(region, evidenceEmailInput) ||
		!index.has(region, evidenceSubscriptionForm) {
		t.Fatal("region did not inherit subscription evidence")
	}
	if controls, ok := index.controlCount(region); !ok || controls != 2 {
		t.Fatalf("control count = %d, %v; want 2, true", controls, ok)
	}
	if index.sections != 1 {
		t.Fatalf("section count = %d; want 1", index.sections)
	}
}

func TestEvidenceQueriesFallBackForUnindexedNodes(t *testing.T) {
	indexedRoot := &html.Node{Type: html.ElementNode, Data: "div"}
	index, err := buildEvidence(indexedRoot, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	a := &analysis{evidence: index}

	unindexed := &html.Node{Type: html.ElementNode, Data: "div"}
	postBody := &html.Node{Type: html.ElementNode, Data: "div", Attr: []html.Attribute{{Key: "class", Val: "comment-body"}}}
	paragraph := &html.Node{Type: html.ElementNode, Data: "p"}
	article := &html.Node{Type: html.ElementNode, Data: "article"}
	postBody.AppendChild(paragraph)
	unindexed.AppendChild(postBody)
	unindexed.AppendChild(article)

	if !a.hasDiscussionBodyDescendant(unindexed) {
		t.Error("unindexed discussion body was not found")
	}
	if !a.hasBlockDescendant(unindexed) {
		t.Error("unindexed block was not found")
	}
	if !a.hasArticleBodyDescendant(unindexed) {
		t.Error("unindexed article body was not found")
	}
	if a.hasDiscussionBodyDescendant(nil) || a.hasBlockDescendant(nil) || a.hasArticleBodyDescendant(nil) {
		t.Error("nil nodes must not have descendant evidence")
	}

	hidden := &html.Node{Type: html.ElementNode, Data: "div", Attr: []html.Attribute{{Key: "hidden"}}}
	hidden.AppendChild(&html.Node{Type: html.ElementNode, Data: "article"})
	if a.hasArticleBodyDescendant(hidden) {
		t.Error("hidden unindexed root supplied article-body evidence")
	}
}

func elementByID(root *html.Node, id string) *html.Node {
	var result *html.Node
	walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && attrValue(n, "id") == id {
			result = n
			return false
		}
		return result == nil
	})
	return result
}

func firstElementWithClass(root *html.Node, class string) *html.Node {
	var result *html.Node
	walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && hasExactClass(n, class) {
			result = n
			return false
		}
		return result == nil
	})
	return result
}
