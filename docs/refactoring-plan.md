# Pagemark Internal Design Refactoring Plan

## Purpose

Refactor Pagemark to reduce internal complexity without changing its public behavior. Apply the design principles from John Ousterhout's *A Philosophy of Software Design*:

- Create deep modules with small interfaces.
- Hide implementation decisions.
- Remove temporal coupling.
- Keep related knowledge together.
- Prefer semantic types over encoded primitive values.
- Do not add shallow wrappers or speculative abstractions.

This document is self-contained. An agent can select one task and implement it using only the repository and the instructions in that task.

## Repository summary

Pagemark is a Go module that extracts useful content and metadata from HTML. The public package is `github.com/ryanfowler/pagemark`. Most extraction logic is in `internal/engine`. HTML-to-Markdown conversion is in `internal/markdown`.

Important current files:

- `api.go`: public aliases and forwarding functions.
- `internal/engine/extract.go`: extraction entry points and pipeline orchestration.
- `internal/engine/analysis.go`: shared mutable extraction state.
- `internal/engine/evidence.go`: immutable DOM evidence index.
- `internal/engine/page_type.go`: page-type evidence collection and scoring.
- `internal/engine/score.go`: block scoring and retry profiles.
- `internal/engine/fallback.go`: semantic and high-recall fallbacks.
- `internal/engine/article_auxiliary.go`: article auxiliary-region policies.
- `internal/engine/title.go`: title resolution and title removal.
- `internal/engine/metadata.go`: HTML, microdata, and JSON-LD metadata extraction.
- `internal/markdown/markdown.go`: HTML conversion, intermediate tree, pruning, and rendering.
- `cmd/diagnose-blocks`: internal scoring diagnostics command.

## Global constraints

Every task must follow these constraints unless the task explicitly says otherwise.

1. Preserve the public API and documented behavior.
2. Preserve extraction results for the existing test corpus.
3. Do not change scoring weights, thresholds, fallback order, URL rules, or resource limits during structural tasks.
4. Do not add mutable package-level extraction state.
5. Preserve concurrent extraction safety.
6. Do not add an interface unless it hides a meaningful implementation or has at least two real implementations, such as a recorder and a no-op recorder.
7. Do not create a package for a single trivial type or forwarding function.
8. Keep performance-sensitive behavior documented in the existing code. In particular, do not route `ExtractBytes` through `Extract`, because that would copy the full input.
9. Use semantic names instead of generic names such as `manager`, `processor`, or `util`.
10. Run formatting and tests before completing a task.

## Required validation

Run these commands for every task:

```sh
gofmt -w .
go test ./...
go test -race ./...
```

If `staticcheck` is installed, also run:

```sh
staticcheck ./...
```

For changes to extraction, selection, title handling, metadata, or Markdown output, also run the existing benchmark or regression tests relevant to the changed package. Do not update expected output merely to make a structural refactor pass.

---

# Task 1: Introduce typed memoized values

## Objective

Hide the primitive `uint8` encodings used by `internal/engine/nodeState`. Policy code must no longer know that `0` means unknown, `1` means false, and `2` means true. Count caches must not use an unexplained `count + 1` encoding at call sites.

## Current problem

`internal/engine/analysis.go` defines `nodeState` with many `uint8` fields. Code in files such as `article_auxiliary.go` and `metadata.go` reads and writes numeric values directly. Some fields are tri-state booleans, while other fields are cached counts. This representation saves memory but spreads obscure implementation knowledge through the engine.

## Required changes

1. Add a private semantic type for a memoized boolean. Use named constants for unknown, false, and true.
2. Provide methods that:
   - Read a cached boolean and return `(value, known bool)`.
   - Store a boolean without exposing its numeric representation.
3. Add a separate private type for memoized bounded counts.
4. Convert all boolean cache fields in `nodeState` to the memoized boolean type.
5. Convert `commentCount` and `articleCardCount` to the memoized count type.
6. Replace all direct comparisons and assignments involving the old numeric encodings.
7. Keep one `map[*html.Node]nodeState` unless benchmarks prove that another representation is better.
8. Add focused unit tests for both cache types, including zero, true, false, count zero, maximum retained count, and saturation behavior.

## Non-goals

- Do not change which values are cached.
- Do not split the node-state map into many maps.
- Do not change auxiliary classification behavior.
- Do not change cache saturation limits.

