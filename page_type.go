package pagemark

import (
	"math"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

func (a *analysis) inferType() (PageType, float64, []PageCandidate) {
	scores := map[PageType]float64{
		PageTypeArticle: 0, PageTypeDocumentation: 0, PageTypeDiscussion: 0,
		PageTypeProduct: 0, PageTypeListing: 0, PageTypeCollection: 0,
		PageTypeService: 0, PageTypeGeneric: 1,
	}
	schema := strings.ToLower(a.meta.schemaType)
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
	documentationPath := strings.Contains(urlPath, "/doc/") || strings.Contains(urlPath, "/docs") || strings.Contains(urlPath, "/api")
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
	discussionContext := a.primaryDiscussionContext()
	documentationContext := false
	proseChars, codeChars, primaryArticleProse := 0, 0, 0
	inferenceChars, narrativeProseChars, longNarrativeParagraphs := 0, 0, 0
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
		inferenceChars += blockChars
		listingRecord := a.inferenceListingRecord(b.node)
		if listingRecord != nil {
			listingRecordChars[listingRecord] += blockChars
		} else if b.kind == "p" {
			narrativeProseChars += blockChars
			if blockChars >= 80 {
				longNarrativeParagraphs++
			}
		}
		article := a.primaryArticleAncestor(b.node)
		if article == nil && nodeWithin(b.node, a.dominantMicrodataArticle) {
			article = a.dominantMicrodataArticle
		}
		if article != nil {
			primaryArticles[article] = true
			if b.kind == "p" {
				primaryArticleProse += b.textChars()
			}
		}
		switch b.kind {
		case "p":
			proseChars += b.textChars()
		case "pre":
			codeChars += b.textChars()
		}
		// Attribute vocabulary is only consumed as boolean evidence below. Scan
		// each ancestor in place instead of repeatedly concatenating and
		// lowercasing a growing token string for every block.
		productVocabulary, documentationVocabulary := false, false
		for p := b.node; p != nil && (!productVocabulary || !documentationVocabulary); p = p.Parent {
			if p.Type != html.ElementNode {
				continue
			}
			if !productVocabulary {
				productVocabulary = elementContainsAny(p, "product", "price", "sku")
			}
			if !documentationVocabulary {
				documentationVocabulary = elementContainsAny(p, "docs", "documentation", "api", "reference") ||
					(documentationPath && elementContainsAny(p, "doc"))
			}
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
		if productVocabulary {
			if region := nearestTokenAncestor(b.node, "product", "price", "sku"); region != nil {
				productRegionChars[region] += blockChars
			}
			if record := nearestTokenAncestor(b.node, "product", "sku"); record != nil {
				productRecords[record] = true
			}
		}
		if documentationVocabulary {
			documentationContext = true
		}
	}
	if len(neutralDiscussionRecords) >= 2 {
		for record, chars := range neutralDiscussionRecords {
			discussionRecords[record] = chars
		}
	}
	discussionProse := 0
	for _, chars := range discussionRecords {
		discussionProse += chars
	}
	// Repeated, substantive records establish thread context. A lone marked
	// record remains compatible with an article, and several tiny status/action
	// records do not amount to a conversation.
	substantiveDiscussionRecords := len(discussionRecords) >= 2 && discussionProse >= 80
	if substantiveDiscussionRecords {
		discussionContext = true
	}
	if discussionContext {
		scores[PageTypeDiscussion] += 2
	}
	switch len(discussionRecords) {
	case 0:
	case 1:
		scores[PageTypeDiscussion] += .25
	default:
		if substantiveDiscussionRecords {
			// Repeated comment-like records are useful evidence, but are capped so
			// annotations cannot overwhelm publication and dominant-prose signals.
			scores[PageTypeDiscussion] += math.Min(4, float64(len(discussionRecords)))
		} else {
			scores[PageTypeDiscussion] += math.Min(1.5, .5*float64(len(discussionRecords)))
		}
	}
	if documentationContext {
		// Ancestor tokens describe one region, not each descendant block. An
		// explicit documentation container is nevertheless strong page-level
		// evidence, including on sites that use neutral /guide/ URLs.
		scores[PageTypeDocumentation] += 3
	}
	sectionCount := 0
	walk(a.root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "section") {
			sectionCount++
		}
		return true
	})
	if sectionCount >= 3 {
		scores[PageTypeService] += 3
	}
	// Product and price vocabulary also describes regions, not every heading and
	// paragraph inside them. Repeated product records are collection evidence;
	// a single product region remains evidence for a product detail page.
	if len(productRegionChars) == 1 {
		for _, chars := range productRegionChars {
			share := float64(chars) / float64(max(1, inferenceChars))
			if share >= .5 {
				// A dominant product wrapper is sufficient detail-page evidence even
				// when the URL and metadata are neutral.
				scores[PageTypeProduct] += 2
			} else {
				// Keep an embedded price or affiliate widget below the generic prior.
				scores[PageTypeProduct] += .5
			}
		}
	} else if len(productRegionChars) > 1 {
		scores[PageTypeProduct] += math.Min(3, .75*float64(len(productRegionChars)))
	}
	if len(productRecords) >= 4 {
		scores[PageTypeCollection] += 2 * float64(len(productRecords))
	}
	// Card vocabulary is region-level evidence, not one vote per descendant
	// block. Repeated records should identify a listing only when they make up a
	// substantial share of the primary content. This keeps metrics, quotes, and
	// pricing tiers embedded later in a long article from overwhelming its
	// dominant prose sequence.
	if len(listingRecordChars) >= 2 {
		recordChars := 0
		for _, chars := range listingRecordChars {
			recordChars += chars
		}
		recordShare := float64(recordChars) / float64(max(1, inferenceChars))
		scores[PageTypeListing]++ // Repetition is weak evidence by itself.
		if recordShare >= .35 {
			scores[PageTypeListing] += math.Min(3, float64(len(listingRecordChars)))
		}
		if recordShare >= .55 {
			scores[PageTypeListing] += 2
		}
		if recordShare >= .75 {
			scores[PageTypeListing]++
		}
	}
	if a.meta.articleType || strings.Contains(schema, "article") || strings.Contains(schema, "news") {
		scores[PageTypeArticle] += 5
	}
	if strings.Contains(schema, "product") {
		scores[PageTypeProduct] += 5
	}
	if strings.Contains(schema, "discussion") || strings.Contains(schema, "question") ||
		strings.Contains(schema, "qapage") || strings.Contains(schema, "forumposting") {
		scores[PageTypeDiscussion] += 5
	}
	if strings.Contains(schema, "searchresultspage") {
		// SearchResultsPage is explicit page-level evidence and should outweigh
		// generic Article metadata added by publishing platforms.
		scores[PageTypeListing] += 10
	} else if strings.Contains(schema, "itemlist") || a.meta.microdataListing {
		scores[PageTypeListing] += 5
	}
	if strings.Contains(schema, "governmentservice") || strings.Contains(schema, "service") {
		// A specialized service entity is more informative than a generic Article
		// entity when both describe the same page.
		scores[PageTypeService] += 20
	}
	if a.textListingPre != nil {
		// Text-mode archives have few of the card/list elements used by modern
		// listings, so their combined pre/link/record evidence is page-level.
		scores[PageTypeListing] += 10
	}
	title := strings.ToLower(a.meta.title)
	if counts["pre"] > 1 {
		// Code is common in technical articles. It is strong documentation
		// evidence only when it dominates the prose structure.
		if counts["p"] <= 2 || codeChars > proseChars {
			scores[PageTypeDocumentation] += 2
		} else {
			scores[PageTypeDocumentation] += .5
		}
	}
	if counts["table"] > 0 {
		scores[PageTypeProduct]++
		scores[PageTypeDocumentation]++
	}
	// Paragraph volume is ambiguous inside an explicit documentation region;
	// guides should not become articles merely because they explain a topic in
	// prose. Strong article metadata and structure below can still prevail.
	if !documentationContext && counts["p"] > 4 {
		scores[PageTypeArticle] += 2
	}
	if !documentationContext && counts["p"] >= 4 && proseChars >= 600 && proseChars >= codeChars {
		scores[PageTypeArticle] += 2
	}
	// Several long paragraphs outside record-shaped regions establish one
	// sequential narrative. Introductory copy on a catalog normally has only one
	// or two such paragraphs, while an article containing supporting cards keeps
	// accumulating prose independently of those cards.
	if !documentationContext && longNarrativeParagraphs >= 3 && narrativeProseChars >= 500 {
		scores[PageTypeArticle] += 2
		if longNarrativeParagraphs >= 6 && narrativeProseChars >= 1000 {
			scores[PageTypeArticle]++
		}
	}
	if len(primaryArticles) == 1 && (counts["p"] >= 2 || primaryArticleProse >= 120) {
		scores[PageTypeArticle] += 2
	}
	// A headline attached to a real prose region is much stronger than headings
	// repeated by cards. Require body text so a bare schema template does not
	// turn an archive into an article.
	if a.meta.headline && (primaryArticleProse >= 120 || proseChars >= 300) {
		scores[PageTypeArticle] += 2
	}
	if primaryArticleProse >= 300 {
		scores[PageTypeArticle] += 2
	}
	if a.meta.articlePublished {
		// Generic <time> elements occur on comments, products, and events. Only
		// publication metadata with article-specific provenance gets this bonus.
		scores[PageTypeArticle] += 4
	}
	if documentationPath {
		scores[PageTypeDocumentation] += 3
	}
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
	var cs []PageCandidate
	for t, s := range scores {
		cs = append(cs, PageCandidate{t, s})
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Score == cs[j].Score {
			return cs[i].Type < cs[j].Type
		}
		return cs[i].Score > cs[j].Score
	})
	top := cs[0]
	second := 0.0
	if len(cs) > 1 {
		second = cs[1].Score
	}
	confidence := .5 + (top.Score-second)/(2*math.Max(1, top.Score))
	return top.Type, clamp(confidence), cs
}
