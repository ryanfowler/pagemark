package engine

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/dom"
	"golang.org/x/net/html"
)

// isSubscriptionRegion identifies the wrapper around a newsletter form, not
// merely the controls that Markdown conversion already omits. It requires a
// promotional heading, or form controls corroborated by consent/honeypot copy:
// class names such as newsletter-example occur in substantive tutorials.
func (a *analysis) isSubscriptionRegion(n *html.Node) bool {
	if !subscriptionContainer(n) {
		return false
	}
	// Nodes in the caller's tree were indexed once in a post-order pass. Reuse
	// those aggregate bits instead of walking the same subtree for every wrapper.
	inRoot := false
	for p := n; p != nil; p = p.Parent {
		if p == a.root {
			inRoot = true
			break
		}
	}
	if !inRoot {
		// Heading normalization may supply a cloned tree.
		return isSubscriptionRegion(n)
	}
	if a.evidence == nil {
		return isSubscriptionRegion(n)
	}
	controlCount, _ := a.evidence.controlCount(n)
	return evaluateSubscriptionRegion(n,
		a.evidence.has(n, evidenceForm),
		a.evidence.has(n, evidenceEmailInput),
		a.evidence.has(n, evidenceSubscriptionForm), controlCount)
}

func isSubscriptionRegion(n *html.Node) bool {
	if !subscriptionContainer(n) {
		return false
	}
	hasForm, hasEmail, subscriptionForm := false, false, false
	walk(n, func(x *html.Node) bool {
		if hardHidden(x) {
			return false
		}
		if x.Type != html.ElementNode {
			return true
		}
		switch strings.ToLower(x.Data) {
		case "input":
			hasEmail = hasEmail || strings.EqualFold(strings.TrimSpace(attrValue(x, "type")), "email")
		case "form":
			hasForm = true
			subscriptionForm = subscriptionForm || subscriptionAttributeMarker(x) ||
				containsSubscriptionWord(attrValue(x, "action"))
		}
		return true
	})
	return evaluateSubscriptionRegion(n, hasForm, hasEmail, subscriptionForm, controls(n))
}

func subscriptionContainer(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "div", "section", "aside", "fieldset":
		return true
	default:
		return false
	}
}

func evaluateSubscriptionRegion(n *html.Node, hasForm, hasEmail, subscriptionForm bool, controlCount int) bool {
	attributeMarker := subscriptionAttributeMarker(n)
	// Text collection and heading discovery are comparatively expensive on large
	// wrappers. Neither matters without a form or an explicit marker.
	if !hasForm && !attributeMarker {
		return false
	}
	heading := firstRegionHeading(n)
	cta, consent, honeypot := subscriptionTextEvidence(n)

	if !hasForm && attributeMarker && cta && !substantialArticleScope(n) &&
		(isSubscriptionPromptHeading(heading) || hasSubscriptionDestination(n)) {
		return true
	}
	formEvidence := hasEmail || subscriptionForm || (hasForm && cta)
	if isSubscriptionPromptHeading(heading) {
		return formEvidence && cta
	}

	if !hasForm || (!hasEmail && controlCount < 2) {
		return false
	}
	// CTA labels are needed only when the surrounding text and form action did
	// not already provide equivalent evidence.
	if !cta && !subscriptionForm && !hasJoinCTA(n) {
		return false
	}
	return consent || honeypot
}

// subscriptionTextEvidence recognizes the few phrases used by subscription
// classification without constructing and lowercasing the complete subtree
// text. Page-wide wrappers often contain a form, causing this check to run at
// several ancestor levels; materializing each ancestor's text makes that path
// quadratic in allocated bytes on large pages.
func subscriptionTextEvidence(n *html.Node) (cta, consent, honeypot bool) {
	s := subscriptionTextScanner{}
	s.scan(n)
	s.endWord()
	return s.cta, s.consent, s.honeypot
}

type subscriptionTextScanner struct {
	contains                   [32]byte
	containsLen, containsStart int
	words                      [32]byte
	wordsLen, wordsStart       int
	inWord, pendingSpace       bool
	cta, consent, honeypot     bool
}

func (s *subscriptionTextScanner) scan(n *html.Node) {
	if n == nil || dom.Hidden(n) || s.cta && s.consent && s.honeypot {
		return
	}
	if n.Type == html.TextNode {
		// nodeText inserts a separator between visible text nodes. Delaying it
		// lets normalization collapse that separator with surrounding whitespace.
		if s.containsLen > 0 {
			s.pendingSpace = true
			s.endWord()
		}
		for text := n.Data; text != ""; {
			r := rune(text[0])
			size := 1
			if r >= utf8.RuneSelf {
				r, size = utf8.DecodeRuneInString(text)
			}
			text = text[size:]
			if r == ' ' || r == '\t' || r == '\n' || r == '\f' || r == '\r' ||
				r >= utf8.RuneSelf && unicode.IsSpace(r) {
				s.pendingSpace = s.containsLen > 0
				s.endWord()
				continue
			}
			if s.pendingSpace {
				s.pushContains(' ')
				s.pendingSpace = false
			}
			lower := unicode.ToLower(r)
			if lower <= unicode.MaxASCII {
				s.pushContains(byte(lower))
			} else {
				// None of the phrases is non-ASCII. A sentinel preserves the
				// substring boundary without retaining the original rune.
				s.pushContains(0)
			}
			word := unicode.IsLetter(lower) || unicode.IsDigit(lower)
			if word {
				if !s.inWord && s.wordsLen > 0 {
					s.pushWordByte(' ')
				}
				s.inWord = true
				if folded, ok := foldedASCII(lower); ok {
					s.pushWordByte(folded)
				} else {
					s.pushWordByte(0)
				}
			} else {
				s.endWord()
			}
		}
		return
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		s.scan(ch)
	}
}

