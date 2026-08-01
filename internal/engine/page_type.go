package engine

import (
	"math"
	"sort"
	"strings"
)

// pageClassification is the result of applying page-type policy to collected
// evidence. It contains no DOM or analysis state so classification can be
// tested independently from inspection.
type pageClassification struct {
	pageType   PageType
	confidence float64
	candidates []PageCandidate
}

// classifyPage applies the page-type policy to semantic evidence only.
func classifyPage(e pageEvidence, wantCandidates bool) pageClassification {
	scores := map[PageType]float64{
		PageTypeArticle: 0, PageTypeDocumentation: 0, PageTypeDiscussion: 0,
		PageTypeProduct: 0, PageTypeListing: 0, PageTypeCollection: 0,
		PageTypeService: 0, PageTypeGeneric: 1,
	}

	// Repeated, substantive records establish thread context. A lone marked
	// record remains compatible with an article, and several tiny status/action
	// records do not amount to a conversation.
	substantiveDiscussionRecords := e.discussionRecords >= 2 && e.discussionProseChars >= 80
	discussionContext := e.discussionContext || substantiveDiscussionRecords
	if discussionContext {
		scores[PageTypeDiscussion] += 2
	}
	switch e.discussionRecords {
	case 0:
	case 1:
		scores[PageTypeDiscussion] += .25
	default:
		if substantiveDiscussionRecords {
			// Repeated comment-like records are useful evidence, but are capped so
			// annotations cannot overwhelm publication and dominant-prose signals.
			scores[PageTypeDiscussion] += math.Min(4, float64(e.discussionRecords))
			// A discussion page may still publish generic Article metadata for
			// its opening post. When the repeated records dominate the visible
			// prose and there is no semantic article body, let records beyond
			// the ordinary cap distinguish a thread from an article with a small
			// comments section.
			if e.discussionRecords > 4 && e.discussionProseChars*2 >= e.inferenceChars &&
				e.primaryArticleProseChars == 0 {
				scores[PageTypeDiscussion] += math.Min(8, float64(e.discussionRecords-4))
			}
		} else {
			scores[PageTypeDiscussion] += math.Min(1.5, .5*float64(e.discussionRecords))
		}
	}
	if e.documentationContext {
		// Ancestor tokens describe one region, not each descendant block. An
		// explicit documentation container is nevertheless strong page-level
		// evidence, including on sites that use neutral /guide/ URLs.
		scores[PageTypeDocumentation] += 3
	}
	if e.sectionCount >= 3 {
		scores[PageTypeService] += 3
	}
	// Product and price vocabulary also describes regions, not every heading and
	// paragraph inside them. Repeated product records are collection evidence;
	// a single product region remains evidence for a product detail page.
	if e.productRegions == 1 {
		share := float64(e.productRegionChars) / float64(max(1, e.inferenceChars))
		if share >= .5 {
			// A dominant product wrapper is sufficient detail-page evidence even
			// when the URL and metadata are neutral.
			scores[PageTypeProduct] += 2
		} else {
			// Keep an embedded price or affiliate widget below the generic prior.
			scores[PageTypeProduct] += .5
		}
	} else if e.productRegions > 1 {
		scores[PageTypeProduct] += math.Min(3, .75*float64(e.productRegions))
	}
	if e.productRecords >= 4 {
		scores[PageTypeCollection] += 2 * float64(e.productRecords)
	}
	// Card vocabulary is region-level evidence, not one vote per descendant
	// block. Repeated records should identify a listing only when they make up a
	// substantial share of the primary content. This keeps metrics, quotes, and
	// pricing tiers embedded later in a long article from overwhelming its
	// dominant prose sequence.
	if e.listingRecords >= 2 {
		recordShare := float64(e.listingRecordChars) / float64(max(1, e.inferenceChars))
		scores[PageTypeListing]++ // Repetition is weak evidence by itself.
		if recordShare >= .35 {
			scores[PageTypeListing] += math.Min(3, float64(e.listingRecords))
		}
		if recordShare >= .55 {
			scores[PageTypeListing] += 2
		}
		if recordShare >= .75 {
			scores[PageTypeListing]++
		}
	}
	if e.articleType {
		scores[PageTypeArticle] += 5
	}
	if e.schemaProduct {
		scores[PageTypeProduct] += 5
	}
	if e.schemaDiscussion {
		scores[PageTypeDiscussion] += 5
	}
	if e.schemaDocumentation {
		scores[PageTypeDocumentation] += 5
	}
	if e.schemaListing {
		// Explicit page/listing schema should outweigh generic Article metadata
		// emitted by publishing platforms.
		scores[PageTypeListing] += 10
	} else if e.microdataListing {
		scores[PageTypeListing] += 5
	}
	if e.schemaService {
		// A specialized service entity is more informative than a generic Article.
		scores[PageTypeService] += 20
	}
	if e.hasTextListing {
		// Text-mode archives have few of the card/list elements used by modern
		// listings, so their combined pre/link/record evidence is page-level.
		scores[PageTypeListing] += 10
	}
	if e.codeBlocks > 1 {
		// Code is common in technical articles. It is strong documentation
		// evidence only when it dominates the prose structure.
		if e.proseBlocks <= 2 || e.codeChars > e.proseChars {
			scores[PageTypeDocumentation] += 2
		} else {
			scores[PageTypeDocumentation] += .5
		}
	}
	if e.tableBlocks > 0 {
		scores[PageTypeProduct]++
		scores[PageTypeDocumentation]++
	}
	// Paragraph volume is ambiguous inside an explicit documentation region;
	// guides should not become articles merely because they explain a topic in
	// prose. Strong article metadata and structure below can still prevail.
	if !e.documentationContext && e.proseBlocks > 4 {
		scores[PageTypeArticle] += 2
	}
	if !e.documentationContext && e.proseBlocks >= 4 && e.proseChars >= 600 && e.proseChars >= e.codeChars {
		scores[PageTypeArticle] += 2
	}
	// Several long paragraphs outside record-shaped regions establish one
	// sequential narrative. Introductory copy on a catalog normally has only one
	// or two such paragraphs, while an article containing supporting cards keeps
	// accumulating prose independently of those cards.
	if !e.documentationContext && e.longNarrativeParagraphs >= 3 && e.narrativeProseChars >= 500 {
		scores[PageTypeArticle] += 2
		if e.longNarrativeParagraphs >= 6 && e.narrativeProseChars >= 1000 {
			scores[PageTypeArticle]++
		}
	}
	if e.primaryArticleRegions == 1 && (e.proseBlocks >= 2 || e.primaryArticleProseChars >= 120) {
		scores[PageTypeArticle] += 2
	}
	// A headline attached to a real prose region is much stronger than headings
	// repeated by cards. Require body text so a bare schema template does not
	// turn an archive into an article.
	if e.headline && (e.primaryArticleProseChars >= 120 || e.proseChars >= 300) {
		scores[PageTypeArticle] += 2
	}
	if e.primaryArticleProseChars >= 300 {
		scores[PageTypeArticle] += 2
	}
	if e.articlePublished {
		// Generic <time> elements occur on comments, products, and events. Only
		// publication metadata with article-specific provenance gets this bonus.
		scores[PageTypeArticle] += 4
	}
	if e.documentationPath {
		scores[PageTypeDocumentation] += 3
	}
	title := strings.ToLower(e.title)
	urlPath := strings.ToLower(e.urlPath)
	if containsAny(title, "documentation", "reference") || (containsAny(title, "api") && containsAny(title, "guide", "reference")) {
		scores[PageTypeDocumentation] += 2
	}
	if articleURLPath(urlPath) {
		scores[PageTypeArticle] += 2
	}
	if strings.Contains(urlPath, "forum") || strings.Contains(urlPath, "question") || strings.Contains(urlPath, "issue") ||
		strings.Contains(urlPath, "/thread/") || strings.Contains(urlPath, "/threads/") ||
		strings.Contains(urlPath, "/topic/") || strings.Contains(urlPath, "/topics/") ||
		strings.Contains(urlPath, "/t/") || strings.Contains(title, " forum") {
		scores[PageTypeDiscussion] += 3
	}
	if strings.Contains(urlPath, "product") {
		scores[PageTypeProduct] += 3
	}
	return rankPageTypes(scores, wantCandidates)
}

