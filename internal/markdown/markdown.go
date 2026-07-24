// Package markdown converts selected HTML nodes to a safe Markdown tree.
package markdown

import (
	"fmt"
	"io"
	"math/big"
	"net/url"
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
	controlURL    string
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
	if n == nil || isSourceCodeLineNumberGutter(n) || (c.cfg.Exclude != nil && c.cfg.Exclude(n)) {
		return true
	}
	if dom.Hidden(n) {
		// SVG is opaque to all generic traversal. The converter only admits its
		// accessible label through the dedicated SVG branch below.
		return !(c.cfg.Images && dom.AccessibleSVGLabel(n) != "")
	}
	return false
}

// Syntax highlighters often use a two-column layout table whose first cell is
// only a line-number gutter. It is presentation, not source code: retaining it
// produces a second fenced block and corrupts copied examples. Require an
// exact gutter class, a recognized highlighter table, and a sibling source-code
// cell so an ordinary data column with the same class remains intact.
func isSourceCodeLineNumberGutter(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || (!strings.EqualFold(n.Data, "td") && !strings.EqualFold(n.Data, "th")) || !hasAnyClassToken(n, "linenos", "line-numbers", "linenumbers", "rouge-gutter") {
		return false
	}
	row := n.Parent
	if row == nil || !strings.EqualFold(row.Data, "tr") {
		return false
	}
	var table *html.Node
	for p := row.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, "table") {
			table = p
			break
		}
	}
	if table == nil || !hasAnyClassToken(table, "highlighttable", "highlight-table", "codehilitetable", "rouge-table") {
		return false
	}
	for sibling := row.FirstChild; sibling != nil; sibling = sibling.NextSibling {
		if sibling == n || sibling.Type != html.ElementNode || (!strings.EqualFold(sibling.Data, "td") && !strings.EqualFold(sibling.Data, "th")) {
			continue
		}
		if hasDescendantElement(sibling, "pre") && (hasDescendantElement(sibling, "code") || hasAnyClassToken(sibling, "code", "source")) {
			return true
		}
	}
	return false
}

func hasAnyClassToken(n *html.Node, wanted ...string) bool {
	for _, token := range strings.Fields(strings.ToLower(attr(n, "class"))) {
		for _, candidate := range wanted {
			if token == candidate {
				return true
			}
		}
	}
	return false
}

func hasDescendantElement(n *html.Node, tag string) bool {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if (child.Type == html.ElementNode && strings.EqualFold(child.Data, tag)) || hasDescendantElement(child, tag) {
			return true
		}
	}
	return false
}

