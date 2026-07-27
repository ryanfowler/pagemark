// Package dom contains shared HTML tree rules.
package dom

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Hidden reports whether an element and its subtree must not appear in output.
func Hidden(n *html.Node) bool {
	return hiddenElement(n) || hiddenByAttributes(n)
}

// HiddenExceptARIA reports hidden content while allowing only aria-hidden to
// be ignored. Math renderers commonly mark their visual glyph branch
// aria-hidden because an accessible branch is present alongside it. Callers
// using this exception must still reject non-content elements, CSS-hidden
// content, hidden/inert subtrees, and modal UI.
func HiddenExceptARIA(n *html.Node) bool {
	return hiddenElement(n) || hiddenByNonARIAAttributes(n)
}

func hiddenElement(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	// The HTML parser canonicalizes tag names. Keep the overwhelmingly common
	// path allocation-free and only normalize caller-built mixed-case trees.
	tag := n.Data
	switch tag {
	case "script", "style", "template", "canvas", "svg", "iframe", "object", "embed":
		return true
	}
	for i := 0; i < len(tag); i++ {
		if tag[i] >= 'A' && tag[i] <= 'Z' {
			switch strings.ToLower(tag) {
			case "script", "style", "template", "canvas", "svg", "iframe", "object", "embed":
				return true
			}
			break
		}
		if tag[i] >= utf8.RuneSelf {
			// Preserve Unicode case-folding for manually constructed trees.
			return strings.EqualFold(tag, "script") || strings.EqualFold(tag, "style") ||
				strings.EqualFold(tag, "template") || strings.EqualFold(tag, "canvas") ||
				strings.EqualFold(tag, "svg") || strings.EqualFold(tag, "iframe") ||
				strings.EqualFold(tag, "object") || strings.EqualFold(tag, "embed")
		}
	}
	return false
}

// AccessibleSVGLabel returns the concise label for an SVG that may be handled
// as an opaque image. Hidden reports all SVG as hidden so generic DOM walkers
// never descend into SVG text, links, or metadata; callers that explicitly
// support this representation may use this function before their hidden check.
func AccessibleSVGLabel(n *html.Node) string {
	if n == nil || n.Type != html.ElementNode || !strings.EqualFold(n.Data, "svg") ||
		!strings.EqualFold(strings.TrimSpace(attr(n, "role")), "img") {
		return ""
	}
	label := strings.TrimSpace(attr(n, "aria-label"))
	if label == "" || hiddenByAttributes(n) {
		return ""
	}
	return label
}

// hiddenByAttributes is shared by ordinary visibility checks and opaque SVG
// handling so the latter cannot bypass part of the visibility policy.
func hiddenByAttributes(n *html.Node) bool {
	return hiddenByAttributesMode(n, true)
}

func hiddenByNonARIAAttributes(n *html.Node) bool {
	return hiddenByAttributesMode(n, false)
}

// hiddenByAttributesMode scans attributes once. Visibility checks are among
// the hottest DOM operations, and repeatedly looking up each relevant
// attribute made nodes with large attribute lists disproportionately costly.
func hiddenByAttributesMode(n *html.Node, includeARIAHidden bool) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	open := false
	style := ""
	for _, a := range n.Attr {
		key := a.Key
		// Parsed HTML has canonical lowercase names, so dispatch those directly.
		// Only manually constructed mixed-case trees need case folding. This keeps
		// common attributes such as class and href to one switch and a short ASCII
		// scan instead of comparing them with every visibility key.
		switch key {
		case "hidden", "inert", "open", "aria-hidden", "aria-modal", "style":
		default:
			mixedCase := false
			for i := 0; i < len(key); i++ {
				if key[i] >= 'A' && key[i] <= 'Z' {
					mixedCase = true
					break
				}
			}
			if !mixedCase {
				continue
			}
			switch {
			case strings.EqualFold(key, "hidden"):
				key = "hidden"
			case strings.EqualFold(key, "inert"):
				key = "inert"
			case strings.EqualFold(key, "open"):
				key = "open"
			case strings.EqualFold(key, "aria-hidden"):
				key = "aria-hidden"
			case strings.EqualFold(key, "aria-modal"):
				key = "aria-modal"
			case strings.EqualFold(key, "style"):
				key = "style"
			default:
				continue
			}
		}
		switch key {
		case "hidden", "inert":
			return true
		case "open":
			open = true
		case "aria-hidden":
			if includeARIAHidden && equalFoldTrimmedTrue(a.Val) {
				return true
			}
		case "aria-modal":
			if equalFoldTrimmedTrue(a.Val) {
				return true
			}
		case "style":
			style = a.Val
		}
	}
	// A dialog is not rendered until its boolean open attribute is present.
	if (n.Data == "dialog" || len(n.Data) == len("dialog") && strings.EqualFold(n.Data, "dialog")) && !open {
		return true
	}
	if style == "" {
		return false
	}
	return hiddenStyle(style)
}

