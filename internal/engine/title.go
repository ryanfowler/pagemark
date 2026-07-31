package engine

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/markdown"
	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

func (a *analysis) separateDocumentTitle(nodes []*html.Node, cfg markdown.Config, pageType PageType, authored bool) ([]*html.Node, string) {
	resolved := nodes
	if !authored {
		// A title no longer consumes output budget. Disable the renderer limit for
		// title recovery so the old title/body fit checks cannot suppress metadata.
		titleCfg := cfg
		titleCfg.MaxBytes = 0
		resolved = a.ensureDocumentTitle(nodes, titleCfg, pageType)
	}

	heading := a.leadingSelectedHeading(resolved, cfg)
	metadataTitle := a.restorationTitle()
	if pageType != PageTypeArticle && !authored {
		allowListingH1 := pageType != PageTypeListing && pageType != PageTypeCollection || a.meta.browserTitle != "" || a.meta.socialTitle != ""
		if sourceH1 := a.firstSelectedSourceH1(resolved, cfg); sourceH1 != nil && allowListingH1 {
			// Prefer an authored source h1 over a synthetic metadata heading. This
			// also removes browser branding when metadata lacks a site-name field.
			heading = sourceH1
		}
		// A discussion classification can include both an article and its reader
		// responses. When the browser title identifies a later selected h1 after
		// removing hostname-derived branding, prefer that document heading over
		// an earlier site masthead.
		if pageType == PageTypeDiscussion && a.meta.socialTitle == "" {
			browserContentTitle := a.cleanedMetadataTitle(a.meta.browserTitle)
			if browserContentTitle != "" && normalizedLabel(browserContentTitle) != normalizedLabel(a.meta.browserTitle) {
				if sourceH1 := a.firstSelectedEquivalentHeading(resolved, cfg, browserContentTitle); sourceH1 != nil {
					heading = sourceH1
					metadataTitle = browserContentTitle
				}
			}
		}
	}
	if heading != nil && pageType != PageTypeArticle && !authored {
		headingTitle := articleHeadingText(heading)
		// Non-article pages often place navigation or a tool heading before the
		// actual h1. A leading heading is authoritative only when it agrees with
		// metadata; otherwise prefer the first selected h1 below.
		if metadataTitle != "" && !titleEquivalent(headingTitle, metadataTitle, a.meta.site) {
			heading = nil
		}
	}
	if heading == nil && metadataTitle != "" {
		heading = a.firstSelectedEquivalentHeading(resolved, cfg, metadataTitle)
	}
	if heading == nil && (pageType != PageTypeListing && pageType != PageTypeCollection || a.meta.browserTitle != "" || a.meta.socialTitle != "") {
		heading = a.firstSelectedH1(resolved, cfg)
	}
	if heading == nil {
		return resolved, ""
	}
	title := normalizeText(articleHeadingText(heading))
	if title == "" {
		return resolved, ""
	}
	// H1 is the only independently authoritative heading level. Lower-level
	// headings are normally sections and require metadata agreement or an
	// explicit article-headline marker before they can be separated as a title.
	if !strings.EqualFold(heading.Data, "h1") &&
		(metadataTitle == "" || !titleEquivalent(title, metadataTitle, a.meta.site)) &&
		!hasArticleHeadlineMarker(heading) {
		return resolved, ""
	}
	// A metadata-less listing or collection often begins with the h1 of its
	// first record. Retain that record heading and suppress the heading-derived
	// metadata fallback. A page-level h1 outside any record remains authoritative.
	if (pageType == PageTypeListing || pageType == PageTypeCollection) &&
		a.meta.browserTitle == "" && a.meta.socialTitle == "" && a.listingHeadingIsRecord(heading) {
		a.suppressHeadingTitle = true
		return resolved, ""
	}

	content := make([]*html.Node, 0, len(resolved))
	for _, root := range resolved {
		if root == heading {
			continue
		}
		content = append(content, root)
	}
	if a.titleExcluded == nil {
		a.titleExcluded = make(map[*html.Node]bool)
	}
	if len(content) == len(resolved) {
		// The title is nested in a selected container. Mark only that heading as
		// excluded; ExtractNode's caller-owned tree is never changed.
		a.titleExcluded[heading] = true
		a.overrideIrrelevant(heading, true)
	}
	// Title recovery can prepend a synthetic heading while an equivalent source
	// heading remains inside a selected ancestor. Exclude that source copy too;
	// equivalent prose is deliberately preserved.
	a.contentTitle = title
	for _, root := range content {
		walk(root, func(n *html.Node) bool {
			if n.Type == html.ElementNode && isHeadingTag(strings.ToLower(n.Data)) &&
				titleEquivalent(articleHeadingText(n), title, a.meta.site) {
				a.titleExcluded[n] = true
				a.overrideIrrelevant(n, true)
				return false
			}
			return true
		})
	}
	return content, title
}

func (a *analysis) firstSelectedEquivalentHeading(nodes []*html.Node, cfg markdown.Config, title string) *html.Node {
	var found *html.Node
	for _, root := range nodes {
		walk(root, func(n *html.Node) bool {
			if found != nil || hardHidden(n) || (cfg.Exclude != nil && cfg.Exclude(n)) {
				return false
			}
			if n.Type == html.ElementNode && isHeadingTag(strings.ToLower(n.Data)) &&
				titleEquivalent(articleHeadingText(n), title, a.meta.site) {
				found = n
				return false
			}
			return true
		})
		if found != nil {
			return found
		}
	}
	return nil
}

func (a *analysis) firstSelectedSourceH1(nodes []*html.Node, cfg markdown.Config) *html.Node {
	var found *html.Node
	for _, root := range nodes {
		walk(root, func(n *html.Node) bool {
			if found != nil || hardHidden(n) || (cfg.Exclude != nil && cfg.Exclude(n)) {
				return false
			}
			if n.Type == html.ElementNode && strings.EqualFold(n.Data, "h1") && n.Parent != nil {
				found = n
				return false
			}
			return true
		})
		if found != nil {
			return found
		}
	}
	return nil
}

func (a *analysis) firstSelectedH1(nodes []*html.Node, cfg markdown.Config) *html.Node {
	var found *html.Node
	for _, root := range nodes {
		walk(root, func(n *html.Node) bool {
			if found != nil || hardHidden(n) || (cfg.Exclude != nil && cfg.Exclude(n)) {
				return false
			}
			if n.Type == html.ElementNode && strings.EqualFold(n.Data, "h1") {
				found = n
				return false
			}
			return true
		})
		if found != nil {
			return found
		}
	}
	return nil
}