// rankPageTypes selects the highest-scoring type and, when requested, builds a
// deterministic diagnostic candidate list. The ordinary path does not sort or
// allocate that list.
func rankPageTypes(scores map[PageType]float64, wantCandidates bool) pageClassification {
	top := PageCandidate{Type: PageTypeGeneric, Score: scores[PageTypeGeneric]}
	second := PageCandidate{}
	for pageType, score := range scores {
		candidate := PageCandidate{Type: pageType, Score: score}
		if score > top.Score || score == top.Score && pageType < top.Type {
			if pageType != top.Type {
				second = top
			}
			top = candidate
		} else if pageType != top.Type && (score > second.Score || score == second.Score && (second.Type == "" || pageType < second.Type)) {
			second = candidate
		}
	}
	confidence := .5 + (top.Score-second.Score)/(2*math.Max(1, top.Score))
	if !wantCandidates {
		return pageClassification{pageType: top.Type, confidence: clamp(confidence)}
	}
	cs := make([]PageCandidate, 0, len(scores))
	for pageType, score := range scores {
		cs = append(cs, PageCandidate{Type: pageType, Score: score})
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Score == cs[j].Score {
			return cs[i].Type < cs[j].Type
		}
		return cs[i].Score > cs[j].Score
	})
	return pageClassification{pageType: top.Type, confidence: clamp(confidence), candidates: cs}
}

func (a *analysis) inferType(wantCandidates bool) (PageType, float64, []PageCandidate) {
	classification := classifyPage(a.collectPageEvidence(), wantCandidates)
	return classification.pageType, classification.confidence, classification.candidates
}
