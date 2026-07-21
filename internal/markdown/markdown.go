// Package markdown converts selected HTML nodes to a safe Markdown tree.
package markdown

import (
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/dom"
	"golang.org/x/net/html"
)

type Kind uint8

const (
	Document Kind = iota
	Heading
	Paragraph
	Text
	Emphasis
	Strong
	InlineCode
	CodeBlock
	Blockquote
	UnorderedList
	OrderedList
	ListItem
	Table
	TableRow
	TableCell
	Link
	Image
	ThematicBreak
	HardBreak
)

type Node struct {
	Kind     Kind
	Level    int
	Start    int
	Reversed bool
	HasValue bool
	Value    string
	URL      string
	Info     string
	Align    string
	Children []*Node
}

type URLPolicy struct {
	Schemes       []string
	AllowMailto   bool
	MaxLength     int
	StripTracking bool
}
type Config struct {
	Base                                         *url.URL
	Links, Images, Tables                        bool
	MaxLinks, MaxImages, MaxTableCells, MaxBytes int
	Policy                                       URLPolicy
	// Exclude removes extraction-specific boilerplate subtrees. Hidden DOM
	// handling remains built in to the converter.
	Exclude func(*html.Node) bool
}
type Result struct {
	Markdown, Text       string
	Links                []LinkValue
	Images               []ImageValue
	Sections             []SectionValue
	Rejected             []string
	EmittedBlocks        int
	EmittedContentBlocks int
	Truncated            bool
}
type LinkValue struct{ Text, URL string }
type ImageValue struct{ Alt, URL string }
type SectionValue struct{ Heading, Text string }

type converter struct {
	cfg                   Config
	linkCount, imageCount int
	rejected              []string
	cells                 int
}

func Convert(nodes []*html.Node, cfg Config) Result {
	c := &converter{cfg: cfg}
	doc := &Node{Kind: Document}
	for _, n := range nodes {
		if x := c.block(n); x != nil {
			doc.Children = append(doc.Children, x)
		}
	}
	r := render(doc, cfg.MaxBytes)
	r.Rejected = c.rejected
	return r
}

func (c *converter) skip(n *html.Node) bool {
	return n == nil || dom.Hidden(n) || (c.cfg.Exclude != nil && c.cfg.Exclude(n))
}

func (c *converter) block(n *html.Node) *Node {
	if c.skip(n) {
		return nil
	}
	if n.Type == html.TextNode {
		v := clean(n.Data)
		if v != "" {
			return &Node{Kind: Paragraph, Children: []*Node{{Kind: Text, Value: v}}}
		}
		return nil
	}
	tag := strings.ToLower(n.Data)
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return &Node{Kind: Heading, Level: int(tag[1] - '0'), Children: c.inlines(n)}
	case "p", "figcaption", "caption", "dt", "dd":
		return &Node{Kind: Paragraph, Children: c.inlines(n)}
	case "pre":
		return &Node{Kind: CodeBlock, Value: c.textRaw(n), Info: codeInfo(n)}
	case "blockquote":
		return &Node{Kind: Blockquote, Children: c.blocks(n)}
	case "ul":
		return &Node{Kind: UnorderedList, Children: c.listItems(n)}
	case "ol":
		items := c.listItems(n)
		start, hasStart := parseIntegerAttr(n, "start")
		if !hasStart {
			start = 1
		}
		reversed := hasAttr(n, "reversed")
		if reversed && !hasStart {
			start = len(items)
		}
		return &Node{Kind: OrderedList, Start: start, Reversed: reversed, Children: items}
	case "dl":
		return &Node{Kind: UnorderedList, Children: c.definitionItems(n)}
	case "table":
		if !c.cfg.Tables {
			return c.fallbackTable(n)
		}
		return c.table(n)
	case "hr":
		return &Node{Kind: ThematicBreak}
	case "html", "body", "figure", "article", "section", "main", "div", "aside", "header", "footer", "nav", "address", "details":
		return &Node{Kind: Document, Children: c.blocks(n)}
	case "summary":
		return &Node{Kind: Paragraph, Children: c.inlines(n)}
	default:
		if c.hasBlockDescendant(n) {
			return &Node{Kind: Document, Children: c.blocks(n)}
		}
		in := c.inlines(n)
		if len(in) > 0 {
			return &Node{Kind: Paragraph, Children: in}
		}
	}
	return nil
}

