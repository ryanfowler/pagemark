# Implementation Plan: General-Purpose HTML → Clean Markdown Extraction for AI Agents

**Audience:** An AI coding agent implementing a new Go module
**Status:** Proposed implementation plan
**Primary objective:** Extract the meaningful content of arbitrary web pages—not only articles—and render it as deterministic, compact, structurally faithful, safe Markdown.

---

## 1. Executive recommendation

Build this as a **new Go module with a different contract from `readability`**, while reusing the ideas and, initially, the public API of `github.com/ryanfowler/readability` as one signal and fallback.

The central design should be:

1. Parse the HTML once into a DOM.
2. Divide the document into semantically meaningful **content blocks**.
3. Compute structural, textual, semantic, and repetition features for every block.
4. Infer a coarse page type.
5. Classify blocks by role: primary content, supporting content, navigation, boilerplate, interaction controls, or uncertain.
6. Select and assemble **multiple related content regions**, preserving DOM order.
7. Convert the selected structure into a restricted CommonMark/GFM-compatible Markdown AST.
8. Render Markdown without raw HTML, unsafe URL schemes, unbounded output, or nondeterministic behavior.
9. Return extraction diagnostics and a calibrated quality/confidence score.
10. Evaluate every change against a reproducible, offline corpus, principally the WCXB multi-type benchmark.

Do **not** implement this by simply making Mozilla Readability less strict. Readability is optimized around a dominant article container and paragraph-like prose. A general extractor needs to retain distributed sections, repeated records, code, tables, specifications, and multi-author conversations.

---

## 2. Goals

The module should:

- Accept raw HTML and an optional source URL.
- Work without JavaScript execution, network access, CSS layout, or an LLM.
- Extract useful content from:
  - articles and blog posts;
  - documentation and API references;
  - forums, Q&A pages, and discussion threads;
  - product pages and specification pages;
  - collection, category, and listing pages;
  - service and marketing pages with content spread across sections;
  - wiki and repository-like pages;
  - malformed but parseable HTML.
- Preserve meaningful structure:
  - headings;
  - paragraphs;
  - ordered and unordered lists;
  - definition-like material;
  - blockquotes;
  - code blocks and inline code;
  - data tables;
  - links;
  - useful image alt text and captions.
- Exclude or strongly reduce:
  - global navigation;
  - repeated headers and footers;
  - cookie and consent dialogs;
  - advertisements;
  - social/share controls;
  - login/signup forms;
  - unrelated recommendations;
  - empty layout wrappers;
  - duplicated content.
- Produce deterministic Markdown suitable for indexing, retrieval, summarization, and agent context.
- Expose enough diagnostics to explain why a block was retained or removed.
- Enforce explicit input, DOM, traversal, and output limits.
- Remain fast enough for crawler and agent pipelines.

---

## 3. Non-goals

The first version should not:

- Fetch URLs.
- Execute JavaScript.
- Reconstruct content that is absent from the supplied HTML.
- Perform screenshot or visual-layout analysis.
- Circumvent paywalls, authentication, or anti-bot systems.
- Guarantee protection against prompt injection contained in page text.
- Use an LLM in the extraction path.
- Contain a large set of hard-coded selectors for individual websites.
- Attempt arbitrary structured-data extraction into a universal product or entity schema.
- Perfectly reproduce the visual page.
- Preserve executable HTML, scripts, embedded widgets, forms, or event handlers.

“Safe Markdown” must mean structurally sanitized output with constrained links and no raw executable HTML. It must **not** imply that the extracted words are trustworthy instructions. The returned document should explicitly identify the content as untrusted source material.

---

## 4. What Mozilla Readability does

The current Go port closely follows Mozilla Readability and already provides several useful components:

- HTML parsing and cloned-tree retries.
- Metadata extraction from HTML and JSON-LD.
- title, byline, language, site, excerpt, and publication-time extraction;
- URL/base resolution;
- hidden-node removal;
- class/id pattern weighting;
- link-density calculations;
- article candidate scoring;
- sibling inclusion;
- cleanup of tables, lists, headings, forms, media, and empty nodes;
- a fast readerability heuristic;
- resource limits and debug logging;
- the official Mozilla fixture corpus.

### 4.1 Readability’s main algorithm

At a high level, Readability:

1. Prepares and normalizes the document.
2. Removes invisible and strongly unlikely nodes.
3. Identifies paragraph-like elements worth scoring.
4. Gives each text block a score based largely on:
   - minimum text length;
   - punctuation/comma count;
   - text length;
   - tag type;
   - positive or negative class/id patterns.
5. Propagates the block score to ancestors with decreasing weight.
6. Reduces candidate scores according to link density.
7. Chooses the highest-scoring candidate.
8. Sometimes promotes the candidate to a parent.
9. Includes nearby siblings when their score or paragraph characteristics are sufficiently strong.
10. Aggressively cleans the resulting subtree.
11. Retries with successively less strict flags if the result is too short.
12. Returns the longest acceptable article-like result.

### 4.2 Readability’s strengths

This is effective for pages with:

- one dominant article;
- long prose paragraphs;
- a clear article body;
- low link density in the main body;
- navigation outside the main article container;
- comments that are secondary to the article.

The existing Go port is especially valuable because it is already tested against Mozilla’s official 130-case corpus.

### 4.3 Assumptions that break on general pages

Readability’s assumptions cause predictable failures outside articles:

| Page shape | Why article scoring fails |
|---|---|
| Documentation | Headings, code, tables, and short reference entries may contain little prose and punctuation. |
| Forum or Q&A | Repeated comments/posts are the primary content, but “comment” classes and repeated structures often look like boilerplate. |
| Product | Important facts may be short labels, lists, tables, accordions, or JSON-LD rather than paragraphs. |
| Collection/listing | The content is a sequence of repeated cards; selecting one dominant subtree can retain one item or remove the whole repeated region. |
| Service/marketing | Useful content is distributed across hero, features, pricing, testimonials, and FAQ sections. |
| Homepage/index | Link density is intrinsically high because the page’s purpose is to expose meaningful destinations. |
| Repository/issue page | Code, metadata, event timelines, and repeated comments are more useful than paragraph density. |

The key architectural limitation is not merely strict cleanup. It is the objective: **choose one article-like region**. The new objective must be: **identify and assemble all primary content regions appropriate to this page type**.

---

## 5. Proposed architecture

Use a staged pipeline with explicit intermediate representations.

```text
HTML bytes
   │
   ▼
Parse + limits + URL/base normalization
   │
   ▼
DOM index and page metadata
   │
   ▼
Visibility / hard-exclusion pass
   │
   ▼
Block segmentation
   │
   ▼
Feature extraction
   │
   ├──────────────► Page-type inference
   │
   ▼
Block role scoring/classification
   │
   ▼
Region graph construction and assembly
   │
   ▼
Pruning, deduplication, and quality checks
   │
   ▼
Restricted Markdown AST
   │
   ▼
Safe deterministic Markdown + text + diagnostics
```

### 5.1 Recommended package layout

