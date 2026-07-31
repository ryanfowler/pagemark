package engine

import (
	"strings"

	"golang.org/x/net/html"
)

const (
	listingShapeKnown uint8 = 1 << iota
	listingShapeMatched
	repeatedListingKnown
	repeatedListingMatched
)

const (
	inferenceTokensKnown uint16 = 1 << iota
	inferenceTokenCard
	inferenceTokenItem
	inferenceTokenRecord
	inferenceTokenListing
	inferenceTokenListings
	inferenceTokenResult
	inferenceTokenResults
	inferenceTokenProduct
	inferenceTokenPrice
	inferenceTokenSKU
	inferenceTokenDocs
	inferenceTokenDocumentation
	inferenceTokenAPI
	inferenceTokenReference
	inferenceTokenDoc
)

// inferenceTokenFlags tokenizes the classification vocabulary once per DOM
// element. Page-type and listing inference used to rescan the same id, class,
// and role values for several overlapping token sets for every descendant
// block.
func (a *analysis) inferenceTokenFlags(n *html.Node) uint16 {
	if n == nil || n.Type != html.ElementNode {
		return inferenceTokensKnown
	}
	if flags := a.nodeStates[n].inferenceTokens; flags != 0 {
		return flags
	}
	flags := inferenceTokensKnown
	for _, attr := range n.Attr {
		key := attr.Key
		tokenAttribute := key == "id" || key == "class" || key == "role"
		if !tokenAttribute {
			switch len(key) {
			case len("id"):
				tokenAttribute = strings.EqualFold(key, "id")
			case len("role"):
				tokenAttribute = strings.EqualFold(key, "role")
			case len("class"):
				tokenAttribute = strings.EqualFold(key, "class")
			}
			if !tokenAttribute {
				continue
			}
		}
		start := -1
		for end := 0; end <= len(attr.Val); end++ {
			if end < len(attr.Val) && attr.Val[end] < 0x80 && asciiAlnum[attr.Val[end]] {
				if start < 0 {
					start = end
				}
				continue
			}
			if end < len(attr.Val) && attr.Val[end] >= 0x80 {
				// Unicode identifiers are uncommon in structural attributes.
				// Preserve their case-folding behavior through the established
				// matcher while keeping parsed HTML on the single-pass path.
				for _, vocabulary := range inferenceVocabulary {
					if containsAnyFold(attr.Val, vocabulary.token) {
						flags |= vocabulary.flag
					}
				}
				start = -1
				break
			}
			if start >= 0 {
				flags |= inferenceTokenFlag(attr.Val[start:end])
				start = -1
			}
		}
	}
	state := a.nodeStates[n]
	state.inferenceTokens = flags
	a.nodeStates[n] = state
	return flags
}

var inferenceVocabulary = [...]struct {
	token string
	flag  uint16
}{
	{"card", inferenceTokenCard}, {"item", inferenceTokenItem}, {"record", inferenceTokenRecord},
	{"listing", inferenceTokenListing}, {"listings", inferenceTokenListings},
	{"result", inferenceTokenResult}, {"results", inferenceTokenResults},
	{"product", inferenceTokenProduct}, {"price", inferenceTokenPrice}, {"sku", inferenceTokenSKU},
	{"docs", inferenceTokenDocs}, {"documentation", inferenceTokenDocumentation},
	{"api", inferenceTokenAPI}, {"reference", inferenceTokenReference}, {"doc", inferenceTokenDoc},
}

func inferenceTokenFlag(token string) uint16 {
	switch len(token) {
	case 3:
		switch lowerASCII(token[0]) {
		case 'a':
			if equalFoldASCII(token, "api") {
				return inferenceTokenAPI
			}
		case 'd':
			if equalFoldASCII(token, "doc") {
				return inferenceTokenDoc
			}
		case 's':
			if equalFoldASCII(token, "sku") {
				return inferenceTokenSKU
			}
		}
	case 4:
		switch lowerASCII(token[0]) {
		case 'c':
			if equalFoldASCII(token, "card") {
				return inferenceTokenCard
			}
		case 'd':
			if equalFoldASCII(token, "docs") {
				return inferenceTokenDocs
			}
		case 'i':
			if equalFoldASCII(token, "item") {
				return inferenceTokenItem
			}
		}
	case 5:
		if lowerASCII(token[0]) == 'p' && equalFoldASCII(token, "price") {
			return inferenceTokenPrice
		}
	case 6:
		if lowerASCII(token[0]) == 'r' && equalFoldASCII(token, "record") {
			return inferenceTokenRecord
		}
		if lowerASCII(token[0]) == 'r' && equalFoldASCII(token, "result") {
			return inferenceTokenResult
		}
	case 7:
		if lowerASCII(token[0]) == 'l' && equalFoldASCII(token, "listing") {
			return inferenceTokenListing
		}
		if lowerASCII(token[0]) == 'p' && equalFoldASCII(token, "product") {
			return inferenceTokenProduct
		}
		if lowerASCII(token[0]) == 'r' && equalFoldASCII(token, "results") {
			return inferenceTokenResults
		}
	case 8:
		if lowerASCII(token[0]) == 'l' && equalFoldASCII(token, "listings") {
			return inferenceTokenListings
		}
	case 9:
		if lowerASCII(token[0]) == 'r' && equalFoldASCII(token, "reference") {
			return inferenceTokenReference
		}
	case 13:
		if lowerASCII(token[0]) == 'd' && equalFoldASCII(token, "documentation") {
			return inferenceTokenDocumentation
		}
	}
	return 0
}

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
		if a.repeatedUnmarkedListingRecord(p) {
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

func (a *analysis) repeatedUnmarkedListingRecord(n *html.Node) bool {
	if n == nil || n.Parent == nil || n.Type != html.ElementNode {
		return false
	}
	if state := a.listingStates[n]; state&repeatedListingKnown != 0 {
		return state&repeatedListingMatched != 0
	}
	tag := strings.ToLower(n.Data)
	matched := false
	if (tag == "div" || tag == "section") && !listingCohortFurniture(n) && a.unmarkedListingRecordShape(n) {
		matches := 0
		for sibling := n.Parent.FirstChild; sibling != nil; sibling = sibling.NextSibling {
			if sibling.Type == html.ElementNode && strings.EqualFold(sibling.Data, tag) &&
				!listingCohortFurniture(sibling) && a.unmarkedListingRecordShape(sibling) {
				matches++
				if matches >= 2 {
					matched = true
					break
				}
			}
		}
	}
	if a.listingStates == nil {
		a.listingStates = make(map[*html.Node]uint8)
	}
	state := a.listingStates[n] | repeatedListingKnown
	if matched {
		state |= repeatedListingMatched
	}
	a.listingStates[n] = state
	return matched
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
func (a *analysis) unmarkedListingRecordShape(n *html.Node) bool {
	if state := a.listingStates[n]; state&listingShapeKnown != 0 {
		return state&listingShapeMatched != 0
	}
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
	matched := headings == 1 && proseOrLink
	if a.listingStates == nil {
		a.listingStates = make(map[*html.Node]uint8)
	}
	state := a.listingStates[n] | listingShapeKnown
	if matched {
		state |= listingShapeMatched
	}
	a.listingStates[n] = state
	return matched
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
		flags := a.inferenceTokenFlags(p)
		if flags&(inferenceTokenCard|inferenceTokenItem|inferenceTokenRecord) != 0 {
			return p
		}
		wrapper := flags&(inferenceTokenListing|inferenceTokenListings|inferenceTokenResults) != 0
		if wrapper || flags&inferenceTokenResult != 0 {
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
