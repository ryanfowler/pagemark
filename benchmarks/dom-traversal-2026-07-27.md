# DOM traversal optimization results (2026-07-27)

Machine: Apple M2 Pro, Darwin/arm64. Go benchmark results are the median of five runs.

Commands:

```sh
go test -run '^$' -bench '^BenchmarkExtractRealWorld$' -benchmem -count=5
go test -run '^$' -bench '^BenchmarkExtractRealWorld$' -cpuprofile cpu.out -memprofile mem.out
go tool pprof -top cpu.out
go tool pprof -top -alloc_space mem.out
```

| Fixture | Before ns/op | After ns/op | Change | Before B/op | After B/op | Before allocs/op | After allocs/op |
|---|---:|---:|---:|---:|---:|---:|---:|
| Wikipedia article | 17,190,673 | 16,731,572 | -2.7% | 1,851,864 | 1,840,112 | 42,927 | 42,531 |
| Go tutorial | 1,936,191 | 1,816,631 | -6.2% | 741,817 | 731,240 | 8,772 | 8,647 |
| MDN reference | 5,399,287 | 5,073,493 | -6.0% | 1,495,899 | 1,477,068 | 27,247 | 26,964 |
| GitHub README | 10,502,078 | 10,292,560 | -2.0% | 2,314,979 | 2,308,780 | 48,035 | 47,848 |
| Discourse thread | 5,861,812 | 5,542,395 | -5.4% | 1,514,039 | 1,484,487 | 28,295 | 28,147 |
| Open Food Facts | 20,617,333 | 19,864,814 | -3.6% | 3,972,442 | 3,934,362 | 48,414 | 48,138 |
| GOV.UK voting search | 9,052,696 | 8,907,298 | -1.6% | 1,426,243 | 1,416,070 | 19,414 | 19,339 |
| The Conversation | 26,667,134 | 26,506,874 | -0.6% | 1,476,472 | 1,470,071 | 20,934 | 20,805 |
| GOV.UK registration | 3,348,883 | 3,253,063 | -2.9% | 849,714 | 843,969 | 15,740 | 15,602 |
| Wikibooks recipe | 4,119,513 | 3,991,503 | -3.1% | 787,092 | 785,210 | 14,425 | 14,311 |
| Stack Overflow | 9,935,178 | 9,674,106 | -2.6% | 2,141,922 | 2,123,833 | 36,398 | 36,147 |

## Profile observation

Before the change, generic `walk` accounted for 3.10 s cumulative CPU (23.81%), `normalizeText` for 0.87 s (6.68%), and `linkTextLength` for 0.18 s (1.38%). `linkTextLength` allocated 25.5 MB in the sampled run by constructing and normalizing link strings.

After combining immutable block link/control evidence into one direct traversal, `linkTextLength` no longer appeared in the CPU or allocation top output. Cumulative `walk` CPU fell to 2.26 s and `normalizeText` to 0.35 s in the corresponding profile. Profile totals and benchmark iteration mixes vary, so the five-run benchmark medians above are the throughput comparison.