// leadingSelectedHeading returns only a heading that precedes substantive
// selected prose. This avoids turning a later section heading into the title.
// Synthetic and reordered title roots are checked first because they are not
// present in the segmented source block index.
func (a *analysis) leadingSelectedHeading(nodes []*html.Node, cfg markdown.Config) *html.Node {
	var inspect func(*html.Node) (*html.Node, bool)
	inspect = func(n *html.Node) (*html.Node, bool) {
		if n == nil || hardHidden(n) || (cfg.Exclude != nil && cfg.Exclude(n)) {
			return nil, false
		}
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)
			if isHeadingTag(tag) {
				return n, true
			}
			// Publication furniture may precede a title. Other rendered blocks with
			// text establish that a later heading is a section, not the title.
			if isBlockTag(tag) && tag != "div" && tag != "main" && tag != "article" && tag != "section" && tag != "header" &&
				normalizedTextAtLeast(n, 1) && !isPublicationFurnitureBlock(n) {
				return nil, true
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if heading, stopped := inspect(child); stopped {
				return heading, true
			}
		}
		return nil, false
	}
	for _, root := range nodes {
		if heading, stopped := inspect(root); stopped {
			return heading
		}
	}
	for i := range a.blocks {
		b := &a.blocks[i]
		if !representedBySelection(b.node, nodes) || hardHidden(b.node) || a.hasIrrelevantAncestor(b.node) || (cfg.Exclude != nil && cfg.Exclude(b.node)) {
			continue
		}
		if isHeadingTag(b.kind) {
			return b.node
		}
		if isSubstantiveProseBlock(b) {
			return nil
		}
	}
	return nil
}

// ensureDocumentTitle restores titles according to the shape of the selected
// output. Articles retain the broader source-heading recovery below. Other
// classifications only receive a synthetic title when they still look like a
// single prose document; this covers prose pages misclassified by surrounding
// widgets without adding browser titles to collections or application shells.
func (a *analysis) ensureDocumentTitle(nodes []*html.Node, cfg markdown.Config, pageType PageType) []*html.Node {
	if pageType == PageTypeArticle {
		return a.ensureArticleTitle(nodes, cfg)
	}
	title := a.restorationTitle()
	// A prose page can be classified as generic when an old template has no
	// article element. If a later selected h1 exactly identifies the document,
	// do not let a site masthead before it remain the apparent title.
	if title != "" && a.hasDominantProseOutput(nodes, cfg) {
		for i := range a.blocks {
			b := &a.blocks[i]
			if b.kind != "h1" || !titleEquivalent(b.text, title, a.meta.site) || !representedBySelection(b.node, nodes) || adjacentSelectedBlockDistance(a.blocks, i, nodes, 2) == 0 {
				continue
			}
			content := removeSelectedNode(nodes, b.node)
			for j := 0; j < i; j++ {
				before := &a.blocks[j]
				if isHeadingTag(before.kind) && !titleEquivalent(before.text, title, a.meta.site) {
					content = removeSelectedNode(content, before.node)
				}
			}
			withTitle := append([]*html.Node{b.node}, content...)
			if a.titleLeavesOutputForArticleProse(withTitle, cfg) {
				return withTitle
			}
			return content
		}
	}
	structuredDocument := pageType == PageTypeDocumentation || pageType == PageTypeDiscussion
	if title == "" || a.hasEquivalentHeading(nodes, title, cfg) || a.hasLeadingOutputHeading(nodes, cfg) ||
		(!structuredDocument && !a.hasDominantProseOutput(nodes, cfg)) {
		return nodes
	}
	titleNode := articleTitleNode(title)
	a.overrideIrrelevant(titleNode, false)
	withTitle := append([]*html.Node{titleNode}, nodes...)
	if !a.titleLeavesOutputForArticleProse(withTitle, cfg) {
		return nodes
	}
	return withTitle
}

