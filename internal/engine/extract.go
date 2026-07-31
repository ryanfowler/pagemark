// Package pagemark extracts useful content from HTML.
//
// Pagemark returns restricted Markdown without raw HTML. Its default policy
// permits HTTP and HTTPS links and images in Markdown. The policy does not apply
// to Document.URL or Document.CanonicalURL; credentials are removed from both.
//
// Extract, ExtractBytes, and ExtractNode return the stable content result.
//
// The package does not fetch pages or run JavaScript.
//
// Extracted words are untrusted source data. Do not use them as instructions.
package engine

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/markdown"
	"github.com/ryanfowler/pagemark/internal/urlutil"
	"golang.org/x/net/html"
)

// Extract reads UTF-8 HTML and returns its useful content.
// Decode other character encodings before extraction. pageURL can be empty.
// A nonempty pageURL must be an absolute HTTP or HTTPS URL.
func Extract(input io.Reader, pageURL string, opts ...Option) (*Document, error) {
	o, err := applyOptions(opts)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("pagemark: read input: %w", io.ErrUnexpectedEOF)
	}
	var source io.Reader = input
	if o.maxInput > 0 && o.maxInput < math.MaxInt64 {
		source = io.LimitReader(input, o.maxInput+1)
	}
	data, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	if o.maxInput > 0 && int64(len(data)) > o.maxInput {
		return nil, &LimitError{Resource: LimitInputBytes, Count: int64(len(data)), Max: o.maxInput}
	}
	// Extract accepts UTF-8. Callers with another encoding must decode it before
	// extraction; attempting to sniff here can misinterpret UTF-8 as Windows-1252
	// and can decode input a second time.
	doc, err := extractBytes(data, pageURL, o)
	if doc != nil && doc.diagnostic != nil {
		doc.diagnostic.Stats.InputBytes = len(data)
	}
	return doc, err
}

// ExtractBytes reads UTF-8 HTML from input and returns its useful content.
// Decode other character encodings before extraction. pageURL can be empty.
// A nonempty pageURL must be an absolute HTTP or HTTPS URL.
func ExtractBytes(input []byte, pageURL string, opts ...Option) (*Document, error) {
	o, err := applyOptions(opts)
	if err != nil {
		return nil, err
	}
	if o.maxInput > 0 && int64(len(input)) > o.maxInput {
		return nil, &LimitError{Resource: LimitInputBytes, Count: int64(len(input)), Max: o.maxInput}
	}
	// Parse the caller's byte slice directly. Routing through Extract would make
	// io.ReadAll copy the complete input before parsing it.
	doc, err := extractBytes(input, pageURL, o)
	if doc != nil && doc.diagnostic != nil {
		doc.diagnostic.Stats.InputBytes = len(input)
	}
	return doc, err
}

func extractBytes(input []byte, pageURL string, o options) (*Document, error) {
	root, err := html.Parse(bytes.NewReader(input))
	if err != nil {
		return nil, fmt.Errorf("pagemark: parse HTML: %w", err)
	}
	doc, extractErr := extractNode(root, pageURL, o)
	// Some script-driven sites put their complete, server-rendered fallback in a
	// noscript element. The HTML parser follows browser scripting semantics and
	// therefore exposes that fallback as text. Reparse only when the ordinary
	// result is empty or tiny; normal pages keep the single-parse fast path and
	// pages with both versions do not duplicate their content.
	if (doc == nil || utf8.RuneCountInString(doc.Text) < 120) && bytes.Contains(bytes.ToLower(input), []byte("<noscript")) {
		fallbackRoot, parseErr := html.ParseWithOptions(bytes.NewReader(input), html.ParseOptionEnableScripting(false))
		if parseErr == nil {
			if fallback, fallbackErr := extractNode(fallbackRoot, pageURL, o); fallbackErr == nil &&
				fallback != nil && utf8.RuneCountInString(fallback.Text) >= 120 &&
				(doc == nil || utf8.RuneCountInString(fallback.Text) > 2*utf8.RuneCountInString(doc.Text)) {
				if fallback.diagnostic != nil {
					fallback.diagnostic.Fallback = "noscript"
				}
				return fallback, nil
			}
		}
	}
	return doc, extractErr
}

