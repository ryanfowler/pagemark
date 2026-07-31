package engine

import (
	"strings"

	"golang.org/x/net/html"
)

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

func (a *analysis) hasMicrodataArticleRecordAncestor(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if a.microdataArticleRecords[p] {
			return true
		}
	}
	return false
}

// Auxiliary classification applies policy in this order:
//  1. Base exclusions.
//  2. Page-type-specific exclusions.
//  3. Trailing or repeated auxiliary-region exclusions.
func (a *analysis) isIrrelevantNode(n *html.Node) bool {
	if value, known := a.nodeStates[n].irrelevant.value(); known {
		return value
	}
	irrelevant := a.baseAuxiliaryNode(n)
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
		if value, known := a.nodeStates[p].inferenceAuxiliary.value(); known {
			a.cacheInferenceAuxiliaryPath(n, p, value)
			return value
		}
		auxiliary := a.baseAuxiliaryNode(p)
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
			a.cacheInferenceAuxiliaryPath(n, p, true)
			return true
		}
	}
	a.cacheInferenceAuxiliaryPath(n, nil, false)
	return false
}

// cacheInferenceAuxiliaryPath avoids allocating a temporary ancestor slice on
// every query. The second parent walk is cheap and only occurs on cache misses.
func (a *analysis) cacheInferenceAuxiliaryPath(n, end *html.Node, value bool) {
	for p := n; p != nil; p = p.Parent {
		state := a.nodeStates[p]
		state.inferenceAuxiliary.store(value)
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

func (a *analysis) conventionalArticleBodyAncestor(n *html.Node) *html.Node {
	for p := n; p != nil; p = p.Parent {
		// Classification needs a narrower signal than structural article-body
		// protection. Generic post/content tokens are common on forum wrappers,
		// and post-content commonly names an opening message.
		publicationBody := a.isPublicationArticleContent(p)
		if publicationBody && a.commentRecordCount(p) < 2 && !a.inferenceAuxiliaryBlock(p) {
			return p
		}
	}
	return nil
}

func (a *analysis) isPublicationArticleContent(n *html.Node) bool {
	return n != nil && (hasCompactClass(n, "entrycontent", "articlecontent") || a.isPublicationPostContent(n))
}

// isPublicationPostContent disambiguates the widely shared post-content class.
// A lone opening forum message is not article structure, while a substantial
// local prose scope backed by publication metadata is.
func (a *analysis) isPublicationPostContent(n *html.Node) bool {
	return n != nil && hasCompactClass(n, "postcontent") &&
		(a.meta.articleType || a.meta.articlePublished || a.meta.headline) &&
		a.substantialArticleScope(n) && !a.hasSurroundingDiscussionRecords(n)
}

// hasSurroundingDiscussionRecords distinguishes a publisher body from a
// substantial forum opening post. The replies are normally siblings of the
// post or of one of its wrappers, so counting only descendants of post-content
// misses exactly the evidence needed for this decision.
func (a *analysis) hasSurroundingDiscussionRecords(n *html.Node) bool {
	count := 0
	for branch := n; branch != nil && branch.Parent != nil; branch = branch.Parent {
		for sibling := branch.Parent.FirstChild; sibling != nil && count < 2; sibling = sibling.NextSibling {
			if sibling == branch || sibling.Type != html.ElementNode || hardHidden(sibling) {
				continue
			}
			count += a.commentRecordCount(sibling)
		}
		if count >= 2 {
			return true
		}
		if branch.Parent.Type == html.ElementNode &&
			(strings.EqualFold(branch.Parent.Data, "main") || strings.EqualFold(branch.Parent.Data, "body")) {
			break
		}
	}
	return false
}

func isConventionalArticleBody(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || elementContainsAny(n, "comment", "reply") {
		return false
	}
	return elementContainsAny(n, "entry", "post", "article") && elementContainsAny(n, "content") ||
		hasCompactClass(n, "entrycontent", "postcontent", "articlecontent")
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

func (a *analysis) articleAuxiliaryNode(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if value, known := a.nodeStates[n].articleAuxiliary.value(); known {
		return value
	}
	auxiliary := a.articleAuxiliaryNodeUncached(n)
	state := a.nodeStates[n]
	state.articleAuxiliary.store(auxiliary)
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
		itemtype := strings.ToLower(attrValue(n, "itemtype"))
		// Author profiles commonly precede the article in a sidebar. Microformats
		// use h-card while schema.org uses Person; neither is article content when
		// the profile sits outside the semantic article.
		personProfile := containsAny(itemtype, "person") || elementContainsAny(n, "h-card")
		if !hasNonCardArticleAncestor(n) && (personProfile ||
			(tag == "aside" && elementContainsAny(n, "author", "byline", "bio", "profile"))) {
			return true
		}
		if isRelatedCardRegion(n) && !a.hasArticleBodyDescendant(n) &&
			!hasSubstantiveContentBeforeDescendant(n, isMarkedCard) {
			return true
		}
		if !a.hasArticleBodyDescendant(n) &&
			(hasAuxiliaryHeading(n) || hasDeepLeadingAuxiliaryHeading(n)) && countLinkedRecords(n, 2) >= 2 {
			// Do not classify a shared article wrapper from a toolbar or a trailing
			// recommendation heading. Its narrower auxiliary children are visited
			// independently. Broad “Recommended …” and “Related …” labels are common
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
