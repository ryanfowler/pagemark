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

func TestEmptyHeadingsArePrunedAfterConversion(t *testing.T) {
	for _, tc := range []struct {
		name, source, want string
	}{
		{"empty section", `<section><h2>Web mentions</h2></section>`, ""},
		{"empty dynamic container", `<section><h2>Web mentions</h2><div id="webmentions"></div></section>`, ""},
		{"paragraph sibling", `<h2>Installation</h2><p>Run the installer.</p>`, "## Installation\n\nRun the installer."},
		{"nested content", `<h2>Installation</h2><div><p>Run the installer.</p></div>`, "## Installation\n\nRun the installer."},
		{"direct section text", `<section><h2>Introduction</h2>Useful introductory text.</section>`, "## Introduction\n\nUseful introductory text."},
		{"nested semantic section", `<section><h2>Guide</h2><section><h3>Install</h3><p>Run the installer.</p></section></section>`, "## Guide\n\n### Install\n\nRun the installer."},
		{"content after nested section", `<section><h2>Guide</h2><section><h3>Install</h3><p>Run the installer.</p></section><p>Troubleshoot failed installations.</p></section>`, "## Guide\n\n### Install\n\nRun the installer.\n\nTroubleshoot failed installations."},
		{"empty nested section does not borrow parent content", `<section><h2>Guide</h2><section><h3>Empty</h3></section><p>Read the overview.</p></section>`, "## Guide\n\nRead the overview."},
		{"consecutive headings", `<h2>Empty</h2><h2>Installation</h2><p>Run the installer.</p>`, "## Installation\n\nRun the installer."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, _ := url.Parse("https://example.com/")
			r := convertHTMLConfig(t, tc.source, Config{Base: base, PruneEmptyHeadings: true})
			if r.Markdown != tc.want {
				t.Fatalf("want %q, got %q", tc.want, r.Markdown)
			}
			for _, section := range r.Sections {
				if section.Heading == "Empty" || section.Heading == "Web mentions" {
					t.Fatalf("pruned heading survived in sections: %#v", r.Sections)
				}
			}
		})
	}
}

