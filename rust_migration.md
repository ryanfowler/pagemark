# Port Pagemark from Go to Rust

You are responsible for creating a production-quality Rust implementation of the Go library at:

`https://github.com/ryanfowler/pagemark`

The Rust implementation must preserve Pagemark's observable behavior, safety properties, resource bounds, and test coverage while using idiomatic Rust architecture. Do not perform a mechanical line-by-line translation.

## Primary objective

Create a new Rust crate named `pagemark` that extracts useful content from UTF-8 HTML and returns:

* document metadata;
* restricted Markdown without raw HTML;
* plain text;
* sections;
* retained links;
* retained images;
* detected page type;
* output truncation state.

The Rust implementation must remain a pure extraction library:

* Do not fetch URLs.
* Do not run JavaScript.
* Do not trust extracted page text as instructions.
* Do not emit raw HTML in Markdown.
* Do not introduce mutable global extraction state.

## Source of truth

Before implementing anything, inspect the complete Go repository, including:

* the public API;
* all files under `internal/engine`;
* all files under `internal/markdown`;
* all files under `internal/urlutil`;
* package tests;
* command tests;
* fixtures;
* the Mozilla Readability compatibility corpus;
* the real-world regression corpus;
* documentation describing behavior and compatibility.

Treat the current Go implementation and its tests as the behavioral specification. Do not infer behavior from the README alone.

Record the exact Go commit SHA used as the migration baseline.

## Migration principles

1. Preserve behavior before optimizing.
2. Port the extraction pipeline by responsibility, not Go source file.
3. Do not change heuristics merely to make the Rust implementation easier.
4. Do not expose internal DOM or scoring details in the public API.
5. Avoid dependencies that are not suitable for hostile production HTML.
6. Keep all extraction deterministic.
7. Make intentional behavior differences explicit and test them.
8. Prefer clear, deep modules over many shallow wrapper abstractions.
9. Follow “A Philosophy of Software Design” when defining module boundaries.
10. Use clear technical English in documentation and comments.

## Public API

Design an idiomatic Rust API approximately like this:

```rust
pub fn extract(
    html: &str,
    page_url: Option<&url::Url>,
    options: &Options,
) -> Result<Document, Error>;

pub fn extract_bytes(
    html: &[u8],
    page_url: Option<&url::Url>,
    options: &Options,
) -> Result<Document, Error>;
```

Provide owned, serializable public result types:

```rust
pub struct Document {
    pub url: Option<Url>,
    pub canonical_url: Option<Url>,
    pub title: Option<String>,
    pub description: Option<String>,
    pub author: Option<String>,
    pub site_name: Option<String>,
    pub language: Option<String>,
    pub published_time: Option<String>,
    pub page_type: PageType,
    pub markdown: String,
    pub text: String,
    pub sections: Vec<Section>,
    pub links: Vec<Link>,
    pub images: Vec<Image>,
    pub truncated: bool,
}
```

Adjust optionality only where necessary, but preserve serialized compatibility with the Go JSON representation where practical.

Use:

* a non-exhaustive error enum;
* `thiserror` for error implementations;
* `serde` behind an enabled-by-default or clearly documented feature if appropriate;
* `url::Url` internally for URL handling;
* an `Options` struct with `Default`;
* enums for `PageType` and `SelectionMode`;
* a dedicated `UrlPolicy` type.

Do not copy Go's functional-option pattern unless there is a compelling Rust-specific reason.

Do not expose a parsed-node extraction function in the initial public API. Keep the DOM implementation private so it can change without breaking users.

## Required option semantics

Preserve the Go implementation's defaults and behavior:

* maximum input size: 10 MiB;
* maximum Markdown output: 2 MiB;
* fixed element and depth bounds;
* links enabled;
* images enabled;
* tables enabled;
* balanced selection mode;
* automatic page-type detection;
* HTTP and HTTPS allowed by the default resource URL policy;
* default resource URL length limit of 4,096 bytes;
* optional tracking-parameter stripping;
* output truncation only at complete block boundaries;
* truncation is nonfatal;
* invalid options are reported distinctly;
* input, element, and depth limit failures are typed;
* the caller-supplied page URL and canonical URL always have credentials removed;
* the configurable resource URL policy applies to output links and images, but not to the document URL or canonical URL.

Represent “default”, “unlimited”, and explicit limits idiomatically in Rust. Do not preserve sentinel integers in the public API merely for Go compatibility.

## DOM architecture

Use `html5ever` as the HTML5 parser.

Do not use `markup5ever_rcdom` as the production DOM.

Implement a private, arena-backed DOM using stable node identifiers:

```rust
struct NodeId(u32);

struct Dom {
    nodes: Vec<Node>,
}
```

Each node must provide efficient access to:

* its parent;
* children in source order;
* previous and next siblings where needed;
* element name;
* attributes;
* text;
* node kind.

Implement the `html5ever` tree sink needed to construct this arena.

The DOM must support malformed real-world HTML safely and deterministically. Enforce element-count and depth limits during or immediately after tree construction, matching the Go behavior as closely as possible.

Keep node-specific analysis state outside immutable DOM nodes. Prefer vectors indexed by `NodeId` over hash maps when the state is dense.

Examples include:

```rust
struct Analysis {
    node_states: Vec<NodeState>,
    hidden: BitSet,
    title_excluded: BitSet,
    blocks: Vec<Block>,
}
```

Use hash maps only for genuinely sparse or keyed data.

## Extraction pipeline

Implement distinct internal stages:

1. Input validation and bounds.
2. HTML parsing.
3. Immutable DOM evidence collection.
4. Base URL discovery.
5. Metadata extraction.
6. Structural segmentation into candidate blocks.
7. Special-case detection such as text listings.
8. Page-type inference.
9. Block scoring.
10. Content selection.
11. Article retry profiles.
12. Semantic fallbacks.
13. High-recall and metadata fallbacks.
14. Document-title separation.
15. Restricted Markdown conversion.
16. Plain-text and section generation.
17. Link and image collection.
18. Output truncation.
19. Final result validation.

Maintain the same ordering dependencies as the Go implementation. In particular, do not combine page classification, scoring, selection and rendering into one opaque traversal.

Port the alternate `<noscript>` behavior. The Go implementation may reparse with scripting disabled when the primary extraction is empty or very small. Reproduce this behavior or document and test an equivalent implementation.

## Markdown renderer

Implement a private Markdown renderer rather than delegating all output to a generic HTML-to-Markdown crate.

The renderer must preserve Pagemark's restricted Markdown contract, including:

* no raw HTML;
* normalized whitespace;
* headings;
* paragraphs;
* lists;
* block quotes;
* code and preformatted blocks;
* links subject to URL policy;
* useful images subject to URL policy;
* tables when enabled;
* table content without table syntax when disabled;
* complete-block output truncation;
* title removal;
* plain-text output;
* section construction;
* retained-link and retained-image collection.

A generic conversion crate may be used only for reference or isolated helpers. It must not define Pagemark's externally visible output.

## URL handling

Port the URL policy as its own module.

Cover:

* absolute and relative URL resolution;
* base elements;
* canonical URLs;
* allowed schemes;
* ASCII case normalization of schemes;
* duplicate allowed schemes;
* credential removal;
* control-character rejection;
* maximum source URL length;
* tracking-parameter stripping;
* unsafe and nonhierarchical URLs;
* distinction between page/canonical URLs and output-resource URLs.

Do not implement URL parsing manually when the `url` crate provides the required behavior. Add explicit compatibility handling where Go's `net/url` and Rust's `url` crate differ.

## Errors

Define structured errors that callers can match without parsing strings.

At minimum distinguish:

* no content;
* invalid page URL;
* invalid option;
* invalid UTF-8 where applicable;
* input-byte limit;
* element-count limit;
* depth limit;
* parse or internal extraction failure.

Preserve resource, observed count and configured maximum for limit errors.

Do not use `anyhow::Error` in the public library API.

## Differential test harness

Build a differential harness before porting complex scoring behavior.

Add or reuse a small Go command that:

1. accepts HTML, page URL and extraction options;
2. runs the current Go implementation;
3. emits canonical JSON containing either a document or a structured error.

Add a Rust test tool with the same input and output protocol.

For every fixture, compare Go and Rust results.

Compare:

* success versus error;
* error category;
* limit resource, count and maximum;
* URL;
* canonical URL;
* title;
* description;
* author;
* site name;
* language;
* published time;
* page type;
* Markdown;
* text;
* sections;
* links;
* images;
* truncation.

During development, write mismatches to categorized reports:

* parser or DOM;
* normalization;
* metadata;
* page classification;
* selection;
* Markdown rendering;
* URL handling;
* resource limit;
* unexplained.

The final compatibility suite must fail on unexplained differences.

## Testing strategy

Port tests in this order:

1. Text and whitespace normalization.
2. URL validation and resolution.
3. Options and errors.
4. Metadata.
5. Markdown conversion.
6. DOM bounds and malformed HTML.
7. Segmentation.
8. Page-type classification.
9. Block scoring and selection.
10. Fallback behavior.
11. Title handling.
12. End-to-end fixtures.
13. Mozilla compatibility corpus.
14. Real-world regression corpus.
15. CLI behavior, if the CLI is included.

Retain source HTML fixtures whenever licensing allows. Avoid rewriting fixtures into Rust source strings unnecessarily.

