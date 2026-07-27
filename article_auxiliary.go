package pagemark

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

var badTokens = []string{"cookie", "cookies", "consent", "banner", "share", "newsletter", "signup", "sign-up", "promo", "copyright"}

// hasBoilerplateToken retains the cross-page social-furniture signal without
// treating every compound use of “social” as page chrome. In particular,
// subject classes such as social-impact and social-science remain content,
// while exact and conventional widget classes keep the historical penalty.
func hasBoilerplateToken(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if elementContainsAny(n, badTokens...) {
		return true
	}
	for _, attr := range []string{"id", "class", "role"} {
		value := attrValue(n, attr)
		for start := 0; start < len(value); {
			for start < len(value) && htmlSpace(value[start]) {
				start++
			}
			end := start
			for end < len(value) && !htmlSpace(value[end]) {
				end++
			}
			if end == start {
				break
			}
			token := value[start:end]
			if strings.EqualFold(token, "social") {
				return true
			}
			if containsAnyFold(token, "social") {
				// Compound social tokens are uncommon. Only allocate a lowercase
				// copy after the allocation-free filter has matched.
				token = strings.ToLower(token)
				if containsAny(token, "follow", "link", "links", "media", "widget", "icon", "icons",
					"share", "sharing", "profile", "network", "networks", "nav") {
					return true
				}
			}
			start = end
		}
	}
	return false
}

// These labels introduce navigational or promotional regions regardless of
// page type. Matching is deliberately exact so subject sections that happen to
// use similar words are retained.
var auxiliaryLabels = map[string]bool{
	"on this page": true, "in this article": true, "table of contents": true,
	"help us improve gov.uk": true,
	"more news":              true, "latest news": true, "related news": true,
	"related articles": true, "related content": true, "related keywords": true,
	"related projects": true, "related topics": true, "related tags": true, "more publications": true,
	"follow on social media": true,
	"recommended for you":    true,
	"you may also like":      true, "you may also enjoy": true, "read next": true, "more stories": true,
	"latest stories": true, "see also": true,
}

// These short labels are strong boilerplate signals on articles, but can name
// legitimate sections on other page types (for example Web Share API docs).
var articleAuxiliaryLabels = map[string]bool{
	"related posts": true, "read more": true, "keep reading": true, "share": true,
	"share this": true, "share this article": true, "share this post": true,
	"share this story": true, "like this": true, "more by": true,
	"leave a comment": true, "leave a comment below": true,
}

func isArticleAuxiliaryLabel(label string) bool {
	if articleAuxiliaryLabels[label] {
		return true
	}
	// Author recommendation headings include a name and therefore cannot be
	// enumerated (for example, “More by Ben Thompson”). Keep the match anchored
	// to the complete leading phrase so ordinary uses of "more" are unaffected.
	return strings.HasPrefix(label, "more by ")
}

var callToActionLabels = map[string]bool{
	"read more": true, "learn more": true, "continue reading": true,
	"view more": true, "see more": true,
}

// Other structural names need navigational evidence because they are also
// common documentation subjects.
var navigationStructureTokens = []string{"breadcrumb", "pagination", "toolbar"}

func irrelevantNode(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(n.Data)
	// Document shells may contain a mixture of primary and auxiliary regions.
	// Never classify the shell itself from descendant link density or trailing
	// article heuristics; its individual children are still classified normally.
	if tag == "html" || tag == "body" {
		return false
	}
	if tag == "nav" || tag == "footer" || hasDataMarker(n, "site-footer") || hasExactClass(n, "article-footer") ||
		isEmptyRecordList(n) ||
		isPageFooterConvention(n) || hasClassConvention(n, "step-nav") || hasExactClass(n, "crawler-linkback-list") ||
		hasExactClass(n, "post-likes") || hasClassPrefix(n, "jetpack-likes-widget") ||
		hasExactClass(n, "mw-editsection") || hasExactClass(n, "printfooter") || hasExactClass(n, "catlinks") ||
		strings.EqualFold(attrValue(n, "id"), "siteSub") ||
		strings.EqualFold(attrValue(n, "itemprop"), "interactionStatistic") ||
		strings.EqualFold(attrValue(n, "id"), "warning_not_complete") {
		return true
	}
	role := strings.ToLower(attrValue(n, "role"))
	if containsAny(role, "navigation", "complementary", "contentinfo", "menu") {
		return true
	}
	if isTableOfContentsRegion(n) || isLinkedImageMasthead(n) || isOversizedContributorRoll(n) ||
		elementContainsAny(n, "banner") && controls(n) > 0 {
		return true
	}
	if elementContainsAny(n, navigationStructureTokens...) && !headingDocumentsStructure(n) && hasNavigationShape(n) {
		return true
	}
	// Interactive control strips are commonly generic divs rather than nav or
	// toolbar roles. Likewise, an inline related-content component may contain a
	// single recommendation, so repeated-card detection cannot identify it.
	// Require both conventional compound naming and navigational shape to avoid
	// excluding prose merely because one broad word occurs in a class name.
	controlBar := elementContainsAny(n, "action", "control", "follow") && elementContainsAny(n, "bar", "toolbar")
	relatedContent := elementContainsAny(n, "related", "recommended") && elementContainsAny(n, "content", "story", "article", "card")
	if (controlBar || relatedContent) && hasNavigationShape(n) {
		return true
	}
	label := normalizedLabel(firstNonempty(attrValue(n, "aria-label"), attrValue(n, "title")))
	if auxiliaryLabels[label] {
		return true
	}
	if tag == "a" || tag == "button" || isHeadingTag(tag) {
		text := normalizedLabel(nodeText(n))
		if tag == "a" && strings.HasPrefix(text, "skip to ") {
			return true
		}
		if (tag == "a" || tag == "button") && (callToActionLabels[text] || auxiliaryLabels[text]) {
			return true
		}
		if isHeadingTag(tag) && auxiliaryLabels[text] {
			return true
		}
	}
	if tag == "div" || tag == "section" || tag == "aside" {
		if heading := firstRegionHeading(n); auxiliaryLabels[heading] {
			return true
		}
		// Editorial grids sometimes put a short taxonomy kicker (for example,
		// "News") before the actual "Related News" heading. Inspect only the
		// heading prefix, before any body block, so a later related section cannot
		// classify its enclosing article or main element as auxiliary.
		if elementContainsAny(n, "feature", "listing") && leadingRegionHasAuxiliaryHeading(n, 2) {
			return true
		}
	}
	if tag == "aside" && normalizedLabel(attrValue(n, "aria-label")) == "article details" {
		return true
	}
	return false
}

// A generic .footer outside primary semantic content is page furniture. The
// ancestor check preserves documentation for footer-named components.
func isPageFooterConvention(n *html.Node) bool {
	if !hasExactClass(n, "footer") {
		return false
	}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && (strings.EqualFold(p.Data, "main") || strings.EqualFold(p.Data, "article")) {
			return false
		}
	}
	return true
}

func hasClassPrefix(n *html.Node, prefix string) bool {
	for class := range strings.FieldsSeq(strings.ToLower(attrValue(n, "class"))) {
		if class == prefix || strings.HasPrefix(class, prefix+"-") || strings.HasPrefix(class, prefix+"_") {
			return true
		}
	}
	return false
}

// isTableOfContentsRegion recognizes a region marker, not a state flag on a
// content layout. Classes such as "toc-visible" and "toc-available" say that a
// separate TOC affects the grid; treating their ancestors as the TOC can remove
// the entire article.
func isTableOfContentsRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, key := range []string{"id", "class"} {
		for identifier := range strings.FieldsSeq(strings.ToLower(attrValue(n, key))) {
			if identifier == "table-of-contents" || identifier == "table_of_contents" {
				return true
			}
			// Avoid allocating segment slices on the overwhelmingly common path.
			if !containsAnyFold(identifier, "toc") {
				continue
			}
			segments := strings.FieldsFunc(identifier, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r)
			})
			for i, segment := range segments {
				if segment != "toc" {
					continue
				}
				// Responsive frameworks put state around the TOC segment in classes
				// such as toc-visible:md:grid-cols-10 and has-toc. Those classes
				// describe the content grid, while article-toc and sidebar-toc name
				// actual regions.
				if i+1 < len(segments) && tocStateSegment(segments[i+1]) ||
					i > 0 && tocStatePrefixSegment(segments[i-1]) {
					continue
				}
				return true
			}
		}
	}
	return false
}

