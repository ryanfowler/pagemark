package engine

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark/internal/urlutil"
	"golang.org/x/net/html"
)

var schemaArticleTypes = map[string]struct{}{
	"Article": {}, "AdvertiserContentArticle": {}, "NewsArticle": {},
	"AnalysisNewsArticle": {}, "AskPublicNewsArticle": {}, "BackgroundNewsArticle": {},
	"OpinionNewsArticle": {}, "ReportageNewsArticle": {}, "ReviewNewsArticle": {},
	"Report": {}, "SatiricalArticle": {}, "ScholarlyArticle": {},
	"MedicalScholarlyArticle": {}, "SocialMediaPosting": {}, "BlogPosting": {},
	"LiveBlogPosting": {}, "DiscussionForumPosting": {}, "TechArticle": {}, "APIReference": {},
}

// These recognized Article subclasses describe a different Pagemark page shape.
// Keep schema recognition separate from page-type evidence.
var schemaNonArticlePageTypes = map[string]PageType{
	"DiscussionForumPosting": PageTypeDiscussion,
	"APIReference":           PageTypeDocumentation,
}

var schemaPageTypes = map[string]PageType{
	"DiscussionForumPosting": PageTypeDiscussion,
	"Question":               PageTypeDiscussion, "QAPage": PageTypeDiscussion,
	"Product": PageTypeProduct, "IndividualProduct": PageTypeProduct,
	"ProductCollection": PageTypeProduct, "ProductGroup": PageTypeProduct,
	"ProductModel": PageTypeProduct, "SomeProducts": PageTypeProduct,
	"Vehicle":  PageTypeProduct,
	"ItemList": PageTypeListing, "SearchResultsPage": PageTypeListing,
	"Service": PageTypeService, "BroadcastService": PageTypeService,
	"CableOrSatelliteService": PageTypeService, "FinancialProduct": PageTypeService,
	"FoodService": PageTypeService, "GovernmentService": PageTypeService,
	"TaxiService": PageTypeService, "WebAPI": PageTypeService,
	"APIReference": PageTypeDocumentation,
}

type jsonLDContext struct {
	vocab    string
	vocabSet bool
	prefixes map[string]string
}

func (c jsonLDContext) with(raw any) jsonLDContext {
	n := jsonLDContext{vocab: c.vocab, vocabSet: c.vocabSet, prefixes: make(map[string]string, len(c.prefixes))}
	for k, v := range c.prefixes {
		n.prefixes[k] = v
	}
	var apply func(any)
	apply = func(value any) {
		switch value := value.(type) {
		case []any:
			for _, entry := range value {
				apply(entry)
			}
		case string:
			// Remote contexts are never fetched. Only Schema.org's canonical context
			// has a known vocabulary; another URL clears the implicit assumption.
			n.vocabSet = true
			if isSchemaVocab(value) {
				n.vocab = value
			} else {
				n.vocab = ""
			}
		case nil:
			n.vocab, n.vocabSet = "", true
		case map[string]any:
			if vocab, exists := value["@vocab"]; exists {
				n.vocabSet = true
				if s, ok := vocab.(string); ok {
					n.vocab = s
				} else {
					n.vocab = ""
				}
			}
			for term, definition := range value {
				if strings.HasPrefix(term, "@") {
					continue
				}
				switch definition := definition.(type) {
				case string:
					n.prefixes[term] = definition
				case map[string]any:
					if id, ok := definition["@id"].(string); ok {
						n.prefixes[term] = id
					}
				}
			}
		}
	}
	apply(raw)
	return n
}

func isSchemaVocab(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") &&
		(strings.EqualFold(u.Host, "schema.org") || strings.EqualFold(u.Host, "www.schema.org")) &&
		strings.Trim(u.Path, "/") == "" && u.RawQuery == "" && u.Fragment == ""
}

