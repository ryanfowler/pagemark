// Package pagemark extracts useful page content as safe Markdown.
//
// The output contains untrusted source data. It does not protect an agent from
// prompt injection. The package does not fetch pages or run JavaScript.
package pagemark

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/markdown"
	"golang.org/x/net/html"
)

func Extract(input io.Reader, pageURL string, opts ...Option) (*Document, error) {
	o := applyOptions(opts)
	if input == nil {
		return nil, fmt.Errorf("pagemark: read input: %w", io.ErrUnexpectedEOF)
	}
	var source io.Reader = input
	if o.maxInput > 0 {
		source = io.LimitReader(input, o.maxInput+1)
	}
	data, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	if o.maxInput > 0 && int64(len(data)) > o.maxInput {
		return nil, &LimitError{"input bytes", int64(len(data)), o.maxInput}
	}
	// Extract accepts UTF-8. Callers with another encoding must decode it before
	// extraction; attempting to sniff here can misinterpret UTF-8 as Windows-1252
	// and can decode input a second time.
	doc, err := extractBytes(data, pageURL, o)
	if doc != nil {
		doc.Stats.InputBytes = len(data)
	}
	return doc, err
}

// ExtractBytes extracts useful content from UTF-8 HTML bytes.
func ExtractBytes(input []byte, pageURL string, opts ...Option) (*Document, error) {
	o := applyOptions(opts)
	if o.maxInput > 0 && int64(len(input)) > o.maxInput {
		return nil, &LimitError{"input bytes", int64(len(input)), o.maxInput}
	}
	// Parse the caller's byte slice directly. Routing through Extract would make
	// io.ReadAll copy the complete input before parsing it.
	doc, err := extractBytes(input, pageURL, o)
	if doc != nil {
		doc.Stats.InputBytes = len(input)
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
				fallback.Warnings = append(fallback.Warnings, Warning{"fallback", "The noscript fallback produced the result."})
				return fallback, nil
			}
		}
	}
	return doc, extractErr
}

// ExtractNode extracts useful content from a parsed HTML tree. It does not change root.
// The caller must not change root during extraction.
func ExtractNode(root *html.Node, pageURL string, opts ...Option) (*Document, error) {
	return extractNode(root, pageURL, applyOptions(opts))
}

func applyOptions(opts []Option) options {
	o := defaultOptions()
	for _, f := range opts {
		if f != nil {
			f(&o)
		}
	}
	if o.maxInput < 0 {
		o.maxInput = 0
	}
	if o.maxOutput < 0 {
		o.maxOutput = 0
	}
	return o
}

