# Real-world fixtures

These are frozen HTML responses from popular public websites. They exercise page chrome, responsive duplicates, structured documentation, rendered repository Markdown, content-heavy article layouts, discussion records, product facts and tables, linked search records, and distributed service content. Normal tests read only these files and never access the network. Anonymous action, upload, session, CSP nonce, and CSRF tokens are replaced with `REDACTED` before snapshots are committed.

The 2026-07-23 corpus expansion added these anonymous, server-rendered captures:

| Fixture | Shape and failure mode | Initial extraction |
|---|---|---|
| Discourse Meta new-user guide | Discussion with a long first post, replies, repeated controls, and crawler linkbacks | `discussion`, quality 0.75; crawler linkbacks and like counters survived |
| Open Food Facts Nutella | Product facts in generic containers, nested panels, and nutrition tables | `product`, quality 0.75; the incomplete-record contribution prompt survived |
| GOV.UK voting search | Search listing with filters, nested result links, and descriptions | incorrectly `article`, quality 0.75; feedback furniture survived |
| The Conversation Creative Commons article | News article surrounded by publisher, sharing, newsletter, and partner chrome | `article`, quality 0.75; primary article and disclosure were retained |
| GOV.UK register to vote | Distributed service sections plus a large step-navigation component | incorrectly `article`, quality 0.75; feedback and step navigation survived |

All had no warnings or fallback. The Discourse session identifier and GOV.UK CSRF/CSP values were redacted; no other snapshot reduction or normalization was performed. The Discourse guide itself explicitly invites readers to copy and paste it to another site, and only the two rendered reply excerpts needed to preserve thread structure are asserted by the fixture.

`fixtures.json` records each source URL, capture date, content license, SHA-256 digest, expected page type, quality floor, and required/forbidden Markdown. The digest prevents an accidental fixture refresh from silently changing the regression corpus.

## Refreshing a fixture

Refresh snapshots deliberately, one at a time:

1. Download the source URL with redirects and compression enabled, using a non-personal session and no cookies.
2. Inspect the HTML for personal data, authentication state, challenge pages, and unexpectedly embedded content.
3. Update `captured_at` and `sha256` in `fixtures.json`.
4. Review required and forbidden snippets against the intended primary content; do not merely change expectations to match a regression.
5. Run `go test ./...`.

For example:

```sh
curl -L --compressed \
  -A 'pagemark-fixture-capture/1.0' \
  -o testdata/real-world/wikipedia-web-page.html \
  'https://en.wikipedia.org/wiki/Web_page'
shasum -a 256 testdata/real-world/wikipedia-web-page.html
```

Site layouts and licensing can change. Recheck the source terms before updating or adding a snapshot. Prefer pages whose primary content has an explicit open license.
