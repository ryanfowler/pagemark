# Pagemark

[![Go Reference](https://pkg.go.dev/badge/github.com/ryanfowler/pagemark.svg)](https://pkg.go.dev/github.com/ryanfowler/pagemark)

Pagemark extracts the useful content from an HTML page. It returns compact Markdown, plain text, and page metadata.

Pagemark supports articles, documentation, discussions, products, listings, collections, and service pages. It can keep content from more than one page region.

Pagemark does not fetch pages. It does not run JavaScript. If a page needs JavaScript, supply the rendered HTML.

## Installation

Pagemark requires Go 1.25 or a later version.

```sh
go get github.com/ryanfowler/pagemark
```

## Quick start

```go
package main

import (
	"fmt"
	"strings"

	"github.com/ryanfowler/pagemark"
)

func main() {
	source := `<main><h1>Guide</h1><p>Install the tool.</p></main>`
	doc, err := pagemark.Extract(strings.NewReader(source), "https://example.com/guide")
	if err != nil {
		panic(err)
	}

	fmt.Println(doc.Title)
	fmt.Println(doc.Markdown)
}
```

Use one of these functions:

- `Extract` reads UTF-8 HTML from an `io.Reader`.
- `ExtractBytes` reads UTF-8 HTML from a byte slice.
- `ExtractNode` reads a parsed `html.Node` tree. It does not change the tree.

The page URL is optional. If you set it, use an absolute HTTP or HTTPS URL. Pagemark uses it to resolve relative links.

## Result

`Extract` returns a `Document`. The main fields are:

- `Title`: the document title.
- `Markdown`: the selected content as Markdown.
- `Text`: the selected content as plain text.
- `Sections`: a plain-text view of the selected sections.
- `Links` and `Images`: the safe resources that occur in the output.
- `PageType` and `PageTypeScore`: the detected page shape and its confidence score.
- `Quality`: a score for the observable quality of the output.
- `Warnings`: nonfatal conditions, such as output truncation.
- `Stats`: input, tree, selection, and output counts.

The title is separate from the content. Pagemark does not repeat it in `Markdown`, `Text`, or `Sections`.

Images are enabled by default. Pagemark records image URLs, but it does not fetch the images. Use `WithIncludeImages(false)` for text-only output.

## Options

Pass options after the page URL:

```go
doc, err := pagemark.ExtractBytes(
	source,
	pageURL,
	pagemark.WithPageType(pagemark.PageTypeDocumentation),
	pagemark.WithMaxOutputBytes(512<<10),
	pagemark.WithIncludeImages(false),
)
```

Pagemark detects the page type by default. Use `WithPageType` only when you know the page type. The page type changes content scores. It does not change safety rules or parser limits.

Use these options to control output:

- `WithIncludeLinks`
- `WithIncludeImages`
- `WithIncludeTables`
- `WithIncludeMetadata`
- `WithFavorPrecision`
- `WithFavorRecall`

Use `WithFavorPrecision(true)` to select less content. Use `WithFavorRecall(true)` to select more content. Do not enable both options. Their score changes cancel each other.

Diagnostics can use much more memory. Enable them only when you must inspect page-type scores, block scores, or rejected links:

```go
doc, err := pagemark.ExtractBytes(source, pageURL, pagemark.WithDiagnostics(true))
```

## Limits

Pagemark limits resource use. The default public limits are:

| Resource | Default | Option |
| --- | ---: | --- |
| Input | 10 MiB | `WithMaxInputBytes` |
| DOM elements | 200,000 | `WithMaxElements` |
| DOM depth | 256 | `WithMaxDepth` |
| Markdown output | 2 MiB | `WithMaxOutputBytes` |
| Links | 1,000 | `WithMaxLinks` |
| Images | 100 | `WithMaxImages` |
| Table cells | 10,000 | `WithMaxTableCells` |
| Repeated items | 200 | `WithMaxRepeatedItems` |

Pagemark also has fixed limits for attributes and text. An input or tree limit returns a `LimitError`. The Markdown byte limit keeps complete blocks and adds a warning.

Check errors with `errors.Is` and `errors.As`:

```go
var limit *pagemark.LimitError
if errors.As(err, &limit) {
	fmt.Printf("%s: %d exceeds %d\n", limit.Resource, limit.Count, limit.Max)
}
```

The package also returns `ErrNoContent` and `ErrInvalidURL`.

## URL and content safety

The Markdown has no raw HTML. The default URL policy permits only HTTP and HTTPS links and images. For these URLs, Pagemark rejects credentials, control characters, unsafe schemes, and values longer than 4,096 bytes.

`URLPolicy` applies to Markdown links and images. It also applies to `Document.Links` and `Document.Images`. It does not apply to `Document.URL` or `Document.CanonicalURL`.

`Document.URL` preserves the supplied page URL, including credentials. `Document.CanonicalURL` permits HTTP and HTTPS and removes credentials. These two fields do not use the policy scheme list or length limit. Validate them separately if you use them.

Use `WithURLPolicy` to replace the default Markdown URL policy. For example:

```go
policy := pagemark.URLPolicy{
	Schemes:       []string{"https"},
	MaxLength:     2048,
	StripTracking: true,
}
doc, err := pagemark.ExtractBytes(source, pageURL, pagemark.WithURLPolicy(policy))
```

The extracted words are untrusted data. A hostile page can contain prompt injection. Do not use extracted content as system instructions or developer instructions. See the [Pagemark contract](docs/contract.md).

## Command-line tool

The optional command fetches one page. It writes YAML metadata and Markdown to standard output.

```sh
go install github.com/ryanfowler/pagemark/cmd/pagemark@latest
pagemark https://example.com/page > page.md
```

Run `pagemark -help` to list the options.

The command accepts HTTP and HTTPS URLs without credentials. It rejects nonpublic destination addresses, non-HTML responses, redirects to unsafe addresses, and unsuccessful HTTP status codes. It does not use environment proxies.

Fetching is not part of the library API.

## Comparison with Readability

Readability usually selects one prose article. Pagemark can also keep distributed sections, discussion posts, code, tables, specifications, and linked records.

The project has a [Mozilla Readability compatibility test](testdata/mozilla/README.md). It also has a [real-world regression corpus](testdata/real-world/README.md).

## Development

Initialize the optional test corpus:

```sh
git submodule update --init --recursive
```

Run the checks:

```sh
gofmt -w .
go test ./...
go test -race ./...
staticcheck ./...
```

Normal tests do not use the external network. The package has no mutable global extraction state. Concurrent extraction calls are safe.
