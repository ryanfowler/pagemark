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

type memoizedBool uint8

const (
	memoizedBoolUnknown memoizedBool = iota
	memoizedBoolFalse
	memoizedBoolTrue
)

func (m memoizedBool) value() (value, known bool) {
	switch m {
	case memoizedBoolFalse:
		return false, true
	case memoizedBoolTrue:
		return true, true
	default:
		return false, false
	}
}

func (m *memoizedBool) store(value bool) {
	*m = memoizedBoolFalse
	if value {
		*m = memoizedBoolTrue
	}
}

type memoizedCount uint8

// memoizedCountMax is the largest count needed by repeated-record policy.
const memoizedCountMax = 2

func (m memoizedCount) value() (value int, known bool) {
	if m == 0 {
		return 0, false
	}
	return int(m - 1), true
}

func (m *memoizedCount) store(value int) {
	value = max(0, min(value, memoizedCountMax))
	*m = memoizedCount(value + 1)
}

// nodeState contains memoized policy decisions. Immutable DOM facts belong in
// evidenceIndex instead of this classification cache.
type nodeState struct {
	irrelevant, irrelevantAncestor, baseAuxiliary, articleAuxiliary memoizedBool
	inferenceAuxiliary                                              memoizedBool
	boilerplateTokenCheck                                           memoizedBool
	articleComment                                                  memoizedBool
	commentCount                                                    memoizedCount
	semanticBefore, semanticAfter                                   memoizedBool
	articleProseBefore, selfReference                               memoizedBool
	articleCardCount                                                memoizedCount
	substantialArticle                                              memoizedBool
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
