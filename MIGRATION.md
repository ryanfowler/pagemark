# Go-to-Rust migration status

## Baseline

The migration baseline is Go commit `fbc436513717c1c119783c2b4e49bcbdd527104d` (including Readability submodule commit `ab4027a8b37669745016869a37a504727992b2ba`). The public package, internal engine, Markdown renderer, URL utility, tests, fixtures, documentation, and available corpus layout were inspected before implementation.

## Architecture map

| Go responsibility | Rust module |
| --- | --- |
| Public aliases and functional options | `src/lib.rs`, `src/types.rs`, `src/options.rs` |
| Engine errors and bounds | `src/error.rs`, `src/dom.rs` |
| `x/net/html` DOM | private arena and `html5ever::TreeSink` in `src/dom.rs` |
| Metadata and base discovery | `src/metadata.rs` |
| URL utility and Markdown resource policy | `src/url_policy.rs` |
| Segmentation, classification, selection, fallback, title separation | `src/extractor.rs` |
| Restricted Markdown, text, sections, links, images, truncation | `src/markdown.rs` |
| Compatibility report | `compatibility/report.json` |

DOM nodes use stable `NodeId(u32)` handles, parent links, and source-ordered child vectors. Parsed nodes contain no mutable scoring state. Extraction state is local to each call. No unsafe code or production `markup5ever_rcdom` dependency is used.

## Deliberate API differences

- Rust accepts `&str` or `&[u8]`; there is no reader or parsed-node public API.
- URLs use owned `url::Url` values rather than strings.
- Optional metadata uses `Option<T>`.
- `Limit::{Default, Unlimited, Max}` replaces integer sentinels.
- `Options` replaces functional options.
- Errors are one non-exhaustive, matchable `thiserror` enum.
- Serde is enabled by default and can be disabled with `--no-default-features`.

## Dependency rationale

- `html5ever` supplies a hardened HTML5 tokenizer/tree builder; implementing hostile-input parsing locally would be substantially less safe. It is private and dual MIT/Apache licensed.
- `url` supplies standards-based parsing, resolution, IDNA, and serialization. `Url` is deliberately public. It is maintained by the Servo/Rust URL project and dual MIT/Apache licensed.
- `thiserror` provides a conventional typed public error implementation without type erasure. It processes no input and is dual MIT/Apache licensed.
- `serde` provides optional result serialization and `serde_json` parses bounded JSON-LD metadata and powers diagnostics. Both are widely maintained and dual MIT/Apache licensed.
- `criterion` is development-only benchmark infrastructure.

## Compatibility results

Run:

```sh
./scripts/differential.py
./scripts/differential.py --strict path/to/fixture.html
```

The latest report is [`compatibility/report.json`](compatibility/report.json). On the 33 checked-in non-submodule HTML fixtures, 25 documents are byte-for-byte identical and 8 have categorized differences. Consequently, full Go behavioral parity has **not** been established. The report records each fixture, complete observed values, suspected subsystem, and proposed resolution. These are material known differences, not approved compatibility exceptions.

The implementation now additionally covers discussion-record reconstruction, text-mode archive listings, inferred table headers and captions, nested list flow, authored Markdown roots, publisher ledes, noscript markup suppression, duplicate blocks, structured-data entity filtering, and substantially broader auxiliary-region handling. Remaining differences are concentrated in complex documentation lists/custom elements and the large Discourse, Stack Overflow, MediaWiki, Open Food Facts, and Conversation page shells.

## Corpus tests

Initialize optional data and run the no-panic corpus smoke test:

```sh
git submodule update --init --recursive
cargo test --test corpus -- --ignored
```

Run differential checks against Mozilla fixtures by passing their HTML paths to `scripts/differential.py`. Normal tests perform no network access.

## Fuzzing

```sh
cargo install cargo-fuzz
cargo fuzz run extract_bytes
```

The public byte entry point validates limits and UTF-8 before parsing. The integration suite also checks deterministic arbitrary byte sequences under `catch_unwind`.

## Performance

Run Rust benchmarks with:

```sh
cargo bench --bench extract
```

On the recorded development host (`DO-Premium-Intel`), the large checked-in article benchmark measured **56.991–67.060 µs** (Criterion, 10 samples, one-second measurement). Existing Go real-world benchmarks on the same host ranged from **4.50 ms to 34.53 ms**, but use different fixtures and therefore are not a valid direct speedup comparison. Peak memory and allocation parity have not been measured. No optimization claim should be made from these results.

## Quality gates

```sh
cargo fmt --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test --all-features
cargo doc --no-deps
cargo deny check
cargo audit
```

MSRV is Rust 1.86. CI checks MSRV and current stable. The crate forbids unsafe code.

## Release-readiness assessment

**Not ready for a parity release.** The crate compiles, passes its Rust tests and static gates, and provides the intended safety architecture, but the differential report has material mismatches and the required corpus/performance parity work remains unresolved. A release must not claim the migration plan is 100% complete until those report entries are resolved and direct benchmarks plus memory measurements are recorded.