func schemaTypeName(raw string, context jsonLDContext) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}

	// Compact IRI definitions are untrusted and may be cyclic. Expand them
	// iteratively, rejecting repeated prefixes and excessively deep chains.
	seenPrefixes := map[string]bool{}
	for expansions := 0; expansions <= 16; expansions++ {
		if u, err := url.Parse(value); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			if !strings.EqualFold(u.Host, "schema.org") && !strings.EqualFold(u.Host, "www.schema.org") || u.RawQuery != "" {
				return "", false
			}
			name := strings.Trim(strings.TrimSpace(u.Path), "/")
			if u.Fragment != "" {
				if name != "" {
					return "", false
				}
				name = u.Fragment
			}
			return name, name != "" && !strings.ContainsAny(name, "/#")
		}
		if i := strings.IndexByte(value, ':'); i >= 0 {
			prefix := value[:i]
			base, ok := context.prefixes[prefix]
			if !ok || seenPrefixes[prefix] {
				return "", false
			}
			seenPrefixes[prefix] = true
			value = strings.TrimSpace(base) + value[i+1:]
			continue
		}
		if strings.ContainsAny(value, "/#") {
			return "", false
		}
		if context.vocabSet {
			if isSchemaVocab(context.vocab) {
				return value, true
			}
			return "", false
		}
		// Bare Schema.org names are retained for compatibility with the widespread
		// context-less form, but remain exact and case-sensitive.
		return value, true
	}
	return "", false
}

func schemaArticlePageType(raw string, context jsonLDContext) bool {
	name, schema := schemaTypeName(raw, context)
	if !schema {
		return false
	}
	_, article := schemaArticleTypes[name]
	_, specialized := schemaNonArticlePageTypes[name]
	return article && !specialized
}

func addSchemaEvidence(m *metadata, raw string, context jsonLDContext) {
	name, schema := schemaTypeName(raw, context)
	if !schema {
		return
	}
	m.schemaType = appendSchemaType(m.schemaType, name)
	if _, recognized := schemaArticleTypes[name]; recognized {
		if pageType, specialized := schemaNonArticlePageTypes[name]; specialized {
			if pageType == PageTypeDiscussion {
				m.schemaDiscussion = true
			}
			if pageType == PageTypeDocumentation {
				m.schemaDocumentation = true
			}
		} else {
			m.articleType = true
		}
	}
	switch schemaPageTypes[name] {
	case PageTypeDiscussion:
		m.schemaDiscussion = true
	case PageTypeDocumentation:
		m.schemaDocumentation = true
	case PageTypeProduct:
		m.schemaProduct = true
	case PageTypeListing:
		m.schemaListing = true
	case PageTypeService:
		m.schemaService = true
	}
}

func appendSchemaType(existing, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return existing
	}
	for _, current := range strings.Split(existing, " | ") {
		if strings.EqualFold(current, value) {
			return existing
		}
	}
	if existing == "" {
		return value
	}
	return existing + " | " + value
}

func plausibleMetadataDescription(description string) string {
	// Descriptions are summaries, not alternate article bodies. Some CMS themes
	// incorrectly copy the complete rendered body into description metadata,
	// which can add tens of kilobytes to an otherwise compact result.
	if utf8.RuneCountInString(description) > 1000 {
		return ""
	}
	switch normalizedLabel(description) {
	case "article", "other", "page", "post":
		return ""
	}
	return description
}

