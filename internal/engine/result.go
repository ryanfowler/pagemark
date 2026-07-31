package engine

// PageType identifies the main shape of a page.
type PageType string

const (
	// PageTypeArticle identifies a page with one main prose work.
	PageTypeArticle PageType = "article"
	// PageTypeDocumentation identifies a guide or reference page.
	PageTypeDocumentation PageType = "documentation"
	// PageTypeDiscussion identifies a question, thread, or conversation.
	PageTypeDiscussion PageType = "discussion"
	// PageTypeProduct identifies one product detail page.
	PageTypeProduct PageType = "product"
	// PageTypeListing identifies a page with repeated linked records.
	PageTypeListing PageType = "listing"
	// PageTypeCollection identifies a page with repeated related items.
	PageTypeCollection PageType = "collection"
	// PageTypeService identifies a page that describes a service.
	PageTypeService PageType = "service"
	// PageTypeGeneric identifies a page with no more specific shape.
	PageTypeGeneric PageType = "generic"
)

// Document contains the selected content and metadata from one HTML document.
// Markdown has no raw HTML, but its words are untrusted source data.
// The title does not occur again in Markdown, Text, or Sections.
type Document struct {
	// URL is the page URL that the caller supplied, with credentials removed.
	// URLPolicy does not apply to this field.
	URL string `json:"url,omitempty"`
	// CanonicalURL is the HTTP or HTTPS canonical URL from page metadata.
	// Pagemark removes credentials. URLPolicy does not apply to this field.
	CanonicalURL string `json:"canonical_url,omitempty"`
	// Title is the document title.
	Title string `json:"title,omitempty"`
	// Description is the page description from metadata.
	Description string `json:"description,omitempty"`
	// Author is the author name from metadata.
	Author string `json:"author,omitempty"`
	// SiteName is the site or publication name from metadata.
	SiteName string `json:"site_name,omitempty"`
	// Language is the language value from the HTML lang attribute.
	Language string `json:"language,omitempty"`
	// PublishedTime is the unparsed publication value from page metadata. It is
	// not guaranteed to be RFC 3339 or any other timestamp format.
	PublishedTime string `json:"published_time,omitempty"`
	// PageType is the detected or specified page shape.
	PageType PageType `json:"page_type"`
	// Markdown is the selected content as restricted Markdown.
	Markdown string `json:"markdown"`
	// Text is the selected content as plain text.
	Text string `json:"text"`
	// Sections contains a plain-text view of the selected sections.
	Sections []Section `json:"sections,omitempty"`
	// Links contains the links that occur in Markdown.
	Links []Link `json:"links,omitempty"`
	// Images contains the useful images that occur in Markdown.
	Images []Image `json:"images,omitempty"`
	// Truncated reports whether the output byte limit omitted content.
	Truncated bool `json:"truncated,omitempty"`

	// diagnostic is populated only during internal diagnostic extraction.
	diagnostic *diagnosticState
}

// Section contains one selected section as plain text.
type Section struct {
	// Heading is the section heading. It is empty for content before a heading.
	Heading string `json:"heading,omitempty"`
	// Text is the plain text in the section.
	Text string `json:"text"`
}

// Link contains one safe link that occurs in Markdown.
type Link struct {
	// Text is the visible link text.
	Text string `json:"text"`
	// URL is the resolved link destination.
	URL string `json:"url"`
}

// Image contains one useful source image.
type Image struct {
	// Alt is the normalized alternative text.
	Alt string `json:"alt"`
	// URL is the resolved source URL.
	URL string `json:"url,omitempty"`
}

// diagnosticStats contains extraction counts for internal diagnostics.
type diagnosticStats struct {
	// InputBytes is the HTML byte count for Extract or ExtractBytes.
	// It is zero for ExtractNode.
	InputBytes int `json:"input_bytes"`
	// Elements is the number of indexed HTML elements.
	Elements int `json:"elements"`
	// TextBytes is the number of indexed source-text bytes.
	TextBytes int `json:"text_bytes"`
	// Blocks is the number of content blocks that Pagemark scored.
	Blocks int `json:"blocks"`
	// SelectedBlocks is the number of blocks in the output.
	SelectedBlocks int `json:"selected_blocks"`
	// OutputBytes is the Markdown byte count.
	OutputBytes int `json:"output_bytes"`
}

// diagnosticState is the internal diagnostic data collected for a detailed
// extraction. It is kept off the public Document result.
type diagnosticState struct {
	ProfileVersion string
	Fallback       string
	PageCandidates []PageCandidate
	Blocks         []BlockDiagnostic
	RejectedLinks  []string
	Stats          diagnosticStats
	Quality        float64
	PageTypeScore  float64
}

// PageCandidate contains one possible page type and its raw score.
type PageCandidate struct {
	// Type is the possible page type.
	Type PageType `json:"type"`
	// Score is the raw classification score. It is not a confidence value.
	Score float64 `json:"score"`
}

// BlockDiagnostic explains one content block.
type BlockDiagnostic struct {
	// ID identifies the block in source order.
	ID int `json:"id"`
	// Kind identifies the block structure, such as p or pre.
	Kind string `json:"kind"`
	// Text is the normalized block text.
	Text string `json:"text"`
	// Score is the final content-selection score.
	Score float64 `json:"score"`
	// Selected reports whether the output selected the block.
	Selected bool `json:"selected"`
	// Reasons contains human-readable score reasons.
	Reasons []string `json:"reasons,omitempty"`
}

// DiagnosticReport contains experimental, algorithm-specific extraction data.
// Its fields may change in a minor release.
type DiagnosticReport struct {
	ProfileVersion string            `json:"profile_version"`
	Fallback       string            `json:"fallback"`
	PageCandidates []PageCandidate   `json:"page_candidates,omitempty"`
	Blocks         []BlockDiagnostic `json:"blocks,omitempty"`
	RejectedLinks  []string          `json:"rejected_links,omitempty"`
	Stats          diagnosticStats   `json:"stats"`
	Quality        float64           `json:"quality"`
	PageTypeScore  float64           `json:"page_type_score"`
}