func tocStateSegment(segment string) bool {
	switch segment {
	case "visible", "available", "enabled", "active", "open":
		return true
	}
	return false
}

func tocStatePrefixSegment(segment string) bool {
	return segment == "has" || segment == "with"
}

// Image-only headings linked to the site root are publication wordmarks, not
// article headings. Requiring the heading, home link, and image-only shape
// leaves linked article headings and ordinary figures unaffected.
func isLinkedImageMasthead(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !isHeadingTag(strings.ToLower(n.Data)) || normalizeText(nodeText(n)) != "" {
		return false
	}
	link := n.FirstChild
	for link != nil && link.Type == html.TextNode && strings.TrimSpace(link.Data) == "" {
		link = link.NextSibling
	}
	if link == nil || link.Type != html.ElementNode || !strings.EqualFold(link.Data, "a") {
		return false
	}
	for sibling := link.NextSibling; sibling != nil; sibling = sibling.NextSibling {
		if sibling.Type != html.CommentNode && (sibling.Type != html.TextNode || strings.TrimSpace(sibling.Data) != "") {
			return false
		}
	}
	href := strings.TrimSpace(attrValue(link, "href"))
	if href != "/" && href != "./" {
		return false
	}
	foundImage := false
	walk(link, func(x *html.Node) bool {
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "img") && normalizeText(attrValue(x, "alt")) != "" {
			foundImage = true
		}
		return true
	})
	return foundImage
}

func isAdvertisementRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(n.Data)
	if tag != "aside" && tag != "div" && tag != "section" {
		return false
	}
	// Restrict the direct marker to class names. An id such as
	// "advertisement" can legitimately name a documentation section.
	for class := range strings.FieldsSeq(strings.ToLower(attrValue(n, "class"))) {
		class = strings.Trim(class, "_- ")
		if class == "ad" || class == "ads" || class == "advert" || class == "advertisement" ||
			class == "advertising" || class == "sponsor" || class == "sponsored" ||
			strings.HasPrefix(class, "ad-") || strings.HasPrefix(class, "advert-") {
			return true
		}
	}
	if normalizedLabel(firstNonempty(attrValue(n, "aria-label"), attrValue(n, "title"))) == "advertisement" {
		return true
	}

	// Affiliate product widgets are often unlabeled ads. Require the product
	// marker on this candidate itself: borrowing shape from one child and a
	// sponsored link elsewhere in the subtree can otherwise classify the entire
	// editorial content container as an advertisement. Child widgets are visited
	// and classified independently by the normal ancestry checks.
	if !elementContainsAny(n, "product", "price", "buy-button", "affiliate") {
		return false
	}
	sponsored := false
	walk(n, func(x *html.Node) bool {
		if hardHidden(x) {
			return false
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "a") &&
			containsAny(strings.ToLower(attrValue(x, "rel")), "sponsored") {
			sponsored = true
		}
		return !sponsored
	})
	return sponsored
}

func hasClassConvention(n *html.Node, convention string) bool {
	for class := range strings.FieldsSeq(attrValue(n, "class")) {
		class = strings.ToLower(strings.Trim(class, "_- "))
		if class == convention || strings.HasPrefix(class, convention+"--") ||
			strings.HasPrefix(class, convention+"__") || strings.Contains(class, "-"+convention) {
			return true
		}
	}
	return false
}

func hasExactClass(n *html.Node, want string) bool {
	value := attrValue(n, "class")
	for start := 0; start < len(value); {
		for start < len(value) && htmlSpace(value[start]) {
			start++
		}
		end := start
		for end < len(value) && !htmlSpace(value[end]) {
			end++
		}
		if end == start {
			return false
		}
		if strings.EqualFold(value[start:end], want) {
			return true
		}
		start = end
	}
	return false
}

// HTML class, id, and role tokenization uses the five ASCII whitespace bytes.
func htmlSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

func hasAuthorProfileClass(n *html.Node) bool {
	for class := range strings.FieldsSeq(strings.ToLower(attrValue(n, "class"))) {
		class = strings.Trim(class, "_- ")
		if class == "author-profile" || class == "author-box" || class == "author-bio" || class == "author-biography" ||
			class == "about-author" || class == "about-the-author" {
			return true
		}
	}
	return false
}

func hasTrailingArticleRegionClass(n *html.Node) bool {
	for class := range strings.FieldsSeq(strings.ToLower(attrValue(n, "class"))) {
		class = strings.Trim(class, "_- ")
		if class == "post-nav" || class == "article-nav" || class == "related-stories" ||
			class == "related-posts" || class == "recommended-stories" || class == "recommendations" ||
			class == "post-info" || class == "post-meta" || class == "article-meta" || class == "entry-meta" {
			return true
		}
	}
	return false
}

func headingDocumentsStructure(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !strings.EqualFold(n.Data, "section") {
		return false
	}
	heading := firstRegionHeading(n)
	if heading == "" {
		return false
	}
	for _, token := range navigationStructureTokens {
		if elementContainsAny(n, token) && containsAny(heading, token) {
			return true
		}
	}
	return false
}

func hasNavigationShape(n *html.Node) bool {
	textLength := utf8.RuneCountInString(normalizeText(nodeText(n)))
	if textLength > 0 && float64(linkTextLength(n))/float64(textLength) >= .6 {
		return true
	}
	return controls(n) > 1
}

func (a *analysis) setIrrelevant(n *html.Node, irrelevant bool) {
	state := a.nodeStates[n]
	state.irrelevant = 1
	if irrelevant {
		state.irrelevant = 2
	}
	a.nodeStates[n] = state
}

// overrideIrrelevant is for late classification changes after ancestor results
// may already have been memoized. Normal first-time classification uses
// setIrrelevant directly and does not pay for a descendant walk.
func (a *analysis) overrideIrrelevant(n *html.Node, irrelevant bool) {
	a.setIrrelevant(n, irrelevant)
	walk(n, func(descendant *html.Node) bool {
		state, cached := a.nodeStates[descendant]
		if cached {
			state.irrelevantAncestor = 0
			a.nodeStates[descendant] = state
		}
		return true
	})
}

func (a *analysis) isIrrelevantNode(n *html.Node) bool {
	if state := a.nodeStates[n].irrelevant; state != 0 {
		return state == 2
	}
	irrelevant := irrelevantNode(n) || isAdvertisementRegion(n)
	// An empty comments header is auxiliary regardless of the selected profile.
	// This also covers generic pages, where article-only filtering would otherwise
	// allow labels such as “thread” and “discussion” into Markdown.
	if !irrelevant && a.isEmptyCommentControlRegion(n) {
		irrelevant = true
	}
	if !irrelevant && a.pageType == PageTypeDiscussion && isDiscussionAuxiliaryLabelNode(n) {
		irrelevant = true
	}
	if !irrelevant && a.pageType == PageTypeArticle {
		irrelevant = a.articleAuxiliaryNode(n) || a.isTrailingSocialCardRegion(n) ||
			a.isPeripheralLinkRegion(n) || a.isTrailingMarketingRegion(n) || a.microdataArticleRecords[n]
	}
	if !irrelevant && a.isTrailingArticleCardRegion(n) {
		// A final article classification makes trailing cards auxiliary. When
		// card tokens instead caused an inferred listing classification, require
		// an explicit promotional-region marker. Never override a caller's
		// listing/collection classification.
		irrelevant = a.pageType == PageTypeArticle ||
			(a.pageType == PageTypeListing && !a.pageTypeExplicit && isPromotionalCardRegion(n))
	}
	a.setIrrelevant(n, irrelevant)
	return irrelevant
}