func (c *converter) blocks(n *html.Node) []*Node {
	var out []*Node
	var pending []*html.Node
	flush := func() {
		if in := c.inlineNodes(pending); len(in) > 0 && clean(plain(&Node{Kind: Document, Children: in})) != "" {
			out = append(out, &Node{Kind: Paragraph, Children: in})
		}
		pending = nil
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if c.skip(ch) {
			continue
		}
		if ch.Type != html.ElementNode || (!isBlockElement(strings.ToLower(ch.Data)) && !c.hasBlockDescendant(ch)) {
			pending = append(pending, ch)
			continue
		}
		flush()
		if x := c.block(ch); x != nil {
			if x.Kind == Document {
				out = append(out, x.Children...)
			} else {
				out = append(out, x)
			}
		}
	}
	flush()
	return out
}

func isBlockElement(tag string) bool {
	switch tag {
	case "html", "body", "address", "article", "aside", "blockquote", "details", "dialog", "div", "dl", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "main", "nav", "ol", "p", "pre", "section", "summary", "table", "ul":
		return true
	}
	return false
}

func (c *converter) hasBlockDescendant(n *html.Node) bool {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if c.skip(child) || child.Type != html.ElementNode {
			continue
		}
		if isBlockElement(strings.ToLower(child.Data)) || c.hasBlockDescendant(child) {
			return true
		}
	}
	return false
}

func (c *converter) inlines(n *html.Node) []*Node {
	var children []*html.Node
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		children = append(children, ch)
	}
	return c.inlineNodes(children)
}

func (c *converter) inlineNodes(nodes []*html.Node) []*Node {
	var out []*Node
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if c.skip(x) {
			return
		}
		if x.Type == html.TextNode {
			if v := inlineText(x.Data); strings.TrimSpace(v) != "" {
				out = append(out, &Node{Kind: Text, Value: v})
			} else if len(out) > 0 && strings.IndexFunc(x.Data, unicode.IsSpace) >= 0 {
				out = append(out, &Node{Kind: Text, Value: " "})
			}
			return
		}
		if x.Type != html.ElementNode {
			return
		}
		tag := strings.ToLower(x.Data)
		switch tag {
		case "br":
			out = append(out, &Node{Kind: HardBreak})
			return
		case "form", "button", "input", "select", "textarea":
			return
		case "a":
			children := c.inlines(x)
			href := attr(x, "href")
			if c.cfg.Links && href != "" {
				safe, ok := c.safeURL(href)
				if !ok {
					c.rejected = append(c.rejected, href)
				} else if len(children) > 0 && c.linkCount < c.cfg.MaxLinks {
					out = append(out, &Node{Kind: Link, URL: safe, Children: children})
					c.linkCount++
					return
				}
			}
			if len(children) == 0 && len(out) > 0 && c.hasWhitespace(x) {
				out = append(out, &Node{Kind: Text, Value: " "})
			} else {
				out = append(out, children...)
			}
			return
		case "img":
			alt, src := clean(attr(x, "alt")), attr(x, "src")
			if c.cfg.Images && alt != "" && c.imageCount < c.cfg.MaxImages {
				if safe, ok := c.safeURL(src); ok {
					out = append(out, &Node{Kind: Image, Value: alt, URL: safe})
					c.imageCount++
				}
			}
			return
		case "em", "i":
			out = append(out, &Node{Kind: Emphasis, Children: c.inlines(x)})
			return
		case "strong", "b":
			out = append(out, &Node{Kind: Strong, Children: c.inlines(x)})
			return
		case "code":
			out = append(out, &Node{Kind: InlineCode, Value: c.textRaw(x)})
			return
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	var merged []*Node
	for _, n := range out {
		if n.Kind == Text && len(merged) > 0 && merged[len(merged)-1].Kind == Text {
			merged[len(merged)-1].Value = inlineText(merged[len(merged)-1].Value + n.Value)
			continue
		}
		merged = append(merged, n)
	}
	return merged
}

func (c *converter) listItems(n *html.Node) []*Node {
	var out []*Node
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if c.skip(ch) {
			continue
		}
		if ch.Type == html.ElementNode && strings.EqualFold(ch.Data, "li") {
			value, hasValue := parseIntegerAttr(ch, "value")
			out = append(out, &Node{Kind: ListItem, Level: value, HasValue: hasValue, Children: c.mixedItem(ch)})
		}
	}
	return out
}
func (c *converter) mixedItem(n *html.Node) []*Node {
	var out []*Node
	var pending []*html.Node
	flush := func() {
		if in := c.inlineNodes(pending); len(in) > 0 {
			out = append(out, &Node{Kind: Paragraph, Children: in})
		}
		pending = nil
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if c.skip(ch) {
			continue
		}
		if ch.Type == html.ElementNode && (isBlockElement(strings.ToLower(ch.Data)) || c.hasBlockDescendant(ch)) {
			flush()
			if x := c.block(ch); x != nil {
				if x.Kind == Document {
					out = append(out, x.Children...)
				} else {
					out = append(out, x)
				}
			}
			continue
		}
		pending = append(pending, ch)
	}
	flush()
	return out
}

