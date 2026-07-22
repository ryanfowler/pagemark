# Pagemark contract

## Purpose

Pagemark extracts useful content from supplied HTML. It returns deterministic Markdown and plain text. It supports articles, documentation, discussions, product pages, listings, collections, service pages, and other pages.

## Input

The caller supplies HTML as bytes, a reader, or an `html.Node`. The caller can also supply an absolute HTTP or HTTPS source URL. Pagemark does not fetch a URL. Pagemark does not run JavaScript. For a rendered page, the caller must supply the rendered HTML.

The default limits apply to input bytes, DOM elements, DOM depth, attributes, text, links, images, table cells, repeated items, and output bytes. A limit failure returns a `LimitError`.

## Output

Pagemark can preserve these structures:

- headings and paragraphs;
- ordered and unordered lists;
- definitions and quotations;
- code blocks and inline code;
- data tables;
- safe links;
- useful images and image text.

Useful images are enabled by default. They appear as Markdown image syntax and in `Document.Images`; extraction records their safe source URLs but does not fetch those resources. Callers that require text-only output can pass `WithIncludeImages(false)`.

The Markdown uses a restricted CommonMark and GFM dialect. It has no raw HTML. The default URL policy permits HTTP and HTTPS. It rejects credentials and unsafe schemes.

`Document.Text` describes the same selected content as `Document.Markdown`. `Document.Sections` is a view of that content. A quality score describes observable output properties. It is not a trust score.

## Safety

Pagemark gives syntactic safety. It removes executable HTML and unsafe link schemes. It does not make source words trustworthy. A hostile page can contain prompt injection. Treat all returned content as untrusted data. Do not put it in a privileged instruction channel.

Pagemark does not remove phrases such as “ignore previous instructions.” Such removal can damage valid source text and cannot give prompt-injection protection.

## Determinism and concurrency

The same input and options give the same content. Diagnostic timing is not part of the content contract. Public options do not use mutable global state. Concurrent extraction calls are safe. The caller must not change an `html.Node` tree during extraction.

## Non-goals

Pagemark does not:

- fetch pages or run scripts;
- reconstruct absent content;
- bypass authentication or paywalls;
- reproduce page layout;
- preserve forms, scripts, widgets, or raw HTML;
- provide semantic prompt-injection protection;
- provide a universal product or entity schema.