// ensureArticleTitle restores a source headline next to selected article
// content. Publishers sometimes use h2 for the article headline because a
// page-level h1 is reserved for the site or section name. Metadata remains the
// fallback when no nearby, well-supported source heading exists.
func (a *analysis) ensureArticleTitle(nodes []*html.Node, cfg markdown.Config) []*html.Node {
	// Use the same preferred, normalized metadata title for source-heading
	// selection and synthetic fallback. In particular, a site masthead matching
	// the browser title must not override a distinct social title.
	restorationTitle := a.restorationTitle()

	// Prefer the source heading over metadata. Looking only a small number of
	// segmented blocks away keeps headings elsewhere on the page from being
	// mistaken for the article title, while allowing publication metadata or a
	// byline between the heading and body.
	bestIndex, bestDistance := -1, 3
	bestEquivalent, bestRepresented, bestMarked, bestCredible := false, false, false, false
	for i := range a.blocks {
		b := &a.blocks[i]
		if (b.kind != "h1" && b.kind != "h2") || hardHidden(b.node) {
			continue
		}
		headingText := articleHeadingText(b.node)
		marked := hasArticleHeadlineMarker(b.node)
		equivalent := restorationTitle != "" && (titleEquivalent(headingText, restorationTitle, a.meta.site) ||
			marked && articleTitleVariantEquivalent(headingText, restorationTitle))
		composedBrowserTitle := a.meta.socialTitle == "" && a.headingComposesBrowserTitle(i, headingText)
		// Minimal publishing templates sometimes reserve h1 for the site name and
		// use an unmarked h2 for the story. A plain browser title then joins those
		// two visible headings ("Site - Story"). Treat that exact composition as
		// metadata agreement so intervening dates/bylines do not make the site
		// masthead win.
		if composedBrowserTitle {
			equivalent = true
		}
		// Article headers are often scored as page chrome because they contain a
		// byline and publication controls. An explicitly marked headline that
		// agrees with document metadata remains usable across a short run of that
		// furniture; weaker headings still obey the irrelevant-region score.
		strongDetachedHeadline := equivalent && (marked || composedBrowserTitle)
		if a.hasIrrelevantAncestor(b.node) && !strongDetachedHeadline {
			continue
		}
		distance := adjacentSelectedBlockDistance(a.blocks, i, nodes, 2)
		if distance == 0 && strongDetachedHeadline {
			distance = adjacentSelectedBlockDistance(a.blocks, i, nodes, 6)
			// Selected nodes may have been cloned while normalizing headings and
			// therefore cannot always be matched by pointer against the segmented DOM.
			// Containment in the semantic article supplies the structural tie instead.
			if distance == 0 {
				region := primaryHeadingRegion(b.node)
				if region != nil && strings.EqualFold(region.Data, "article") {
					distance = 3
				}
			}
		}
		if distance == 0 {
			// Proximity is required even with metadata agreement. Otherwise a
			// headline from a recommendation or another article could win.
			continue
		}
		represented := representedBySelection(b.node, nodes)
		credible := a.isCredibleArticleHeading(i, nodes)
		// A conflicting heading is authoritative only with independent structural
		// evidence. This prevents an adjacent site masthead from replacing the
		// metadata fallback. H2 also requires such evidence when metadata is absent;
		// proximity alone must not turn an ordinary section heading into a title.
		if (restorationTitle != "" && !equivalent && !credible) ||
			(b.kind == "h2" && !equivalent && (!credible || restorationTitle == "" && !marked)) {
			continue
		}
		// A source heading that agrees with the normalized metadata is the least
		// ambiguous choice. Only compare structural credibility after equivalence;
		// otherwise an internal marked heading can beat the real title merely
		// because both happen to be near selected prose.
		if bestIndex < 0 || (equivalent && !bestEquivalent) ||
			(equivalent == bestEquivalent && represented && !bestRepresented) ||
			(equivalent == bestEquivalent && represented == bestRepresented && marked && !bestMarked) ||
			(equivalent == bestEquivalent && represented == bestRepresented && marked == bestMarked && credible && !bestCredible) ||
			(equivalent == bestEquivalent && represented == bestRepresented && marked == bestMarked && credible == bestCredible && distance < bestDistance) {
			bestIndex, bestDistance, bestEquivalent, bestRepresented, bestMarked, bestCredible = i, distance, equivalent, represented, marked, credible
		}
	}
	if bestIndex >= 0 {
		candidate := &a.blocks[bestIndex]
		if candidate.kind == "h1" && representedBySelection(candidate.node, nodes) {
			// A source headline may follow an unmarked site masthead when the page
			// has no semantic article wrapper. Make the matching headline the
			// document root and discard only unsupported headings before it. Dates
			// and bylines remain, now correctly following the document title.
			directlySelected := false
			for _, n := range nodes {
				directlySelected = directlySelected || n == candidate.node
			}
			content := removeSelectedNode(nodes, candidate.node)
			for i := 0; i < bestIndex; i++ {
				b := &a.blocks[i]
				if isHeadingTag(b.kind) && !titleEquivalent(b.text, restorationTitle, a.meta.site) && !a.isCredibleArticleHeading(i, nodes) {
					content = removeSelectedNode(content, b.node)
					// A fallback can select an ancestor rather than individual blocks.
					// Exclude the nested masthead when removing its block root cannot
					// affect that selected ancestor.
					if !directlySelected {
						a.overrideIrrelevant(b.node, true)
					}
				}
			}
			if !directlySelected {
				a.overrideIrrelevant(candidate.node, true)
				title := articleTitleNode(articleHeadingText(candidate.node))
				a.overrideIrrelevant(title, false)
				withTitle := append([]*html.Node{title}, content...)
				if a.titleLeavesOutputForArticleProse(withTitle, cfg) {
					return withTitle
				}
				return content
			}
			withTitle := append([]*html.Node{candidate.node}, content...)
			if a.titleLeavesOutputForArticleProse(withTitle, cfg) {
				return withTitle
			}
			return content
		}

		// Render an h2 article headline as the document's h1. Also detach any
		// headline nested inside a selected ancestor: removing its block root alone
		// cannot prevent the ancestor renderer from emitting it a second time.
		directlySelected := false
		for _, n := range nodes {
			directlySelected = directlySelected || n == candidate.node
		}
		title := candidate.node
		content := nodes
		if candidate.kind == "h2" || !directlySelected {
			title = articleTitleNode(articleHeadingText(candidate.node))
			a.overrideIrrelevant(title, false)
			content = removeSelectedNode(content, candidate.node)
			if !directlySelected {
				a.overrideIrrelevant(candidate.node, true)
			}
		}
		if candidate.kind == "h2" {
			// A selected h1 before an h2 headline is usually the page masthead. Drop
			// only unsupported, metadata-conflicting h1 blocks before the candidate.
			for i := 0; i < bestIndex; i++ {
				b := &a.blocks[i]
				if b.kind == "h1" && !titleEquivalent(b.text, restorationTitle, a.meta.site) && !a.isCredibleArticleHeading(i, nodes) {
					content = removeSelectedNode(content, b.node)
				}
			}
		}
		content = a.demoteConflictingSelectedH1s(content, articleHeadingText(candidate.node))
		withTitle := append([]*html.Node{title}, content...)
		if a.titleLeavesOutputForArticleProse(withTitle, cfg) {
			return withTitle
		}
		// Omit the selected source headline too when its promoted form would
		// consume budget intended for the article body.
		return content
	}
	if restorationTitle == "" || a.hasEquivalentHeading(nodes, restorationTitle, cfg) {
		return nodes
	}
	// Remove only an unsupported h1 positively identified as the browser-title
	// masthead. A different nearby heading may be a legitimate section (for
	// example, "Introduction") and must survive below the synthesized title.
	// Structurally credible article headings were already selected above and
	// never reach this fallback.
	content := nodes
	for i := range a.blocks {
		b := &a.blocks[i]
		browserMasthead := b.kind == "h1" && a.meta.browserTitle != "" && titleEquivalent(b.text, a.meta.browserTitle, a.meta.site)
		if browserMasthead && !titleEquivalent(b.text, restorationTitle, a.meta.site) && !a.isCredibleArticleHeading(i, nodes) && adjacentSelectedBlockDistance(a.blocks, i, nodes, 2) > 0 {
			content = removeSelectedNode(content, b.node)
		}
	}

	// Once a synthetic document title is added, unsupported source h1 elements
	// are sections, not alternative document titles. Preserve them, but render
	// selected block roots one level lower.
	content = a.demoteConflictingSelectedH1s(content, restorationTitle)
	title := articleTitleNode(restorationTitle)
	// Synthetic nodes are not part of the indexed DOM. Explicitly mark this one
	// as relevant so article auxiliary heuristics cannot classify it by itself.
	a.overrideIrrelevant(title, false)
	withTitle := append([]*html.Node{title}, content...)
	if !a.titleLeavesOutputForArticleProse(withTitle, cfg) {
		return nodes
	}
	return withTitle
}

