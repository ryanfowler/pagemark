package pagemark

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
}

// scoringProfile is deliberately internal. Relaxation is an article recovery
// mechanism, not a general-purpose extraction mode.
type nodeState struct {
	irrelevant, irrelevantAncestor, articleAuxiliary uint8
	inferenceAuxiliary, discussionBody               uint8
	subscriptionEvidence                             uint8
	articleComment, commentCount                     uint8
	articleDescendant, semanticBefore, semanticAfter uint8
	articleProseBefore, selfReference                uint8
	articleCardCount, substantialArticle             uint8
}

type analysis struct {
	o                                                options
	root                                             *html.Node
	pageURL, base                                    *url.URL
	elements, attrs, attrBytes, textBytes, maxDepth  int
	blocks                                           []block
	meta                                             metadata
	pageType                                         PageType
	pageTypeExplicit                                 bool
	diag                                             *Diagnostics
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

// Extract reads UTF-8 HTML and extracts useful content. Callers must decode
// input in other character encodings before calling Extract.
func (a *analysis) index(n *html.Node, depth int) error {
	if depth > a.maxDepth {
		a.maxDepth = depth
	}
	if a.o.maxDepth > 0 && depth > a.o.maxDepth {
		return &LimitError{"DOM depth", int64(depth), int64(a.o.maxDepth)}
	}
	if n.Type == html.ElementNode {
		a.elements++
		if a.o.maxElements > 0 && a.elements > a.o.maxElements {
			return &LimitError{"elements", int64(a.elements), int64(a.o.maxElements)}
		}
		a.attrs += len(n.Attr)
		for _, x := range n.Attr {
			a.attrBytes += len(x.Key) + len(x.Val)
		}
		if a.o.maxAttributes > 0 && a.attrs > a.o.maxAttributes {
			return &LimitError{"attributes", int64(a.attrs), int64(a.o.maxAttributes)}
		}
		if a.o.maxAttributeBytes > 0 && a.attrBytes > a.o.maxAttributeBytes {
			return &LimitError{"attribute bytes", int64(a.attrBytes), int64(a.o.maxAttributeBytes)}
		}
	}
	if n.Type == html.TextNode {
		a.textBytes += len(n.Data)
		if a.o.maxText > 0 && a.textBytes > a.o.maxText {
			return &LimitError{"text bytes", int64(a.textBytes), int64(a.o.maxText)}
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if err := a.index(ch, depth+1); err != nil {
			return err
		}
	}
	return nil
}
