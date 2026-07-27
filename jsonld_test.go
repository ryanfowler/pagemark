package pagemark

import "testing"

func jsonLDMetadata(raw string) metadata {
	var m metadata
	new(analysis).readJSONLD(raw, &m)
	return m
}

func TestJSONLDArticleTypeNormalization(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"exact", `{"@type":"Article"}`, true},
		{"full URL", `{"@type":"https://schema.org/NewsArticle"}`, true},
		{"HTTP URL", `{"@type":"http://schema.org/Article"}`, true},
		{"type array", `{"@type":["CreativeWork","NewsArticle"]}`, true},
		{"context string", `{"@context":"https://schema.org","@type":"Article"}`, true},
		{"context array", `{"@context":["https://schema.org",{"@language":"en"}],"@type":"Article"}`, true},
		{"vocab", `{"@context":{"@vocab":"https://schema.org/"},"@type":"Article"}`, true},
		{"supported prefix", `{"@context":{"schema":"https://schema.org/"},"@type":"schema:Article"}`, true},
		{"self-referential prefix", `{"@context":{"x":"x:"},"@type":"x:Article"}`, false},
		{"mutually recursive prefixes", `{"@context":{"x":"y:","y":"x:"},"@type":"x:Article"}`, false},
		{"non-HTTP absolute IRI", `{"@context":{"x":"urn:example:"},"@type":"x:Article"}`, false},
		{"unsupported prefix", `{"@type":"example:Article"}`, false},
		{"substring prefix", `{"@type":"NotAnArticle"}`, false},
		{"substring suffix", `{"@type":"ArticleList"}`, false},
		{"case sensitive", `{"@type":"article"}`, false},
		{"later vocab override", `{"@context":["https://schema.org",{"@vocab":"https://example.test/"}],"@type":"Article"}`, false},
		{"vocab null reset", `{"@context":["https://schema.org",{"@vocab":null}],"@type":"Article"}`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := jsonLDMetadata(test.raw).articleType; got != test.want {
				t.Fatalf("article evidence = %v, want %v", got, test.want)
			}
		})
	}
}

func TestJSONLDGraphScopingAndReferences(t *testing.T) {
	tests := []struct {
		name, raw, title, author string
		article                  bool
	}{
		{
			name:  "root graph article",
			raw:   `{"@context":"https://schema.org","@graph":[{"@id":"#story","@type":"Article","headline":"Graph story"}]}`,
			title: "Graph story", article: true,
		},
		{
			name:  "main entity id",
			raw:   `{"@context":"https://schema.org","@type":"WebPage","name":"Page name","mainEntity":{"@id":"#story"},"@graph":[{"@id":"#story","@type":"NewsArticle","author":{"name":"Ada"}}]}`,
			title: "Page name", author: "Ada", article: true,
		},
		{
			name:  "split id",
			raw:   `{"mainEntity":{"@id":"#story"},"@graph":[{"@id":"#story","@type":"Article"},{"@id":"#story","headline":"Split story"}]}`,
			title: "Split story", article: true,
		},
		{
			name:  "unrelated graph article",
			raw:   `{"@context":"https://schema.org","@graph":[{"@id":"#page","@type":"WebPage","name":"Primary page"},{"@id":"#card","@type":"Article","headline":"Unrelated card","author":{"name":"Wrong"}}]}`,
			title: "Primary page", article: false,
		},
		{
			name:  "article linked to another page",
			raw:   `{"@context":"https://schema.org","@graph":[{"@id":"#page","@type":"WebPage","name":"Primary page"},{"@id":"#card","@type":"Article","headline":"Other page article","mainEntityOfPage":{"@id":"#other-page"}}]}`,
			title: "Primary page", article: false,
		},
		{
			name:  "article linked to selected page",
			raw:   `{"@context":"https://schema.org","@graph":[{"@id":"#page","@type":"WebPage","name":"Primary page"},{"@id":"#story","@type":"Article","headline":"Primary story","mainEntityOfPage":{"@id":"#page"}}]}`,
			title: "Primary page", article: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := jsonLDMetadata(test.raw)
			if m.title != test.title || m.author != test.author || m.articleType != test.article {
				t.Fatalf("metadata = title %q author %q article %v", m.title, m.author, m.articleType)
			}
		})
	}
}

func TestJSONLDCycleAndSpecializedArticleClasses(t *testing.T) {
	m := jsonLDMetadata(`{"mainEntity":{"@id":"#a"},"@graph":[{"@id":"#a","@type":"Article","mainEntity":{"@id":"#b"}},{"@id":"#b","mainEntity":{"@id":"#a"}}]}`)
	if !m.articleType {
		t.Fatal("cyclic referenced Article was not recognized")
	}

	discussion := jsonLDMetadata(`{"@type":"DiscussionForumPosting"}`)
	if discussion.articleType || !discussion.schemaDiscussion {
		t.Fatalf("discussion classification = %#v", discussion)
	}
	documentation := jsonLDMetadata(`{"@type":"APIReference"}`)
	if documentation.articleType || !documentation.schemaDocumentation {
		t.Fatalf("documentation classification = %#v", documentation)
	}
}

