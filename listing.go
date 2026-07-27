package pagemark

import (
	"strings"

	"golang.org/x/net/html"
)

func listingRecord(n *html.Node) *html.Node {
	for p := n; p != nil; p = p.Parent {
		if isListingRecordElement(p) {
			return p
		}
	}
	return nil
}

func (a *analysis) listingRecord(n *html.Node) *html.Node {
	if record := listingRecord(n); record != nil {
		return record
	}
	return a.inferenceListingRecord(n)
}

func (a *analysis) listingHeadingIsRecord(n *html.Node) bool {
	if a.listingRecord(n) != nil {
		return true
	}
	// On listing pages an article or list row is a record even when its class
	// only says "featured" and does not carry a conventional record token.
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		// A forced listing may have no record classes at all. Recognize repeated
		// sibling div/section records only when each has a compatible card shape.
		// Merely having two generic layout children is insufficient: a wrapped page
		// heading next to a grid is a common heterogeneous layout.
		if repeatedUnmarkedListingRecord(p) {
			return true
		}
		switch strings.ToLower(p.Data) {
		case "article", "li", "tr":
			return true
		case "main", "body", "html":
			return false
		}
	}
	return false
}

func repeatedUnmarkedListingRecord(n *html.Node) bool {
	if n == nil || n.Parent == nil || n.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(n.Data)
	if tag != "div" && tag != "section" || listingCohortFurniture(n) || !unmarkedListingRecordShape(n) {
		return false
	}
	matches := 0
	for sibling := n.Parent.FirstChild; sibling != nil; sibling = sibling.NextSibling {
		if sibling.Type == html.ElementNode && strings.EqualFold(sibling.Data, tag) &&
			!listingCohortFurniture(sibling) && unmarkedListingRecordShape(sibling) {
			matches++
			if matches >= 2 {
				return true
			}
		}
	}
	return false
}

// listingCohortFurniture identifies heterogeneous page-level panels that often
// sit beside a results grid but are not records in that grid. Explicitly marked
// records are recognized before this conservative unmarked-record fallback.
func listingCohortFurniture(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "header", "nav", "aside":
		return true
	}
	if elementContainsAny(n, "heading", "header", "title", "intro", "filter", "help", "toolbar", "controls") {
		return true
	}
	switch normalizedLabel(firstRegionHeading(n)) {
	case "filter", "filters", "help", "search", "refine results", "filter results":
		return true
	}
	return false
}

// unmarkedListingRecordShape requires one record heading plus body or link
// evidence. Requiring exactly one heading prevents a grid wrapper containing
// several cards from looking compatible with a separate page-title wrapper.
func unmarkedListingRecordShape(n *html.Node) bool {
	headings, proseOrLink := 0, false
	walk(n, func(current *html.Node) bool {
		if current != n && (hardHidden(current) || irrelevantNode(current) || isAdvertisementRegion(current)) {
			return false
		}
		if current.Type != html.ElementNode {
			return true
		}
		tag := strings.ToLower(current.Data)
		if isHeadingTag(tag) && normalizeText(nodeText(current)) != "" {
			headings++
			return headings <= 1
		}
		if tag == "p" || tag == "blockquote" {
			proseOrLink = proseOrLink || normalizeText(nodeText(current)) != ""
		}
		if tag == "a" && attrValue(current, "href") != "" && normalizeText(nodeText(current)) != "" {
			proseOrLink = true
		}
		return headings <= 1
	})
	return headings == 1 && proseOrLink
}

func isListingRecordElement(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !elementContainsAny(n, "card", "result", "item", "product", "record") {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "div", "article", "section", "li", "tr", "a", "figure":
		return true
	}
	return false
}

