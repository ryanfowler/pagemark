package engine

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/dom"
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
	return hasBoilerplateTokenAttributes(n)
}

// hasBoilerplateTokenNode is the memoized version of hasBoilerplateToken for
// use inside a scoring analysis pass. It avoids re-scanning the same element's
// attributes during every block's scoring iteration.
func (a *analysis) hasBoilerplateTokenNode(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if value, known := a.nodeStates[n].boilerplateTokenCheck.value(); known {
		return value
	}
	result := hasBoilerplateToken(n)
	state := a.nodeStates[n]
	state.boilerplateTokenCheck.store(result)
	a.nodeStates[n] = state
	return result
}

// hasBoilerplateTokenAttributes is the slow attribute-scanning part of
// hasBoilerplateToken, factored out so the memoized wrapper can skip the fast
// elementContainsAny path.
func hasBoilerplateTokenAttributes(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
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
	"latest stories": true, "see also": true, "next article": true, "previous article": true,
}

var callToActionLabels = map[string]bool{
	"read more": true, "learn more": true, "continue reading": true,
	"view more": true, "see more": true,
}

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
		isCopyrightNotice(n) || isScriptRequirementNotice(n) || isEmptyRecordList(n) ||
		isPageFooterConvention(n) || hasClassConvention(n, "step-nav") || hasExactClass(n, "crawler-linkback-list") ||
		hasExactClass(n, "post-likes") || hasClassPrefix(n, "jetpack-likes-widget") ||
		hasExactClass(n, "mw-editsection") || hasExactClass(n, "printfooter") || hasExactClass(n, "catlinks") ||
		isArticleNavigationControl(n) || isTaxonomyLinkParagraph(n) || isFilterControlRegion(n) || isMastheadRegion(n) ||
		strings.EqualFold(attrValue(n, "id"), "siteSub") ||
		strings.EqualFold(attrValue(n, "itemprop"), "interactionStatistic") ||
		strings.EqualFold(attrValue(n, "id"), "warning_not_complete") {
		return true
	}
	role := strings.ToLower(attrValue(n, "role"))
	if containsAny(role, "navigation", "complementary", "contentinfo", "menu") ||
		role == "dialog" || role == "alertdialog" {
		return true
	}
	if isCookieConsentRegion(n) {
		return true
	}
	if isTableOfContentsRegion(n) || isLinkedImageMasthead(n) || isOversizedContributorRoll(n) ||
		isConventionallyNamedNavigation(n) || elementContainsAny(n, "banner") && controls(n) > 0 {
		return true
	}
	if elementContainsAny(n, navigationStructureTokens...) && !headingDocumentsStructure(n) && hasNavigationShape(n) {
		return true
	}
	if isBreadcrumbLike(n) {
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

func isScriptRequirementNotice(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(n.Data)
	if tag != "p" && tag != "div" && tag != "section" && tag != "noscript" {
		return false
	}
	if !hasConciseScriptRequirementText(n) {
		return false
	}
	if tag == "noscript" {
		return true
	}
	// The same words can be authored documentation. Treat them as an application
	// shell notice only when they occur outside semantic primary content.
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && (strings.EqualFold(p.Data, "main") || strings.EqualFold(p.Data, "article")) {
			return false
		}
	}
	return true
}

// hasConciseScriptRequirementText performs normalization, length counting,
// lowercasing, and phrase matching in one bounded subtree scan. Its text-node
// separators match nodeText, so phrases can span inline elements.
func hasConciseScriptRequirementText(n *html.Node) bool {
	s := scriptRequirementTextScanner{}
	s.scan(n)
	return !s.tooLong && s.enableJavaScript && s.applicationContext
}

type scriptRequirementTextScanner struct {
	window                         [32]byte
	windowLen, windowStart, runes  int
	textNodes                      int
	started, pendingSpace, tooLong bool
	enableJavaScript               bool
	applicationContext             bool
}

// scan returns true when the 301st normalized rune makes further traversal
// unnecessary.
func (s *scriptRequirementTextScanner) scan(n *html.Node) bool {
	if n == nil || dom.Hidden(n) {
		return false
	}
	if n.Type == html.TextNode {
		if s.textNodes > 0 && s.started {
			s.pendingSpace = true
		}
		s.textNodes++
		for text := n.Data; text != ""; {
			r := rune(text[0])
			size := 1
			space := r == ' ' || r >= '\t' && r <= '\r'
			if r >= utf8.RuneSelf {
				r, size = utf8.DecodeRuneInString(text)
				space = unicode.IsSpace(r)
			}
			text = text[size:]
			if space {
				if s.started {
					s.pendingSpace = true
				}
				continue
			}
			if s.pendingSpace {
				if s.push(' ') {
					return true
				}
				s.pendingSpace = false
			}
			if s.push(unicode.ToLower(r)) {
				return true
			}
			s.started = true
		}
		return false
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if s.scan(child) {
			return true
		}
	}
	return false
}

