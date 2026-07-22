// Package pagemark extracts useful page content as safe Markdown.
//
// The output contains untrusted source data. It does not protect an agent from
// prompt injection. The package does not fetch pages or run JavaScript.
package pagemark

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/dom"
	"github.com/ryanfowler/pagemark/internal/markdown"
	"github.com/ryanfowler/readability"
	"golang.org/x/net/html"
)

type block struct {
	id         int
	node       *html.Node
	kind, text string
	score      float64
	selected   bool
	imageOnly  bool
	reasons    []string
}

type analysis struct {
	o                                               options
	root                                            *html.Node
	pageURL, base                                   *url.URL
	elements, attrs, attrBytes, textBytes, maxDepth int
	blocks                                          []block
	meta                                            metadata
	pageType                                        PageType
	pageTypeExplicit                                bool
	diag                                            *Diagnostics
	irrelevant                                      map[*html.Node]bool
	discussionBodyDescendants                       map[*html.Node]uint8
	articleCommentRegions, commentRecordCounts      map[*html.Node]uint8
	semanticArticleDescendants                      map[*html.Node]uint8
	semanticArticleBefore                           map[*html.Node]bool
	selfReferences                                  map[*html.Node]uint8
	microdataArticleRecords                         map[*html.Node]bool
	dominantMicrodataArticle, textListingPre        *html.Node
}

type metadata struct {
	title, browserTitle, socialTitle, description, author, site, language, published, canonical, schemaType string
	articlePublished, articleType, headline, microdataListing                                               bool
}

// Extract reads UTF-8 HTML and extracts useful content. Callers must decode
// input in other character encodings before calling Extract.
func Extract(input io.Reader, pageURL string, opts ...Option) (*Document, error) {
	o := applyOptions(opts)
	if input == nil {
		return nil, fmt.Errorf("pagemark: read input: %w", io.ErrUnexpectedEOF)
	}
	var source io.Reader = input
	if o.maxInput > 0 {
		source = io.LimitReader(input, o.maxInput+1)
	}
	data, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	if o.maxInput > 0 && int64(len(data)) > o.maxInput {
		return nil, &LimitError{"input bytes", int64(len(data)), o.maxInput}
	}
	// Extract accepts UTF-8. Callers with another encoding must decode it before
	// extraction; attempting to sniff here can misinterpret UTF-8 as Windows-1252
	// and can decode input a second time.
	root, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("pagemark: parse HTML: %w", err)
	}
	doc, err := extractNode(root, pageURL, o)
	if doc != nil {
		doc.Stats.InputBytes = len(data)
	}
	return doc, err
}

// ExtractBytes extracts useful content from UTF-8 HTML bytes.
func ExtractBytes(input []byte, pageURL string, opts ...Option) (*Document, error) {
	return Extract(bytes.NewReader(input), pageURL, opts...)
}

// ExtractNode extracts useful content from a parsed HTML tree. It does not change root.
// The caller must not change root during extraction.
func ExtractNode(root *html.Node, pageURL string, opts ...Option) (*Document, error) {
	return extractNode(root, pageURL, applyOptions(opts))
}

func applyOptions(opts []Option) options {
	o := defaultOptions()
	for _, f := range opts {
		if f != nil {
			f(&o)
		}
	}
	if o.maxInput < 0 {
		o.maxInput = 0
	}
	if o.maxOutput < 0 {
		o.maxOutput = 0
	}
	return o
}

func extractNode(root *html.Node, rawURL string, o options) (*Document, error) {
	if root == nil {
		return nil, ErrNoContent
	}
	var page *url.URL
	if rawURL != "" {
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, ErrInvalidURL
		}
		page = u
	}
	a := &analysis{o: o, root: root, pageURL: page, base: page, irrelevant: make(map[*html.Node]bool)}
	if o.diagnostics {
		a.diag = &Diagnostics{ProfileVersion: "1", Fallback: "primary"}
	}
	if err := a.index(root, 0); err != nil {
		return nil, err
	}
	a.findBase()
	a.extractMetadata()
	a.segment(root, false)
	a.detectTextListingPre()
	pageType, confidence, candidates := a.inferType()
	if o.pageType != "" {
		pageType = o.pageType
		confidence = 1
		a.pageTypeExplicit = true
	}
	if a.diag != nil {
		a.diag.PageCandidates = candidates
	}
	// Auxiliary-region detection has a small number of article-specific rules.
	// Record the final type before scoring so those regions are hard exclusions,
	// rather than relying on score penalties that long card copy can overcome.
	a.pageType = pageType
	a.score(pageType)
	selected, repeatedExcluded, repeatedDropped := a.selectedNodes(pageType)
	fallback := "primary"
	if len(selected) == 0 {
		selected = a.semanticFallback()
		repeatedExcluded = nil
		repeatedDropped = 0
		fallback = "semantic-main"
	}
	quality := a.quality(selected)
	if (pageType == PageTypeArticle || pageType == PageTypeGeneric) && quality < .42 {
		if article, err := readability.ParseNode(root, rawURL, nil); err == nil && article.Node != nil && len(article.TextContent) > 100 {
			selected = []*html.Node{article.Node}
			repeatedExcluded = nil
			repeatedDropped = 0
			fallback = "readability"
			quality = math.Max(quality, .58)
		}
	}
	if len(selected) == 0 {
		selected = a.highRecall()
		repeatedExcluded = nil
		repeatedDropped = 0
		fallback = "high-recall"
		quality = .2
	}
	if len(selected) == 0 && a.meta.description != "" {
		selected = metadataNodes(a.meta)
		repeatedExcluded = nil
		repeatedDropped = 0
		fallback = "metadata"
		quality = .15
	}
	if len(selected) == 0 {
		return nil, ErrNoContent
	}
	exclude := func(n *html.Node) bool {
		discussionAuxiliary := pageType == PageTypeDiscussion &&
			(isDiscussionControlNode(n) || a.hasStandaloneMessageAncestor(n))
		visualAuxiliary := o.includeImages && isVisualElement(n) && !meaningfulVisual(n)
		return a.isIrrelevantNode(n) || repeatedExcluded[n] || discussionAuxiliary || visualAuxiliary
	}
	cfg := markdown.Config{Base: a.base, Links: o.includeLinks, Images: o.includeImages, Tables: o.includeTables, MaxLinks: o.maxLinks, MaxImages: o.maxImages, MaxTableCells: o.maxTableCells, MaxBytes: o.maxOutput, Policy: markdown.URLPolicy{Schemes: append([]string(nil), o.urlPolicy.Schemes...), AllowMailto: o.urlPolicy.AllowMailto, MaxLength: o.urlPolicy.MaxLength, StripTracking: o.urlPolicy.StripTracking}, Exclude: exclude, PruneEmptyHeadings: true}
	if a.textListingPre != nil {
		cfg.TextPreformatted = func(n *html.Node) bool { return n == a.textListingPre }
	}
	selected = a.ensureDocumentTitle(selected, cfg, pageType)
	mr := markdown.Convert(selected, cfg)
	if strings.TrimSpace(mr.Text) == "" {
		return nil, ErrNoContent
	}
	doc := &Document{URL: rawURL, CanonicalURL: a.meta.canonical, Title: a.meta.title, Description: a.meta.description, Author: a.meta.author, SiteName: a.meta.site, Language: a.meta.language, PublishedTime: a.meta.published, PageType: pageType, PageTypeScore: confidence, Markdown: mr.Markdown, Text: mr.Text, Quality: clamp(quality), Diagnostics: a.diag, Stats: Stats{Elements: a.elements, TextBytes: a.textBytes, Blocks: len(a.blocks), OutputBytes: len(mr.Markdown)}}
	for _, l := range mr.Links {
		doc.Links = append(doc.Links, Link{Text: l.Text, URL: l.URL})
	}
	for _, im := range mr.Images {
		doc.Images = append(doc.Images, Image{Alt: im.Alt, URL: im.URL})
	}
	doc.Stats.SelectedBlocks = mr.EmittedBlocks
	if repeatedDropped > 0 {
		doc.Warnings = append(doc.Warnings, Warning{"repeated-items-truncated", fmt.Sprintf("The repeated-item limit dropped %d selected content items.", repeatedDropped)})
	}
	if mr.Truncated {
		doc.Warnings = append(doc.Warnings, Warning{"output-truncated", "The output reached the configured byte limit."})
	}
	if fallback != "primary" {
		doc.Warnings = append(doc.Warnings, Warning{"fallback", "The " + fallback + " fallback produced the result."})
	}
	if a.diag != nil {
		a.diag.Fallback = fallback
		a.diag.RejectedLinks = mr.Rejected
	}
	for _, section := range mr.Sections {
		doc.Sections = append(doc.Sections, Section{Heading: section.Heading, Text: section.Text})
	}
	if !o.includeMetadata {
		doc.Title = ""
		doc.Description = ""
		doc.Author = ""
		doc.SiteName = ""
		doc.Language = ""
		doc.PublishedTime = ""
		doc.CanonicalURL = ""
	}
	if o.logger != nil {
		o.logger.Debug("extracted page", "type", pageType, "quality", doc.Quality, "blocks", len(a.blocks), "selected", doc.Stats.SelectedBlocks)
	}
	return doc, nil
}