## Acceptance criteria

- No policy method compares a memoized boolean field to numeric literals.
- No policy method manually adds or subtracts one to encode a cached count.
- Existing extraction and performance-regression tests pass without expected-output changes.

---

# Task 2: Reorganize article auxiliary policy by domain

## Objective

Make article auxiliary-region policy navigable by keeping each policy domain and its vocabulary together. Preserve one cohesive package and preserve all behavior.

## Current problem

`internal/engine/article_auxiliary.go` is more than 3,000 lines and combines unrelated policies for navigation, advertisements, marketing, subscriptions, comments, cards, taxonomy, mastheads, and organization profiles.

## Required changes

Move existing declarations into the following files in `internal/engine`:

- `auxiliary_base.go`: general boilerplate tokens, irrelevant-node checks, cache access, and page-type-independent policy.
- `auxiliary_navigation.go`: navigation, breadcrumbs, tables of contents, mastheads, and taxonomy controls.
- `auxiliary_marketing.go`: advertisements, social cards, related content, marketing regions, and organization profiles.
- `auxiliary_subscription.go`: subscription regions, subscription scanners, forms, email evidence, and join calls to action.
- `auxiliary_comments.go`: article comment regions, comment records, empty comment controls, and discussion headings.
- `auxiliary_cards.go`: article cards, promotional cards, repeated card counting, and card-region helpers.
- `auxiliary_article.go`: final article-specific composition policy and helpers that genuinely span the preceding domains.

Use the existing function names where practical. Move each helper with the policy knowledge it supports. Keep shared low-level DOM helpers in their current general-purpose file if they are not specific to auxiliary classification.

After moving code, add a short package-internal comment near the final composition method that states the policy order:

1. Base exclusions.
2. Page-type-specific exclusions.
3. Trailing or repeated auxiliary-region exclusions.

## Non-goals

- Do not rewrite predicates.
- Do not change predicate order.
- Do not introduce a generic rule engine or plugin interface.
- Do not move these files into separate Go packages.
- Do not rename all helpers merely for consistency.

## Acceptance criteria

- `internal/engine/article_auxiliary.go` no longer exists, or contains only a small high-level composition section.
- Each policy vocabulary, scanner, and decision function is in the same file as its domain.
- No existing test expectation changes.

---

# Task 3: Separate page evidence collection from page-type scoring

## Objective

Turn page-type classification into a pure policy decision over a compact semantic evidence value.

## Current problem

`analysis.inferType` in `internal/engine/page_type.go` traverses blocks, discovers records, computes text totals, reads metadata, applies score weights, ranks candidates, and computes confidence in one method. Traversal policy and classification policy cannot be tested independently.

## Required design

Introduce private values similar to:

```go
type pageEvidence struct {
    proseChars               int
    codeChars                int
    inferenceChars           int
    narrativeProseChars      int
    longNarrativeParagraphs  int
    primaryArticleProseChars int
    discussionRecords        int
    discussionProseChars     int
    listingRecords           int
    listingRecordChars       int
    productRecords           int
    productRegions           int
    sectionCount             int

    documentationContext bool
    documentationPath    bool
    discussionContext    bool
    hasTextListing       bool

    // Include explicit metadata and schema evidence with semantic names.
}

type pageClassification struct {
    pageType   PageType
    confidence float64
    candidates []PageCandidate
}
```

Field names can differ, but each field must represent domain evidence rather than an implementation detail.

Implement three stages:

```go
func (a *analysis) collectPageEvidence() pageEvidence
func classifyPage(e pageEvidence, wantCandidates bool) pageClassification
func rankPageTypes(scores map[PageType]float64, wantCandidates bool) pageClassification
```

Requirements:

1. `collectPageEvidence` may inspect the DOM, metadata, blocks, and engine caches.
2. `classifyPage` must be pure. It must not access `analysis`, DOM nodes, global mutable state, or caches.
3. Keep all existing score values and conditions unchanged.
4. Preserve deterministic tie-breaking.
5. Preserve the optimization that allocates and sorts the full candidate list only when diagnostics request it.
6. Add table-driven unit tests for `classifyPage` using synthetic `pageEvidence` values. Cover at least:
   - Generic default.
   - Strong article metadata and prose.
   - Documentation path and context.
   - Repeated substantive discussion records.
   - Product schema.
   - Listing schema.
   - Service schema.
   - Text archive listing.
   - Deterministic tie-breaking.

