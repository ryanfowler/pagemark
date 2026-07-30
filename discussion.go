package pagemark

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/dom"
	"golang.org/x/net/html"
)

func (a *analysis) primaryDiscussionContext() bool {
	var primary *html.Node
	walk(a.root, func(n *html.Node) bool {
		if hardHidden(n) {
			return false
		}
		if primary != nil {
			return false
		}
		if n.Type != html.ElementNode {
			return true
		}
		if strings.EqualFold(n.Data, "main") || strings.EqualFold(attrValue(n, "role"), "main") {
			primary = n
			return false
		}
		return true
	})
	if primary == nil {
		walk(a.root, func(n *html.Node) bool {
			if primary == nil && n.Type == html.ElementNode && strings.EqualFold(n.Data, "body") {
				primary = n
			}
			return primary == nil
		})
	}
	if primary == nil {
		return false
	}
	if elementContainsAny(primary, "discussion", "thread", "topic", "forum", "qna", "question") {
		return true
	}
	label := ""
	walk(primary, func(n *html.Node) bool {
		if label != "" || hardHidden(n) {
			return false
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "h1") && !a.inferenceAuxiliaryBlock(n) {
			label = normalizedLabel(nodeText(n))
			return false
		}
		return true
	})
	switch label {
	case "discussion", "community discussion", "forum", "question", "questions and answers", "q&a":
		return true
	}
	return false
}

func nearestDiscussionRecordAncestor(n *html.Node) *html.Node {
	for p := n; p != nil; p = p.Parent {
		if isPlausibleDiscussionRecord(p) {
			return p
		}
	}
	return nil
}

func isPlausibleDiscussionRecord(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || hardHidden(n) {
		return false
	}
	tag := strings.ToLower(n.Data)
	switch tag {
	case "article", "li", "div", "section":
	default:
		return false
	}
	tokens := elementTokens(n)
	itemtype := strings.ToLower(attrValue(n, "itemtype"))
	// A generic .question class is common in FAQs, surveys, and forms. It only
	// identifies a discussion record when backed by Question microdata; QAPage
	// schema and explicit primary thread structure are scored separately.
	marked := containsAny(tokens, "comment", "answer", "post", "reply", "message") ||
		containsAny(itemtype, "comment", "answer", "question")
	if !marked {
		return false
	}
	// An explicitly marked record is stronger evidence than prompt-like wording.
	// Do not discard a real short response merely because its sentence starts
	// with “share your feedback” or similar UI text. All supported record tags
	// are eligible, but form/status wrappers and input widgets are not. Ordinary
	// per-comment action buttons do not negate otherwise substantive prose.
	explicitRecord := (containsAny(tokens, "comment", "answer", "reply", "message") ||
		containsAny(itemtype, "comment", "answer", "question")) &&
		!containsAny(tokens, "form", "status", "prompt", "cta", "control") &&
		!hasDiscussionRecordControls(n)
	if explicitRecord && (hasCommentRecordProse(n) || commentRecordTextLength(n) >= 20) {
		return true
	}
	// Controls, author links, and headings do not make a message. A prose
	// element supports legitimately short replies; otherwise require enough
	// non-control text for div-and-br based forum software.
	return hasSubstantiveCommentProse(n) ||
		(commentRecordTextLength(n) >= 20 && !isCommentStatusPrompt(commentRecordText(n)))
}