func equalFoldTrimmedTrue(value string) bool {
	value = strings.TrimSpace(value)
	return value == "true" || strings.EqualFold(value, "true")
}

// inlineVisibilityValues resolves both visibility properties in one pass.
// Hidden checks always need both values, and parsing the complete declaration
// list separately for each property is disproportionately expensive on pages
// generated by frameworks with large inline style attributes.
func inlineVisibilityValues(style string) (display, visibility string) {
	var displayImportant, visibilityImportant bool
	var displayFound, visibilityFound bool

	for len(style) > 0 {
		declaration := style
		if semi := cssTopLevelDelimiter(style, ';'); semi >= 0 {
			declaration = style[:semi]
			style = style[semi+1:]
		} else {
			style = ""
		}

		colon := cssTopLevelDelimiter(declaration, ':')
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(cssWithoutComments(declaration[:colon]))
		name = cssDecodeIdentifierEscapes(name)
		var winning *string
		var winningImportant, found *bool
		switch {
		case strings.EqualFold(name, "display"):
			winning = &display
			winningImportant = &displayImportant
			found = &displayFound
		case strings.EqualFold(name, "visibility"):
			winning = &visibility
			winningImportant = &visibilityImportant
			found = &visibilityFound
		default:
			continue
		}

		value := strings.TrimSpace(cssWithoutComments(declaration[colon+1:]))
		if value == "" {
			continue
		}
		value, important := cssStripImportant(value)
		value = strings.TrimSpace(value)
		if value == "" || !validInlineStyleValue(name, value) {
			continue
		}
		if *found && *winningImportant && !important {
			continue
		}
		*winning = cssDecodeIdentifierEscapes(value)
		*winningImportant = important
		*found = true
	}
	return display, visibility
}

// cssStripImportant recognizes a trailing importance annotation while
// retaining the distinction between a literal '!' delimiter and an escaped
// exclamation mark. Escapes are permitted within the important identifier.
func cssStripImportant(value string) (string, bool) {
	bang := -1
	var quote byte
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' {
			i++
			continue
		}
		if quote != 0 {
			if value[i] == quote {
				quote = 0
			}
			continue
		}
		if value[i] == '\'' || value[i] == '"' {
			quote = value[i]
		} else if value[i] == '!' {
			bang = i
		}
	}
	if bang < 0 || !strings.EqualFold(
		cssDecodeIdentifierEscapes(strings.TrimSpace(value[bang+1:])), "important") {
		return value, false
	}
	return strings.TrimSpace(value[:bang]), true
}

