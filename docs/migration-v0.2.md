# Migrating to the simplified public API

This pre-1.0 release removes redundant options and makes invalid combinations
fail before parsing begins.

## Page type

Replace `Profile` and `WithProfile`:

```go
pagemark.WithPageType(pagemark.PageTypeDocumentation)
```

## Selection mode

Replace `WithFavorPrecision(true)`:

```go
pagemark.WithSelectionMode(pagemark.SelectionPrecision)
```

Replace `WithFavorRecall(true)`:

```go
pagemark.WithSelectionMode(pagemark.SelectionRecall)
```

Use `SelectionBalanced` for the default behavior. Unknown modes return
`ErrInvalidOption`.

## Limits

The public resource options are `WithMaxInputBytes` and
`WithMaxOutputBytes`. Zero selects the package default, a positive value sets
the limit, and `-1` means unlimited. Values below `-1` are invalid.

DOM element and depth limits remain fixed internal safety bounds. Use
`WithIncludeLinks`, `WithIncludeImages`, and `WithIncludeTables` to control
output features.

## URL policy

`Schemes` is now `AllowedSchemes`, and `AllowMailto` has been removed. Add
`"mailto"` to the scheme list when required:

```go
policy := pagemark.DefaultURLPolicy()
policy.AllowedSchemes = []string{"https"}
policy.StripTracking = true

doc, err := pagemark.ExtractBytes(
	source,
	pageURL,
	pagemark.WithURLPolicy(policy),
)
```

The option copies the scheme slice and is safe to reuse concurrently.
`Document.URL` now strips credentials, as `Document.CanonicalURL` already did.

## Metadata

Remove `WithIncludeMetadata(false)`. Metadata extraction is part of the stable
document result, and callers can omit unwanted fields when serializing or
formatting it. This prevents a separated authored title from disappearing from
both metadata and content.

## Typed identifiers

`Warning.Code` is a `WarningCode`; use constants such as
`WarningOutputTruncated`. `LimitError.Resource` is a `LimitResource`; use
constants such as `LimitInputBytes`. JSON string values remain stable.

## Diagnostics

Use the experimental detailed API:

```go
doc, report, err := pagemark.ExtractDetailedBytes(source, pageURL)
```

`report` contains page-type and quality scores, block details, fallback names,
rejected links, and extraction counters. Its shape may change in a minor
release. Diagnostic details are available only through `DiagnosticReport`;
the legacy `WithDiagnostics` option and diagnostics pointer are not part of the
current API.

## Implementation decisions

- The module is pre-1.0 and has no stated compatibility guarantee. `Profile`,
  `WithProfile`, `WithFavorPrecision`, `WithFavorRecall`, advanced individual
  limit options, and `WithIncludeMetadata` were removed directly. Only the
  legacy diagnostics option and fields have a temporary compatibility path.
- Public resource configuration is limited to input and output byte bounds.
  DOM element and depth bounds are fixed internal safety limits.
- `ExtractDetailedBytes` enables diagnostics inside the existing extraction
  pass and builds `DiagnosticReport`. Diagnostic state remains internal to the
  extraction engine, avoiding a second extraction pipeline and import-cycle
  workarounds.
- `Quality`, `PageTypeScore`, and `Stats` remain on `Document` for the stable
  result contract; the detailed report also includes their diagnostic view.
- The `URLPolicy` field rename and `AllowMailto` removal are included now.
  Mailto has the same scheme-policy behavior as other allowed schemes.
- A complete repository search found old-option usage only in this module's
  tests and documentation; those callers were migrated. No external downstream
  usage was measured.
- A representative real-world benchmark against the previous commit showed no
  material throughput regression. Default extraction adds one small fixed
  allocation for validated option application; bytes per operation remained
  effectively unchanged. Custom URL policies intentionally add fixed defensive
  copy allocations.
- Regression fixtures produce the same Markdown and text. The intentional
  result changes are credential stripping in `Document.URL`, typed kebab-case
  `LimitError.Resource` values, and metadata always remaining available.
