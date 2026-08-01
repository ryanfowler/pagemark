package engine

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// pageEvidence is the compact, semantic input to page classification. It is
// collected from the DOM once so the classifier does not depend on traversal
// order, DOM nodes, or analysis caches.
type pageEvidence struct {
	proseChars               int
	codeChars                int
	inferenceChars           int
	narrativeProseChars      int
	longNarrativeParagraphs  int
	primaryArticleProseChars int
	discussionRecords        int
	discussionProseChars     int
	listingRecords           int
	listingRecordChars       int
	productRecords           int
	productRegions           int
	productRegionChars       int
	sectionCount             int

	proseBlocks           int
	codeBlocks            int
	tableBlocks           int
	primaryArticleRegions int

	documentationContext bool
	documentationPath    bool
	discussionContext    bool
	hasTextListing       bool

	// Metadata and schema evidence is kept separate from visible text evidence.
	articleType         bool
	schemaProduct       bool
	schemaDiscussion    bool
	schemaDocumentation bool
	schemaListing       bool
	microdataListing    bool
	schemaService       bool
	headline            bool
	articlePublished    bool
	title               string
	urlPath             string
}

func (a *analysis) collectPageEvidence() pageEvidence {
	e := pageEvidence{}
	urlPath := ""
	if a.pageURL != nil {
		urlPath = strings.ToLower(a.pageURL.Path)
	}
	// Prefer a canonical path when present: the supplied URL may be an archive,
	// redirect, or tracking URL that says little about the page itself. Resolve
	// this before block inference so a generic `doc` wrapper is meaningful only
	// on a documentation route.
	if canonical, err := url.Parse(a.meta.canonical); err == nil && canonical.Path != "" {
		urlPath = strings.ToLower(canonical.Path)
	}
	e.urlPath = urlPath
	e.documentationPath = strings.Contains(urlPath, "/doc/") || strings.Contains(urlPath, "/docs") || strings.Contains(urlPath, "/api")

	counts := map[string]int{}
	productRecords := map[*html.Node]bool{}
	productRegionChars := map[*html.Node]int{}
	listingRecordChars := map[*html.Node]int{}
	discussionRecords := map[*html.Node]int{}
	neutralDiscussionRecords := map[*html.Node]int{}
	// Discussion vocabulary is page-level evidence only when it belongs to the
	// primary container. Vocabulary inherited from an arbitrary ancestor is
	// deliberately not used: a sidebar or comments widget may itself be called a
	// thread without making the page a thread.
	e.discussionContext = a.primaryDiscussionContext()
	primaryArticles := map[*html.Node]bool{}

	for _, b := range a.blocks {
		// Recommendations are page furniture, not records belonging to the page's
		// subject. In particular, do not let every heading and excerpt in a card
		// grid cast another vote for a listing classification.
		if a.inferenceAuxiliaryBlock(b.node) || a.hasMicrodataArticleRecordAncestor(b.node) {
			continue
		}
		counts[b.kind]++
		blockChars := b.textChars()
		e.inferenceChars += blockChars
		listingRecord := a.inferenceListingRecord(b.node)
		if listingRecord != nil {
			listingRecordChars[listingRecord] += blockChars
		} else if b.kind == "p" {
			e.narrativeProseChars += blockChars
			if blockChars >= 80 {
				e.longNarrativeParagraphs++
			}
		}
		article := a.primaryArticleAncestor(b.node)
		if article == nil && nodeWithin(b.node, a.dominantMicrodataArticle) {
			article = a.dominantMicrodataArticle
		}
		if article == nil {
			article = a.conventionalArticleBodyAncestor(b.node)
		}
		if article != nil {
			primaryArticles[article] = true
			if b.kind == "p" {
				e.primaryArticleProseChars += b.textChars()
			}
		}
		switch b.kind {
		case "p":
			e.proseChars += b.textChars()
		case "pre":
			e.codeChars += b.textChars()
		}
		// Attribute vocabulary is only consumed as boolean evidence below. Scan
		// each ancestor in place instead of repeatedly concatenating and
		// lowercasing a growing token string for every block.
		var productRegion, productRecord *html.Node
		documentationVocabulary := false
		for p := b.node; p != nil; p = p.Parent {
			if p.Type != html.ElementNode {
				continue
			}
			flags := a.inferenceTokenFlags(p)
			if productRegion == nil && flags&(inferenceTokenProduct|inferenceTokenPrice|inferenceTokenSKU) != 0 {
				productRegion = p
			}
			if productRecord == nil && flags&(inferenceTokenProduct|inferenceTokenSKU) != 0 {
				productRecord = p
			}
			documentationVocabulary = documentationVocabulary ||
				flags&(inferenceTokenDocs|inferenceTokenDocumentation|inferenceTokenAPI|inferenceTokenReference) != 0 ||
				e.documentationPath && flags&inferenceTokenDoc != 0
		}
		// Count substantive records independently, but do not promote vocabulary
		// inherited from their enclosing widget to page-level context. In
		// particular, one .message in a discussion sidebar and one .post article
		// are weak evidence. Repetition and prose volume are considered below.
		if record := nearestDiscussionRecordAncestor(b.node); record != nil {
			if _, seen := discussionRecords[record]; !seen {
				discussionRecords[record] = commentRecordTextLength(record)
			}
		} else if record := nearestNeutralDiscussionRecord(b.node); record != nil {
			if _, seen := neutralDiscussionRecords[record]; !seen {
				neutralDiscussionRecords[record] = commentRecordTextLength(record)
			}
			// Some forums put all vocabulary on the thread wrapper and use plain
			// semantic articles for individual messages. Keep these records
			// separate so one neutral article cannot turn an ordinary page into a
			// discussion merely because an ancestor happens to say “thread”.
		}
		if productRegion != nil {
			productRegionChars[productRegion] += blockChars
		}
		if productRecord != nil {
			productRecords[productRecord] = true
		}
		if documentationVocabulary {
			e.documentationContext = true
		}
	}
	if len(neutralDiscussionRecords) >= 2 {
		for record, chars := range neutralDiscussionRecords {
			discussionRecords[record] = chars
		}
	}
	for _, chars := range discussionRecords {
		e.discussionProseChars += chars
	}
	for _, chars := range listingRecordChars {
		e.listingRecordChars += chars
	}
	for _, chars := range productRegionChars {
		e.productRegionChars += chars
	}

	e.proseBlocks = counts["p"]
	e.codeBlocks = counts["pre"]
	e.tableBlocks = counts["table"]
	e.primaryArticleRegions = len(primaryArticles)
	e.discussionRecords = len(discussionRecords)
	e.listingRecords = len(listingRecordChars)
	e.productRecords = len(productRecords)
	e.productRegions = len(productRegionChars)
	e.sectionCount = a.pageSectionCount()
	e.hasTextListing = a.textListingPre != nil
	e.articleType = a.meta.articleType
	e.schemaProduct = a.meta.schemaProduct
	e.schemaDiscussion = a.meta.schemaDiscussion
	e.schemaDocumentation = a.meta.schemaDocumentation
	e.schemaListing = a.meta.schemaListing
	e.microdataListing = a.meta.microdataListing
	e.schemaService = a.meta.schemaService
	e.headline = a.meta.headline
	e.articlePublished = a.meta.articlePublished
	e.title = a.meta.title
	return e
}

func (a *analysis) pageSectionCount() int {
	if a.evidence != nil {
		return a.evidence.sections
	}
	sectionCount := 0
	walk(a.root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "section") {
			sectionCount++
		}
		return true
	})
	return sectionCount
}
