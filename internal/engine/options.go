package engine

import (
	"fmt"
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
	defaultMaxInputBytes  int64 = 10 << 20
	defaultMaxElements          = 200000
	defaultMaxDepth             = 256
	defaultMaxOutputBytes       = 2 << 20
	defaultURLMaxLength         = 4096
)

type options struct {
	pageType                                   PageType
	selectionMode                              SelectionMode
	maxInput                                   int64
	maxElements, maxDepth                      int
	maxOutput                                  int
	includeLinks, includeImages, includeTables bool
	urlPolicy                                  URLPolicy
	customURLPolicy, diagnostics               bool
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
		maxOutput:    defaultMaxOutputBytes,
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
// If no selected substantive content block fits, extraction returns an empty
// Document with Truncated set instead of ErrNoContent.
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

// withDiagnostics enables diagnostic collection for the detailed extraction
// path. It is intentionally not part of the public option set.
func withDiagnostics() Option { return func(o *options) { o.diagnostics = true } }

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
	normalizeUnlimitedLimit(&o.maxOutput, 0)
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
