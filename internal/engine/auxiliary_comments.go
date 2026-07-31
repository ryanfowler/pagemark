package engine

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// isArticleCommentRegion identifies the region containing reader responses,
// rather than trying to filter every reply, like, and form control separately.
// These signals are deliberately article-only: the same records are primary
// content when the selected profile is a discussion.
func (a *analysis) isArticleCommentRegion(n *html.Node) (result bool) {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if value, known := a.nodeStates[n].articleComment.value(); known {
		return value
	}
	defer func() {
		state := a.nodeStates[n]
		state.articleComment.store(result)
		a.nodeStates[n] = state
	}()

	tokens := elementTokens(n)
	// Plural comment markers and established comment-list conventions are
	// sufficiently specific on article pages. “Responses” and “replies” are
	// ambiguous (for example, survey responses), so they require the heading or
	// repeated-record evidence checked below.
	if containsAny(tokens, "comments", "commentlist") || hasCompactClass(n, "commentbox") ||
		(containsAny(tokens, "comment") && containsAny(tokens, "list")) ||
		containsAny(tokens, "discussion") && hasArticleDiscussionHeading(n) {
		return true
	}

	// A schema.org Comment is unambiguous even when the publisher uses neutral
	// classes. Excluding the record also removes controls nested in that record.
	if containsAny(strings.ToLower(attrValue(n, "itemtype")), "comment") {
		return true
	}
	if isPlausibleCommentRecord(n) && !hasNonCardArticleAncestor(n) &&
		a.belongsToRepeatedCommentRecords(n) {
		return true
	}

	tag := strings.ToLower(n.Data)
	switch tag {
	case "div", "section", "aside", "ol", "ul":
		if isCommentRegionHeading(firstRegionHeading(n)) {
			return true
		}
		// Some systems omit a comments heading and expose only repeated records.
		// Do not apply this to a layout that also contains the article body;
		// otherwise a page-wide wrapper could hide the article along with replies.
		// WordPress commonly uses a .type-post wrapper and .entry-content instead
		// of the semantic article element.
		if !a.hasArticleBodyDescendant(n) && a.commentRecordCount(n) >= 2 {
			return true
		}
	}
	return false
}

func isEmptyRecordList(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !hasExactClass(n, "empty-list") || hasSubstantiveCommentProse(n) {
		return false
	}
	status := false
	walk(n, func(x *html.Node) bool {
		if status || hardHidden(x) {
			return false
		}
		if x.Type == html.ElementNode && (strings.EqualFold(x.Data, "p") || isHeadingTag(strings.ToLower(x.Data))) &&
			isCommentStatusPrompt(nodeText(x)) {
			status = true
			return false
		}
		return true
	})
	return status
}

func hasArticleDiscussionHeading(n *html.Node) bool {
	found := false
	budget := 64
	walk(n, func(x *html.Node) bool {
		if found || budget <= 0 || hardHidden(x) {
			return false
		}
		if x.Type == html.ElementNode {
			budget--
			if isHeadingTag(strings.ToLower(x.Data)) {
				label := normalizedLabel(nodeText(x))
				found = label == "discussion about this post" || label == "discussion about this article" ||
					label == "discussion about this story"
				return false
			}
		}
		return true
	})
	return found
}

func isCommentRegionHeading(label string) bool {
	if label == "comments" || label == "responses" || label == "replies" ||
		label == "leave a comment" || label == "leave a reply" {
		return true
	}
	// Labels are normalized before this check, so the first two fields can be
	// inspected without allocating the []string produced by strings.Fields.
	space := strings.IndexByte(label, ' ')
	if space <= 0 || !allASCIIDigits(label[:space]) {
		return false
	}
	rest := label[space+1:]
	if next := strings.IndexByte(rest, ' '); next >= 0 {
		rest = rest[:next]
	}
	return rest == "comments" || rest == "responses" || rest == "replies"
}

// isEmptyCommentControlRegion recognizes comment UI with no visible,
// substantive messages. It intentionally requires a plural comments marker so
// an ordinary article element using a singular .comment annotation is not
// discarded. Hidden records do not count because they cannot be extracted.
func (a *analysis) isEmptyCommentControlRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || hardHidden(n) {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "div", "section", "aside", "header", "fieldset":
	default:
		return false
	}
	if !elementContainsAny(n, "comments", "commentlist") {
		return false
	}
	// A visible prose element can be substantive even when the comments
	// container itself is the record and has no marked descendants. Known empty
	// and authentication prompts are still furniture despite using <p>.
	if hasSubstantiveCommentProse(n) {
		return false
	}
	// Forum software may place message text directly in a div or section and
	// separate lines with <br>. Apply the same non-control text fallback used
	// for marked discussion records, while continuing to reject known prompts.
	text := commentRecordText(n)
	if utf8.RuneCountInString(text) >= 20 && !isCommentStatusPrompt(text) {
		return false
	}
	found := false
	walk(n, func(x *html.Node) bool {
		if found || hardHidden(x) {
			return false
		}
		if x != n && isPlausibleDiscussionRecord(x) {
			found = true
			return false
		}
		return true
	})
	return !found
}