func (a *analysis) extractMetadata() {
	m := metadata{}
	microdataEntities, repeatedMicrodataArticles, microdataRecords, dominantMicrodata := a.pageMicrodataEntities(a.root)
	m.microdataListing = repeatedMicrodataArticles
	a.microdataArticleRecords = microdataRecords
	a.dominantMicrodataArticle = dominantMicrodata
	walk(a.root, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		tag := strings.ToLower(n.Data)
		if tag == "html" {
			m.language = attrValue(n, "lang")
		}
		if tag == "title" {
			value := normalizeText(nodeText(n))
			if m.browserTitle == "" {
				m.browserTitle = value
			}
			if value != "" && m.titlePriority < 1 {
				m.title, m.titlePriority = value, 1
			}
		} else if tag == "h1" && m.titlePriority < 1 {
			m.title = normalizeText(nodeText(n))
			m.titlePriority = 1
			m.titleFromHeading = m.title != ""
		}
		itemprop := strings.ToLower(attrValue(n, "itemprop"))
		itemtype := attrValue(n, "itemtype")
		pageEntity := microdataEntities[n]
		if itemtype != "" && pageEntity {
			for _, typ := range strings.Fields(itemtype) {
				addSchemaEvidence(&m, typ, jsonLDContext{})
			}
		}
		if containsAny(itemprop, "headline") && isPageMicrodataProperty(n, microdataEntities) {
			m.headline = true
			if m.title == "" {
				m.title = normalizeText(firstNonempty(attrValue(n, "content"), nodeText(n)))
			}
		}
		if itemprop == "name" && hasAncestorItemprop(n, "author") && m.authorPriority < 1 {
			if visible := normalizeText(nodeText(n)); visible != "" {
				m.author, m.authorPriority = visible, 1
			}
		}
		if (tag == "time" || itemprop == "datepublished") && m.published == "" {
			m.published = firstNonempty(attrValue(n, "datetime"), attrValue(n, "content"), normalizeText(nodeText(n)))
		}
		if tag == "meta" {
			key := strings.ToLower(firstNonempty(attrValue(n, "property"), attrValue(n, "name"), attrValue(n, "itemprop")))
			v := normalizeText(attrValue(n, "content"))
			switch key {
			case "description", "og:description", "twitter:description":
				priority := uint8(1)
				if key != "description" {
					priority = 3
				}
				if v = plausibleMetadataDescription(v); v != "" && priority > m.descriptionPriority {
					m.description, m.descriptionPriority = v, priority
				}
			case "author", "article:author":
				priority := uint8(1)
				if key == "article:author" {
					priority = 3
				}
				if v != "" && priority > m.authorPriority {
					m.author, m.authorPriority = v, priority
				}
			case "og:site_name":
				m.site = v
			case "article:published_time":
				if v != "" {
					m.articlePublished = true
					if m.publishedPriority < 3 {
						m.published, m.publishedPriority = v, 3
					}
				}
			case "datepublished":
				if v != "" && m.publishedPriority < 1 {
					m.published, m.publishedPriority = v, 1
				}
			case "og:title", "twitter:title":
				if m.socialTitle == "" {
					m.socialTitle = v
				}
				if v != "" && m.titlePriority < 3 {
					m.title, m.titlePriority = v, 3
				}
			case "og:type":
				m.schemaType = appendSchemaType(m.schemaType, v)
				// Open Graph's article token is case-insensitive, unlike Schema.org names.
				if strings.EqualFold(strings.TrimSpace(v), "article") {
					m.articleType = true
				}
			}
		}
		if tag == "link" && containsAny(strings.ToLower(attrValue(n, "rel")), "canonical") {
			m.canonical = a.resolveMetadataURL(attrValue(n, "href"))
		}
		if tag == "script" && isJSONLDMIME(attrValue(n, "type")) {
			a.readJSONLD(rawNodeText(n), &m)
		}
		return true
	})
	a.meta = m
}
func isJSONLDMIME(value string) bool {
	mediaType := strings.TrimSpace(strings.SplitN(value, ";", 2)[0])
	return strings.EqualFold(mediaType, "application/ld+json")
}

