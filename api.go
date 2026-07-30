// Package pagemark extracts useful content from HTML.
//
// The extraction implementation lives in internal/engine. This package keeps
// the public API small and stable while preventing implementation details from
// becoming importable by other modules.
package pagemark

import (
	"io"
	"log/slog"

	"github.com/ryanfowler/pagemark/internal/engine"
	"golang.org/x/net/html"
)

// Public result and configuration types are aliases so values can cross the
// API boundary without conversion or duplicated definitions.
type (
	PageType         = engine.PageType
	Document         = engine.Document
	Section          = engine.Section
	Link             = engine.Link
	Image            = engine.Image
	WarningCode      = engine.WarningCode
	Warning          = engine.Warning
	Stats            = engine.Stats
	Diagnostics      = engine.Diagnostics
	PageCandidate    = engine.PageCandidate
	BlockDiagnostic  = engine.BlockDiagnostic
	DiagnosticReport = engine.DiagnosticReport
	SelectionMode    = engine.SelectionMode
	Limits           = engine.Limits
	URLPolicy        = engine.URLPolicy
	Option           = engine.Option
	LimitResource    = engine.LimitResource
	LimitError       = engine.LimitError
)

const (
	PageTypeArticle       = engine.PageTypeArticle
	PageTypeDocumentation = engine.PageTypeDocumentation
	PageTypeDiscussion    = engine.PageTypeDiscussion
	PageTypeProduct       = engine.PageTypeProduct
	PageTypeListing       = engine.PageTypeListing
	PageTypeCollection    = engine.PageTypeCollection
	PageTypeService       = engine.PageTypeService
	PageTypeGeneric       = engine.PageTypeGeneric

	SelectionBalanced  = engine.SelectionBalanced
	SelectionPrecision = engine.SelectionPrecision
	SelectionRecall    = engine.SelectionRecall

	WarningOutputTruncated        = engine.WarningOutputTruncated
	WarningRepeatedItemsTruncated = engine.WarningRepeatedItemsTruncated
	WarningFallbackUsed           = engine.WarningFallbackUsed
	WarningRelaxedExtraction      = engine.WarningRelaxedExtraction

	LimitInputBytes     = engine.LimitInputBytes
	LimitElements       = engine.LimitElements
	LimitDepth          = engine.LimitDepth
	LimitAttributes     = engine.LimitAttributes
	LimitAttributeBytes = engine.LimitAttributeBytes
	LimitTextBytes      = engine.LimitTextBytes
)

var (
	ErrNoContent     = engine.ErrNoContent
	ErrInvalidURL    = engine.ErrInvalidURL
	ErrLimit         = engine.ErrLimit
	ErrInvalidOption = engine.ErrInvalidOption
)

// Extract reads UTF-8 HTML and returns its useful content.
func Extract(input io.Reader, pageURL string, opts ...Option) (*Document, error) {
	return engine.Extract(input, pageURL, opts...)
}

// ExtractBytes reads UTF-8 HTML from input and returns its useful content.
func ExtractBytes(input []byte, pageURL string, opts ...Option) (*Document, error) {
	return engine.ExtractBytes(input, pageURL, opts...)
}

// ExtractNode extracts useful content from a parsed HTML tree without changing it.
func ExtractNode(root *html.Node, pageURL string, opts ...Option) (*Document, error) {
	return engine.ExtractNode(root, pageURL, opts...)
}

// ExtractDetailedBytes extracts content and returns an experimental diagnostic report.
func ExtractDetailedBytes(input []byte, pageURL string, opts ...Option) (*Document, *DiagnosticReport, error) {
	return engine.ExtractDetailedBytes(input, pageURL, opts...)
}

// DefaultURLPolicy returns the default link and image URL policy.
func DefaultURLPolicy() URLPolicy { return engine.DefaultURLPolicy() }

func WithPageType(v PageType) Option           { return engine.WithPageType(v) }
func WithSelectionMode(v SelectionMode) Option { return engine.WithSelectionMode(v) }
func WithLimits(v Limits) Option               { return engine.WithLimits(v) }
func WithMaxInputBytes(v int64) Option         { return engine.WithMaxInputBytes(v) }
func WithMaxOutputBytes(v int) Option          { return engine.WithMaxOutputBytes(v) }
func WithIncludeLinks(v bool) Option           { return engine.WithIncludeLinks(v) }
func WithIncludeImages(v bool) Option          { return engine.WithIncludeImages(v) }
func WithIncludeTables(v bool) Option          { return engine.WithIncludeTables(v) }
func WithURLPolicy(v URLPolicy) Option         { return engine.WithURLPolicy(v) }
func WithLogger(v *slog.Logger) Option         { return engine.WithLogger(v) }
func WithDiagnostics(v bool) Option            { return engine.WithDiagnostics(v) }
