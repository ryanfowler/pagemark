# Pagemark contract

## Purpose

Pagemark extracts useful content from supplied HTML. It returns deterministic Markdown, plain text, and metadata.

Pagemark supports articles, documentation, discussions, products, listings, collections, services, and generic pages.

## Input

Supply HTML with a byte slice, an `io.Reader`, or an `html.Node` tree. You can also supply an absolute HTTP or HTTPS page URL. The page URL is optional.

Pagemark does not fetch the page. It does not run JavaScript. Supply rendered HTML when a page needs JavaScript.

Decode non-UTF-8 input before extraction.

Pagemark limits input bytes, tree size, tree depth, attributes, text, links,
images, table cells, repeated items, and output bytes. Public `Limits` fields
use `0` for the package default, a positive value for an explicit limit, and
`-1` for unlimited. Limits do not control whether links, images, or tables are
included. The input-byte limit applies to `Extract` and `ExtractBytes`, not
`ExtractNode`, whose DOM is already parsed. A tree or input limit returns a
`LimitError`.

## Output

Pagemark can keep these structures:

- headings and paragraphs;
- ordered and unordered lists;
- definitions and quotations;
- code blocks and inline code;
- data tables;
- safe links;
- useful images and image text.

Images are enabled by default. Images occur in `Document.Markdown` and `Document.Images`. Pagemark records their source URLs, but it does not fetch them. Use `WithIncludeImages(false)` for text-only output.

The Markdown uses a restricted CommonMark and GitHub Flavored Markdown format. It has no raw HTML.

The default URL policy permits HTTP and HTTPS links and images. It rejects credentials and unsafe schemes for these URLs. The policy applies to Markdown links and images. It also applies to `Document.Links` and `Document.Images`.

The policy does not apply to `Document.URL` or `Document.CanonicalURL`. Both
fields permit HTTP and HTTPS and remove credentials before returning a result.

`Document.Title` contains the document title. The title does not occur again in `Document.Markdown`, `Document.Text`, or `Document.Sections`. It does not use the Markdown byte limit.

`Document.Text` and `Document.Markdown` contain the same selected content. `Document.Sections` is a view of that content.

`Document.PublishedTime` is an unparsed source metadata value. It is not
guaranteed to be a valid timestamp.

The durable result contract consists of source and canonical URLs, metadata,
page type, Markdown, text, sections, links, images, and warnings. Page-type
scores, quality heuristics, block scores, fallback names, and detailed counters
are experimental diagnostics. Request them with `ExtractDetailedBytes`;
`DiagnosticReport` may change in a minor release. Legacy diagnostic fields on
`Document` are deprecated during the pre-1.0 migration.

## Safety

Pagemark removes executable HTML. It removes unsafe schemes from Markdown links and images. It does not make the source words safe or true.

A hostile page can contain prompt injection. Treat all returned content as untrusted data. Do not put it in a system instruction or a developer instruction.

Pagemark does not remove phrases such as "ignore previous instructions." This type of removal can damage valid text. It cannot prevent prompt injection.

## Determinism and concurrency

The same input and options produce the same content. Diagnostic timing is not part of this contract.

Public options do not use mutable global state. Concurrent extraction calls are safe.
`WithURLPolicy` clones caller-owned scheme slices, including when one option is
reused concurrently.

Do not change an `html.Node` tree during extraction.

## Exclusions

Pagemark does not:

- fetch pages;
- run scripts;
- restore absent content;
- bypass authentication or paywalls;
- reproduce the page layout;
- keep forms, scripts, widgets, or raw HTML;
- prevent semantic prompt injection;
- supply a universal product or entity schema.