// foldedASCII returns the ASCII member of a rune's Unicode simple-fold orbit.
// containsWordSequence previously used strings.EqualFold, so characters such
// as long-s must remain equivalent to their ASCII phrase character.
func foldedASCII(r rune) (byte, bool) {
	if r <= unicode.MaxASCII {
		return byte(r), true
	}
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if folded <= unicode.MaxASCII {
			return lowerASCII(byte(folded)), true
		}
	}
	return 0, false
}

func (s *subscriptionTextScanner) pushContains(c byte) {
	pushTextWindow(&s.contains, &s.containsLen, &s.containsStart, c)
	if s.cta {
		return
	}
	switch c {
	case 'e':
		s.cta = windowHasSuffix(&s.contains, s.containsLen, s.containsStart, "subscribe", false)
	case 'p':
		s.cta = windowHasSuffix(&s.contains, s.containsLen, s.containsStart, "sign up", false)
	case 't':
		s.cta = windowHasSuffix(&s.contains, s.containsLen, s.containsStart, "mailing list", false)
	case 's':
		s.cta = windowHasSuffix(&s.contains, s.containsLen, s.containsStart, "get updates", false)
	}
}

func (s *subscriptionTextScanner) pushWordByte(c byte) {
	pushTextWindow(&s.words, &s.wordsLen, &s.wordsStart, c)
}

func (s *subscriptionTextScanner) endWord() {
	if !s.inWord {
		return
	}
	s.inWord = false
	last := textWindowByte(&s.words, s.wordsLen, s.wordsStart, s.wordsLen-1)
	switch last {
	case 'y':
		s.consent = s.consent || windowHasSuffix(&s.words, s.wordsLen, s.wordsStart, "privacy policy", true)
	case 'e':
		s.consent = s.consent || windowHasSuffix(&s.words, s.wordsLen, s.wordsStart, "terms of use", true)
	case 's':
		s.consent = s.consent || windowHasSuffix(&s.words, s.wordsLen, s.wordsStart, "terms and conditions", true)
	case 'n':
		s.honeypot = s.honeypot || windowHasSuffix(&s.words, s.wordsLen, s.wordsStart, "field is for validation", true)
	case 'd':
		s.honeypot = s.honeypot || windowHasSuffix(&s.words, s.wordsLen, s.wordsStart, "leave this field unchanged", true)
	case 'l':
		s.honeypot = s.honeypot || windowHasSuffix(&s.words, s.wordsLen, s.wordsStart, "do not fill", true)
	}
}

func isFormElement(n *html.Node) bool {
	return n != nil && n.Type == html.ElementNode && strings.EqualFold(n.Data, "form")
}

func hasSubscriptionDestination(n *html.Node) bool {
	found := false
	walk(n, func(x *html.Node) bool {
		if found || hardHidden(x) {
			return false
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "a") &&
			containsSubscriptionWord(attrValue(x, "href")) {
			found = true
		}
		return !found
	})
	return found
}

func hasJoinCTA(n *html.Node) bool {
	found := false
	walk(n, func(x *html.Node) bool {
		if found || hardHidden(x) {
			return false
		}
		if x.Type != html.ElementNode {
			return true
		}
		switch strings.ToLower(x.Data) {
		case "input":
			t := strings.ToLower(strings.TrimSpace(attrValue(x, "type")))
			found = (t == "submit" || t == "button") && isJoinCTA(attrValue(x, "value"))
		case "button", "a":
			found = isJoinCTA(nodeText(x))
		}
		return !found
	})
	return found
}

func isJoinCTA(value string) bool {
	label := normalizedLabel(value)
	return label == "join" || strings.HasPrefix(label, "join now") ||
		strings.HasPrefix(label, "join the ") || strings.HasPrefix(label, "join our ")
}

func isSubscriptionPromptHeading(heading string) bool {
	if heading == "stay updated" || strings.HasPrefix(heading, "stay updated ") ||
		strings.HasPrefix(heading, "stay up to date") || strings.HasPrefix(heading, "be the first to") ||
		heading == "get updates" || strings.HasPrefix(heading, "get updates ") ||
		strings.HasPrefix(heading, "get the latest") || heading == "subscribe" ||
		strings.HasPrefix(heading, "subscribe to ") {
		return true
	}
	return heading == "join our newsletter" || heading == "join the newsletter" ||
		heading == "newsletter signup" || heading == "newsletter sign-up" ||
		heading == "sign up" || strings.HasPrefix(heading, "sign up for updates")
}

func subscriptionAttributeMarker(n *html.Node) bool {
	return containsSubscriptionWord(attrValue(n, "id")) || containsSubscriptionWord(attrValue(n, "class"))
}

func containsSubscriptionWord(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "subscribe") || strings.Contains(value, "subscription") ||
		strings.Contains(value, "newsletter") || strings.Contains(value, "signup") ||
		strings.Contains(value, "sign-up")
}
