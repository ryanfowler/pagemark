package engine

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/dom"
	"golang.org/x/net/html"
)

func (a *analysis) segment(n *html.Node, excluded bool) {
	if n.Type == html.ElementNode {
		tag := strings.ToLower(n.Data)
		// SVG remains hidden to generic DOM walkers so its internals cannot affect
		// scoring. Only this explicit opaque-image path may bypass that rule.
		opaqueSVG := a.o.includeImages && tag == "svg" && meaningfulVisual(n)
		excluded = excluded || (hardHidden(n) && !opaqueSVG)
		if excluded {
			return
		}
		// A visual does not need a paragraph or figure wrapper in HTML. Segment it
		// directly when no selected wrapper has already stopped traversal above.
		if a.o.includeImages && isVisualElement(n) && meaningfulVisual(n) {
			a.appendBlock(n, "image", "", true)
			return
		}
		// Forum software often puts a post's prose directly in a generic div,
		// using <br> (and occasionally <hr>) rather than paragraphs. Prefer the
		// innermost explicitly marked body over its wrappers and enclosing table.
		// The ambiguous post-content convention is also common on publishers; keep
		// its paragraph structure when local prose and article metadata agree.
		discussionBody := isDiscussionBodyContainer(n) && !a.isPublicationArticleContent(n)
		hasPostBody := a.hasDiscussionBodyDescendant(n)
		if discussionBody && !hasPostBody {
			text := normalizeText(nodeText(n))
			if text != "" {
				a.appendBlock(n, "generic", text, false)
				return
			}
		}
		if isBlockTag(tag) && !hasPostBody {
			text := normalizeText(nodeText(n))
			imageOnly := text == "" && a.o.includeImages && hasMeaningfulVisual(n)
			if text != "" || tag == "hr" || imageOnly {
				a.appendBlock(n, tag, text, imageOnly)
				return
			}
		}
		if isGenericContainer(tag) && !hasPostBody && !hasBlockDescendant(n) {
			text := normalizeText(nodeText(n))
			visual := a.o.includeImages && hasMeaningfulVisual(n)
			if utf8.RuneCountInString(text) >= 12 || visual {
				kind := "generic"
				if visual {
					// Publishers frequently build a figure from div, picture, and
					// caption divs rather than using <figure>. Treat that complete wrapper
					// as one visual block so its caption does not prevent image selection.
					kind = "figure"
				}
				a.appendBlock(n, kind, text, visual && text == "")
				return
			}
		}
	}
	// Invalid but widespread publisher HTML puts a block advertisement inside an
	// open paragraph. The HTML parser closes that paragraph before the ad, leaving
	// the continuation as direct text and inline children of the content div.
	// Normal block segmentation would silently lose that text. Recover each such
	// inline run as a synthetic paragraph while retaining the source container as
	// its ancestry for scoring and auxiliary checks.
	if n.Type == html.ElementNode && isGenericContainer(strings.ToLower(n.Data)) && hasBlockDescendant(n) &&
		(strings.EqualFold(n.Data, "article") || strings.EqualFold(n.Data, "main") ||
			elementContainsAny(n, "article", "content", "entry", "post", "story", "body")) {
		a.segmentDirectFlow(n, excluded)
		return
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		a.segment(ch, excluded)
	}
}

func (a *analysis) segmentDirectFlow(parent *html.Node, excluded bool) {
	var run []*html.Node
	flush := func() {
		if len(run) == 0 {
			return
		}
		p := &html.Node{Type: html.ElementNode, Data: "p"}
		for _, source := range run {
			p.AppendChild(cloneHTMLNode(source))
		}
		run = nil
		text := normalizeText(nodeText(p))
		if utf8.RuneCountInString(text) < 12 {
			return
		}
		// Synthetic nodes are intentionally not inserted into the caller's DOM,
		// but this ancestry lets the scorer see the original prose region.
		p.Parent = parent
		a.appendBlock(p, "p", text, false)
	}
	for ch := parent.FirstChild; ch != nil; ch = ch.NextSibling {
		boundary := hardHidden(ch)
		if ch.Type == html.ElementNode {
			tag := strings.ToLower(ch.Data)
			boundary = boundary || isBlockTag(tag) || isGenericContainer(tag) || hasBlockDescendant(ch) ||
				tag == "aside" || tag == "header" || tag == "footer" || tag == "nav" || isVisualElement(ch)
		}
		if boundary {
			flush()
			a.segment(ch, excluded)
			continue
		}
		run = append(run, ch)
	}
	flush()
}

