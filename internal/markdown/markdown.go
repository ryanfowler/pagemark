// Package markdown converts selected HTML nodes to a safe Markdown tree.
package markdown

import (
	"fmt"
	"net/url"
	"regexp"
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
)

type Node struct {
	Kind     Kind
	Level    int
	Value    string
	URL      string
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
	Markdown, Text string
	Links          []LinkValue
	Images         []ImageValue
	Sections       []SectionValue
	Rejected       []string
	Truncated      bool
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
		return &Node{Kind: CodeBlock, Value: textRaw(n)}
	case "blockquote":
		return &Node{Kind: Blockquote, Children: c.blocks(n)}
	case "ul":
		return &Node{Kind: UnorderedList, Children: c.listItems(n)}
	case "ol":
		return &Node{Kind: OrderedList, Children: c.listItems(n)}
	case "dl":
		return &Node{Kind: UnorderedList, Children: c.definitionItems(n)}
	case "table":
		if !c.cfg.Tables {
			return c.fallbackTable(n)
		}
		return c.table(n)
	case "hr":
		return &Node{Kind: ThematicBreak}
	case "figure", "article", "section", "main", "div":
		return &Node{Kind: Document, Children: c.blocks(n)}
	default:
		children := c.blocks(n)
		if len(children) > 0 {
			return &Node{Kind: Document, Children: children}
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
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if c.skip(ch) {
			continue
		}
		if ch.Type == html.TextNode {
			if v := clean(ch.Data); v != "" {
				out = append(out, &Node{Kind: Paragraph, Children: []*Node{{Kind: Text, Value: v}}})
			}
			continue
		}
		if x := c.block(ch); x != nil {
			if x.Kind == Document {
				out = append(out, x.Children...)
			} else {
				out = append(out, x)
			}
		}
	}
	return out
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
			out = append(out, &Node{Kind: Text, Value: "\n"})
			return
		case "form", "button", "input", "select", "textarea":
			return
		case "a":
			label := clean(c.nodeText(x))
			href := attr(x, "href")
			if c.cfg.Links && label != "" && c.linkCount < c.cfg.MaxLinks {
				if safe, ok := c.safeURL(href); ok {
					out = append(out, &Node{Kind: Link, URL: safe, Children: []*Node{{Kind: Text, Value: label}}})
					c.linkCount++
					return
				}
			}
			if href != "" {
				c.rejected = append(c.rejected, href)
			}
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
			out = append(out, &Node{Kind: InlineCode, Value: textRaw(x)})
			return
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

func (c *converter) listItems(n *html.Node) []*Node {
	var out []*Node
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if c.skip(ch) {
			continue
		}
		if ch.Type == html.ElementNode && strings.EqualFold(ch.Data, "li") {
			out = append(out, &Node{Kind: ListItem, Children: c.mixedItem(ch)})
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
		if ch.Type == html.ElementNode && isListItemBlock(strings.ToLower(ch.Data)) {
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

func isListItemBlock(tag string) bool {
	switch tag {
	case "p", "div", "pre", "blockquote", "ul", "ol", "dl", "table", "figure":
		return true
	}
	return false
}
func (c *converter) definitionItems(n *html.Node) []*Node {
	var out []*Node
	var term string
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if c.skip(ch) || ch.Type != html.ElementNode {
			continue
		}
		if strings.EqualFold(ch.Data, "dt") {
			term = clean(c.nodeText(ch))
		}
		if strings.EqualFold(ch.Data, "dd") {
			v := clean(c.nodeText(ch))
			if term != "" {
				v = term + ": " + v
			}
			out = append(out, &Node{Kind: ListItem, Children: []*Node{{Kind: Paragraph, Children: []*Node{{Kind: Text, Value: v}}}}})
		}
	}
	return out
}

func (c *converter) table(n *html.Node) *Node {
	var rows [][]*html.Node
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if c.skip(x) {
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
	if len(rows) == 0 {
		return nil
	}
	width := len(rows[0])
	if width == 0 || c.cells+width*len(rows) > c.cfg.MaxTableCells {
		return c.fallbackTable(n)
	}
	for _, r := range rows {
		if len(r) != width {
			return c.fallbackTable(n)
		}
	}
	t := &Node{Kind: Table}
	for _, r := range rows {
		rr := &Node{Kind: TableRow}
		for _, cell := range r {
			rr.Children = append(rr.Children, &Node{Kind: TableCell, Children: c.inlines(cell)})
			c.cells++
		}
		t.Children = append(t.Children, rr)
	}
	return t
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
func textRaw(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if dom.Hidden(x) {
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
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func render(doc *Node, max int) Result {
	var markdownBlocks, keptText []string
	var keptNodes []*Node
	used, truncated := 0, false
	for _, n := range doc.Children {
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
	return Result{
		Markdown: md, Text: clean(strings.Join(keptText, "\n")),
		Links: links, Images: images, Sections: retainedSections(keptNodes), Truncated: truncated,
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
		return f + "\n" + strings.TrimRight(strings.ReplaceAll(n.Value, "\r\n", "\n"), "\n") + "\n" + f
	case Blockquote:
		s := renderBlock(&Node{Kind: Document, Children: n.Children}, depth)
		return "> " + strings.ReplaceAll(s, "\n", "\n> ")
	case UnorderedList, OrderedList:
		var a []string
		for i, x := range n.Children {
			body := renderBlock(x, depth+1)
			mark := "- "
			if n.Kind == OrderedList {
				mark = fmt.Sprintf("%d. ", i+1)
			}
			a = append(a, mark+strings.ReplaceAll(body, "\n", "\n  "))
		}
		return strings.Join(a, "\n")
	case ListItem:
		var a []string
		for _, x := range n.Children {
			a = append(a, renderBlock(x, depth))
		}
		return strings.Join(a, "\n")
	case Table:
		return renderTable(n)
	case ThematicBreak:
		return "---"
	}
	return renderInline([]*Node{n})
}
func renderInline(ns []*Node) string {
	var b strings.Builder
	for _, n := range ns {
		switch n.Kind {
		case Text:
			b.WriteString(escape(n.Value))
		case Emphasis:
			b.WriteString("*")
			b.WriteString(renderInline(n.Children))
			b.WriteString("*")
		case Strong:
			b.WriteString("**")
			b.WriteString(renderInline(n.Children))
			b.WriteString("**")
		case InlineCode:
			v := n.Value
			tick := "`"
			for strings.Contains(v, tick) {
				tick += "`"
			}
			b.WriteString(tick)
			if strings.HasPrefix(v, "`") || strings.HasSuffix(v, "`") {
				b.WriteByte(' ')
				b.WriteString(v)
				b.WriteByte(' ')
			} else {
				b.WriteString(v)
			}
			b.WriteString(tick)
		case Link:
			b.WriteString("[")
			b.WriteString(renderInline(n.Children))
			b.WriteString("](")
			b.WriteString(strings.NewReplacer("(", "%28", ")", "%29").Replace(n.URL))
			b.WriteString(")")
		case Image:
			b.WriteString("Image: ")
			b.WriteString(escape(n.Value))
			if n.URL != "" {
				b.WriteString(" (")
				b.WriteString(strings.NewReplacer("(", "%28", ")", "%29").Replace(n.URL))
				b.WriteString(")")
			}
		}
	}
	return b.String()
}
func renderTable(n *Node) string {
	if len(n.Children) == 0 {
		return ""
	}
	var lines []string
	for _, r := range n.Children {
		var cells []string
		for _, c := range r.Children {
			cells = append(cells, strings.ReplaceAll(renderInline(c.Children), "|", "\\|"))
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
	}
	cols := len(n.Children[0].Children)
	sep := make([]string, cols)
	for i := range sep {
		sep[i] = "---"
	}
	lines = append(lines[:1], append([]string{"| " + strings.Join(sep, " | ") + " |"}, lines[1:]...)...)
	return strings.Join(lines, "\n")
}
func escape(s string) string {
	var b strings.Builder
	for i, r := range s {
		if strings.ContainsRune("\\\\*[]<>_#`", r) || (i == 0 && (r == '-' || r == '+' || r == '>')) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
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
