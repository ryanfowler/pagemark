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
	Superscript
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
	Kind          Kind
	Level         int
	Start         int
	Reversed      bool
	HasValue      bool
	Value         string
	URL           string
	Info          string
	Align         string
	Children      []*Node
	section       bool
	sourceSection *html.Node
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
	// TextPreformatted marks a page-wide text interface that should retain inline
	// links and line breaks instead of being emitted as source code.
	TextPreformatted func(*html.Node) bool
	// PruneEmptyHeadings removes headings that do not label any emitted content.
	// It is intended for extraction, after selection and exclusions are final.
	PruneEmptyHeadings bool
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
	heading               *html.Node
	textPreformatted      bool
}

func Convert(nodes []*html.Node, cfg Config) Result {
	c := &converter{cfg: cfg}
	doc := &Node{Kind: Document}
	for _, n := range nodes {
		if x := c.block(n); x != nil {
			doc.Children = append(doc.Children, x)
		}
	}
	if cfg.PruneEmptyHeadings {
		pruneEmptySections(doc)
	}
	r := render(doc, cfg.MaxBytes, cfg.PruneEmptyHeadings)
	r.Rejected = c.rejected
	return r
}

func enclosingSection(n *html.Node) *html.Node {
	for current := n; current != nil; current = current.Parent {
		if current.Type == html.ElementNode && strings.EqualFold(current.Data, "section") {
			return current
		}
	}
	return nil
}

func (c *converter) skip(n *html.Node) bool {
	if n == nil || (c.cfg.Exclude != nil && c.cfg.Exclude(n)) {
		return true
	}
	if dom.Hidden(n) {
		// SVG is opaque to all generic traversal. The converter only admits its
		// accessible label through the dedicated SVG branch below.
		return !(c.cfg.Images && dom.AccessibleSVGLabel(n) != "")
	}
	return false
}