func (a *analysis) appendBlock(n *html.Node, kind, text string, imageOnly bool) {
	b := block{id: len(a.blocks) + 1, node: n, kind: kind, text: text, imageOnly: imageOnly}
	b.indexEvidence()
	a.blocks = append(a.blocks, b)
}

func (b *block) indexEvidence() {
	if b == nil || b.evidenceIndexed {
		return
	}
	b.chars = utf8.RuneCountInString(b.text)
	b.linkedChars, b.controlCount = blockSubtreeEvidence(b.node)
	b.evidenceIndexed = true
}

func (b *block) textChars() int {
	b.indexEvidence()
	return b.chars
}

func (b *block) linkChars() int {
	b.indexEvidence()
	return b.linkedChars
}

func (b *block) controls() int {
	b.indexEvidence()
	return b.controlCount
}

// normalizedRuneCounter counts normalizeText(nodeText(...)) without building
// either intermediate string. nodeText inserts one space between visible text
// nodes, which is represented by pendingSpace.
type normalizedRuneCounter struct {
	runes, textNodes      int
	started, pendingSpace bool
}

func (c *normalizedRuneCounter) addText(text string) {
	if c.textNodes > 0 && c.started {
		c.pendingSpace = true
	}
	c.textNodes++
	for len(text) > 0 {
		space, size := textRuneSpace(text)
		if space {
			if c.started {
				c.pendingSpace = true
			}
		} else {
			if c.pendingSpace {
				c.runes++
				c.pendingSpace = false
			}
			c.runes++
			c.started = true
		}
		text = text[size:]
	}
}

// normalizedTextAtLeast reports whether visible normalized text reaches limit
// without materializing the subtree text. It is used by classification passes
// that only need a threshold and can stop scanning as soon as it is met.
func normalizedTextAtLeast(n *html.Node, limit int) bool {
	if limit <= 0 {
		return true
	}
	var counter normalizedRuneCounter
	var visit func(*html.Node) bool
	visit = func(current *html.Node) bool {
		if current == nil || dom.Hidden(current) {
			return false
		}
		if current.Type == html.TextNode {
			counter.addText(current.Data)
			return counter.runes >= limit
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			if visit(child) {
				return true
			}
		}
		return false
	}
	return visit(n)
}

// blockSubtreeEvidence combines the two complete subtree scans needed by block
// scoring. An outer link owns all of its descendant text, matching the pruning
// behavior of linkTextLength even for malformed nested anchors.
func blockSubtreeEvidence(root *html.Node) (linkedChars, controlCount int) {
	accumulateBlockSubtreeEvidence(root, root, nil, &linkedChars, &controlCount)
	return linkedChars, controlCount
}

func accumulateBlockSubtreeEvidence(n, root *html.Node, linked *normalizedRuneCounter, linkedChars, controlCount *int) {
	if n == nil || dom.Hidden(n) {
		return
	}
	if n.Type == html.TextNode {
		if linked != nil {
			linked.addText(n.Data)
		}
		return
	}
	if n.Type == html.ElementNode {
		tag := n.Data
		switch tag {
		case "button", "input", "select", "textarea":
			*controlCount++
		default:
			// Preserve ExtractNode support for caller-built mixed-case trees
			// without putting EqualFold on the parser-lowercase path.
			for i := 0; i < len(tag); i++ {
				if tag[i] >= 'A' && tag[i] <= 'Z' {
					if strings.EqualFold(tag, "button") || strings.EqualFold(tag, "input") ||
						strings.EqualFold(tag, "select") || strings.EqualFold(tag, "textarea") {
						*controlCount++
					}
					break
				}
			}
		}
		anchor := tag == "a"
		if !anchor {
			for i := 0; i < len(tag); i++ {
				if tag[i] >= 'A' && tag[i] <= 'Z' {
					anchor = strings.EqualFold(tag, "a")
					break
				}
			}
		}
		if n != root && linked == nil && anchor {
			var link normalizedRuneCounter
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				accumulateBlockSubtreeEvidence(ch, root, &link, linkedChars, controlCount)
			}
			*linkedChars += link.runes
			return
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		accumulateBlockSubtreeEvidence(ch, root, linked, linkedChars, controlCount)
	}
}