Normal unit tests must not require external network access.

Use property or fuzz testing for:

* arbitrary byte input;
* malformed nesting;
* extreme depth;
* very many elements;
* unusual Unicode;
* invalid URLs;
* large attributes;
* Markdown escaping;
* table shapes;
* repeated extraction.

The crate must never panic on arbitrary input supplied to its public extraction functions.

## Compatibility milestones

Implement the migration in reviewable milestones.

### Milestone 1: Skeleton

Deliver:

* crate structure;
* public types;
* options;
* errors;
* URL policy;
* baseline CI;
* formatting, linting and documentation configuration.

### Milestone 2: Parser and DOM

Deliver:

* arena DOM;
* `html5ever` tree sink;
* traversal helpers;
* input, element and depth limits;
* parser-focused tests.

### Milestone 3: Metadata and Markdown

Deliver:

* metadata extraction;
* URL resolution;
* restricted Markdown renderer;
* text, sections, links and images;
* truncation behavior.

### Milestone 4: Core extraction

Deliver:

* evidence;
* segmentation;
* classification;
* primary scoring;
* selection.

### Milestone 5: Full parity

Deliver:

* alternate scoring profiles;
* semantic fallbacks;
* article reconstruction;
* special page handling;
* title separation;
* `<noscript>` fallback;
* differential test reports.

### Milestone 6: Corpus and optimization

Deliver:

* Mozilla corpus results;
* real-world corpus results;
* benchmarks against Go;
* profiling;
* justified optimizations;
* migration documentation.

Each milestone must leave the crate compiling, formatted and tested. Do not submit one enormous translation commit.

## Performance

First establish correctness. Then benchmark:

* parse time;
* evidence construction;
* segmentation and scoring;
* Markdown rendering;
* total extraction time;
* peak memory;
* allocations where measurable.

Compare Rust and Go on:

* small article;
* large article;
* documentation page;
* discussion;
* product page;
* listing;
* malformed HTML;
* maximum-size input;
* corpus aggregate.

Do not use unsafe code merely to outperform the Go version. Any unsafe code requires a specific documented need, focused tests and a safety explanation.

Likely optimization opportunities after parity include:

* dense node-state vectors;
* compact node IDs;
* interned or atomized tag names;
* reusable traversal buffers;
* reduced text normalization allocations;
* writing Markdown directly into a bounded buffer;
* pre-sized vectors based on evidence counts;
* replacing sparse hash sets with bit sets.

Do not add these prematurely.

## Dependency policy

Use a small, justified dependency set. Likely dependencies include:

* `html5ever`;
* `url`;
* `thiserror`;
* `serde`;
* `serde_json` for tests and diagnostics.

Before adding another dependency, explain:

* what functionality it provides;
* why implementing that functionality locally would be worse;
* whether it handles untrusted input;
* whether it becomes part of the public API;
* its maintenance and licensing status.

Do not use `markup5ever_rcdom` for the production DOM.

## Code quality requirements

The final crate must pass:

```sh
cargo fmt --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test --all-features
cargo doc --no-deps
```

Also add:

* an MSRV policy;
* CI on the MSRV and current stable Rust;
* dependency license checking;
* vulnerability auditing;
* fuzz targets or documented fuzz commands;
* benchmarks;
* crate-level documentation;
* examples equivalent to the Go README examples.

Avoid excessive comments that restate code. Document invariants, scoring rationale, safety rules, compatibility decisions and non-obvious ownership choices.

## Required deliverables

Provide:

1. The complete Rust crate.
2. A `MIGRATION.md` containing:

   * the baseline Go commit;
   * architecture mapping;
   * deliberate API differences;
   * known behavioral differences;
   * compatibility results;
   * performance results.
3. A machine-readable compatibility report.
4. A differential test command.
5. Corpus test instructions.
6. Benchmark instructions.
7. A list of unresolved differences, each with:

   * fixture;
   * observed Go result;
   * observed Rust result;
   * suspected subsystem;
   * proposed resolution.
8. A concise release-readiness assessment.

## Definition of done

The migration is complete only when:

* the public Rust API is documented and stable enough for an initial release;
* all ordinary tests pass;
* the extractor does not panic under fuzzed input;
* resource limits are enforced;
* URL safety behavior is covered;
* Markdown contains no raw HTML;
* output is deterministic;
* normal tests use no external network;
* the differential corpus has no unexplained material differences;
* known differences are documented and approved;
* performance is measured rather than guessed;
* the code is organized as an idiomatic Rust implementation, not a transliteration of Go.

Begin by inspecting the repository and producing a short implementation plan and architecture map. Then implement Milestone 1 and continue through the milestones without redesigning extraction behavior unless a verified incompatibility makes that necessary.