// cssDecodeIdentifierEscapes decodes CSS escapes used by property names and
// keyword values. It preserves the allocation-free path for ordinary styles.
func cssDecodeIdentifierEscapes(value string) string {
	if strings.IndexByte(value, '\\') < 0 {
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			b.WriteByte(value[i])
			continue
		}
		if i+1 == len(value) {
			b.WriteRune(utf8.RuneError)
			break
		}

		start := i + 1
		if isCSSHex(value[start]) {
			end := start
			var decoded rune
			for end < len(value) && end-start < 6 && isCSSHex(value[end]) {
				decoded = decoded*16 + rune(cssHexValue(value[end]))
				end++
			}
			if decoded == 0 || decoded > utf8.MaxRune || decoded >= 0xD800 && decoded <= 0xDFFF {
				decoded = utf8.RuneError
			}
			b.WriteRune(decoded)
			if end < len(value) && isCSSWhitespace(value[end]) {
				if value[end] == '\r' && end+1 < len(value) && value[end+1] == '\n' {
					end++
				}
				i = end
			} else {
				i = end - 1
			}
			continue
		}

		r, size := utf8.DecodeRuneInString(value[start:])
		if r == '\n' || r == '\f' {
			i += size
			continue
		}
		if r == '\r' {
			i += size
			if i+1 < len(value) && value[i+1] == '\n' {
				i++
			}
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func isCSSHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func cssHexValue(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func isCSSWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func validInlineStyleValue(property, value string) bool {
	if cssSubstitutionValue(value) {
		return true
	}

	var words [3]string
	wordCount, validTokens := cssIdentifierWords(value, &words)
	if !validTokens || wordCount == 0 {
		return false
	}
	if strings.EqualFold(property, "visibility") {
		return wordCount == 1 &&
			(equalFoldAny(words[0], "visible", "hidden", "collapse") || cssWideKeyword(words[0]))
	}
	if !strings.EqualFold(property, "display") {
		return false
	}
	if wordCount == 1 {
		return cssWideKeyword(words[0]) || displaySingleKeyword(words[0])
	}

	// Multi-keyword display syntax combines at most one outside value, one
	// inside value, and optionally list-item. Order is not significant.
	var outside, inside, insideAllowsListItem, listItem bool
	for _, word := range words[:wordCount] {
		switch {
		case equalFoldAny(word, "block", "inline", "run-in"):
			if outside {
				return false
			}
			outside = true
		case equalFoldAny(word, "flow", "flow-root", "table", "flex", "grid", "ruby", "math"):
			if inside {
				return false
			}
			inside = true
			insideAllowsListItem = equalFoldAny(word, "flow", "flow-root")
			if listItem && !insideAllowsListItem {
				return false
			}
		case strings.EqualFold(word, "list-item"):
			if listItem || inside && !insideAllowsListItem {
				return false
			}
			listItem = true
		default:
			return false
		}
	}
	if listItem {
		return true // Incompatible inside values were rejected while scanning.
	}
	return outside && inside
}

// cssIdentifierWords tokenizes the identifier-only value grammars used by
// display and visibility. Escapes are consumed as part of their identifier
// token before being decoded, so escaped punctuation remains identifier data.
func cssIdentifierWords(value string, words *[3]string) (int, bool) {
	count := 0
	for i := 0; ; {
		for i < len(value) && isCSSWhitespace(value[i]) {
			i++
		}
		if i == len(value) {
			return count, true
		}
		if count == len(words) {
			return 0, false
		}

		start := i
		for i < len(value) {
			if cssNameByte(value[i]) {
				i++
				continue
			}
			if value[i] != '\\' {
				break
			}
			var ok bool
			i, ok = cssEscapeEnd(value, i)
			if !ok {
				return 0, false
			}
		}
		if i == start || i < len(value) && !isCSSWhitespace(value[i]) {
			return 0, false
		}
		words[count] = cssDecodeIdentifierEscapes(value[start:i])
		count++
	}
}

// cssEscapeEnd returns the first byte after one CSS escape, including the
// optional whitespace terminator of a hexadecimal escape.
func cssEscapeEnd(value string, slash int) (int, bool) {
	if slash+1 >= len(value) {
		return slash, false
	}
	i := slash + 1
	if value[i] == '\n' || value[i] == '\r' || value[i] == '\f' {
		return slash, false
	}
	if isCSSHex(value[i]) {
		start := i
		for i < len(value) && i-start < 6 && isCSSHex(value[i]) {
			i++
		}
		if i < len(value) && isCSSWhitespace(value[i]) {
			if value[i] == '\r' && i+1 < len(value) && value[i+1] == '\n' {
				i++
			}
			i++
		}
		return i, true
	}
	_, size := utf8.DecodeRuneInString(value[i:])
	return i + size, true
}

func cssWideKeyword(value string) bool {
	return equalFoldAny(value, "inherit", "initial", "revert", "revert-layer", "unset")
}

// cssSubstitutionValue reports whether a value contains a complete var(),
// env(), or attr() component. Substitution functions make the declaration's
// grammar dependent on the computed replacement and may occur alongside
// ordinary display keywords.
func cssSubstitutionValue(value string) bool {
	for i := 0; i < len(value); {
		if value[i] == '\'' || value[i] == '"' {
			quote := value[i]
			for i++; i < len(value); i++ {
				if value[i] == '\\' {
					i++
				} else if value[i] == quote {
					i++
					break
				}
			}
			continue
		}
		if !cssNameByte(value[i]) && value[i] != '\\' {
			i++
			continue
		}

		start := i
		for i < len(value) && (cssNameByte(value[i]) || value[i] == '\\') {
			if value[i] == '\\' {
				var ok bool
				i, ok = cssEscapeEnd(value, i)
				if !ok {
					return false
				}
			} else {
				i++
			}
		}
		if i >= len(value) || value[i] != '(' {
			continue
		}
		name := cssDecodeIdentifierEscapes(value[start:i])
		if !equalFoldAny(name, "var", "env", "attr") {
			continue
		}
		return cssFunctionEnd(value, i) >= 0
	}
	return false
}

func cssFunctionEnd(value string, open int) int {
	depth := 1
	var quote byte
	for i := open + 1; i < len(value); i++ {
		if value[i] == '\\' {
			i++
			continue
		}
		if quote != 0 {
			if value[i] == quote {
				quote = 0
			}
			continue
		}
		switch value[i] {
		case '\'', '"':
			quote = value[i]
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func cssNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '-' || c == '_' || c >= 0x80
}

func displaySingleKeyword(value string) bool {
	return equalFoldAny(value,
		"none", "contents", "block", "inline", "run-in", "flow", "flow-root",
		"table", "flex", "grid", "ruby", "math", "list-item", "inline-block",
		"inline-table", "inline-flex", "inline-grid", "table-row-group",
		"table-header-group", "table-footer-group", "table-row", "table-cell",
		"table-column-group", "table-column", "table-caption", "ruby-base",
		"ruby-text", "ruby-base-container", "ruby-text-container", "-webkit-box",
		"-webkit-inline-box", "-ms-flexbox", "-ms-inline-flexbox")
}

func equalFoldAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}

// cssTopLevelDelimiter finds a declaration delimiter while ignoring comments,
// strings, escapes, and nested blocks. This is deliberately narrower than a
// full CSS parser, but prevents content inside values from becoming phantom
// declarations.
func cssTopLevelDelimiter(s string, delimiter byte) int {
	var quote byte
	depth := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\':
			i++ // An escape consumes the following byte, including a delimiter.
		case quote != 0:
			if s[i] == quote {
				quote = 0
			}
		case s[i] == '\'' || s[i] == '"':
			quote = s[i]
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			if end := strings.Index(s[i+2:], "*/"); end >= 0 {
				i += end + 3
			} else {
				return -1
			}
		case s[i] == '(' || s[i] == '[' || s[i] == '{':
			depth++
		case s[i] == ')' || s[i] == ']' || s[i] == '}':
			if depth > 0 {
				depth--
			}
		case s[i] == delimiter && depth == 0:
			return i
		}
	}
	return -1
}

// cssWithoutComments treats comments as whitespace, as CSS tokenization does.
// The usual comment-free style value is returned without allocation.
func cssWithoutComments(s string) string {
	var b strings.Builder
	changed := false
	var quote byte
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			if changed {
				b.WriteByte(s[i])
				if i+1 < len(s) {
					i++
					b.WriteByte(s[i])
				}
			} else if i+1 < len(s) {
				i++
			}
			continue
		}
		if quote != 0 {
			if changed {
				b.WriteByte(s[i])
			}
			if s[i] == quote {
				quote = 0
			}
			continue
		}
		if s[i] == '\'' || s[i] == '"' {
			quote = s[i]
			if changed {
				b.WriteByte(s[i])
			}
			continue
		}
		if s[i] == '/' && i+1 < len(s) && s[i+1] == '*' {
			if !changed {
				b.Grow(len(s))
				b.WriteString(s[:i])
				changed = true
			}
			b.WriteByte(' ')
			if end := strings.Index(s[i+2:], "*/"); end >= 0 {
				i += end + 3
			} else {
				break
			}
			continue
		}
		if changed {
			b.WriteByte(s[i])
		}
	}
	if changed {
		return b.String()
	}
	return s
}

