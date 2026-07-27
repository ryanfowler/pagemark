package dom

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func TestHiddenStyleCascade(t *testing.T) {
	tests := []struct {
		style  string
		hidden bool
	}{
		{style: "display:none", hidden: true},
		{style: "DISPLAY: NONE", hidden: true},
		{style: "display:block", hidden: false},
		{style: "display:none; display:block", hidden: false},
		{style: "display:block; display:none", hidden: true},
		{style: "display:none !important; display:block", hidden: true},
		{style: "display:none; display:block !important", hidden: false},
		{style: "display:none !IMPORTANT; display:block", hidden: true},
		{style: `display:none !\69mportant; display:block`, hidden: true},
		{style: `display:block; display:n\6f ne`, hidden: true},
		{style: `display:none; display:v\61 r\28 --x\29`, hidden: true},
		{style: `d\69splay:none`, hidden: true},
		{style: `visibility:h\69 dden`, hidden: true},
		{style: "visibility:hidden; visibility:visible", hidden: false},
		{style: "visibility:visible; visibility:hidden", hidden: true},
		{style: "visibility:hidden!important; visibility:visible", hidden: true},
		{style: "x-display:none", hidden: false},
		{style: "notvisibility:hidden", hidden: false},
		{style: "display", hidden: false},
		{style: "display: ; visibility: visible", hidden: false},
		{style: "color:red; display : none ; color:blue", hidden: true},
		{style: "display:none /* hidden */", hidden: true},
		{style: "display:none; display:", hidden: true},
		{style: "display:none; display:bogus", hidden: true},
		{style: "display:none; display:bogus !important", hidden: true},
		{style: "visibility:hidden; visibility:", hidden: true},
		{style: "visibility:hidden; visibility:bogus", hidden: true},
		{style: "display:none; display:inline flex", hidden: false},
		{style: "--layout: flex; display:none; display:inline var(--layout)", hidden: false},
		{style: `--layout: flex; display:none; display:inline \76 ar(--layout)`, hidden: false},
		{style: "display:none; display:inline ENV(layout)", hidden: false},
		{style: `display:none; display:"var(--layout)"`, hidden: true},
		{style: "visibility:hidden; visibility:collapse", hidden: false},
		{style: "display:none; display: /* empty */", hidden: true},
		{style: `display:none; --note:"; display:block"`, hidden: true},
		{style: `display:none; --note:func(; display:block)`, hidden: true},
		{style: `--note:"; display:none"; display:block`, hidden: false},
		{style: "display:none /* note */ ! IMPORTANT /* tail */; display:block", hidden: true},
		{style: "visibility: collapse", hidden: false},
		{style: "opacity: 0", hidden: false},
		{style: "content-visibility: hidden", hidden: false},
	}

	for _, test := range tests {
		t.Run(test.style, func(t *testing.T) {
			if got := hiddenStyle(test.style); got != test.hidden {
				t.Fatalf("hiddenStyle(%q) = %v, want %v", test.style, got, test.hidden)
			}
		})
	}
}

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

func TestHiddenAcceptsAtomizedMixedCaseManualNodes(t *testing.T) {
	tests := []struct {
		name string
		node *html.Node
	}{
		{
			name: "mixed-case hidden attribute",
			node: &html.Node{
				Type:     html.ElementNode,
				Data:     "div",
				DataAtom: atom.Div,
				Attr:     []html.Attribute{{Key: "HiDdEn"}},
			},
		},
		{
			name: "mixed-case excluded element",
			node: &html.Node{
				Type:     html.ElementNode,
				Data:     "SCRIPT",
				DataAtom: atom.Script,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !Hidden(test.node) {
				t.Fatal("atomized mixed-case manual node reported visible")
			}
		})
	}
}