func (c *converter) block(n *html.Node) (result *Node) {
	defer func() {
		if result != nil {
			result.sourceSection = enclosingSection(n)
			// Responsive components sometimes wrap a block paragraph in an anchor.
			// Starting conversion at that paragraph loses the inline Link node, so
			// retain only its destination for adjacent-control comparison.
			if result.Kind == Paragraph {
				for current := n; current != nil; current = current.Parent {
					if current.Type == html.ElementNode && strings.EqualFold(current.Data, "a") {
						if safe, ok := c.safeURL(attr(current, "href")); ok {
							result.controlURL = safe
						}
						break
					}
				}
			}
		}
	}()
	if c.skip(n) {
		return nil
	}
	if n.Type == html.TextNode {
		if suppressSerializedMediaText(n) {
			return nil
		}
		v := clean(n.Data)
		if v != "" {
			return &Node{Kind: Paragraph, Children: []*Node{{Kind: Text, Value: v}}}
		}
		return nil
	}
	tag := strings.ToLower(n.Data)
	// A selected math wrapper may itself be block-like (or even be the selected
	// root). Route the wrapper through the opaque inline math handling rather
	// than traversing its equivalent branches as separate blocks.
	if _, ok := c.mathRepresentation(n); ok {
		return &Node{Kind: Paragraph, Children: c.inlineNodes([]*html.Node{n})}
	}
	// ARIA tables and grids are frequently built entirely from divs. Handle the
	// explicit table root before generic container traversal so row/cell
	// relationships are not flattened into prose.
	if tag != "table" && semanticTableRole(n) {
		if !c.cfg.Tables {
			return c.fallbackSemanticTable(n)
		}
		return c.semanticTable(n)
	}
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
		return &Node{Kind: CodeBlock, Value: c.textRawPreformatted(n), Info: codeInfo(n)}
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
		if ch.Type != html.ElementNode || (!isBlockElement(strings.ToLower(ch.Data)) && !semanticTableRole(ch) && !c.hasBlockDescendant(ch)) {
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
		if isBlockElement(strings.ToLower(child.Data)) || semanticTableRole(child) || c.hasBlockDescendant(child) {
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
			if suppressSerializedMediaText(x) {
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
		if value, ok := c.mathRepresentation(x); ok {
			if value != "" {
				out = append(out, &Node{Kind: Text, Value: value})
			}
			return
		}
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
			if children := c.inlines(x); len(children) > 0 {
				out = append(out, &Node{Kind: Emphasis, Children: children})
			} else if c.hasWhitespace(x) {
				// Editors sometimes wrap only the separating space in formatting
				// markup. The empty emphasis disappears when rendered, but the space
				// still separates the surrounding words.
				out = append(out, &Node{Kind: Text, Value: " "})
			}
			return
		case "strong", "b":
			if children := c.inlines(x); len(children) > 0 {
				out = append(out, &Node{Kind: Strong, Children: children})
			} else if c.hasWhitespace(x) {
				out = append(out, &Node{Kind: Text, Value: " "})
			}
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

// mathRepresentation treats a rendered equation as one opaque inline value.
// Math renderers commonly put an accessible tree, a TeX source annotation, and
// a visual glyph tree next to one another. Walking such a wrapper normally
// concatenates two or three equivalent formulas.
func (c *converter) mathRepresentation(n *html.Node) (string, bool) {
	if n == nil || n.Type != html.ElementNode {
		return "", false
	}
	tag := strings.ToLower(n.Data)
	isMath := tag == "math" || tag == "mjx-container" ||
		strings.EqualFold(strings.TrimSpace(attr(n, "role")), "math") ||
		hasClassToken(n, "katex") || hasClassToken(n, "mathjax")
	if !isMath {
		return "", false
	}

	// An explicit accessible name is already a concise textual rendering.
	if label := clean(attr(n, "aria-label")); label != "" {
		return label, true
	}
	if tag == "math" {
		return c.mathElementText(n), true
	}

	// Prefer an explicitly assistive branch. This also covers KaTeX and MathJax
	// trees whose visual sibling is aria-hidden.
	if branch := c.firstMathDescendant(n, func(x *html.Node) bool {
		return x.Type == html.ElementNode && (screenReaderOnly(x) ||
			hasClassToken(x, "katex-mathml") || hasClassToken(x, "mjx-assistive-mml"))
	}); branch != nil {
		if math := c.firstMathDescendantOrSelf(branch, func(x *html.Node) bool {
			return x.Type == html.ElementNode && strings.EqualFold(x.Data, "math")
		}); math != nil {
			if value := c.mathElementText(math); value != "" {
				return value, true
			}
		}
		if value := clean(c.visibleMathText(branch, false)); value != "" {
			return value, true
		}
	}

	if math := c.firstMathDescendant(n, func(x *html.Node) bool {
		return x.Type == html.ElementNode && strings.EqualFold(x.Data, "math")
	}); math != nil {
		if value := c.mathElementText(math); value != "" {
			return value, true
		}
	}

	// Last resort for renderer versions with only a visual tree. Only
	// aria-hidden is relaxed here. Non-content elements, other hidden states,
	// and extraction-specific exclusions remain pruned.
	return clean(c.visibleMathText(n, true)), true
}

func (c *converter) mathElementText(math *html.Node) string {
	if label := clean(attr(math, "aria-label")); label != "" {
		return label
	}
	if annotation := c.firstMathDescendant(math, func(n *html.Node) bool {
		if n.Type != html.ElementNode || !strings.EqualFold(n.Data, "annotation") {
			return false
		}
		encoding := strings.ToLower(strings.TrimSpace(attr(n, "encoding")))
		return encoding == "application/x-tex" || encoding == "application/tex" ||
			encoding == "text/tex" || strings.Contains(encoding, "latex")
	}); annotation != nil {
		if value := cleanTeXAnnotation(c.rawMathText(annotation)); value != "" {
			return value
		}
	}
	return clean(c.mathMLText(math))
}

func cleanTeXAnnotation(value string) string {
	// TeX spacing commands and escapes for ordinary punctuation are useful to
	// the renderer but noisy after the formula becomes escaped Markdown text.
	value = strings.NewReplacer(
		`\ `, " ", `\,`, " ", `\;`, " ", `\:`, " ", `\!`, "",
		`\\`, " ", `\%`, "%", `\&`, "&", `\#`, "#", `\$`, "$", `\_`, "_",
	).Replace(value)
	return clean(value)
}

func (c *converter) mathMLText(n *html.Node) string {
	if c.pruneMathNode(n, false) {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Type != html.ElementNode {
		return ""
	}
	tag := strings.ToLower(n.Data)
	if tag == "annotation" || tag == "annotation-xml" {
		return ""
	}
	var parts []string
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		parts = append(parts, c.mathMLText(child))
	}
	joined := strings.Join(parts, "")
	switch tag {
	case "msup":
		return c.mathScript(n, "^")
	case "msub":
		return c.mathScript(n, "_")
	case "msubsup":
		return c.mathScripts(n)
	case "mfrac":
		return c.mathFraction(n)
	case "msqrt":
		return "sqrt(" + joined + ")"
	case "mroot":
		children := c.mathElementChildren(n)
		if len(children) >= 2 {
			return "root(" + c.mathMLText(children[0]) + ", " + c.mathMLText(children[1]) + ")"
		}
	}
	return joined
}

func (c *converter) mathScript(n *html.Node, operator string) string {
	children := c.mathElementChildren(n)
	if len(children) < 2 {
		return strings.Join(c.childMathTexts(n), "")
	}
	return c.mathMLText(children[0]) + operator + braceMath(c.mathMLText(children[1]))
}

func (c *converter) mathScripts(n *html.Node) string {
	children := c.mathElementChildren(n)
	if len(children) < 3 {
		return strings.Join(c.childMathTexts(n), "")
	}
	return c.mathMLText(children[0]) + "_" + braceMath(c.mathMLText(children[1])) +
		"^" + braceMath(c.mathMLText(children[2]))
}

func (c *converter) mathFraction(n *html.Node) string {
	children := c.mathElementChildren(n)
	if len(children) < 2 {
		return strings.Join(c.childMathTexts(n), "")
	}
	return "(" + c.mathMLText(children[0]) + ")/(" + c.mathMLText(children[1]) + ")"
}

func braceMath(s string) string {
	s = clean(s)
	if utf8.RuneCountInString(s) <= 1 {
		return s
	}
	return "{" + s + "}"
}

func (c *converter) childMathTexts(n *html.Node) []string {
	var out []string
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		out = append(out, c.mathMLText(child))
	}
	return out
}

func (c *converter) mathElementChildren(n *html.Node) []*html.Node {
	var out []*html.Node
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && !c.pruneMathNode(child, false) {
			out = append(out, child)
		}
	}
	return out
}

func (c *converter) visibleMathText(n *html.Node, includeAriaHidden bool) string {
	if c.pruneMathNode(n, includeAriaHidden) {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Type != html.ElementNode || strings.EqualFold(n.Data, "annotation") ||
		strings.EqualFold(n.Data, "annotation-xml") {
		return ""
	}
	var out strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		out.WriteString(c.visibleMathText(child, includeAriaHidden))
	}
	return out.String()
}

func (c *converter) rawMathText(n *html.Node) string {
	if c.pruneMathNode(n, false) {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	var out strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		out.WriteString(c.rawMathText(child))
	}
	return out.String()
}

func (c *converter) pruneMathNode(n *html.Node, includeAriaHidden bool) bool {
	if n == nil || (c.cfg.Exclude != nil && c.cfg.Exclude(n)) {
		return true
	}
	if includeAriaHidden {
		return dom.HiddenExceptARIA(n)
	}
	return dom.Hidden(n)
}

func (c *converter) firstMathDescendant(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if c.pruneMathNode(child, false) {
			continue
		}
		if match(child) {
			return child
		}
		if found := c.firstMathDescendant(child, match); found != nil {
			return found
		}
	}
	return nil
}

func (c *converter) firstMathDescendantOrSelf(n *html.Node, match func(*html.Node) bool) *html.Node {
	if c.pruneMathNode(n, false) {
		return nil
	}
	if match(n) {
		return n
	}
	return c.firstMathDescendant(n, match)
}

func hasClassToken(n *html.Node, want string) bool {
	for _, class := range strings.Fields(attr(n, "class")) {
		if strings.EqualFold(class, want) {
			return true
		}
	}
	return false
}

// inlineBoundary preserves boundaries for elements whose HTML or ARIA
// semantics establish separate layout items. Ordinary inline elements are not
// boundaries: authors commonly split a single word across them for styling.
func (c *converter) inlineBoundary(left, right *html.Node) bool {
	if left.Type != html.ElementNode || right.Type != html.ElementNode ||
		c.skip(left) || c.skip(right) || strings.EqualFold(right.Data, "sup") {
		return false
	}
	if !layoutItem(left) || !layoutItem(right) {
		if !c.structuredNumberedRecordSiblings(left, right) {
			return false
		}
	}
	return clean(c.nodeText(left)) != "" && clean(c.nodeText(right)) != ""
}

// structuredNumberedRecordSiblings recognizes compact layout rows whose source
// omits whitespace because CSS supplies the columns. A leading integer, a
// bold label, and a description are common for numbered procedures and visual
// explainers. Requiring that complete shape avoids adding spaces between
// arbitrary styling spans which may deliberately split a single word.
func (c *converter) structuredNumberedRecordSiblings(left, right *html.Node) bool {
	if left.Parent == nil || left.Parent != right.Parent || left.Parent.Type != html.ElementNode ||
		!strings.EqualFold(left.Parent.Data, "div") {
		return false
	}
	var fields []*html.Node
	for ch := left.Parent.FirstChild; ch != nil; ch = ch.NextSibling {
		if c.skip(ch) || ch.Type == html.TextNode && strings.TrimSpace(ch.Data) == "" {
			continue
		}
		if ch.Type != html.ElementNode {
			return false
		}
		fields = append(fields, ch)
	}
	if len(fields) != 3 {
		return false
	}
	// CSS-driven columns need styling hooks on both the row and ordinal field.
	// Without them, this shape is also common in ordinary inline markup such as
	// <span>1</span><b>st</b><span> place</span>.
	if !hasStyleHook(left.Parent) || !hasStyleHook(fields[0]) {
		return false
	}
	ordinal := clean(c.nodeText(fields[0]))
	if ordinal == "" || strings.IndexFunc(ordinal, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return false
	}
	labelTag := strings.ToLower(fields[1].Data)
	return labelTag == "b" || labelTag == "strong"
}

func hasStyleHook(n *html.Node) bool {
	return strings.TrimSpace(attr(n, "class")) != "" || strings.TrimSpace(attr(n, "id")) != ""
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
			children := c.mixedItem(ch)
			value, hasValue := parseIntegerAttr(ch, "value")
			item := &Node{Kind: ListItem, Level: value, HasValue: hasValue, Children: children}
			// Excluding a control inside a list item (for example, a skip link)
			// must not leave an empty Markdown bullet behind. Check converted
			// content because an excluded descendant may leave an empty wrapper.
			if strings.TrimSpace(renderBlock(item, 0)) == "" {
				continue
			}
			out = append(out, item)
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

type tableSource struct {
	rows     [][]*html.Node
	caption  *html.Node
	ariaGrid bool
	semantic bool
	title    *html.Node
}

func (c *converter) table(n *html.Node) *Node {
	s := tableSource{ariaGrid: strings.EqualFold(strings.TrimSpace(attr(n, "role")), "grid")}
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if c.skip(x) {
			return
		}
		// Never borrow rows or captions from a nested table.
		if x != n && x.Type == html.ElementNode && strings.EqualFold(x.Data, "table") {
			return
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "caption") {
			if s.caption == nil {
				s.caption = x
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
				s.rows = append(s.rows, row)
			}
			return
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return c.buildTable(s, func() *Node { return c.fallbackTable(n) })
}

func semanticTableRole(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(attr(n, "role"))) {
	case "table", "grid":
		return true
	}
	return false
}

// semanticTable reconstructs only explicitly bounded ARIA rows and cells. In
// particular, class names and repeated div layouts are not considered table
// evidence, which keeps ordinary card grids as prose.
func (c *converter) semanticTable(n *html.Node) *Node {
	s := tableSource{semantic: true, ariaGrid: strings.EqualFold(strings.TrimSpace(attr(n, "role")), "grid")}
	var rows func(*html.Node)
	rows = func(x *html.Node) {
		if c.skip(x) || (x != n && semanticTableRole(x)) {
			return
		}
		if x != n && strings.EqualFold(strings.TrimSpace(attr(x, "role")), "row") {
			var row []*html.Node
			var cells func(*html.Node)
			cells = func(y *html.Node) {
				if c.skip(y) {
					return
				}
				if y != x {
					role := strings.ToLower(strings.TrimSpace(attr(y, "role")))
					if role == "row" || semanticTableRole(y) {
						return
					}
					switch role {
					case "cell", "gridcell", "columnheader", "rowheader":
						row = append(row, y)
						return
					}
				}
				for ch := y.FirstChild; ch != nil; ch = ch.NextSibling {
					cells(ch)
				}
			}
			cells(x)
			if len(row) > 0 {
				s.rows = append(s.rows, row)
			}
			return
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			rows(ch)
		}
	}
	rows(n)
	return c.buildTable(s, func() *Node { return c.fallbackSemanticTable(n) })
}

func (c *converter) buildTable(s tableSource, fallbackContent func() *Node) *Node {
	caption := func() []*Node {
		if s.caption != nil {
			return c.inlines(s.caption)
		}
		return nil
	}
	fallback := func() *Node {
		// Captions render before fallback rows and must therefore consume link and
		// image budgets first as well.
		captions := caption()
		return tableWithCaption(fallbackContent(), captions)
	}
	if len(s.rows) == 0 {
		return tableWithCaption(nil, caption())
	}

	// A common publishing-table shape uses one full-width first cell as its
	// title, followed by the actual header row. Treat that cell like a caption,
	// but only after the remaining rectangular table has been validated.
	rows := s.rows
	if !s.semantic && len(rows) > 1 && len(rows[0]) == 1 {
		span := integerAttr(rows[0][0], "colspan", 1)
		if span > 1 && len(rows[1]) == span && integerAttr(rows[0][0], "rowspan", 1) == 1 {
			s.title = rows[0][0]
			rows = rows[1:]
		}
	}
	if len(rows) == 0 {
		return fallback()
	}
	width, total := len(rows[0]), 0
	for _, row := range rows {
		if len(row) != width {
			return fallback()
		}
		total += len(row)
		for _, cell := range row {
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
		role := strings.ToLower(strings.TrimSpace(attr(cell, "role")))
		header = header && (strings.EqualFold(cell.Data, "th") || role == "columnheader")
	}
	if !header && s.ariaGrid {
		// ARIA grids use gridcell for both their heading and data rows. The
		// explicit grid/row/cell contract and rectangular shape make promotion of
		// the first row substantially safer than guessing from arbitrary divs.
		header = len(rows) > 1 && width > 1
	}
	if !header && !s.semantic && s.title != nil {
		header = visualHeaderRow(c, rows[0])
	}
	// GFM requires a header row. Do not promote mixed native th/td rows: those
	// normally represent row headers rather than column headers.
	if !header {
		return fallback()
	}

	// Reserve the complete outer table before converting any content. Cell
	// conversion may encounter nested tables; those tables must observe the
	// reduced remaining budget rather than allowing the final total to exceed
	// MaxTableCells.
	c.cells += total

	// Caption and promoted-title content appears before body cells, so convert it
	// first to preserve document-order link and image limits.
	captions := caption()
	if s.title != nil {
		title := c.tableCellInlines(s.title)
		if clean(plain(&Node{Kind: Document, Children: captions})) != "" &&
			clean(plain(&Node{Kind: Document, Children: title})) != "" {
			captions = append(captions, &Node{Kind: Text, Value: " "})
		}
		captions = append(captions, title...)
	}

	t := &Node{Kind: Table}
	for _, row := range rows {
		rr := &Node{Kind: TableRow}
		for _, cell := range row {
			rr.Children = append(rr.Children, &Node{Kind: TableCell, Align: cellAlignment(cell), Children: c.tableCellInlines(cell)})
		}
		t.Children = append(t.Children, rr)
	}
	return tableWithCaption(t, captions)
}

// tableCellInlines compacts block-level and line-oriented cell content into a
// single Markdown-table line while retaining a boundary between each block.
// Running normal block conversion first also keeps link and image sanitization
// identical to content outside tables.
func (c *converter) tableCellInlines(n *html.Node) []*Node {
	parts := c.mixedItem(n)
	var out []*Node
	var flatten func(*Node, *[]*Node)
	flatten = func(x *Node, dst *[]*Node) {
		if x == nil {
			return
		}
		switch x.Kind {
		case Text, Emphasis, Strong, Superscript, InlineCode, Link, Image, HardBreak:
			*dst = append(*dst, x)
		case CodeBlock:
			// Markdown tables cannot contain fenced blocks. Preserve the code as a
			// compact inline span rather than dropping Value-only CodeBlock nodes.
			if value := clean(x.Value); value != "" {
				*dst = append(*dst, &Node{Kind: InlineCode, Value: value})
			}
		case Paragraph, Heading, TableCell:
			for _, child := range x.Children {
				flatten(child, dst)
			}
		default:
			// Documents, lists, and other block containers may place several
			// line-level children inside one wrapper div. Keep those boundaries.
			for _, child := range x.Children {
				var childIn []*Node
				flatten(child, &childIn)
				if clean(plain(&Node{Kind: Document, Children: childIn})) == "" {
					continue
				}
				if len(*dst) > 0 {
					*dst = append(*dst, &Node{Kind: Text, Value: " "})
				}
				*dst = append(*dst, childIn...)
			}
		}
	}
	for _, part := range parts {
		var in []*Node
		flatten(part, &in)
		if clean(plain(&Node{Kind: Document, Children: in})) == "" {
			continue
		}
		if len(out) > 0 {
			out = append(out, &Node{Kind: Text, Value: " "})
		}
		out = append(out, in...)
	}
	return out
}

// visualHeaderRow is deliberately narrow: it is only used after a full-width
// title row in a native table, and requires an empty corner plus fully bold
// column labels. This covers responsive publishing tables without making bold
// card layouts look tabular.
func visualHeaderRow(c *converter, row []*html.Node) bool {
	if len(row) < 2 || clean(c.nodeText(row[0])) != "" {
		return false
	}
	for _, cell := range row[1:] {
		if clean(c.nodeText(cell)) == "" || !allCellTextStrong(cell, false) {
			return false
		}
	}
	return true
}

func allCellTextStrong(n *html.Node, strong bool) bool {
	if n.Type == html.TextNode {
		return strings.TrimSpace(n.Data) == "" || strong
	}
	if n.Type == html.ElementNode {
		strong = strong || strings.EqualFold(n.Data, "b") || strings.EqualFold(n.Data, "strong")
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if !allCellTextStrong(ch, strong) {
			return false
		}
	}
	return true
}

func (c *converter) fallbackSemanticTable(n *html.Node) *Node {
	root := &Node{Kind: UnorderedList}
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if c.skip(x) || (x != n && semanticTableRole(x)) {
			return
		}
		if x != n && strings.EqualFold(strings.TrimSpace(attr(x, "role")), "row") {
			item := &Node{Kind: ListItem, Children: c.mixedItem(x)}
			if clean(plain(item)) != "" {
				root.Children = append(root.Children, item)
			}
			return
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return root
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
	// A single row whose cells each contain block structure is a common legacy
	// two-column page layout, not one data record. Preserve its headings,
	// paragraphs, and lists as normal document blocks instead of nesting the
	// entire page under a synthetic bullet. Inline key/value rows and multi-row
	// record tables continue through the list fallback below.
	if columns := c.layoutTableColumns(n); columns != nil {
		return &Node{Kind: Document, Children: columns}
	}

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

func (c *converter) layoutTableColumns(n *html.Node) []*Node {
	var rows, cells []*html.Node
	var visit func(*html.Node)
	visit = func(x *html.Node) {
		if c.skip(x) || x != n && x.Type == html.ElementNode && strings.EqualFold(x.Data, "table") {
			return
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "tr") {
			rows = append(rows, x)
			if len(rows) > 1 {
				return
			}
			for child := x.FirstChild; child != nil; child = child.NextSibling {
				if !c.skip(child) && child.Type == html.ElementNode && (strings.EqualFold(child.Data, "td") || strings.EqualFold(child.Data, "th")) {
					cells = append(cells, child)
				}
			}
			return
		}
		for child := x.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(n)
	if len(rows) != 1 || len(cells) < 2 {
		return nil
	}
	for _, cell := range cells {
		if !c.hasBlockDescendant(cell) {
			return nil
		}
	}
	var out []*Node
	for _, cell := range cells {
		out = append(out, c.mixedItem(cell)...)
	}
	return out
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

type serializedMedia struct {
	imageSources map[string]bool
	frameSources map[string]bool
}

// suppressSerializedMediaText recognizes CMS fallbacks which contain HTML
// markup as a text node. A fragment is suppressed only in a noscript context
// or when matching real media is present nearby; an otherwise ambiguous
// fragment may be a literal example and is retained.
func suppressSerializedMediaText(n *html.Node) bool {
	media, ok := parseSerializedMedia(n.Data)
	if ok {
		for ancestor := n.Parent; ancestor != nil; ancestor = ancestor.Parent {
			if ancestor.Type == html.ElementNode && strings.EqualFold(ancestor.Data, "noscript") {
				return true
			}
		}
		return matchingNearbyMedia(n, media)
	}
	// In a parsed document, body-level noscript fallback markup is represented
	// as one text node. Emitting that node exposes escaped tags (and usually a
	// duplicate of the rendered component) as prose. Suppress only a complete,
	// balanced HTML fragment; plain noscript text and examples outside noscript
	// retain their existing behavior.
	for ancestor := n.Parent; ancestor != nil; ancestor = ancestor.Parent {
		if ancestor.Type == html.ElementNode && strings.EqualFold(ancestor.Data, "noscript") {
			return serializedHTMLFragment(n.Data)
		}
	}
	return false
}

func serializedHTMLFragment(value string) bool {
	value = strings.TrimSpace(value)
	for i := 0; i < 3; i++ {
		decoded := html.UnescapeString(value)
		if decoded == value {
			break
		}
		value = strings.TrimSpace(decoded)
	}
	if !strings.HasPrefix(value, "<") || !strings.HasSuffix(value, ">") {
		return false
	}
	z := html.NewTokenizer(strings.NewReader(value))
	var stack []string
	elements := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return z.Err() == io.EOF && len(stack) == 0 && elements > 0
		case html.StartTagToken:
			tag := strings.ToLower(z.Token().Data)
			elements++
			if !isVoidHTMLElement(tag) {
				stack = append(stack, tag)
			}
		case html.SelfClosingTagToken:
			elements++
		case html.EndTagToken:
			tag := strings.ToLower(z.Token().Data)
			if len(stack) == 0 || stack[len(stack)-1] != tag {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
}

func isVoidHTMLElement(tag string) bool {
	switch tag {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}

// matchingNearbyMedia deliberately uses only adjacent siblings and a small
// immediate container. It must not search an entire article or body, where an
// unrelated tutorial example could happen to mention an image used elsewhere.
func matchingNearbyMedia(n *html.Node, media serializedMedia) bool {
	matches := func(current *html.Node, limit int) (bool, bool) {
		count := 0
		matched := false
		var walk func(*html.Node)
		walk = func(x *html.Node) {
			if count > limit {
				return
			}
			count++
			if x.Type == html.ElementNode {
				src := strings.TrimSpace(attr(x, "src"))
				switch strings.ToLower(x.Data) {
				case "img":
					matched = matched || media.imageSources[src]
				case "iframe", "embed":
					matched = matched || media.frameSources[src]
				}
			}
			for child := x.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
		walk(current)
		return matched, count <= limit
	}
	meaningfulSibling := func(sibling *html.Node, step func(*html.Node) *html.Node) *html.Node {
		for sibling != nil && sibling.Type == html.TextNode && strings.TrimSpace(sibling.Data) == "" {
			sibling = step(sibling)
		}
		return sibling
	}
	previous := meaningfulSibling(n.PrevSibling, func(x *html.Node) *html.Node { return x.PrevSibling })
	next := meaningfulSibling(n.NextSibling, func(x *html.Node) *html.Node { return x.NextSibling })
	for _, sibling := range []*html.Node{previous, next} {
		if sibling != nil {
			if matched, bounded := matches(sibling, 32); matched && bounded {
				return true
			}
		}
	}
	if n.Parent != nil && n.Parent.Type == html.ElementNode {
		switch strings.ToLower(n.Parent.Data) {
		case "p", "figure", "picture", "div", "span", "a", "li":
			if matched, bounded := matches(n.Parent, 32); matched && bounded {
				return true
			}
		}
	}
	return false
}

// parseSerializedMedia deliberately accepts only a complete fragment made
// from media elements and simple wrappers. In particular, prose which merely
// mentions an HTML tag is not classified as a fallback.
func parseSerializedMedia(value string) (serializedMedia, bool) {
	var result serializedMedia
	value = strings.TrimSpace(value)
	for i := 0; i < 3; i++ {
		decoded := html.UnescapeString(value)
		if decoded == value {
			break
		}
		value = strings.TrimSpace(decoded)
	}
	if !strings.HasPrefix(value, "<") || !strings.HasSuffix(value, ">") {
		return result, false
	}
	// Ordinary text nodes are overwhelmingly more common than serialized media.
	// Allocate the source sets only after the cheap fragment check succeeds.
	result.imageSources = make(map[string]bool)
	result.frameSources = make(map[string]bool)

	allowed := func(tag string) bool {
		switch tag {
		case "img", "picture", "source", "iframe", "div", "span", "figure", "a", "noscript":
			return true
		}
		return false
	}
	void := func(tag string) bool { return tag == "img" || tag == "source" }
	media := func(tag string) bool {
		return tag == "img" || tag == "picture" || tag == "source" || tag == "iframe"
	}

	z := html.NewTokenizer(strings.NewReader(value))
	var stack []string
	foundMedia := false
	for {
		tokenType := z.Next()
		switch tokenType {
		case html.ErrorToken:
			return result, z.Err() == io.EOF && len(stack) == 0 && foundMedia
		case html.TextToken:
			// iframe fallback contents are not article prose. Other wrappers must
			// be structurally empty apart from their media descendants.
			if strings.TrimSpace(string(z.Text())) != "" && (len(stack) == 0 || stack[len(stack)-1] != "iframe") {
				return result, false
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			token := z.Token()
			tag := strings.ToLower(token.Data)
			if !allowed(tag) {
				return result, false
			}
			foundMedia = foundMedia || media(tag)
			if src := strings.TrimSpace(attr(tokenToNode(token), "src")); src != "" {
				switch tag {
				case "img":
					result.imageSources[src] = true
				case "iframe":
					result.frameSources[src] = true
				}
			}
			if tokenType == html.StartTagToken && !void(tag) {
				stack = append(stack, tag)
			}
		case html.EndTagToken:
			tag := strings.ToLower(z.Token().Data)
			if !allowed(tag) || void(tag) || len(stack) == 0 || stack[len(stack)-1] != tag {
				return result, false
			}
			stack = stack[:len(stack)-1]
		case html.CommentToken:
			// Comments in an otherwise media-only fallback carry no prose.
		default:
			return result, false
		}
	}
}

func tokenToNode(token html.Token) *html.Node {
	return &html.Node{Type: html.ElementNode, Data: token.Data, Attr: token.Attr}
}

// clean trims Unicode whitespace and collapses the ASCII whitespace recognized
// by the previous regexp. It returns a slice of s without allocating when the
// input is already normalized, which is the common case for HTML text nodes.
func clean(s string) string {
	start := 0
	for start < len(s) {
		r, size := utf8.DecodeRuneInString(s[start:])
		if !unicode.IsSpace(r) {
			break
		}
		start += size
	}
	end := len(s)
	for end > start {
		r, size := utf8.DecodeLastRuneInString(s[:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	if start == end {
		return ""
	}
	firstSpace := -1
	for i := start; i < end; i++ {
		if asciiSpace(s[i]) {
			firstSpace = i
			break
		}
	}
	if firstSpace < 0 {
		return s[start:end]
	}
	var b strings.Builder
	b.Grow(end - start)
	b.WriteString(s[start:firstSpace])
	for i := firstSpace; i < end; {
		for i < end && asciiSpace(s[i]) {
			i++
		}
		b.WriteByte(' ')
		run := i
		for i < end && !asciiSpace(s[i]) {
			i++
		}
		b.WriteString(s[run:i])
	}
	return b.String()
}

func asciiSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}
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

// textRawPreformatted preserves layout-derived lines in addition to literal
// newlines. Syntax highlighters often represent every source line as a div,
// ARIA row, or span.line, leaving no newline text for textRaw to collect.
// Keeping this separate from textRaw ensures nested spans in inline code do not
// unexpectedly acquire line breaks.
func (c *converter) textRawPreformatted(n *html.Node) string {
	type extracted struct {
		value                                                               string
		present, firstLine, lastLine, firstEmptyLine, lastEmptyLine, brOnly bool
	}
	var extract func(*html.Node) extracted
	extract = func(x *html.Node) extracted {
		if c.skip(x) {
			return extracted{}
		}
		if x.Type == html.TextNode {
			return extracted{value: x.Data, present: true}
		}
		if x.Type != html.ElementNode {
			return extracted{}
		}
		tag := strings.ToLower(x.Data)
		if tag == "script" || tag == "style" {
			return extracted{}
		}
		if tag == "br" {
			return extracted{value: "\n", present: true, brOnly: true}
		}

		var b strings.Builder
		havePrevious := false
		allBROnly := true
		firstLine := false
		firstEmptyLine := false
		previousLastLine := false
		previousEmptyLine := false
		for child := x.FirstChild; child != nil; child = child.NextSibling {
			current := extract(child)
			if !current.present {
				continue
			}
			if havePrevious && (previousLastLine || current.firstLine) {
				// Newlines inside leading/trailing empty descendants separate those
				// descendants from their siblings; they cannot also supply the
				// boundary outside the neutral container.
				switch {
				case previousEmptyLine && current.firstEmptyLine:
					b.WriteByte('\n')
				case previousEmptyLine:
					if !startsLineBreak(current.value) {
						b.WriteByte('\n')
					}
				case current.firstEmptyLine:
					if !endsLineBreak(b.String()) {
						b.WriteByte('\n')
					}
				case !endsLineBreak(b.String()) && !startsLineBreak(current.value):
					b.WriteByte('\n')
				}
			}
			if !havePrevious {
				firstLine = current.firstLine
				firstEmptyLine = current.firstEmptyLine
			}
			b.WriteString(current.value)
			havePrevious = true
			allBROnly = allBROnly && current.brOnly
			previousLastLine = current.lastLine
			previousEmptyLine = current.lastEmptyLine
		}

		// Propagate line semantics through neutral containers. Highlighters may,
		// for example, wrap every span.line in an anchor or styling span.
		lineWrapper := x != n && preformattedLineWrapper(x)
		value := b.String()
		brPlaceholder := lineWrapper && havePrevious && allBROnly && value == "\n"
		if brPlaceholder {
			// A break is often used only to give an otherwise empty visual line
			// height. The wrapper supplies the line semantics in that case.
			value = ""
		}
		emptyLine := lineWrapper && (!havePrevious || brPlaceholder)
		return extracted{
			value:          value,
			present:        havePrevious || lineWrapper,
			firstLine:      lineWrapper || firstLine,
			lastLine:       lineWrapper || (havePrevious && previousLastLine),
			firstEmptyLine: emptyLine || (havePrevious && firstEmptyLine),
			lastEmptyLine:  emptyLine || (havePrevious && previousEmptyLine),
			brOnly:         havePrevious && allBROnly,
		}
	}

	return extract(n).value
}

func preformattedLineWrapper(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(n.Data)
	if isBlockElement(tag) {
		return true
	}
	switch tag {
	case "li", "dt", "dd", "tr":
		return true
	}
	switch strings.ToLower(attr(n, "role")) {
	case "listitem", "row":
		return true
	}
	for _, class := range strings.Fields(strings.ToLower(attr(n, "class"))) {
		switch class {
		case "line", "code-line", "line-content", "highlight-line":
			return true
		}
	}
	return false
}

func startsLineBreak(value string) bool {
	return strings.HasPrefix(value, "\n") || strings.HasPrefix(value, "\r")
}

func endsLineBreak(value string) bool {
	return strings.HasSuffix(value, "\n") || strings.HasSuffix(value, "\r")
}

func codeInfo(n *html.Node) string {
	valid := func(info string) string {
		if info != "" && strings.IndexFunc(info, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_+-#.", r)
		}) < 0 {
			return info
		}
		return ""
	}
	code := n.FirstChild
	for code != nil && code.Type != html.ElementNode {
		code = code.NextSibling
	}
	if code != nil && strings.EqualFold(code.Data, "code") {
		for _, class := range strings.Fields(attr(code, "class")) {
			lower := strings.ToLower(class)
			if strings.HasPrefix(lower, "language-") {
				if info := valid(class[len("language-"):]); info != "" {
					return info
				}
			} else if strings.HasPrefix(lower, "lang-") {
				if info := valid(class[len("lang-"):]); info != "" {
					return info
				}
			}
		}
	}
	// GitHub and compatible renderers put the language marker on the wrapper
	// around a bare pre rather than on a nested code element.
	if parent := n.Parent; parent != nil && parent.Type == html.ElementNode {
		for _, class := range strings.Fields(attr(parent, "class")) {
			const prefix = "highlight-source-"
			if strings.HasPrefix(strings.ToLower(class), prefix) {
				if info := valid(class[len(prefix):]); info != "" {
					return info
				}
			}
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
	blocks = collapseAdjacentControls(blocks)
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

// collapseAdjacentControls removes responsive copies of the same short block
// anchor. Eligibility requires source provenance recorded when a block paragraph
// is nested inside an anchor. Ordinary link-only paragraphs are intentionally not
// candidates: adjacency and equal links alone do not establish duplication.
// Repeated prose, code, quotations, tables, and image fallbacks also retain their
// multiplicity. Comparing destinations preserves equally labelled controls that
// perform different actions.
func collapseAdjacentControls(blocks []*Node) []*Node {
	kept := make([]*Node, 0, len(blocks))
	previous := ""
	for _, block := range blocks {
		if strings.TrimSpace(renderBlock(block, 0)) == "" {
			// Empty conversion artifacts do not separate visible blocks.
			kept = append(kept, block)
			continue
		}
		key, candidate := shortLinkedControlKey(block)
		if candidate && key == previous {
			continue
		}
		kept = append(kept, block)
		if candidate {
			previous = key
		} else {
			previous = ""
		}
	}
	return kept
}

func shortLinkedControlKey(n *Node) (string, bool) {
	if n == nil || n.Kind != Paragraph || n.controlURL == "" || !plainInlineParagraph(n) {
		return "", false
	}
	text := clean(plain(n))
	if text == "" || utf8.RuneCountInString(text) > 80 {
		return "", false
	}
	return text + "\x00" + n.controlURL, true
}

func plainInlineParagraph(n *Node) bool {
	valid := true
	var inspect func(*Node)
	inspect = func(current *Node) {
		if current == nil || !valid {
			return
		}
		switch current.Kind {
		case Paragraph, Text, Emphasis, Strong, Superscript:
		default:
			valid = false
			return
		}
		for _, child := range current.Children {
			inspect(child)
		}
	}
	inspect(n)
	return valid
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

var markdownDestinationReplacer = strings.NewReplacer(
	"\\", "%5C", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E",
)

func markdownDestination(value string) string {
	return markdownDestinationReplacer.Replace(value)
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