```text
/
├── extract.go                 # Public API
├── options.go
├── result.go
├── errors.go
├── internal/
│   ├── dom/
│   │   ├── parse.go
│   │   ├── index.go
│   │   ├── visibility.go
│   │   └── url.go
│   ├── metadata/
│   ├── block/
│   │   ├── segment.go
│   │   ├── features.go
│   │   ├── role.go
│   │   └── repetition.go
│   ├── classify/
│   │   ├── pagetype.go
│   │   └── profile.go
│   ├── select/
│   │   ├── score.go
│   │   ├── graph.go
│   │   ├── assemble.go
│   │   └── quality.go
│   ├── markdown/
│   │   ├── ast.go
│   │   ├── convert.go
│   │   ├── render.go
│   │   └── urlpolicy.go
│   └── debug/
├── cmd/
│   ├── extract/
│   ├── inspect/
│   ├── corpus/
│   └── evaluate/
├── testdata/
│   ├── synthetic/
│   ├── regressions/
│   └── manifests/
└── benchmarks/
    ├── wcxb/
    └── reports/
```

Keep the extraction engine independent of the CLI, corpus downloader, browser capture tools, and benchmark adapters.

---

## 6. Core data model

### 6.1 Public result

A suggested initial API:

```go
type Document struct {
    URL          string
    CanonicalURL string

    Title         string
    Description   string
    Author        string
    SiteName      string
    Language      string
    PublishedTime string

    PageType      PageType
    PageTypeScore float64

    Markdown string
    Text     string

    Sections []Section
    Links    []Link
    Images   []Image

    Quality     float64
    Diagnostics *Diagnostics
    Warnings    []Warning

    Stats Stats
}
```

`Markdown` and `Text` should describe the same retained content. `Sections` should be optional structured output, not a second independently extracted result.

### 6.2 Internal block

```go
type Block struct {
    ID       int
    Node     *html.Node
    ParentID int
    Order    int
    Depth    int

    Kind BlockKind
    Role BlockRole

    Text        string
    DirectText  string
    LinkText    string
    HeadingText string

    Features Features
    Scores   Scores

    Selected bool
    Reasons  []Reason
}
```

A block is the smallest unit independently classified as content or boilerplate. It should normally correspond to a meaningful block-level unit—not every DOM node and not an arbitrary character window.

### 6.3 Block kinds

At minimum:

- heading;
- paragraph;
- list;
- list item;
- definition group;
- code block;
- table;
- blockquote;
- figure/caption;
- card/repeated item;
- conversation post;
- metadata row;
- generic section;
- unsupported/other.

### 6.4 Block roles

At minimum:

- `Primary`;
- `Supporting`;
- `Navigation`;
- `Boilerplate`;
- `Control`;
- `Advertisement`;
- `Related`;
- `Unknown`.

Keep `Kind` and `Role` separate. A list can be primary content on a documentation or listing page and navigation on another page.

---

## 7. Detailed extraction algorithm

## 7.1 Stage A: Parse, constrain, and normalize

1. Read through a limiting reader.
2. Detect/decode character encoding when input is bytes.
3. Parse with `golang.org/x/net/html`.
4. Reject or stop processing after configured limits:
   - input bytes;
   - element count;
   - DOM depth;
   - attribute count/size;
   - text bytes;
   - traversal operations.
5. Resolve the effective base URL from:
   - supplied page URL;
   - valid `<base href>`.
6. Index each node once:
   - preorder position;
   - depth;
   - parent;
   - element siblings;
   - tag;
   - normalized id/class tokens;
   - landmark/ARIA role;
   - selected microdata/schema attributes.
7. Cache subtree aggregates bottom-up so feature extraction remains approximately linear.

Do not repeatedly call “all text under this node” during scoring. Precompute text, link text, descendant tag counts, and visible text length.

## 7.2 Stage B: Metadata and semantic hints

Extract page-level metadata independently from main-content selection:

- `<title>`;
- Open Graph;
- Twitter Card;
- JSON-LD;
- microdata;
- canonical URL;
- author;
- publication date;
- language and direction;
- description;
- schema.org page/entity types.

Use metadata as hints, not unquestioned truth. JSON-LD may be stale, duplicated, unrelated, or injected by a template.

Create semantic hints from:

- `main`, `article`, `section`, `nav`, `aside`, `header`, `footer`;
- ARIA landmarks;
- `itemprop`, `itemtype`, and `itemscope`;
- heading hierarchy;
- `rel=author`, `rel=canonical`;
- common generic tokens such as `content`, `main`, `article`, `docs`, `post`, `thread`, `product`, `spec`, `results`;
- strong boilerplate tokens such as `cookie`, `consent`, `share`, `social`, `newsletter`, `login`, `signup`, `advert`.

Class/id tokens should be weak features. They must not be irreversible deletion rules except for very high-confidence cases.

## 7.3 Stage C: Visibility and hard exclusions

Immediately discard or ignore:

- `script`, `style`, `template`, `noscript` where it only contains fallback duplication, `canvas`;
- document metadata elements;
- nodes hidden by:
  - `hidden`;
  - `aria-hidden=true`;
  - `inert`;
  - inline `display:none`;
  - inline `visibility:hidden`;
- modal dialogs explicitly marked as modal;
- empty nodes with no useful text, media, code, or table content;
- form controls and interactive widgets unless the profile explicitly preserves their labels as content.

Do not remove all `nav`, `aside`, `form`, or “comment” nodes here. Their meaning depends on page type and context.

## 7.4 Stage D: Block segmentation

Walk the visible DOM and emit blocks using semantic boundaries.

Create a block when encountering:

- headings;
- paragraphs;
- `pre`;
- tables;
- blockquotes;
- lists;
- definition lists;
- figures with captions;
- top-level repeated cards;
- article/forum posts;
- sections containing direct text not captured by children.

Merge adjacent inline phrasing nodes into their nearest block.

Avoid:

- one block per `<span>`;
- one giant block for the whole `<main>`;
- duplicate parent and child blocks containing the same text.

For generic `div` and `section` nodes, emit a block only when:

- they contain meaningful direct text;
- they represent a repeated-item boundary;
- they are a semantic region;
- their descendants do not already account for most of their content.

Track the source DOM node for every block so selected blocks can later be converted structurally.

## 7.5 Stage E: Feature extraction

Calculate features in one bottom-up/top-down analysis.

### Text features

- visible character count;
- word count;
- sentence-ending punctuation count;
- average word length;
- line count;
- direct-text ratio;
- stopword/function-word ratio when a language list is available;
- alphabetic-character ratio;
- numeric ratio;
- all-caps ratio;
- repeated-token ratio;
- title similarity;
- description similarity;
- nearby heading similarity.

Language-specific stopwords should be optional. The generic algorithm must still work without language detection.

### Link features

- link text length;
- link density;
- number of links;
- unique destination count;
- same-host versus external link ratio;
- average link-label length;
- navigational separator frequency;
- whether most text is one link;
- whether links appear as a coherent repeated record set.

High link density is not always bad. It is expected on listings, documentation indexes, and collections.

### Structural features

- tag and semantic landmark;
- DOM depth;
- normalized document position;
- sibling count;
- child block count;
- heading level;
- ancestor roles;
- distance to title/H1;
- distance to a `main` landmark;
- whether inside header/footer/nav/aside/dialog/form;
- whether inside schema.org `Article`, `Product`, `DiscussionForumPosting`, etc.;
- subtree-to-page text share;
- direct-text-to-subtree-text share.

### Content-shape features

- paragraph count and density;
- list item count;
- table dimensions;
- code-line count;
- heading-to-text ratio;
- key/value row patterns;
- number of short label/value pairs;
- presence of currency, rating, SKU, version, command syntax, timestamps, authors;
- media caption/alt-text quality.

### Repetition features

Detect repeated sibling structures using a structural signature:

```text
tag + selected class tokens + child tag sequence + text/link shape
```

For each repeated group, calculate:

- group size;
- average text length;
- variance in text length;
- average link density;
- shared structural signature;
- position span;
- whether each item contains a heading/title;
- whether each item contains author/time/body fields;
- whether the group resembles cards, search results, comments, reviews, or menu items.

Repetition is context-dependent:

- large repeated groups in a header are likely navigation;
- repeated blocks with authors/timestamps/body text are likely a discussion;
- repeated blocks with headings, descriptions, and links may be a listing;
- repeated one-word links are likely navigation;
- repeated product names/prices may be primary content on a collection page.

### Boilerplate features

- positive/negative class and id tokens;
- cookie/consent vocabulary;
- share/follow/newsletter vocabulary;
- copyright/legal footer vocabulary;
- login/account/cart controls;
- excessive button/input counts;
- very low unique-text ratio;
- exact or near-duplicate text elsewhere;
- common header/footer position;
- tiny text surrounded by controls.

## 7.6 Stage F: Page-type inference

Infer one of:

- `Article`;
- `Documentation`;
- `Discussion`;
- `Product`;
- `Listing`;
- `Collection`;
- `Service`;
- `Generic`.

Use a small deterministic classifier first. Features should include:

- schema.org and Open Graph types;
- URL path tokens;
- counts and ratios of paragraphs, code blocks, tables, lists, repeated groups, forms, prices, authors, timestamps, and headings;
- number and shape of repeated records;
- distribution of text across top-level sections;
- dominant candidate concentration;
- presence of common documentation or forum structures.

The output must include a confidence score.

When confidence is low, do not force a single profile. Run the selector with two or three plausible profiles and choose the result with the best quality score. This is a cheap deterministic ensemble and avoids brittle early classification.

Do not begin with a trained model. Preserve the feature vector and benchmark harness so an optional compact model can be added later if rules plateau.

## 7.7 Stage G: Block scoring

Compute separate scores rather than a single opaque number:

```go
type Scores struct {
    Content       float64
    Boilerplate   float64
    Navigation    float64
    Coherence     float64
    Structured    float64
    PageTypeFit   float64
    Final         float64
}
```

A useful conceptual formula is:

```text
final =
    semantic prior
  + content value
  + structured-data value
  + local coherence
  + page-type fit
  - boilerplate likelihood
  - control likelihood
  - duplicate penalty
```

### Strong positive signals

- inside `main` or `article`;
- near the page title;
- substantial unique visible text;
- coherent headings followed by content;
- meaningful code, table, definition, or list content;
- schema matches inferred page type;
- repeated records match the inferred page type;
- selected neighboring blocks support the same section.

### Strong negative signals

- hidden or modal;
- global header/footer/navigation landmark;
- consent, sharing, account, cart, or signup controls;
- almost entirely buttons or form controls;
- repeated short site-wide links;
- duplicate text;
- unrelated recommendation section far after primary content.

### Important rule

Never globally assign negative weight to headings, lists, tables, code, or repeated structures. Their value must be profile-aware.

## 7.8 Stage H: Region graph and assembly

Model blocks as an ordered graph.

Edges should connect:

- parent and child blocks;
- adjacent blocks;
- a heading and the following blocks until the next equal/higher heading;
- members of a repeated group;
- blocks inside the same semantic region.

Selection algorithm:

1. Choose high-confidence primary seeds.
2. Expand through edges when the neighbor:
   - has compatible role;
   - is structurally contiguous;
   - adds meaningful unique content;
   - does not cross a strong boilerplate boundary.
3. Merge selected blocks into regions.
4. Score each region for:
   - coverage;
   - precision;
   - coherence;
   - title relevance;
   - page-type fit;
   - structural completeness.
5. Keep all primary regions above threshold.
6. Add supporting regions when they materially explain the page:
   - article metadata;
   - product specifications;
   - documentation examples;
   - answer comments;
   - FAQ;
   - captions.
7. Preserve original DOM order.
8. Apply output budgets only after scoring, and truncate at semantic boundaries.

This is the major difference from Readability: there may be several valid regions.

## 7.9 Stage I: Profile-specific behavior

Profiles should alter thresholds and interpretation—not replace the generic engine.

### Article

- prefer one dominant prose region;
- retain title/byline/date;
- include adjacent preamble and article sections;
- normally exclude comments and recommendations;
- use the existing Readability result as a candidate or fallback.

### Documentation

- strongly preserve headings, code, tables, definitions, and examples;
- exclude sidebar navigation and page TOC when they duplicate headings;
- accept short technical blocks;
- avoid punctuation and paragraph-density requirements;
- preserve API signatures and command examples.

### Discussion

- treat repeated posts/comments as primary;
- preserve author, timestamp, post body, accepted-answer markers;
- remove vote controls, avatars without useful alt text, signatures, and reply buttons;
- retain the question/topic plus responses in order.

### Product

- combine product title, description, price/availability when visible, feature lists, specifications, and meaningful reviews;
- use JSON-LD as a fallback or supplement only when it corresponds to the visible product;
- remove cart controls, shipping calculators, recommendations, and unrelated collections;
- support label/value and table-heavy layouts.

### Listing

- retain repeated records with meaningful titles/descriptions;
- keep item links because they are part of the content;
- remove filters, sort controls, pagination boilerplate, and global navigation;
- cap extremely large result sets with an explicit truncation warning.

### Collection

- similar to listing, but expect product/item cards and higher link density;
- preserve names, short descriptions, prices, and useful attributes;
- avoid retaining every repeated decorative label.

### Service

- aggregate multiple top-level sections;
- keep hero explanation, features, process, pricing summary, testimonials, FAQ, and constraints when substantive;
- remove repeated calls to action and signup controls;
- use section-level coherence rather than one dominant candidate.

### Generic

- prefer semantic `main`;
- retain coherent regions;
- use conservative boilerplate removal;
- produce a lower confidence when the page shape is ambiguous.

## 7.10 Stage J: Deduplication and cleanup

Perform structural cleanup after selection:

- remove exact duplicate blocks;
- remove near-duplicate blocks using normalized text hashes and token similarity;
- collapse duplicate title/H1;
- collapse table-of-contents entries that duplicate retained headings;
- remove empty headings;
- repair heading hierarchy without changing wording;
- normalize list nesting;
- merge fragmented paragraphs;
- remove tracking-only query parameters only if explicitly enabled;
- cap repeated records, tables, links, and images according to options;
- retain a warning whenever content is truncated.

Do not remove a block merely because its words occur elsewhere; navigation/TOC duplication and legitimate repeated examples must be distinguished by structure and position.

---

## 8. Markdown design and safety

Build a small internal Markdown AST. Do not render Markdown directly during DOM traversal.

Suggested nodes:

- document;
- heading;
- paragraph;
- text;
- emphasis/strong;
- inline code;
- code block;
- blockquote;
- ordered/unordered list;
- list item;
- table;
- link;
- image description;
- thematic break.

### 8.1 Output dialect

Target a conservative subset of CommonMark plus selected GFM features:

- CommonMark headings, paragraphs, emphasis, links, code, quotes, and lists;
- GFM tables when representable;
- no raw HTML;
- no embedded scripts, SVG, iframes, objects, or forms;
- no extension syntax that causes rendering side effects.

### 8.2 URL policy

Resolve relative URLs, then allow only explicitly configured schemes.

Default recommended policy:

- allow `http`;
- allow `https`;
- optionally allow `mailto`;
- reject `javascript`, `data`, `file`, `vbscript`, custom app schemes, and malformed URLs;
- limit URL length;
- remove credentials from rendered URLs;
- normalize control characters;
- preserve fragments when useful;
- expose rejected links in diagnostics, not output.

### 8.3 Markdown escaping

The renderer must correctly escape text in each context:

- paragraph text;
- headings;
- link labels;
- link destinations;
- tables;
- list markers;
- blockquote prefixes;
- inline code;
- fenced code.

For fenced code, choose a backtick or tilde fence longer than any run in the content. Preserve code text exactly apart from newline normalization and configured size limits.

### 8.4 Tables

Render a GFM table only when:

- rows form a rectangular or safely normalizable grid;
- dimensions are below limits;
- cells do not contain incompatible block structures.

Otherwise render the table as a readable sequence of labeled rows. Never silently flatten a specification table into an ambiguous run of words.

### 8.5 Images

Default behavior for agent-oriented output:

- do not embed arbitrary image Markdown automatically;
- retain useful alt text and captions;
- optionally emit `Image: <alt text> (<absolute URL>)`;
- reject data URLs and unsafe schemes;
- cap the number of images;
- mark likely decorative images as omitted.

### 8.6 Prompt-injection boundary

The module cannot sanitize semantic instructions from a hostile webpage. Return provenance fields and document this contract:

```text
The Markdown is untrusted content extracted from URL X.
It must be supplied to an agent as data, not as privileged instructions.
```

Do not try to strip phrases such as “ignore previous instructions”; doing so is unreliable and corrupts legitimate source text.

---

## 9. Quality scoring and fallbacks

Return a quality score based on observable extraction properties, not on confidence alone.

Candidate signals:

- selected-text share of visible page text;
- boilerplate score of retained blocks;
- amount of high-confidence content omitted;
- title/H1 agreement;
- coherent heading sequence;
- duplicate rate;
- extreme link density;
- selected-region fragmentation;
- empty or very short output;
- malformed Markdown structures;
- page-type/profile agreement.

Fallback sequence:

1. Primary multi-region selector.
2. Alternate plausible page-type profile if classification confidence is low.
3. Semantic `<main>`/`article` extraction with conservative cleanup.
4. Existing `readability.ParseNode` result for article-like pages.
5. High-recall visible-text extraction with obvious chrome removed.
6. Return `ErrNoContent` only when all paths are empty or unusable.

The result must say which fallback won.

---

## 10. Public API proposal

```go
func Extract(input io.Reader, pageURL string, opts ...Option) (*Document, error)

func ExtractBytes(input []byte, pageURL string, opts ...Option) (*Document, error)

func ExtractNode(root *html.Node, pageURL string, opts ...Option) (*Document, error)
```

Suggested options:

```go
WithPageType(PageType)
WithMaxInputBytes(int64)
WithMaxElements(int)
WithMaxDepth(int)
WithMaxOutputBytes(int)
WithMaxLinks(int)
WithMaxImages(int)
WithMaxTableCells(int)
WithMaxRepeatedItems(int)

WithIncludeLinks(bool)
WithIncludeImages(bool)
WithIncludeTables(bool)
WithIncludeMetadata(bool)

WithURLPolicy(URLPolicy)
WithProfile(Profile)
WithFavorPrecision(bool)
WithFavorRecall(bool)

WithDiagnostics(bool)
WithLogger(*slog.Logger)
```

Avoid dozens of exposed scoring constants in v1. Keep internal scoring tunable through private profiles and benchmarked configuration files until the behavior stabilizes.

---

## 11. Testing strategy

Use four distinct test layers.

## 11.1 Layer 1: Small unit and synthetic fixtures

Commit small hand-written fixtures that isolate one behavior:

- hidden-node handling;
- semantic landmarks;
- malformed nesting;
- relative URL resolution;
- unsafe URL rejection;
- heading repair;
- nested lists;
- code fences containing backticks;
- ragged tables;
- definition lists;
- duplicate content;
- repeated cards;
- forum posts;
- cookie banners;
- consent modal;
- product specification table;
- documentation sidebar versus article body;
- service page with distributed sections;
- output limits.

These tests should use exact assertions because each fixture tests one rule.

## 11.2 Layer 2: Mozilla Readability corpus

Retain the existing 130 Mozilla fixtures as an article-regression suite.

For the new module:

- force or detect the article profile;
- compare normalized text with the existing `readability` output and expected corpus;
- track metadata;
- add structure metrics for headings, lists, code, tables, and links;
- permit deliberate differences only through an explicit allowlist with rationale.

The current test’s token-set similarity is useful but too weak on its own because it ignores order, duplicates, and structure. Add sequence and structure checks.

## 11.3 Layer 3: WCXB benchmark

Adopt WCXB as the primary broad benchmark before building a new corpus.

At the time of this plan, WCXB contains:

- 2,008 cached real web pages;
- 1,613 domains;
- 7 page types;
- 1,497 development pages;
- 511 held-out test pages;
- gzipped source HTML;
- full ground-truth text;
- required-content snippets;
- forbidden-boilerplate snippets;
- title, author, and date labels;
- a CC-BY-4.0 license.

Integration approach:

```text
benchmarks/wcxb/
├── README.md
├── VERSION
├── LICENSE
├── manifest.json
└── data/               # ignored or fetched; do not duplicate unnecessarily
```

Add:

```bash
go run ./cmd/corpus wcxb download
go run ./cmd/evaluate -corpus wcxb -split dev
go run ./cmd/evaluate -corpus wcxb -split test
```

The normal `go test ./...` command must remain offline and fast. Run the full corpus in a separate CI job or behind a build tag.

### WCXB metrics

At minimum report:

- multiset word precision;
- multiset word recall;
- F1;
- required-snippet pass rate;
- forbidden-snippet pass rate;
- metadata accuracy;
- result-empty rate;
- error rate;
- output/input byte ratio;
- runtime and allocations;
- all metrics by page type.

Add structure metrics not present in the plain-text benchmark:

- heading recall/order;
- code block recall;
- list item recall;
- table cell recall;
- link safety violations;
- raw HTML count;
- duplicate-text ratio.

Do not tune against the held-out split during routine development. Use it only for milestone/release evaluation.

## 11.4 Layer 4: Popular-web snapshot suite

WCXB is the foundation. Build a smaller project-owned corpus to cover very popular and operationally important pages, especially layouts that change over time.

### Selection

Target approximately 300–500 pages, split by **domain**, not merely by URL.

Suggested distribution:

| Type | Count |
|---|---:|
| Articles/news/blogs | 80 |
| Documentation/API/reference | 80 |
| Forums/Q&A/issues/discussions | 70 |
| Products/specification pages | 60 |
| Listings/collections/search results | 70 |
| Service/marketing/FAQ/pricing | 50 |
| Generic/utility/wiki/repository | 40 |

Cross-cutting targets:

- at least 20% non-English;
- at least 15% JavaScript-rendered snapshots;
- at least 20% with code, tables, or complex lists;
- mobile and desktop variants for a small subset;
- malformed or unusual HTML;
- pages from both very large and smaller domains.

Use a documented popularity source such as a frozen Tranco ranking or another reproducible top-domain list. Record the ranking version and selection method. Do not scrape “the top sites” ad hoc.

### Capture

Never make live network requests in tests.

Create a separate capture tool that records:

- original URL;
- final URL after redirects;
- capture timestamp;
- HTTP status;
- response content type and charset;
- raw response HTML;
- optionally rendered DOM after a fixed browser wait policy;
- browser name/version;
- viewport;
- locale;
- user agent;
- content SHA-256;
- robots/capture notes;
- license or redistribution notes.

Prefer one fixture per final page state. If both raw and rendered HTML are retained, evaluate them separately.

Recommended layout:

```text
testdata/pages/<page-id>/
├── manifest.json
├── source.html.gz
├── rendered.html.gz        # optional
├── expected.json
├── expected.md             # only for fully golden pages
└── notes.md
```

### Annotation

Use three levels:

1. **Golden**: fully reviewed expected Markdown.
2. **Text ground truth**: full expected primary text with metadata.
3. **Snippet**: required and forbidden snippets plus structural expectations.

A practical annotation workflow:

1. Run several baseline extractors.
2. Generate an initial draft from their union/intersection.
3. Optionally use an LLM to prepare an annotation draft.
4. Require human review before marking the fixture golden.
5. Run automatic checks:
   - every required snippet exists in source;
   - every forbidden snippet exists in source;
   - no overlap between required and forbidden snippets;
   - metadata is source-supported;
   - expected content is not silently truncated.
6. Record reviewer and annotation version.

Baseline consensus must never be treated as ground truth.

### Storage

Real HTML snapshots can be large and may have redistribution constraints.

- Keep manifests and annotations in the repository.
- Store approved snapshots in Git LFS, a versioned release asset, or an external object store.
- Include license and attribution files.
- Do not commit authenticated, personalized, or private pages.
- Strip cookies, tokens, user identifiers, and response headers containing secrets.
- Obtain legal review before redistributing copyrighted snapshots.
- When redistribution is not permitted, keep only a URL manifest and private CI corpus; retain open datasets for public reproducibility.

### Refresh policy

- Freeze every corpus release.
- Never replace a fixture in place.
- Capture a new version under a new corpus version.
- Refresh the popular-page suite quarterly or when a major regression is reported.
- Maintain a small “current canary” job separately from deterministic release tests.
- A live canary failure should create a report, not fail normal unit tests.

---

## 12. Evaluation methodology

### 12.1 Text normalization

Define normalization once and version it:

- Unicode normalization policy;
- case folding for metrics only;
- whitespace collapsing;
- punctuation tokenization;
- whether repeated tokens count;
- treatment of URLs and code;
- language-independent word segmentation fallback.

Use multiset overlap rather than unique-token Jaccard. Unique-token metrics hide duplicate boilerplate and repeated content.

### 12.2 Per-page output

For every evaluated page, save:

```json
{
  "id": "page-id",
  "page_type": "documentation",
  "precision": 0.91,
  "recall": 0.87,
  "f1": 0.89,
  "required_snippets": {"passed": 8, "total": 8},
  "forbidden_snippets": {"passed": 6, "total": 7},
  "quality": 0.84,
  "runtime_ms": 11.2,
  "output_bytes": 18422,
  "fallback": "primary",
  "warnings": []
}
```

### 12.3 Regression reports

Generate:

- overall summary;
- per-page-type summary;
- worst 25 precision regressions;
- worst 25 recall regressions;
- pages whose output became empty;
- pages with unsafe links/raw HTML;
- pages whose quality score is badly calibrated;
- performance and allocation deltas;
- exact before/after extraction artifacts for failed pages.

### 12.4 Differential testing

Run the corpus against:

- current `readability`;
- the new extractor;
- a naive visible-HTML-to-Markdown baseline;
- optionally Trafilatura/jusText through benchmark adapters.

Differential output helps find regressions but is not the oracle.

---

## 13. Implementation backlog

The following tasks are ordered so an AI agent can implement them sequentially. Each task should normally be one focused pull request.

## Phase 0 — Establish the contract and baseline

### T001 — Write the module charter and behavior contract

**Purpose:** Prevent article-extractor assumptions and safety claims from leaking into the new API.

**Work:**

- Create `docs/contract.md`.
- Define goals, non-goals, supported inputs, output dialect, safety meaning, determinism, and resource limits.
- Define whether JavaScript-rendered HTML must be supplied by the caller.
- State that extracted content is untrusted data.

**Acceptance criteria:**

- The contract distinguishes syntactic safety from prompt-injection safety.
- The contract names supported structures and page types.
- Every later public option can be justified by the contract.

**Dependencies:** None.

### T002 — Create a benchmark-first repository scaffold

**Purpose:** Make measurement available before algorithm work.

**Work:**

- Initialize the Go module.
- Add package skeletons, CLI skeletons, linting, formatting, race tests, and benchmark directories.
- Add CI jobs for unit tests, fuzz smoke tests, and corpus evaluation.
- Add a machine-readable benchmark result schema.

**Acceptance criteria:**

- `go test ./...` passes offline.
- `go test -race ./...` passes.
- `go run ./cmd/evaluate -help` works.
- CI can upload benchmark reports as artifacts.

**Dependencies:** T001.

### T003 — Integrate the existing Readability baseline

**Purpose:** Establish article behavior and a baseline against which general extraction is measured.

**Work:**

- Add `github.com/ryanfowler/readability` as a benchmark dependency.
- Build an adapter that accepts the same HTML/URL pair and returns normalized text and metadata.
- Record errors, runtime, and allocation statistics.

**Acceptance criteria:**

- The adapter runs on a directory of `.html` and `.html.gz` fixtures.
- Results are deterministic.
- Baseline errors do not abort the full evaluation.

**Dependencies:** T002.

### T004 — Integrate WCXB development data

**Purpose:** Begin with a broad, licensed, real-page corpus rather than inventing one.

**Work:**

- Add a download/import command pinned to a dataset version or commit.
- Verify checksums.
- Parse ground-truth JSON and gzipped HTML.
- Preserve attribution and license.
- Add split and page-type filters.

**Acceptance criteria:**

- The evaluator processes all development pages.
- It reports the expected count by page type.
- No network access occurs after the corpus is downloaded.
- Dataset version and checksum appear in every report.

**Dependencies:** T002.

### T005 — Publish the baseline report

**Purpose:** Quantify exactly where Readability fails before designing heuristics.

**Work:**

- Run Readability and the naive visible-text baseline on WCXB development.
- Report precision, recall, F1, snippets, errors, runtime, and output ratio by page type.
- Save the lowest-scoring examples for each type.

**Acceptance criteria:**

- Report is committed under `benchmarks/reports/baseline.md`.
- At least ten representative failure modes are documented with fixture IDs.
- No new extraction heuristics are added in this task.

**Dependencies:** T003, T004.

---

## Phase 1 — Parsing, indexing, and diagnostics

### T006 — Implement bounded HTML input and parsing

**Purpose:** Safely accept untrusted HTML.

**Work:**

- Implement byte limits.
- Decode common HTML encodings.
- Parse with `x/net/html`.
- Validate the optional source URL.
- Count nodes, attributes, depth, and text.
- Return typed limit errors.

**Acceptance criteria:**

- Tests cover oversized input, excessive nodes, depth, malformed HTML, and invalid URLs.
- Parsing is deterministic.
- Limits cannot be bypassed by deeply nested or attribute-heavy HTML.

**Dependencies:** T002.

### T007 — Build a linear-time DOM index

**Purpose:** Avoid Readability-style repeated subtree scans.

**Work:**

