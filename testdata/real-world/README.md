# Real-world fixtures

These are frozen HTML responses from popular public websites. They exercise page chrome, responsive duplicates, structured documentation, rendered repository Markdown, and content-heavy article layouts. Normal tests read only these files and never access the network. Anonymous action, upload, and CSRF tokens are replaced with `REDACTED` before snapshots are committed.

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