## Non-goals

- Do not tune classification quality.
- Do not alter score weights.
- Do not generalize classification into a data-driven rule language.
- Do not expose `pageEvidence` publicly.

## Acceptance criteria

- The pure classifier can be tested without HTML or `analysis`.
- Existing page-type results and diagnostic candidate scores remain unchanged.
- `inferType`, if retained as a compatibility helper, only delegates to evidence collection and pure classification.

---

# Task 4: Encapsulate scoring, retries, and fallbacks in a selector

## Objective

Create one deep content-selection module. The extraction orchestrator must not know scoring-profile order, retry criteria, or fallback thresholds.

## Current problem

`extractNode` in `internal/engine/extract.go` coordinates primary scoring, rendered-Markdown handling, relaxed profiles, semantic fallback, article fallback, region reconstruction, high-recall fallback, and metadata fallback. This exposes selection implementation details at the top level.

## Required design

Add private types similar to:

```go
type selection struct {
    roots    []*html.Node
    quality  float64
    strategy string
    profile  string
}

type selector struct {
    analysis *analysis
}

func (s *selector) select(pageType PageType) selection
```

The exact representation can differ. The selector must own:

- Primary scoring.
- Rendered-Markdown root selection.
- Relaxed article retry profiles.
- Attempt comparison and restoration.
- Semantic-main fallback.
- Semantic-article fallback.
- Article-region reconstruction.
- High-recall fallback.
- Metadata fallback.
- Final quality and strategy names used by diagnostics.

Requirements:

1. Preserve the current order and conditions exactly.
2. Preserve block diagnostic population after the winning scoring state is restored.
3. Preserve `ErrNoContent` behavior when all selection paths fail.
4. Keep title resolution and Markdown conversion outside the selector.
5. Add focused selector tests that verify strategy selection for representative fixtures. Use existing fixtures where possible.
6. Keep selection state private to `internal/engine`.

## Non-goals

- Do not tune thresholds.
- Do not combine title resolution with selection.
- Do not create a public selector interface.
- Do not add strategy plugins.

## Acceptance criteria

- `extractNode` contains one high-level call for content selection.
- `extractNode` does not iterate scoring profiles.
- `extractNode` does not contain article-region quality thresholds or fallback-order logic.
- Existing diagnostics report the same winning profile, fallback name, scores, and selected blocks.

---

# Task 5: Replace the broad analysis state with explicit pipeline stages

## Objective

Reduce temporal coupling in `analysis`. Make the required stage order visible in construction and function signatures.

## Prerequisite

Complete Tasks 3 and 4 first.

## Current problem

The `analysis` type contains input facts, metadata, segmentation state, classification state, selection state, title state, caches, and diagnostics. Many methods work only after an implicit sequence of earlier mutations.

## Required design

Introduce an immutable or append-only inspected-page value:

```go
type inspectedPage struct {
    root     *html.Node
    pageURL  *url.URL
    baseURL  *url.URL
    evidence *evidenceIndex
    metadata metadata
    blocks   []block
}
```

The final fields can differ. The design must separate:

- Input and immutable evidence.
- Classification caches.
- Selection caches and mutable block scoring.
- Output/title state.

Requirements:

1. Add one stage function that validates the URL, builds evidence, discovers the base URL, extracts metadata, segments blocks, and detects text listings.
2. Return a complete inspected-page value only after those operations succeed.
3. Move page-type-dependent caches and methods into classification or selection state as appropriate.
4. Remove fields from `analysis` that merely carry a result from one pipeline stage to another.
5. Prefer explicit parameters over reading unrelated mutable fields.
6. Do not copy the DOM or all blocks merely to establish stage types.
7. If retaining the name `analysis`, narrow it to one stage and document that stage.

## Non-goals

- Do not make every structure immutable at the cost of large copies.
- Do not add constructors that only assign fields.
- Do not create interfaces for pipeline stages.
- Do not change extraction policy.

## Acceptance criteria

- A reader can identify the valid pipeline order from `extractNode` alone.
- Classification cannot run before inspection without constructing an invalid private value manually.
- Selection-specific mutable state is not stored in the immutable page description.
- Existing tests and performance gates pass.

---

# Task 6: Decouple title resolution from Markdown configuration

## Objective

Make title resolution depend on semantic selected content, not on Markdown renderer configuration.