func (a *analysis) headingComposesBrowserTitle(index int, heading string) bool {
	browser := normalizeText(a.meta.browserTitle)
	if browser == "" || normalizedLabel(heading) == "" {
		return false
	}
	runes := []rune(browser)
	for split, r := range runes {
		if !isTitleSeparator(r) {
			continue
		}
		left := normalizeText(string(runes[:split]))
		right := normalizeText(string(runes[split+1:]))
		var masthead string
		switch {
		case normalizedLabel(left) == normalizedLabel(heading):
			masthead = right
		case normalizedLabel(right) == normalizedLabel(heading):
			masthead = left
		default:
			continue
		}
		if masthead == "" {
			continue
		}
		for i := max(0, index-8); i < index; i++ {
			if a.blocks[i].kind == "h1" && titleEquivalent(a.blocks[i].text, masthead, a.meta.site) {
				return true
			}
		}
	}
	return false
}

func articleTitleNode(text string) *html.Node {
	title := &html.Node{Type: html.ElementNode, Data: "h1"}
	title.AppendChild(&html.Node{Type: html.TextNode, Data: text})
	return title
}

func removeSelectedNode(nodes []*html.Node, remove *html.Node) []*html.Node {
	out := make([]*html.Node, 0, len(nodes))
	for _, n := range nodes {
		if n != remove {
			out = append(out, n)
		}
	}
	return out
}

func (a *analysis) demoteConflictingSelectedH1s(nodes []*html.Node, title string) []*html.Node {
	demote := map[*html.Node]bool{}
	for _, selected := range nodes {
		walk(selected, func(n *html.Node) bool {
			if n.Type == html.ElementNode && strings.EqualFold(n.Data, "h1") &&
				!titleEquivalent(articleHeadingText(n), title, a.meta.site) {
				demote[n] = true
			}
			return true
		})
	}
	if len(demote) == 0 {
		return nodes
	}
	out := make([]*html.Node, len(nodes))
	for i, root := range nodes {
		out[i] = a.cloneWithHeadingDemotions(root, demote)
	}
	return out
}

func (a *analysis) cloneWithHeadingDemotions(n *html.Node, demote map[*html.Node]bool) *html.Node {
	clone := &html.Node{Type: n.Type, DataAtom: n.DataAtom, Data: n.Data, Namespace: n.Namespace,
		Attr: append([]html.Attribute(nil), n.Attr...)}
	if demote[n] {
		clone.Data = "h2"
	}
	if state := a.nodeStates[n].irrelevant; state != 0 {
		cloneState := a.nodeStates[clone]
		cloneState.irrelevant = state
		a.nodeStates[clone] = cloneState
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		clone.AppendChild(a.cloneWithHeadingDemotions(child, demote))
	}
	return clone
}

// titleLeavesOutputForArticleProse prevents a short date or byline from being
// mistaken for the body block that a reordered title must leave room for.
func (a *analysis) titleLeavesOutputForArticleProse(nodes []*html.Node, cfg markdown.Config) bool {
	if cfg.MaxBytes <= 0 {
		return true
	}
	actualContent := markdown.Convert(nodes, cfg).EmittedContentBlocks
	if actualContent == 0 {
		return false
	}

	// Count only publication furniture preceding the first body block. Derive
	// this prefix from the selected tree itself because heading normalization may
	// clone nodes that cannot be matched against a.blocks from the original DOM.
	// Furniture after the body is deliberately ignored so removing prose cannot
	// pull a trailing date forward and make a fitting title look unsafe.
	prefix := publicationFurniturePrefix(nodes, cfg)
	prefixFurniture := markdown.Convert(prefix, cfg).EmittedContentBlocks
	return actualContent > prefixFurniture
}

func publicationFurniturePrefix(nodes []*html.Node, cfg markdown.Config) []*html.Node {
	prefix := make([]*html.Node, 0, 4)
	seen := map[*html.Node]bool{}
	stopped := false
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if stopped || n == nil || seen[n] || hardHidden(n) || cfg.Exclude != nil && cfg.Exclude(n) {
			return
		}
		seen[n] = true
		if n.Type == html.TextNode {
			if normalizeText(n.Data) != "" {
				stopped = true
			}
			return
		}
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)
			if isBlockTag(tag) {
				if isHeadingTag(tag) || tag == "hr" || isPublicationFurnitureBlock(n) {
					prefix = append(prefix, n)
					return
				}
				stopped = true
				return
			}
			if tag == "img" || tag == "svg" {
				stopped = true
				return
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
			if stopped {
				return
			}
		}
	}
	for _, n := range nodes {
		visit(n)
		if stopped {
			break
		}
	}
	return prefix
}

func isPublicationFurnitureBlock(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if hasPublicationMetadataElement(n) {
		return true
	}
	text := normalizeText(nodeText(n))
	if utf8.RuneCountInString(text) > 100 {
		return false
	}
	label := normalizedLabel(text)
	if strings.HasPrefix(label, "by ") {
		return true
	}
	nonempty, dated := listingLineEvidence(text)
	return nonempty == 1 && (dated == 1 || standaloneYearlessDatePattern.MatchString(text))
}

func hasPublicationMetadataElement(n *html.Node) bool {
	if isPublicationMetadataElement(n) {
		return true
	}
	blockText := normalizeText(nodeText(n))
	var visit func(*html.Node) bool
	visit = func(current *html.Node) bool {
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			// Inline time can also appear in ordinary prose. Treat the wrapper as
			// furniture only when the metadata element supplies all of its text.
			if isPublicationMetadataElement(child) && normalizeText(nodeText(child)) == blockText {
				return true
			}
			if visit(child) {
				return true
			}
		}
		return false
	}
	return blockText != "" && visit(n)
}

// restorationTitle returns a document-specific metadata title. Social titles
// are preferred because they normally omit browser chrome. Publication and
// author prefixes or suffixes are removed only when they agree with metadata
// or the page hostname.
func (a *analysis) restorationTitle() string {
	title := firstNonempty(a.meta.socialTitle, a.meta.title)
	if title == "" {
		return ""
	}
	// A title inferred from visible document structure is authored content, not
	// browser chrome. Only clean it when a separate social title was selected.
	if a.meta.socialTitle != "" || !a.meta.titleFromHeading {
		title = a.cleanedMetadataTitle(title)
	}
	normalized := normalizedLabel(title)
	if normalized == "" || utf8.RuneCountInString(title) > 180 || genericDocumentTitle(normalized) {
		return ""
	}
	// A few publishing platforms use only their brand as the browser title.
	// Treat that as chrome, but do not discard the same text when it came from
	// document-specific social metadata.
	if a.meta.socialTitle == "" && title == a.meta.browserTitle && genericBrowserChromeTitle(normalized) {
		return ""
	}
	// A browser-only title at the origin root is usually the publication or
	// product name. Stronger social metadata remains eligible there.
	if a.meta.socialTitle == "" && a.pageURL != nil && (a.pageURL.Path == "" || a.pageURL.Path == "/") && title == a.meta.browserTitle {
		return ""
	}
	return normalizeText(title)
}

