# Pagemark Mozilla Readability compatibility baseline

- Mozilla Readability commit: `ab4027a8b37669745016869a37a504727992b2ba`
- `test/test-pages` tree: `582c0693a5f171d6568c82554dba462f0c44c46b`
- Pagemark commit before this lane: `9d7485a`
- Corpus: all 130 fixtures
- Environment: Go 1.26.5, macOS arm64

The unchanged Pagemark extraction pipeline was run with Mozilla's fixed `http://fakehost/test/page.html` synthetic URL and an explicit article page type. Aggregate multiset content results were:

| Precision | Recall | F1 | Extraction errors |
|---:|---:|---:|---:|
| 0.943975 | 0.919896 | 0.931780 | 3 |

The three extraction errors (`pagemark: no useful content`) were `005-unescape-html-entities`, `lazy-image-2`, and `lazy-image-3`. They contribute their expected token counts and zero matched/actual tokens to the aggregate.

Metadata uses exact matches after entity decoding and Unicode-whitespace collapse. Null expected values compare as empty values; absent JSON fields are not compared.

| Field | Matches | Compared | Rate |
|---|---:|---:|---:|
| title | 79 | 130 | 0.6077 |
| byline/author | 74 | 130 | 0.5692 |
| excerpt/description | 64 | 128 | 0.5000 |
| siteName | 120 | 130 | 0.9231 |
| publishedTime | 94 | 130 | 0.7231 |
| lang/language | 73 | 74 | 0.9865 |

Lowest-content-F1 fixtures at baseline were `005-unescape-html-entities` (0), `lazy-image-2` (0), `lazy-image-3` (0), `webmd-2` (0.0150), `herald-sun-1` (0.1034), `webmd-1` (0.2069), `mathjax` (0.2102), `svg-parsing` (0.3333), `nytimes-5` (0.3928), and `dev418` (0.4444).

`gate.json` floors aggregate minima to four decimal places to avoid representation noise while remaining close to this result. It records observed extraction-error and metadata-match counts plus exact per-fixture content scores and metadata match states for regression diagnostics. This is a secondary article-compatibility baseline, not Pagemark's primary product specification and not Readability's per-fixture 0.90 threshold.