// ExtractNode returns useful content from a parsed HTML tree.
// It does not change root. Do not change root during extraction.
// pageURL can be empty. A nonempty pageURL must be an absolute HTTP or HTTPS URL.
// WithMaxInputBytes does not apply to this function.
func ExtractNode(root *html.Node, pageURL string, opts ...Option) (*Document, error) {
	o, err := applyOptions(opts)
	if err != nil {
		return nil, err
	}
	return extractNode(root, pageURL, o)
}

func applyOptions(opts []Option) (options, error) {
	o := defaultOptions()
	for _, f := range opts {
		if f != nil {
			f(&o)
		}
	}
	if err := o.validate(); err != nil {
		return options{}, err
	}
	o.normalizeUnlimited()
	return o, nil
}

func extractNode(root *html.Node, rawURL string, o options) (*Document, error) {
	if root == nil {
		return nil, ErrNoContent
	}
	var page *url.URL
	if rawURL != "" {
		u, err := url.Parse(rawURL)
		if err != nil || !urlutil.IsHierarchicalHTTP(u) {
			return nil, ErrInvalidURL
		}
		u.User = nil
		page = u
	}
	a := &analysis{o: o, root: root, pageURL: page, base: page}
	if o.diagnostics {
		a.diag = &diagnosticState{ProfileVersion: "1", Fallback: "primary"}
	}
	if err := a.index(root, 0); err != nil {
		return nil, err
	}
	// Most classification passes eventually memoize nearly every element. Size
	// the unified state table now that index has counted them, avoiding repeated
	// map growth and short-lived old bucket arrays on large documents.
	a.nodeStates = make(map[*html.Node]nodeState, a.elements)
	a.findBase()
	// Index subtree evidence before metadata extraction: microdata filtering uses
	// auxiliary-region detection, and subscription checks otherwise rescan large
	// wrappers once for every descendant.
	a.indexSubtreeEvidence(root)
	a.extractMetadata()
	a.segment(root, false)
	a.detectTextListingPre()
	pageType, confidence, candidates := a.inferType()
	if o.pageType != "" {
		pageType = o.pageType
		confidence = 1
		a.pageTypeExplicit = true
	}
	if a.diag != nil {
		a.diag.PageCandidates = candidates
	}
	// Auxiliary-region detection has a small number of article-specific rules.
	// Record the final type before scoring so those regions are hard exclusions,
	// rather than relying on score penalties that long card copy can overcome.
	a.pageType = pageType
	a.score(pageType, scoringPrimary)
	selected := a.selectedNodes()
	winningProfile := scoringPrimary.name()
	// Rendered Markdown documents are already an explicit primary-content
	// boundary. Selecting their complete root both removes surrounding project
	// chrome (file browsers, repository controls, and sidebars) and preserves
	// direct text inside structures such as disclosure details, which is not a
	// standalone scoring block.
	authored := renderedMarkdownDocument(root)
	if authored != nil {
		selected = []*html.Node{authored}
		for i := range a.blocks {
			inside := nodeWithin(a.blocks[i].node, authored)
			a.blocks[i].selected = inside
			if inside {
				a.addReason(&a.blocks[i], "inside rendered Markdown document")
			}
		}
	}
	if authored == nil {
		if a.shouldRetryArticle(pageType, selected) {
			primary := a.makeExtractionAttempt(scoringPrimary, selected)
			winner := primary
			for _, profile := range []scoringProfile{scoringRelaxedLabels, scoringRelaxedThreshold} {
				a.score(pageType, profile)
				nodes := a.selectedNodes()
				candidate := a.makeExtractionAttempt(profile, nodes)
				if betterArticleAttempt(winner, candidate) {
					winner = candidate
				}
			}
			a.restoreExtractionAttempt(winner)
			selected = winner.nodes
			winningProfile = winner.profile
		}
	}
	a.populateBlockDiagnostics()
	fallback := winningProfile
	if len(selected) == 0 {
		selected = a.semanticFallback()
		fallback = "semantic-main"
	}
	quality := a.quality(selected)
	if authored == nil && (pageType == PageTypeArticle || pageType == PageTypeGeneric) && quality < .42 {
		if article, articleQuality := a.semanticArticleFallback(); article != nil {
			selected = []*html.Node{article}
			fallback = "semantic-article"
			quality = articleQuality
		}
	}
	// Region reconstruction is deliberately a bounded article fallback. Normal
	// block extraction and non-article page types keep their existing paths.
	if authored == nil && pageType == PageTypeArticle {
		currentChars, _, _ := a.nodeSetBlockEvidence(selected)
		unexpectedlyShort := currentChars < 450
		if quality < .50 || unexpectedlyShort {
			if region := a.reconstructArticleRegion(); len(region) > 0 {
				regionChars, regionLinks, regionBlocks := a.nodeSetBlockEvidence(region)
				regionQuality := qualityFromEvidence(regionChars, regionLinks, regionBlocks)
				added := regionChars - currentChars
				material := added >= 80 && regionChars*5 >= max(1, currentChars)*6
				if unexpectedlyShort {
					material = added >= 20 && regionChars*25 >= max(1, currentChars)*27
				}
				if material && regionLinks*2 < max(1, regionChars) && regionQuality >= quality-.05 {
					selected = region
					fallback = "article-region"
					quality = regionQuality
				}
			}
		}
	}
	if len(selected) == 0 {
		selected = a.highRecall()
		fallback = "high-recall"
		quality = .2
	}
	if len(selected) == 0 && a.meta.description != "" {
		selected = metadataNodes(a.meta)
		fallback = "metadata"
		quality = .15
	}
	if len(selected) == 0 {
		return nil, ErrNoContent
	}
	exclude := func(n *html.Node) bool {
		// Exclusions identify structural regions. Their text descendants are only
		// reached when the parent was retained, so probing them separately would
		// just populate the node-state map with every text node in the document.
		if n == nil || n.Type != html.ElementNode {
			return false
		}
		discussionAuxiliary := pageType == PageTypeDiscussion &&
			(isDiscussionControlNode(n) || a.hasStandaloneMessageAncestor(n))
		visualAuxiliary := o.includeImages && isVisualElement(n) && !meaningfulVisual(n)
		titleHeading := a.contentTitle != "" && isHeadingTag(strings.ToLower(n.Data)) &&
			(titleEquivalent(articleHeadingText(n), a.contentTitle, a.meta.site) || titleEquivalent(nodeText(n), a.contentTitle, a.meta.site))
		return titleHeading || a.titleExcluded[n] || a.hasIrrelevantAncestor(n) || discussionAuxiliary || visualAuxiliary
	}
	cfg := markdown.Config{Base: a.base, Links: o.includeLinks, Images: o.includeImages, Tables: o.includeTables, MaxBytes: o.maxOutput, Policy: markdown.URLPolicy{Schemes: o.urlPolicy.AllowedSchemes, MaxLength: o.urlPolicy.MaxLength, StripTracking: o.urlPolicy.StripTracking}, Exclude: exclude, PruneEmptyHeadings: true}
	if a.textListingPre != nil {
		cfg.TextPreformatted = func(n *html.Node) bool { return n == a.textListingPre }
	}
	// Resolve the document title before rendering and remove only the heading
	// that represents it. Titles are returned as metadata rather than repeated
	// in Markdown, plain text, sections, or retained media.
	selected, resolvedTitle := a.separateDocumentTitle(selected, cfg, pageType, authored != nil)
	mr := markdown.Convert(selected, cfg)
	// A selected content block can be useful even when the output budget is too
	// small to emit it. Keep that result distinct from an extraction with no
	// useful content: Convert reports discarded substantive content separately
	// from truncation of headings or thematic breaks.
	if strings.TrimSpace(mr.Text) == "" && !mr.DiscardedContent {
		return nil, ErrNoContent
	}
	documentTitle := normalizeText(resolvedTitle)
	if documentTitle == "" && !(a.suppressHeadingTitle && a.meta.titleFromHeading) {
		documentTitle = normalizeText(a.meta.title)
		if !a.meta.titleFromHeading {
			documentTitle = a.cleanedMetadataTitle(documentTitle)
			// A browser-only title at the origin root is normally chrome, but an
			// exactly matching visible h1 makes it authored page content. This
			// also covers accessible image-only product wordmarks.
			browserOnlyRootTitle := a.meta.socialTitle == "" && a.meta.title == a.meta.browserTitle &&
				a.pageURL != nil && (a.pageURL.Path == "" || a.pageURL.Path == "/")
			if browserOnlyRootTitle {
				if visible, found := a.exactVisibleH1Title(a.meta.browserTitle); found {
					documentTitle = visible
				} else {
					documentTitle = ""
				}
			}
		}
	}
	cleanBrowserTitle := ""
	needsVisibleVariant := hasDelimitedTitleSegment(documentTitle)
	if !needsVisibleVariant && normalizedLabel(documentTitle) != normalizedLabel(a.meta.browserTitle) {
		cleanBrowserTitle = a.cleanedMetadataTitle(a.meta.browserTitle)
		needsVisibleVariant = normalizedLabel(documentTitle) != normalizedLabel(cleanBrowserTitle)
	}
	if needsVisibleVariant {
		if cleanBrowserTitle == "" {
			cleanBrowserTitle = a.cleanedMetadataTitle(a.meta.browserTitle)
		}
		documentTitle = a.visibleH1TitleVariant(documentTitle, cleanBrowserTitle)
	}
	if a.pageURL != nil && (a.pageURL.Path == "" || a.pageURL.Path == "/") && a.meta.socialTitle != "" {
		social := a.cleanedMetadataTitle(a.meta.socialTitle)
		if social == "" {
			// A social title is document-specific even when it equals the product or
			// publication name inferred from the host.
			social = normalizeText(a.meta.socialTitle)
		}
		if cleanBrowserTitle == "" {
			cleanBrowserTitle = a.cleanedMetadataTitle(a.meta.browserTitle)
			if cleanBrowserTitle == "" {
				cleanBrowserTitle = normalizeText(a.meta.browserTitle)
			}
		}
		if titlePrefixAtBoundary(social, cleanBrowserTitle) {
			documentTitle = social
		}
	}
	sourceURL := ""
	if page != nil {
		sourceURL = page.String()
	}
	doc := &Document{URL: sourceURL, CanonicalURL: a.meta.canonical, Title: documentTitle, Description: a.meta.description, Author: a.meta.author, SiteName: a.meta.site, Language: a.meta.language, PublishedTime: a.meta.published, PageType: pageType, Markdown: mr.Markdown, Text: mr.Text, Truncated: mr.Truncated, diagnostic: a.diag}
	if len(mr.Links) > 0 {
		doc.Links = make([]Link, len(mr.Links))
		for i, l := range mr.Links {
			doc.Links[i] = Link{Text: l.Text, URL: l.URL}
		}
	}
	if len(mr.Images) > 0 {
		doc.Images = make([]Image, len(mr.Images))
		for i, im := range mr.Images {
			doc.Images[i] = Image{Alt: im.Alt, URL: im.URL}
		}
	}
	if a.diag != nil {
		a.diag.Fallback = fallback
		a.diag.RejectedLinks = mr.Rejected
		a.diag.Stats = diagnosticStats{Elements: a.elements, TextBytes: a.textBytes, Blocks: len(a.blocks), SelectedBlocks: mr.EmittedBlocks, OutputBytes: len(mr.Markdown)}
		a.diag.Quality = clamp(quality)
		a.diag.PageTypeScore = confidence
	}
	if len(mr.Sections) > 0 {
		doc.Sections = make([]Section, len(mr.Sections))
		for i, section := range mr.Sections {
			doc.Sections[i] = Section{Heading: section.Heading, Text: section.Text}
		}
	}
	return doc, nil
}