## Prerequisite

Complete Task 4 first. Task 5 is recommended but not required.

## Current problem

Many methods in `internal/engine/title.go` accept `markdown.Config` and call Markdown conversion to answer semantic questions such as whether a heading is visible or whether substantive prose remains. Changes to formatting can therefore change title policy.

## Required design

Introduce a private resolved-document value similar to:

```go
type resolvedDocument struct {
    title      string
    roots      []*html.Node
    exclusions map[*html.Node]struct{}
}
```

Add semantic queries in the engine for the information title resolution needs, for example:

```go
func selectedVisibleText(n *html.Node, excluded nodeSet, hidden func(*html.Node) bool) string
func selectedHasSubstantiveContent(roots []*html.Node, excluded nodeSet) bool
func firstSelectedHeading(roots []*html.Node, excluded nodeSet) *html.Node
```

Requirements:

1. Remove `markdown.Config` from all title-resolution function signatures.
2. Do not call `markdown.Convert` from title policy.
3. Preserve heading equivalence, title restoration, heading demotion, and title-removal behavior.
4. Preserve the rule that the document title does not occur again in Markdown, text, sections, links, or images.
5. Keep heading removal at the DOM or selection level. Do not post-process rendered Markdown text.
6. Add focused tests for:
   - A selected title heading followed by prose.
   - A browser title with site decoration.
   - An article whose title must be restored.
   - Conflicting selected H1 elements.
   - A title-only result with a very small output budget.
   - An image-only accessible title.

## Non-goals

- Do not redesign title heuristics.
- Do not change title equivalence rules.
- Do not move title logic into `internal/markdown`.

## Acceptance criteria

- `internal/engine/title.go` does not import `internal/markdown`.
- Title tests and full extraction corpus tests pass without expected-output changes.
- Markdown conversion occurs only after title resolution is complete.

---

# Task 7: Split Markdown conversion into build and render phases

## Objective

Give `internal/markdown` two clear internal modules: one that builds a safe semantic document tree from HTML, and one that renders bounded outputs from that tree.

## Current problem

`internal/markdown/markdown.go` combines DOM filtering, HTML conversion, math, tables, media recovery, normalization, pruning, URL policy, byte budgeting, Markdown rendering, plain text, sections, and retained media.

## Required design

Keep one Go package named `markdown`. Introduce two explicit internal phases:

```go
func buildDocument(nodes []*html.Node, cfg buildConfig) *node
func renderDocument(doc *node, cfg renderConfig) Result
```

`Convert` can remain the package entry point and compose these phases.

Reorganize declarations into cohesive files:

- `convert.go`: package entry point, converter state, block and inline conversion.
- `node.go`: private semantic tree types and node predicates.
- `math.go`: MathML and TeX handling.
- `table.go`: semantic and layout table conversion.
- `media.go`: image handling and serialized media recovery.
- `url.go`: safe URL resolution and tracking removal.
- `normalize.go`: text and whitespace normalization.
- `prune.go`: empty-section and standalone-heading pruning.
- `render.go`: ordinary Markdown and inline rendering.
- `budget.go`: byte-bounded block and code rendering.
- `plain.go`: plain-text rendering and section extraction.
- `result.go`: result and retained media values.

Requirements:

1. Make `Kind` and `Node` private unless another package has a demonstrated need to construct them.
2. Keep HTML-specific state out of the render phase.
3. Keep output-byte budgeting out of the build phase.
4. Preserve the guarantee that byte limits keep complete blocks.
5. Preserve `DiscardedContent`, `Truncated`, emitted block counts, links, images, sections, and rejected URL reporting.
6. Preserve URL safety and raw-HTML exclusion.
7. Add direct tests for `buildDocument` and `renderDocument` where this provides clearer failure localization than testing only `Convert`.

## Non-goals

- Do not create separate Go packages for the build and render phases.
- Do not replace the semantic tree with a third-party Markdown AST.
- Do not change rendered Markdown.
- Do not tune table, math, or media heuristics.

## Acceptance criteria

- `markdown.go` is removed or reduced to a small package entry point.
- Rendering code does not inspect `html.Node` values.
- Building code does not implement output-byte budgets.
- All Markdown golden tests and benchmarks pass unchanged.

---

# Task 8: Replace metadata priority fields with candidate resolution

## Objective