func (a *analysis) readJSONLD(raw string, m *metadata) {
	var document any
	if json.Unmarshal([]byte(raw), &document) != nil {
		return
	}

	// Build one deterministic @id index. JSON-LD permits an entity to be split
	// over node objects; complementary properties are merged without allowing a
	// later duplicate to replace an earlier explicit value.
	entities := map[string]map[string]any{}
	var index func(any)
	index = func(value any) {
		switch value := value.(type) {
		case []any:
			for _, child := range value {
				index(child)
			}
		case map[string]any:
			if id, ok := value["@id"].(string); ok && id != "" {
				if entity := entities[id]; entity == nil {
					entities[id] = value
				} else {
					for key, child := range value {
						if _, exists := entity[key]; !exists {
							entity[key] = child
						}
					}
				}
			}
			// Only indexing traverses arbitrary properties. Metadata traversal below
			// follows page entities and explicit relationships exclusively.
			for _, child := range value {
				index(child)
			}
		}
	}
	index(document)

	resolve := func(value any) any {
		if ref, ok := value.(map[string]any); ok {
			if id, ok := ref["@id"].(string); ok && entities[id] != nil {
				return entities[id]
			}
		}
		return value
	}
	typeNames := func(entity map[string]any, context jsonLDContext) []string {
		var raw []string
		switch value := entity["@type"].(type) {
		case string:
			raw = append(raw, value)
		case []any:
			for _, item := range value {
				if name, ok := item.(string); ok {
					raw = append(raw, name)
				}
			}
		}
		var names []string
		for _, value := range raw {
			if name, ok := schemaTypeName(value, context); ok {
				names = append(names, name)
			}
		}
		return names
	}
	isPageType := func(name string) bool {
		switch name {
		case "WebPage", "AboutPage", "CheckoutPage", "CollectionPage", "ContactPage",
			"FAQPage", "ItemPage", "MedicalWebPage", "ProfilePage", "QAPage",
			"RealEstateListing", "SearchResultsPage":
			return true
		}
		return false
	}

	active := map[string]bool{}
	var apply func(map[string]any, jsonLDContext, uint8)
	apply = func(entity map[string]any, inherited jsonLDContext, priority uint8) {
		context := inherited
		if rawContext, exists := entity["@context"]; exists {
			context = context.with(rawContext)
		}
		id, _ := entity["@id"].(string)
		if id != "" {
			if active[id] {
				return
			}
			active[id] = true
			defer delete(active, id)
		}
		article := false
		for _, name := range typeNames(entity, context) {
			addSchemaEvidence(m, name, jsonLDContext{})
			if _, ok := schemaArticleTypes[name]; ok {
				_, specialized := schemaNonArticlePageTypes[name]
				article = article || !specialized
			}
		}
		if author := resolve(entity["author"]); priority > m.authorPriority {
			var name string
			switch author := author.(type) {
			case string:
				name = normalizeText(author)
			case map[string]any:
				if value, ok := author["name"].(string); ok {
					name = normalizeText(value)
				}
			case []any:
				for _, item := range author {
					if person, ok := resolve(item).(map[string]any); ok {
						if value, ok := person["name"].(string); ok {
							name = normalizeText(value)
							break
						}
					}
				}
			}
			if name != "" {
				m.author, m.authorPriority = name, priority
			}
		}
		if value, ok := entity["datePublished"].(string); ok && value != "" {
			if article {
				m.articlePublished = true
			}
			if priority > m.publishedPriority {
				m.published, m.publishedPriority = value, priority
			}
		}
		if value, ok := entity["headline"].(string); ok {
			value = normalizeText(value)
			if value != "" {
				m.headline = true
				if priority > m.titlePriority {
					m.title, m.titlePriority = value, priority
				}
			}
		} else if value, ok := entity["name"].(string); ok && priority > m.titlePriority {
			if value = normalizeText(value); value != "" {
				m.title, m.titlePriority = value, priority
			}
		}
		if value, ok := entity["description"].(string); ok && priority > m.descriptionPriority {
			if value = plausibleMetadataDescription(normalizeText(value)); value != "" {
				m.description, m.descriptionPriority = value, priority
			}
		}
		if main, exists := entity["mainEntity"]; exists {
			switch main := main.(type) {
			case []any:
				for _, item := range main {
					if child, ok := resolve(item).(map[string]any); ok {
						apply(child, context, 4)
					}
				}
			default:
				if child, ok := resolve(main).(map[string]any); ok {
					apply(child, context, 4)
				}
			}
		}
	}

	// A graph is a container, not a declaration that all of its nodes describe
	// the page. Prefer an explicit page node, then a node tied to the page by
	// mainEntityOfPage, and accept an unambiguous single-node graph.
	normalizeID := func(id string) string {
		id = strings.TrimSpace(id)
		if id == "" || a.pageURL == nil {
			return id
		}
		if parsed, err := url.Parse(id); err == nil {
			return a.pageURL.ResolveReference(parsed).String()
		}
		return id
	}
	linkedToPage := func(node, page map[string]any) bool {
		var target string
		switch relation := node["mainEntityOfPage"].(type) {
		case string:
			target = relation
		case map[string]any:
			target, _ = relation["@id"].(string)
		}
		if target == "" {
			return false
		}
		target = normalizeID(target)
		if pageID, _ := page["@id"].(string); pageID != "" && target == normalizeID(pageID) {
			return true
		}
		return a.pageURL != nil && target == a.pageURL.String()
	}
	selectEntities := func(values []any, context jsonLDContext, allowRootEntity bool) []map[string]any {
		var nodes, pages, linked, typedRoots []map[string]any
		seenIDs := map[string]bool{}
		for _, value := range values {
			if node, ok := resolve(value).(map[string]any); ok {
				if rawID, _ := node["@id"].(string); rawID != "" {
					if id := normalizeID(rawID); id != "" {
						if seenIDs[id] {
							continue
						}
						seenIDs[id] = true
					}
				}
				nodes = append(nodes, node)
				childContext := context
				if rawContext, exists := node["@context"]; exists {
					childContext = childContext.with(rawContext)
				}
				for _, name := range typeNames(node, childContext) {
					if isPageType(name) {
						pages = append(pages, node)
						break
					}
					if _, article := schemaArticleTypes[name]; article {
						typedRoots = append(typedRoots, node)
						break
					}
					if _, recognizedPageShape := schemaPageTypes[name]; recognizedPageShape {
						typedRoots = append(typedRoots, node)
						break
					}
				}
				if _, exists := node["mainEntityOfPage"]; exists {
					linked = append(linked, node)
				}
			}
		}
		if len(pages) > 0 {
			selected := pages[:1]
			var matching []map[string]any
			for _, candidate := range linked {
				if linkedToPage(candidate, selected[0]) {
					matching = append(matching, candidate)
				}
			}
			if len(matching) == 1 {
				selected = append(selected, matching[0])
			}
			return selected
		}
		if len(linked) == 1 {
			return linked
		}
		if allowRootEntity && len(typedRoots) == 1 {
			return typedRoots
		}
		if len(nodes) == 1 {
			return nodes
		}
		return nil
	}

	base := jsonLDContext{}
	switch root := document.(type) {
	case []any:
		for i, entity := range selectEntities(root, base, true) {
			priority := uint8(5)
			if i > 0 {
				priority = 4
			}
			apply(entity, base, priority)
		}
	case map[string]any:
		context := base
		if rawContext, exists := root["@context"]; exists {
			context = context.with(rawContext)
		}
		_, hasType := root["@type"]
		_, hasMain := root["mainEntity"]
		_, hasHeadline := root["headline"]
		if hasType || hasMain || hasHeadline {
			apply(root, base, 5)
			return
		}
		if graph, ok := root["@graph"].([]any); ok {
			for i, entity := range selectEntities(graph, context, true) {
				priority := uint8(5)
				if i > 0 {
					priority = 4
				}
				apply(entity, context, priority)
			}
		}
	}
}
func (a *analysis) pageMicrodataEntities(root *html.Node) (map[*html.Node]bool, bool, map[*html.Node]bool, *html.Node) {
	entities := map[*html.Node]bool{}
	records := map[*html.Node]bool{}
	var articleEntities []*html.Node
	walk(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode || (!hasHTMLAttr(n, "itemscope") && attrValue(n, "itemtype") == "") {
			return true
		}
		// Nested scoped entities (authors, images, and card properties) are not
		// page-level metadata regardless of their surrounding content region.
		// Reject that common case before running the more expensive auxiliary
		// ancestry classifier.
		if !isPageMicrodataEntity(n) || a.inferenceAuxiliaryBlock(n) {
			return true
		}
		entities[n] = true
		itemtype := attrValue(n, "itemtype")
		for _, typ := range strings.Fields(itemtype) {
			if schemaArticlePageType(typ, jsonLDContext{}) {
				articleEntities = append(articleEntities, n)
				break
			}
		}
		return true
	})
	// Listing records are frequently wrapped individually (for example,
	// ul > li > article), so immediate parent equality is not meaningful. More
	// than one unnested article scope in the primary content region represents a
	// repeated set; only an explicitly designated mainEntity remains eligible
	// to supply page-level article metadata.
	if len(articleEntities) < 2 {
		return entities, false, records, nil
	}

	// A substantial primary article may have one or more sibling teaser cards.
	// Those cards are records from other pages, but they do not make this page a
	// listing. Prefer an explicit mainEntity; otherwise require exactly one
	// substantial non-record article and record-shaped remaining entities.
	var dominant *html.Node
	for _, entity := range articleEntities {
		if containsAny(strings.ToLower(attrValue(entity, "itemprop")), "mainentity") {
			if dominant != nil {
				dominant = nil
				break
			}
			dominant = entity
		}
	}
	if dominant == nil {
		for _, entity := range articleEntities {
			if !microdataRecordShape(entity) && a.substantialArticleScope(entity) {
				if dominant != nil {
					dominant = nil
					break
				}
				dominant = entity
			}
		}
	}
	if dominant != nil {
		onlyRecordsRemain := true
		for _, entity := range articleEntities {
			if entity != dominant && !microdataRecordShape(entity) {
				onlyRecordsRemain = false
				break
			}
		}
		if onlyRecordsRemain {
			for _, entity := range articleEntities {
				if entity != dominant {
					entities[entity] = false
					records[entity] = true
				}
			}
			return entities, false, records, dominant
		}
	}

	for _, entity := range articleEntities {
		if !containsAny(strings.ToLower(attrValue(entity, "itemprop")), "mainentity") {
			entities[entity] = false
			records[entity] = true
		}
	}
	return entities, true, records, nil
}