func (a *analysis) index(n *html.Node, depth int) error {
	if depth > a.maxDepth {
		a.maxDepth = depth
	}
	if a.o.maxDepth > 0 && depth > a.o.maxDepth {
		return &LimitError{"DOM depth", int64(depth), int64(a.o.maxDepth)}
	}
	if n.Type == html.ElementNode {
		a.elements++
		if a.o.maxElements > 0 && a.elements > a.o.maxElements {
			return &LimitError{"elements", int64(a.elements), int64(a.o.maxElements)}
		}
		a.attrs += len(n.Attr)
		for _, x := range n.Attr {
			a.attrBytes += len(x.Key) + len(x.Val)
		}
		if a.o.maxAttributes > 0 && a.attrs > a.o.maxAttributes {
			return &LimitError{"attributes", int64(a.attrs), int64(a.o.maxAttributes)}
		}
		if a.o.maxAttributeBytes > 0 && a.attrBytes > a.o.maxAttributeBytes {
			return &LimitError{"attribute bytes", int64(a.attrBytes), int64(a.o.maxAttributeBytes)}
		}
	}
	if n.Type == html.TextNode {
		a.textBytes += len(n.Data)
		if a.o.maxText > 0 && a.textBytes > a.o.maxText {
			return &LimitError{"text bytes", int64(a.textBytes), int64(a.o.maxText)}
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if err := a.index(ch, depth+1); err != nil {
			return err
		}
	}
	return nil
}

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
			a.blocks = append(a.blocks, block{id: len(a.blocks) + 1, node: n, kind: "image", imageOnly: true})
			return
		}
		// Forum software often puts a post's prose directly in a generic div,
		// using <br> (and occasionally <hr>) rather than paragraphs. Prefer the
		// innermost explicitly marked body over its wrappers and enclosing table.
		hasPostBody := a.hasDiscussionBodyDescendant(n)
		if isDiscussionBodyContainer(n) && !hasPostBody {
			text := normalizeText(nodeText(n))
			if text != "" {
				a.blocks = append(a.blocks, block{id: len(a.blocks) + 1, node: n, kind: "generic", text: text})
				return
			}
		}
		if isBlockTag(tag) && !hasPostBody {
			text := normalizeText(nodeText(n))
			imageOnly := text == "" && a.o.includeImages && hasMeaningfulVisual(n)
			if text != "" || tag == "hr" || imageOnly {
				a.blocks = append(a.blocks, block{id: len(a.blocks) + 1, node: n, kind: tag, text: text, imageOnly: imageOnly})
				return
			}
		}
		if isGenericContainer(tag) && !hasPostBody && !hasBlockDescendant(n) {
			text := normalizeText(nodeText(n))
			if utf8.RuneCountInString(text) >= 12 {
				a.blocks = append(a.blocks, block{id: len(a.blocks) + 1, node: n, kind: "generic", text: text})
				return
			}
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		a.segment(ch, excluded)
	}
}

