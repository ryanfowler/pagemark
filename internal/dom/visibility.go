// Package dom contains shared HTML tree rules.
package dom

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Hidden reports whether an element and its subtree must not appear in output.
func Hidden(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "script", "style", "template", "canvas", "svg", "iframe", "object", "embed":
		return true
	}
	if hasAttr(n, "hidden") || hasAttr(n, "inert") || strings.EqualFold(strings.TrimSpace(attr(n, "aria-hidden")), "true") {
		return true
	}
	style := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, attr(n, "style"))
	if strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(attr(n, "aria-modal")), "true")
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return true
		}
	}
	return false
}