// nearestNeutralDiscussionRecord recognizes semantic records whose discussion
// meaning comes from an explicit ancestor rather than their own attributes.
func hasDiscussionRecordControls(n *html.Node) bool {
	found := false
	walk(n, func(x *html.Node) bool {
		if found || hardHidden(x) {
			return false
		}
		if x != n && x.Type == html.ElementNode {
			switch strings.ToLower(x.Data) {
			case "form", "input", "select", "textarea":
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func nearestNeutralDiscussionRecord(n *html.Node) *html.Node {
	var record *html.Node
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		tag := strings.ToLower(p.Data)
		if record == nil && (tag == "article" || tag == "li") &&
			(hasSubstantiveCommentProse(p) ||
				(commentRecordTextLength(p) >= 20 && !isCommentStatusPrompt(commentRecordText(p)))) {
			record = p
		}
		if elementContainsAny(p, "discussion", "thread", "topic") {
			return record
		}
	}
	return nil
}
func elementTokens(n *html.Node) string {
	if n == nil || n.Type != html.ElementNode {
		return ""
	}
	// HTML parsing already normalizes attribute keys, but retain EqualFold for
	// caller-built trees. Collect in one pass instead of doing three scans and
	// allocating both a concatenation and its lower-case copy.
	var id, class, role string
	for _, attr := range n.Attr {
		switch {
		case id == "" && strings.EqualFold(attr.Key, "id"):
			id = attr.Val
		case class == "" && strings.EqualFold(attr.Key, "class"):
			class = attr.Val
		case role == "" && strings.EqualFold(attr.Key, "role"):
			role = attr.Val
		}
	}
	length := len(id) + len(class) + len(role)
	if length == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(length + 2)
	for i, value := range [...]string{id, class, role} {
		if i != 0 {
			b.WriteByte(' ')
		}
		for j := 0; j < len(value); {
			if value[j] < utf8.RuneSelf {
				c := value[j]
				if c >= 'A' && c <= 'Z' {
					c += 'a' - 'A'
				}
				b.WriteByte(c)
				j++
				continue
			}
			r, size := utf8.DecodeRuneInString(value[j:])
			b.WriteRune(unicode.ToLower(r))
			j += size
		}
	}
	return b.String()
}

// elementContainsAny tests the token-bearing attributes without concatenating
// and lowercasing them. Most classification checks only need a boolean answer.
func elementContainsAny(n *html.Node, values ...string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, attr := range n.Attr {
		// Parsed HTML has canonical lowercase attribute names. Keep EqualFold as
		// the uncommon fallback for caller-built trees passed to ExtractNode.
		key := attr.Key
		tokenAttribute := key == "id" || key == "class" || key == "role"
		if !tokenAttribute {
			switch len(key) {
			case len("id"):
				tokenAttribute = strings.EqualFold(key, "id")
			case len("role"):
				tokenAttribute = strings.EqualFold(key, "role")
			case len("class"):
				tokenAttribute = strings.EqualFold(key, "class")
			}
		}
		if tokenAttribute && containsAnyFold(attr.Val, values...) {
			return true
		}
	}
	return false
}

func containsAnyFold(s string, values ...string) bool {
	// Class, id, and role values are overwhelmingly ASCII. Scan that common
	// case once; only restart with Unicode tokenization when a non-ASCII byte is
	// actually encountered.
	start := -1
	for end := 0; end < len(s); end++ {
		c := s[end]
		if c >= utf8.RuneSelf {
			return containsAnyFoldUnicode(s, values)
		}
		if asciiAlnum[c] {
			if start < 0 {
				start = end
			}
			continue
		}
		if start >= 0 {
			token := s[start:end]
			for _, value := range values {
				// Length and first-byte checks reject nearly all vocabulary entries
				// before the full case-insensitive comparison.
				if len(token) == len(value) && lowerASCII(token[0]) == lowerASCII(value[0]) && equalFoldASCII(token, value) {
					return true
				}
			}
			start = -1
		}
	}
	if start >= 0 {
		token := s[start:]
		for _, value := range values {
			if len(token) == len(value) && lowerASCII(token[0]) == lowerASCII(value[0]) && equalFoldASCII(token, value) {
				return true
			}
		}
	}
	return false
}

func containsAnyFoldUnicode(s string, values []string) bool {
	start := -1
	for end, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = end
			}
			continue
		}
		if start >= 0 {
			for _, value := range values {
				if strings.EqualFold(s[start:end], value) {
					return true
				}
			}
			start = -1
		}
	}
	if start >= 0 {
		for _, value := range values {
			if strings.EqualFold(s[start:], value) {
				return true
			}
		}
	}
	return false
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

func hasDataMarker(n *html.Node, marker string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	want := "data-" + marker
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, want) {
			return true
		}
	}
	return false
}

