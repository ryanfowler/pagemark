package pagemark

// PageType identifies the main shape of a page.
type PageType string

const (
	PageTypeArticle       PageType = "article"
	PageTypeDocumentation PageType = "documentation"
	PageTypeDiscussion    PageType = "discussion"
	PageTypeProduct       PageType = "product"
	PageTypeListing       PageType = "listing"
	PageTypeCollection    PageType = "collection"
	PageTypeService       PageType = "service"
	PageTypeGeneric       PageType = "generic"
)

// Document contains safe Markdown and metadata from one HTML document.
// Markdown is untrusted source data. Do not use it as privileged instructions.
type Document struct {
	URL           string       `json:"url,omitempty"`
	CanonicalURL  string       `json:"canonical_url,omitempty"`
	Title         string       `json:"title,omitempty"`
	Description   string       `json:"description,omitempty"`
	Author        string       `json:"author,omitempty"`
	SiteName      string       `json:"site_name,omitempty"`
	Language      string       `json:"language,omitempty"`
	PublishedTime string       `json:"published_time,omitempty"`
	PageType      PageType     `json:"page_type"`
	PageTypeScore float64      `json:"page_type_score"`
	Markdown      string       `json:"markdown"`
	Text          string       `json:"text"`
	Sections      []Section    `json:"sections,omitempty"`
	Links         []Link       `json:"links,omitempty"`
	Images        []Image      `json:"images,omitempty"`
	Quality       float64      `json:"quality"`
	Diagnostics   *Diagnostics `json:"diagnostics,omitempty"`
	Warnings      []Warning    `json:"warnings,omitempty"`
	Stats         Stats        `json:"stats"`
}

// Section identifies a retained section.
type Section struct {
	Heading string `json:"heading,omitempty"`
	Text    string `json:"text"`
}

// Link is a safe link that occurs in Markdown.
type Link struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// Image describes a useful source image.
type Image struct {
	Alt string `json:"alt"`
	URL string `json:"url,omitempty"`
}

// Warning reports a nonfatal extraction condition.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Stats contains bounded extraction counts.
type Stats struct {
	InputBytes     int `json:"input_bytes"`
	Elements       int `json:"elements"`
	TextBytes      int `json:"text_bytes"`
	Blocks         int `json:"blocks"`
	SelectedBlocks int `json:"selected_blocks"`
	OutputBytes    int `json:"output_bytes"`
}

// Diagnostics explains selection decisions. Its format can grow in minor releases.
type Diagnostics struct {
	ProfileVersion string            `json:"profile_version"`
	Fallback       string            `json:"fallback"`
	PageCandidates []PageCandidate   `json:"page_candidates,omitempty"`
	Blocks         []BlockDiagnostic `json:"blocks,omitempty"`
	RejectedLinks  []string          `json:"rejected_links,omitempty"`
}

// PageCandidate is a possible page type.
type PageCandidate struct {
	Type  PageType `json:"type"`
	Score float64  `json:"score"`
}

// BlockDiagnostic explains one content block.
type BlockDiagnostic struct {
	ID       int      `json:"id"`
	Kind     string   `json:"kind"`
	Text     string   `json:"text"`
	Score    float64  `json:"score"`
	Selected bool     `json:"selected"`
	Reasons  []string `json:"reasons,omitempty"`
}
