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
	"sort"
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
	microdataArticleRecords                         map[*html.Node]bool
	dominantMicrodataArticle                        *html.Node
}

type metadata struct {
	title, description, author, site, language, published, canonical, schemaType string
	articlePublished, articleType, headline, microdataListing                    bool
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
		return a.isIrrelevantNode(n) || repeatedExcluded[n] || discussionAuxiliary
	}
	cfg := markdown.Config{Base: a.base, Links: o.includeLinks, Images: o.includeImages, Tables: o.includeTables, MaxLinks: o.maxLinks, MaxImages: o.maxImages, MaxTableCells: o.maxTableCells, MaxBytes: o.maxOutput, Policy: markdown.URLPolicy{Schemes: append([]string(nil), o.urlPolicy.Schemes...), AllowMailto: o.urlPolicy.AllowMailto, MaxLength: o.urlPolicy.MaxLength, StripTracking: o.urlPolicy.StripTracking}, Exclude: exclude}
	if pageType == PageTypeArticle {
		selected = a.ensureArticleTitle(selected, cfg)
	}
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
		excluded = excluded || hardHidden(n)
		if excluded {
			return
		}
		tag := strings.ToLower(n.Data)
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
			if text != "" || tag == "hr" {
				a.blocks = append(a.blocks, block{id: len(a.blocks) + 1, node: n, kind: tag, text: text})
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
		case "generic":
			score = 0.4 + math.Min(2, float64(length)/250)
		}
		b.reasons = append(b.reasons, "content shape")
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
			if containsToken(tokens, badTokens) {
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

// ensureArticleTitle restores a source h1 next to selected article content.
// Headings in a page-level header often score below the normal threshold even
// when the adjacent article body scores well. If the source does not provide a
// surviving title, metadata supplies one so article output does not begin in
// the middle of the story.
func (a *analysis) ensureArticleTitle(nodes []*html.Node, cfg markdown.Config) []*html.Node {
	if a.hasTitleEquivalentHeading(nodes) {
		return nodes
	}

	// Prefer the source heading over metadata. Looking only a small number of
	// segmented blocks away keeps a site masthead elsewhere on the page from
	// being mistaken for the article title, while allowing a byline or
	// breadcrumb between the heading and body.
	bestIndex, bestDistance := -1, 3
	bestEquivalent := false
	for i := range a.blocks {
		b := &a.blocks[i]
		if b.kind != "h1" || hardHidden(b.node) || a.hasIrrelevantAncestor(b.node) {
			continue
		}
		distance := adjacentSelectedBlockDistance(a.blocks, i, nodes, 2)
		if distance == 0 {
			// No selected article block is close enough to make this h1 part of
			// the extracted region.
			continue
		}
		equivalent := titleEquivalent(b.text, a.meta.title)
		// With authoritative metadata, a different adjacent h1 is usually the
		// site's masthead rather than the article title.
		if a.meta.title != "" && !equivalent {
			continue
		}
		if bestIndex < 0 || (equivalent && !bestEquivalent) || (equivalent == bestEquivalent && distance < bestDistance) {
			bestIndex, bestDistance, bestEquivalent = i, distance, equivalent
		}
	}
	if bestIndex >= 0 {
		withTitle := append([]*html.Node{a.blocks[bestIndex].node}, nodes...)
		if titleLeavesOutputForContent(withTitle, cfg) {
			return withTitle
		}
		// Do not replace an omitted source title with metadata: either title
		// would consume budget intended for the article body.
		return nodes
	}
	if a.meta.title == "" {
		return nodes
	}

	title := &html.Node{Type: html.ElementNode, Data: "h1"}
	title.AppendChild(&html.Node{Type: html.TextNode, Data: a.meta.title})
	withTitle := append([]*html.Node{title}, nodes...)
	if !titleLeavesOutputForContent(withTitle, cfg) {
		return nodes
	}
	return withTitle
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

func (a *analysis) hasTitleEquivalentHeading(nodes []*html.Node) bool {
	found := false
	for _, root := range nodes {
		walk(root, func(n *html.Node) bool {
			if found || hardHidden(n) || a.hasIrrelevantAncestor(n) {
				return false
			}
			if n.Type == html.ElementNode && isHeadingTag(strings.ToLower(n.Data)) && titleEquivalent(nodeText(n), a.meta.title) {
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

func titleEquivalent(heading, title string) bool {
	heading = normalizedLabel(heading)
	title = normalizedLabel(title)
	if heading == "" {
		return false
	}
	if title == "" || heading == title {
		return true
	}
	// Browser titles commonly put a site name before or after the visible h1.
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

func isTitleSeparator(r rune) bool {
	return strings.ContainsRune("|:-–—·•", r)
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
				if t == "header" || t == "footer" || t == "nav" || containsToken(elementTokens(p), badTokens) {
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
		if inferenceAuxiliaryBlock(b.node) || a.hasMicrodataArticleRecordAncestor(b.node) {
			continue
		}
		counts[b.kind]++
		article := primaryArticleAncestor(b.node)
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
	microdataEntities, repeatedMicrodataArticles, microdataRecords, dominantMicrodata := pageMicrodataEntities(a.root)
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
		if (tag == "title" || tag == "h1") && m.title == "" {
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
func pageMicrodataEntities(root *html.Node) (map[*html.Node]bool, bool, map[*html.Node]bool, *html.Node) {
	entities := map[*html.Node]bool{}
	records := map[*html.Node]bool{}
	var articleEntities []*html.Node
	walk(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode || (!hasHTMLAttr(n, "itemscope") && attrValue(n, "itemtype") == "") {
			return true
		}
		if inferenceAuxiliaryBlock(n) || !isPageMicrodataEntity(n) {
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

var badTokens = []string{"cookie", "cookies", "consent", "banner", "share", "social", "newsletter", "signup", "sign-up", "promo", "copyright", "toc"}

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
		irrelevant = articleAuxiliaryNode(n) || a.microdataArticleRecords[n]
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
func inferenceAuxiliaryBlock(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if irrelevantNode(p) {
			return true
		}
		if articleAuxiliaryNode(p) && (!isRelatedCardRegion(p) || hasSemanticArticleBeforeOrAround(p)) {
			return true
		}
		if isPromotionalCardRegion(p) && isTrailingArticleCardRegion(p) {
			return true
		}
	}
	return false
}

func primaryArticleAncestor(n *html.Node) *html.Node {
	for p := n; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, "article") &&
			!containsAny(elementTokens(p), "card") && !inferenceAuxiliaryBlock(p) {
			return p
		}
	}
	return nil
}

func isRelatedCardRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	return containsAny(elementTokens(n), "related", "recommended", "recommendations") && countMarkedCards(n, 2) >= 2
}

func articleAuxiliaryNode(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
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