func (c *converter) definitionItems(n *html.Node) []*Node {
	var out []*Node
	var terms [][]*Node
	hadDefinition := false
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if c.skip(ch) || ch.Type != html.ElementNode {
			continue
		}
		if strings.EqualFold(ch.Data, "dt") {
			if hadDefinition {
				terms = nil
				hadDefinition = false
			}
			terms = append(terms, c.inlines(ch))
			continue
		}
		if strings.EqualFold(ch.Data, "dd") {
			hadDefinition = true
			var in []*Node
			for i, term := range terms {
				if i > 0 {
					in = append(in, &Node{Kind: Text, Value: ", "})
				}
				in = append(in, &Node{Kind: Strong, Children: term})
			}
			if len(terms) > 0 {
				in = append(in, &Node{Kind: Text, Value: ": "})
			}
			in = append(in, c.inlines(ch)...)
			out = append(out, &Node{Kind: ListItem, Children: []*Node{{Kind: Paragraph, Children: in}}})
		}
	}
	return out
}

func (c *converter) table(n *html.Node) *Node {
	var rows [][]*html.Node
	var caption []*Node
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if c.skip(x) {
			return
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "caption") {
			if caption == nil {
				caption = c.inlines(x)
			}
			return
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "tr") {
			var row []*html.Node
			for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
				if !c.skip(ch) && ch.Type == html.ElementNode && (strings.EqualFold(ch.Data, "td") || strings.EqualFold(ch.Data, "th")) {
					row = append(row, ch)
				}
			}
			if len(row) > 0 {
				rows = append(rows, row)
			}
			return
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	fallback := func() *Node { return tableWithCaption(c.fallbackTable(n), caption) }
	if len(rows) == 0 {
		return tableWithCaption(nil, caption)
	}
	width := len(rows[0])
	total := 0
	for _, r := range rows {
		if len(r) != width {
			return fallback()
		}
		total += len(r)
		for _, cell := range r {
			if integerAttr(cell, "colspan", 1) != 1 || integerAttr(cell, "rowspan", 1) != 1 {
				return fallback()
			}
		}
	}
	if width == 0 || c.cfg.MaxTableCells <= 0 || c.cells+total > c.cfg.MaxTableCells {
		return fallback()
	}
	header := true
	for _, cell := range rows[0] {
		header = header && strings.EqualFold(cell.Data, "th")
	}
	// GFM requires a column-header row. A mixed th/td row generally uses th
	// as a row header, so promoting its td cells would change the semantics.
	if !header {
		return fallback()
	}
	t := &Node{Kind: Table}
	for _, r := range rows {
		rr := &Node{Kind: TableRow}
		for _, cell := range r {
			rr.Children = append(rr.Children, &Node{Kind: TableCell, Align: cellAlignment(cell), Children: c.inlines(cell)})
			c.cells++
		}
		t.Children = append(t.Children, rr)
	}
	return tableWithCaption(t, caption)
}