func extractNode(root *html.Node, rawURL string, o options) (*Document, error) {
	if root == nil {
		return nil, ErrNoContent
	}
	var page *url.URL
	if rawURL != "" {
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, ErrInvalidURL
		}
		page = u
	}
	a := &analysis{o: o, root: root, pageURL: page, base: page}
	if o.diagnostics {
		a.diag = &Diagnostics{ProfileVersion: "1", Fallback: "primary"}
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
	selected, repeatedExcluded, repeatedDropped := a.selectedNodes(pageType)
	winningProfile := scoringPrimary.name()
	// Rendered Markdown documents are already an explicit primary-content
	// boundary. Selecting their complete root both removes surrounding project
	// chrome (file browsers, repository controls, and sidebars) and preserves
	// direct text inside structures such as disclosure details, which is not a
	// standalone scoring block.
	authored := renderedMarkdownDocument(root)
	if authored != nil {
		selected = []*html.Node{authored}
		repeatedExcluded = nil
		repeatedDropped = 0
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
				nodes, _, _ := a.selectedNodes(pageType)
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
		repeatedExcluded = nil
		repeatedDropped = 0
		fallback = "semantic-main"
	}
	quality := a.quality(selected)
	if authored == nil && (pageType == PageTypeArticle || pageType == PageTypeGeneric) && quality < .42 {
		if article, articleQuality := a.semanticArticleFallback(); article != nil {
			selected = []*html.Node{article}
			repeatedExcluded = nil
			repeatedDropped = 0
			fallback = "semantic-article"
			quality = articleQuality
		}
	}
	// Region reconstruction is deliberately a bounded article fallback. Normal
	// block extraction, non-article page types, and repeated-item limiting keep
	// their existing paths.
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
					repeatedExcluded = nil
					repeatedDropped = 0
					fallback = "article-region"
					quality = regionQuality
				}
			}
		}
	}
	if len(selected) == 0 {
		selected = a.highRecall()
		repeatedExcluded = nil
		repeatedDropped = 0
		fallback = "high-recall"
		quality = .2
	}
	if len(selected) == 0 && a.meta.description != "" {
		selected = metadataNodes(a.meta)
		repeatedExcluded = nil
		repeatedDropped = 0
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
		return titleHeading || a.titleExcluded[n] || a.hasIrrelevantAncestor(n) || repeatedExcluded[n] || discussionAuxiliary || visualAuxiliary
	}
	cfg := markdown.Config{Base: a.base, Links: o.includeLinks, Images: o.includeImages, Tables: o.includeTables, MaxLinks: o.maxLinks, MaxImages: o.maxImages, MaxTableCells: o.maxTableCells, MaxBytes: o.maxOutput, Policy: markdown.URLPolicy{Schemes: o.urlPolicy.Schemes, AllowMailto: o.urlPolicy.AllowMailto, MaxLength: o.urlPolicy.MaxLength, StripTracking: o.urlPolicy.StripTracking}, Exclude: exclude, PruneEmptyHeadings: true}
	if a.textListingPre != nil {
		cfg.TextPreformatted = func(n *html.Node) bool { return n == a.textListingPre }
	}
	// Resolve the document title before rendering and remove only the heading
	// that represents it. Titles are returned as metadata rather than repeated
	// in Markdown, plain text, sections, or retained media.
	selected, resolvedTitle := a.separateDocumentTitle(selected, cfg, pageType, authored != nil)
	mr := markdown.Convert(selected, cfg)
	if strings.TrimSpace(mr.Text) == "" {
		return nil, ErrNoContent
	}
	documentTitle := normalizeText(resolvedTitle)
	if documentTitle == "" && !(a.suppressHeadingTitle && a.meta.titleFromHeading) {
		documentTitle = normalizeText(a.meta.title)
		if !a.meta.titleFromHeading {
			documentTitle = a.cleanedMetadataTitle(documentTitle)
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
	doc := &Document{URL: rawURL, CanonicalURL: a.meta.canonical, Title: documentTitle, Description: a.meta.description, Author: a.meta.author, SiteName: a.meta.site, Language: a.meta.language, PublishedTime: a.meta.published, PageType: pageType, PageTypeScore: confidence, Markdown: mr.Markdown, Text: mr.Text, Quality: clamp(quality), Diagnostics: a.diag, Stats: Stats{Elements: a.elements, TextBytes: a.textBytes, Blocks: len(a.blocks), OutputBytes: len(mr.Markdown)}}
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
	doc.Stats.SelectedBlocks = mr.EmittedBlocks
	if repeatedDropped > 0 {
		doc.Warnings = append(doc.Warnings, Warning{"repeated-items-truncated", fmt.Sprintf("The repeated-item limit dropped %d selected content items.", repeatedDropped)})
	}
	if mr.Truncated {
		doc.Warnings = append(doc.Warnings, Warning{"output-truncated", "The output reached the configured byte limit."})
	}
	if strings.HasPrefix(fallback, "relaxed-") {
		doc.Warnings = append(doc.Warnings, Warning{"relaxed-article-extraction", "A relaxed article extraction profile produced the result."})
	} else if fallback != "primary" {
		doc.Warnings = append(doc.Warnings, Warning{"fallback", "The " + fallback + " fallback produced the result."})
	}
	if a.diag != nil {
		a.diag.Fallback = fallback
		a.diag.RejectedLinks = mr.Rejected
	}
	if len(mr.Sections) > 0 {
		doc.Sections = make([]Section, len(mr.Sections))
		for i, section := range mr.Sections {
			doc.Sections[i] = Section{Heading: section.Heading, Text: section.Text}
		}
	}
	if !o.includeMetadata {
		doc.Title = ""
		doc.Description = ""
		doc.Author = ""
		doc.SiteName = ""
		doc.Language = ""
		doc.PublishedTime = ""
		doc.CanonicalURL = ""
	}
	if o.logger != nil {
		o.logger.Debug("extracted page", "type", pageType, "quality", doc.Quality, "blocks", len(a.blocks), "selected", doc.Stats.SelectedBlocks)
	}
	return doc, nil
}