- Assign stable preorder IDs.
- Cache parent, depth, sibling position, tag, class/id tokens, and landmark role.
- Compute bottom-up aggregates for visible text, link text, descendant tags, and controls.

**Acceptance criteria:**

- Index construction is O(nodes + text).
- A deep-DOM benchmark does not show quadratic behavior.
- Tests verify aggregate values on nested fixtures.

**Dependencies:** T006.

### T008 — Implement structured diagnostics

**Purpose:** Make heuristic decisions inspectable and testable.

**Work:**

- Define block, feature, score, reason, region, fallback, and warning diagnostics.
- Add JSON output to `cmd/inspect`.
- Keep diagnostics disabled by default to reduce allocations.

**Acceptance criteria:**

- `cmd/inspect` can show retained/removed blocks and reasons.
- Diagnostics are stable enough for regression fixtures.
- Normal extraction does not allocate large reason arrays when disabled.

**Dependencies:** T007.

### T009 — Implement page metadata extraction

**Purpose:** Separate metadata from content-region selection.

**Work:**

- Extract title, description, canonical URL, language, author, site, date, Open Graph, microdata, and JSON-LD.
- Track source and confidence for each field.
- Deduplicate conflicting metadata candidates.

**Acceptance criteria:**

- Metadata fixtures cover conflicting title/date/author sources.
- JSON-LD does not override a stronger visible source without a reason.
- Every returned metadata value has an internal provenance.

**Dependencies:** T007, T008.

---

## Phase 2 — Blocks and features

### T010 — Implement visibility and hard-exclusion rules

**Purpose:** Remove only universally unusable content before classification.

**Work:**

- Handle script/style/template, hidden, aria-hidden, inert, inline hidden styles, and modal dialogs.
- Distinguish hard exclusions from profile-aware candidates.
- Preserve source positions for diagnostics.

**Acceptance criteria:**

- Hidden content never appears in output.
- `nav`, `aside`, `form`, and comment-like classes are not universally deleted.
- Unit fixtures cover visibility edge cases.

**Dependencies:** T007, T008.

### T011 — Implement semantic block segmentation

**Purpose:** Produce independently classifiable content units.

**Work:**

- Segment headings, paragraphs, lists, definitions, code, tables, quotes, figures, semantic regions, and repeated records.
- Merge inline phrasing content.
- Prevent duplicate parent/child text blocks.

**Acceptance criteria:**

- Every visible text character belongs to at most one primary leaf block.
- Structural blocks retain their source nodes.
- Unit tests cover fragmented `div` paragraphs and deeply nested inline markup.

**Dependencies:** T010.

### T012 — Implement text and language-neutral features

**Purpose:** Capture content value without relying on article punctuation alone.

**Work:**

- Add length, words, direct-text ratio, sentence punctuation, alphabetic/numeric ratios, average word length, line shape, title similarity, and uniqueness.
- Make stopword features optional.

**Acceptance criteria:**

- Feature extraction is cached and linear.
- Documentation/code fixtures receive useful nonzero content features even with little prose.
- Tests cover CJK and no-space text without panics or empty scoring.

**Dependencies:** T011.

### T013 — Implement link and control features

**Purpose:** Distinguish meaningful linked records from navigation and controls.

**Work:**

- Calculate link density, link-label shape, destination diversity, host ratios, button/input counts, and control density.
- Add safe URL parsing but do not render yet.

**Acceptance criteria:**

- A listing fixture is not classified as boilerplate solely because of high link density.
- A header menu receives strong navigation features.
- Malformed URLs do not panic.

**Dependencies:** T011.

### T014 — Implement structured-content features

**Purpose:** Preserve information that prose-oriented extractors lose.

**Work:**

- Add heading, list, code, table, definition, key/value, price, author/time, and schema features.
- Calculate table dimensions and code-line counts.
- Detect label/value structures.

**Acceptance criteria:**

- Product spec, API reference, and command-example fixtures get positive structured-content scores.
- Layout tables are distinguishable from data tables using tested heuristics.
- Large tables respect configured analysis limits.

**Dependencies:** T011, T009.

### T015 — Implement repeated-structure detection

**Purpose:** Recognize listings, cards, comments, reviews, and repeated navigation.

**Work:**

- Define structural signatures.
- Group repeated siblings.
- Calculate group statistics.
- Classify group shape as menu-like, card-like, conversation-like, or unknown.

**Acceptance criteria:**

- Tests cover site navigation, search results, product cards, and forum posts.
- Similar classes alone are insufficient to declare repetition.
- Detection is bounded on pages with thousands of siblings.

**Dependencies:** T011, T012, T013, T014.

---

## Phase 3 — Classification and selection

### T016 — Implement deterministic page-type inference

**Purpose:** Select appropriate interpretation rules for different content shapes.

**Work:**

- Implement the initial rules for Article, Documentation, Discussion, Product, Listing, Collection, Service, and Generic.
- Combine URL, metadata, schema, and block-distribution signals.
- Return top candidates and confidence.

**Acceptance criteria:**

- Unit fixtures classify correctly.
- WCXB page-type confusion matrix is generated.
- Low-confidence pages expose multiple candidate types.
- No domain-specific selector is required.

**Dependencies:** T009, T012–T015.

### T017 — Define versioned extraction profiles

**Purpose:** Keep page-type behavior explicit and benchmarkable.

**Work:**

- Define private profile structs containing scoring weights, thresholds, expansion rules, and limits.
- Add profile version to diagnostics and benchmark reports.
- Support an internal override for experiments.

**Acceptance criteria:**

- Profiles alter interpretation, not parsing or universal safety rules.
- Profile changes are visible in benchmark diffs.
- Public API does not expose unstable individual weights.

**Dependencies:** T016.

### T018 — Implement block role scoring

**Purpose:** Assign interpretable content and boilerplate likelihoods.

**Work:**

- Calculate separate content, navigation, boilerplate, control, structured, coherence, and page-type-fit scores.
- Attach reasons for major contributions.
- Calibrate initial weights from the baseline failure analysis.

**Acceptance criteria:**

- `cmd/inspect` explains the five largest positive and negative contributions per block.
- Scores are finite for all fuzz inputs.
- Synthetic fixtures select expected blocks without site-specific rules.

**Dependencies:** T017.

### T019 — Implement region graph construction

**Purpose:** Model relationships between content blocks.

**Work:**

- Add parent/child, adjacency, heading-scope, semantic-region, and repeated-group edges.
- Identify strong boilerplate boundaries.
- Keep DOM order.

**Acceptance criteria:**

- Graph construction is linear or near-linear.
- A service page can contain several disconnected primary regions.
- A forum repeated group remains one coherent region.

**Dependencies:** T011, T015, T018.

### T020 — Implement seed-and-expand region selection

**Purpose:** Select multiple meaningful content regions.

**Work:**

- Choose primary seeds.
- Expand through compatible graph edges.
- Merge contiguous selected blocks.
- Reject regions below precision/coherence thresholds.
- Add supporting regions.

**Acceptance criteria:**

- Article fixtures normally produce one region.
- Service fixtures retain several sections.
- Listing fixtures retain repeated records.
- Header/footer regions are excluded.
- Selection decisions appear in diagnostics.

**Dependencies:** T019.

### T021 — Implement low-confidence profile ensemble

**Purpose:** Avoid brittle failures from incorrect page-type classification.

**Work:**

- Run the top plausible profiles when type confidence is below threshold.
- Score complete extraction candidates.
- Choose the highest-quality result.
- Bound total work.

