package pagemark

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
	// URL is the page URL that the caller supplied. Pagemark preserves credentials.
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
	// PublishedTime is the publication value from page metadata.
	PublishedTime string `json:"published_time,omitempty"`
	// PageType is the detected or specified page shape.
	PageType PageType `json:"page_type"`
	// PageTypeScore is the page-type confidence from 0 through 1.
	// An explicit page type has a score of 1.
	PageTypeScore float64 `json:"page_type_score"`
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
	// Quality measures observable output properties from 0 through 1.
	// It does not measure trust or factual accuracy.
	Quality float64 `json:"quality"`
	// Diagnostics contains selection details when diagnostics are enabled.
	Diagnostics *Diagnostics `json:"diagnostics,omitempty"`
	// Warnings contains nonfatal extraction conditions.
	Warnings []Warning `json:"warnings,omitempty"`
	// Stats contains extraction counts.
	Stats Stats `json:"stats"`
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

// Warning reports a nonfatal extraction condition.
type Warning struct {
	// Code is a short machine-readable identifier.
	Code string `json:"code"`
	// Message describes the condition.
	Message string `json:"message"`
}

// Stats contains extraction counts.
type Stats struct {
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

// Diagnostics explains selection decisions. Enable it with WithDiagnostics.
// Fields can change in a minor release.
type Diagnostics struct {
	// ProfileVersion identifies the diagnostic scoring format.
	ProfileVersion string `json:"profile_version"`
	// Fallback identifies the extraction path that produced the result.
	Fallback string `json:"fallback"`
	// PageCandidates contains the page types in score order.
	PageCandidates []PageCandidate `json:"page_candidates,omitempty"`
	// Blocks contains score details for content blocks.
	Blocks []BlockDiagnostic `json:"blocks,omitempty"`
	// RejectedLinks contains source link URLs that failed URLPolicy.
	RejectedLinks []string `json:"rejected_links,omitempty"`
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
