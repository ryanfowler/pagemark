package pagemark

import (
	"testing"

	"golang.org/x/net/html"
)

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