**Acceptance criteria:**

- Ensemble execution has a strict maximum number of profiles.
- It improves or preserves development F1 compared with forced top-1 classification.
- Diagnostics list attempted profiles and winning rationale.

**Dependencies:** T016, T017, T020.

### T022 — Integrate Readability as an article candidate/fallback

**Purpose:** Preserve the strong article implementation without forcing article assumptions on every page.

**Work:**

- Call `readability.ParseNode` for likely articles or low-quality outputs.
- Convert its result into the new block/region representation or candidate result.
- Compare quality with the generic selector.
- Track extra parsing/cloning cost.

**Acceptance criteria:**

- Mozilla article regressions remain within the agreed threshold.
- Readability is not called on every page unless benchmarks justify it.
- The selected fallback is visible in diagnostics.

**Dependencies:** T003, T020, T021.

### T023 — Implement extraction quality scoring

**Purpose:** Identify likely bad output and enable safe fallbacks.

**Work:**

- Define quality features and a deterministic score.
- Record actual WCXB F1 alongside predicted quality.
- Add calibration plots/buckets.

**Acceptance criteria:**

- Very low-quality bucket has materially lower actual F1 than high-quality bucket.
- Empty/noisy/truncated fixtures receive low quality.
- Quality is not simply page-type confidence.

**Dependencies:** T020–T022, T004.

### T024 — Implement fallback chain and failure policy

**Purpose:** Return useful output on unusual pages without silently returning the entire page.

**Work:**

- Add semantic-main, Readability, and high-recall fallbacks.
- Define `ErrNoContent`.
- Emit warnings for truncation and fallback.

**Acceptance criteria:**

- Every fallback has tests.
- Error rate on WCXB is reported.
- High-recall fallback still excludes hard-hidden and executable content.

**Dependencies:** T022, T023.

---

## Phase 4 — Markdown conversion and safety

### T025 — Implement the internal Markdown AST

**Purpose:** Separate selection from rendering and make safety enforceable.

**Work:**

- Define nodes for headings, paragraphs, lists, code, tables, quotes, links, and image descriptions.
- Convert selected DOM regions to AST.
- Preserve source block IDs for diagnostics.

**Acceptance criteria:**

- AST contains no arbitrary raw HTML node.
- DOM conversion tests cover all supported structures.
- Unsupported elements degrade to their safe textual children.

**Dependencies:** T020.

### T026 — Implement deterministic CommonMark/GFM rendering

**Purpose:** Produce readable stable Markdown.

**Work:**

- Render headings, paragraphs, emphasis, code, quotes, lists, and tables.
- Normalize blank lines.
- Repair heading levels.
- Use robust code fences.

**Acceptance criteria:**

- Golden tests cover Markdown metacharacters in every context.
- Output is byte-for-byte deterministic.
- Re-rendering equivalent HTML produces equivalent Markdown after normalization.

**Dependencies:** T025.

### T027 — Implement link policy and URL sanitization

**Purpose:** Prevent unsafe or malformed link output.

**Work:**

- Resolve relative URLs.
- Enforce scheme allowlist.
- reject credentials/control characters/oversized URLs;
- escape link labels and destinations.
- Expose rejected-link diagnostics.

**Acceptance criteria:**

- No `javascript:`, `data:`, `file:`, or obfuscated equivalent is emitted by default.
- Fuzz tests cover URL and Markdown escaping interactions.
- Relative links resolve against page/base URL correctly.

**Dependencies:** T006, T013, T026.

### T028 — Implement table, image, and structured fallback rendering

**Purpose:** Preserve structured information without malformed Markdown.

**Work:**

- Render valid rectangular GFM tables.
- Render ragged/complex tables as labeled rows.
- Render images according to policy.
- Limit table cells and image count.

**Acceptance criteria:**

- Product specification tables remain understandable.
- Tables with spans/nested blocks degrade predictably.
- Data URLs and decorative images are omitted.
- Truncation emits warnings.

**Dependencies:** T014, T025–T027.

### T029 — Implement output budgets and semantic truncation

**Purpose:** Bound agent context and resource use.

**Work:**

- Add byte/block/link/table/repeated-item budgets.
- Truncate at region, section, block, or record boundaries.
- Never cut inside UTF-8, a code fence, link, or table row.
- Add a visible or metadata truncation marker according to options.

**Acceptance criteria:**

- Property tests prove output never exceeds the configured hard limit except for a documented minimal closing allowance.
- Markdown remains syntactically balanced after truncation.
- Diagnostics identify omitted region counts.

**Dependencies:** T026–T028.

### T030 — Add Markdown safety and round-trip tests

**Purpose:** Validate the output contract.

**Work:**

- Parse generated Markdown with a CommonMark/GFM parser in tests.
- Assert no raw HTML nodes or unsafe links.
- Add adversarial HTML fixtures.
- Add fuzzing for extraction-to-Markdown.

**Acceptance criteria:**

- Generated Markdown parses successfully for all corpus outputs.
- Safety assertions are zero-tolerance release gates.
- Fuzzing finds no panics, infinite loops, or unbounded allocation on configured limits.

**Dependencies:** T027–T029.

---

## Phase 5 — Corpus, regression, and performance engineering

### T031 — Expand Mozilla article regression metrics

**Purpose:** Ensure the general extractor does not regress the existing specialty.

**Work:**

- Reuse the 130 fixtures.
- Add ordered-token similarity, paragraph order, heading/list/code/table counts, and metadata.
- Maintain an explicit intentional-difference file.

**Acceptance criteria:**

- Every exception has a fixture, explanation, and issue/task reference.
- Article performance is reported separately from overall performance.
- Exact structure checks are used where the fixture supports them.

**Dependencies:** T022, T026.

### T032 — Implement full WCXB evaluator and report generator

**Purpose:** Make benchmark feedback actionable.

**Work:**

- Add multiset word metrics and snippet checks.
- Add per-page-type summaries.
- Save extraction artifacts for regressions.
- Compare against the baseline report.
- Add Markdown safety and structure metrics.

**Acceptance criteria:**

- One command produces JSON, CSV, and Markdown reports.
- Reports list the worst precision and recall regressions.
- The evaluator can compare two commits or result directories.

**Dependencies:** T004, T024, T030.

### T033 — Define release quality gates

**Purpose:** Prevent heuristic improvements that help one type while breaking others.

**Work:**

- Define relative and absolute thresholds.
- Suggested initial gates:
  - zero unsafe URL/raw-HTML violations;
  - zero panics/errors caused by valid corpus input;
  - overall WCXB development F1 at least 0.10 above the recorded Readability baseline;
  - no page type regresses more than 0.02 from the previous release without explicit approval;
  - article performance remains within 0.02 of the chosen article baseline;
  - p95 runtime and allocation remain within agreed multiples of baseline.
- Tighten gates as the implementation matures.

**Acceptance criteria:**

- CI fails with a clear diff when a gate is violated.
- Gates are versioned in a configuration file.
- Safety gates cannot be waived by a score improvement.

**Dependencies:** T005, T032.

### T034 — Build the popular-page manifest and capture tool

**Purpose:** Add current, operationally important layouts beyond public benchmarks.

**Work:**

- Define selection strata and frozen popularity source.
- Implement raw HTTP capture.
- Add optional rendered-DOM capture through a separate browser-backed command.
- Record metadata/checksums.
- Redact sensitive headers and state.

**Acceptance criteria:**

