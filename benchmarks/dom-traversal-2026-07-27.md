# Extraction performance results (2026-07-27)

Machine: Apple M2 Pro, Darwin/arm64. Go version: 1.26.5. Results are the median of three runs.

Command:

```sh
go test ./internal/engine -run '^$' -bench '^BenchmarkExtractRealWorld$' -benchmem -count=3
```

| Fixture | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| Wikipedia article | 9,027,735 | 1,694,887 | 33,674 |
| Go tutorial | 1,859,619 | 709,338 | 8,195 |
| MDN reference | 5,202,122 | 1,522,384 | 26,292 |
| GitHub README | 10,935,532 | 2,306,990 | 46,391 |
| Discourse thread | 5,165,484 | 1,467,654 | 25,961 |
| Open Food Facts | 14,407,783 | 3,325,385 | 43,847 |
| GOV.UK voting search | 4,774,745 | 1,041,351 | 18,086 |
| The Conversation | 4,518,657 | 1,006,251 | 11,461 |
| GOV.UK registration | 3,399,292 | 837,030 | 14,662 |
| Wikibooks recipe | 3,997,266 | 732,946 | 13,572 |
| Stack Overflow | 9,761,217 | 2,092,390 | 35,077 |

The following comparison uses the reported pre-fix rerun on the same machine and Go version.

| Fixture | Pre-fix time | Fixed time | Change | Pre-fix B/op | Fixed B/op | Change |
|---|---:|---:|---:|---:|---:|---:|
| Wikipedia | 36.1 ms | 9.03 ms | -75.0% | 5.62 MB | 1.69 MB | -69.9% |
| GitHub README | 29.3 ms | 10.94 ms | -62.7% | 4.88 MB | 2.31 MB | -52.7% |
| Open Food Facts | 56.4 ms | 14.41 ms | -74.5% | 27.47 MB | 3.33 MB | -87.9% |
| Stack Overflow | 21.5 ms | 9.76 ms | -54.6% | 5.85 MB | 2.09 MB | -64.3% |

## Regression fix

The extraction pass now rejects ordinary elements before subtree-based navigation checks. JavaScript notice detection uses one bounded streaming scan instead of materializing overlapping subtree strings. Visibility checks use a cheap class gate, and the Markdown converter reuses visibility decisions from the extraction evidence pass.

Page-type-independent auxiliary classification is cached across inference, listing detection, and final scoring. Explicit page types skip inference unless diagnostics need candidate scores. Ordinary inference tracks the best two page types without allocating and sorting a candidate report.

These changes restore the main regressed fixtures to their earlier range. In particular, Wikipedia is approximately 9.0 ms/op and Open Food Facts is approximately 14.4 ms/op with 3.33 MB/op on this machine.
