# Mozilla Readability compatibility corpus

This directory describes Pagemark's secondary, article-only compatibility lane. The fixtures are **referenced as a git submodule**, not copied into this repository:

- upstream: <https://github.com/mozilla/readability>
- pinned commit: `ab4027a8b37669745016869a37a504727992b2ba`
- pinned `test/test-pages` tree: `582c0693a5f171d6568c82554dba462f0c44c46b`
- fixture path: `testdata/readability-js/test/test-pages/<fixture>/{source.html,expected.html,expected-metadata.json}`
- fixture count: 130

Initialize the frozen corpus with:

```sh
git submodule update --init --recursive
```

Normal tests never download data. If the submodule is absent, the compatibility test skips with the initialization command. CI initializes it and runs the lane.

## Scope and comparison

This lane measures compatibility with Readability's single-article output. Every source is passed to `ExtractBytes` with Mozilla's fixed synthetic URL, `http://fakehost/test/page.html`, and an explicit `WithPageType(PageTypeArticle)`, so fixture names and page-type inference are excluded. It does not define Pagemark's behavior for documentation, discussions, products, listings, collections, services, or generic structured pages. WCXB and Pagemark's frozen real-world corpus remain the primary product-quality gates.

`expected.html` is parsed with `golang.org/x/net/html`; script, style, template, and noscript text is omitted. Text is lower-cased and tokenized into runs of Unicode letters, numbers, and combining marks. Punctuation and Unicode whitespace are boundaries. Content precision, recall, and F1 use multiset word counts, so repeated words count and HTML serialization and attribute order do not matter.

Supported metadata is compared separately after HTML entity decoding and Unicode-whitespace collapse, preserving case and punctuation: `title`, `byline`, `excerpt`, `siteName`, `publishedTime`, and `lang`. Unsupported fields such as `dir` and `readerable` are not checked.

`gate.json` records the observed Pagemark baseline. Aggregate minima are rounded down to four decimal places; extraction errors and metadata exact-match counts use the observed values. Per-fixture content scores and metadata match states are retained only to identify regressions. Deliberately regenerate the gate after reviewing a compatibility change with:

```sh
UPDATE_MOZILLA_GATE=1 go test . -run '^TestMozillaReadabilityCompatibility$' -count=1 -v
```

## Provenance and licensing

The Readability repository is licensed under Apache License 2.0. Its `LICENSE.md` and `NOTICE` remain in the pinned submodule. The fixtures are publisher test pages collected upstream; rights in page content may remain with their original authors and publishers. Pagemark references the unmodified upstream tree as a submodule and does not claim ownership of it.

To upgrade, choose and review an upstream commit; update the gitlink, constants in `mozilla_corpus_test.go`, CI pin checks, and the commit/tree/count above; run the complete corpus; review score changes and worst fixtures; then deliberately update `gate.json` and the baseline report. Never fetch or refresh fixtures during a normal test.
