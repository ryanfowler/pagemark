# pagemark

[![Go Reference](https://pkg.go.dev/badge/github.com/ryanfowler/pagemark.svg)](https://pkg.go.dev/github.com/ryanfowler/pagemark)

Pagemark extracts useful web page content as compact, safe Markdown. It supports prose and structured pages. It uses block classification and can keep multiple content regions.

Pagemark does not fetch pages or run JavaScript. Supply rendered HTML when a page needs JavaScript.

## Install

```sh
go get github.com/ryanfowler/pagemark
```

## Use

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
    fmt.Println(doc.Markdown)
}
```

You can also use `ExtractBytes` or `ExtractNode`. `ExtractNode` does not change the supplied tree.

The `Document` result contains metadata, a page type, Markdown, plain text, sections, safe links, useful images, a quality score, warnings, and extraction statistics. Useful images are included by default as Markdown image syntax and in `Document.Images`; Pagemark records their safe remote URLs but does not fetch them. Pass `WithIncludeImages(false)` for text-only output.

Enable diagnostics only when you need block scores and rejected-link details:

```go
doc, err := pagemark.ExtractBytes(source, pageURL, pagemark.WithDiagnostics(true))
```

## Safety

The Markdown has no raw HTML. The default policy permits only HTTP and HTTPS links. Pagemark rejects link credentials, control characters, unsafe schemes, and long URLs.

The extracted words are untrusted source data. Pagemark does not protect an agent from prompt injection. Supply the result to an agent as data, not as privileged instructions. See [the contract](docs/contract.md).

## Limits

Pagemark has default limits for input size, DOM size, depth, text, output, links, images, tables, and repeated items. Use options such as `WithMaxInputBytes`, `WithMaxElements`, and `WithMaxOutputBytes` to change these limits. Output truncation occurs only at a block boundary and adds a warning.

## Page types

Pagemark detects article, documentation, discussion, product, listing, collection, service, and generic pages. Use `WithPageType` when the caller has a reliable type. Type profiles change scores. They do not change parser limits or URL safety.

## Command-line tool

The repository has one optional command. It fetches a page and writes Markdown to standard output. Page metadata is included as YAML frontmatter, followed by an empty line and the extracted content. Fetching remains outside the library API.

```sh
go install github.com/ryanfowler/pagemark/cmd/pagemark@latest
pagemark https://example.com/page > page.md
```

Use `pagemark -help` to see timeout, input limit, and User-Agent options. The command accepts HTTP and HTTPS URLs without credentials. It resolves each initial and redirect host itself and connects to the resolved public IP address. It rejects loopback, private, link-local, multicast, reserved, and documentation address ranges. It does not use environment proxies. These rules also prevent a DNS change between validation and connection from redirecting the connection to a private address. The command also rejects non-HTML responses and unsuccessful HTTP status codes.

Normal tests do not access the external network.

## Benchmark method

The benchmark uses multiset word precision, recall, and F1. It also checks required and forbidden snippets. The WCXB version file pins the source commit and archive SHA-256 checksum.

On WCXB v1.0 development data, an implementation run on the initial profile gave 0.760 overall F1. Results vary only when code or profiles change. Use the held-out split only for a release review. Do not compare this number with a benchmark that uses a different normalization method.

The project includes synthetic safety and structure tests. It also keeps a small, checksummed [real-world regression corpus](testdata/real-world/README.md) with declarative page-type and content expectations. Tests use frozen snapshots and never refresh them from the network.

The full WCXB data is not in this repository because it is large. WCXB uses the CC BY 4.0 license.

## Difference from Readability

Readability is an article specialist that usually selects one prose region. Pagemark uses its own page-type-aware extraction pipeline and can keep distributed sections, discussion posts, code, tables, specifications, and linked records.

## Development

```sh
gofmt -w .
go test ./...
go test -race ./...
staticcheck ./...
```

Run the end-to-end benchmarks against the frozen real-world corpus with:

```sh
go test -run '^$' -bench '^BenchmarkExtractRealWorld$' -benchmem
```

The benchmark includes HTML parsing and reports time, throughput, bytes, and allocations for representative articles, documentation, discussions, products, listings, services, and generic pages. CPU and allocation profiles can be captured by adding `-cpuprofile cpu.out -memprofile mem.out` and inspected with `go tool pprof`.

The package has no mutable global extraction state. Concurrent calls are safe.