// inferenceAuxiliaryBlock identifies regions whose repeated records describe
// other pages. This is intentionally independent of the eventual page type so
// recommendation cards cannot cause that type to become a listing in the first
// place.
func (a *analysis) inferenceAuxiliaryBlock(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		switch a.nodeStates[p].inferenceAuxiliary {
		case 1:
			a.cacheInferenceAuxiliaryPath(n, p, 1)
			return true
		case 2:
			a.cacheInferenceAuxiliaryPath(n, p, 2)
			return false
		}
		auxiliary := irrelevantNode(p) || isAdvertisementRegion(p)
		if !auxiliary && p.Type == html.ElementNode && (strings.EqualFold(p.Data, "aside") ||
			elementContainsAny(p, "sidebar")) {
			// Asides and explicitly named sidebars may contain complete-looking
			// comments or message previews, but they are not candidates for the
			// page's primary shape.
			auxiliary = true
		}
		// Comment regions with substantive records remain page-type evidence;
		// empty/collapsed widgets are only page furniture. In particular, their
		// thread and reply vocabulary must not classify an article as a forum.
		if !auxiliary && a.isEmptyCommentControlRegion(p) {
			auxiliary = true
		}
		if !auxiliary && a.articleAuxiliaryNode(p) && !a.isArticleCommentRegion(p) &&
			(!isRelatedCardRegion(p) || hasSemanticArticleBeforeOrAround(p)) {
			auxiliary = true
		}
		if !auxiliary && (a.isTrailingSocialCardRegion(p) || a.isPeripheralLinkRegion(p) || a.isTrailingMarketingRegion(p)) {
			auxiliary = true
		}
		if !auxiliary && isPromotionalCardRegion(p) && a.isTrailingArticleCardRegion(p) {
			auxiliary = true
		}
		if auxiliary {
			a.cacheInferenceAuxiliaryPath(n, p, 1)
			return true
		}
	}
	a.cacheInferenceAuxiliaryPath(n, nil, 2)
	return false
}

// cacheInferenceAuxiliaryPath avoids allocating a temporary ancestor slice on
// every query. The second parent walk is cheap and only occurs on cache misses.
func (a *analysis) cacheInferenceAuxiliaryPath(n, end *html.Node, value uint8) {
	for p := n; p != nil; p = p.Parent {
		state := a.nodeStates[p]
		state.inferenceAuxiliary = value
		a.nodeStates[p] = state
		if p == end {
			return
		}
	}
}

func (a *analysis) primaryArticleAncestor(n *html.Node) *html.Node {
	for p := n; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, "article") &&
			!elementContainsAny(p, "card") && !a.inferenceAuxiliaryBlock(p) {
			return p
		}
	}
	return nil
}

// isTrailingSocialCardRegion identifies social/profile furniture and preview
// cards placed after the primary article. Social vocabulary alone is not
// enough: posts embedded within the semantic article can be authored content.
func (a *analysis) isTrailingSocialCardRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	// Reject ordinary containers before doing ancestry, document-order, or
	// subtree work. Pages can have thousands of neutral siblings after an
	// article, and scanning all preceding siblings for each one is quadratic.
	tag := strings.ToLower(n.Data)
	switch tag {
	case "aside", "section", "div", "article", "figure":
	default:
		return false
	}
	cardShape := tag == "aside" || elementContainsAny(n, "card", "embed", "post")
	platformMarker := elementContainsAny(n,
		"bsky", "bluesky", "mastodon", "twitter", "tweet", "instagram",
		"facebook", "linkedin", "fediverse")
	// “Social” and “threads” can describe substantive article subjects. They
	// only become auxiliary evidence when paired with recognizable card shape.
	genericSocialMarker := elementContainsAny(n, "social", "threads") && cardShape
	profileMarker := elementContainsAny(n, "share", "profile", "subscribe") && cardShape
	selfPreviewCandidate := cardShape && (tag == "aside" || elementContainsAny(n, "card", "preview"))
	if !platformMarker && !genericSocialMarker && !profileMarker && !selfPreviewCandidate {
		return false
	}
	if hasNonCardArticleAncestor(n) || !a.hasSemanticArticleBefore(n) {
		return false
	}
	if platformMarker || genericSocialMarker || profileMarker {
		return true
	}
	// Only structured preview candidates pay for the cached subtree query.
	return a.hasSelfReference(n)
}

// hasSemanticArticleBefore answers a document-order query from a lazily built
// index. Building the index once avoids repeatedly scanning preceding sibling
// subtrees for every trailing candidate.
func (a *analysis) hasSemanticArticleBefore(n *html.Node) bool {
	if !a.semanticBeforeIndexed {
		a.semanticBeforeIndexed = true
		seen := false
		walk(a.root, func(x *html.Node) bool {
			if hardHidden(x) {
				return false
			}
			// All callers query regions (elements), so indexing text nodes only
			// inflated this document-wide map.
			if x.Type != html.ElementNode {
				return true
			}
			state := a.nodeStates[x]
			state.semanticBefore = 1
			if seen {
				state.semanticBefore = 2
			}
			a.nodeStates[x] = state
			if strings.EqualFold(x.Data, "article") && !elementContainsAny(x, "card") {
				seen = true
			}
			return true
		})
	}
	return a.nodeStates[n].semanticBefore == 2
}

func (a *analysis) hasSemanticArticleAfter(n *html.Node) bool {
	if !a.semanticAfterIndexed {
		a.semanticAfterIndexed = true
		seen := false
		// Visit in reverse preorder rather than collecting the complete document
		// into a temporary slice. This preserves the document-order semantics while
		// avoiding an elements-sized allocation on pages that need this index.
		walkVisibleReverse(a.root, func(x *html.Node) {
			if x.Type != html.ElementNode {
				return
			}
			state := a.nodeStates[x]
			state.semanticAfter = 1
			if seen {
				state.semanticAfter = 2
			}
			a.nodeStates[x] = state
			if strings.EqualFold(x.Data, "article") && !elementContainsAny(x, "card") {
				seen = true
			}
		})
	}
	return a.nodeStates[n].semanticAfter == 2
}

func (a *analysis) hasSelfReference(root *html.Node) (result bool) {
	if root == nil || hardHidden(root) {
		return false
	}
	if state := a.nodeStates[root].selfReference; state != 0 {
		return state == 2
	}
	defer func() {
		state := a.nodeStates[root]
		state.selfReference = 1
		if result {
			state.selfReference = 2
		}
		a.nodeStates[root] = state
	}()

	target := a.meta.canonical
	if target == "" && a.pageURL != nil {
		target = a.pageURL.String()
	}
	target = comparablePageURL(target, nil)
	if target == "" {
		return false
	}
	if root.Type == html.ElementNode && strings.EqualFold(root.Data, "a") &&
		comparablePageURL(attrValue(root, "href"), a.base) == target {
		return true
	}
	for ch := root.FirstChild; ch != nil; ch = ch.NextSibling {
		if a.hasSelfReference(ch) {
			return true
		}
	}
	return false
}