func microdataRecordShape(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		tag := strings.ToLower(p.Data)
		if tag == "aside" || tag == "li" || elementContainsAny(p, "card", "result", "item", "teaser", "archive") {
			return true
		}
		if p != n && (tag == "main" || tag == "article") {
			break
		}
	}
	return false
}

func (a *analysis) substantialArticleScope(n *html.Node) bool {
	if n == nil || a.nodeStates == nil {
		return substantialArticleScope(n)
	}
	if state := a.nodeStates[n].substantialArticle; state != 0 {
		return state == 2
	}
	result := substantialArticleScope(n)
	state := a.nodeStates[n]
	state.substantialArticle = 1
	if result {
		state.substantialArticle = 2
	}
	a.nodeStates[n] = state
	return result
}

func substantialArticleScope(n *html.Node) bool {
	paragraphs, chars := 0, 0
	walk(n, func(x *html.Node) bool {
		if x != n && x.Type == html.ElementNode && hasHTMLAttr(x, "itemscope") {
			return false
		}
		if x.Type == html.ElementNode && strings.EqualFold(x.Data, "p") {
			paragraphs++
			chars += utf8.RuneCountInString(normalizeText(nodeText(x)))
		}
		return true
	})
	return chars >= 120 || (paragraphs >= 2 && chars >= 80)
}