Centralize metadata precedence and keep source-specific parsing separate from field resolution.

## Current problem

`internal/engine/metadata.go` mutates `metadata` directly while tracking parallel fields such as `titlePriority`, `descriptionPriority`, `authorPriority`, and `publishedPriority`. Precedence knowledge is spread across HTML, Open Graph, microdata, and JSON-LD branches.

## Required design

Add private semantic source and candidate types:

```go
type metadataSource uint8

type stringCandidate struct {
    value    string
    source   metadataSource
    priority uint8
}

func (c *stringCandidate) offer(value string, source metadataSource, priority uint8)
```

Use named source constants such as browser title, visible heading, HTML metadata, Open Graph, microdata, and JSON-LD. Exact names can differ.

Requirements:

1. Replace parallel priority fields in `metadata` with candidate collection or a dedicated metadata collector.
2. Keep final `metadata` as resolved values plus classification evidence.
3. Centralize the rule that a higher-priority nonempty candidate replaces a lower-priority candidate.
4. Preserve source-specific validation, including plausible descriptions and canonical URL safety.
5. Keep page-classification evidence, such as `schemaProduct` and `articlePublished`, distinct from user-visible metadata values.
6. Split metadata parsing into cohesive files if useful:
   - `metadata_html.go`
   - `metadata_microdata.go`
   - `metadata_jsonld.go`
   - `metadata_resolve.go`
7. Add table-driven tests for candidate precedence and equal-priority behavior.
8. Preserve deterministic handling of duplicate or split JSON-LD entities.

## Non-goals

- Do not implement a complete JSON-LD processor.
- Do not change metadata precedence.
- Do not parse `PublishedTime` into a timestamp.
- Do not broaden canonical URL schemes.

## Acceptance criteria

- Final metadata values match the existing corpus.
- No resolved metadata struct contains parallel numeric priority fields.
- Metadata precedence can be tested without parsing a complete HTML document.

---

# Task 9: Remove diagnostic lifecycle state from Document

## Objective

Return diagnostic data beside the extraction result instead of temporarily storing it in `Document`.

## Current problem

`internal/engine/Document` contains an unexported `diagnostic` field. Detailed extraction populates it during ordinary extraction, copies it into a report, and clears it before returning. This creates temporal coupling between extraction and diagnostics.

## Required design

Introduce an internal result similar to:

```go
type extractionResult struct {
    document *Document
    report   *DiagnosticReport
}
```

Use a recorder abstraction only if it has two concrete implementations:

- A no-op recorder for ordinary extraction.
- A diagnostic recorder for detailed extraction.

Requirements:

1. Remove the `diagnostic` field from `Document`.
2. Make ordinary extraction and detailed extraction call the same core pipeline.
3. Ensure ordinary extraction does not allocate block reason slices or a diagnostic report.
4. Preserve `ExtractDetailedBytes` behavior used by `cmd/diagnose-blocks`.
5. Preserve input-byte statistics for both `Extract` and `ExtractBytes`.
6. Preserve all report fields and strategy names.
7. Add tests proving that ordinary and detailed extraction return equivalent documents.

## Non-goals

- Do not make diagnostics part of the stable root public API.
- Do not remove `cmd/diagnose-blocks`.
- Do not collect diagnostics unconditionally.

## Acceptance criteria

- `Document` contains only user-facing result state.
- No code clears diagnostic state from a completed document.
- Ordinary extraction benchmarks show no material allocation regression.
- Diagnostic command tests and engine tests pass.

---

# Task 10: Deepen the public-to-engine package boundary

## Objective

Replace the current type-alias and forwarding boundary with a stable public contract and one substantive internal engine entry point.

## Prerequisite

Complete Tasks 4, 5, and 9 first. This task intentionally comes last because it changes many type locations.

## Current problem

`api.go` aliases public types directly to `internal/engine` types and forwards each public function and option constructor. The root package is shallow, while the aliases couple the public contract to engine representation.

## Required design

Keep `internal/engine` as a deep implementation module. Make the root package own concrete public types:

- `PageType`
- `Document`
- `Section`
- `Link`
- `Image`
- `SelectionMode`
- `URLPolicy`
- `Option`
- `LimitResource`
- `LimitError`

The root package must also own public constants and sentinel errors.

Define one explicit internal request and result boundary. For example:

