# WCXB development baseline

Corpus: WCXB v1.0, development split, 1,497 pages.

The WCXB project reports these multiset word scores:

| Extractor | Precision | Recall | F1 |
|---|---:|---:|---:|
| Readability | 0.685 | 0.713 | 0.675 |
| Pagemark initial profile | 0.725 | 0.893 | 0.760 |

The Pagemark run used Go 1.26.5 on Apple arm64. The full run took approximately 15 seconds. The report is a development measurement, not a held-out release result.

The principal initial failure modes are forum content in fallback `noscript` elements, product facts in nonparagraph structures, absent SPA content, linked collection cards, repeated forum controls, short service sections, specification tables, duplicated mobile markup, side navigation, and content in generic containers.