func (s *scriptRequirementTextScanner) push(r rune) bool {
	s.runes++
	if s.runes == 301 {
		s.tooLong = true
		return true
	}
	c := byte(0)
	if r <= unicode.MaxASCII {
		c = byte(r)
	}
	pushTextWindow(&s.window, &s.windowLen, &s.windowStart, c)
	switch c {
	case 't':
		if !s.enableJavaScript {
			s.enableJavaScript = windowHasSuffix(&s.window, s.windowLen, s.windowStart, "enable javascript", false)
		}
		if !s.applicationContext {
			s.applicationContext = windowHasSuffix(&s.window, s.windowLen, s.windowStart, "requires javascript", false)
		}
	case 'n':
		if !s.applicationContext {
			s.applicationContext = windowHasSuffix(&s.window, s.windowLen, s.windowStart, "web application", false)
		}
	case 'p':
		if !s.applicationContext {
			s.applicationContext = windowHasSuffix(&s.window, s.windowLen, s.windowStart, "run this app", false)
		}
	}
	return false
}

func isCopyrightNotice(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !strings.EqualFold(n.Data, "p") {
		return false
	}
	// Image credits can begin with © but continue with the article's opening
	// prose. Never infer a footer notice from a paragraph in a semantic or
	// conventionally named article body.
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if strings.EqualFold(p.Data, "article") || isConventionalArticleBody(p) ||
			strings.EqualFold(attrValue(p, "itemprop"), "articleBody") {
			return false
		}
	}
	text := normalizeText(nodeText(n))
	textRunes := utf8.RuneCountInString(text)
	if text == "" || textRunes > 300 {
		return false
	}
	lower := strings.ToLower(text)
	allRights := strings.Contains(lower, "all rights reserved")
	copyrightLead := strings.HasPrefix(lower, "copyright ") && strings.Contains(text, "©")
	// A year makes a short credit line look like conventional footer furniture,
	// but it is common for a longer article opening to begin with a dated image
	// attribution. Keep the date signal limited to standalone-length notices.
	conciseDatedNotice := textRunes <= 64 && containsCopyrightYear(lower)
	datedCopyrightLead := strings.HasPrefix(lower, "copyright ") && conciseDatedNotice
	attributionLead := strings.HasPrefix(text, "©") &&
		(strings.Contains(lower, "copyright") || strings.Contains(lower, "trademark") || strings.Contains(lower, "registered"))
	leadingDatedMark := strings.HasPrefix(text, "©") && conciseDatedNotice
	return copyrightLead || attributionLead || datedCopyrightLead || leadingDatedMark ||
		allRights && strings.Contains(text, "©")
}

func containsCopyrightYear(text string) bool {
	for i := 0; i+4 <= len(text); i++ {
		if (text[i:i+2] == "19" || text[i:i+2] == "20") &&
			text[i+2] >= '0' && text[i+2] <= '9' && text[i+3] >= '0' && text[i+3] <= '9' {
			return true
		}
	}
	return false
}

// A generic .footer outside primary semantic content is page furniture. The
// ancestor check preserves documentation for footer-named components.
func isPageFooterConvention(n *html.Node) bool {
	named := false
	for class := range strings.FieldsSeq(attrValue(n, "class")) {
		class = strings.ToLower(class)
		if class == "footer" || strings.HasPrefix(class, "footer-") || strings.HasPrefix(class, "footer_") ||
			class == "site-footer" || strings.HasPrefix(class, "site-footer-") || strings.HasPrefix(class, "site-footer_") {
			named = true
			break
		}
	}
	if !named {
		return false
	}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && (strings.EqualFold(p.Data, "main") || strings.EqualFold(p.Data, "article")) {
			return false
		}
	}
	return true
}

