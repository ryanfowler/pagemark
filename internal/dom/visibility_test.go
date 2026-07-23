package dom

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestDialogVisibilityFollowsOpenAttribute(t *testing.T) {
	root, err := html.Parse(strings.NewReader(`<body><dialog>closed</dialog><dialog open>open</dialog></body>`))
	if err != nil {
		t.Fatal(err)
	}
	var dialogs []*html.Node
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "dialog" {
			dialogs = append(dialogs, n)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	if len(dialogs) != 2 {
		t.Fatalf("found %d dialogs, want 2", len(dialogs))
	}
	if !Hidden(dialogs[0]) {
		t.Fatal("closed dialog reported visible")
	}
	if Hidden(dialogs[1]) {
		t.Fatal("open dialog reported hidden")
	}
}