// cleanedMetadataTitle removes browser chrome while preserving separators that
// are genuinely part of a headline. A suffix is removed only when the complete
// delimited segment matches the author, publication, or a hostname-derived
// publication name.
func (a *analysis) cleanedMetadataTitle(title string) string {
	type decorationLabel struct {
		text        string
		publication bool
	}
	// A title that consists only of a publication is browser chrome. An author
	// name, however, can also be a legitimate document title, so author equality
	// alone must not erase it.
	labels := []decorationLabel{{text: a.meta.author}, {text: a.meta.site, publication: true}}
	if a.pageURL != nil {
		host := strings.TrimPrefix(strings.ToLower(a.pageURL.Hostname()), "www.")
		if host != "" {
			labels = append(labels, decorationLabel{text: host, publication: true})
			if dot := strings.IndexByte(host, '.'); dot > 0 {
				labels = append(labels, decorationLabel{text: host[:dot], publication: true})
				if humanized := humanizedHostLabel(host[:dot]); humanized != host[:dot] {
					labels = append(labels, decorationLabel{text: humanized, publication: true})
				}
			}
			// Subdomains such as en.wikipedia.org use the registrable-domain
			// label as the visible site name. Consult the public suffix list so
			// hosts below suffixes such as com.au do not mistake "com" for it.
			if registrable, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
				if label, _, ok := strings.Cut(registrable, "."); ok && label != "" {
					labels = append(labels, decorationLabel{text: label, publication: true})
					if humanized := humanizedHostLabel(label); humanized != label {
						labels = append(labels, decorationLabel{text: humanized, publication: true})
					}
				}
			}
		}
	}
	for {
		changed := false
		for _, label := range labels {
			if normalizedLabel(label.text) == "" {
				continue
			}
			if label.publication && normalizedLabel(title) == normalizedLabel(label.text) {
				return ""
			}
			if stripped := stripTitleDecorationPreservingCase(title, label.text); stripped != title {
				title = normalizeText(stripped)
				changed = true
				break
			}
		}
		if !changed {
			return normalizeText(title)
		}
	}
}

// visibleH1TitleVariant prefers a visible h1 when browser metadata is either
// exactly that heading or that heading plus a delimited branding segment. The
// browser-title agreement prevents a masthead or marketing slogan from winning,
// while allowing a source headline to be more complete than an abbreviated
// social title.
func (a *analysis) visibleH1TitleVariant(title, browserTitle string) string {
	if title == "" {
		return title
	}
	best := ""
	walk(a.root, func(n *html.Node) bool {
		if best != "" || n.Type != html.ElementNode || !strings.EqualFold(n.Data, "h1") || hardHidden(n) {
			return best == ""
		}
		heading := normalizeText(articleHeadingText(n))
		if heading == "" {
			return true
		}
		// Exact browser agreement is safe even when a banner wrapper was classified
		// as auxiliary because it also contains category controls. An exact social
		// headline is likewise stronger than a decorated browser title.
		if (browserTitle != "" && normalizedLabel(browserTitle) == normalizedLabel(heading) &&
			(titleEquivalent(heading, title, a.meta.site) || metadataTitlePrefix(title, heading))) ||
			normalizedLabel(a.meta.socialTitle) == normalizedLabel(heading) {
			best = heading
			return false
		}
		if a.hasIrrelevantAncestor(n) {
			return true
		}
		runes := []rune(title)
		for i, r := range runes {
			if !isTitleSeparator(r) {
				continue
			}
			left := normalizeText(string(runes[:i]))
			right := normalizeText(string(runes[i+1:]))
			if normalizedLabel(left) == normalizedLabel(heading) && a.matchesKnownTitleDecoration(right) ||
				normalizedLabel(right) == normalizedLabel(heading) && a.matchesKnownTitleDecoration(left) {
				best = heading
				return false
			}
		}
		return true
	})
	if best != "" {
		return best
	}
	return title
}

func (a *analysis) exactVisibleH1Title(title string) (string, bool) {
	want := normalizedLabel(title)
	if want == "" {
		return "", false
	}
	found := ""
	walk(a.root, func(n *html.Node) bool {
		if found != "" || hardHidden(n) {
			return false
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "h1") {
			heading := normalizeText(articleHeadingText(n))
			if normalizedLabel(heading) == want {
				found = heading
				return false
			}
		}
		return true
	})
	return found, found != ""
}

func hasDelimitedTitleSegment(title string) bool {
	return strings.Contains(title, " | ") || strings.Contains(title, " - ") ||
		strings.Contains(title, " — ") || strings.Contains(title, " – ") || strings.Contains(title, " :: ")
}

func titlePrefixAtBoundary(shorter, longer string) bool {
	shorter, longer = normalizedLabel(shorter), normalizedLabel(longer)
	if shorter == "" || !strings.HasPrefix(longer, shorter) {
		return false
	}
	if len(longer) == len(shorter) {
		return true
	}
	next, _ := utf8.DecodeRuneInString(longer[len(shorter):])
	return unicode.IsSpace(next) || !unicode.IsLetter(next) && !unicode.IsDigit(next)
}

func metadataTitlePrefix(shorter, longer string) bool {
	return len(strings.Fields(normalizedLabel(shorter))) >= 4 && titlePrefixAtBoundary(shorter, longer)
}

func (a *analysis) matchesKnownTitleDecoration(segment string) bool {
	segment = normalizedLabel(segment)
	matches := func(label string) bool {
		label = normalizedLabel(label)
		return label != "" && (segment == label || segment == "by "+label ||
			strings.HasPrefix(segment, label+" by ") || strings.HasPrefix(segment, label+",") ||
			strings.HasSuffix(segment, " by "+label))
	}
	if matches(a.meta.site) || matches(a.meta.author) {
		return true
	}
	if a.pageURL == nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(a.pageURL.Hostname()), "www.")
	if matches(host) {
		return true
	}
	if dot := strings.IndexByte(host, '.'); dot > 0 && matches(host[:dot]) {
		return true
	}
	if dot := strings.IndexByte(host, '.'); dot > 0 && matches(humanizedHostLabel(host[:dot])) {
		return true
	}
	if registrable, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		if label, _, ok := strings.Cut(registrable, "."); ok {
			if matches(label) || matches(humanizedHostLabel(label)) {
				return true
			}
		}
	}
	return false
}

