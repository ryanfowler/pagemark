package pagemark

import "log/slog"

// URLPolicy controls URLs in output.
type URLPolicy struct {
	Schemes       []string
	AllowMailto   bool
	MaxLength     int
	StripTracking bool
}

// Profile selects a page profile. Use WithPageType for normal overrides.
type Profile struct {
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

func defaultOptions() options {
	return options{maxInput: 10 << 20, maxElements: 200000, maxDepth: 256,
		maxAttributes: 1000000, maxAttributeBytes: 8 << 20, maxText: 20 << 20,
		maxOutput: 2 << 20, maxLinks: 1000, maxImages: 100, maxTableCells: 10000,
		maxRepeated: 200, includeLinks: true, includeImages: true, includeTables: true, includeMetadata: true,
		urlPolicy: URLPolicy{Schemes: []string{"http", "https"}, MaxLength: 4096}}
}

// Option changes extraction. Options are safe for concurrent reuse.
type Option func(*options)

func WithPageType(v PageType) Option    { return func(o *options) { o.pageType = v } }
func WithMaxInputBytes(v int64) Option  { return func(o *options) { o.maxInput = v } }
func WithMaxElements(v int) Option      { return func(o *options) { o.maxElements = v } }
func WithMaxDepth(v int) Option         { return func(o *options) { o.maxDepth = v } }
func WithMaxOutputBytes(v int) Option   { return func(o *options) { o.maxOutput = v } }
func WithMaxLinks(v int) Option         { return func(o *options) { o.maxLinks = v } }
func WithMaxImages(v int) Option        { return func(o *options) { o.maxImages = v } }
func WithMaxTableCells(v int) Option    { return func(o *options) { o.maxTableCells = v } }
func WithMaxRepeatedItems(v int) Option { return func(o *options) { o.maxRepeated = v } }
func WithIncludeLinks(v bool) Option    { return func(o *options) { o.includeLinks = v } }

// WithIncludeImages controls useful images in Markdown and Document.Images.
// Images are included by default; pass false for text-only output.
func WithIncludeImages(v bool) Option   { return func(o *options) { o.includeImages = v } }
func WithIncludeTables(v bool) Option   { return func(o *options) { o.includeTables = v } }
func WithIncludeMetadata(v bool) Option { return func(o *options) { o.includeMetadata = v } }
func WithURLPolicy(v URLPolicy) Option  { return func(o *options) { o.urlPolicy = v } }
func WithProfile(v Profile) Option      { return func(o *options) { o.pageType = v.PageType } }
func WithFavorPrecision(v bool) Option  { return func(o *options) { o.favorPrecision = v } }
func WithFavorRecall(v bool) Option     { return func(o *options) { o.favorRecall = v } }
func WithDiagnostics(v bool) Option     { return func(o *options) { o.diagnostics = v } }
func WithLogger(v *slog.Logger) Option  { return func(o *options) { o.logger = v } }