// inferenceListingRecord returns the repeated record containing n. A token on
// the record itself is preferred. Plural result/listing containers are treated
// as collection context, with their repeated semantic or direct children used
// as the record keys instead of collapsing the whole collection into one key.
func (a *analysis) inferenceListingRecord(n *html.Node) *html.Node {
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		// Test token-bearing attributes directly. This lookup runs for every block
		// ancestor during type inference; building a normalized token string here
		// made listing-heavy pages spend much of their time copying attributes.
		if elementContainsAny(p, "card", "item", "record") {
			return p
		}
		wrapper := elementContainsAny(p, "listing", "listings", "results")
		if wrapper || elementContainsAny(p, "result") {
			for q := n; q != nil && q != p; q = q.Parent {
				if a.inferenceListingWrapperRecords(p)[q] {
					return q
				}
			}
			// Singular .result commonly marks an individual record. Only use the
			// container itself when it did not expose repeated child records.
			if !wrapper {
				return p
			}
		}
	}
	return nil
}

func (a *analysis) inferenceListingWrapperRecords(wrapper *html.Node) map[*html.Node]bool {
	if a.listingWrapperRecords == nil {
		a.listingWrapperRecords = make(map[*html.Node]map[*html.Node]bool)
	}
	if records, ok := a.listingWrapperRecords[wrapper]; ok {
		return records
	}
	records := make(map[*html.Node]bool)
	type semanticCohort struct {
		parent *html.Node
		tag    string
	}
	cohorts := make(map[semanticCohort][]*html.Node)
	var cohortOrder []semanticCohort

	// Compare sibling cohorts rather than pooling semantic descendants from the
	// entire wrapper. Otherwise small tag/feature lists nested inside generic
	// cards can displace the cards themselves as the inferred records.
	walk(wrapper, func(n *html.Node) bool {
		if n == wrapper || n.Type != html.ElementNode {
			return true
		}
		// Type inference runs before a.pageType is assigned. Do not call the
		// cached, type-dependent isIrrelevantNode here: doing so would preserve an
		// incomplete result after the final article profile is known.
		if hardHidden(n) || irrelevantNode(n) || isAdvertisementRegion(n) {
			return false
		}
		tag := strings.ToLower(n.Data)
		switch tag {
		case "article", "li", "tr":
			key := semanticCohort{parent: n.Parent, tag: tag}
			if _, seen := cohorts[key]; !seen {
				cohortOrder = append(cohortOrder, key)
			}
			cohorts[key] = append(cohorts[key], n)
			return false
		}
		return true
	})

	var best []*html.Node
	bestPriority := -1
	consider := func(candidate []*html.Node, priority int) {
		if len(candidate) < 2 {
			return
		}
		if len(candidate) > len(best) || (len(candidate) == len(best) && priority > bestPriority) {
			best = candidate
			bestPriority = priority
		}
	}
	// Generic direct children form the fallback cohort. Prefer article/tr
	// cohorts on a tie, but prefer direct cards over nested li metadata.
	var direct []*html.Node
	for ch := wrapper.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode || hardHidden(ch) || irrelevantNode(ch) || isAdvertisementRegion(ch) {
			continue
		}
		switch strings.ToLower(ch.Data) {
		case "div", "section", "a", "figure":
			direct = append(direct, ch)
		}
	}
	consider(direct, 1)
	for _, key := range cohortOrder {
		priority := 0
		if key.tag == "article" || key.tag == "tr" {
			priority = 2
		}
		consider(cohorts[key], priority)
	}
	for _, record := range best {
		records[record] = true
	}
	a.listingWrapperRecords[wrapper] = records
	return records
}

func (a *analysis) descendantListingRecords(n *html.Node) (records []*html.Node) {
	var visit func(*html.Node)
	visit = func(parent *html.Node) {
		for ch := parent.FirstChild; ch != nil; ch = ch.NextSibling {
			if hardHidden(ch) || a.isIrrelevantNode(ch) {
				continue
			}
			if isListingRecordElement(ch) || a.inferenceListingRecord(ch) == ch {
				records = append(records, ch)
				continue
			}
			visit(ch)
		}
	}
	visit(n)
	return records
}