func humanizedHostLabel(label string) string {
	if !strings.ContainsAny(label, "-_") {
		return label
	}
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' {
			return ' '
		}
		return r
	}, label)
}

func stripTitleDecorationPreservingCase(title, site string) string {
	runes := []rune(title)
	for i, r := range runes {
		if !isTitleSeparator(r) {
			continue
		}
		left := strings.TrimSpace(string(runes[:i]))
		right := strings.TrimSpace(string(runes[i+1:]))
		if normalizedLabel(left) == normalizedLabel(site) && right != "" {
			return right
		}
		if normalizedLabel(right) == normalizedLabel(site) && left != "" {
			return left
		}
	}
	return title
}

func genericDocumentTitle(title string) bool {
	switch title {
	case "home", "homepage", "welcome", "index", "untitled", "website", "site", "menu", "navigation":
		return true
	}
	words := strings.Fields(title)
	return len(words) <= 3 && len(words) > 0 && (words[len(words)-1] == "site" || words[len(words)-1] == "website" || words[len(words)-1] == "homepage")
}

func genericBrowserChromeTitle(title string) bool {
	switch title {
	case "medium":
		return true
	}
	return false
}

// hasLeadingOutputHeading prevents a discussion topic (or another surviving
// structural title) from being replaced merely because it differs slightly
// from metadata. Later section headings do not block restoration.
func (a *analysis) hasLeadingOutputHeading(nodes []*html.Node, cfg markdown.Config) bool {
	for i := range a.blocks {
		b := &a.blocks[i]
		if !representedBySelection(b.node, nodes) || hardHidden(b.node) || a.hasIrrelevantAncestor(b.node) || (cfg.Exclude != nil && cfg.Exclude(b.node)) {
			continue
		}
		if isHeadingTag(b.kind) {
			return true
		}
		if isSubstantiveProseBlock(b) {
			return false
		}
	}
	return false
}

// hasDominantProseOutput is intentionally conservative. A title-less document
// must contain multiple substantial paragraphs, with most prose sharing one
// immediate content container. Card grids naturally spread their text across
// record containers and therefore fail this test even if page type inference
// was explicitly forced to generic.
func (a *analysis) hasDominantProseOutput(nodes []*html.Node, cfg markdown.Config) bool {
	regions := map[*html.Node]int{}
	total, paragraphs := 0, 0
	// Segmentation emits disjoint block roots, and every fallback selects either
	// one subtree or disjoint roots. Avoid a page-sized visited map here: this
	// title check is on the normal path for generic and product pages.
	for _, root := range nodes {
		walk(root, func(n *html.Node) bool {
			if n.Type != html.ElementNode {
				return true
			}
			if hardHidden(n) || a.hasIrrelevantAncestor(n) || (cfg.Exclude != nil && cfg.Exclude(n)) {
				return false
			}
			tag := strings.ToLower(n.Data)
			if tag != "p" && tag != "blockquote" {
				return true
			}
			length := utf8.RuneCountInString(normalizeText(nodeText(n)))
			if length < 40 {
				return false
			}
			paragraphs++
			total += length
			regions[n.Parent] += length
			return false
		})
	}
	if paragraphs < 2 || total < 160 {
		return false
	}
	largest := 0
	for _, length := range regions {
		if length > largest {
			largest = length
		}
	}
	return float64(largest)/float64(total) >= .70
}