// isCookieConsentRegion recognizes explicitly named consent UI rather than
// penalizing ordinary prose that happens to discuss cookies. Requiring both a
// consent subject and a panel-shaped marker keeps examples inside authored
// articles and documentation eligible.
func isCookieConsentRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	// Reject ordinary elements from local attributes before walking ancestors or
	// scanning controls in the candidate subtree.
	id, class := attrValue(n, "id"), attrValue(n, "class")
	subject := containsAnyFold(id, "cookie", "consent") || containsAnyFold(class, "cookie", "consent")
	panel := containsAnyFold(id, "banner", "dialog", "modal", "notice", "preferences", "popup", "pop-up") ||
		containsAnyFold(class, "banner", "dialog", "modal", "notice", "preferences", "popup", "pop-up")
	if !subject || !panel {
		return false
	}
	insideMain := false
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if strings.EqualFold(p.Data, "article") {
			return false
		}
		insideMain = insideMain || strings.EqualFold(p.Data, "main")
	}
	// Inside a broad application main, naming alone is ambiguous with authored
	// documentation. Real consent UI has controls; an explanatory section with
	// the same class and heading does not.
	return !insideMain || controls(n) > 0
}

func hasClassPrefix(n *html.Node, prefix string) bool {
	for class := range strings.FieldsSeq(strings.ToLower(attrValue(n, "class"))) {
		if class == prefix || strings.HasPrefix(class, prefix+"-") || strings.HasPrefix(class, prefix+"_") {
			return true
		}
	}
	return false
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

func hasCompactClass(n *html.Node, wants ...string) bool {
	for class := range strings.FieldsSeq(attrValue(n, "class")) {
		for _, want := range wants {
			if compactClassEqual(class, want) {
				return true
			}
		}
	}
	return false
}

func compactClassEqual(class, want string) bool {
	at := 0
	for i := 0; i < len(class); i++ {
		c := class[i]
		if c == '-' || c == '_' {
			continue
		}
		if at == len(want) || lowerASCII(c) != want[at] {
			return false
		}
		at++
	}
	return at == len(want)
}

// HTML class, id, and role tokenization uses the five ASCII whitespace bytes.
func htmlSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

// headingDocumentsBoilerplate reports when a boilerplate-looking identifier
// names the subject of an authored documentation section. This keeps sections
// such as cookie-consent-notice while ordinary consent widgets without a
// matching explanatory heading retain their score penalty.
func headingDocumentsBoilerplate(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	heading := firstRegionHeading(n)
	if heading == "" {
		return false
	}
	identifier := strings.ToLower(attrValue(n, "id") + " " + attrValue(n, "class"))
	for _, subject := range badTokens {
		if strings.Contains(identifier, subject) && containsWordSequence(heading, subject) {
			return true
		}
	}
	return false
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

func (a *analysis) setIrrelevant(n *html.Node, irrelevant bool) {
	state := a.nodeStates[n]
	state.irrelevant.store(irrelevant)
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
			state.irrelevantAncestor = memoizedBoolUnknown
			a.nodeStates[descendant] = state
		}
		return true
	})
}

// baseAuxiliaryNode caches page-type-independent auxiliary classification.
// Type inference, listing detection, and final scoring all query this same
// evidence, so each element must pay for its local classification only once.
func (a *analysis) baseAuxiliaryNode(n *html.Node) bool {
	if value, known := a.nodeStates[n].baseAuxiliary.value(); known {
		return value
	}
	auxiliary := irrelevantNode(n) || isAdvertisementRegion(n)
	state := a.nodeStates[n]
	state.baseAuxiliary.store(auxiliary)
	a.nodeStates[n] = state
	return auxiliary
}

func pushTextWindow(window *[32]byte, length, start *int, c byte) {
	if *length < len(window) {
		window[(*start+*length)%len(window)] = c
		(*length)++
		return
	}
	window[*start] = c
	*start = (*start + 1) % len(window)
}

func textWindowByte(window *[32]byte, length, start, index int) byte {
	if index < 0 || index >= length {
		return 0
	}
	return window[(start+index)%len(window)]
}

func windowHasSuffix(window *[32]byte, length, start int, phrase string, wholeWord bool) bool {
	if len(phrase) > length {
		return false
	}
	offset := length - len(phrase)
	for i := range len(phrase) {
		if textWindowByte(window, length, start, offset+i) != phrase[i] {
			return false
		}
	}
	return !wholeWord || offset == 0 || textWindowByte(window, length, start, offset-1) == ' '
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
	if value, known := a.nodeStates[n].irrelevantAncestor.value(); known {
		return value
	}
	irrelevant := a.isIrrelevantNode(n) || a.hasIrrelevantAncestor(n.Parent)
	state := a.nodeStates[n] // isIrrelevantNode may have updated the entry.
	state.irrelevantAncestor.store(irrelevant)
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
			if isBlockTag(tag) && normalizedTextAtLeast(ch, 1) {
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
