package engine

import (
	"fmt"
	"log/slog"
	"math"
	"strings"
)

// SelectionMode controls the precision and recall tradeoff during content
// selection.
type SelectionMode uint8

const (
	// SelectionBalanced uses Pagemark's default selection behavior.
	SelectionBalanced SelectionMode = iota
	// SelectionPrecision usually selects less content.
	SelectionPrecision
	// SelectionRecall usually selects more content.
	SelectionRecall
)

// Limits controls extraction resource limits. A zero field uses the package
// default, a positive field sets that limit, and -1 makes that resource
// unlimited.
type Limits struct {
	// InputBytes does not apply to ExtractNode because its DOM is already parsed.
	InputBytes    int64
	Elements      int
	Depth         int
	OutputBytes   int
	Links         int
	Images        int
	TableCells    int
	RepeatedItems int
}

// URLPolicy controls link and image URLs from the Markdown converter.
// It applies to Markdown links and images, Document.Links, and Document.Images.
// It does not apply to Document.URL or Document.CanonicalURL.
type URLPolicy struct {
	// AllowedSchemes contains the permitted link and image URL schemes.
	// Scheme checks ignore case.
	AllowedSchemes []string
	// MaxLength is the maximum source link or image URL length in bytes.
	// Zero uses the default, a positive value sets the limit, and -1 disables it.
	MaxLength int
	// StripTracking removes utm_*, fbclid, and gclid parameters from links and images.
	StripTracking bool
}

const (
	defaultMaxInputBytes    int64 = 10 << 20
	defaultMaxElements            = 200000
	defaultMaxDepth               = 256
	defaultMaxOutputBytes         = 2 << 20
	defaultMaxLinks               = 1000
	defaultMaxImages              = 100
	defaultMaxTableCells          = 10000
	defaultMaxRepeatedItems       = 200
	defaultURLMaxLength           = 4096
)

type options struct {
	pageType                                                         PageType
	selectionMode                                                    SelectionMode
	maxInput                                                         int64
	maxElements, maxDepth, maxAttributes, maxAttributeBytes, maxText int
	maxOutput, maxLinks, maxImages, maxTableCells, maxRepeated       int
	includeLinks, includeImages, includeTables                       bool
	urlPolicy                                                        URLPolicy
	customURLPolicy, diagnostics                                     bool
	logger                                                           *slog.Logger
}

var defaultAllowedSchemes = []string{"http", "https"}

// DefaultURLPolicy returns the policy used when WithURLPolicy is not supplied.
// Each call returns a policy with its own AllowedSchemes slice.
func DefaultURLPolicy() URLPolicy {
	return URLPolicy{
		AllowedSchemes: append([]string(nil), defaultAllowedSchemes...),
		MaxLength:      defaultURLMaxLength,
	}
}

func defaultOptions() options {
	return options{
		maxInput: defaultMaxInputBytes, maxElements: defaultMaxElements, maxDepth: defaultMaxDepth,
		maxAttributes: 1000000, maxAttributeBytes: 8 << 20, maxText: 20 << 20,
		maxOutput: defaultMaxOutputBytes, maxLinks: defaultMaxLinks, maxImages: defaultMaxImages,
		maxTableCells: defaultMaxTableCells, maxRepeated: defaultMaxRepeatedItems,
		includeLinks: true, includeImages: true, includeTables: true,
		urlPolicy: URLPolicy{AllowedSchemes: defaultAllowedSchemes, MaxLength: defaultURLMaxLength},
	}
}

// Option changes extraction. You can use an Option in concurrent calls.
type Option func(*options)

// WithPageType overrides page-type detection. The selected type changes content
// scores. It does not change limits or URL rules.
func WithPageType(v PageType) Option { return func(o *options) { o.pageType = v } }

// WithSelectionMode sets the content-selection tradeoff.
func WithSelectionMode(v SelectionMode) Option {
	return func(o *options) { o.selectionMode = v }
}

// WithLimits replaces all public resource limits. Options apply in order.
// Each zero field uses the package default, a positive field sets the limit,
// and -1 makes that resource unlimited.
func WithLimits(v Limits) Option {
	return func(o *options) {
		o.maxInput = limit64(v.InputBytes, defaultMaxInputBytes)
		o.maxElements = limit(v.Elements, defaultMaxElements)
		o.maxDepth = limit(v.Depth, defaultMaxDepth)
		o.maxOutput = limit(v.OutputBytes, defaultMaxOutputBytes)
		o.maxLinks = limit(v.Links, defaultMaxLinks)
		o.maxImages = limit(v.Images, defaultMaxImages)
		o.maxTableCells = limit(v.TableCells, defaultMaxTableCells)
		o.maxRepeated = limit(v.RepeatedItems, defaultMaxRepeatedItems)
	}
}

func limit(v, defaultValue int) int {
	if v == 0 {
		return defaultValue
	}
	return v
}

func limit64(v, defaultValue int64) int64 {
	if v == 0 {
		return defaultValue
	}
	return v
}

// WithMaxInputBytes sets the maximum HTML input size. Zero uses the 10 MiB
// default and -1 disables the limit. This option does not apply to ExtractNode.
func WithMaxInputBytes(v int64) Option {
	return func(o *options) { o.maxInput = limit64(v, defaultMaxInputBytes) }
}