func (a *analysis) hasEquivalentHeading(nodes []*html.Node, title string, cfg markdown.Config) bool {
	found := false
	for _, root := range nodes {
		walk(root, func(n *html.Node) bool {
			if found || hardHidden(n) || a.hasIrrelevantAncestor(n) || (cfg.Exclude != nil && cfg.Exclude(n)) {
				return false
			}
			if n.Type == html.ElementNode && isHeadingTag(strings.ToLower(n.Data)) && titleEquivalent(nodeText(n), title, a.meta.site) {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// isCredibleArticleHeading identifies a metadata-conflicting source heading.
// Unlike title equivalence, this deliberately requires independent structural
// evidence that the heading labels the selected prose.
func (a *analysis) isCredibleArticleHeading(headingIndex int, selected []*html.Node) bool {
	heading := a.blocks[headingIndex].node
	region := primaryHeadingRegion(heading)
	if region == nil {
		return false
	}
	marked := hasArticleHeadlineMarker(heading)
	// Merely being inside <article> is not headline evidence: several publishers
	// use h1 for every section. An unmarked heading must instead occupy the
	// leading headline position, before publication furniture and selected prose.
	// Outside an article, continue to require an explicit headline marker.
	if !marked {
		if !strings.EqualFold(region.Data, "article") || isNumberedSectionHeading(a.blocks[headingIndex].text) || !a.isLeadingArticleHeading(headingIndex, selected, region) {
			return false
		}
	}
	// A page header inside <main> is still commonly a site masthead. An article
	// header is valid because its enclosing article is chosen as the region.
	for p := heading.Parent; p != nil && p != region; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if strings.EqualFold(p.Data, "nav") {
			return false
		}
		if strings.EqualFold(p.Data, "header") && !strings.EqualFold(region.Data, "article") && !elementContainsAny(p, "article", "post", "entry", "story") {
			return false
		}
	}

	for distance := 1; distance <= 2; distance++ {
		for _, i := range []int{headingIndex - distance, headingIndex + distance} {
			if i < 0 || i >= len(a.blocks) {
				continue
			}
			b := &a.blocks[i]
			if !isSubstantiveProseBlock(b) || !nodeWithin(b.node, region) || !representedBySelection(b.node, selected) {
				continue
			}
			return true
		}
	}
	return false
}

func (a *analysis) isLeadingArticleHeading(headingIndex int, selected []*html.Node, region *html.Node) bool {
	heading := a.blocks[headingIndex].node
	for i := 0; i < headingIndex; i++ {
		b := &a.blocks[i]
		if nodeWithin(b.node, region) && representedBySelection(b.node, selected) && isSubstantiveProseBlock(b) {
			return false
		}
	}
	return !hasPublicationMetadataBefore(region, heading)
}

func hasPublicationMetadataBefore(region, heading *html.Node) bool {
	var visit func(*html.Node) (reachedHeading, found bool)
	visit = func(n *html.Node) (bool, bool) {
		if n == heading {
			return true, false
		}
		// Hidden machine-readable dates and bylines do not establish the visible
		// order of an article. Prune the whole subtree so hidden descendants cannot
		// disqualify a legitimate source headline either.
		if hardHidden(n) {
			return false, false
		}
		// Ancestors of the heading are layout, not preceding metadata.
		if n != region && n.Type == html.ElementNode && !nodeWithin(heading, n) && isPublicationMetadataElement(n) {
			return false, true
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			reached, found := visit(ch)
			if found || reached {
				return reached, found
			}
		}
		return false, false
	}
	_, found := visit(region)
	return found
}

func isPublicationMetadataElement(n *html.Node) bool {
	if strings.EqualFold(n.Data, "time") {
		return true
	}
	for value := range strings.FieldsSeq(attrValue(n, "itemprop")) {
		value = strings.ToLower(value)
		if strings.Contains(value, "datepublished") || strings.Contains(value, "author") {
			return true
		}
	}
	tokens := elementTokens(n)
	if containsAny(tokens, "byline", "dateline", "published") {
		return true
	}
	for token := range strings.FieldsSeq(tokens) {
		token = strings.ReplaceAll(token, "_", "-")
		switch token {
		case "post-date", "entry-date", "article-date", "publication-date", "post-meta", "entry-meta", "article-meta":
			return true
		}
	}
	return false
}

func isNumberedSectionHeading(text string) bool {
	runes := []rune(strings.TrimSpace(text))
	i := 0
	for i < len(runes) && runes[i] >= '0' && runes[i] <= '9' {
		i++
	}
	// Section ordinals are ordinarily short. This bound excludes year-leading
	// titles without trying to infer their subject from the remaining words.
	if i == 0 || i > 3 || i >= len(runes) {
		return false
	}
	switch runes[i] {
	case '.', ')', ':':
		i++
		return i < len(runes) && unicode.IsSpace(runes[i])
	}
	if !unicode.IsSpace(runes[i]) {
		return false
	}
	for i < len(runes) && unicode.IsSpace(runes[i]) {
		i++
	}
	if i >= len(runes) {
		return false
	}
	if runes[i] == '-' || runes[i] == '–' || runes[i] == '—' {
		i++
		if i >= len(runes) || !unicode.IsSpace(runes[i]) {
			return false
		}
		for i < len(runes) && unicode.IsSpace(runes[i]) {
			i++
		}
		return i < len(runes)
	}
	// Separator-free number prefixes are ambiguous with list-style article
	// titles such as "7 Ways to Improve Reliability". Do not reject those based
	// on text alone.
	return false
}

func hasArticleHeadlineMarker(n *html.Node) bool {
	if hasHeadlineAttribute(n) {
		return true
	}
	tokens := elementTokens(n)
	if containsAny(tokens, "headline") || (containsAny(tokens, "title") && containsAny(tokens, "article", "post", "entry", "story")) {
		return true
	}
	// Some templates put the marker on an enclosing article header instead of
	// the heading itself.
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if strings.EqualFold(p.Data, "article") || strings.EqualFold(p.Data, "main") || strings.EqualFold(attrValue(p, "role"), "main") {
			break
		}
		pt := elementTokens(p)
		if strings.EqualFold(p.Data, "header") && containsAny(pt, "article", "post", "entry", "story") {
			return true
		}
	}
	return false
}

func hasHeadlineAttribute(n *html.Node) bool {
	for _, key := range []string{"itemprop", "property"} {
		for value := range strings.FieldsSeq(attrValue(n, key)) {
			if isHeadlineProperty(value) {
				return true
			}
		}
	}
	return false
}

func isHeadlineProperty(value string) bool {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "headline") || strings.EqualFold(value, "schema:headline") {
		return true
	}

	u, err := url.Parse(value)
	if err != nil || !u.IsAbs() {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "schema.org" && host != "www.schema.org" {
		return false
	}
	path := strings.TrimRight(strings.ToLower(u.Path), "/")
	fragment := strings.TrimRight(strings.ToLower(u.Fragment), "/")
	return path == "/headline" || strings.HasSuffix(path, "/headline") || fragment == "headline" || strings.HasSuffix(fragment, "/headline")
}

func primaryHeadingRegion(n *html.Node) *html.Node {
	var primary *html.Node
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if strings.EqualFold(p.Data, "article") {
			return p
		}
		if primary == nil && (strings.EqualFold(p.Data, "main") || strings.EqualFold(attrValue(p, "role"), "main")) {
			primary = p
		}
	}
	return primary
}

// articleHeadingText omits publication furniture embedded in a heading. Some
// templates put the date and byline in a nested <small> inside the article h2,
// while others use <small> for a real subtitle, so the tag alone is not enough
// to discard its text.
func articleHeadingText(heading *html.Node) string {
	var text strings.Builder
	imageAlt := ""
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
			return
		}
		if n != heading && n.Type == html.ElementNode {
			if isExplicitHeadingPublicationMetadata(n) || strings.EqualFold(n.Data, "small") && headingPublicationFurniture(n) {
				return
			}
			if hasExactClass(n, "descriptor") {
				label := normalizeText(nodeText(n))
				if strings.HasSuffix(label, ":") {
					switch normalizedLabel(label) {
					case "title", "headline":
						return
					}
				}
			}
			if strings.EqualFold(n.Data, "img") && imageAlt == "" {
				imageAlt = normalizeText(attrValue(n, "alt"))
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(heading)
	if visible := normalizeText(text.String()); visible != "" {
		return visible
	}
	// An image-only h1 can be the accessible product or publication name. Use
	// its alternative text only when the heading has no authored text, so an
	// illustration beside a textual headline cannot change that headline.
	return imageAlt
}

// isExplicitHeadingPublicationMetadata is narrower than the general metadata
// detector because an unmarked <time> may be part of the headline itself (for
// example, "The 2024 Report"). Time elements need publication-specific markup
// or an enclosing furniture wrapper before articleHeadingText drops them.
func isExplicitHeadingPublicationMetadata(n *html.Node) bool {
	if !strings.EqualFold(n.Data, "time") {
		return isPublicationMetadataElement(n)
	}
	for value := range strings.FieldsSeq(attrValue(n, "itemprop")) {
		value = strings.ToLower(value)
		if strings.Contains(value, "datepublished") || strings.Contains(value, "datemodified") || strings.Contains(value, "datecreated") {
			return true
		}
	}
	property := strings.ToLower(attrValue(n, "property"))
	if strings.Contains(property, "published_time") || strings.Contains(property, "modified_time") {
		return true
	}
	tokens := elementTokens(n)
	if containsAny(tokens, "byline", "dateline", "published") {
		return true
	}
	for token := range strings.FieldsSeq(tokens) {
		token = strings.ReplaceAll(token, "_", "-")
		switch token {
		case "post-date", "entry-date", "article-date", "publication-date", "post-meta", "entry-meta", "article-meta":
			return true
		}
	}
	return false
}

func headingPublicationFurniture(n *html.Node) bool {
	if isPublicationFurnitureBlock(n) {
		return true
	}
	found := false
	walk(n, func(current *html.Node) bool {
		if current != n && current.Type == html.ElementNode && isExplicitHeadingPublicationMetadata(current) {
			found = true
			return false
		}
		return !found
	})
	return found
}

func isSubstantiveProseBlock(b *block) bool {
	switch b.kind {
	case "p", "blockquote", "generic":
		return b.textChars() >= 40
	}
	return false
}

func adjacentSelectedBlockDistance(blocks []block, headingIndex int, selected []*html.Node, maxDistance int) int {
	for distance := 1; distance <= maxDistance; distance++ {
		for _, i := range []int{headingIndex - distance, headingIndex + distance} {
			if i >= 0 && i < len(blocks) && representedBySelection(blocks[i].node, selected) {
				return distance
			}
		}
	}
	return 0
}

func nodeWithin(n, ancestor *html.Node) bool {
	if ancestor == nil {
		return false
	}
	for p := n; p != nil; p = p.Parent {
		if p == ancestor {
			return true
		}
	}
	return false
}

func representedBySelection(n *html.Node, selected []*html.Node) bool {
	for _, root := range selected {
		for p := n; p != nil; p = p.Parent {
			if p == root {
				return true
			}
		}
		for p := root; p != nil; p = p.Parent {
			if p == n {
				return true
			}
		}
	}
	return false
}

// titleEquivalent compares a visible heading with a metadata title. siteName is
// optional to preserve the small helper's existing use in tests, but callers
// with metadata should provide it: an exact site-name decoration can then be
// removed without treating an arbitrary continuation as part of the title.
func titleEquivalent(heading, title string, siteName ...string) bool {
	heading = normalizedLabel(heading)
	title = normalizedLabel(title)
	if heading == "" {
		return false
	}
	if title == "" || heading == title {
		return true
	}

	var site string
	if len(siteName) > 0 {
		site = normalizedLabel(siteName[0])
	}
	if site != "" {
		// A site may decorate either value (although in practice this is usually
		// the browser title). Consider both prefix and suffix forms.
		heading = stripSiteTitleDecoration(heading, site)
		title = stripSiteTitleDecoration(title, site)
		if heading == title {
			return true
		}
	}

	// Publication dates are another common browser-only suffix. Keep this
	// deliberately narrow: only a year/date at the end of an otherwise exact
	// title is ignored.
	if titleWithDateDecoration(heading, title) || titleWithDateDecoration(title, heading) {
		return true
	}

	// Retain separator-based compatibility when SiteName is unavailable. The
	// match remains exact on one complete side of the separator; ordinary prefix
	// matches (for example, "Release notes" and "Release notes for v2") do not
	// qualify. When SiteName is known, do not mistake a different separator-
	// delimited subtitle for that site.
	if site != "" {
		return false
	}
	for _, pair := range [][2]string{{heading, title}, {title, heading}} {
		shorter, longer := pair[0], pair[1]
		if strings.HasPrefix(longer, shorter) {
			rest := []rune(strings.TrimSpace(strings.TrimPrefix(longer, shorter)))
			if len(rest) > 0 && isTitleSeparator(rest[0]) {
				return true
			}
		}
		if strings.HasSuffix(longer, shorter) {
			rest := []rune(strings.TrimSpace(strings.TrimSuffix(longer, shorter)))
			if len(rest) > 0 && isTitleSeparator(rest[len(rest)-1]) {
				return true
			}
		}
	}
	return false
}

// articleTitleVariantEquivalent recognizes editorial rewrites of the same
// headline. Social metadata often adds search-oriented words while the visible
// headline stays concise. This is used only for explicitly marked article
// headings; it is intentionally not part of general title equivalence.
func articleTitleVariantEquivalent(heading, title string) bool {
	words := func(value string) map[string]bool {
		out := make(map[string]bool)
		for word := range strings.FieldsSeq(normalizedLabel(value)) {
			switch word {
			case "a", "an", "and", "the", "to", "of", "for", "in", "on", "with", "after", "before",
				"may", "might", "can", "could", "be", "been", "is", "are", "was", "were", "new",
				"more", "than", "over":
				continue
			}
			out[word] = true
		}
		return out
	}
	a, b := words(heading), words(title)
	if len(a) < 4 || len(b) < 4 {
		return false
	}
	shared := 0
	for word := range a {
		if b[word] {
			shared++
		}
	}
	shorter := min(len(a), len(b))
	union := len(a) + len(b) - shared
	return shared >= 4 && float64(shared)/float64(shorter) >= .8 && float64(shared)/float64(union) >= .55
}

func stripSiteTitleDecoration(title, site string) string {
	for _, prefix := range []bool{true, false} {
		var rest string
		if prefix && strings.HasPrefix(title, site) {
			rest = strings.TrimSpace(strings.TrimPrefix(title, site))
		} else if !prefix && strings.HasSuffix(title, site) {
			rest = strings.TrimSpace(strings.TrimSuffix(title, site))
		} else {
			continue
		}
		runes := []rune(rest)
		if len(runes) == 0 {
			return title
		}
		separator := runes[0]
		if !prefix {
			separator = runes[len(runes)-1]
		}
		if isTitleSeparator(separator) {
			if prefix {
				return normalizedLabel(string(runes[1:]))
			}
			return normalizedLabel(string(runes[:len(runes)-1]))
		}
	}
	return title
}

func titleWithDateDecoration(base, decorated string) bool {
	if !strings.HasPrefix(decorated, base) {
		return false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(decorated, base))
	if strings.HasPrefix(rest, "in ") {
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "in "))
	} else {
		rest = strings.TrimSpace(strings.Trim(rest, "()[]"))
		runes := []rune(rest)
		if len(runes) > 0 && isTitleSeparator(runes[0]) {
			rest = strings.TrimSpace(string(runes[1:]))
		}
	}
	return isTitleDate(rest)
}

func isTitleDate(s string) bool {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '/' || r == '.' })
	if len(parts) < 1 || len(parts) > 3 || len(parts[0]) != 4 || !allASCIIDigits(parts[0]) {
		return false
	}
	year := parts[0]
	if year < "1900" || year > "2199" {
		return false
	}
	for _, part := range parts[1:] {
		if len(part) < 1 || len(part) > 2 || !allASCIIDigits(part) {
			return false
		}
	}
	return true
}

func isTitleSeparator(r rune) bool {
	return strings.ContainsRune("|:~-–—·•", r)
}
