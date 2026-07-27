package pagemark

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

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
			if m.title == "" {
				m.title = value
			}
		} else if tag == "h1" && m.title == "" {
			m.title = normalizeText(nodeText(n))
			m.titleFromHeading = m.title != ""
		}
		itemprop := strings.ToLower(attrValue(n, "itemprop"))
		itemtype := strings.ToLower(attrValue(n, "itemtype"))
		pageEntity := microdataEntities[n]
		if itemtype != "" && pageEntity {
			m.schemaType = appendSchemaType(m.schemaType, itemtype)
		}
		if pageEntity && containsAny(itemtype, "article", "newsarticle", "blogposting") {
			m.articleType = true
		}
		if containsAny(itemprop, "headline") && isPageMicrodataProperty(n, microdataEntities) {
			m.headline = true
			if m.title == "" {
				m.title = normalizeText(firstNonempty(attrValue(n, "content"), nodeText(n)))
			}
		}
		if itemprop == "name" && hasAncestorItemprop(n, "author") {
			if visible := normalizeText(nodeText(n)); visible != "" {
				m.author = visible
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
				if key == "og:description" {
					priority = 2
				} else if key == "twitter:description" {
					priority = 3
				}
				if v = plausibleMetadataDescription(v); v != "" && priority > m.descriptionPriority {
					m.description, m.descriptionPriority = v, priority
				}
			case "author", "article:author":
				priority := uint8(1)
				if key == "article:author" {
					priority = 2
				}
				if v != "" && priority > m.authorPriority {
					m.author, m.authorPriority = v, priority
				}
			case "og:site_name":
				m.site = v
			case "article:published_time":
				if v != "" {
					m.published = v
					m.articlePublished = true
				}
			case "datepublished":
				if m.published == "" {
					m.published = v
				}
			case "og:title", "twitter:title":
				if m.socialTitle == "" {
					m.socialTitle = v
				}
				if m.title == "" {
					m.title = v
				}
			case "og:type":
				m.schemaType = appendSchemaType(m.schemaType, v)
				if strings.EqualFold(v, "article") || strings.Contains(strings.ToLower(v), "article") {
					m.articleType = true
				}
			}
		}
		if tag == "link" && containsAny(strings.ToLower(attrValue(n, "rel")), "canonical") {
			m.canonical = a.resolveMetadataURL(attrValue(n, "href"))
		}
		if tag == "script" && strings.Contains(strings.ToLower(attrValue(n, "type")), "ld+json") {
			a.readJSONLD(rawNodeText(n), &m)
		}
		return true
	})
	a.meta = m
}
func (a *analysis) readJSONLD(raw string, m *metadata) {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return
	}

	// Resolve @id references used by a page entity's mainEntity without treating
	// every sibling in @graph as page-level metadata.
	entities := map[string]map[string]any{}
	var index func(any)
	index = func(x any) {
		switch z := x.(type) {
		case []any:
			for _, q := range z {
				index(q)
			}
		case map[string]any:
			if id, ok := z["@id"].(string); ok && id != "" {
				// JSON-LD permits one entity to be split across several node objects.
				// Merge complementary properties so resolution is independent of the
				// order of full entities, partial entities, and @id-only references.
				if existing := entities[id]; existing == nil {
					entities[id] = z
				} else {
					for key, value := range z {
						if _, exists := existing[key]; !exists {
							existing[key] = value
						}
					}
				}
			}
			for _, q := range z {
				index(q)
			}
		}
	}
	index(v)

	activeIDs := map[string]bool{}
	var visit func(any, bool)
	visit = func(x any, pageEntity bool) {
		switch z := x.(type) {
		case []any:
			for _, q := range z {
				visit(q, pageEntity)
			}
		case map[string]any:
			var typeNames []string
			switch types := z["@type"].(type) {
			case string:
				typeNames = append(typeNames, types)
			case []any:
				for _, value := range types {
					if name, ok := value.(string); ok {
						typeNames = append(typeNames, name)
					}
				}
			}
			articleType := false
			for _, typeName := range typeNames {
				if strings.Contains(strings.ToLower(typeName), "article") || strings.EqualFold(typeName, "BlogPosting") {
					articleType = true
				}
			}
			if pageEntity {
				for _, typeName := range typeNames {
					m.schemaType = appendSchemaType(m.schemaType, typeName)
				}
			}
			if pageEntity && articleType {
				m.articleType = true
			}
			if m.authorPriority < 2 && pageEntity {
				author := ""
				switch au := z["author"].(type) {
				case string:
					author = normalizeText(au)
				case map[string]any:
					if s, ok := au["name"].(string); ok {
						author = normalizeText(s)
					}
				}
				if author != "" {
					m.author, m.authorPriority = author, 2
				}
			}
			if s, ok := z["datePublished"].(string); ok && (m.published == "" || (pageEntity && articleType)) {
				m.published = s
				if pageEntity && articleType {
					m.articlePublished = true
				}
			}
			if s, ok := z["headline"].(string); pageEntity && ok && normalizeText(s) != "" {
				m.headline = true
				if m.title == "" {
					m.title = normalizeText(s)
				}
			}
			if m.descriptionPriority < 2 && pageEntity {
				if s, ok := z["description"].(string); ok {
					if description := plausibleMetadataDescription(normalizeText(s)); description != "" {
						m.description, m.descriptionPriority = description, 2
					}
				}
			}
			for key, q := range z {
				if key == "@graph" {
					visit(q, false)
					continue
				}
				mainEntity := strings.EqualFold(key, "mainEntity")
				if mainEntity {
					if ref, ok := q.(map[string]any); ok {
						id, hasID := ref["@id"].(string)
						currentID, _ := z["@id"].(string)
						if hasID && entities[id] != nil && id != currentID {
							if !activeIDs[id] {
								activeIDs[id] = true
								visit(entities[id], true)
								delete(activeIDs, id)
							}
							continue
						}
					}
				}
				visit(q, mainEntity)
			}
		}
	}
	visit(v, true)
}
func (a *analysis) pageMicrodataEntities(root *html.Node) (map[*html.Node]bool, bool, map[*html.Node]bool, *html.Node) {
	entities := map[*html.Node]bool{}
	records := map[*html.Node]bool{}
	var articleEntities []*html.Node
	walk(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode || (!hasHTMLAttr(n, "itemscope") && attrValue(n, "itemtype") == "") {
			return true
		}
		if a.inferenceAuxiliaryBlock(n) || !isPageMicrodataEntity(n) {
			return true
		}
		entities[n] = true
		itemtype := strings.ToLower(attrValue(n, "itemtype"))
		if containsAny(itemtype, "article", "newsarticle", "blogposting") {
			articleEntities = append(articleEntities, n)
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
	walk(a.root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "base") {
			if u, err := url.Parse(attrValue(n, "href")); err == nil {
				if a.pageURL != nil {
					u = a.pageURL.ResolveReference(u)
				}
				if u.Scheme == "http" || u.Scheme == "https" {
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
	if u.Scheme != "http" && u.Scheme != "https" {
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
