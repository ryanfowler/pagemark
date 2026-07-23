// Package dom contains shared HTML tree rules.
package dom

import (
	"strings"
	"unicode"
	"unicode/utf8"

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
	return hiddenByAttributesMode(n, true)
}

func hiddenByNonARIAAttributes(n *html.Node) bool {
	return hiddenByAttributesMode(n, false)
}

// hiddenByAttributesMode scans attributes once. Visibility checks are among
// the hottest DOM operations, and repeatedly looking up each relevant
// attribute made nodes with large attribute lists disproportionately costly.
func hiddenByAttributesMode(n *html.Node, includeARIAHidden bool) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	open := false
	style := ""
	for _, a := range n.Attr {
		switch {
		case strings.EqualFold(a.Key, "hidden"), strings.EqualFold(a.Key, "inert"):
			return true
		case strings.EqualFold(a.Key, "open"):
			open = true
		case strings.EqualFold(a.Key, "aria-hidden"):
			if includeARIAHidden && strings.EqualFold(strings.TrimSpace(a.Val), "true") {
				return true
			}
		case strings.EqualFold(a.Key, "aria-modal"):
			if strings.EqualFold(strings.TrimSpace(a.Val), "true") {
				return true
			}
		case strings.EqualFold(a.Key, "style"):
			style = a.Val
		}
	}
	// A dialog is not rendered until its boolean open attribute is present.
	if strings.EqualFold(n.Data, "dialog") && !open {
		return true
	}
	if style == "" {
		return false
	}
	return hiddenStyle(style)
}

// hiddenStyle recognizes the two relevant declarations in one pass, without
// allocating a normalized copy of every style attribute. The common ASCII
// path avoids Unicode tables and decoding.
func hiddenStyle(s string) bool {
	const display = "display:none"
	const visibility = "visibility:hidden"
	di, vi := 0, 0
	for i := 0; i < len(s); {
		var r rune
		if s[i] < utf8.RuneSelf {
			r = rune(s[i])
			i++
			if r == ' ' || r >= '\t' && r <= '\r' {
				continue
			}
			if r >= 'A' && r <= 'Z' {
				r += 'a' - 'A'
			}
		} else {
			var size int
			r, size = utf8.DecodeRuneInString(s[i:])
			i += size
			if unicode.IsSpace(r) {
				continue
			}
			r = unicode.ToLower(r)
		}
		di = advanceMatch(display, di, r)
		vi = advanceMatch(visibility, vi, r)
		if di == len(display) || vi == len(visibility) {
			return true
		}
	}
	return false
}

func advanceMatch(want string, matched int, c rune) int {
	if c == rune(want[matched]) {
		return matched + 1
	}
	if c == rune(want[0]) {
		return 1
	}
	return 0
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}
