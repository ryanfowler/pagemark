// Package dom contains shared HTML tree rules.
package dom

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Hidden reports whether an element and its subtree must not appear in output.
func Hidden(n *html.Node) bool {
	return hiddenElement(n) || hiddenByAttributes(n)
}

// HiddenExceptARIA reports hidden content while allowing only aria-hidden to
// be ignored. Math renderers commonly mark their visual glyph branch
// aria-hidden because an accessible branch is present alongside it. Callers
// using this exception must still reject non-content elements, CSS-hidden
// content, hidden/inert subtrees, and modal UI.
func HiddenExceptARIA(n *html.Node) bool {
	return hiddenElement(n) || hiddenByNonARIAAttributes(n)
}

func hiddenElement(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "script", "style", "template", "canvas", "svg", "iframe", "object", "embed":
		return true
	}
	return false
}

// AccessibleSVGLabel returns the concise label for an SVG that may be handled
// as an opaque image. Hidden reports all SVG as hidden so generic DOM walkers
// never descend into SVG text, links, or metadata; callers that explicitly
// support this representation may use this function before their hidden check.
func AccessibleSVGLabel(n *html.Node) string {
	if n == nil || n.Type != html.ElementNode || !strings.EqualFold(n.Data, "svg") ||
		!strings.EqualFold(strings.TrimSpace(attr(n, "role")), "img") {
		return ""
	}
	label := strings.TrimSpace(attr(n, "aria-label"))
	if label == "" || hiddenByAttributes(n) {
		return ""
	}
	return label
}

// hiddenByAttributes is shared by ordinary visibility checks and opaque SVG
// handling so the latter cannot bypass part of the visibility policy.
func hiddenByAttributes(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(attr(n, "aria-hidden")), "true") ||
		hiddenByNonARIAAttributes(n)
}

func hiddenByNonARIAAttributes(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if hasAttr(n, "hidden") || hasAttr(n, "inert") ||
		strings.EqualFold(strings.TrimSpace(attr(n, "aria-modal")), "true") {
		return true
	}
	style := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, attr(n, "style"))
	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")
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