func comparablePageURL(raw string, base *url.URL) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	q := u.Query()
	for key := range q {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "fbclid" || lower == "gclid" {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	u.Path = strings.TrimSuffix(u.Path, "/")
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

func isRelatedCardRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	return elementContainsAny(n, "related", "recommended", "recommendations") && countMarkedCards(n, 2) >= 2
}

// hasAuxiliaryHeading is deliberately broader than the unconditional label
// checks. It is only used together with repeated-record structure.
func hasAuxiliaryHeading(n *html.Node) bool {
	heading := firstRegionHeading(n)
	if auxiliaryLabels[heading] || isArticleAuxiliaryLabel(heading) {
		return true
	}
	return isAmbiguousRecommendationsHeading(heading) ||
		strings.HasPrefix(heading, "related ") || strings.HasPrefix(heading, "recommended ") ||
		strings.HasPrefix(heading, "more stories ") || strings.HasPrefix(heading, "you may also ")
}

// hasDeepLeadingAuxiliaryHeading handles presentation wrappers which put a
// heading and its card grid in sibling divs. It considers only the first
// heading or prose block, so an auxiliary section later in an article cannot
// cause the article root itself to be discarded.
func hasDeepLeadingAuxiliaryHeading(n *html.Node) bool {
	budget := 64
	label := ""
	walk(n, func(x *html.Node) bool {
		if label != "" || budget <= 0 || hardHidden(x) {
			return false
		}
		if x.Type != html.ElementNode {
			return true
		}
		budget--
		tag := strings.ToLower(x.Data)
		if isHeadingTag(tag) {
			label = normalizedLabel(nodeText(x))
			return false
		}
		if x != n && (tag == "p" || tag == "blockquote" || tag == "pre") && normalizeText(nodeText(x)) != "" {
			label = "content"
			return false
		}
		return true
	})
	return auxiliaryLabels[label] || isArticleAuxiliaryLabel(label)
}

func isAmbiguousRecommendationsHeading(heading string) bool {
	return heading == "recommended" || heading == "recommendations"
}

// isBroadEditorialAuxiliaryHeading identifies labels whose prefix is often
// used for publication furniture but is also conventional editorial language.
// Exact labels already known to be boilerplate remain unambiguous.
func isBroadEditorialAuxiliaryHeading(heading string) bool {
	if auxiliaryLabels[heading] || isArticleAuxiliaryLabel(heading) {
		return false
	}
	return isAmbiguousRecommendationsHeading(heading) ||
		strings.HasPrefix(heading, "related ") || strings.HasPrefix(heading, "recommended ")
}

// countLinkedRecords recognizes recommendation collections even when the site
// does not use card classes. A record needs its own container, a link, and a
// title-like heading or date; nested wrappers are counted only once.
func countLinkedRecords(root *html.Node, limit int) int {
	count := 0
	var visit func(*html.Node) bool
	visit = func(n *html.Node) bool {
		if hardHidden(n) || n.Type != html.ElementNode || count >= limit {
			return false
		}
		// Prefer the deepest matching containers. Otherwise a neutral grid
		// wrapper around several cards would be mistaken for one large record.
		hasChildRecord := false
		for ch := n.FirstChild; ch != nil && count < limit; ch = ch.NextSibling {
			if visit(ch) {
				hasChildRecord = true
			}
		}
		if hasChildRecord || n == root || count >= limit {
			return hasChildRecord
		}
		tag := strings.ToLower(n.Data)
		if tag != "article" && tag != "li" && tag != "div" {
			return false
		}
		links, titleOrDate := 0, false
		walk(n, func(x *html.Node) bool {
			if hardHidden(x) {
				return false
			}
			if x != n && x.Type == html.ElementNode {
				t := strings.ToLower(x.Data)
				if t == "a" {
					links++
				}
				if isHeadingTag(t) || t == "time" {
					titleOrDate = true
				}
			}
			return links <= 3
		})
		if links > 0 && links <= 3 && titleOrDate {
			count++
			return true
		}
		return false
	}
	visit(root)
	return count
}

// isOversizedContributorRoll removes attribution/history paragraphs that can
// contain hundreds of linked usernames. A short author or contributor list is
// useful metadata, but an unbounded roll can dwarf the page's primary content.
// The high link threshold and explicit attribution language avoid treating
// ordinary citation-heavy prose as auxiliary.
func isOversizedContributorRoll(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !strings.EqualFold(n.Data, "p") {
		return false
	}
	links := 0
	walk(n, func(x *html.Node) bool {
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "a") {
			links++
		}
		return links < 25
	})
	if links < 25 {
		return false
	}
	text := " " + normalizedLabel(nodeText(n)) + " "
	return strings.Contains(text, " also edited by ") ||
		strings.Contains(text, " contributors: ") ||
		strings.HasPrefix(strings.TrimSpace(text), "contributors ")
}

func isArticleDiscussionLinks(n *html.Node) bool {
	if n == nil || !strings.EqualFold(n.Data, "p") {
		return false
	}
	label := normalizedLabel(nodeText(n))
	if !strings.HasPrefix(label, "discuss on ") || utf8.RuneCountInString(label) > 120 {
		return false
	}
	links := 0
	walk(n, func(x *html.Node) bool {
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "a") {
			links++
			return false
		}
		return true
	})
	return links > 0
}

func isArticleSharingControls(n *html.Node) bool {
	if n == nil || !strings.EqualFold(n.Data, "ul") || !strings.HasPrefix(normalizedLabel(nodeText(n)), "share") {
		return false
	}
	shareLinks := 0
	walk(n, func(x *html.Node) bool {
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "a") {
			label := normalizedLabel(attrValue(x, "aria-label"))
			href := strings.ToLower(attrValue(x, "href"))
			if strings.HasPrefix(label, "share on ") || containsAny(href, "/share?", "/sharer/", "sharearticle?") {
				shareLinks++
			}
			return false
		}
		return true
	})
	return shareLinks > 0
}

func isArticleBackControl(n *html.Node) bool {
	if n == nil || !containsToken(elementTokens(n), []string{"back"}) {
		return false
	}
	text := normalizedLabel(nodeText(n))
	links := 0
	walk(n, func(x *html.Node) bool {
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "a") {
			links++
			return false
		}
		return true
	})
	return links == 1 && utf8.RuneCountInString(text) <= 40 && strings.HasSuffix(text, "all posts")
}

func isArticleTaxonomySeparator(n *html.Node) bool {
	if n == nil || !strings.EqualFold(n.Data, "hr") {
		return false
	}
	tokens := elementTokens(n)
	return containsAny(tokens, "tag", "tags", "topic", "topics", "taxonomy", "category", "categories") &&
		containsAny(tokens, "separator", "divider")
}

func isTrailingArticleSeparator(n *html.Node) bool {
	if n == nil || !strings.EqualFold(n.Data, "hr") {
		return false
	}
	for sibling := n.NextSibling; sibling != nil; sibling = sibling.NextSibling {
		if sibling.Type == html.CommentNode ||
			(sibling.Type == html.TextNode && normalizeText(sibling.Data) == "") ||
			hardHidden(sibling) {
			continue
		}
		return sibling.Type == html.ElementNode &&
			(hasTrailingArticleRegionClass(sibling) || strings.EqualFold(sibling.Data, "footer") || hasDataMarker(sibling, "site-footer"))
	}
	return false
}

func isArticleTaxonomyRegion(n *html.Node) bool {
	if n == nil {
		return false
	}
	tag := strings.ToLower(n.Data)
	if tag != "section" && tag != "div" && tag != "aside" {
		return false
	}
	heading := firstRegionHeading(n)
	if heading != "tags" && heading != "topics" && heading != "categories" {
		return false
	}
	tagLinks, proseParagraphs := 0, 0
	walk(n, func(x *html.Node) bool {
		if x.Type != html.ElementNode {
			return true
		}
		if strings.EqualFold(x.Data, "a") && containsAny(strings.ToLower(attrValue(x, "rel")), "tag") {
			tagLinks++
			return false
		}
		if strings.EqualFold(x.Data, "p") && normalizeText(nodeText(x)) != "" {
			proseParagraphs++
			return false
		}
		return true
	})
	// A taxonomy heading and rel=tag link are not sufficient by themselves:
	// articles can discuss categories or topics and link to a live example.
	// Publication taxonomy furniture is list-like, so retain any region that
	// contains prose rather than trying to classify it by paragraph length.
	return tagLinks > 0 && proseParagraphs == 0
}

func (a *analysis) articleAuxiliaryNode(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if state := a.nodeStates[n].articleAuxiliary; state != 0 {
		return state == 2
	}
	auxiliary := a.articleAuxiliaryNodeUncached(n)
	state := a.nodeStates[n]
	state.articleAuxiliary = 1
	if auxiliary {
		state.articleAuxiliary = 2
	}
	a.nodeStates[n] = state
	return auxiliary
}

