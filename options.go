package pagemark

import "log/slog"

// URLPolicy controls link and image URLs from the Markdown converter.
// It applies to Markdown links and images, Document.Links, and Document.Images.
// It does not apply to Document.URL or Document.CanonicalURL.
type URLPolicy struct {
	// Schemes contains the permitted link and image URL schemes.
	// Scheme checks ignore case.
	Schemes []string
	// AllowMailto permits mailto links in addition to Schemes.
	AllowMailto bool
	// MaxLength is the maximum source link or image URL length in bytes.
	// A nonpositive value does not set a length limit.
	MaxLength int
	// StripTracking removes utm_*, fbclid, and gclid parameters from links and images.
	StripTracking bool
}

// Profile specifies a page profile.
// Use WithPageType when you only need to override the detected page type.
type Profile struct {
	// PageType is the page shape that the profile uses.
	PageType PageType
}

type options struct {
	pageType                                                         PageType
	maxInput                                                         int64
	maxElements, maxDepth, maxAttributes, maxAttributeBytes, maxText int
	maxOutput, maxLinks, maxImages, maxTableCells, maxRepeated       int
	includeLinks, includeImages, includeTables, includeMetadata      bool
	urlPolicy                                                        URLPolicy
	favorPrecision, favorRecall, diagnostics                         bool
	logger                                                           *slog.Logger
}

var defaultURLSchemes = []string{"http", "https"}

func defaultOptions() options {
	return options{maxInput: 10 << 20, maxElements: 200000, maxDepth: 256,
		maxAttributes: 1000000, maxAttributeBytes: 8 << 20, maxText: 20 << 20,
		maxOutput: 2 << 20, maxLinks: 1000, maxImages: 100, maxTableCells: 10000,
		maxRepeated: 200, includeLinks: true, includeImages: true, includeTables: true, includeMetadata: true,
		urlPolicy: URLPolicy{Schemes: defaultURLSchemes, MaxLength: 4096}}
}

// Option changes extraction. You can use an Option in concurrent calls.
type Option func(*options)

// WithPageType overrides page-type detection. The selected type changes content
// scores. It does not change limits or URL rules.
func WithPageType(v PageType) Option { return func(o *options) { o.pageType = v } }

// WithMaxInputBytes sets the maximum HTML input size. The default is 10 MiB.
// A nonpositive value disables this limit. This option does not apply to ExtractNode.
func WithMaxInputBytes(v int64) Option { return func(o *options) { o.maxInput = v } }

// WithMaxElements sets the maximum number of HTML elements. The default is
// 200,000. A nonpositive value disables this limit.
func WithMaxElements(v int) Option { return func(o *options) { o.maxElements = v } }

// WithMaxDepth sets the maximum DOM depth. The default is 256.
// A nonpositive value disables this limit.
func WithMaxDepth(v int) Option { return func(o *options) { o.maxDepth = v } }

// WithMaxOutputBytes sets the maximum Markdown size. The default is 2 MiB.
// A nonpositive value disables this limit. Truncation occurs at a block boundary.
func WithMaxOutputBytes(v int) Option { return func(o *options) { o.maxOutput = v } }

// WithMaxLinks sets the maximum number of links in Markdown. The default is
// 1,000. Link text remains when the output reaches the limit. A nonpositive
// value removes all link destinations.
func WithMaxLinks(v int) Option { return func(o *options) { o.maxLinks = v } }

// WithMaxImages sets the maximum number of images in Markdown. The default is
// 100. A nonpositive value removes all images.
func WithMaxImages(v int) Option { return func(o *options) { o.maxImages = v } }

// WithMaxTableCells sets the total table-cell limit. The default is 10,000.
// Pagemark converts a table that exceeds the limit to fallback content.
// A nonpositive value converts all tables to fallback content.
func WithMaxTableCells(v int) Option { return func(o *options) { o.maxTableCells = v } }

// WithMaxRepeatedItems sets the item limit for listings and collections.
// The default is 200. A nonpositive value disables this limit.
func WithMaxRepeatedItems(v int) Option { return func(o *options) { o.maxRepeated = v } }

// WithIncludeLinks controls links in Markdown and Document.Links.
// If v is false, visible link text remains without a destination.
func WithIncludeLinks(v bool) Option { return func(o *options) { o.includeLinks = v } }

// WithIncludeImages controls useful images in Markdown and Document.Images.
// Images are enabled by default. Set v to false for text-only output.
func WithIncludeImages(v bool) Option { return func(o *options) { o.includeImages = v } }

// WithIncludeTables controls Markdown table syntax. Tables are enabled by default.
// If v is false, Pagemark keeps table content without table syntax.
func WithIncludeTables(v bool) Option { return func(o *options) { o.includeTables = v } }

// WithIncludeMetadata controls metadata fields such as Document.Title.
// The title does not occur in the content when metadata is disabled.
func WithIncludeMetadata(v bool) Option { return func(o *options) { o.includeMetadata = v } }

// WithURLPolicy replaces the default Markdown link and image URL policy.
// It does not change Document.URL or Document.CanonicalURL.
func WithURLPolicy(v URLPolicy) Option { return func(o *options) { o.urlPolicy = v } }

// WithProfile overrides page-type detection with v.PageType.
func WithProfile(v Profile) Option { return func(o *options) { o.pageType = v.PageType } }

// WithFavorPrecision decreases content scores when v is true.
// The result usually contains less content.
func WithFavorPrecision(v bool) Option { return func(o *options) { o.favorPrecision = v } }

// WithFavorRecall increases content scores when v is true.
// The result usually contains more content.
func WithFavorRecall(v bool) Option { return func(o *options) { o.favorRecall = v } }

// WithDiagnostics controls selection diagnostics. Diagnostics are disabled by default.
// Diagnostics can increase memory use.
func WithDiagnostics(v bool) Option { return func(o *options) { o.diagnostics = v } }

// WithLogger sets a logger for extraction debug messages. A nil logger disables messages.
func WithLogger(v *slog.Logger) Option { return func(o *options) { o.logger = v } }