func containsToken(s string, tokens []string) bool {
	for _, t := range tokens {
		if containsAny(s, t) {
			return true
		}
	}
	return false
}
func containsAny(s string, values ...string) bool {
	// DOM tokens are almost always ASCII. Avoid rune decoding and Unicode table
	// lookups on that hot path, falling back only when a non-ASCII byte occurs.
	start := -1
	for end := 0; end < len(s); end++ {
		c := s[end]
		if c >= utf8.RuneSelf {
			return containsAnyUnicode(s, values)
		}
		if asciiAlnum[c] {
			if start < 0 {
				start = end
			}
			continue
		}
		if start >= 0 {
			for _, value := range values {
				if s[start:end] == value {
					return true
				}
			}
			start = -1
		}
	}
	if start >= 0 {
		token := s[start:]
		for _, value := range values {
			if token == value {
				return true
			}
		}
	}
	return false
}

var asciiAlnum = func() [utf8.RuneSelf]bool {
	var table [utf8.RuneSelf]bool
	for c := byte('0'); c <= '9'; c++ {
		table[c] = true
	}
	for c := byte('A'); c <= 'Z'; c++ {
		table[c] = true
	}
	for c := byte('a'); c <= 'z'; c++ {
		table[c] = true
	}
	return table
}()

func containsAnyUnicode(s string, values []string) bool {
	start := -1
	for end, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = end
			}
			continue
		}
		if start >= 0 {
			for _, value := range values {
				if s[start:end] == value {
					return true
				}
			}
			start = -1
		}
	}
	if start >= 0 {
		for _, value := range values {
			if s[start:] == value {
				return true
			}
		}
	}
	return false
}

var discussionAuxiliaryLabels = map[string]bool{
	"start asking to get answers": true, "find the answer to your question by asking": true,
	"sign up to request clarification or add additional context in comments": true,
	"add a comment": true, "explore related questions": true, "see similar questions with these tags": true,
}

// isDiscussionAuxiliaryLabelNode keeps ambiguous UI phrases out of the global
// boilerplate vocabulary. The same text can be a legitimate documentation
// heading, while on a discussion page these exact short control labels are
// auxiliary when rendered as headings, buttons, or standalone prompt text.
func isDiscussionAuxiliaryLabelNode(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(n.Data)
	if tag != "p" && tag != "button" && !isHeadingTag(tag) {
		return false
	}
	return discussionAuxiliaryLabels[normalizedLabel(nodeText(n))]
}

func isDiscussionControlNode(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	tokens := elementTokens(n)
	return containsAny(tokens, "rating", "forumjump") || hasExactClass(n, "comments-link") ||
		(containsAny(tokens, "thread") && containsAny(tokens, "tools")) ||
		(containsAny(tokens, "post") && containsAny(tokens, "menu"))
}

func isDiscussionControlBlock(n *html.Node) bool {
	found := false
	walk(n, func(x *html.Node) bool {
		if hardHidden(x) {
			return false
		}
		if isDiscussionControlNode(x) {
			found = true
			return false
		}
		return !found
	})
	return found
}

func (a *analysis) hasStandaloneMessageAncestor(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode || !isGenericContainer(strings.ToLower(p.Data)) {
			continue
		}
		tokens := elementTokens(p)
		if containsAny(tokens, "message") &&
			!containsAny(tokens, "body", "content", "text", "post", "comment", "answer", "reply") &&
			!a.hasDiscussionBodyDescendant(p) {
			return true
		}
	}
	return false
}

func controls(n *html.Node) int {
	v := 0
	walk(n, func(x *html.Node) bool {
		if dom.Hidden(x) {
			return false
		}
		if x.Type == html.ElementNode {
			switch strings.ToLower(x.Data) {
			case "button", "input", "select", "textarea":
				v++
			}
		}
		return true
	})
	return v
}
func linkTextLength(n *html.Node) int {
	v := 0
	walk(n, func(x *html.Node) bool {
		if dom.Hidden(x) {
			return false
		}
		if x != n && x.Type == html.ElementNode && strings.EqualFold(x.Data, "a") {
			v += utf8.RuneCountInString(normalizeText(nodeText(x)))
			return false
		}
		return true
	})
	return v
}