```go
// internal/engine

type Config struct {
    PageType       PageType
    SelectionMode  SelectionMode
    MaxInputBytes  int64
    MaxOutputBytes int
    IncludeLinks   bool
    IncludeImages  bool
    IncludeTables  bool
    URLPolicy      URLPolicy
}

type Result struct {
    // Internal result fields.
}
```

The exact API can differ, but the root package should convert public configuration once, call one deep engine operation, and convert the result once.

Requirements:

1. Remove public type aliases to `internal/engine`.
2. Keep all existing public function signatures source-compatible where possible.
3. Preserve `errors.Is` and `errors.As` behavior for all public errors.
4. Preserve JSON field names and omission behavior.
5. Preserve functional option order, defensive URL-policy copying, nil-option handling, and validation timing.
6. Do not expose internal diagnostics through the root package.
7. Avoid one internal forwarding function per public option. Convert the completed root configuration at the extraction boundary.
8. Add compile-time and behavior tests for the public API, including reusable options and concurrent extraction.
9. Update package comments to describe the real boundary rather than aliases.

## Non-goals

- Do not redesign the user-facing API.
- Do not remove functional options.
- Do not move all engine implementation into the root package.
- Do not expose engine configuration publicly.

## Acceptance criteria

- `api.go` contains no aliases to `internal/engine` types.
- Public API examples compile without changes.
- The root package performs one configuration conversion and one result conversion per extraction.
- `internal/engine` exposes a small, substantive interface to the root package.
- The diagnostic command remains functional without making diagnostic types public at the root.

---

# Task 11: Simplify extraction entry-point orchestration

## Objective

Finish the refactor by making extraction orchestration show only the stable pipeline stages and error boundaries.

## Prerequisites

Complete Tasks 3 through 10.

## Required target shape

The core node extraction function should be structurally equivalent to:

```go
func extractNode(root *html.Node, rawURL string, cfg config, recorder traceRecorder) (*Document, error) {
    page, err := inspectPage(root, rawURL, cfg)
    if err != nil {
        return nil, err
    }

    classification := classifyInspectedPage(page, cfg)
    selected, err := selectContent(page, classification, cfg, recorder)
    if err != nil {
        return nil, err
    }

    resolved := resolveDocument(page, selected, classification)
    return formatDocument(resolved, cfg, recorder)
}
```

Names can differ. The function must make these stages clear:

1. Inspect and index input.
2. Classify the page.
3. Select content.
4. Resolve title and metadata.
5. Format the result.

Requirements:

1. Move stage-specific thresholds and retry loops behind their stage modules.
2. Keep URL validation and resource-limit errors at clear boundaries.
3. Keep the `noscript` second-parse policy in the byte-input layer, not the node pipeline.
4. Keep input-size enforcement in `Extract` and `ExtractBytes`, not `ExtractNode`.
5. Keep result conversion in one helper rather than interleaving it with selection.
6. Add a short pipeline comment that explains each stage and its ownership.
7. Remove obsolete compatibility helpers and fields made unnecessary by prior tasks.

## Non-goals

- Do not merge `Extract` and `ExtractBytes` if doing so adds a full input copy.
- Do not introduce a generic pipeline framework.
- Do not change error messages or fallback behavior.

## Acceptance criteria

- The core extraction function is understandable without reading scoring, fallback, metadata, title, or Markdown internals.
- Each stage has one clear input and result.
- No stage relies on undocumented call order or partially initialized shared state.
- Full tests, race tests, corpus tests, and performance gates pass.

---

# Completion criteria for the full plan

The refactor is complete when all of the following are true:

1. The public API and extraction corpus remain stable.
2. Page classification is a pure decision over semantic evidence.
3. Selection hides scoring profiles and fallback strategy.
4. Title resolution does not depend on Markdown configuration or rendering.
5. Markdown building and rendering are separate phases.
6. Metadata precedence is centralized.
7. Diagnostic state does not live in `Document`.
8. Cache encodings do not leak into policy code.
9. The root package owns its public contract without aliases to engine types.
10. The core extraction function shows a short, explicit pipeline.

## Guidance for agents

Before starting a task:

1. Read every file named in that task.
2. Read the tests for the affected package.
3. Run the required validation once to establish a clean baseline.
4. Make the smallest change that completes the task.
5. Do not combine policy tuning with structural work.
6. If an existing regression test contradicts this document, preserve the tested behavior and document the conflict in the task result.
