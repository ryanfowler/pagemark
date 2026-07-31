package engine

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/dom"
	"github.com/ryanfowler/pagemark/internal/urlutil"
	"golang.org/x/net/html"
)

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
			state.semanticBefore.store(seen)
			a.nodeStates[x] = state
			if strings.EqualFold(x.Data, "article") && !elementContainsAny(x, "card") {
				seen = true
			}
			return true
		})
	}
	value, _ := a.nodeStates[n].semanticBefore.value()
	return value
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
			state.semanticAfter.store(seen)
			a.nodeStates[x] = state
			if strings.EqualFold(x.Data, "article") && !elementContainsAny(x, "card") {
				seen = true
			}
		})
	}
	value, _ := a.nodeStates[n].semanticAfter.value()
	return value
}

func (a *analysis) hasSelfReference(root *html.Node) (result bool) {
	if root == nil || hardHidden(root) {
		return false
	}
	if value, known := a.nodeStates[root].selfReference.value(); known {
		return value
	}
	defer func() {
		state := a.nodeStates[root]
		state.selfReference.store(result)
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
	if !urlutil.IsHierarchicalHTTP(u) {
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
	if hasCompactClass(n, "relatedarticles", "recommendedarticles", "relatedcontent") {
		return countLinkedRecords(n, 2) >= 2
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
		strings.HasPrefix(heading, "more stories ") || strings.HasPrefix(heading, "more from ") ||
		strings.HasPrefix(heading, "you may also ")
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
		if x != n && (tag == "p" || tag == "blockquote" || tag == "pre") && normalizedTextAtLeast(x, 1) {
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

// isTrailingOrganizationProfileRegion identifies a separately headed company
// profile appended to an article. "About Us" by itself is deliberately not an
// auxiliary label: it is excluded only when trailing structure and at least two
// independent organization-profile signals agree.
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
	if u.Scheme != "" && !urlutil.IsHierarchicalHTTP(u) {
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
			if (tag == "p" || tag == "li" || isHeadingTag(tag)) && normalizedTextAtLeast(x, 1) {
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
			if (tag == "p" || tag == "li" || isHeadingTag(tag)) && normalizedTextAtLeast(x, 1) {
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
	// Most broad layout containers either have no heading or contain ordinary
	// article prose. Reject them before walking every descendant interaction:
	// link-label normalization is substantially more expensive than either
	// disqualifying check on pages with large navigation or product trees.
	if heading == "" || regionHasLongProse(n, 180) {
		return false
	}
	interactions, links := marketingInteractions(n)
	if interactions == 0 {
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
			if marketingInteractionLabel(x) {
				interactions++
			}
			return false
		}
		return true
	})
	return interactions, links
}

// marketingInteractionLabel recognizes the short normalized labels used by
// marketingInteractions without retaining an arbitrarily large linked subtree.
// Leading and trailing punctuation/space are ignored exactly as normalizedLabel
// ignores them, including when the label is split across inline elements.
func marketingInteractionLabel(n *html.Node) bool {
	s := normalizedLabelScanner{}
	s.scan(n)
	label := string(s.prefix[:s.prefixLen])
	return strings.HasPrefix(label, "get ") || strings.HasPrefix(label, "start ") ||
		strings.HasPrefix(label, "connect ") || strings.HasPrefix(label, "apply") ||
		strings.HasPrefix(label, "register") || strings.HasPrefix(label, "sign up") ||
		s.total == len("learn more") && label == "learn more" ||
		s.total == len("contact us") && label == "contact us"
}

type normalizedLabelScanner struct {
	prefix                   [16]byte
	prefixLen, total         int
	pending                  [16]byte
	pendingLen               int
	pendingOverflow, started bool
}

func (s *normalizedLabelScanner) scan(n *html.Node) {
	if n == nil || dom.Hidden(n) {
		return
	}
	if n.Type == html.TextNode {
		if s.started {
			s.queueTrim(' ')
		}
		for text := n.Data; text != ""; {
			r, size := utf8.DecodeRuneInString(text)
			text = text[size:]
			if labelTrimRune(r) {
				if s.started {
					s.queueTrim(r)
				}
				continue
			}
			s.flushPending()
			s.started = true
			r = unicode.ToLower(r)
			var encoded [utf8.UTFMax]byte
			n := utf8.EncodeRune(encoded[:], r)
			for _, c := range encoded[:n] {
				s.add(c)
			}
		}
		return
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		s.scan(ch)
	}
}

func labelTrimRune(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(".:;!?–—-", r)
}

func (s *normalizedLabelScanner) queueTrim(r rune) {
	if unicode.IsSpace(r) {
		if s.pendingLen > 0 && s.pending[s.pendingLen-1] == ' ' {
			return
		}
		r = ' '
	}
	var encoded [utf8.UTFMax]byte
	n := utf8.EncodeRune(encoded[:], r)
	if s.pendingLen+n > len(s.pending) {
		s.pendingOverflow = true
		return
	}
	s.pendingLen += copy(s.pending[s.pendingLen:], encoded[:n])
}

func (s *normalizedLabelScanner) flushPending() {
	for _, c := range s.pending[:s.pendingLen] {
		s.add(c)
	}
	if s.pendingOverflow {
		s.total += len(s.prefix) + 1
	}
	s.pendingLen = 0
	s.pendingOverflow = false
}

func (s *normalizedLabelScanner) add(c byte) {
	if s.prefixLen < len(s.prefix) {
		s.prefix[s.prefixLen] = c
		s.prefixLen++
	}
	s.total++
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
				state.articleProseBefore.store(seen)
				a.nodeStates[x] = state
			}
			if strings.EqualFold(x.Data, "p") &&
				normalizedTextAtLeast(x, 100) {
				seen = true
			}
			return true
		})
	}
	value, _ := a.nodeStates[n].articleProseBefore.value()
	return value
}

func regionHasLongProse(n *html.Node, limit int) bool {
	found := false
	walk(n, func(x *html.Node) bool {
		if found || hardHidden(x) {
			return false
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "p") &&
			normalizedTextAtLeast(x, limit) {
			found = true
			return false
		}
		return true
	})
	return found
}