func isPageMicrodataEntity(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	// Records in a collection describe linked items, not the containing page.
	for p := n; p != nil; p = p.Parent {
		property := strings.ToLower(attrValue(p, "itemprop"))
		if containsAny(property, "itemlistelement", "recommendation", "recommendations") ||
			(p != n && containsAny(strings.ToLower(attrValue(p, "itemtype")), "itemlist")) {
			return false
		}
	}
	if containsAny(strings.ToLower(attrValue(n, "itemprop")), "mainentity") {
		return true
	}
	// A nested scoped entity is normally an author, image, card, or other
	// property of the outer page entity. It must not become page-level metadata.
	for p := n.Parent; p != nil; p = p.Parent {
		if hasHTMLAttr(p, "itemscope") || attrValue(p, "itemtype") != "" {
			return false
		}
	}
	return true
}

func isPageMicrodataProperty(n *html.Node, entities map[*html.Node]bool) bool {
	for p := n; p != nil; p = p.Parent {
		if hasHTMLAttr(p, "itemscope") || attrValue(p, "itemtype") != "" {
			return entities[p]
		}
	}
	return true
}

func hasHTMLAttr(n *html.Node, key string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return true
		}
	}
	return false
}

func hasAncestorItemprop(n *html.Node, value string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if containsAny(strings.ToLower(attrValue(p, "itemprop")), value) {
			return true
		}
	}
	return false
}
func (a *analysis) findBase() {
	found := false
	walk(a.root, func(n *html.Node) bool {
		if found {
			return false
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "base") && hasHTMLAttr(n, "href") {
			// HTML uses the first base element with an href. An invalid first
			// element must not allow a later element to replace the document URL.
			found = true
			if u, err := url.Parse(strings.TrimSpace(attrValue(n, "href"))); err == nil {
				if a.pageURL != nil {
					u = a.pageURL.ResolveReference(u)
				}
				if urlutil.IsHierarchicalHTTP(u) {
					a.base = u
				}
			}
			return false
		}
		return true
	})
}
func (a *analysis) resolveMetadataURL(s string) string {
	u, e := url.Parse(strings.TrimSpace(s))
	if e != nil {
		return ""
	}
	if a.base != nil {
		u = a.base.ResolveReference(u)
	}
	if !urlutil.IsHierarchicalHTTP(u) {
		return ""
	}
	u.User = nil
	return u.String()
}

func metadataNodes(m metadata) []*html.Node {
	var nodes []*html.Node
	if m.title != "" {
		n := &html.Node{Type: html.ElementNode, Data: "h1"}
		n.AppendChild(&html.Node{Type: html.TextNode, Data: m.title})
		nodes = append(nodes, n)
	}
	n := &html.Node{Type: html.ElementNode, Data: "p"}
	n.AppendChild(&html.Node{Type: html.TextNode, Data: m.description})
	return append(nodes, n)
}