func cloneHTMLNode(n *html.Node) *html.Node {
	clone := &html.Node{Type: n.Type, DataAtom: n.DataAtom, Data: n.Data, Namespace: n.Namespace,
		Attr: append([]html.Attribute(nil), n.Attr...)}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		clone.AppendChild(cloneHTMLNode(ch))
	}
	return clone
}

// detectTextListingPre identifies old text-mode interfaces whose primary UI is
// a preformatted archive. A large pre alone is intentionally insufficient:
// links, repeated lines, archive metadata, dominance, and little outside prose
// must agree before code rendering is disabled.
func (a *analysis) detectTextListingPre() {
	total := 0
	for i := range a.blocks {
		total += a.blocks[i].textChars()
	}
	if total == 0 {
		return
	}
	hints := strings.ToLower(strings.Join([]string{a.meta.title, a.meta.description, a.meta.schemaType, a.meta.canonical}, " "))
	if a.pageURL != nil {
		hints += " " + strings.ToLower(a.pageURL.Path)
	}
	archiveHint := containsAny(hints, "archive", "inbox", "mailing list", "message list")

	for i := range a.blocks {
		b := &a.blocks[i]
		if b.kind != "pre" {
			continue
		}
		chars := b.textChars()
		if chars < 120 || float64(chars)/float64(total) < .65 || total-chars > max(200, chars/3) {
			continue
		}
		anchors, linkedLines := linkedPreLineEvidence(b.node)
		lines, dated := listingLineEvidence(nodeText(b.node))
		explicitArticle := a.o.pageType == PageTypeArticle
		articleContext := explicitArticle || a.meta.articleType || a.meta.articlePublished
		for p := b.node.Parent; p != nil; p = p.Parent {
			articleContext = articleContext || (p.Type == html.ElementNode && strings.EqualFold(p.Data, "article"))
		}
		// An explicit article override is authoritative for ambiguous pre content.
		// Inferred article semantics merely make the bar higher because an archive
		// can carry inaccurate article metadata while still having repeated dates.
		if explicitArticle || (articleContext && dated < 3) {
			continue
		}
		if anchors >= 4 && linkedLines >= 4 && lines >= 4 && (archiveHint || dated >= 3) {
			a.textListingPre = b.node
			return
		}
	}
}

const archiveMonthPattern = `(?:jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)`

var archiveDatePattern = regexp.MustCompile(`(?i)(?:` +
	`\b(?:19|20)\d{2}[-/](?:0[1-9]|1[0-2])[-/](?:0[1-9]|[12]\d|3[01])\b|` +
	`\b` + archiveMonthPattern + `(?:\s+\d{1,2},?)?\s+(?:19|20)\d{2}\b|` +
	`\b(?:0?[1-9]|[12]\d|3[01])\s+` + archiveMonthPattern + `\s+(?:19|20)\d{2}\b|` +
	`\b(?:19|20)\d{2}\s+` + archiveMonthPattern + `\b)`)

// A yearless date is too ambiguous for general archive detection, but a short
// standalone publication-furniture block commonly uses this exact shape.
var standaloneYearlessDatePattern = regexp.MustCompile(`(?i)^\s*(?:` +
	archiveMonthPattern + `\s+(?:0?[1-9]|[12]\d|3[01])(?:st|nd|rd|th)?,?|` +
	`(?:0?[1-9]|[12]\d|3[01])(?:st|nd|rd|th)?\s+` + archiveMonthPattern + `)\s*$`)