func hiddenStyle(style string) bool {
	// Most inline styles only describe layout or decoration. Avoid invoking the
	// CSS declaration scanner unless one of the two properties can be present.
	// Escapes and non-ASCII text retain the full parser path because they may
	// participate in the case folding accepted below.
	if !possiblyContainsVisibilityProperty(style) {
		return false
	}
	display, visibility := inlineVisibilityValues(style)
	return strings.EqualFold(strings.TrimSpace(display), "none") ||
		strings.EqualFold(strings.TrimSpace(visibility), "hidden")
}

func possiblyContainsVisibilityProperty(style string) bool {
	if strings.Contains(style, "display") || strings.Contains(style, "visibility") {
		return true
	}
	mixedCase := false
	for i := 0; i < len(style); i++ {
		if style[i] == '\\' || style[i] >= utf8.RuneSelf {
			return true
		}
		if style[i] >= 'A' && style[i] <= 'Z' {
			mixedCase = true
		}
	}
	return mixedCase && (containsASCIIFold(style, "display") || containsASCIIFold(style, "visibility"))
}

func containsASCIIFold(value, substr string) bool {
	for i := 0; i+len(substr) <= len(value); i++ {
		if asciiPrefixFold(value[i:], substr) {
			return true
		}
	}
	return false
}

func asciiPrefixFold(value, prefix string) bool {
	if len(value) < len(prefix) {
		return false
	}
	for i := range prefix {
		c := value[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != prefix[i] {
			return false
		}
	}
	return true
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}
