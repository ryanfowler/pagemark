package markdown

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func convertHTML(t *testing.T, source string) Result {
	t.Helper()
	base, _ := url.Parse("https://example.com/base/")
	return convertHTMLConfig(t, source, Config{
		Base: base, Links: true, Images: true, Tables: true,
		MaxLinks: 100, MaxImages: 100, MaxTableCells: 100,
		Policy: URLPolicy{Schemes: []string{"https"}, MaxLength: 4096},
	})
}

func convertHTMLConfig(t *testing.T, source string, cfg Config) Result {
	t.Helper()
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	var body *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if body == nil && n.Type == html.ElementNode && n.Data == "body" {
			body = n
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(root)
	return Convert([]*html.Node{body}, cfg)
}

func TestMixedInlineContainerStaysInOneParagraph(t *testing.T) {
	r := convertHTML(t, `<div>Hello <span>wide <strong>world</strong></span>; see <a href="guide"><em>the guide</em></a>.</div>`)
	want := `Hello wide **world**; see [*the guide*](https://example.com/base/guide).`
	if r.Markdown != want {
		t.Fatalf("want %q, got %q", want, r.Markdown)
	}
}

func TestHardBreakAndLineStartEscaping(t *testing.T) {
	r := convertHTML(t, `<p>first<br><span>1</span><span>.  not a list</span><br># not a heading</p>`)
	want := "first\\\n1\\. not a list\\\n\\# not a heading"
	if r.Markdown != want {
		t.Fatalf("want %q, got %q", want, r.Markdown)
	}
}

func TestHardBreakInHeadingRendersAsSpace(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   string
	}{
		{`<h1>It's okay to be<br> a little jelly</h1>`, `# It's okay to be a little jelly`},
		{`<h2>Everything you need.<br><em>Nothing you don’t.</em></h2>`, `## Everything you need. *Nothing you don’t.*`},
		{`<h3><a href="/docs">Read<br>the docs</a></h3>`, `### [Read the docs](https://example.com/docs)`},
	} {
		r := convertHTML(t, tc.source)
		if r.Markdown != tc.want {
			t.Errorf("source %q: want %q, got %q", tc.source, tc.want, r.Markdown)
		}
	}
}

func TestUnsafeLinkIsRejectedAfterOutputLimit(t *testing.T) {
	base, _ := url.Parse("https://example.com/")
	r := convertHTMLConfig(t, `<p><a href="/safe">safe</a> <a href="javascript:alert(1)">unsafe</a></p>`, Config{
		Base: base, Links: true, MaxLinks: 1,
		Policy: URLPolicy{Schemes: []string{"https"}, MaxLength: 4096},
	})
	if r.Markdown != "[safe](https://example.com/safe) unsafe" {
		t.Fatal(r.Markdown)
	}
	if len(r.Rejected) != 1 || r.Rejected[0] != "javascript:alert(1)" {
		t.Fatalf("rejected links: %#v", r.Rejected)
	}
}

func TestFormattedLinkAndLinkedImage(t *testing.T) {
	r := convertHTML(t, `<p><a href="/docs"><strong>Read</strong> now</a> <a href="/"><img src="/logo.png" alt="Logo"></a></p>`)
	want := `[**Read** now](https://example.com/docs) [![Logo](https://example.com/logo.png)](https://example.com/)`
	if r.Markdown != want {
		t.Fatalf("want %q, got %q", want, r.Markdown)
	}
	if len(r.Links) != 2 || len(r.Images) != 1 {
		t.Fatalf("links=%v images=%v", r.Links, r.Images)
	}
}

func TestLinkWhitespaceIsOutsideLabel(t *testing.T) {
	r := convertHTML(t, `<p>See <a href="/docs">the guide </a>and <a href="/more"> <strong>more</strong> </a> now.</p>`)
	want := `See [the guide](https://example.com/docs) and [**more**](https://example.com/more) now.`
	if r.Markdown != want {
		t.Fatalf("want %q, got %q", want, r.Markdown)
	}
}

func TestLinkPreservesTrailingHardBreak(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   string
	}{
		{`<p><a href="/docs">guide<br></a>after</p>`, "[guide\\\n](https://example.com/docs)after"},
		{`<p><a href="/docs">guide<br> </a>after</p>`, "[guide\\\n](https://example.com/docs) after"},
	} {
		r := convertHTML(t, tc.source)
		if r.Markdown != tc.want {
			t.Errorf("source %q: want %q, got %q", tc.source, tc.want, r.Markdown)
		}
	}
}