func (c *converter) block(n *html.Node) (result *Node) {
	defer func() {
		if result != nil {
			result.sourceSection = enclosingSection(n)
		}
	}()
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
		previousHeading := c.heading
		c.heading = n
		children := c.inlines(n)
		c.heading = previousHeading
		return &Node{Kind: Heading, Level: int(tag[1] - '0'), Children: children}
	case "p", "figcaption", "caption", "dt", "dd":
		return &Node{Kind: Paragraph, Children: c.inlines(n)}
	case "img", "svg":
		// A visual may be a direct child of article/main rather than wrapped in a
		// paragraph. Process the element itself as inline content.
		return &Node{Kind: Paragraph, Children: c.inlineNodes([]*html.Node{n})}
	case "pre":
		if c.cfg.TextPreformatted != nil && c.cfg.TextPreformatted(n) {
			previous := c.textPreformatted
			c.textPreformatted = true
			inlines := c.inlines(n)
			c.textPreformatted = previous
			// Physical lines are separate blocks. Besides reflecting archive record
			// structure, this gives the output limiter safe boundaries on very long
			// indexes instead of making the entire interface one indivisible block.
			var lines []*Node
			var line []*Node
			flush := func() {
				paragraph := &Node{Kind: Paragraph, Children: line}
				if strings.TrimSpace(plain(paragraph)) != "" {
					lines = append(lines, paragraph)
				}
				line = nil
			}
			for _, child := range inlines {
				if child.Kind == HardBreak {
					flush()
				} else {
					line = append(line, child)
				}
			}
			flush()
			return &Node{Kind: Document, Children: lines}
		}
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
	case "html", "body", "figure", "article", "main", "div", "aside", "header", "footer", "nav", "address", "details":
		return &Node{Kind: Document, Children: c.blocks(n)}
	case "section":
		return &Node{Kind: Document, Children: c.blocks(n), section: true}
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
			out = append(out, &Node{Kind: Paragraph, Children: in, sourceSection: enclosingSection(n)})
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
	var walkSiblings func([]*html.Node)
	walk = func(x *html.Node) {
		if c.skip(x) {
			return
		}
		if x.Type == html.TextNode {
			if c.textPreformatted {
				value := strings.ReplaceAll(strings.ReplaceAll(x.Data, "\r\n", "\n"), "\r", "\n")
				parts := strings.Split(value, "\n")
				for i, part := range parts {
					if v := inlineText(part); strings.TrimSpace(v) != "" {
						out = append(out, &Node{Kind: Text, Value: v})
					}
					if i < len(parts)-1 {
						out = append(out, &Node{Kind: HardBreak})
					}
				}
				return
			}
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
			if c.decorativeHeadingPermalink(x) {
				return
			}
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
		case "svg":
			// Inline SVG has no image URL to report. Preserve an accessible name
			// as a concise textual stand-in rather than silently dropping a
			// meaningful diagram. It still consumes the image budget so SVG cannot
			// bypass the configured visual-output limit.
			if c.cfg.Images && c.imageCount < c.cfg.MaxImages {
				if label := clean(dom.AccessibleSVGLabel(x)); label != "" {
					out = append(out, &Node{Kind: Text, Value: "Diagram: " + label})
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
		case "sup":
			if children := c.inlines(x); len(children) > 0 {
				out = append(out, &Node{Kind: Superscript, Children: children})
			}
			return
		case "code":
			if c.textPreformatted {
				out = append(out, c.inlines(x)...)
			} else {
				out = append(out, &Node{Kind: InlineCode, Value: c.textRaw(x)})
			}
			return
		}
		var children []*html.Node
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			children = append(children, ch)
		}
		walkSiblings(children)
	}
	walkSiblings = func(nodes []*html.Node) {
		var previous *html.Node
		for _, n := range nodes {
			if previous != nil && c.inlineBoundary(previous, n) {
				out = append(out, &Node{Kind: Text, Value: " "})
			}
			walk(n)
			previous = n
		}
	}
	walkSiblings(nodes)
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

// inlineBoundary preserves boundaries for elements whose HTML or ARIA
// semantics establish separate layout items. Ordinary inline elements are not
// boundaries: authors commonly split a single word across them for styling.
func (c *converter) inlineBoundary(left, right *html.Node) bool {
	if left.Type != html.ElementNode || right.Type != html.ElementNode ||
		c.skip(left) || c.skip(right) || strings.EqualFold(right.Data, "sup") ||
		!layoutItem(left) || !layoutItem(right) {
		return false
	}
	return clean(c.nodeText(left)) != "" && clean(c.nodeText(right)) != ""
}

func layoutItem(n *html.Node) bool {
	if isBlockElement(strings.ToLower(n.Data)) {
		return true
	}
	switch strings.ToLower(attr(n, "role")) {
	case "cell", "columnheader", "gridcell", "listitem", "row", "rowheader":
		return true
	}
	return false
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
	type rowItem struct {
		item     *Node
		level    int
		hasLevel bool
	}
	var rows []rowItem
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if c.skip(x) {
			return
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "tr") {
			// Layout tables commonly wrap each logical record in another table.
			// A wrapper row has no content of its own, so descend rather than
			// collapsing all of its nested rows into one enormous list item.
			if hasDescendantRow(x) && !c.tableRowHasOwnRenderableContent(x) {
				for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
					walk(ch)
				}
				return
			}
			item := &Node{Kind: ListItem, Children: c.mixedItem(x)}
			if clean(plain(item)) != "" {
				level, ok := tableRowLevel(x)
				rows = append(rows, rowItem{item: item, level: level, hasLevel: ok})
			}
			return
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)

	root := &Node{Kind: UnorderedList}
	lists := []*Node{root}
	var previous *Node
	previousLevel := 0
	for _, row := range rows {
		level := 0
		if row.hasLevel {
			level = row.level
			if level < 0 {
				level = 0
			}
			// A missing ancestor must not create empty list levels.
			if previous == nil || level > previousLevel+1 {
				level = previousLevel + 1
			}
		}
		if !row.hasLevel || previous == nil {
			level = 0
		}
		if level > previousLevel && previous != nil {
			nested := &Node{Kind: UnorderedList}
			previous.Children = append(previous.Children, nested)
			lists = append(lists[:previousLevel+1], nested)
		} else if level < len(lists)-1 {
			lists = lists[:level+1]
		}
		lists[level].Children = append(lists[level].Children, row.item)
		previous, previousLevel = row.item, level
	}
	return root
}

// tableRowHasOwnRenderableContent distinguishes a presentational wrapper row
// from a record which happens to contain a table. Nested tables are ignored,
// but visible text and non-text Markdown content outside them keep the row.
func (c *converter) tableRowHasOwnRenderableContent(n *html.Node) bool {
	var text []string
	renderable := false
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if renderable || c.skip(x) {
			return
		}
		if x != n && x.Type == html.ElementNode && strings.EqualFold(x.Data, "table") {
			return
		}
		if x.Type == html.TextNode {
			text = append(text, x.Data)
			return
		}
		if x.Type == html.ElementNode {
			switch strings.ToLower(x.Data) {
			case "hr", "pre":
				renderable = true
				return
			case "img":
				if c.cfg.Images && c.imageCount < c.cfg.MaxImages && clean(attr(x, "alt")) != "" {
					_, renderable = c.safeURL(attr(x, "src"))
				}
				return
			}
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return renderable || clean(strings.Join(text, " ")) != ""
}

func hasDescendantRow(n *html.Node) bool {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.ElementNode && strings.EqualFold(ch.Data, "tr") || hasDescendantRow(ch) {
			return true
		}
	}
	return false
}

// tableRowLevel recognizes common explicit hierarchy annotations on the row
// or its leading cell. It intentionally does not inspect arbitrary descendants:
// attributes such as aria-level may describe a heading inside a cell rather
// than the row's relationship to adjacent records.
func tableRowLevel(n *html.Node) (int, bool) {
	if level, ok := hierarchyLevel(n); ok {
		return level, true
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode || (!strings.EqualFold(ch.Data, "td") && !strings.EqualFold(ch.Data, "th")) {
			continue
		}
		return hierarchyLevel(ch)
	}
	return 0, false
}

func hierarchyLevel(n *html.Node) (int, bool) {
	for _, key := range []string{"aria-level", "data-depth", "data-level", "depth", "indent"} {
		if level, ok := parseIntegerAttr(n, key); ok {
			if key == "aria-level" {
				level--
			}
			return level, true
		}
	}
	return 0, false
}

// decorativeHeadingPermalink recognizes heading self-links without relying on
// their converted children. In particular, this prevents accessible SVG labels
// and visually hidden screen-reader text from becoming link labels.
func (c *converter) decorativeHeadingPermalink(link *html.Node) bool {
	if c.heading == nil || !headingFragmentMatches(c.heading, attr(link, "href")) ||
		!c.samePageFragment(attr(link, "href")) {
		return false
	}

	var text []string
	iconOnly := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if dom.Hidden(n) {
				iconOnly = true
				return
			}
			if strings.EqualFold(n.Data, "svg") || screenReaderOnly(n) {
				iconOnly = true
				return
			}
		}
		if n.Type == html.TextNode {
			text = append(text, n.Data)
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	for child := link.FirstChild; child != nil; child = child.NextSibling {
		walk(child)
	}
	label := clean(strings.Join(text, " "))
	return label == "#" || label == "¶" || label == "§" || (label == "" && iconOnly)
}

func screenReaderOnly(n *html.Node) bool {
	for _, class := range strings.Fields(strings.ToLower(attr(n, "class"))) {
		switch strings.Trim(class, "-_") {
		case "sr-only", "sr_only", "screen-reader-text", "screen_reader_text", "visually-hidden", "visually_hidden", "visuallyhidden":
			return true
		}
	}
	return false
}

func headingFragmentMatches(heading *html.Node, rawHref string) bool {
	u, err := url.Parse(strings.TrimSpace(rawHref))
	if err != nil || u.Fragment == "" {
		return false
	}
	fragment := u.Fragment
	var matches func(*html.Node) bool
	matches = func(n *html.Node) bool {
		if n.Type == html.ElementNode && (attr(n, "id") == fragment ||
			(strings.EqualFold(n.Data, "a") && attr(n, "name") == fragment)) {
			return true
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if matches(child) {
				return true
			}
		}
		return false
	}
	return matches(heading)
}

func (c *converter) samePageFragment(rawHref string) bool {
	u, err := url.Parse(strings.TrimSpace(rawHref))
	if err != nil || u.Fragment == "" {
		return false
	}
	if c.cfg.Base == nil {
		return u.Scheme == "" && u.Host == "" && u.Path == "" && u.RawQuery == ""
	}
	base := *c.cfg.Base
	base.Fragment, base.RawFragment = "", ""
	resolved := base.ResolveReference(u)
	resolved.Fragment, resolved.RawFragment = "", ""
	return strings.EqualFold(resolved.Scheme, base.Scheme) &&
		strings.EqualFold(resolved.Host, base.Host) && resolved.Path == base.Path &&
		resolved.RawQuery == base.RawQuery
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

// pruneEmptySections removes semantic sections whose exclusions and conversion
// left no emitted content other than headings. This prevents a section title
// from borrowing content that follows the section wrapper.
func pruneEmptySections(n *Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == Document {
		kept := n.Children[:0]
		for _, child := range n.Children {
			if pruneEmptySections(child) {
				kept = append(kept, child)
			}
		}
		n.Children = kept
		if n.section && !hasSubstantiveBlock(n) {
			return false
		}
	}
	return true
}

func hasSubstantiveBlock(n *Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == Document {
		for _, child := range n.Children {
			if hasSubstantiveBlock(child) {
				return true
			}
		}
		return false
	}
	return n.Kind != Heading && n.Kind != ThematicBreak && strings.TrimSpace(renderBlock(n, 0)) != ""
}

func render(doc *Node, max int, pruneHeadings bool) Result {
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
	if pruneHeadings {
		blocks = pruneStandaloneHeadings(blocks)
	}
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

// pruneStandaloneHeadings keeps a heading when a substantive block follows it
// before the next heading of equal or higher level. Lower-level headings do not
// end the range because their content also belongs to the enclosing section.
func pruneStandaloneHeadings(blocks []*Node) []*Node {
	kept := make([]*Node, 0, len(blocks))
	for i, block := range blocks {
		if block.Kind != Heading {
			kept = append(kept, block)
			continue
		}
		hasContent := false
		for _, following := range blocks[i+1:] {
			if following.Kind == Heading && following.Level <= block.Level {
				break
			}
			// A separately selected section heading must not borrow content
			// that follows its section wrapper. Content in a nested semantic
			// section still belongs to the enclosing section.
			if block.sourceSection != nil && !sectionWithin(following.sourceSection, block.sourceSection) {
				break
			}
			if hasSubstantiveBlock(following) {
				hasContent = true
				break
			}
		}
		if hasContent {
			kept = append(kept, block)
		}
	}
	return kept
}

func sectionWithin(section, ancestor *html.Node) bool {
	for current := section; current != nil; current = enclosingSection(current.Parent) {
		if current == ancestor {
			return true
		}
	}
	return false
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
		return strings.Repeat("#", l) + " " + strings.TrimSpace(renderInlineWithHardBreak(n.Children, " "))
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
	return renderInlineWithHardBreak(ns, "\\\n")
}

// renderInlineWithHardBreak lets contexts that cannot contain Markdown line
// breaks, such as ATX headings, render an HTML break as horizontal whitespace.
func renderInlineWithHardBreak(ns []*Node, hardBreak string) string {
	var b strings.Builder
	for i, n := range ns {
		var value string
		switch n.Kind {
		case Text:
			value = escape(n.Value)
		case Emphasis:
			value = renderDelimited(renderInlineWithHardBreak(n.Children, hardBreak), "*")
		case Strong:
			value = renderDelimited(renderInlineWithHardBreak(n.Children, hardBreak), "**")
		case Superscript:
			value = renderInlineWithHardBreak(n.Children, hardBreak)
			// A superscript made entirely from linked content commonly represents
			// a footnote reference. Adding a caret would create the invalid hybrid
			// ^[label](URL), so render that narrow case as ordinary linked text.
			if !superscriptIsLinkedReference(n) {
				if superscriptNeedsBounds(ns, i) {
					value = "^(" + value + ")"
				} else {
					value = "^" + value
				}
			}
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
			label := renderInlineWithHardBreak(n.Children, hardBreak)
			value = renderWrapped(label, "[", "]("+markdownDestination(n.URL)+")")
		case Image:
			value = "![" + escape(n.Value) + "](" + markdownDestination(n.URL) + ")"
		case HardBreak:
			value = hardBreak
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
	switch n.Kind {
	case Superscript:
		value := plainInlineNode(n)
		if superscriptIsLinkedReference(n) {
			return clean(value)
		}
		return clean("^" + value)
	case Heading, Paragraph, Text, Emphasis, Strong, InlineCode, Link, Image, HardBreak, TableCell:
		return clean(plainInlineNode(n))
	}
	if n.Value != "" {
		return clean(n.Value)
	}
	var values []string
	for _, x := range n.Children {
		if v := plain(x); v != "" {
			values = append(values, v)
		}
	}
	return strings.Join(values, " ")
}

func plainInlineNodes(ns []*Node) string {
	var b strings.Builder
	for i, n := range ns {
		value := plainInlineNode(n)
		if n.Kind == Superscript && !superscriptIsLinkedReference(n) {
			if superscriptNeedsBounds(ns, i) {
				value = "^(" + value + ")"
			} else {
				value = "^" + value
			}
		}
		writeInline(&b, value)
	}
	return b.String()
}

func plainInlineNode(n *Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind {
	case Text, InlineCode:
		return n.Value
	case Image:
		return n.Value
	case HardBreak:
		return " "
	case Superscript, Heading, Paragraph, Emphasis, Strong, Link, TableCell:
		return plainInlineNodes(n.Children)
	}
	return plain(n)
}

func superscriptIsLinkedReference(n *Node) bool {
	if n == nil {
		return false
	}
	hasLink := false
	var linkedOrWhitespace func([]*Node) bool
	linkedOrWhitespace = func(nodes []*Node) bool {
		for _, child := range nodes {
			switch child.Kind {
			case Link:
				hasLink = true
			case Text:
				if strings.TrimSpace(child.Value) != "" {
					return false
				}
			case HardBreak:
				// A line break is whitespace rather than meaningful reference text.
			case Emphasis, Strong:
				if !linkedOrWhitespace(child.Children) {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	return linkedOrWhitespace(n.Children) && hasLink
}

func superscriptNeedsBounds(ns []*Node, i int) bool {
	value := plainInlineNode(ns[i])
	if value == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(value)
	if !unicode.IsLetter(last) && !unicode.IsDigit(last) {
		return false
	}
	for j := i + 1; j < len(ns); j++ {
		next := plainInlineNode(ns[j])
		if next == "" {
			continue
		}
		first, _ := utf8.DecodeRuneInString(next)
		return unicode.IsLetter(first) || unicode.IsDigit(first)
	}
	return false
}