// WithMaxOutputBytes sets the maximum Markdown size. Zero uses the 2 MiB
// default and -1 disables the limit. Truncation occurs at a block boundary.
func WithMaxOutputBytes(v int) Option {
	return func(o *options) { o.maxOutput = limit(v, defaultMaxOutputBytes) }
}

// WithIncludeLinks controls links in Markdown and Document.Links.
// If v is false, visible link text remains without a destination.
func WithIncludeLinks(v bool) Option { return func(o *options) { o.includeLinks = v } }

// WithIncludeImages controls useful images in Markdown and Document.Images.
// Images are enabled by default. Set v to false for text-only output.
func WithIncludeImages(v bool) Option { return func(o *options) { o.includeImages = v } }

// WithIncludeTables controls Markdown table syntax. Tables are enabled by default.
// If v is false, Pagemark keeps table content without table syntax.
func WithIncludeTables(v bool) Option { return func(o *options) { o.includeTables = v } }

// WithURLPolicy replaces the default Markdown link and image URL policy.
// It defensively copies AllowedSchemes and does not change source or canonical URLs.
func WithURLPolicy(v URLPolicy) Option {
	schemes := append([]string(nil), v.AllowedSchemes...)
	maxLength := v.MaxLength
	stripTracking := v.StripTracking
	return func(o *options) {
		o.urlPolicy = URLPolicy{
			AllowedSchemes: append([]string(nil), schemes...),
			MaxLength:      maxLength,
			StripTracking:  stripTracking,
		}
		o.customURLPolicy = true
	}
}

// WithLogger sets a logger for extraction debug messages. A nil logger disables messages.
func WithLogger(v *slog.Logger) Option { return func(o *options) { o.logger = v } }

// WithDiagnostics enables legacy diagnostics on Document.
//
// Deprecated: use ExtractDetailedBytes. This option and Document.Diagnostics
// are retained temporarily for diagnostic tooling during the pre-1.0 migration.
func WithDiagnostics(v bool) Option { return func(o *options) { o.diagnostics = v } }

func (o *options) validate() error {
	if !validPageType(o.pageType) {
		return invalidOption("unknown page type %q", o.pageType)
	}
	if o.selectionMode > SelectionRecall {
		return invalidOption("unknown selection mode %d", o.selectionMode)
	}
	for _, item := range []struct {
		name  string
		value int64
	}{
		{"input bytes", o.maxInput},
		{"elements", int64(o.maxElements)},
		{"depth", int64(o.maxDepth)},
		{"output bytes", int64(o.maxOutput)},
		{"links", int64(o.maxLinks)},
		{"images", int64(o.maxImages)},
		{"table cells", int64(o.maxTableCells)},
		{"repeated items", int64(o.maxRepeated)},
	} {
		if item.value < -1 {
			return invalidOption("%s limit is %d; values below -1 are invalid", item.name, item.value)
		}
	}
	if o.urlPolicy.MaxLength == 0 {
		o.urlPolicy.MaxLength = defaultURLMaxLength
	}
	if o.urlPolicy.MaxLength < -1 {
		return invalidOption("URL maximum length is %d; values below -1 are invalid", o.urlPolicy.MaxLength)
	}
	if o.customURLPolicy {
		seen := make(map[string]struct{}, len(o.urlPolicy.AllowedSchemes))
		normalized := make([]string, 0, len(o.urlPolicy.AllowedSchemes))
		for _, raw := range o.urlPolicy.AllowedSchemes {
			scheme := strings.ToLower(raw)
			if !validScheme(scheme) {
				return invalidOption("invalid URL scheme %q", raw)
			}
			if _, exists := seen[scheme]; exists {
				continue
			}
			seen[scheme] = struct{}{}
			normalized = append(normalized, scheme)
		}
		o.urlPolicy.AllowedSchemes = normalized
	}
	return nil
}

func validPageType(v PageType) bool {
	switch v {
	case "", PageTypeArticle, PageTypeDocumentation, PageTypeDiscussion,
		PageTypeProduct, PageTypeListing, PageTypeCollection, PageTypeService, PageTypeGeneric:
		return true
	default:
		return false
	}
}

func validScheme(v string) bool {
	for i := range len(v) {
		c := v[i]
		letter := c >= 'a' && c <= 'z'
		digit := c >= '0' && c <= '9'
		if (i == 0 && !letter) ||
			(i > 0 && !letter && !digit && c != '+' && c != '-' && c != '.') {
			return false
		}
	}
	return v != ""
}

func (o *options) normalizeUnlimited() {
	if o.maxInput == -1 {
		o.maxInput = 0
	}
	normalizeUnlimitedLimit(&o.maxElements, 0)
	normalizeUnlimitedLimit(&o.maxDepth, 0)
	normalizeUnlimitedLimit(&o.maxOutput, 0)
	normalizeUnlimitedLimit(&o.maxRepeated, 0)
	normalizeUnlimitedLimit(&o.maxLinks, math.MaxInt)
	normalizeUnlimitedLimit(&o.maxImages, math.MaxInt)
	normalizeUnlimitedLimit(&o.maxTableCells, math.MaxInt)
	if o.urlPolicy.MaxLength == -1 {
		o.urlPolicy.MaxLength = 0
	}
}

func normalizeUnlimitedLimit(v *int, unlimited int) {
	if *v == -1 {
		*v = unlimited
	}
}

func invalidOption(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOption, fmt.Sprintf(format, args...))
}