func (a *analysis) articleAuxiliaryNodeUncached(n *html.Node) bool {
	if isArticleDiscussionLinks(n) || isArticleSharingControls(n) || isArticleBackControl(n) ||
		isArticleTaxonomySeparator(n) || isTrailingArticleSeparator(n) || isArticleTaxonomyRegion(n) {
		return true
	}
	if a.isSubscriptionRegion(n) {
		// Subscription evidence may live in a trailing child of a page-wide or
		// article-wide wrapper. Exclude that child when it is visited, rather
		// than hiding substantive prose that precedes it in the shared wrapper.
		if !a.hasArticleBodyDescendant(n) && !hasSubstantiveContentBeforeDescendant(n, isFormElement) {
			return true
		}
	}
	if a.isArticleCommentRegion(n) {
		return true
	}
	// A separately marked author profile is publication furniture, even when it
	// uses section/div rather than aside or schema.org Person markup.
	if hasAuthorProfileClass(n) {
		return true
	}
	if hasTrailingArticleRegionClass(n) && a.hasSemanticArticleBefore(n) {
		return true
	}
	tag := strings.ToLower(n.Data)
	label := normalizedLabel(firstNonempty(attrValue(n, "aria-label"), attrValue(n, "title")))
	if isArticleAuxiliaryLabel(label) {
		return true
	}
	if tag == "a" || tag == "button" || isHeadingTag(tag) {
		if isArticleAuxiliaryLabel(normalizedLabel(nodeText(n))) {
			return true
		}
	}
	if tag == "div" || tag == "section" || tag == "aside" {
		regionHeading := firstRegionHeading(n)
		if isArticleAuxiliaryLabel(regionHeading) {
			return true
		}
		tokens := elementTokens(n)
		itemtype := strings.ToLower(attrValue(n, "itemtype"))
		// Author profiles commonly precede the article in a sidebar. Microformats
		// use h-card while schema.org uses Person; neither is article content when
		// the profile sits outside the semantic article.
		personProfile := containsAny(itemtype, "person") || containsAny(tokens, "h-card")
		if !hasNonCardArticleAncestor(n) && (personProfile ||
			(tag == "aside" && containsAny(tokens, "author", "byline", "bio", "profile"))) {
			return true
		}
		if isRelatedCardRegion(n) && !a.hasArticleBodyDescendant(n) &&
			!hasSubstantiveContentBeforeDescendant(n, isMarkedCard) {
			return true
		}
		if (hasAuxiliaryHeading(n) || hasDeepLeadingAuxiliaryHeading(n)) && countLinkedRecords(n, 2) >= 2 {
			// Broad “Recommended …” and “Related …” labels are common
			// editorial headings. Linked records alone do not make such a
			// section promotional when it belongs to the primary article.
			if !isBroadEditorialAuxiliaryHeading(firstRegionHeading(n)) || !hasNonCardArticleAncestor(n) {
				return true
			}
		}
		if a.isTrailingOrganizationProfileRegion(n) {
			return true
		}
	}
	return false
}

// isTrailingOrganizationProfileRegion identifies a separately headed company
// profile appended to an article. "About Us" by itself is deliberately not an
// auxiliary label: it is excluded only when trailing structure and at least two
// independent organization-profile signals agree.
func (a *analysis) isTrailingOrganizationProfileRegion(n *html.Node) bool {
	if !isOrganizationAboutHeading(firstRegionHeading(n), a.meta.site) ||
		a.hasLaterArticleContent(n) || !hasArticleContentBefore(n) {
		return false
	}

	text := strings.ToLower(normalizeText(nodeText(n)))
	signals := 0
	tokens := elementTokens(n)
	if containsAny(tokens, "company", "corporate", "organization", "organisation", "about-us", "aboutus") {
		signals++
	}
	if organizationProfileLanguage(text) {
		signals++
	}
	if a.mentionsSiteIdentity(text) {
		signals++
	}

	hasOrganizationSchema, hasOrganizationLink := false, false
	walk(n, func(x *html.Node) bool {
		if hardHidden(x) {
			return false
		}
		if x.Type != html.ElementNode {
			return true
		}
		if containsAny(strings.ToLower(attrValue(x, "itemtype")), "organization", "organisation", "corporation") {
			hasOrganizationSchema = true
		}
		if strings.EqualFold(x.Data, "a") && isOrganizationLink(attrValue(x, "href"), a.base) {
			hasOrganizationLink = true
		}
		return !(hasOrganizationSchema && hasOrganizationLink)
	})
	if hasOrganizationSchema {
		signals++
	}
	if hasOrganizationLink {
		signals++
	}
	return signals >= 2
}

func isOrganizationAboutHeading(label, site string) bool {
	if label == "about us" || label == "about the company" || label == "about our company" ||
		label == "about the organization" || label == "about the organisation" {
		return true
	}
	if !strings.HasPrefix(label, "about ") || len(strings.Fields(label)) > 7 {
		return false
	}
	// A publisher name makes "About <organization>" strong heading evidence.
	// Partial names must match complete words and contain a meaningful token;
	// this avoids treating "About Press" as a match for "Pressure Labs".
	normalizedSite := normalizedLabel(site)
	organization := strings.TrimPrefix(label, "about ")
	return normalizedSite != "" && (label == "about "+normalizedSite ||
		hasMeaningfulIdentity(organization) && containsWordSequence(normalizedSite, organization))
}

func isOrganizationLink(raw string, base *url.URL) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, socialHost := range []string{"linkedin.com", "facebook.com", "instagram.com"} {
		if host == socialHost || strings.HasSuffix(host, "."+socialHost) {
			return true
		}
	}
	for _, segment := range strings.Split(strings.ToLower(strings.Trim(u.Path, "/")), "/") {
		switch strings.Trim(segment, "-_") {
		case "about", "about-us", "about_us", "company", "careers", "contact", "products":
			return true
		}
	}
	return false
}

func (a *analysis) mentionsSiteIdentity(text string) bool {
	if site := strings.ToLower(normalizeText(a.meta.site)); hasMeaningfulIdentity(site) && containsWordSequence(text, site) {
		return true
	}
	if a.pageURL == nil {
		return false
	}
	// Publisher metadata is not universal. A distinctive hostname label is a
	// useful fallback identity signal (for example system76.com -> system76),
	// but generic hosting and site-purpose labels are ignored.
	generic := map[string]bool{"www": true, "blog": true, "news": true, "medium": true, "wordpress": true, "blogspot": true, "github": true}
	for _, label := range strings.Split(strings.ToLower(a.pageURL.Hostname()), ".") {
		if hasMeaningfulIdentity(label) && !generic[label] && containsWordSequence(text, label) {
			return true
		}
	}
	return false
}

func containsWordSequence(text, phrase string) bool {
	phraseOffset := 0
	firstPhrase, ok := nextWord(phrase, &phraseOffset)
	if !ok {
		return false
	}
	textOffset := 0
	for {
		textWord, ok := nextWord(text, &textOffset)
		if !ok {
			return false
		}
		if !strings.EqualFold(textWord, firstPhrase) {
			continue
		}

		// Try the remaining words using local offsets. A failed match leaves the
		// outer scan at the word after this candidate, so overlapping candidates
		// are still considered without allocating token slices.
		t, p := textOffset, phraseOffset
		for {
			phraseWord, more := nextWord(phrase, &p)
			if !more {
				return true
			}
			textWord, more = nextWord(text, &t)
			if !more || !strings.EqualFold(textWord, phraseWord) {
				break
			}
		}
	}
}