func TestOrderedListAttributesAndIndentation(t *testing.T) {
	r := convertHTML(t, `<ol start="9"><li>nine</li><li>ten<ul><li>nested</li></ul></li></ol>`)
	want := "9. nine\n10. ten\n    - nested"
	if r.Markdown != want {
		t.Fatalf("want %q, got %q", want, r.Markdown)
	}

	r = convertHTML(t, `<ol reversed><li>three</li><li value="7">seven</li><li>six</li></ol>`)
	if r.Markdown != "- 3\\. three\n- 7\\. seven\n- 6\\. six" {
		t.Fatal(r.Markdown)
	}

	r = convertHTML(t, `<ol start="0"><li>zero</li><li>one</li></ol>`)
	if r.Markdown != "0. zero\n1. one" {
		t.Fatal(r.Markdown)
	}

	r = convertHTML(t, `<ol><li>one</li><li value="0">zero</li><li>one again</li></ol>`)
	if r.Markdown != "- 1\\. one\n- 0\\. zero\n- 1\\. one again" {
		t.Fatal(r.Markdown)
	}

	for _, source := range []string{
		`<ol start="-1"><li>negative</li></ol>`,
		`<ol start="1000000000"><li>ten digits</li></ol>`,
	} {
		if r = convertHTML(t, source); !strings.HasPrefix(r.Markdown, "- ") {
			t.Fatalf("expected literal ordinal fallback: %q", r.Markdown)
		}
	}
}

func TestTableHeaderCaptionAlignmentAndBreak(t *testing.T) {
	r := convertHTML(t, `<table><caption>Sizes</caption><tr><th align="right">Name</th><th style="text-align:center">Value</th></tr><tr><td>A<br>B</td><td>x|y</td></tr></table>`)
	want := "Sizes\n\n| Name | Value |\n| ---: | :---: |\n| A B | x\\|y |"
	if r.Markdown != want {
		t.Fatalf("want:\n%s\ngot:\n%s", want, r.Markdown)
	}
}

func TestTableWithoutHeaderOrWithSpansFallsBack(t *testing.T) {
	for _, source := range []string{
		`<table><tr><td>A</td><td>B</td></tr></table>`,
		`<table><tr><th>Q1</th><td>10</td></tr><tr><th>Q2</th><td>20</td></tr></table>`,
		`<table><tr><th colspan="2">Header</th></tr><tr><td>A</td><td>B</td></tr></table>`,
	} {
		r := convertHTML(t, source)
		if strings.Contains(r.Markdown, "| ---") || strings.TrimSpace(r.Text) == "" {
			t.Fatalf("unexpected table rendering: %q", r.Markdown)
		}
	}
}

func TestDefinitionListPreservesGroupsAndFormatting(t *testing.T) {
	r := convertHTML(t, `<dl><dt>One</dt><dd><em>First</em> <a href="/one">link</a></dd><dt>Two</dt><dd>Second</dd></dl>`)
	want := "- **One**: *First* [link](https://example.com/one)\n- **Two**: Second"
	if r.Markdown != want {
		t.Fatalf("want %q, got %q", want, r.Markdown)
	}
}

func TestUnknownContainerRetainsNestedBlocks(t *testing.T) {
	for _, source := range []string{
		`<custom-box>intro <p>first</p><p>second</p></custom-box>`,
		`<custom-box>intro <wrapper><p>first</p><p>second</p></wrapper></custom-box>`,
	} {
		r := convertHTML(t, source)
		if r.Markdown != "intro\n\nfirst\n\nsecond" {
			t.Fatalf("source %q rendered as %q", source, r.Markdown)
		}
	}
}

func TestWhitespaceOnlyAnchorKeepsWordBoundary(t *testing.T) {
	r := convertHTML(t, `<p>hello<a href="/"> </a>world</p>`)
	if r.Markdown != "hello world" || r.Text != "hello world" {
		t.Fatalf("markdown=%q text=%q", r.Markdown, r.Text)
	}
}

func TestCodeFenceInfoAndInlineCodePadding(t *testing.T) {
	r := convertHTML(t, `<pre><code class="language-go">fmt.Println("ok")
</code></pre><p><code> value </code></p>`)
	if !strings.Contains(r.Markdown, "```go\nfmt.Println") || !strings.Contains(r.Markdown, "`  value  `") {
		t.Fatal(r.Markdown)
	}
}

func TestOutputLimitFlattensContainerAtBlockBoundaries(t *testing.T) {
	root := `<main><p>first</p><p>` + strings.Repeat("long ", 20) + `</p></main>`
	r := convertHTML(t, root)
	// Re-run with a small limit to exercise flattening of the selected <body>.
	parsed, _ := html.Parse(strings.NewReader(root))
	var main *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "main" {
			main = n
			return
		}
		for ch := n.FirstChild; ch != nil && main == nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(parsed)
	limited := Convert([]*html.Node{main}, Config{MaxBytes: 20})
	if limited.Markdown != "first" || !limited.Truncated || r.Markdown == "" {
		t.Fatalf("markdown=%q truncated=%v", limited.Markdown, limited.Truncated)
	}
}