func listingLineEvidence(text string) (nonempty, dated int) {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		nonempty++
		if archiveDatePattern.MatchString(line) {
			dated++
		}
	}
	return nonempty, dated
}

// linkedPreLineEvidence counts links and the physical lines on which linked
// text occurs. Requiring distribution across lines prevents a navigation row
// or one linked source-code line from masquerading as repeated records.
func linkedPreLineEvidence(root *html.Node) (anchors, linkedLines int) {
	lineLinked := false
	var visit func(*html.Node, bool)
	visit = func(n *html.Node, inLink bool) {
		if hardHidden(n) {
			return
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") && attrValue(n, "href") != "" && normalizeText(nodeText(n)) != "" {
			anchors++
			inLink = true
		}
		if n.Type == html.TextNode {
			parts := strings.Split(strings.ReplaceAll(strings.ReplaceAll(n.Data, "\r\n", "\n"), "\r", "\n"), "\n")
			for i, part := range parts {
				if inLink && strings.TrimSpace(part) != "" {
					lineLinked = true
				}
				if i < len(parts)-1 {
					if lineLinked {
						linkedLines++
					}
					lineLinked = false
				}
			}
			return
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			visit(ch, inLink)
		}
	}
	visit(root, false)
	if lineLinked {
		linkedLines++
	}
	return anchors, linkedLines
}

// renderedMarkdownDocument identifies a complete authored document embedded
// in an application shell. The conjunction is intentionally strict: broad
// names such as "content" or itemprop=text alone occur on ordinary articles
// and product descriptions, while markdown-body is a convention used by
// rendered repository READMEs and similar Markdown viewers.
func renderedMarkdownDocument(root *html.Node) *html.Node {
	var best *html.Node
	bestLength := 0
	walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && hardHidden(n) {
			return false
		}
		if n.Type != html.ElementNode {
			return true
		}
		if best != nil && nodeWithin(n, best) {
			return false
		}
		markdownBody := false
		for class := range strings.FieldsSeq(attrValue(n, "class")) {
			if strings.EqualFold(class, "markdown-body") {
				markdownBody = true
				break
			}
		}
		if !markdownBody || !strings.EqualFold(attrValue(n, "itemprop"), "text") {
			return true
		}
		length := utf8.RuneCountInString(normalizeText(nodeText(n)))
		if length > bestLength {
			best, bestLength = n, length
		}
		return false
	})
	return best
}

func isGenericContainer(tag string) bool {
	switch tag {
	case "div", "section", "article", "main", "address":
		return true
	}
	return false
}

func isDiscussionBodyContainer(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !isGenericContainer(strings.ToLower(n.Data)) {
		return false
	}
	tokens := elementTokens(n)
	bodyToken := containsAny(tokens, "body", "content", "text")
	return bodyToken && containsAny(tokens, "post", "comment", "answer", "reply", "message")
}

func (a *analysis) hasDiscussionBodyDescendant(n *html.Node) bool {
	if n == nil {
		return false
	}
	return a.nodeStates[n].discussionBody == 2
}

const (
	subtreeHasForm uint8 = 1 << iota
	subtreeHasEmail
	subtreeHasSubscriptionForm
)

