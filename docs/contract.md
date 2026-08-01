# Pagemark contract

## Purpose

Pagemark extracts useful content from supplied HTML. It returns deterministic
Markdown, plain text, and metadata.

Pagemark supports articles, documentation, discussions, products, listings,
collections, services, and generic pages.

## Input

Call `pagemark::extract` with a UTF-8 string, an optional absolute HTTP(S) page
URL, and an `Options` value. `extract_bytes` accepts UTF-8 bytes and enforces
the input limit before parsing.

Pagemark does not fetch pages or run JavaScript. Supply rendered HTML when a
page needs JavaScript. Invalid UTF-8 returns `Error::InvalidUtf8`.

Pagemark limits input bytes, DOM elements, DOM depth, and output bytes. The
public input and output options use `Limit::Default`, `Limit::Max`, or
`Limit::Unlimited`. DOM element and depth limits are fixed safety bounds. The
output-byte limit is nonfatal for substantive content; omitted complete blocks
set `Document::truncated`.

## Output

Pagemark can keep headings, paragraphs, lists, definitions, quotations, code,
data tables, safe links, and useful images. Images are enabled by default and
are returned in both Markdown and `Document::images`.

The Markdown uses restricted CommonMark and GitHub Flavored Markdown. It has no
raw HTML.

The default `UrlPolicy` permits HTTP and HTTPS links and images. It rejects
credentials and unsafe schemes. The policy applies to Markdown links and
images and to `Document::links` and `Document::images`. It does not apply to
`Document::url` or `Document::canonical_url`; those fields accept HTTP(S) URLs
and remove credentials.

`Document::title` contains the document title. The title does not occur again
in `Document::markdown`, `Document::text`, or `Document::sections` and does not
use the Markdown byte limit.

`Document::published_time` is an unparsed source metadata value and is not
guaranteed to be a valid timestamp.

The durable result consists of URLs, metadata, page type, Markdown, text,
sections, links, images, and the truncation flag. Page-type scores, quality
heuristics, fallback names, and detailed counters are internal diagnostics.

## Safety

Pagemark removes executable HTML and unsafe links and images. It does not make
source words safe or true. Treat all returned content as untrusted data; a
hostile page can contain prompt injection.

Pagemark does not fetch pages, run scripts, restore absent content, bypass
authentication or paywalls, reproduce page layout, or prevent semantic prompt
injection.

## Determinism and concurrency

The same input and options produce the same content. `Options` and `UrlPolicy`
are ordinary values and extraction has no mutable global state, so concurrent
calls are safe when callers do not mutate shared values during extraction.