- The first corpus release contains at least 100 reviewed pages across all target types.
- Capture is reproducible and versioned.
- Fixtures contain no credentials, cookies, or personal account data.
- Normal tests remain offline.

**Dependencies:** T002, T006.

### T035 — Build annotation and review tooling

**Purpose:** Make human-quality corpus expansion practical.

**Work:**

- Create a local review UI or CLI showing source, candidate output, baselines, required snippets, and forbidden snippets.
- Validate annotations automatically.
- Track reviewer and annotation version.

**Acceptance criteria:**

- A reviewer can annotate a page without manually editing several files.
- Invalid snippets or unsupported metadata cannot be committed.
- Golden and snippet-only fixtures are clearly distinguished.

**Dependencies:** T034, T032.

### T036 — Add fuzz, property, and adversarial suites

**Purpose:** Harden the module against hostile HTML.

**Work:**

- Fuzz parser limits, DOM indexing, segmentation, URL handling, Markdown escaping, and truncation.
- Add generated deep/wide/repeated DOMs.
- Add catastrophic-regex and pathological-text cases.

**Acceptance criteria:**

- No unbounded regex patterns are used on attacker-controlled text.
- Fuzz smoke runs execute in CI.
- A longer scheduled fuzz job stores crashers as fixtures.

**Dependencies:** T006–T030.

### T037 — Optimize after correctness

**Purpose:** Reach crawler-grade performance without obscuring behavior prematurely.

**Work:**

- Profile CPU, allocations, and peak memory on WCXB.
- Remove repeated text extraction and regex allocations.
- Pool only where measurement proves useful.
- Benchmark optional Readability fallback overhead.
- Add `BenchmarkExtract` by page type and size.

**Acceptance criteria:**

- Performance report includes hardware, Go version, corpus version, and percentiles.
- No optimization changes output without a documented reason.
- Deep and large fixtures remain bounded.

**Dependencies:** T032, T036.

### T038 — Add concurrency-safety and API tests

**Purpose:** Make the package safe in crawlers and servers.

**Work:**

- Ensure extractors/options are immutable or request-local.
- Test concurrent calls under race detector.
- Document reuse rules.
- Avoid global mutable caches.

**Acceptance criteria:**

- `go test -race ./...` passes with concurrent corpus samples.
- Public types clearly state thread-safety.
- There is no global logger or mutable profile state.

**Dependencies:** T024, T030.

### T039 — Write user documentation and examples

**Purpose:** Make the safety and extraction behavior difficult to misuse.

**Work:**

- Add quick start.
- Add raw HTML and parsed-node examples.
- Add agent integration example that treats output as untrusted content.
- Document rendered-page responsibility.
- Document limits, page types, quality, and diagnostics.
- Explain differences from `readability`.

**Acceptance criteria:**

- Examples compile as tests.
- Documentation never claims prompt-injection protection.
- The README includes benchmark methodology, not only a headline score.

**Dependencies:** T033, T038.

### T040 — Prepare v0.1 release

**Purpose:** Ship a measurable, supportable first version.

**Work:**

- Freeze profile and corpus versions.
- Run development and held-out benchmarks.
- Review worst failures manually.
- Publish benchmark artifacts and known limitations.
- Tag the release and add a compatibility policy.

**Acceptance criteria:**

- All release gates pass.
- Held-out results are generated only for the release review.
- Known failure categories are documented.
- Public API has Go documentation and examples.

**Dependencies:** T033–T039.

---

## 14. Recommended milestone sequence

### Milestone A — Benchmark harness

Complete T001–T005.

**Exit condition:** A reproducible report shows exactly how current Readability performs by page type.

### Milestone B — Inspectable block engine

Complete T006–T015.

**Exit condition:** Every page can be rendered as a block/feature diagnostic without selection.

### Milestone C — Multi-region text extraction

Complete T016–T024.

**Exit condition:** The module materially outperforms Readability on non-article WCXB types while preserving article behavior.

### Milestone D — Safe Markdown

Complete T025–T030.

**Exit condition:** All selected content renders as valid, deterministic Markdown with zero unsafe-link/raw-HTML violations.

### Milestone E — Production hardening

Complete T031–T040.

**Exit condition:** Corpus gates, fuzzing, performance, documentation, and held-out evaluation are ready for release.

---

## 15. Rules for future heuristic changes

Every heuristic change must:

1. Include or reference a failing real fixture.
2. Explain which feature or page type it addresses.
3. Show before/after per-type benchmark results.
4. Include precision and recall, not only an overall score.
5. Avoid domain-specific selectors unless:
   - the rule represents a reusable platform/theme;
   - generic methods cannot recover the content;
   - multiple fixtures justify it;
   - it lives in an optional platform-profile layer.
6. Preserve zero-tolerance Markdown safety tests.
7. Avoid changing the public API merely to expose a temporary tuning knob.

This discipline is critical. Content extractors otherwise become collections of mutually conflicting regexes.

---

## 16. Suggested first implementation experiment

Before building the full classifier, implement a narrow experiment:

1. Parse and index the DOM.
2. Segment blocks.
3. Hard-remove hidden/executable content.
4. Give positive priors to `main`, `article`, visible headings, paragraphs, code, tables, and lists.
5. Give negative priors to header/footer/nav/dialog/form/control-heavy blocks.
6. Detect repeated sibling groups.
7. Select:
   - the best coherent region;
   - all strong top-level sections inside `main`;
   - repeated groups when they resemble posts or records.
8. Render plain text only.
9. Evaluate on WCXB.
10. Inspect the worst 20 pages per type.

This experiment will reveal whether page-type inference is immediately necessary and which features have the highest leverage. Do not build the Markdown renderer or trained classifier until this measurement exists.

---

## 17. Important design choices to defer

Defer these until the deterministic benchmarked engine is working:

- trained XGBoost or neural block classifier;
- browser layout features;
- accessibility-tree input;
- domain/platform selector packs;
- LLM fallback;
- semantic chunking for embeddings;
- extraction directly from Common Crawl WARC;
- formula/MathML-to-LaTeX conversion;
- embedded video/audio representation;
- readability-compatible HTML output.

The internal block feature vector, diagnostics, and benchmark files should make these additions possible later without redesigning the public API.

---

## 18. References

- Ryan Fowler, Go Readability port: <https://github.com/ryanfowler/readability>
- Mozilla Readability: <https://github.com/mozilla/readability>
- WCXB multi-type benchmark: <https://github.com/Murrough-Foley/web-content-extraction-benchmark>
- WCXB paper: <https://arxiv.org/abs/2605.21097>
- Trafilatura: <https://github.com/adbar/trafilatura>
- Trafilatura paper: <https://aclanthology.org/2021.acl-demo.15/>
- jusText: <https://github.com/miso-belica/justext>
- Web2Text and CleanEval-aligned data: <https://github.com/dalab/web2text>
- Web2Text paper: <https://arxiv.org/abs/1801.02607>
- CommonMark specification: <https://spec.commonmark.org/current/>
- GitHub Flavored Markdown specification: <https://github.github.io/gfm/>

---

## 19. Final implementation principle

Treat content extraction as **structured block classification and region assembly**, not as subtree selection followed by HTML-to-Markdown conversion.

Readability remains an excellent article specialist. The new module should generalize by preserving its strongest ideas—semantic hints, text and link features, ancestor context, retries, and conservative cleanup—while replacing the single-dominant-article objective with a page-type-aware, multi-region model that can retain the varied structures an AI agent actually needs.