func TestSeparatelySelectedSectionHeadingDoesNotBorrowLaterContent(t *testing.T) {
	root, err := html.Parse(strings.NewReader(`<section><h2>Web mentions</h2><div id="webmentions"></div></section><p>Outside content.</p>`))
	if err != nil {
		t.Fatal(err)
	}
	var heading, paragraph *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "h2" {
			heading = n
		}
		if n.Type == html.ElementNode && n.Data == "p" {
			paragraph = n
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(root)
	r := Convert([]*html.Node{heading, paragraph}, Config{PruneEmptyHeadings: true})
	if r.Markdown != "Outside content." {
		t.Fatalf("section heading borrowed outside content: %q", r.Markdown)
	}
}

func TestHeadingPermalinks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{"hash permalink", `<h2 id="conclusion">Conclusion <a href="#conclusion">#</a></h2>`, `## Conclusion`},
		{"pilcrow permalink", `<h2 id="conclusion"><a href="#conclusion">¶</a> Conclusion</h2>`, `## Conclusion`},
		{"icon-only fragment link", `<h2 id="conclusion">Conclusion <a href="#conclusion"><svg aria-hidden="true"><path></path></svg><span class="sr-only">Permalink</span></a></h2>`, `## Conclusion`},
		{"absolute same-page fragment link", `<h2 id="conclusion">Conclusion <a href="https://example.com/base/#conclusion">§</a></h2>`, `## Conclusion`},
		{"meaningful heading link", `<h2><a href="/guide">Installation guide</a></h2>`, `## [Installation guide](https://example.com/guide)`},
		{"external heading link", `<h2>Read <a href="https://other.example/docs">the documentation</a></h2>`, `## Read [the documentation](https://other.example/docs)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := convertHTML(t, tc.source).Markdown; got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
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

func TestSerializedMediaTextIsNotEmittedAsProse(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{
			"entity encoded image in noscript",
			`<p>Before.</p><noscript>&lt;img src=&quot;/fallback.png&quot; alt=&quot;Fallback&quot; width=&quot;800&quot;&gt;</noscript><p>After.</p>`,
			"Before.\n\nAfter.",
		},
		{
			"double encoded image fallback",
			`<noscript>&amp;lt;img src=&amp;quot;/fallback.png&amp;quot; alt=&amp;quot;Fallback&amp;quot; /&amp;gt;</noscript>`,
			"",
		},
		{
			"standalone image example is preserved",
			`<p>&lt;img src="example.png"&gt;</p>`,
			`\<img src="example.png"\>`,
		},
		{
			"serialized iframe in noscript",
			`<noscript>&lt;iframe width=&quot;560&quot; src=&quot;https://www.youtube.com/embed/video&quot; title=&quot;Video&quot; allowfullscreen&gt;&lt;/iframe&gt;</noscript>`,
			"",
		},
		{
			"standalone iframe example is preserved",
			`<p>&lt;iframe src="https://video.example/embed/demo" title="Embed example"&gt;&lt;/iframe&gt;</p>`,
			`\<iframe src="https://video.example/embed/demo" title="Embed example"\>\</iframe\>`,
		},
		{
			"real iframe and matching serialized fallback",
			`<div><iframe src="https://video.example/embed/demo"></iframe>&lt;iframe src="https://video.example/embed/demo"&gt;&lt;/iframe&gt;</div>`,
			"",
		},
		{
			"real image and matching serialized fallback",
			`<figure><img src="/photo.png" alt="A useful photo">&lt;div class=&quot;fallback&quot;&gt;&lt;img src=&quot;/photo.png&quot; alt=&quot;A useful photo&quot;&gt;&lt;/div&gt;</figure>`,
			`![A useful photo](https://example.com/photo.png)`,
		},
		{
			"matching image elsewhere in article is not nearby",
			`<article><img src="/example.png" alt="Actual image"><p>Discussion of the markup follows.</p>&lt;img src=&quot;/example.png&quot;&gt;</article>`,
			"![Actual image](https://example.com/example.png)\n\nDiscussion of the markup follows.\n\n\\<img src=\"/example.png\"\\>",
		},
		{
			"preformatted example",
			`<pre>&lt;img src="example.png" alt="Example"&gt;</pre>`,
			"```\n<img src=\"example.png\" alt=\"Example\">\n```",
		},
		{
			"inline code example",
			`<p><code>&lt;img src="example.png"&gt;</code></p>`,
			"`<img src=\"example.png\">`",
		},
		{
			"ordinary prose mentioning tag",
			`<p>Use the &lt;img&gt; tag, with useful alternative text.</p>`,
			`Use the \<img\> tag, with useful alternative text.`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := convertHTML(t, tc.source).Markdown; got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestAccessibleSVGTextFallback(t *testing.T) {
	t.Run("labeled and escaped", func(t *testing.T) {
		r := convertHTML(t, `<svg role="img" aria-label="System *flow* [v2]"><path></path></svg>`)
		want := `Diagram: System \*flow\* \[v2\]`
		if r.Markdown != want {
			t.Fatalf("want %q, got %q", want, r.Markdown)
		}
		if len(r.Images) != 0 {
			t.Fatalf("URL-less SVG unexpectedly reported as an image: %#v", r.Images)
		}
	})

	t.Run("decorative hidden and disabled", func(t *testing.T) {
		if got := convertHTML(t, `<p>before<svg role="img"><path></path></svg>after</p>`).Markdown; got != "beforeafter" {
			t.Fatalf("unlabeled SVG output = %q", got)
		}
		if got := convertHTML(t, `<p>before<svg role="img" aria-label="Modal diagram" aria-modal="true"></svg>after</p>`).Markdown; got != "beforeafter" {
			t.Fatalf("aria-modal SVG output = %q", got)
		}
		cfg := Config{Images: false, MaxImages: 100, Policy: URLPolicy{Schemes: []string{"https"}, MaxLength: 4096}}
		if got := convertHTMLConfig(t, `<svg role="img" aria-label="Hidden diagram"></svg>`, cfg).Markdown; got != "" {
			t.Fatalf("disabled SVG output = %q", got)
		}
	})

	t.Run("shares image limit", func(t *testing.T) {
		base, _ := url.Parse("https://example.com/")
		cfg := Config{Base: base, Images: true, MaxImages: 1, Policy: URLPolicy{Schemes: []string{"https"}, MaxLength: 4096}}
		r := convertHTMLConfig(t, `<p><svg role="img" aria-label="First diagram"></svg><img src="/second.png" alt="Second diagram"></p>`, cfg)
		if r.Markdown != "Diagram: First diagram" || len(r.Images) != 0 {
			t.Fatalf("SVG did not consume image limit: markdown=%q images=%#v", r.Markdown, r.Images)
		}
		cfg.MaxImages = 0
		if got := convertHTMLConfig(t, `<svg role="img" aria-label="Limited diagram"></svg>`, cfg).Markdown; got != "" {
			t.Fatalf("MaxImages=0 SVG output = %q", got)
		}
	})
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

func TestPublishingTablePreservesColumnsAndEmptyCells(t *testing.T) {
	source := `<table><tbody>` +
		`<tr><td colspan="5"><b>Impact of AI Data Centers on Education Spending</b></p><p><i>Select Northern Virginia counties</i></p></td></tr>` +
		`<tr><td></td><td><b>Loudoun County, VA</b></td><td><b>Prince William County, VA</b></td><td><b>Fairfax County, VA</b></td><td><b>Stafford County, VA</b></td></tr>` +
		`<tr><td><b>Number of data centers</b></td><td><span>176</span></p><p>(most in the U.S.)</p></td><td>77</td><td>45</td><td>1</td></tr>` +
		`<tr><td><b>Increase in personal property tax revenue</b></td><td>639%</td><td>349%</td><td>91%</td><td></td></tr>` +
		`</tbody></table>`
	r := convertHTML(t, source)
	want := "**Impact of AI Data Centers on Education Spending** *Select Northern Virginia counties*\n\n" +
		"|  | **Loudoun County, VA** | **Prince William County, VA** | **Fairfax County, VA** | **Stafford County, VA** |\n" +
		"| --- | --- | --- | --- | --- |\n" +
		"| **Number of data centers** | 176 (most in the U.S.) | 77 | 45 | 1 |\n" +
		"| **Increase in personal property tax revenue** | 639% | 349% | 91% |  |"
	if r.Markdown != want {
		t.Fatalf("want:\n%s\ngot:\n%s", want, r.Markdown)
	}
}

func TestNativeCaptionAndPromotedTitleHaveBoundary(t *testing.T) {
	source := `<table><caption>Official caption</caption>` +
		`<tr><td colspan="2"><strong>Report title</strong></td></tr>` +
		`<tr><td></td><td><strong>Value</strong></td></tr>` +
		`<tr><td>Count</td><td>10</td></tr></table>`
	r := convertHTML(t, source)
	want := "Official caption **Report title**\n\n|  | **Value** |\n| --- | --- |\n| Count | 10 |"
	if r.Markdown != want || !strings.Contains(r.Text, "Official caption Report title") {
		t.Fatalf("want %q, got markdown=%q text=%q", want, r.Markdown, r.Text)
	}
}

func TestARIATableAndGridPreserveCells(t *testing.T) {
	t.Run("table with headers row headers multiline and link", func(t *testing.T) {
		source := `<div role="table">` +
			`<div role="row"><span role="columnheader">County</span><span role="columnheader" style="text-align:right">Value</span></div>` +
			`<div role="row"><span role="rowheader"><strong>Loudoun</strong></span><span role="cell"><span>176</span><p><a href="/note">most in U.S.</a></p></span></div>` +
			`<div role="row"><span role="rowheader">Stafford</span><span role="cell"></span></div>` +
			`</div>`
		want := "| County | Value |\n| --- | ---: |\n| **Loudoun** | 176 [most in U.S.](https://example.com/note) |\n| Stafford |  |"
		if got := convertHTML(t, source).Markdown; got != want {
			t.Fatalf("want:\n%s\ngot:\n%s", want, got)
		}
	})

	t.Run("gridcell", func(t *testing.T) {
		want := "| Name | Score |\n| --- | --- |\n| A | 10 |"
		for _, source := range []string{
			`<div role="grid"><div role="row"><span role="gridcell">Name</span><span role="gridcell">Score</span></div><div role="row"><span role="gridcell">A</span><span role="gridcell">10</span></div></div>`,
			`<table role="grid"><tr><td role="gridcell">Name</td><td role="gridcell">Score</td></tr><tr><td role="gridcell">A</td><td role="gridcell">10</td></tr></table>`,
		} {
			if got := convertHTML(t, source).Markdown; got != want {
				t.Fatalf("source %q: want %q, got %q", source, want, got)
			}
		}
	})
}

func TestNestedTableCannotExceedCellBudget(t *testing.T) {
	source := `<table><tr><th>A</th><th>B</th></tr><tr><td>outer<table><tr><th>X</th><th>Y</th></tr><tr><td>1</td><td>2</td></tr></table></td><td>end</td></tr></table>`
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	var outer *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if outer == nil && n.Type == html.ElementNode && n.Data == "table" {
			outer = n
			return
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			find(ch)
		}
	}
	find(root)
	c := &converter{cfg: Config{Tables: true, MaxTableCells: 6}}
	if got := c.table(outer); got == nil {
		t.Fatal("outer table was not converted")
	}
	if c.cells != 4 || c.cells > c.cfg.MaxTableCells {
		t.Fatalf("nested table bypassed cell budget: cells=%d limit=%d", c.cells, c.cfg.MaxTableCells)
	}
}

func TestTableMetadataConsumesMediaLimitsBeforeBody(t *testing.T) {
	base, _ := url.Parse("https://example.com/")
	cfg := Config{
		Base: base, Links: true, Images: true, Tables: true,
		MaxLinks: 1, MaxImages: 1, MaxTableCells: 100,
		Policy: URLPolicy{Schemes: []string{"https"}, MaxLength: 4096},
	}

	t.Run("caption before body and fallback", func(t *testing.T) {
		for _, source := range []string{
			`<table><caption><a href="/caption">Caption</a></caption><tr><th>Head</th></tr><tr><td><a href="/body">Body</a></td></tr></table>`,
			`<table><caption><a href="/caption">Caption</a></caption><tr><td><a href="/body">Body</a></td></tr></table>`,
		} {
			got := convertHTMLConfig(t, source, cfg).Markdown
			if !strings.Contains(got, `[Caption](https://example.com/caption)`) || strings.Contains(got, `[Body](`) {
				t.Fatalf("caption did not receive the first link budget slot: %q", got)
			}
		}
	})

	t.Run("promoted title before body", func(t *testing.T) {
		source := `<table><tr><td colspan="2"><a href="/title">Title</a> <img src="/title.png" alt="Title image"></td></tr>` +
			`<tr><td></td><td><b>Value</b></td></tr><tr><td>Row</td><td><a href="/body">Body</a><img src="/body.png" alt="Body image"></td></tr></table>`
		got := convertHTMLConfig(t, source, cfg).Markdown
		if !strings.Contains(got, `[Title](https://example.com/title)`) || !strings.Contains(got, `![Title image](https://example.com/title.png)`) ||
			strings.Contains(got, `[Body](`) || strings.Contains(got, `![Body image](`) {
			t.Fatalf("title did not receive the first media budget slots: %q", got)
		}
	})
}

func TestUnsafeTableShapesFallBackWithoutBecomingTables(t *testing.T) {
	t.Run("unequal native rows", func(t *testing.T) {
		got := convertHTML(t, `<table><tr><th>A</th><th>B</th></tr><tr><td>only A</td></tr></table>`)
		if strings.Contains(got.Markdown, "| ---") || !strings.Contains(got.Text, "only A") {
			t.Fatalf("unexpected conversion: %q", got.Markdown)
		}
	})

	t.Run("unequal ARIA rows", func(t *testing.T) {
		got := convertHTML(t, `<div role="table"><div role="row"><span role="columnheader">A</span><span role="columnheader">B</span></div><div role="row"><span role="cell">only A</span></div></div>`)
		if strings.Contains(got.Markdown, "| ---") || !strings.Contains(got.Text, "only A") {
			t.Fatalf("unexpected conversion: %q", got.Markdown)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		base, _ := url.Parse("https://example.com/")
		cfg := Config{Base: base, Tables: true, MaxTableCells: 3}
		got := convertHTMLConfig(t, `<div role="table"><div role="row"><span role="columnheader">A</span><span role="columnheader">B</span></div><div role="row"><span role="cell">1</span><span role="cell">2</span></div></div>`, cfg)
		if strings.Contains(got.Markdown, "| ---") || !strings.Contains(got.Text, "1") {
			t.Fatalf("table limit was not respected: %q", got.Markdown)
		}
	})

	t.Run("ordinary card grid", func(t *testing.T) {
		got := convertHTML(t, `<div class="grid"><div class="card"><h3>Alpha</h3><p>First</p></div><div class="card"><h3>Beta</h3><p>Second</p></div></div>`)
		if strings.Contains(got.Markdown, "| ---") || !strings.Contains(got.Markdown, "### Alpha") || !strings.Contains(got.Markdown, "### Beta") {
			t.Fatalf("card grid became a table: %q", got.Markdown)
		}
	})
}

func TestHiddenResponsiveTableDuplicateIsIgnored(t *testing.T) {
	table := `<div role="table"><div role="row"><span role="columnheader">Name</span><span role="columnheader">Value</span></div><div role="row"><span role="cell">A</span><span role="cell">1</span></div></div>`
	r := convertHTML(t, table+`<div aria-hidden="true">`+table+`</div>`)
	if strings.Count(r.Markdown, "| --- | --- |") != 1 {
		t.Fatalf("responsive duplicate was emitted: %q", r.Markdown)
	}
}

func TestBlockCodeInTableCellBecomesInlineCode(t *testing.T) {
	r := convertHTML(t, `<table><tr><th>Expression</th></tr><tr><td><pre><code>x &lt; y
and y &gt; 0</code></pre></td></tr></table>`)
	want := "| Expression |\n| --- |\n| `x < y and y > 0` |"
	if r.Markdown != want || !strings.Contains(r.Text, "x < y and y > 0") {
		t.Fatalf("want %q, got markdown=%q text=%q", want, r.Markdown, r.Text)
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

func TestFallbackTableIgnoresContentAriaLevels(t *testing.T) {
	r := convertHTML(t, `<table><tr><td><h2 aria-level="2">A</h2></td></tr><tr><td><h3 aria-level="3">B</h3></td></tr></table>`)
	want := "- ## A\n- ### B"
	if r.Markdown != want {
		t.Fatalf("content aria-level changed row hierarchy: want %q, got %q", want, r.Markdown)
	}
}

func TestNestedTableWrapperRetainsOwnImage(t *testing.T) {
	r := convertHTML(t, `<table><tr><td><img src="/badge.png" alt="Badge"><table><tr><td>Nested row</td></tr></table></td></tr></table>`)
	want := "- ![Badge](https://example.com/badge.png)\n  - Nested row"
	if r.Markdown != want {
		t.Fatalf("wrapper image was not retained: want %q, got %q", want, r.Markdown)
	}
}

func TestNestedLayoutTablePreservesRecordsAndHierarchy(t *testing.T) {
	source := `<table><tr><td><table class="records">` +
		`<tr><td><table><tr><td indent="0"></td><td><div><a href="/user/a">alice</a> <time>1 hour ago</time></div><div><p>First post.</p><p>More detail.</p></div></td></tr></table></td></tr>` +
		`<tr><td><table><tr><td indent="1"></td><td><div><a href="/user/b">bob</a> <time>30 minutes ago</time></div><div><p>A reply.</p></div></td></tr></table></td></tr>` +
		`<tr><td><table><tr><td indent="0"></td><td><div><a href="/user/c">carol</a> <time>now</time></div><div><p>Another root.</p></div></td></tr></table></td></tr>` +
		`</table></td></tr></table>`
	r := convertHTML(t, source)
	want := "- [alice](https://example.com/user/a) 1 hour ago\n  First post.\n  More detail.\n  - [bob](https://example.com/user/b) 30 minutes ago\n    A reply.\n- [carol](https://example.com/user/c) now\n  Another root."
	if r.Markdown != want {
		t.Fatalf("want:\n%s\ngot:\n%s", want, r.Markdown)
	}
	if strings.Count(r.Markdown, "\n- ")+strings.Count(r.Markdown, "\n  - ")+1 != 3 {
		t.Fatalf("records were flattened: %q", r.Markdown)
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

func TestAdjacentLayoutItemsKeepSemanticBoundaries(t *testing.T) {
	r := convertHTML(t, `<div role="row"><span role="columnheader">MODEL</span><span role="columnheader">CREATOR</span><span role="columnheader">CONTEXT SIZE</span><span role="columnheader">TYPE</span></div>`)
	want := "MODEL CREATOR CONTEXT SIZE TYPE"
	if r.Markdown != want || r.Text != want {
		t.Fatalf("markdown=%q text=%q", r.Markdown, r.Text)
	}
}

func TestAdjacentStyledElementsDoNotInventWhitespace(t *testing.T) {
	r := convertHTML(t, `<p><strong>Page</strong><span>mark</span> is well<span>-</span>formed<span>.</span></p>`)
	if r.Markdown != "**Page**mark is well-formed." || r.Text != "Pagemark is well-formed." {
		t.Fatalf("markdown=%q text=%q", r.Markdown, r.Text)
	}
}

func TestSuperscriptHasReadableRepresentation(t *testing.T) {
	r := convertHTML(t, `<table><tr><th><span>3</span><sup>trits</sup></th><th>2<sup>bits</sup></th></tr><tr><td>a</td><td>b</td></tr></table>`)
	want := "| 3^trits | 2^bits |\n| --- | --- |\n| a | b |"
	if r.Markdown != want {
		t.Fatalf("want:\n%s\ngot:\n%s", want, r.Markdown)
	}
	if !strings.Contains(r.Text, "3^trits") || !strings.Contains(r.Text, "2^bits") {
		t.Fatalf("plain text lost superscript semantics: %q", r.Text)
	}
}

func TestLinkedSuperscriptsRenderAsNormalLinks(t *testing.T) {
	for _, tc := range []struct {
		name, source, markdown, text string
	}{
		{
			"numeric footnote",
			`<p>Sentence<sup><a href="#fn1">1</a></sup>.</p>`,
			`Sentence[1](https://example.com/base/#fn1).`,
			`Sentence1.`,
		},
		{
			"symbolic footnote",
			`<p>Sentence<sup><a href="#note">†</a></sup>.</p>`,
			`Sentence[†](https://example.com/base/#note).`,
			`Sentence†.`,
		},
		{
			"linked footnote inside formatting",
			`<p>Sentence<sup><strong><em><a href="#fn1">1</a></em></strong></sup>.</p>`,
			`Sentence***[1](https://example.com/base/#fn1)***.`,
			`Sentence1.`,
		},
		{
			"mixed linked and non-linked content",
			`<p>x<sup>2 <a href="/source">source</a></sup>.</p>`,
			`x^2 [source](https://example.com/source).`,
			`x^2 source.`,
		},
		{
			"ordinal superscript",
			`<p>It came 2<sup>nd</sup>.</p>`,
			`It came 2^nd.`,
			`It came 2^nd.`,
		},
		{
			"mathematical superscript",
			`<p>x<sup>2</sup> + y<sup>2</sup> = z<sup>2</sup></p>`,
			`x^2 + y^2 = z^2`,
			`x^2 + y^2 = z^2`,
		},
		{
			"surrounding punctuation and whitespace",
			`<p>Before <sup><a href="#note">*</a></sup>, after.</p>`,
			`Before [\*](https://example.com/base/#note), after.`,
			`Before *, after.`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := convertHTML(t, tc.source)
			if r.Markdown != tc.markdown || r.Text != tc.text {
				t.Fatalf("markdown: want %q, got %q; text: want %q, got %q", tc.markdown, r.Markdown, tc.text, r.Text)
			}
		})
	}
}

func TestSuperscriptWhitespaceAndAdjacentText(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   string
	}{
		{`<p>word <sup>note</sup></p>`, `word ^note`},
		{`<p>2<sup>nd</sup>place</p>`, `2^(nd)place`},
		{`<p>2<sup>nd</sup><em></em>place</p>`, `2^(nd)place`},
		{`<p>2<sup>nd</sup> place</p>`, `2^nd place`},
	} {
		r := convertHTML(t, tc.source)
		if r.Markdown != tc.want || r.Text != tc.want {
			t.Errorf("source %q: want %q, markdown=%q text=%q", tc.source, tc.want, r.Markdown, r.Text)
		}
	}
}

func TestCodeFenceInfoAndInlineCodePadding(t *testing.T) {
	r := convertHTML(t, `<pre><code class="language-go">fmt.Println("ok")
</code></pre><p><code> value </code></p>`)
	if !strings.Contains(r.Markdown, "```go\nfmt.Println") || !strings.Contains(r.Markdown, "`  value  `") {
		t.Fatal(r.Markdown)
	}
}

func TestPreformattedStructuralLines(t *testing.T) {
	tests := []struct {
		name, source, code string
	}{
		{"literal newlines", "<pre><code>first\n  second</code></pre>", "first\n  second"},
		{"highlighting spans remain inline", `<pre><code><span>con</span><span>cat</span></code></pre>`, "concat"},
		{"div line wrappers", `<pre><code><div>first</div><div>second</div></code></pre>`, "first\nsecond"},
		{"break elements", `<pre>first<br>second<br><span>third</span></pre>`, "first\nsecond\nthird"},
		{"nested spans in lines", `<pre><code><div><span>  indented</span></div><div><span>next</span></div></code></pre>`, "  indented\nnext"},
		{"empty line wrapper", `<pre><code><div>first</div><div></div><div>third</div></code></pre>`, "first\n\nthird"},
		{"existing wrapper newline", "<pre><code><div>first\n</div><div>second</div></code></pre>", "first\nsecond"},
		{"aria rows", `<pre><span role="row">first</span><span role="row">second</span></pre>`, "first\nsecond"},
		{"syntax line classes", `<pre><code><span class="line">first</span><span class="line">second</span></code></pre>`, "first\nsecond"},
		{"line classes inside neutral wrappers", `<pre><code><span><span class="line">first</span></span><span><span class="line">second</span></span></code></pre>`, "first\nsecond"},
		{"nested trailing empty line", `<pre><code><div><div>first</div><div></div></div><div>third</div></code></pre>`, "first\n\nthird"},
		{"nested leading empty line", `<pre><code><div>first</div><div><div></div><div>third</div></div></code></pre>`, "first\n\nthird"},
		{"break-only empty line", `<pre><code><div>first</div><div><br></div><div>third</div></code></pre>`, "first\n\nthird"},
		{"multiple breaks remain distinct", `<pre><code><div>first</div><div><br><br><br></div><div>third</div></code></pre>`, "first\n\n\nthird"},
		{"hidden highlighter copy", `<pre><div>shown</div><div aria-hidden="true">duplicate</div><div>next</div></pre>`, "shown\nnext"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := "```\n" + tc.code + "\n```"
			if got := convertHTML(t, tc.source).Markdown; got != want {
				t.Fatalf("want %q, got %q", want, got)
			}
		})
	}
}

func TestNestedSpansInInlineCodeRemainInline(t *testing.T) {
	got := convertHTML(t, `<p>Use <code><span>foo</span><span>bar</span></code> here.</p>`).Markdown
	if want := "Use `foobar` here."; got != want {
		t.Fatalf("want %q, got %q", want, got)
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