func nextWord(s string, offset *int) (string, bool) {
	start := *offset
	for start < len(s) {
		r, size := utf8.DecodeRuneInString(s[start:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			break
		}
		start += size
	}
	if start == len(s) {
		*offset = start
		return "", false
	}
	end := start
	for end < len(s) {
		r, size := utf8.DecodeRuneInString(s[end:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		end += size
	}
	*offset = end
	return s[start:end], true
}

func containsAnyWordSequence(text string, phrases ...string) bool {
	for _, phrase := range phrases {
		if containsWordSequence(text, phrase) {
			return true
		}
	}
	return false
}

func hasMeaningfulIdentity(identity string) bool {
	for _, word := range strings.FieldsFunc(identity, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(word)) >= 4 {
			return true
		}
	}
	return false
}

func organizationProfileLanguage(text string) bool {
	organizationWord := strings.Contains(text, " company") || strings.Contains(text, " organization") ||
		strings.Contains(text, " organisation") || strings.Contains(text, " corporation")
	if !organizationWord {
		return false
	}
	return strings.Contains(text, " is a ") || strings.Contains(text, " is an ") ||
		strings.Contains(text, " is the ") || strings.Contains(text, "we are a ") ||
		strings.Contains(text, "we are an ") || strings.Contains(text, "our company") ||
		strings.Contains(text, "founded in ") || strings.Contains(text, "headquartered in ")
}

// hasArticleContentBefore requires the candidate to be a distinct region after
// the primary article body, either as a later sibling or as a final child of a
// semantic article. This intentionally does not classify ordinary About
// headings embedded directly in flowing article content.
func hasArticleContentBefore(n *html.Node) bool {
	if hasNonCardArticleAncestor(n) {
		for branch := n; branch != nil && branch.Parent != nil; branch = branch.Parent {
			for sibling := branch.PrevSibling; sibling != nil; sibling = sibling.PrevSibling {
				if subtreeHasArticleText(sibling) {
					return true
				}
			}
			if strings.EqualFold(branch.Parent.Data, "article") {
				break
			}
		}
		return false
	}
	return hasSemanticArticleBeforeOrAround(n)
}

// hasLaterArticleContent keeps an About section when the article resumes after
// it (for example with a Conclusion section). Already-classified auxiliary
// siblings do not count as resumed content.
func (a *analysis) hasLaterArticleContent(n *html.Node) bool {
	if !hasNonCardArticleAncestor(n) {
		return false
	}
	for branch := n; branch != nil && branch.Parent != nil; branch = branch.Parent {
		for sibling := branch.NextSibling; sibling != nil; sibling = sibling.NextSibling {
			if a.subtreeHasRelevantArticleText(sibling) {
				return true
			}
		}
		if strings.EqualFold(branch.Parent.Data, "article") {
			break
		}
	}
	return false
}

func (a *analysis) subtreeHasRelevantArticleText(n *html.Node) (found bool) {
	walk(n, func(x *html.Node) bool {
		if found || hardHidden(x) {
			return false
		}
		// Prune complete boilerplate regions before looking at their paragraphs
		// or headings. Calling the classifier on a later sibling is safe: an
		// organization-profile check only scans forward, so it cannot recurse
		// back into the profile currently being classified.
		if x.Type == html.ElementNode && a.isIrrelevantNode(x) {
			return false
		}
		if x.Type == html.ElementNode {
			tag := strings.ToLower(x.Data)
			if (tag == "p" || tag == "li" || isHeadingTag(tag)) && normalizeText(nodeText(x)) != "" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func subtreeHasArticleText(n *html.Node) (found bool) {
	walk(n, func(x *html.Node) bool {
		if found || hardHidden(x) {
			return false
		}
		if x.Type == html.ElementNode {
			tag := strings.ToLower(x.Data)
			if (tag == "p" || tag == "li" || isHeadingTag(tag)) && normalizeText(nodeText(x)) != "" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// isPeripheralLinkRegion removes article-adjacent taxonomy, footer navigation,
// and unlabelled recommendation/contact collections. Link density is only used
// outside the article body and must agree with article-relative position.
func (a *analysis) isPeripheralLinkRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || hasNonCardArticleAncestor(n) || a.hasArticleBodyDescendant(n) {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "div", "section", "aside", "ul", "header":
	default:
		return false
	}
	before, after := a.hasSemanticArticleBefore(n), a.hasSemanticArticleAfter(n)
	if !before && !after {
		return false
	}
	heading := firstRegionHeading(n)
	// Citations and editorial reading lists are part of the article even when
	// publishers place them beside, rather than inside, the semantic article.
	if isEditorialReferenceHeading(heading) {
		return false
	}
	if before && countLinkedRecords(n, 3) >= 3 &&
		(hasAuxiliaryHeading(n) || elementContainsAny(n,
			"related", "recommended", "recommendations", "promo", "contact-cards")) {
		return true
	}

	links, internal, longest := 0, 0, 0
	walk(n, func(x *html.Node) bool {
		if hardHidden(x) {
			return false
		}
		if x.Type != html.ElementNode {
			return true
		}
		tag := strings.ToLower(x.Data)
		if tag == "a" {
			links++
			href := strings.TrimSpace(attrValue(x, "href"))
			if strings.HasPrefix(href, "/") || strings.HasPrefix(href, "#") ||
				(!strings.Contains(href, "://") && !strings.HasPrefix(href, "mailto:")) {
				internal++
			}
			return false
		}
		if tag == "p" {
			if l := utf8.RuneCountInString(normalizeText(nodeText(x))); l > longest {
				longest = l
			}
		}
		return true
	})
	textLen := utf8.RuneCountInString(normalizeText(nodeText(n)))
	if links == 0 || textLen == 0 || longest > 140 {
		return false
	}
	ratio := float64(linkTextLength(n)) / float64(textLen)
	if before {
		return links >= 5 && internal*2 >= links && ratio >= .55
	}
	// Pre-title topic taxonomies use fewer links but normally identify
	// themselves in class/id attributes.
	return links >= 3 && internal*2 >= links && ratio >= .65 &&
		elementContainsAny(n, "tag", "tags", "topic", "topics", "taxonomy", "category", "categories")
}

func isEditorialReferenceHeading(heading string) bool {
	switch heading {
	case "sources", "references", "evidence", "further reading", "sources and evidence",
		"notes and references", "references and notes", "bibliography", "works cited":
		return true
	}
	return strings.HasPrefix(heading, "sources and ") || strings.HasPrefix(heading, "references and ")
}

// isTrailingMarketingRegion catches a distinct call-to-action panel whose
// heading is followed by controls rather than article prose. It intentionally
// requires both structural interaction and earlier article content.
func (a *analysis) isTrailingMarketingRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || hasNonCardArticleAncestor(n) || a.hasArticleBodyDescendant(n) {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "div", "section", "aside", "fieldset":
	default:
		return false
	}
	if !a.hasSemanticArticleBefore(n) && !a.hasLongArticleProseBefore(n) {
		return false
	}
	heading := firstRegionHeading(n)
	interactions, links := marketingInteractions(n)
	if heading == "" || interactions == 0 || regionHasLongProse(n, 180) {
		return false
	}
	text := normalizedLabel(nodeText(n))
	marked := elementContainsAny(n, "promo", "marketing", "register", "signup", "sign-up", "subscribe")
	action := containsAnyWordSequence(text, "sign up", "register", "subscribe", "apply now", "get started", "get updates", "join now")
	socialFollow := strings.HasPrefix(heading, "follow ") && links >= 2
	headingCTA := strings.HasPrefix(heading, "get ") || strings.HasPrefix(heading, "apply ") ||
		strings.HasPrefix(heading, "register ") || strings.HasPrefix(heading, "sign up ")
	return marked || action || socialFollow || headingCTA
}

func marketingInteractions(n *html.Node) (interactions, links int) {
	walk(n, func(x *html.Node) bool {
		if hardHidden(x) {
			return false
		}
		if x.Type != html.ElementNode {
			return true
		}
		switch strings.ToLower(x.Data) {
		case "button", "input", "select", "textarea":
			interactions++
		case "a":
			links++
			label := normalizedLabel(nodeText(x))
			if strings.HasPrefix(label, "get ") || strings.HasPrefix(label, "start ") ||
				strings.HasPrefix(label, "connect ") || strings.HasPrefix(label, "apply") || strings.HasPrefix(label, "register") ||
				strings.HasPrefix(label, "sign up") || label == "learn more" || label == "contact us" {
				interactions++
			}
			return false
		}
		return true
	})
	return interactions, links
}

func (a *analysis) hasLongArticleProseBefore(n *html.Node) bool {
	if !a.articleProseBeforeIndexed {
		a.articleProseBeforeIndexed = true
		seen := false
		walk(a.root, func(x *html.Node) bool {
			if hardHidden(x) {
				return false
			}
			if x.Type != html.ElementNode {
				return true
			}
			// This cache is only queried for possible marketing-region roots. Avoid
			// retaining an entry for every text and inline node in a large document.
			switch strings.ToLower(x.Data) {
			case "div", "section", "aside", "fieldset":
				state := a.nodeStates[x]
				state.articleProseBefore = 1
				if seen {
					state.articleProseBefore = 2
				}
				a.nodeStates[x] = state
			}
			if strings.EqualFold(x.Data, "p") &&
				utf8.RuneCountInString(normalizeText(nodeText(x))) >= 100 {
				seen = true
			}
			return true
		})
	}
	return a.nodeStates[n].articleProseBefore == 2
}

func regionHasLongProse(n *html.Node, limit int) bool {
	found := false
	walk(n, func(x *html.Node) bool {
		if found || hardHidden(x) {
			return false
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "p") &&
			utf8.RuneCountInString(normalizeText(nodeText(x))) >= limit {
			found = true
			return false
		}
		return true
	})
	return found
}

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
	evidence := a.nodeStates[n].subscriptionEvidence
	return evaluateSubscriptionRegion(n, evidence&subtreeHasForm != 0,
		evidence&subtreeHasEmail != 0, evidence&subtreeHasSubscriptionForm != 0)
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
	return evaluateSubscriptionRegion(n, hasForm, hasEmail, subscriptionForm)
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

func evaluateSubscriptionRegion(n *html.Node, hasForm, hasEmail, subscriptionForm bool) bool {
	attributeMarker := subscriptionAttributeMarker(n)
	// Text collection and heading discovery are comparatively expensive on large
	// wrappers. Neither matters without a form or an explicit marker.
	if !hasForm && !attributeMarker {
		return false
	}
	heading := firstRegionHeading(n)
	text := strings.ToLower(normalizeText(nodeText(n)))
	cta := strings.Contains(text, "subscribe") || strings.Contains(text, "sign up") ||
		strings.Contains(text, "mailing list") || strings.Contains(text, "get updates")

	if !hasForm && attributeMarker && cta && !substantialArticleScope(n) &&
		(isSubscriptionPromptHeading(heading) || hasSubscriptionDestination(n)) {
		return true
	}
	formEvidence := hasEmail || subscriptionForm || (hasForm && cta)
	if isSubscriptionPromptHeading(heading) {
		return formEvidence && cta
	}

	if !hasForm || (!hasEmail && controls(n) < 2) {
		return false
	}
	// CTA labels are needed only when the surrounding text and form action did
	// not already provide equivalent evidence.
	if !cta && !subscriptionForm && !hasJoinCTA(n) {
		return false
	}
	consent := containsAnyWordSequence(text, "privacy policy", "terms of use", "terms and conditions")
	honeypot := containsAnyWordSequence(text, "field is for validation", "leave this field unchanged", "do not fill")
	return consent || honeypot
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

// isArticleCommentRegion identifies the region containing reader responses,
// rather than trying to filter every reply, like, and form control separately.
// These signals are deliberately article-only: the same records are primary
// content when the selected profile is a discussion.
func (a *analysis) isArticleCommentRegion(n *html.Node) (result bool) {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if state := a.nodeStates[n].articleComment; state != 0 {
		return state == 2
	}
	defer func() {
		state := a.nodeStates[n]
		state.articleComment = 1
		if result {
			state.articleComment = 2
		}
		a.nodeStates[n] = state
	}()

	tokens := elementTokens(n)
	// Plural comment markers and established comment-list conventions are
	// sufficiently specific on article pages. “Responses” and “replies” are
	// ambiguous (for example, survey responses), so they require the heading or
	// repeated-record evidence checked below.
	if containsAny(tokens, "comments", "commentlist") ||
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
	if state := a.nodeStates[root].commentCount; state != 0 {
		return int(state - 1)
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
	state.commentCount = uint8(count + 1)
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

func (a *analysis) hasArticleBodyDescendant(root *html.Node) bool {
	if root == nil || hardHidden(root) {
		return false
	}
	if state := a.nodeStates[root].articleDescendant; state != 0 {
		return state == 2
	}
	found := false
	for ch := root.FirstChild; ch != nil && !found; ch = ch.NextSibling {
		if hardHidden(ch) || ch.Type != html.ElementNode {
			continue
		}
		semanticArticle := strings.EqualFold(ch.Data, "article") &&
			!elementContainsAny(ch, "card", "comment", "reply")
		// WordPress and several other publishing systems predate widespread use
		// of <article>. Their conventional *-content wrappers are equivalent
		// evidence that this subtree contains the primary article body. Inspect
		// attributes in place to avoid constructing a token string for every node.
		conventionalArticleBody := elementContainsAny(ch, "entry", "post", "article") &&
			elementContainsAny(ch, "content") &&
			!elementContainsAny(ch, "comment", "reply")
		if semanticArticle || conventionalArticleBody {
			found = true
			break
		}
		found = a.hasArticleBodyDescendant(ch)
	}
	state := a.nodeStates[root]
	state.articleDescendant = 1
	if found {
		state.articleDescendant = 2
	}
	a.nodeStates[root] = state
	return found
}

// isTrailingArticleCardRegion catches unlabeled recommendation and newsletter
// grids after an article. Their summaries can contain enough prose to defeat
// ordinary boilerplate penalties. Requiring multiple explicitly marked cards
// and an earlier/containing semantic article avoids treating a single useful
// card or a listing page as auxiliary content.
func hasNonCardArticleAncestor(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, "article") && !elementContainsAny(p, "card") {
			return true
		}
	}
	return false
}

func (a *analysis) isTrailingArticleCardRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "div", "section", "aside", "ul":
	default:
		return false
	}
	if a.articleCardCount(n) < 2 {
		return false
	}
	// A layout wrapper can contain both the article body and a final card grid.
	// The cards are still classified when their narrower region is visited;
	// marking the shared wrapper would make every selected prose block vanish
	// through hasIrrelevantAncestor.
	if hasSubstantiveContentBeforeDescendant(n, isMarkedArticleCard) {
		return false
	}
	return hasSemanticArticleBeforeOrAround(n)
}

func isFormElement(n *html.Node) bool {
	return n != nil && n.Type == html.ElementNode && strings.EqualFold(n.Data, "form")
}

func isMarkedCard(n *html.Node) bool {
	return n != nil && n.Type == html.ElementNode && elementContainsAny(n, "card")
}

func isMarkedArticleCard(n *html.Node) bool {
	if !isMarkedCard(n) {
		return false
	}
	return strings.EqualFold(n.Data, "article") || elementContainsAny(n, "article", "post", "story", "newsletter")
}

// hasSubstantiveContentBeforeDescendant protects a shared ancestor from tail
// classification. The target must be a proper descendant, and prose must occur
// before it in document order; prose inside the promotional target therefore
// cannot protect the target itself.
func hasSubstantiveContentBeforeDescendant(root *html.Node, target func(*html.Node) bool) bool {
	if root == nil {
		return false
	}
	paragraphs, chars, longest := 0, 0, 0
	foundTarget := false
	walk(root, func(n *html.Node) bool {
		if foundTarget || hardHidden(n) {
			return false
		}
		if n != root && target(n) {
			foundTarget = true
			return false
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "p") {
			length := utf8.RuneCountInString(normalizeText(nodeText(n)))
			paragraphs++
			chars += length
			if length > longest {
				longest = length
			}
			return false
		}
		return true
	})
	return foundTarget && (longest >= 120 || (paragraphs >= 2 && chars >= 120))
}

func isPromotionalCardRegion(n *html.Node) bool {
	if elementContainsAny(n, "promo", "promotion", "promotions", "promotional", "related", "recommended", "recommendations") {
		return true
	}
	return isArticleAuxiliaryLabel(firstRegionHeading(n))
}

func countMarkedCards(root *html.Node, limit int) int {
	count := 0
	var visit func(*html.Node)
	visit = func(parent *html.Node) {
		for ch := parent.FirstChild; ch != nil && count < limit; ch = ch.NextSibling {
			if hardHidden(ch) || ch.Type != html.ElementNode {
				continue
			}
			if elementContainsAny(ch, "card") {
				count++
				continue
			}
			visit(ch)
		}
	}
	visit(root)
	return count
}

// articleCardCount returns the number of top-level marked article cards in a
// subtree, capped at two. Caching turns repeated candidate-region checks from
// overlapping subtree walks into one bottom-up pass.
func (a *analysis) articleCardCount(root *html.Node) int {
	if root == nil || hardHidden(root) {
		return 0
	}
	if cached := a.nodeStates[root].articleCardCount; cached != 0 {
		return int(cached - 1)
	}
	count := 0
	for ch := root.FirstChild; ch != nil && count < 2; ch = ch.NextSibling {
		if hardHidden(ch) || ch.Type != html.ElementNode {
			continue
		}
		if isMarkedArticleCard(ch) {
			count++
			continue // Do not count nested wrappers belonging to the same card.
		}
		count += a.articleCardCount(ch)
		if count > 2 {
			count = 2
		}
	}
	state := a.nodeStates[root]
	state.articleCardCount = uint8(count + 1)
	a.nodeStates[root] = state
	return count
}

func hasSemanticArticleBeforeOrAround(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, "article") && !elementContainsAny(p, "card") {
			return true
		}
	}
	// At each ancestor level, previous siblings are entirely before n in
	// document order. Search them for the primary semantic article.
	for branch := n; branch != nil && branch.Parent != nil; branch = branch.Parent {
		for sibling := branch.PrevSibling; sibling != nil; sibling = sibling.PrevSibling {
			found := false
			walk(sibling, func(x *html.Node) bool {
				if found || hardHidden(x) {
					return false
				}
				if x.Type == html.ElementNode && strings.EqualFold(x.Data, "article") && !elementContainsAny(x, "card") {
					found = true
					return false
				}
				return true
			})
			if found {
				return true
			}
		}
	}
	return false
}

func (a *analysis) hasMicrodataArticleRecordAncestor(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if a.microdataArticleRecords[p] {
			return true
		}
	}
	return false
}

func (a *analysis) hasIrrelevantAncestor(n *html.Node) bool {
	if n == nil {
		return false
	}
	// Classification rules apply only to elements. Avoid growing the shared
	// memoization map with every text and comment node during conversion.
	if n.Type != html.ElementNode {
		return a.hasIrrelevantAncestor(n.Parent)
	}
	if cached := a.nodeStates[n].irrelevantAncestor; cached != 0 {
		return cached == 2
	}
	irrelevant := a.isIrrelevantNode(n) || a.hasIrrelevantAncestor(n.Parent)
	state := a.nodeStates[n] // isIrrelevantNode may have updated the entry.
	state.irrelevantAncestor = 1
	if irrelevant {
		state.irrelevantAncestor = 2
	}
	a.nodeStates[n] = state
	return irrelevant
}

func leadingRegionHasAuxiliaryHeading(n *html.Node, limit int) bool {
	if n == nil || limit <= 0 {
		return false
	}
	budget, headings, stopped, found := 64, 0, false, false
	var visit func(*html.Node)
	visit = func(parent *html.Node) {
		for ch := parent.FirstChild; ch != nil && budget > 0 && !stopped && headings < limit && !found; ch = ch.NextSibling {
			if hardHidden(ch) || ch.Type == html.CommentNode {
				continue
			}
			if ch.Type == html.TextNode {
				if strings.TrimSpace(ch.Data) != "" {
					stopped = true
				}
				continue
			}
			if ch.Type != html.ElementNode {
				continue
			}
			budget--
			tag := strings.ToLower(ch.Data)
			if isHeadingTag(tag) {
				headings++
				found = auxiliaryLabels[normalizedLabel(nodeText(ch))]
				continue
			}
			if isBlockTag(tag) && normalizeText(nodeText(ch)) != "" {
				stopped = true
				continue
			}
			visit(ch)
		}
	}
	visit(n)
	return found
}

func firstRegionHeading(n *html.Node) string {
	// Inspect the first content-bearing element in document order, including
	// headings inside transparent layout wrappers. A heading that follows body
	// text or belongs to a nested semantic region does not label the parent.
	budget := 64
	var find func(*html.Node) (string, bool)
	find = func(parent *html.Node) (string, bool) {
		for ch := parent.FirstChild; ch != nil && budget > 0; ch = ch.NextSibling {
			if hardHidden(ch) {
				continue
			}
			if ch.Type == html.TextNode {
				if strings.TrimSpace(ch.Data) != "" {
					return "", true
				}
				continue
			}
			if ch.Type != html.ElementNode {
				continue
			}
			budget--
			tag := strings.ToLower(ch.Data)
			if isHeadingTag(tag) {
				return normalizedLabel(nodeText(ch)), true
			}
			if isRegionBoundary(tag) || isBlockTag(tag) {
				return "", true
			}
			// A generic child with siblings can be an independent region (for
			// example, a div-based sidebar). Do not let its heading label the
			// shared parent layout. Within a semantic region, however, a div
			// containing only a heading is a transparent title wrapper.
			if !isOnlyContentChild(parent, ch) {
				if tag == "div" && headerLabelsRegion(parent) {
					if heading, ok := headingOnlyWrapper(ch); ok {
						return heading, true
					}
				}
				if tag != "header" || !headerLabelsRegion(parent) {
					return "", true
				}
			}
			if heading, done := find(ch); done {
				return heading, true
			}
		}
		return "", false
	}
	heading, _ := find(n)
	return heading
}

func isRegionBoundary(tag string) bool {
	switch tag {
	case "article", "aside", "main", "nav", "section":
		return true
	}
	return false
}

func isOnlyContentChild(parent, child *html.Node) bool {
	for ch := parent.FirstChild; ch != nil; ch = ch.NextSibling {
		if hardHidden(ch) || ch.Type == html.CommentNode || (ch.Type == html.TextNode && strings.TrimSpace(ch.Data) == "") {
			continue
		}
		if ch != child {
			return false
		}
	}
	return true
}

func headerLabelsRegion(parent *html.Node) bool {
	if parent == nil || parent.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(parent.Data) {
	case "aside", "section":
		return true
	}
	return false
}

func headingOnlyWrapper(n *html.Node) (string, bool) {
	for n != nil && n.Type == html.ElementNode {
		if isHeadingTag(strings.ToLower(n.Data)) {
			return normalizedLabel(nodeText(n)), true
		}
		var only *html.Node
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			if hardHidden(ch) || ch.Type == html.CommentNode || (ch.Type == html.TextNode && strings.TrimSpace(ch.Data) == "") {
				continue
			}
			if only != nil || ch.Type != html.ElementNode {
				return "", false
			}
			only = ch
		}
		n = only
	}
	return "", false
}

func isHeadingTag(tag string) bool {
	return len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6'
}

func normalizedLabel(s string) string {
	s = strings.ToLower(normalizeText(s))
	return strings.Trim(s, " .:;!?–—-\u00a0")
}

func articleURLPath(path string) bool {
	parts := strings.FieldsFunc(strings.ToLower(path), func(r rune) bool { return r == '/' })
	for i, part := range parts {
		if i+1 < len(parts) && (part == "blog" || part == "article" || part == "articles" || part == "posts") {
			return true
		}
		if i+2 < len(parts) && len(part) == 4 && len(parts[i+1]) == 2 && allASCIIDigits(part) && allASCIIDigits(parts[i+1]) {
			return true
		}
	}
	return false
}

func allASCIIDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func nearestTokenAncestor(n *html.Node, values ...string) *html.Node {
	for p := n; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && elementContainsAny(p, values...) {
			return p
		}
	}
	return nil
}

// primaryDiscussionContext recognizes explicit page-level thread structure.
// It intentionally examines only the main container (or body when no main is
// present), rather than inheriting tokens from every block ancestor.
