package engine

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Other structural names need navigational evidence because they are also
// common documentation subjects.
var navigationStructureTokens = []string{"breadcrumb", "pagination", "toolbar"}

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

func isConventionallyNamedNavigation(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	// Check the cheap class-name condition first. Most elements are not named as
	// navigation, so they must not pay for a complete subtree evidence scan.
	named := hasCompactClass(n, "breadcrumb", "breadcrumbs")
	if !named {
		for class := range strings.FieldsSeq(strings.ToLower(attrValue(n, "class"))) {
			class = strings.Trim(class, "_-")
			if strings.Contains(class, "breadcrumb") || class == "nav" || class == "navbar" ||
				strings.HasPrefix(class, "nav-") || strings.HasPrefix(class, "nav_") ||
				strings.HasSuffix(class, "-nav") || strings.HasSuffix(class, "_nav") {
				named = true
				break
			}
		}
	}
	return named && hasNavigationShape(n)
}

// Image-only headings linked to the site root are publication wordmarks, not
// article headings. Requiring the heading, home link, and image-only shape
// leaves linked article headings and ordinary figures unaffected.
func isLinkedImageMasthead(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !isHeadingTag(strings.ToLower(n.Data)) || normalizedTextAtLeast(n, 1) {
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
	textLength, linkedLength, controlCount := subtreeShapeEvidence(n)
	return textLength > 0 && float64(linkedLength)/float64(textLength) >= .6 || controlCount > 1
}

// isBreadcrumbLike covers older CMS and wiki templates that emit a plain
// paragraph of breadcrumb links without a breadcrumb class. Requiring several
// links, separator punctuation, and high link density keeps citation-heavy
// authored paragraphs from being treated as navigation.
func isBreadcrumbLike(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !strings.EqualFold(n.Data, "p") {
		return false
	}
	text := normalizeText(nodeText(n))
	if !strings.ContainsAny(text, "|›»") || utf8.RuneCountInString(text) == 0 ||
		float64(linkTextLength(n))/float64(utf8.RuneCountInString(text)) < .75 ||
		!leadingNavigationParagraph(n) {
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
	// Classed breadcrumb containers are handled above. An unmarked paragraph
	// needs stronger evidence because a short authored paragraph can look like
	// "Documentation | Guides | API".
	return links >= 4
}

func leadingNavigationParagraph(n *html.Node) bool {
	for sibling := n.PrevSibling; sibling != nil; sibling = sibling.PrevSibling {
		if sibling.Type == html.CommentNode || sibling.Type == html.TextNode && strings.TrimSpace(sibling.Data) == "" {
			continue
		}
		// Wiki and legacy CMS templates commonly place the breadcrumb directly
		// after an explicitly marked infobox or metadata table. A bare leading
		// paragraph is not enough evidence: it may be an authored resource index.
		return sibling.Type == html.ElementNode && strings.EqualFold(sibling.Data, "table") &&
			elementContainsAny(sibling, "infobox", "metadata")
	}
	return false
}

// isTaxonomyLinkParagraph recognizes a standalone tag list. Requiring every
// non-whitespace child to be a hashtag link avoids removing an authored sentence
// that happens to link to a tag.
func isTaxonomyLinkParagraph(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !strings.EqualFold(n.Data, "p") {
		return false
	}
	found := false
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			if strings.TrimSpace(child.Data) != "" {
				return false
			}
			continue
		}
		if child.Type != html.ElementNode || !strings.EqualFold(child.Data, "a") ||
			!strings.HasPrefix(normalizedLabel(nodeText(child)), "#") {
			return false
		}
		found = true
	}
	return found
}

// isArticleNavigationControl handles the common blog shape where previous and
// next links are placed in an ordinary paragraph instead of a nav element.
// The class is stronger evidence than the short link label, so authored prose
// containing words such as "next" remains untouched.
func isArticleNavigationControl(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !strings.EqualFold(n.Data, "a") {
		return false
	}
	return hasClassConvention(n, "previous-post") || hasClassConvention(n, "next-post") ||
		hasClassConvention(n, "prev-post")
}

// isFilterControlRegion identifies interactive result filters without treating
// a documentation section merely named "Filter" as boilerplate. A structural
// filter marker must also contain a real form control.
func isFilterControlRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || strings.EqualFold(n.Data, "main") || strings.EqualFold(n.Data, "article") ||
		!hasFilterStructureMarker(n) {
		return false
	}
	// A generic filters wrapper can contain the whole result page. Only remove
	// control-only regions; primary containers and substantive result records
	// must remain available to selection.
	return controls(n) > 0 && !hasPrimaryContentDescendant(n)
}

