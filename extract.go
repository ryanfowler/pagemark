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
	diag                                            *Diagnostics
	irrelevant                                      map[*html.Node]bool
}

type metadata struct{ title, description, author, site, language, published, canonical, schemaType string }

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
	}
	if a.diag != nil {
		a.diag.PageCandidates = candidates
	}
	a.score(pageType)
	selected := a.selectedNodes()
	fallback := "primary"
	if len(selected) == 0 {
		selected = a.semanticFallback()
		fallback = "semantic-main"
	}
	quality := a.quality(selected)
	if (pageType == PageTypeArticle || pageType == PageTypeGeneric) && quality < .42 {
		if article, err := readability.ParseNode(root, rawURL, nil); err == nil && article.Node != nil && len(article.TextContent) > 100 {
			selected = []*html.Node{article.Node}
			fallback = "readability"
			quality = math.Max(quality, .58)
		}
	}
	if len(selected) == 0 {
		selected = a.highRecall()
		fallback = "high-recall"
		quality = .2
	}
	if len(selected) == 0 && a.meta.description != "" {
		selected = metadataNodes(a.meta)
		fallback = "metadata"
		quality = .15
	}
	if len(selected) == 0 {
		return nil, ErrNoContent
	}
	cfg := markdown.Config{Base: a.base, Links: o.includeLinks, Images: o.includeImages, Tables: o.includeTables, MaxLinks: o.maxLinks, MaxImages: o.maxImages, MaxTableCells: o.maxTableCells, MaxBytes: o.maxOutput, Policy: markdown.URLPolicy{Schemes: append([]string(nil), o.urlPolicy.Schemes...), AllowMailto: o.urlPolicy.AllowMailto, MaxLength: o.urlPolicy.MaxLength, StripTracking: o.urlPolicy.StripTracking}, Exclude: a.isIrrelevantNode}
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
	for _, b := range a.blocks {
		if b.selected {
			doc.Stats.SelectedBlocks++
		}
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
		if isBlockTag(tag) {
			text := normalizeText(nodeText(n))
			if text != "" || tag == "hr" {
				a.blocks = append(a.blocks, block{id: len(a.blocks) + 1, node: n, kind: tag, text: text})
				return
			}
		}
		if isGenericContainer(tag) && !hasBlockDescendant(n) {
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

func (a *analysis) selectedNodes() []*html.Node {
	var out []*html.Node
	countByParent := map[*html.Node]int{}
	for i := range a.blocks {
		if a.blocks[i].selected {
			countByParent[a.blocks[i].node.Parent]++
		}
	}
	for i := range a.blocks {
		b := &a.blocks[i]
		if !b.selected {
			continue
		}
		if a.o.maxRepeated > 0 && countByParent[b.node.Parent] > a.o.maxRepeated {
			siblings := 0
			for _, x := range out {
				if x.Parent == b.node.Parent {
					siblings++
				}
			}
			if siblings >= a.o.maxRepeated {
				continue
			}
		}
		out = append(out, b.node)
	}
	return out
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
	for _, b := range a.blocks {
		counts[b.kind]++
		tok := elementTokens(b.node)
		for p := b.node.Parent; p != nil; p = p.Parent {
			if p.Type == html.ElementNode {
				tok += " " + elementTokens(p)
			}
		}
		if containsAny(tok, "comment", "answer", "thread", "discussion", "post", "topic", "reply") {
			scores[PageTypeDiscussion] += 2
		}
		if containsAny(tok, "product", "price", "sku") {
			scores[PageTypeProduct] += 1.5
			if record := nearestTokenAncestor(b.node, "product", "sku"); record != nil {
				productRecords[record] = true
			}
		}
		if containsAny(tok, "docs", "documentation", "api", "reference") {
			scores[PageTypeDocumentation] += 1.5
		}
		if containsAny(tok, "card", "result", "listing", "item") {
			scores[PageTypeListing]++
			if record := nearestTokenAncestor(b.node, "card", "result", "item"); record != nil {
				linkedRecords[record] = true
			}
		}
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
	if strings.Contains(schema, "article") || strings.Contains(schema, "news") {
		scores[PageTypeArticle] += 5
	}
	if strings.Contains(schema, "product") {
		scores[PageTypeProduct] += 5
	}
	if strings.Contains(schema, "discussion") || strings.Contains(schema, "question") {
		scores[PageTypeDiscussion] += 5
	}
	if strings.Contains(schema, "itemlist") {
		scores[PageTypeListing] += 5
	}
	if counts["pre"] > 1 {
		scores[PageTypeDocumentation] += 3
	}
	if counts["table"] > 0 {
		scores[PageTypeProduct]++
		scores[PageTypeDocumentation]++
	}
	if counts["p"] > 4 {
		scores[PageTypeArticle] += 2
	}
	if strings.Contains(urlPath, "/docs") || strings.Contains(urlPath, "/api") {
		scores[PageTypeDocumentation] += 3
	}
	title := strings.ToLower(a.meta.title)
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
			case "article:published_time", "datepublished":
				if m.published == "" {
					m.published = v
				}
			case "og:title", "twitter:title":
				if m.title == "" {
					m.title = v
				}
			case "og:type":
				m.schemaType = v
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
	var visit func(any)
	visit = func(x any) {
		switch z := x.(type) {
		case []any:
			for _, q := range z {
				visit(q)
			}
		case map[string]any:
			if t, ok := z["@type"].(string); ok && m.schemaType == "" {
				m.schemaType = t
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
			if m.published == "" {
				if s, ok := z["datePublished"].(string); ok {
					m.published = s
				}
			}
			if m.title == "" {
				if s, ok := z["headline"].(string); ok {
					m.title = normalizeText(s)
				}
			}
			if m.description == "" {
				if s, ok := z["description"].(string); ok {
					m.description = normalizeText(s)
				}
			}
			for _, q := range z {
				visit(q)
			}
		}
	}
	visit(v)
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

// These labels introduce navigational or promotional regions rather than the
// subject of the page. Matching is deliberately exact so ordinary prose that
// happens to contain the same words is retained.
var auxiliaryLabels = map[string]bool{
	"on this page": true, "in this article": true, "table of contents": true,
	"more news": true, "latest news": true, "related news": true,
	"related articles": true, "related content": true, "recommended for you": true,
	"you may also like": true, "read next": true, "more stories": true,
	"latest stories": true, "see also": true,
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
	a.irrelevant[n] = irrelevant
	return irrelevant
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
