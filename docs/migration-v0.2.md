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

`LimitError.Resource` is a `LimitResource`. Use constants such as
`LimitInputBytes`. JSON string values remain stable.

## Extraction status and diagnostics

Replace warning checks for output truncation with `Document.Truncated`:

```go
if doc.Truncated {
	// The output byte limit omitted content.
}
```

`Warning`, `WarningCode`, and all warning constants have been removed. Fallback
and relaxed-extraction warnings described internal algorithm choices and have
no stable replacement.

`Document.Quality`, `Document.PageTypeScore`, and `Document.Stats` have also
been removed. `ExtractDetailedBytes`, `DiagnosticReport`, `PageCandidate`, and
`BlockDiagnostic` are no longer part of the public package. Algorithm
diagnostics remain internal to this module. `WithLogger` has also been removed;
callers can log stable result fields after extraction.

## Implementation decisions

- The module is pre-1.0 and has no stated compatibility guarantee. `Profile`,
  `WithProfile`, `WithFavorPrecision`, `WithFavorRecall`, advanced individual
  limit options, and `WithIncludeMetadata` were removed directly.
- Public resource configuration is limited to input and output byte bounds.
  DOM element and depth bounds are fixed internal safety limits.
- Internal diagnostic extraction uses the existing extraction pass. Diagnostic
  state remains in the extraction engine.
- The stable `Document` reports output truncation with one boolean. Fallback
  choices, scores, and extraction counters remain internal diagnostics.
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