func hasPrimaryContentDescendant(n *html.Node) bool {
	found := false
	walk(n, func(current *html.Node) bool {
		if current == n || current.Type != html.ElementNode {
			return true
		}
		tag := strings.ToLower(current.Data)
		if tag == "main" || tag == "article" {
			found = true
			return false
		}
		if !hasFormAncestor(current) {
			if isListingRecordElement(current) &&
				(normalizedTextAtLeast(current, 40) || hasResultLink(current)) {
				found = true
				return false
			}
			if isRecognizedResultContainer(current) && hasResultLink(current) {
				found = true
				return false
			}
			if substantiveResultRegion(current) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func isRecognizedResultContainer(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "div", "section", "ul", "ol":
		return elementContainsAny(n, "results", "listings", "listing")
	default:
		return false
	}
}

func hasResultLink(n *html.Node) bool {
	found := false
	walk(n, func(current *html.Node) bool {
		if current.Type != html.ElementNode {
			return true
		}
		if current != n && strings.EqualFold(current.Data, "form") {
			return false
		}
		if strings.EqualFold(current.Data, "a") && strings.TrimSpace(attrValue(current, "href")) != "" &&
			normalizedTextAtLeast(current, 1) {
			found = true
			return false
		}
		return true
	})
	return found
}

func substantiveResultRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(n.Data)
	if tag != "div" && tag != "section" && tag != "li" {
		return false
	}
	heading, prose := false, false
	walk(n, func(current *html.Node) bool {
		if current.Type != html.ElementNode {
			return true
		}
		if current != n && strings.EqualFold(current.Data, "form") {
			return false
		}
		if isHeadingTag(strings.ToLower(current.Data)) && normalizedTextAtLeast(current, 1) {
			heading = true
		}
		if (strings.EqualFold(current.Data, "p") || strings.EqualFold(current.Data, "blockquote")) &&
			normalizedTextAtLeast(current, 20) {
			prose = true
		}
		return !(heading && prose)
	})
	return heading && prose
}

func hasFormAncestor(n *html.Node) bool {
	for parent := n.Parent; parent != nil; parent = parent.Parent {
		if parent.Type == html.ElementNode && strings.EqualFold(parent.Data, "form") {
			return true
		}
	}
	return false
}

func hasFilterStructureMarker(n *html.Node) bool {
	conventions := [...]string{"filters", "filter-section", "filter-panel", "filter-group", "filter-controls", "filter-form", "refine-results"}
	for class := range strings.FieldsSeq(attrValue(n, "class")) {
		class = strings.ToLower(strings.Trim(class, "_- "))
		for _, convention := range conventions {
			if class == convention || strings.HasPrefix(class, convention+"--") ||
				strings.HasPrefix(class, convention+"__") || strings.Contains(class, "-"+convention) {
				return true
			}
		}
	}
	id := strings.ToLower(strings.TrimSpace(attrValue(n, "id")))
	switch id {
	case "filters", "filter-section", "filter-panel", "filter-controls", "filter-form", "refine-results":
		return true
	}
	return false
}

// isMastheadRegion recognizes a publisher masthead that is marked in the DOM
// and contains visual branding or a header control. The marker is intentionally
// required: a legitimate article section can discuss logos or headers.
func isMastheadRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !elementContainsAny(n, "masthead") {
		return false
	}
	// Article templates often call their hero/header block an article-masthead.
	// Preserve it when the DOM identifies authored story content, rather than
	// letting the generic masthead rule remove the title and standfirst.
	if mastheadContainsAuthoredContent(n) {
		return false
	}
	visualOrControl := false
	walk(n, func(x *html.Node) bool {
		if x.Type != html.ElementNode {
			return true
		}
		switch strings.ToLower(x.Data) {
		case "img", "form", "button", "input", "select", "textarea":
			visualOrControl = true
		}
		return !visualOrControl
	})
	return visualOrControl
}

func mastheadContainsAuthoredContent(n *html.Node) bool {
	// A heading alone is not enough: publisher mastheads commonly contain a
	// textual site name in an h1. Require an article/story marker or an
	// explicit schema.org headline property before treating the masthead as
	// authored content.
	if elementContainsAny(n, "article", "story", "post", "entry", "standfirst", "dek") {
		return true
	}
	foundHeadline := false
	walk(n, func(current *html.Node) bool {
		if current.Type != html.ElementNode {
			return true
		}
		for _, prop := range strings.Fields(attrValue(current, "itemprop")) {
			if strings.EqualFold(prop, "headline") {
				foundHeadline = true
				return false
			}
		}
		return true
	})
	return foundHeadline
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
		if strings.EqualFold(x.Data, "p") && normalizedTextAtLeast(x, 1) {
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