func (a *analysis) belongsToRepeatedCommentRecords(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if a.commentRecordCount(p) >= 2 {
			return true
		}
		if p.Type == html.ElementNode && (strings.EqualFold(p.Data, "main") || strings.EqualFold(p.Data, "body")) {
			break
		}
	}
	return false
}

// commentRecordCount returns a count capped at two, which is all region
// classification needs. Caching each subtree keeps ancestor checks linear in
// the size of the DOM rather than rescanning descendants for every block.
func (a *analysis) commentRecordCount(root *html.Node) int {
	if root == nil || hardHidden(root) {
		return 0
	}
	if value, known := a.nodeStates[root].commentCount.value(); known {
		return value
	}
	count := 0
	for ch := root.FirstChild; ch != nil && count < 2; ch = ch.NextSibling {
		if hardHidden(ch) || ch.Type != html.ElementNode {
			continue
		}
		if isPlausibleCommentRecord(ch) {
			count++
			continue // Nested reply/body wrappers belong to the same record.
		}
		count += a.commentRecordCount(ch)
		if count > 2 {
			count = 2
		}
	}
	state := a.nodeStates[root]
	state.commentCount.store(count)
	a.nodeStates[root] = state
	return count
}

func isPlausibleCommentRecord(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	// Record markers belong on content containers. In particular, links and
	// buttons commonly use .reply but are controls, not repeated replies.
	switch strings.ToLower(n.Data) {
	case "article", "li", "div", "section":
	default:
		return false
	}
	if containsAny(strings.ToLower(attrValue(n, "itemtype")), "comment") {
		return true
	}
	if !elementContainsAny(n, "comment", "reply") {
		return false
	}
	// A paragraph or quotation supplies record shape even for a very short
	// response such as “Thanks!”. The rune threshold remains a fallback for
	// div-based comments that use text and <br> instead of prose elements.
	return hasCommentRecordProse(n) || commentRecordTextLength(n) >= 20
}

func hasCommentRecordProse(n *html.Node) bool {
	found := false
	walk(n, func(x *html.Node) bool {
		if found || hardHidden(x) {
			return false
		}
		if x != n && x.Type == html.ElementNode {
			switch strings.ToLower(x.Data) {
			case "a", "button", "form", "input", "select", "textarea":
				return false
			case "p", "blockquote":
				if commentRecordTextLength(x) > 0 {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

func hasSubstantiveCommentProse(n *html.Node) bool {
	found := false
	walk(n, func(x *html.Node) bool {
		if found || hardHidden(x) {
			return false
		}
		if x != n && x.Type == html.ElementNode {
			switch strings.ToLower(x.Data) {
			case "a", "button", "form", "input", "select", "textarea":
				return false
			case "p", "blockquote":
				label := normalizedLabel(nodeText(x))
				if label != "" && !isCommentStatusPrompt(label) {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

func isCommentStatusPrompt(label string) bool {
	label = normalizedLabel(label)
	short := utf8.RuneCountInString(label) <= 80
	if label == "no comments" || label == "no posts" || label == "no replies" || short &&
		(strings.HasPrefix(label, "no comments yet") ||
			strings.HasPrefix(label, "there are no comments") ||
			strings.HasPrefix(label, "comments are closed") ||
			strings.HasPrefix(label, "comments are disabled") ||
			strings.HasPrefix(label, "be the first to comment") ||
			strings.HasPrefix(label, "be the first to reply")) {
		return true
	}
	// Status and promotional phrases are ambiguous at the start of a real
	// response. Treat them as UI only while they remain short enough to be a
	// heading or prompt.
	if short && (strings.HasPrefix(label, "join the conversation") ||
		strings.HasPrefix(label, "join the discussion") ||
		strings.HasPrefix(label, "share your thoughts") ||
		strings.HasPrefix(label, "share your feedback") ||
		strings.HasPrefix(label, "leave a comment") ||
		strings.HasPrefix(label, "start the conversation")) {
		return true
	}
	authentication := strings.HasPrefix(label, "sign in") || strings.HasPrefix(label, "sign-in") ||
		strings.HasPrefix(label, "log in") || strings.HasPrefix(label, "login") ||
		strings.HasPrefix(label, "please sign in") || strings.HasPrefix(label, "please log in") ||
		strings.HasPrefix(label, "you must sign in") || strings.HasPrefix(label, "you must log in")
	return utf8.RuneCountInString(label) <= 100 && authentication &&
		containsAny(label, "comment", "discussion", "reply", "respond", "join")
}

func commentRecordTextLength(n *html.Node) int {
	return utf8.RuneCountInString(commentRecordText(n))
}

func commentRecordText(n *html.Node) string {
	var text strings.Builder
	wrote := false
	walk(n, func(x *html.Node) bool {
		if hardHidden(x) {
			return false
		}
		if x != n && x.Type == html.ElementNode {
			switch strings.ToLower(x.Data) {
			case "a", "button", "form", "input", "select", "textarea":
				return false
			}
		}
		if x.Type == html.TextNode {
			if wrote {
				text.WriteByte(' ')
			}
			text.WriteString(x.Data)
			wrote = true
		}
		return true
	})
	return normalizeText(text.String())
}