func TestJSONLDSpecializedTypesInfluencePageType(t *testing.T) {
	tests := []struct {
		typeName string
		want     PageType
	}{
		{"DiscussionForumPosting", PageTypeDiscussion},
		{"APIReference", PageTypeDocumentation},
	}
	for _, test := range tests {
		t.Run(test.typeName, func(t *testing.T) {
			html := `<html><head><script type="application/ld+json">{"@context":"https://schema.org","@type":"` + test.typeName + `"}</script></head><body><main><h1>Page title</h1><p>Useful page content is available here.</p></main></body></html>`
			doc, err := ExtractBytes([]byte(html), "https://example.test/page")
			if err != nil {
				t.Fatal(err)
			}
			if doc.PageType != test.want {
				t.Fatalf("page type = %q, want %q", doc.PageType, test.want)
			}
		})
	}
}

func TestJSONLDMultiNodeContainersSelectRecognizedPageShapes(t *testing.T) {
	tests := []struct {
		name, structuredData string
		want                 PageType
	}{
		{
			name: "product array",
			structuredData: `[{"@context":"https://schema.org","@type":"Product","name":"Desk"},` +
				`{"@context":"https://schema.org","@type":"BreadcrumbList","itemListElement":[]}]`,
			want: PageTypeProduct,
		},
		{
			name: "service array",
			structuredData: `[{"@context":"https://schema.org","@type":"Service","name":"Delivery"},` +
				`{"@context":"https://schema.org","@type":"BreadcrumbList","itemListElement":[]}]`,
			want: PageTypeService,
		},
		{
			name: "split product array",
			structuredData: `[{"@context":"https://schema.org","@id":"#desk","@type":"Product"},` +
				`{"@id":"#desk","name":"Desk"},` +
				`{"@context":"https://schema.org","@type":"BreadcrumbList","itemListElement":[]}]`,
			want: PageTypeProduct,
		},
		{
			name: "product graph",
			structuredData: `{"@context":"https://schema.org","@graph":[{"@type":"Product","name":"Desk"},` +
				`{"@type":"BreadcrumbList","itemListElement":[]}]}`,
			want: PageTypeProduct,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			html := `<html><head><script type="application/ld+json">` + test.structuredData +
				`</script></head><body><main><h1>Page title</h1><p>Useful page content is available here.</p></main></body></html>`
			doc, err := ExtractBytes([]byte(html), "https://example.test/neutral")
			if err != nil {
				t.Fatal(err)
			}
			if doc.PageType != test.want {
				t.Fatalf("page type = %q, want %q", doc.PageType, test.want)
			}
		})
	}
}

func TestJSONLDProductAndServiceSubclassesInfluencePageType(t *testing.T) {
	tests := []struct {
		typeName string
		want     PageType
	}{
		{"ProductModel", PageTypeProduct},
		{"IndividualProduct", PageTypeProduct},
		{"FoodService", PageTypeService},
		{"TaxiService", PageTypeService},
	}
	for _, test := range tests {
		t.Run(test.typeName, func(t *testing.T) {
			html := `<html><head><script type="application/ld+json">{"@context":"https://schema.org","@type":"` + test.typeName + `"}</script></head><body><main><h1>Page title</h1><p>Useful page content is available here.</p></main></body></html>`
			doc, err := ExtractBytes([]byte(html), "https://example.test/neutral")
			if err != nil {
				t.Fatal(err)
			}
			if doc.PageType != test.want {
				t.Fatalf("page type = %q, want %q", doc.PageType, test.want)
			}
		})
	}
}

func TestArticlePublishedEvidenceDoesNotDependOnDatePriority(t *testing.T) {
	html := `<html><head>` +
		`<script type="application/ld+json">{"@context":"https://schema.org","@type":"WebPage","datePublished":"2024-01-01"}</script>` +
		`<meta property="article:published_time" content="2024-02-02">` +
		`</head><body><main><h1>Dated page</h1><p>Useful page content is available here.</p></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.test/neutral")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PublishedTime != "2024-01-01" {
		t.Fatalf("published time = %q, want higher-priority JSON-LD date", doc.PublishedTime)
	}
	if doc.PageType != PageTypeArticle {
		t.Fatalf("page type = %q, want article evidence", doc.PageType)
	}
}

func TestJSONLDScriptMIMEAndMalformedInput(t *testing.T) {
	html := `<html><head><script type="text/plain">{"@type":"Article","author":"Wrong"}</script><script type="application/ld+json">not json</script></head><body><main><h1>Ordinary page</h1><p>Ordinary content.</p></main></body></html>`
	doc, err := ExtractBytes([]byte(html), "https://example.test/page")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Author != "" || doc.PageType == PageTypeArticle {
		t.Fatalf("unexpected JSON-LD evidence: %#v", doc)
	}
}