func tableWithCaption(table *Node, caption []*Node) *Node {
	if len(caption) == 0 {
		return table
	}
	children := []*Node{{Kind: Paragraph, Children: caption}}
	if table != nil {
		children = append(children, table)
	}
	return &Node{Kind: Document, Children: children}
}

func cellAlignment(n *html.Node) string {
	v := strings.ToLower(strings.TrimSpace(attr(n, "align")))
	if v == "left" || v == "center" || v == "right" {
		return v
	}
	style := strings.ToLower(attr(n, "style"))
	for _, declaration := range strings.Split(style, ";") {
		parts := strings.SplitN(declaration, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == "text-align" {
			v = strings.TrimSpace(parts[1])
			if v == "left" || v == "center" || v == "right" {
				return v
			}
		}
	}
	return ""
}
func (c *converter) fallbackTable(n *html.Node) *Node {
	var items []*Node
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if c.skip(x) {
			return
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "tr") {
			v := clean(c.nodeText(x))
			if v != "" {
				items = append(items, &Node{Kind: ListItem, Children: []*Node{{Kind: Paragraph, Children: []*Node{{Kind: Text, Value: v}}}}})
			}
			return
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return &Node{Kind: UnorderedList, Children: items}
}

func (c *converter) safeURL(raw string) (string, bool) {
	if strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || (c.cfg.Policy.MaxLength > 0 && len(raw) > c.cfg.Policy.MaxLength) {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if c.cfg.Base != nil {
		u = c.cfg.Base.ResolveReference(u)
	}
	if u.User != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	allowed := false
	for _, s := range c.cfg.Policy.Schemes {
		if scheme == strings.ToLower(s) {
			allowed = true
		}
	}
	if scheme == "mailto" && c.cfg.Policy.AllowMailto {
		allowed = true
	}
	if !allowed {
		return "", false
	}
	if (scheme == "http" || scheme == "https") && (u.Host == "" || u.Opaque != "") {
		return "", false
	}
	if c.cfg.Policy.StripTracking {
		q := u.Query()
		for k := range q {
			lk := strings.ToLower(k)
			if strings.HasPrefix(lk, "utm_") || lk == "fbclid" || lk == "gclid" {
				q.Del(k)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), true
}

var spaces = regexp.MustCompile(`\s+`)

func clean(s string) string { return strings.TrimSpace(spaces.ReplaceAllString(s, " ")) }
func inlineText(s string) string {
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	leading := len(s) > 0 && unicode.IsSpace(first)
	trailing := len(s) > 0 && unicode.IsSpace(last)
	v := clean(s)
	if v == "" {
		return ""
	}
	if leading {
		v = " " + v
	}
	if trailing {
		v += " "
	}
	return v
}
func (c *converter) hasWhitespace(n *html.Node) bool {
	found := false
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if found || c.skip(x) {
			return
		}
		if x.Type == html.TextNode && strings.IndexFunc(x.Data, unicode.IsSpace) >= 0 {
			found = true
			return
		}
		for child := x.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return found
}

func (c *converter) nodeText(n *html.Node) string {
	var values []string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if c.skip(x) {
			return
		}
		if x.Type == html.TextNode {
			if v := clean(x.Data); v != "" {
				values = append(values, v)
			}
			return
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return strings.Join(values, " ")
}
func (c *converter) textRaw(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if c.skip(x) {
			return
		}
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			return
		}
		if x.Type == html.ElementNode && (strings.EqualFold(x.Data, "script") || strings.EqualFold(x.Data, "style")) {
			return
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return b.String()
}

func codeInfo(n *html.Node) string {
	code := n.FirstChild
	for code != nil && code.Type != html.ElementNode {
		code = code.NextSibling
	}
	if code == nil || !strings.EqualFold(code.Data, "code") {
		return ""
	}
	for _, class := range strings.Fields(attr(code, "class")) {
		info := ""
		if strings.HasPrefix(strings.ToLower(class), "language-") {
			info = class[len("language-"):]
		} else if strings.HasPrefix(strings.ToLower(class), "lang-") {
			info = class[len("lang-"):]
		}
		if info != "" && strings.IndexFunc(info, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_+-#.", r)
		}) < 0 {
			return info
		}
	}
	return ""
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return true
		}
	}
	return false
}

func integerAttr(n *html.Node, key string, fallback int) int {
	v, ok := parseIntegerAttr(n, key)
	if !ok {
		return fallback
	}
	return v
}

func parseIntegerAttr(n *html.Node, key string) (int, bool) {
	if !hasAttr(n, key) {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(attr(n, key)))
	return v, err == nil
}

func render(doc *Node, max int) Result {
	var markdownBlocks, keptText []string
	var keptNodes []*Node
	var blocks []*Node
	var flatten func(*Node)
	flatten = func(n *Node) {
		if n != nil && n.Kind == Document {
			for _, child := range n.Children {
				flatten(child)
			}
			return
		}
		if n != nil {
			blocks = append(blocks, n)
		}
	}
	flatten(doc)
	used, truncated := 0, false
	for _, n := range blocks {
		s := strings.TrimSpace(renderBlock(n, 0))
		if s == "" {
			continue
		}
		add := len(s)
		if len(markdownBlocks) > 0 {
			add += 2
		}
		if max > 0 && used+add > max {
			truncated = true
			break
		}
		markdownBlocks = append(markdownBlocks, s)
		keptText = append(keptText, plain(n))
		keptNodes = append(keptNodes, n)
		used += add
	}
	md := strings.Join(markdownBlocks, "\n\n")
	if truncated {
		marker := "\n\n[Content truncated]"
		if max <= 0 || len(md)+len(marker) <= max {
			md += marker
		}
	}
	links, images := retainedMedia(keptNodes)
	contentBlocks := 0
	for _, n := range keptNodes {
		if n.Kind != Heading && n.Kind != ThematicBreak {
			contentBlocks++
		}
	}
	return Result{
		Markdown: md, Text: clean(strings.Join(keptText, "\n")),
		Links: links, Images: images, Sections: retainedSections(keptNodes),
		EmittedBlocks: len(keptNodes), EmittedContentBlocks: contentBlocks, Truncated: truncated,
	}
}

func retainedMedia(nodes []*Node) ([]LinkValue, []ImageValue) {
	var links []LinkValue
	var images []ImageValue
	var visit func(*Node)
	visit = func(n *Node) {
		if n.Kind == Link {
			links = append(links, LinkValue{plain(n), n.URL})
		}
		if n.Kind == Image {
			images = append(images, ImageValue{n.Value, n.URL})
		}
		for _, ch := range n.Children {
			visit(ch)
		}
	}
	for _, n := range nodes {
		visit(n)
	}
	return links, images
}

func retainedSections(nodes []*Node) []SectionValue {
	var blocks []*Node
	var flatten func(*Node)
	flatten = func(n *Node) {
		if n.Kind == Document {
			for _, ch := range n.Children {
				flatten(ch)
			}
			return
		}
		blocks = append(blocks, n)
	}
	for _, n := range nodes {
		flatten(n)
	}
	var sections []SectionValue
	heading := ""
	var text []string
	flush := func() {
		if value := clean(strings.Join(text, " ")); value != "" {
			sections = append(sections, SectionValue{heading, value})
		}
		text = nil
	}
	for _, n := range blocks {
		if n.Kind == Heading {
			flush()
			heading = clean(plain(n))
		} else if value := plain(n); value != "" {
			text = append(text, value)
		}
	}
	flush()
	return sections
}
func renderBlock(n *Node, depth int) string {
	if n == nil {
		return ""
	}
	switch n.Kind {
	case Document:
		var a []string
		for _, x := range n.Children {
			if s := renderBlock(x, depth); s != "" {
				a = append(a, s)
			}
		}
		return strings.Join(a, "\n\n")
	case Heading:
		l := n.Level
		if l < 1 {
			l = 1
		}
		if l > 6 {
			l = 6
		}
		return strings.Repeat("#", l) + " " + renderInline(n.Children)
	case Paragraph:
		return renderInline(n.Children)
	case CodeBlock:
		f := "```"
		for strings.Contains(n.Value, f) {
			f += "`"
		}
		value := strings.ReplaceAll(strings.ReplaceAll(n.Value, "\r\n", "\n"), "\r", "\n")
		return f + n.Info + "\n" + strings.TrimRight(value, "\n") + "\n" + f
	case Blockquote:
		s := renderBlock(&Node{Kind: Document, Children: n.Children}, depth)
		return "> " + strings.ReplaceAll(s, "\n", "\n> ")
	case UnorderedList, OrderedList:
		var a []string
		number := n.Start
		literalOrdinals := n.Kind == OrderedList && needsLiteralOrdinals(n)
		ordinal := big.NewInt(int64(n.Start))
		step := big.NewInt(1)
		if n.Reversed {
			step.Neg(step)
		}
		for _, x := range n.Children {
			mark := "- "
			if n.Kind == OrderedList {
				if x.HasValue {
					number = x.Level
					ordinal.SetInt64(int64(x.Level))
				}
				if !literalOrdinals {
					mark = fmt.Sprintf("%d. ", number)
				}
			}
			body := renderBlock(x, depth+1)
			if literalOrdinals {
				body = ordinal.String() + "\\. " + body
			}
			indent := strings.Repeat(" ", len(mark))
			a = append(a, mark+strings.ReplaceAll(body, "\n", "\n"+indent))
			if n.Kind == OrderedList {
				if literalOrdinals {
					ordinal.Add(ordinal, step)
				} else {
					number++
				}
			}
		}
		return strings.Join(a, "\n")
	case ListItem:
		var a []string
		for _, x := range n.Children {
			if value := renderBlock(x, depth); value != "" {
				a = append(a, value)
			}
		}
		return strings.Join(a, "\n")
	case Table:
		return renderTable(n)
	case ThematicBreak:
		return "---"
	}
	return renderInline([]*Node{n})
}
func needsLiteralOrdinals(n *Node) bool {
	if n.Reversed {
		return true
	}
	number := n.Start
	for _, item := range n.Children {
		if item.HasValue {
			return true
		}
		// CommonMark ordered-list markers contain at most nine digits and
		// cannot represent negative ordinals.
		if number < 0 || number > 999999999 {
			return true
		}
		number++
	}
	return false
}

func renderInline(ns []*Node) string {
	var b strings.Builder
	for _, n := range ns {
		var value string
		switch n.Kind {
		case Text:
			value = escape(n.Value)
		case Emphasis:
			value = renderDelimited(renderInline(n.Children), "*")
		case Strong:
			value = renderDelimited(renderInline(n.Children), "**")
		case InlineCode:
			v := n.Value
			tick := "`"
			for strings.Contains(v, tick) {
				tick += "`"
			}
			var code strings.Builder
			code.WriteString(tick)
			needsPadding := strings.HasPrefix(v, "`") || strings.HasSuffix(v, "`") ||
				((strings.HasPrefix(v, " ") || strings.HasSuffix(v, " ")) && strings.Trim(v, " ") != "")
			if needsPadding {
				code.WriteByte(' ')
			}
			code.WriteString(v)
			if needsPadding {
				code.WriteByte(' ')
			}
			code.WriteString(tick)
			value = code.String()
		case Link:
			label := renderInline(n.Children)
			value = renderWrapped(label, "[", "]("+markdownDestination(n.URL)+")")
		case Image:
			value = "![" + escape(n.Value) + "](" + markdownDestination(n.URL) + ")"
		case HardBreak:
			value = "\\\n"
		}
		writeInline(&b, value)
	}
	return b.String()
}

// writeInline preserves whitespace moved outside Markdown delimiters without
// duplicating whitespace that already occurs at an adjacent node boundary.
func writeInline(b *strings.Builder, value string) {
	if b.Len() > 0 {
		last, _ := utf8.DecodeLastRuneInString(b.String())
		first, _ := utf8.DecodeRuneInString(value)
		if isHorizontalSpace(last) && isHorizontalSpace(first) {
			value = strings.TrimLeftFunc(value, isHorizontalSpace)
		}
	}
	b.WriteString(value)
}

func renderDelimited(value, delimiter string) string {
	return renderWrapped(value, delimiter, delimiter)
}

func renderWrapped(value, leftDelimiter, rightDelimiter string) string {
	if strings.TrimFunc(value, isHorizontalSpace) == "" {
		return value
	}
	left := value[:len(value)-len(strings.TrimLeftFunc(value, isHorizontalSpace))]
	right := value[len(strings.TrimRightFunc(value, isHorizontalSpace)):]
	core := strings.TrimFunc(value, isHorizontalSpace)
	return left + leftDelimiter + core + rightDelimiter + right
}

func isHorizontalSpace(r rune) bool {
	return unicode.IsSpace(r) && r != '\n' && r != '\r'
}

func markdownDestination(value string) string {
	return strings.NewReplacer("\\", "%5C", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(value)
}
func renderTable(n *Node) string {
	if len(n.Children) == 0 {
		return ""
	}
	var lines []string
	for _, r := range n.Children {
		var cells []string
		for _, c := range r.Children {
			value := renderInline(c.Children)
			value = strings.ReplaceAll(value, "\\\n", " ")
			value = strings.ReplaceAll(value, "\n", " ")
			cells = append(cells, strings.ReplaceAll(value, "|", "\\|"))
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
	}
	cols := len(n.Children[0].Children)
	sep := make([]string, cols)
	for i := range sep {
		switch n.Children[0].Children[i].Align {
		case "left":
			sep[i] = ":---"
		case "center":
			sep[i] = ":---:"
		case "right":
			sep[i] = "---:"
		default:
			sep[i] = "---"
		}
	}
	lines = append(lines[:1], append([]string{"| " + strings.Join(sep, " | ") + " |"}, lines[1:]...)...)
	return strings.Join(lines, "\n")
}
func escape(s string) string {
	var b strings.Builder
	lineStart := true
	markerAt := orderedListMarker(s)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == '\n' {
			b.WriteRune(r)
			lineStart = true
			i += size
			marker := orderedListMarker(s[i:])
			if marker >= 0 {
				markerAt = i + marker
			} else {
				markerAt = -1
			}
			continue
		}
		escapeRune := strings.ContainsRune("\\*[]<>_`&", r) || i == markerAt
		if lineStart {
			escapeRune = escapeRune || r == '#' || r == '>' || r == '-' || r == '+'
		}
		if escapeRune {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
		lineStart = false
		i += size
	}
	return b.String()
}

func orderedListMarker(s string) int {
	i := 0
	for i < len(s) && i < 9 && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i < len(s) && (s[i] == '.' || s[i] == ')') && i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t') {
		return i
	}
	return -1
}
func plain(n *Node) string {
	if n == nil {
		return ""
	}
	if n.Value != "" {
		return clean(n.Value)
	}
	var a []string
	for _, x := range n.Children {
		if v := plain(x); v != "" {
			a = append(a, v)
		}
	}
	return strings.Join(a, " ")
}