// ExtractDetailedBytes extracts content and returns an experimental diagnostic
// report from the same extraction pass. Report fields may change in a minor
// release. Ordinary callers should use ExtractBytes.
func ExtractDetailedBytes(input []byte, pageURL string, opts ...Option) (*Document, *DiagnosticReport, error) {
	detailed := withDiagnostics()
	detailedOptions := make([]Option, len(opts)+1)
	copy(detailedOptions, opts)
	detailedOptions[len(opts)] = detailed
	doc, err := ExtractBytes(input, pageURL, detailedOptions...)
	if doc == nil {
		return nil, nil, err
	}
	report := diagnosticReport(doc)
	doc.diagnostic = nil
	return doc, report, err
}

func diagnosticReport(doc *Document) *DiagnosticReport {
	report := &DiagnosticReport{}
	if doc.diagnostic != nil {
		report.ProfileVersion = doc.diagnostic.ProfileVersion
		report.Fallback = doc.diagnostic.Fallback
		report.PageCandidates = doc.diagnostic.PageCandidates
		report.Blocks = doc.diagnostic.Blocks
		report.RejectedLinks = doc.diagnostic.RejectedLinks
		report.Stats = doc.diagnostic.Stats
		report.Quality = doc.diagnostic.Quality
		report.PageTypeScore = doc.diagnostic.PageTypeScore
	}
	return report
}