// detectTextListingPre identifies old text-mode interfaces whose primary UI is
// a preformatted archive. A large pre alone is intentionally insufficient:
// links, repeated lines, archive metadata, dominance, and little outside prose
// must agree before code rendering is disabled.
func (a *analysis) detectTextListingPre() {
	total := 0
	for _, b := range a.blocks {
		total += utf8.RuneCountInString(b.text)
	}
	if total == 0 {
		return
	}
	hints := strings.ToLower(strings.Join([]string{a.meta.title, a.meta.description, a.meta.schemaType, a.meta.canonical}, " "))
	if a.pageURL != nil {
		hints += " " + strings.ToLower(a.pageURL.Path)
	}
	archiveHint := containsAny(hints, "archive", "inbox", "mailing list", "message list")

	for _, b := range a.blocks {
		if b.kind != "pre" {
			continue
		}
		chars := utf8.RuneCountInString(b.text)
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
	if state := a.discussionBodyDescendants[n]; state != 0 {
		return state == 2
	}
	if a.discussionBodyDescendants == nil {
		a.discussionBodyDescendants = make(map[*html.Node]uint8)
	}
	// Mark false before descending. HTML trees cannot normally cycle, but doing
	// so also prevents malformed caller-built trees from recursing forever.
	a.discussionBodyDescendants[n] = 1
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if hardHidden(ch) {
			continue
		}
		if isDiscussionBodyContainer(ch) || a.hasDiscussionBodyDescendant(ch) {
			a.discussionBodyDescendants[n] = 2
			return true
		}
	}
	return false
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
		if containsAny(elementTokens(p), "author", "profile", "avatar", "logo", "icon", "social", "share", "sidebar", "tracking", "pixel", "related", "recommended") {
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

func (a *analysis) score(pt PageType) {
	seen := map[string]bool{}
	for i := range a.blocks {
		b := &a.blocks[i]
		length := utf8.RuneCountInString(b.text)
		score := 0.0
		switch b.kind {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			score = 1.8
		case "p":
			score = .7 + math.Min(2, float64(length)/180)
		case "pre", "table", "dl":
			score = 2.4
		case "ul", "ol":
			score = 1.3
		case "blockquote", "figure":
			score = 1.1
		case "image":
			score = .7
		case "generic":
			score = 0.4 + math.Min(2, float64(length)/250)
		}
		b.reasons = append(b.reasons, "content shape")
		if b.imageOnly {
			// Descriptive image-only paragraphs have no text length with which to
			// earn the normal prose score. The remaining ancestry and boilerplate
			// signals still decide whether this is primary content.
			score += .4
			b.reasons = append(b.reasons, "descriptive image")
		}
		if a.hasIrrelevantAncestor(b.node) {
			score -= 8
			b.reasons = append(b.reasons, "auxiliary content")
		}
		links, total := linkTextLength(b.node), max(1, length)
		density := float64(links) / float64(total)
		for p := b.node.Parent; p != nil; p = p.Parent {
			if p.Type != html.ElementNode {
				continue
			}
			tag := strings.ToLower(p.Data)
			tokens := elementTokens(p)
			if tag == "main" {
				score += 2
				b.reasons = append(b.reasons, "inside main")
			}
			if tag == "article" {
				score += 1.3
			}
			if tag == "header" || tag == "footer" || tag == "nav" {
				score -= 5
				b.reasons = append(b.reasons, "inside page chrome")
			}
			if tag == "aside" {
				score -= 1.5
			}
			if hasBoilerplateToken(p) {
				score -= 3
				b.reasons = append(b.reasons, "boilerplate label")
			}
			if pt == PageTypeDiscussion && containsAny(tokens, "comment", "answer", "post", "thread") {
				score += 2
			}
			if (pt == PageTypeListing || pt == PageTypeCollection) && containsAny(tokens, "card", "item", "result", "product") {
				score += 1.5
			}
		}
		if b.kind == "p" && (pt == PageTypeArticle || pt == PageTypeDocumentation || pt == PageTypeDiscussion || pt == PageTypeProduct || pt == PageTypeService) {
			score += 0.35
		}
		if pt == PageTypeDiscussion && isDiscussionBodyContainer(b.node) {
			score += 3
			b.reasons = append(b.reasons, "discussion post body")
		}
		if pt == PageTypeDiscussion && a.hasStandaloneMessageAncestor(b.node) {
			// A standalone .message is a UI notice, not a message-body convention.
			// Make this absolute rather than relative: deeply nested thread/main
			// context must not raise it above the selection threshold.
			score = -8
			b.reasons = append(b.reasons, "discussion notice")
		}
		if pt == PageTypeDiscussion && !isDiscussionBodyContainer(b.node) && isDiscussionControlBlock(b.node) {
			score -= 6
			b.reasons = append(b.reasons, "discussion controls")
		}
		if density > .75 && pt != PageTypeListing && pt != PageTypeCollection && pt != PageTypeDocumentation {
			score -= 2
			b.reasons = append(b.reasons, "high link density")
		}
		if controls(b.node) > 2 {
			score -= 2
		}
		hash := strings.ToLower(normalizeText(b.text))
		if seen[hash] && len(hash) > 30 {
			score -= 4
			b.reasons = append(b.reasons, "duplicate")
		}
		seen[hash] = true
		if a.o.favorPrecision {
			score -= .35
		}
		if a.o.favorRecall {
			score += .35
		}
		b.score = score
		b.selected = score >= 1.0
		if a.diag != nil {
			text := b.text
			if len(text) > 160 {
				text = text[:160]
			}
			a.diag.Blocks = append(a.diag.Blocks, BlockDiagnostic{ID: b.id, Kind: b.kind, Text: text, Score: score, Selected: b.selected, Reasons: append([]string(nil), b.reasons...)})
		}
	}
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
	if title == "" || a.hasEquivalentHeading(nodes, title, cfg) || a.hasLeadingOutputHeading(nodes, cfg) || !a.hasDominantProseOutput(nodes, cfg) {
		return nodes
	}
	titleNode := articleTitleNode(title)
	a.irrelevant[titleNode] = false
	withTitle := append([]*html.Node{titleNode}, nodes...)
	if !titleLeavesOutputForContent(withTitle, cfg) {
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
	bestEquivalent, bestCredible := false, false
	for i := range a.blocks {
		b := &a.blocks[i]
		if (b.kind != "h1" && b.kind != "h2") || hardHidden(b.node) || a.hasIrrelevantAncestor(b.node) {
			continue
		}
		distance := adjacentSelectedBlockDistance(a.blocks, i, nodes, 2)
		if distance == 0 {
			// Proximity is required even when the text matches metadata. Otherwise
			// a headline from a recommendation or another article could win.
			continue
		}
		equivalent := restorationTitle != "" && titleEquivalent(b.text, restorationTitle, a.meta.site)
		credible := a.isCredibleArticleHeading(i, nodes)
		// A conflicting heading is authoritative only with independent structural
		// evidence. This prevents an adjacent site masthead from replacing the
		// metadata fallback. H2 also requires such evidence when metadata is absent;
		// proximity alone must not turn an ordinary section heading into a title.
		if (restorationTitle != "" && !equivalent && !credible) || (b.kind == "h2" && !equivalent && !credible) {
			continue
		}
		if bestIndex < 0 || (credible && !bestCredible) || (credible == bestCredible && equivalent && !bestEquivalent) || (credible == bestCredible && equivalent == bestEquivalent && distance < bestDistance) {
			bestIndex, bestDistance, bestEquivalent, bestCredible = i, distance, equivalent, credible
		}
	}
	if bestIndex >= 0 {
		candidate := &a.blocks[bestIndex]
		if candidate.kind == "h1" && representedBySelection(candidate.node, nodes) {
			return nodes
		}

		// Render an h2 article headline as the document's h1. Use a detached node
		// rather than mutating the caller's DOM, and remove the original selected
		// h2 so the title is not duplicated.
		title := candidate.node
		content := nodes
		if candidate.kind == "h2" {
			title = articleTitleNode(candidate.text)
			a.irrelevant[title] = false
			content = removeSelectedNode(content, candidate.node)
			// A selected h1 before an h2 headline is usually the page masthead. Drop
			// only unsupported, metadata-conflicting h1 blocks before the candidate.
			for i := 0; i < bestIndex; i++ {
				b := &a.blocks[i]
				if b.kind == "h1" && !titleEquivalent(b.text, restorationTitle, a.meta.site) && !a.isCredibleArticleHeading(i, nodes) {
					content = removeSelectedNode(content, b.node)
				}
			}
		}
		withTitle := append([]*html.Node{title}, content...)
		if titleLeavesOutputForContent(withTitle, cfg) {
			return withTitle
		}
		// Do not replace an omitted source title with metadata: either title
		// would consume budget intended for the article body.
		return nodes
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

	title := articleTitleNode(restorationTitle)
	// Synthetic nodes are not part of the indexed DOM. Explicitly mark this one
	// as relevant so article auxiliary heuristics cannot classify it by itself.
	a.irrelevant[title] = false
	withTitle := append([]*html.Node{title}, content...)
	if !titleLeavesOutputForContent(withTitle, cfg) {
		return nodes
	}
	return withTitle
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

func titleLeavesOutputForContent(nodes []*html.Node, cfg markdown.Config) bool {
	if cfg.MaxBytes <= 0 {
		return true
	}
	// Rendering stops at the first block that exceeds the budget. Keep a title
	// only when a paragraph, list, code block, or other substantive block also
	// survives; another heading alone is not article content.
	return markdown.Convert(nodes, cfg).EmittedContentBlocks > 0
}

// restorationTitle returns a document-specific metadata title. Social titles
// are preferred because they normally omit browser chrome. For a plain
// <title>, a site prefix or suffix is removed only when it agrees with explicit
// site metadata or the page hostname.
func (a *analysis) restorationTitle() string {
	title := firstNonempty(a.meta.socialTitle, a.meta.title)
	if title == "" {
		return ""
	}
	sites := []string{a.meta.site}
	if a.pageURL != nil {
		host := strings.TrimPrefix(strings.ToLower(a.pageURL.Hostname()), "www.")
		if host != "" {
			sites = append(sites, host)
			if dot := strings.IndexByte(host, '.'); dot > 0 {
				sites = append(sites, host[:dot])
			}
		}
	}
	for _, site := range sites {
		if normalizedLabel(site) == "" {
			continue
		}
		if normalizedLabel(title) == normalizedLabel(site) {
			return ""
		}
		if stripped := stripTitleDecorationPreservingCase(title, site); stripped != title {
			title = stripped
			break
		}
	}
	normalized := normalizedLabel(title)
	if normalized == "" || utf8.RuneCountInString(title) > 180 || genericDocumentTitle(normalized) {
		return ""
	}
	// A browser-only title at the origin root is usually the publication or
	// product name. Stronger social metadata remains eligible there.
	if a.meta.socialTitle == "" && a.pageURL != nil && (a.pageURL.Path == "" || a.pageURL.Path == "/") && title == a.meta.browserTitle {
		return ""
	}
	return normalizeText(title)
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
	seen := map[*html.Node]bool{}
	regions := map[*html.Node]int{}
	total, paragraphs := 0, 0
	for _, root := range nodes {
		walk(root, func(n *html.Node) bool {
			if hardHidden(n) || a.hasIrrelevantAncestor(n) || (cfg.Exclude != nil && cfg.Exclude(n)) {
				return false
			}
			if seen[n] || n.Type != html.ElementNode {
				return true
			}
			seen[n] = true
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
	// A heading inside the selected article is structural headline evidence even
	// when the publisher did not add schema attributes. Outside an article,
	// require an explicit schema or conventional article-headline marker so a
	// page masthead in <main> cannot override metadata.
	if !strings.EqualFold(region.Data, "article") && !hasArticleHeadlineMarker(heading) {
		return false
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
		if strings.EqualFold(p.Data, "header") && !strings.EqualFold(region.Data, "article") && !containsAny(elementTokens(p), "article", "post", "entry", "story") {
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
		for _, value := range strings.Fields(attrValue(n, key)) {
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

func isSubstantiveProseBlock(b *block) bool {
	switch b.kind {
	case "p", "blockquote", "generic":
		return utf8.RuneCountInString(normalizeText(b.text)) >= 40
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

func (a *analysis) selectedNodes(pageType PageType) (nodes []*html.Node, excluded map[*html.Node]bool, dropped int) {
	// A large number of sibling blocks is normal in prose. Repetition limits
	// are only meaningful for records on pages identified as listings or
	// collections.
	limitRecords := a.o.maxRepeated > 0 && (pageType == PageTypeListing || pageType == PageTypeCollection)
	if !limitRecords {
		for i := range a.blocks {
			if a.blocks[i].selected {
				nodes = append(nodes, a.blocks[i].node)
			}
		}
		return nodes, nil, 0
	}

	excluded = make(map[*html.Node]bool)
	accepted := make(map[*html.Node]bool)
	rejected := make(map[*html.Node]bool)
	recordCounts := make(map[*html.Node]int)
	acceptRecord := func(record *html.Node) bool {
		if record == nil || record.Parent == nil {
			return true
		}
		if accepted[record] {
			return true
		}
		if rejected[record] {
			return false
		}
		if recordCounts[record.Parent] >= a.o.maxRepeated {
			rejected[record] = true
			dropped++
			return false
		}
		accepted[record] = true
		recordCounts[record.Parent]++
		return true
	}

	for i := range a.blocks {
		b := &a.blocks[i]
		if !b.selected {
			continue
		}
		if !acceptRecord(listingRecord(b.node)) {
			continue
		}
		// Lists and tables are segmented as single blocks. Limit their marked
		// li/tr records in place through the converter's exclusion hook.
		for _, record := range a.descendantListingRecords(b.node) {
			if !acceptRecord(record) {
				excluded[record] = true
			}
		}
		nodes = append(nodes, b.node)
	}
	return nodes, excluded, dropped
}

// listingRecord finds an explicitly marked record container. Restricting this
// to container elements avoids treating prose headings such as class=item-title
// as independent records.
func listingRecord(n *html.Node) *html.Node {
	for p := n; p != nil; p = p.Parent {
		if isListingRecordElement(p) {
			return p
		}
	}
	return nil
}

func isListingRecordElement(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !containsAny(elementTokens(n), "card", "result", "item", "product", "record") {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "div", "article", "section", "li", "tr", "a", "figure":
		return true
	}
	return false
}

func (a *analysis) descendantListingRecords(n *html.Node) (records []*html.Node) {
	var visit func(*html.Node)
	visit = func(parent *html.Node) {
		for ch := parent.FirstChild; ch != nil; ch = ch.NextSibling {
			if hardHidden(ch) || a.isIrrelevantNode(ch) {
				continue
			}
			if isListingRecordElement(ch) {
				records = append(records, ch)
				continue
			}
			visit(ch)
		}
	}
	visit(n)
	return records
}
func (a *analysis) semanticFallback() []*html.Node {
	var main *html.Node
	walk(a.root, func(n *html.Node) bool {
		if main == nil && n.Type == html.ElementNode && (strings.EqualFold(n.Data, "main") || strings.EqualFold(attrValue(n, "role"), "main")) {
			main = n
		}
		return main == nil
	})
	if main == nil {
		return nil
	}
	return []*html.Node{main}
}
func (a *analysis) highRecall() []*html.Node {
	var out []*html.Node
	for i := range a.blocks {
		b := &a.blocks[i]
		bad := false
		for p := b.node; p != nil; p = p.Parent {
			if p.Type == html.ElementNode {
				t := strings.ToLower(p.Data)
				if t == "header" || t == "footer" || t == "nav" || hasBoilerplateToken(p) {
					bad = true
					break
				}
			}
		}
		if !bad {
			out = append(out, b.node)
		}
	}
	return out
}
func (a *analysis) quality(nodes []*html.Node) float64 {
	if len(nodes) == 0 {
		return 0
	}
	chars := 0
	links := 0
	for _, n := range nodes {
		t := normalizeText(nodeText(n))
		chars += utf8.RuneCountInString(t)
		links += linkTextLength(n)
	}
	q := .35 + math.Min(.4, float64(chars)/1500)
	if chars > 0 && float64(links)/float64(chars) > .8 {
		q -= .25
	}
	if len(nodes) > 100 {
		q -= .1
	}
	return clamp(q)
}

func (a *analysis) inferType() (PageType, float64, []PageCandidate) {
	scores := map[PageType]float64{
		PageTypeArticle: 0, PageTypeDocumentation: 0, PageTypeDiscussion: 0,
		PageTypeProduct: 0, PageTypeListing: 0, PageTypeCollection: 0,
		PageTypeService: 0, PageTypeGeneric: 1,
	}
	schema := strings.ToLower(a.meta.schemaType)
	urlPath := ""
	if a.pageURL != nil {
		urlPath = strings.ToLower(a.pageURL.Path)
	}
	counts := map[string]int{}
	productRecords := map[*html.Node]bool{}
	linkedRecords := map[*html.Node]bool{}
	discussionRecords := map[*html.Node]bool{}
	discussionContext := false
	documentationContext := false
	proseChars, codeChars, primaryArticleProse := 0, 0, 0
	primaryArticles := map[*html.Node]bool{}
	for _, b := range a.blocks {
		// Recommendations are page furniture, not records belonging to the page's
		// subject. In particular, do not let every heading and excerpt in a card
		// grid cast another vote for a listing classification.
		if a.inferenceAuxiliaryBlock(b.node) || a.hasMicrodataArticleRecordAncestor(b.node) {
			continue
		}
		counts[b.kind]++
		article := a.primaryArticleAncestor(b.node)
		if article == nil && nodeWithin(b.node, a.dominantMicrodataArticle) {
			article = a.dominantMicrodataArticle
		}
		if article != nil {
			primaryArticles[article] = true
			if b.kind == "p" {
				primaryArticleProse += utf8.RuneCountInString(b.text)
			}
		}
		switch b.kind {
		case "p":
			proseChars += utf8.RuneCountInString(b.text)
		case "pre":
			codeChars += utf8.RuneCountInString(b.text)
		}
		tok := elementTokens(b.node)
		for p := b.node.Parent; p != nil; p = p.Parent {
			if p.Type == html.ElementNode {
				tok += " " + elementTokens(p)
			}
		}
		// A comment or post class is also commonly used for annotations and blog
		// article wrappers. Count distinct records rather than every block below
		// such a wrapper, and treat unambiguous discussion vocabulary separately.
		if record := nearestTokenAncestor(b.node, "comment", "answer", "post", "reply", "message"); record != nil {
			discussionRecords[record] = true
		}
		if containsAny(tok, "answer", "thread", "discussion", "topic", "reply", "message") {
			discussionContext = true
		}
		if containsAny(tok, "product", "price", "sku") {
			scores[PageTypeProduct] += 1.5
			if record := nearestTokenAncestor(b.node, "product", "sku"); record != nil {
				productRecords[record] = true
			}
		}
		if containsAny(tok, "docs", "documentation", "api", "reference") {
			documentationContext = true
		}
		if containsAny(tok, "card", "result", "listing", "item") {
			scores[PageTypeListing]++
			if record := nearestTokenAncestor(b.node, "card", "result", "item"); record != nil {
				linkedRecords[record] = true
			}
		}
	}
	if discussionContext {
		scores[PageTypeDiscussion] += 2
	}
	switch len(discussionRecords) {
	case 0:
	case 1:
		scores[PageTypeDiscussion] += .5
	default:
		// Repeated comment-like records are useful evidence, but are capped so
		// annotations cannot overwhelm publication and dominant-prose signals.
		scores[PageTypeDiscussion] += math.Min(4, float64(len(discussionRecords)))
	}
	if documentationContext {
		// Ancestor tokens describe one region, not each descendant block. An
		// explicit documentation container is nevertheless strong page-level
		// evidence, including on sites that use neutral /guide/ URLs.
		scores[PageTypeDocumentation] += 3
	}
	sectionCount := 0
	walk(a.root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "section") {
			sectionCount++
		}
		return true
	})
	if sectionCount >= 3 {
		scores[PageTypeService] += 3
	}
	if len(productRecords) >= 4 {
		scores[PageTypeCollection] += 2 * float64(len(productRecords))
	}
	if len(linkedRecords) >= 4 {
		scores[PageTypeListing] += 2
	}
	if a.meta.articleType || strings.Contains(schema, "article") || strings.Contains(schema, "news") {
		scores[PageTypeArticle] += 5
	}
	if strings.Contains(schema, "product") {
		scores[PageTypeProduct] += 5
	}
	if strings.Contains(schema, "discussion") || strings.Contains(schema, "question") {
		scores[PageTypeDiscussion] += 5
	}
	if strings.Contains(schema, "itemlist") || a.meta.microdataListing {
		scores[PageTypeListing] += 5
	}
	if a.textListingPre != nil {
		// Text-mode archives have few of the card/list elements used by modern
		// listings, so their combined pre/link/record evidence is page-level.
		scores[PageTypeListing] += 10
	}
	// Prefer a canonical path when present: the supplied URL may be an archive,
	// redirect, or tracking URL that says little about the page itself.
	if canonical, err := url.Parse(a.meta.canonical); err == nil && canonical.Path != "" {
		urlPath = strings.ToLower(canonical.Path)
	}
	title := strings.ToLower(a.meta.title)
	if counts["pre"] > 1 {
		// Code is common in technical articles. It is strong documentation
		// evidence only when it dominates the prose structure.
		if counts["p"] <= 2 || codeChars > proseChars {
			scores[PageTypeDocumentation] += 2
		} else {
			scores[PageTypeDocumentation] += .5
		}
	}
	if counts["table"] > 0 {
		scores[PageTypeProduct]++
		scores[PageTypeDocumentation]++
	}
	// Paragraph volume is ambiguous inside an explicit documentation region;
	// guides should not become articles merely because they explain a topic in
	// prose. Strong article metadata and structure below can still prevail.
	if !documentationContext && counts["p"] > 4 {
		scores[PageTypeArticle] += 2
	}
	if !documentationContext && counts["p"] >= 4 && proseChars >= 600 && proseChars >= codeChars {
		scores[PageTypeArticle] += 2
	}
	if len(primaryArticles) == 1 && (counts["p"] >= 2 || primaryArticleProse >= 120) {
		scores[PageTypeArticle] += 2
	}
	// A headline attached to a real prose region is much stronger than headings
	// repeated by cards. Require body text so a bare schema template does not
	// turn an archive into an article.
	if a.meta.headline && (primaryArticleProse >= 120 || proseChars >= 300) {
		scores[PageTypeArticle] += 2
	}
	if primaryArticleProse >= 300 {
		scores[PageTypeArticle] += 2
	}
	if a.meta.articlePublished {
		// Generic <time> elements occur on comments, products, and events. Only
		// publication metadata with article-specific provenance gets this bonus.
		scores[PageTypeArticle] += 4
	}
	if strings.Contains(urlPath, "/docs") || strings.Contains(urlPath, "/api") {
		scores[PageTypeDocumentation] += 3
	}
	if containsAny(title, "documentation", "reference") || (containsAny(title, "api") && containsAny(title, "guide", "reference")) {
		scores[PageTypeDocumentation] += 2
	}
	if articleURLPath(urlPath) {
		scores[PageTypeArticle] += 2
	}
	if strings.Contains(urlPath, "forum") || strings.Contains(urlPath, "question") || strings.Contains(urlPath, "issue") || strings.Contains(urlPath, "/t/") || strings.Contains(title, " forum") {
		scores[PageTypeDiscussion] += 3
	}
	if strings.Contains(urlPath, "product") {
		scores[PageTypeProduct] += 3
	}
	var cs []PageCandidate
	for t, s := range scores {
		cs = append(cs, PageCandidate{t, s})
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Score == cs[j].Score {
			return cs[i].Type < cs[j].Type
		}
		return cs[i].Score > cs[j].Score
	})
	top := cs[0]
	second := 0.0
	if len(cs) > 1 {
		second = cs[1].Score
	}
	confidence := .5 + (top.Score-second)/(2*math.Max(1, top.Score))
	return top.Type, clamp(confidence), cs
}

func (a *analysis) extractMetadata() {
	m := metadata{}
	microdataEntities, repeatedMicrodataArticles, microdataRecords, dominantMicrodata := a.pageMicrodataEntities(a.root)
	m.microdataListing = repeatedMicrodataArticles
	a.microdataArticleRecords = microdataRecords
	a.dominantMicrodataArticle = dominantMicrodata
	walk(a.root, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		tag := strings.ToLower(n.Data)
		if tag == "html" {
			m.language = attrValue(n, "lang")
		}
		if tag == "title" {
			value := normalizeText(nodeText(n))
			if m.browserTitle == "" {
				m.browserTitle = value
			}
			if m.title == "" {
				m.title = value
			}
		} else if tag == "h1" && m.title == "" {
			m.title = normalizeText(nodeText(n))
		}
		itemprop := strings.ToLower(attrValue(n, "itemprop"))
		itemtype := strings.ToLower(attrValue(n, "itemtype"))
		pageEntity := microdataEntities[n]
		if itemtype != "" && pageEntity && m.schemaType == "" {
			m.schemaType = itemtype
		}
		if pageEntity && containsAny(itemtype, "article", "newsarticle", "blogposting") {
			m.articleType = true
		}
		if containsAny(itemprop, "headline") && isPageMicrodataProperty(n, microdataEntities) {
			m.headline = true
			if m.title == "" {
				m.title = normalizeText(firstNonempty(attrValue(n, "content"), nodeText(n)))
			}
		}
		if itemprop == "name" && hasAncestorItemprop(n, "author") {
			if visible := normalizeText(nodeText(n)); visible != "" {
				m.author = visible
			}
		}
		if (tag == "time" || itemprop == "datepublished") && m.published == "" {
			m.published = firstNonempty(attrValue(n, "datetime"), attrValue(n, "content"), normalizeText(nodeText(n)))
		}
		if tag == "meta" {
			key := strings.ToLower(firstNonempty(attrValue(n, "property"), attrValue(n, "name"), attrValue(n, "itemprop")))
			v := normalizeText(attrValue(n, "content"))
			switch key {
			case "description", "og:description", "twitter:description":
				if m.description == "" {
					m.description = v
				}
			case "author", "article:author":
				if m.author == "" {
					m.author = v
				}
			case "og:site_name":
				m.site = v
			case "article:published_time":
				if v != "" {
					m.published = v
					m.articlePublished = true
				}
			case "datepublished":
				if m.published == "" {
					m.published = v
				}
			case "og:title", "twitter:title":
				if m.socialTitle == "" {
					m.socialTitle = v
				}
				if m.title == "" {
					m.title = v
				}
			case "og:type":
				m.schemaType = v
				if strings.EqualFold(v, "article") || strings.Contains(strings.ToLower(v), "article") {
					m.articleType = true
				}
			}
		}
		if tag == "link" && containsAny(strings.ToLower(attrValue(n, "rel")), "canonical") {
			m.canonical = a.resolveMetadataURL(attrValue(n, "href"))
		}
		if tag == "script" && strings.Contains(strings.ToLower(attrValue(n, "type")), "ld+json") {
			a.readJSONLD(rawNodeText(n), &m)
		}
		return true
	})
	a.meta = m
}
func (a *analysis) readJSONLD(raw string, m *metadata) {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return
	}

	// Resolve @id references used by a page entity's mainEntity without treating
	// every sibling in @graph as page-level metadata.
	entities := map[string]map[string]any{}
	var index func(any)
	index = func(x any) {
		switch z := x.(type) {
		case []any:
			for _, q := range z {
				index(q)
			}
		case map[string]any:
			if id, ok := z["@id"].(string); ok && id != "" {
				// JSON-LD permits one entity to be split across several node objects.
				// Merge complementary properties so resolution is independent of the
				// order of full entities, partial entities, and @id-only references.
				if existing := entities[id]; existing == nil {
					entities[id] = z
				} else {
					for key, value := range z {
						if _, exists := existing[key]; !exists {
							existing[key] = value
						}
					}
				}
			}
			for _, q := range z {
				index(q)
			}
		}
	}
	index(v)

	activeIDs := map[string]bool{}
	var visit func(any, bool)
	visit = func(x any, pageEntity bool) {
		switch z := x.(type) {
		case []any:
			for _, q := range z {
				visit(q, pageEntity)
			}
		case map[string]any:
			var typeNames []string
			switch types := z["@type"].(type) {
			case string:
				typeNames = append(typeNames, types)
			case []any:
				for _, value := range types {
					if name, ok := value.(string); ok {
						typeNames = append(typeNames, name)
					}
				}
			}
			articleType := false
			for _, typeName := range typeNames {
				if strings.Contains(strings.ToLower(typeName), "article") || strings.EqualFold(typeName, "BlogPosting") {
					articleType = true
				}
			}
			if pageEntity && len(typeNames) > 0 && m.schemaType == "" {
				m.schemaType = typeNames[0]
			}
			if pageEntity && articleType {
				m.articleType = true
			}
			if m.author == "" {
				switch au := z["author"].(type) {
				case string:
					m.author = normalizeText(au)
				case map[string]any:
					if s, ok := au["name"].(string); ok {
						m.author = normalizeText(s)
					}
				}
			}
			if s, ok := z["datePublished"].(string); ok && (m.published == "" || (pageEntity && articleType)) {
				m.published = s
				if pageEntity && articleType {
					m.articlePublished = true
				}
			}
			if s, ok := z["headline"].(string); pageEntity && ok && normalizeText(s) != "" {
				m.headline = true
				if m.title == "" {
					m.title = normalizeText(s)
				}
			}
			if m.description == "" {
				if s, ok := z["description"].(string); ok {
					m.description = normalizeText(s)
				}
			}
			for key, q := range z {
				if key == "@graph" {
					visit(q, false)
					continue
				}
				mainEntity := strings.EqualFold(key, "mainEntity")
				if mainEntity {
					if ref, ok := q.(map[string]any); ok {
						id, hasID := ref["@id"].(string)
						currentID, _ := z["@id"].(string)
						if hasID && entities[id] != nil && id != currentID {
							if !activeIDs[id] {
								activeIDs[id] = true
								visit(entities[id], true)
								delete(activeIDs, id)
							}
							continue
						}
					}
				}
				visit(q, mainEntity)
			}
		}
	}
	visit(v, true)
}
func (a *analysis) pageMicrodataEntities(root *html.Node) (map[*html.Node]bool, bool, map[*html.Node]bool, *html.Node) {
	entities := map[*html.Node]bool{}
	records := map[*html.Node]bool{}
	var articleEntities []*html.Node
	walk(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode || (!hasHTMLAttr(n, "itemscope") && attrValue(n, "itemtype") == "") {
			return true
		}
		if a.inferenceAuxiliaryBlock(n) || !isPageMicrodataEntity(n) {
			return true
		}
		entities[n] = true
		itemtype := strings.ToLower(attrValue(n, "itemtype"))
		if containsAny(itemtype, "article", "newsarticle", "blogposting") {
			articleEntities = append(articleEntities, n)
		}
		return true
	})
	// Listing records are frequently wrapped individually (for example,
	// ul > li > article), so immediate parent equality is not meaningful. More
	// than one unnested article scope in the primary content region represents a
	// repeated set; only an explicitly designated mainEntity remains eligible
	// to supply page-level article metadata.
	if len(articleEntities) < 2 {
		return entities, false, records, nil
	}

	// A substantial primary article may have one or more sibling teaser cards.
	// Those cards are records from other pages, but they do not make this page a
	// listing. Prefer an explicit mainEntity; otherwise require exactly one
	// substantial non-record article and record-shaped remaining entities.
	var dominant *html.Node
	for _, entity := range articleEntities {
		if containsAny(strings.ToLower(attrValue(entity, "itemprop")), "mainentity") {
			if dominant != nil {
				dominant = nil
				break
			}
			dominant = entity
		}
	}
	if dominant == nil {
		for _, entity := range articleEntities {
			if !microdataRecordShape(entity) && substantialArticleScope(entity) {
				if dominant != nil {
					dominant = nil
					break
				}
				dominant = entity
			}
		}
	}
	if dominant != nil {
		onlyRecordsRemain := true
		for _, entity := range articleEntities {
			if entity != dominant && !microdataRecordShape(entity) {
				onlyRecordsRemain = false
				break
			}
		}
		if onlyRecordsRemain {
			for _, entity := range articleEntities {
				if entity != dominant {
					entities[entity] = false
					records[entity] = true
				}
			}
			return entities, false, records, dominant
		}
	}

	for _, entity := range articleEntities {
		if !containsAny(strings.ToLower(attrValue(entity, "itemprop")), "mainentity") {
			entities[entity] = false
			records[entity] = true
		}
	}
	return entities, true, records, nil
}

func microdataRecordShape(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		tag := strings.ToLower(p.Data)
		if tag == "aside" || tag == "li" || containsAny(elementTokens(p), "card", "result", "item", "teaser", "archive") {
			return true
		}
		if p != n && (tag == "main" || tag == "article") {
			break
		}
	}
	return false
}

func substantialArticleScope(n *html.Node) bool {
	paragraphs, chars := 0, 0
	walk(n, func(x *html.Node) bool {
		if x != n && x.Type == html.ElementNode && hasHTMLAttr(x, "itemscope") {
			return false
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "p") {
			paragraphs++
			chars += utf8.RuneCountInString(normalizeText(nodeText(x)))
		}
		return true
	})
	return chars >= 120 || (paragraphs >= 2 && chars >= 80)
}

func isPageMicrodataEntity(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	// Records in a collection describe linked items, not the containing page.
	for p := n; p != nil; p = p.Parent {
		property := strings.ToLower(attrValue(p, "itemprop"))
		if containsAny(property, "itemlistelement", "recommendation", "recommendations") ||
			(p != n && containsAny(strings.ToLower(attrValue(p, "itemtype")), "itemlist")) {
			return false
		}
	}
	if containsAny(strings.ToLower(attrValue(n, "itemprop")), "mainentity") {
		return true
	}
	// A nested scoped entity is normally an author, image, card, or other
	// property of the outer page entity. It must not become page-level metadata.
	for p := n.Parent; p != nil; p = p.Parent {
		if hasHTMLAttr(p, "itemscope") || attrValue(p, "itemtype") != "" {
			return false
		}
	}
	return true
}

func isPageMicrodataProperty(n *html.Node, entities map[*html.Node]bool) bool {
	for p := n; p != nil; p = p.Parent {
		if hasHTMLAttr(p, "itemscope") || attrValue(p, "itemtype") != "" {
			return entities[p]
		}
	}
	return true
}

func hasHTMLAttr(n *html.Node, key string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return true
		}
	}
	return false
}

func hasAncestorItemprop(n *html.Node, value string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if containsAny(strings.ToLower(attrValue(p, "itemprop")), value) {
			return true
		}
	}
	return false
}
func (a *analysis) findBase() {
	walk(a.root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "base") {
			if u, err := url.Parse(attrValue(n, "href")); err == nil {
				if a.pageURL != nil {
					u = a.pageURL.ResolveReference(u)
				}
				if u.Scheme == "http" || u.Scheme == "https" {
					a.base = u
				}
			}
			return false
		}
		return true
	})
}
func (a *analysis) resolveMetadataURL(s string) string {
	u, e := url.Parse(strings.TrimSpace(s))
	if e != nil {
		return ""
	}
	if a.base != nil {
		u = a.base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	u.User = nil
	return u.String()
}

func metadataNodes(m metadata) []*html.Node {
	var nodes []*html.Node
	if m.title != "" {
		n := &html.Node{Type: html.ElementNode, Data: "h1"}
		n.AppendChild(&html.Node{Type: html.TextNode, Data: m.title})
		nodes = append(nodes, n)
	}
	n := &html.Node{Type: html.ElementNode, Data: "p"}
	n.AppendChild(&html.Node{Type: html.TextNode, Data: m.description})
	return append(nodes, n)
}

var badTokens = []string{"cookie", "cookies", "consent", "banner", "share", "newsletter", "signup", "sign-up", "promo", "copyright", "toc"}

// hasBoilerplateToken retains the cross-page social-furniture signal without
// treating every compound use of “social” as page chrome. In particular,
// subject classes such as social-impact and social-science remain content,
// while exact and conventional widget classes keep the historical penalty.
func hasBoilerplateToken(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if containsToken(elementTokens(n), badTokens) {
		return true
	}
	for _, attr := range []string{"id", "class", "role"} {
		for _, token := range strings.Fields(strings.ToLower(attrValue(n, attr))) {
			if token == "social" {
				return true
			}
			if containsAny(token, "social") && containsAny(token,
				"follow", "link", "links", "media", "widget", "icon", "icons",
				"share", "sharing", "profile", "network", "networks", "nav") {
				return true
			}
		}
	}
	return false
}

// These labels introduce navigational or promotional regions regardless of
// page type. Matching is deliberately exact so subject sections that happen to
// use similar words are retained.
var auxiliaryLabels = map[string]bool{
	"on this page": true, "in this article": true, "table of contents": true,
	"more news": true, "latest news": true, "related news": true,
	"related articles": true, "related content": true, "recommended for you": true,
	"you may also like": true, "you may also enjoy": true, "read next": true, "more stories": true,
	"latest stories": true, "see also": true,
}

// These short labels are strong boilerplate signals on articles, but can name
// legitimate sections on other page types (for example Web Share API docs).
var articleAuxiliaryLabels = map[string]bool{
	"related posts": true, "read more": true, "share": true,
	"share this article": true, "share this post": true,
	"share this story": true, "more by": true,
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

// TOC is a sufficiently specific structural convention to exclude by itself.
// Other structural names need navigational evidence because they are also
// common documentation subjects.
var structuralBoilerplateTokens = []string{"toc"}
var navigationStructureTokens = []string{"breadcrumb", "pagination", "toolbar"}

func irrelevantNode(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(n.Data)
	if tag == "nav" || tag == "footer" {
		return true
	}
	role := strings.ToLower(attrValue(n, "role"))
	if containsAny(role, "navigation", "complementary", "contentinfo") {
		return true
	}
	tokens := elementTokens(n)
	if containsToken(tokens, structuralBoilerplateTokens) {
		return true
	}
	if containsToken(tokens, navigationStructureTokens) && !headingDocumentsStructure(n, tokens) && hasNavigationShape(n) {
		return true
	}
	label := normalizedLabel(firstNonempty(attrValue(n, "aria-label"), attrValue(n, "title")))
	if auxiliaryLabels[label] {
		return true
	}
	if tag == "a" || tag == "button" || isHeadingTag(tag) {
		text := normalizedLabel(nodeText(n))
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
	}
	return false
}

func headingDocumentsStructure(n *html.Node, tokens string) bool {
	if n == nil || n.Type != html.ElementNode || !strings.EqualFold(n.Data, "section") {
		return false
	}
	heading := firstRegionHeading(n)
	if heading == "" {
		return false
	}
	for _, token := range navigationStructureTokens {
		if containsAny(tokens, token) && containsAny(heading, token) {
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

func (a *analysis) isIrrelevantNode(n *html.Node) bool {
	if irrelevant, ok := a.irrelevant[n]; ok {
		return irrelevant
	}
	irrelevant := irrelevantNode(n)
	if !irrelevant && a.pageType == PageTypeArticle {
		irrelevant = a.articleAuxiliaryNode(n) || a.isTrailingSocialCardRegion(n) || a.microdataArticleRecords[n]
	}
	if !irrelevant && isTrailingArticleCardRegion(n) {
		// A final article classification makes trailing cards auxiliary. When
		// card tokens instead caused an inferred listing classification, require
		// an explicit promotional-region marker. Never override a caller's
		// listing/collection classification.
		irrelevant = a.pageType == PageTypeArticle ||
			(a.pageType == PageTypeListing && !a.pageTypeExplicit && isPromotionalCardRegion(n))
	}
	a.irrelevant[n] = irrelevant
	return irrelevant
}

// inferenceAuxiliaryBlock identifies regions whose repeated records describe
// other pages. This is intentionally independent of the eventual page type so
// recommendation cards cannot cause that type to become a listing in the first
// place.
func (a *analysis) inferenceAuxiliaryBlock(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if irrelevantNode(p) {
			return true
		}
		// Comment regions are profile-specific and remain page-type evidence here;
		// otherwise an automatically detected discussion would lose its posts.
		if a.articleAuxiliaryNode(p) && !a.isArticleCommentRegion(p) &&
			(!isRelatedCardRegion(p) || hasSemanticArticleBeforeOrAround(p)) {
			return true
		}
		if a.isTrailingSocialCardRegion(p) {
			return true
		}
		if isPromotionalCardRegion(p) && isTrailingArticleCardRegion(p) {
			return true
		}
	}
	return false
}

func (a *analysis) primaryArticleAncestor(n *html.Node) *html.Node {
	for p := n; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, "article") &&
			!containsAny(elementTokens(p), "card") && !a.inferenceAuxiliaryBlock(p) {
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
	tokens := elementTokens(n)
	cardShape := tag == "aside" || containsAny(tokens, "card", "embed", "post")
	platformMarker := containsAny(tokens,
		"bsky", "bluesky", "mastodon", "twitter", "tweet", "instagram",
		"facebook", "linkedin", "fediverse")
	// “Social” and “threads” can describe substantive article subjects. They
	// only become auxiliary evidence when paired with recognizable card shape.
	genericSocialMarker := containsAny(tokens, "social", "threads") && cardShape
	profileMarker := containsAny(tokens, "share", "profile", "subscribe") && cardShape
	selfPreviewCandidate := cardShape && (tag == "aside" || containsAny(tokens, "card", "preview"))
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
	if a.semanticArticleBefore == nil {
		a.semanticArticleBefore = make(map[*html.Node]bool)
		seen := false
		walk(a.root, func(x *html.Node) bool {
			if hardHidden(x) {
				return false
			}
			a.semanticArticleBefore[x] = seen
			if x.Type == html.ElementNode && strings.EqualFold(x.Data, "article") &&
				!containsAny(elementTokens(x), "card") {
				seen = true
			}
			return true
		})
	}
	return a.semanticArticleBefore[n]
}

func (a *analysis) hasSelfReference(root *html.Node) (result bool) {
	if root == nil || hardHidden(root) {
		return false
	}
	if state := a.selfReferences[root]; state != 0 {
		return state == 2
	}
	if a.selfReferences == nil {
		a.selfReferences = make(map[*html.Node]uint8)
	}
	defer func() {
		if result {
			a.selfReferences[root] = 2
		} else {
			a.selfReferences[root] = 1
		}
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
	return containsAny(elementTokens(n), "related", "recommended", "recommendations") && countMarkedCards(n, 2) >= 2
}

func (a *analysis) articleAuxiliaryNode(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if isSubscriptionRegion(n) || a.isArticleCommentRegion(n) {
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
		if isArticleAuxiliaryLabel(firstRegionHeading(n)) {
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
		if isRelatedCardRegion(n) {
			return true
		}
	}
	return false
}

// isSubscriptionRegion identifies the wrapper around a newsletter form, not
// merely the controls that Markdown conversion already omits. A promotional
// heading is required in addition to form and CTA evidence: class names such as
// newsletter-example are common on substantive tutorials with embedded forms.
func isSubscriptionRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "div", "section", "aside", "fieldset":
	default:
		return false
	}

	if !isSubscriptionPromptHeading(firstRegionHeading(n)) {
		return false
	}

	text := strings.ToLower(normalizeText(nodeText(n)))
	cta := strings.Contains(text, "subscribe") || strings.Contains(text, "sign up") ||
		strings.Contains(text, "mailing list") || strings.Contains(text, "get updates")

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
			if strings.EqualFold(strings.TrimSpace(attrValue(x, "type")), "email") {
				hasEmail = true
			}
		case "form":
			hasForm = true
			if subscriptionAttributeMarker(x) || containsSubscriptionWord(attrValue(x, "action")) {
				subscriptionForm = true
			}
		}
		return true
	})

	formEvidence := hasEmail || subscriptionForm || (hasForm && cta)
	return formEvidence && cta
}

func isSubscriptionPromptHeading(heading string) bool {
	if heading == "stay updated" || strings.HasPrefix(heading, "stay updated ") ||
		heading == "get updates" || strings.HasPrefix(heading, "get updates ") ||
		heading == "subscribe" || strings.HasPrefix(heading, "subscribe to ") {
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
	if state := a.articleCommentRegions[n]; state != 0 {
		return state == 2
	}
	if a.articleCommentRegions == nil {
		a.articleCommentRegions = make(map[*html.Node]uint8)
	}
	defer func() {
		if result {
			a.articleCommentRegions[n] = 2
		} else {
			a.articleCommentRegions[n] = 1
		}
	}()

	tokens := elementTokens(n)
	// Plural comment markers and established comment-list conventions are
	// sufficiently specific on article pages. “Responses” and “replies” are
	// ambiguous (for example, survey responses), so they require the heading or
	// repeated-record evidence checked below.
	if containsAny(tokens, "comments", "commentlist") ||
		(containsAny(tokens, "comment") && containsAny(tokens, "list")) {
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

func isCommentRegionHeading(label string) bool {
	if label == "comments" || label == "responses" || label == "replies" ||
		label == "leave a comment" || label == "leave a reply" {
		return true
	}
	fields := strings.Fields(label)
	return len(fields) >= 2 && allASCIIDigits(fields[0]) &&
		(fields[1] == "comments" || fields[1] == "responses" || fields[1] == "replies")
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
	if state := a.commentRecordCounts[root]; state != 0 {
		return int(state - 1)
	}
	if a.commentRecordCounts == nil {
		a.commentRecordCounts = make(map[*html.Node]uint8)
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
	a.commentRecordCounts[root] = uint8(count + 1)
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
	if !containsAny(elementTokens(n), "comment", "reply") {
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

func commentRecordTextLength(n *html.Node) int {
	var text strings.Builder
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
			text.WriteString(x.Data)
			text.WriteByte(' ')
		}
		return true
	})
	return utf8.RuneCountInString(normalizeText(text.String()))
}

func (a *analysis) hasArticleBodyDescendant(root *html.Node) bool {
	if root == nil || hardHidden(root) {
		return false
	}
	if state := a.semanticArticleDescendants[root]; state != 0 {
		return state == 2
	}
	if a.semanticArticleDescendants == nil {
		a.semanticArticleDescendants = make(map[*html.Node]uint8)
	}
	found := false
	for ch := root.FirstChild; ch != nil && !found; ch = ch.NextSibling {
		if hardHidden(ch) || ch.Type != html.ElementNode {
			continue
		}
		tokens := elementTokens(ch)
		semanticArticle := strings.EqualFold(ch.Data, "article") &&
			!containsAny(tokens, "card", "comment", "reply")
		// WordPress and several other publishing systems predate widespread use
		// of <article>. Their conventional *-content wrappers are equivalent
		// evidence that this subtree contains the primary article body.
		conventionalArticleBody := (containsAny(tokens, "entry") ||
			containsAny(tokens, "post") || containsAny(tokens, "article")) &&
			containsAny(tokens, "content") &&
			!containsAny(tokens, "comment", "reply")
		if semanticArticle || conventionalArticleBody {
			found = true
			break
		}
		found = a.hasArticleBodyDescendant(ch)
	}
	if found {
		a.semanticArticleDescendants[root] = 2
	} else {
		a.semanticArticleDescendants[root] = 1
	}
	return found
}

// isTrailingArticleCardRegion catches unlabeled recommendation and newsletter
// grids after an article. Their summaries can contain enough prose to defeat
// ordinary boilerplate penalties. Requiring multiple explicitly marked cards
// and an earlier/containing semantic article avoids treating a single useful
// card or a listing page as auxiliary content.
func hasNonCardArticleAncestor(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, "article") && !containsAny(elementTokens(p), "card") {
			return true
		}
	}
	return false
}

func isTrailingArticleCardRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "div", "section", "aside", "ul":
	default:
		return false
	}
	if countArticleCards(n, 2) < 2 {
		return false
	}
	return hasSemanticArticleBeforeOrAround(n)
}

func isPromotionalCardRegion(n *html.Node) bool {
	tokens := elementTokens(n)
	if containsAny(tokens, "promo", "promotion", "promotions", "promotional", "related", "recommended", "recommendations") {
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
			if containsAny(elementTokens(ch), "card") {
				count++
				continue
			}
			visit(ch)
		}
	}
	visit(root)
	return count
}

func countArticleCards(root *html.Node, limit int) int {
	count := 0
	var visit func(*html.Node)
	visit = func(parent *html.Node) {
		for ch := parent.FirstChild; ch != nil && count < limit; ch = ch.NextSibling {
			if hardHidden(ch) || ch.Type != html.ElementNode {
				continue
			}
			tokens := elementTokens(ch)
			isCard := containsAny(tokens, "card") &&
				(strings.EqualFold(ch.Data, "article") || containsAny(tokens, "article", "post", "story", "newsletter"))
			if isCard {
				count++
				continue // Do not count nested wrappers belonging to the same card.
			}
			visit(ch)
		}
	}
	visit(root)
	return count
}

func hasSemanticArticleBeforeOrAround(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, "article") && !containsAny(elementTokens(p), "card") {
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
				if x.Type == html.ElementNode && strings.EqualFold(x.Data, "article") && !containsAny(elementTokens(x), "card") {
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
	for p := n; p != nil; p = p.Parent {
		if a.isIrrelevantNode(p) {
			return true
		}
	}
	return false
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
		if p.Type == html.ElementNode && containsAny(elementTokens(p), values...) {
			return p
		}
	}
	return nil
}
func elementTokens(n *html.Node) string {
	return strings.ToLower(attrValue(n, "id") + " " + attrValue(n, "class") + " " + attrValue(n, "role"))
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
	fields := strings.FieldsFunc(s, func(r rune) bool { return !(unicode.IsLetter(r) || unicode.IsDigit(r)) })
	for _, f := range fields {
		for _, v := range values {
			if f == v {
				return true
			}
		}
	}
	return false
}
func isDiscussionControlNode(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	tokens := elementTokens(n)
	return containsAny(tokens, "rating", "forumjump") ||
		(containsAny(tokens, "thread") && containsAny(tokens, "tools"))
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
func rawNodeText(n *html.Node) string {
	var b strings.Builder
	walk(n, func(x *html.Node) bool {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		return true
	})
	return b.String()
}
func nodeText(n *html.Node) string {
	var b strings.Builder
	walk(n, func(x *html.Node) bool {
		if dom.Hidden(x) {
			return false
		}
		if x.Type == html.ElementNode {
			switch strings.ToLower(x.Data) {
			case "script", "style", "template":
				return false
			}
		}
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			b.WriteByte(' ')
		}
		return true
	})
	return b.String()
}
func walk(n *html.Node, f func(*html.Node) bool) {
	if n == nil || !f(n) {
		return
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		walk(ch, f)
	}
}
func normalizeText(s string) string { return strings.Join(strings.Fields(s), " ") }
func attrValue(n *html.Node, key string) string {
	for _, x := range n.Attr {
		if strings.EqualFold(x.Key, key) {
			return x.Val
		}
	}
	return ""
}
func firstNonempty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