// indexSubtreeEvidence performs one post-order pass for properties queried on
// many nested containers. Only positive results are recorded, avoiding a large
// hash-map entry for every element on pages without these features.
func (a *analysis) indexSubtreeEvidence(n *html.Node) (bool, uint8) {
	if n == nil || n.Type == html.ElementNode && hardHidden(n) {
		return false, 0
	}
	bodyBelow := false
	var subscription uint8
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		// Text and comment nodes cannot contribute structural evidence.
		if ch.Type == html.ElementNode {
			body, evidence := a.indexSubtreeEvidence(ch)
			bodyBelow = bodyBelow || body
			subscription |= evidence
		}
	}
	if n.Type == html.ElementNode {
		switch strings.ToLower(n.Data) {
		case "form":
			subscription |= subtreeHasForm
			if subscriptionAttributeMarker(n) || containsSubscriptionWord(attrValue(n, "action")) {
				subscription |= subtreeHasSubscriptionForm
			}
		case "input":
			if strings.EqualFold(strings.TrimSpace(attrValue(n, "type")), "email") {
				subscription |= subtreeHasEmail
			}
		}
	}
	if bodyBelow || subscription != 0 {
		state := a.nodeStates[n]
		if bodyBelow {
			state.discussionBody = 2
		}
		state.subscriptionEvidence = subscription
		a.nodeStates[n] = state
	}
	return bodyBelow || isDiscussionBodyContainer(n), subscription
}

func hasBlockDescendant(n *html.Node) bool {
	found := false
	for ch := n.FirstChild; ch != nil && !found; ch = ch.NextSibling {
		walk(ch, func(x *html.Node) bool {
			if dom.Hidden(x) {
				return false
			}
			if x.Type == html.ElementNode && isBlockTag(strings.ToLower(x.Data)) {
				found = true
				return false
			}
			return !found
		})
	}
	return found
}
func isBlockTag(tag string) bool {
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6", "p", "pre", "blockquote", "ul", "ol", "dl", "table", "figure", "hr":
		return true
	}
	return false
}
func hardHidden(n *html.Node) bool { return dom.Hidden(n) }

// hasMeaningfulVisual recognizes visuals that can produce useful output. It is
// deliberately stricter than the Markdown converter: selection must not make
// avatars, logos, icons, or tracking pixels eligible merely because they have
// alt text.
func hasMeaningfulVisual(n *html.Node) bool {
	found := false
	walk(n, func(x *html.Node) bool {
		if found {
			return false
		}
		// Check the opaque SVG representation before the generic hidden rule,
		// which intentionally hides every SVG subtree.
		if isVisualElement(x) && meaningfulVisual(x) {
			found = true
			return false
		}
		return !hardHidden(x)
	})
	return found
}

func isVisualElement(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(n.Data)
	return tag == "img" || (tag == "svg" && strings.EqualFold(attrValue(n, "role"), "img"))
}

func meaningfulVisual(n *html.Node) bool {
	if !isVisualElement(n) {
		return false
	}
	label := normalizeText(attrValue(n, "alt"))
	if strings.EqualFold(n.Data, "svg") {
		label = normalizeText(dom.AccessibleSVGLabel(n))
	} else if dom.Hidden(n) {
		return false
	}
	if label == "" {
		return false
	}
	if containsAny(strings.ToLower(label), "avatar", "logo", "icon") {
		return false
	}
	if strings.EqualFold(n.Data, "img") && explicitlyTinyImage(n) {
		return false
	}
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		tag := strings.ToLower(p.Data)
		if tag == "nav" || tag == "footer" || tag == "aside" {
			return false
		}
		if elementContainsAny(p, "author", "profile", "avatar", "logo", "icon", "social", "share", "sidebar", "tracking", "pixel", "related", "recommended") {
			return false
		}
	}
	return true
}

func explicitlyTinyImage(n *html.Node) bool {
	dimension := func(key string) int {
		value := strings.TrimSpace(attrValue(n, key))
		// Numeric HTML dimensions may have an optional CSS pixel suffix in
		// real-world markup. Other units and responsive values are inconclusive.
		value = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(value), "px"))
		size, err := strconv.Atoi(value)
		if err != nil || size <= 0 {
			return 0
		}
		return size
	}
	width, height := dimension("width"), dimension("height")
	if width > 0 && height > 0 {
		return width <= 32 && height <= 32
	}
	// A lone 1px-style dimension is still strong tracking-pixel evidence, while
	// one ordinary small dimension may describe a legitimate narrow diagram.
	return width > 0 && width <= 8 || height > 0 && height <= 8
}
