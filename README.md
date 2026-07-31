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
- `PageType`: the detected page shape.
- `Warnings`: nonfatal conditions, such as output truncation.

The title is separate from the content. Pagemark does not repeat it in `Markdown`, `Text`, or `Sections`.
`PublishedTime` is the unparsed source metadata value and is not guaranteed to
use RFC 3339.

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
- `WithSelectionMode`

Selection is balanced by default. Use `SelectionPrecision` to usually select
less content or `SelectionRecall` to usually select more:

```go
doc, err := pagemark.ExtractBytes(
	source,
	pageURL,
	pagemark.WithSelectionMode(pagemark.SelectionPrecision),
)
```

Detailed diagnostics are experimental and can use much more memory. Request
them only when you must inspect page-type scores, quality heuristics, block
scores, or rejected links:

```go
doc, report, err := pagemark.ExtractDetailedBytes(source, pageURL)
```

`DiagnosticReport` fields may change in a minor release.

## Resource bounds

Pagemark exposes two resource options:

| Resource | Default | Option |
| --- | ---: | --- |
| Input | 10 MiB | `WithMaxInputBytes` |
| Markdown output | 2 MiB | `WithMaxOutputBytes` |

Zero selects the default. A positive value sets the limit. `-1` disables the
limit. Values below `-1` return `ErrInvalidOption`. Options apply in order, so
a later option overrides an earlier one.

Pagemark also applies fixed internal limits to DOM elements and DOM depth. An
input or DOM limit returns a `LimitError`. The Markdown byte limit keeps
complete blocks and adds a warning. The input-byte limit applies to `Extract`
and `ExtractBytes`, not `ExtractNode`, whose DOM is already parsed.

Use `WithIncludeLinks`, `WithIncludeImages`, and `WithIncludeTables` to control
output features. These options are independent of resource bounds.

Check errors with `errors.Is` and `errors.As`:

```go
var limit *pagemark.LimitError
if errors.As(err, &limit) {
	fmt.Printf("%s: %d exceeds %d\n", limit.Resource, limit.Count, limit.Max)
}
```

The package also returns `ErrNoContent`, `ErrInvalidURL`, and
`ErrInvalidOption`. Output truncation is nonfatal. If no selected substantive
content block fits within the output limit, the package returns a `Document`
with bounded empty output and `WarningOutputTruncated`.

## URL and content safety

The Markdown has no raw HTML. The default URL policy permits only HTTP and HTTPS links and images. For these URLs, Pagemark rejects credentials, control characters, unsafe schemes, and values longer than 4,096 bytes.

`URLPolicy` applies to Markdown links and images. It also applies to `Document.Links` and `Document.Images`. It does not apply to `Document.URL` or `Document.CanonicalURL`.

`Document.URL` and `Document.CanonicalURL` permit HTTP and HTTPS and always
remove credentials. These two fields do not use the policy scheme list or
length limit.

Use `DefaultURLPolicy` to safely modify the default Markdown URL policy:

```go
policy := pagemark.DefaultURLPolicy()
policy.AllowedSchemes = []string{"https"}
policy.MaxLength = 2048
policy.StripTracking = true

doc, err := pagemark.ExtractBytes(source, pageURL, pagemark.WithURLPolicy(policy))
```

Pagemark copies `AllowedSchemes`, so a reusable option is safe for concurrent
extraction. Scheme names are normalized to lowercase and duplicates are
removed. An invalid scheme or a `MaxLength` below `-1` returns
`ErrInvalidOption`. The defaults remain HTTP and HTTPS only; add `"mailto"`
explicitly if needed.

Warning codes and limit resources are typed. Compare `Warning.Code` with
constants such as `WarningOutputTruncated`, and compare `LimitError.Resource`
with constants such as `LimitInputBytes`.

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

See the [v0.2 migration guide](docs/migration-v0.2.md) for the public API
simplification.
