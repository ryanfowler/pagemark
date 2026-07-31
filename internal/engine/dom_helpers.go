package engine

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/dom"
	"golang.org/x/net/html"
)

func rawNodeText(n *html.Node) string {
	var b strings.Builder
	walk(n, func(x *html.Node) bool {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		return true
	})
	return b.String()
}
func nodeText(n *html.Node) string {
	var b strings.Builder
	var first string
	texts := 0
	appendNodeText(n, &b, &first, &texts)
	if texts <= 1 {
		return first
	}
	return b.String()
}

// appendNodeText uses a dedicated traversal instead of the generic callback
// walker. Text collection is one of the hottest complete-subtree operations,
// and avoiding an indirect callback for every DOM node is measurable on large
// pages while retaining the single-text-node allocation fast path.
func appendNodeText(n *html.Node, b *strings.Builder, first *string, texts *int) {
	if n == nil || dom.Hidden(n) {
		return
	}
	if n.Type == html.TextNode {
		(*texts)++
		if *texts == 1 {
			*first = n.Data
		} else {
			if *texts == 2 {
				b.Grow(len(*first) + 1 + len(n.Data))
				b.WriteString(*first)
			}
			b.WriteByte(' ')
			b.WriteString(n.Data)
		}
		return
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		appendNodeText(ch, b, first, texts)
	}
}
func walk(n *html.Node, f func(*html.Node) bool) {
	if n == nil || !f(n) {
		return
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		walk(ch, f)
	}
}

// walkVisibleReverse visits visible nodes in the reverse of walk's preorder
// without auxiliary storage.
func walkVisibleReverse(n *html.Node, f func(*html.Node)) {
	if n == nil || hardHidden(n) {
		return
	}
	for ch := n.LastChild; ch != nil; ch = ch.PrevSibling {
		walkVisibleReverse(ch, f)
	}
	f(n)
}
func normalizeText(s string) string {
	start := 0
	for start < len(s) {
		space, size := textRuneSpace(s[start:])
		if !space {
			break
		}
		start += size
	}
	if start == len(s) {
		return ""
	}

	// Delay allocating until whitespace actually needs trimming or collapsing.
	i := start
	for i < len(s) {
		space, size := textRuneSpace(s[i:])
		if space {
			break
		}
		i += size
	}
	if i == len(s) {
		return s[start:]
	}

	var b strings.Builder
	b.Grow(len(s) - start)
	b.WriteString(s[start:i])
	for i < len(s) {
		for i < len(s) {
			space, size := textRuneSpace(s[i:])
			if !space {
				break
			}
			i += size
		}
		if i == len(s) {
			break
		}
		b.WriteByte(' ')
		run := i
		for i < len(s) {
			space, size := textRuneSpace(s[i:])
			if space {
				break
			}
			i += size
		}
		b.WriteString(s[run:i])
	}
	return b.String()
}

// textRuneSpace keeps normalization's overwhelmingly common ASCII path out of
// utf8.DecodeRuneInString and the Unicode whitespace tables.
func textRuneSpace(s string) (bool, int) {
	if s[0] < utf8.RuneSelf {
		c := s[0]
		return c == ' ' || c >= '\t' && c <= '\r', 1
	}
	r, size := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(r), size
}
func attrValue(n *html.Node, key string) string {
	for _, x := range n.Attr {
		if x.Key == key {
			return x.Val
		}
	}
	return ""
}
func firstNonempty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
