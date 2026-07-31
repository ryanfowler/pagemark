package engine

import (
	"net/url"

	"golang.org/x/net/html"
)

type block struct {
	id         int
	node       *html.Node
	kind, text string
	score      float64
	selected   bool
	imageOnly  bool
	reasons    []string

	// Every scoring profile and several article-recovery passes query these
	// immutable subtree counts. Keep them on the much smaller block set rather
	// than adding evidence to every DOM node.
	chars, linkedChars, controlCount int
	evidenceIndexed                  bool
}

// articleRegionEvidence is a Readability-style, container-level view of the
// block evidence. It is intentionally derived from blocks rather than from a
// second text extraction pipeline.
type articleRegionEvidence struct {
	node             *html.Node
	score            float64
	proseChars       int
	linkedChars      int
	strongParagraphs int
	selectedBlocks   int
	documentOrder    int
}

// nodeState contains memoized policy decisions. Immutable DOM facts belong in
// evidenceIndex instead of this classification cache.
type nodeState struct {
	irrelevant, irrelevantAncestor, baseAuxiliary, articleAuxiliary uint8
	inferenceAuxiliary                                              uint8
	articleComment, commentCount                                    uint8
	semanticBefore, semanticAfter                                   uint8
	articleProseBefore, selfReference                               uint8
	articleCardCount, substantialArticle                            uint8
	inferenceTokens                                                 uint16
}

type analysis struct {
	o                                                options
	root                                             *html.Node
	evidence                                         *evidenceIndex
	pageURL, base                                    *url.URL
	elements, textBytes, maxDepth                    int
	blocks                                           []block
	meta                                             metadata
	pageType                                         PageType
	pageTypeExplicit                                 bool
	diag                                             *diagnosticState
	nodeStates                                       map[*html.Node]nodeState
	titleExcluded                                    map[*html.Node]bool
	contentTitle                                     string
	suppressHeadingTitle                             bool
	semanticBeforeIndexed, semanticAfterIndexed      bool
	articleProseBeforeIndexed                        bool
	microdataArticleRecords                          map[*html.Node]bool
	listingWrapperRecords                            map[*html.Node]map[*html.Node]bool
	listingStates                                    map[*html.Node]uint8
	dominantMicrodataArticle, textListingPre         *html.Node
	strongArticleProse                               map[*html.Node]bool
	strongArticleProseIndexed, hasStrongArticleProse bool
}

type metadata struct {
	title, browserTitle, socialTitle, description, author, site, language, published, canonical, schemaType string
	titlePriority, descriptionPriority, authorPriority, publishedPriority                                   uint8
	articlePublished, articleType, schemaDiscussion, schemaDocumentation                                    bool
	schemaProduct, schemaListing, schemaService, headline, microdataListing, titleFromHeading               bool
}
