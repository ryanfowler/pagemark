# Pagemark

Pagemark extracts useful content from HTML as compact Markdown, plain text, and
metadata. It supports articles, documentation, discussions, products, listings,
collections, services, and generic pages.

Pagemark does not fetch pages or run JavaScript. Supply rendered HTML when a
page requires JavaScript.

## Installation

Add Pagemark and the URL parser to your `Cargo.toml`:

```toml
[dependencies]
pagemark = "0.1"
url = "2"
```

## Quick start

```rust
use pagemark::{extract, Options};
use url::Url;

fn main() -> Result<(), pagemark::Error> {
    let html = "<main><h1>Guide</h1><p>Install the tool.</p></main>";
    let page_url = Url::parse("https://example.com/guide").expect("static URL is valid");
    let document = extract(html, Some(&page_url), &Options::default())?;

    println!("{}", document.title.as_deref().unwrap_or("Untitled"));
    println!("{}", document.markdown);
    Ok(())
}
```

`extract_bytes` accepts UTF-8 bytes and enforces the input limit before parsing.
The page URL is optional, but when supplied it must be an absolute HTTP or
HTTPS URL. Credentials are removed from returned page and canonical URLs.

## Result

`Document` contains:

- optional title, description, author, site name, language, and publication time;
- detected or requested `PageType`;
- restricted Markdown without raw HTML;
- plain text and heading-based sections;
- safe links and useful images retained in the output; and
- a `truncated` flag when the output limit omits complete blocks.

## Options

`Options::default()` enables links, images, and tables, uses balanced selection,
and detects the page type automatically. Options are configured directly:

```rust
use pagemark::{Limit, Options, PageType, SelectionMode};

let options = Options {
    page_type: Some(PageType::Documentation),
    selection_mode: SelectionMode::Precision,
    max_output_bytes: Limit::Max(512 * 1024),
    include_images: false,
    ..Options::default()
};
```

`Limit::Default` uses the 10 MiB input and 2 MiB output defaults. Use
`Limit::Unlimited` to disable a configurable limit. DOM element and depth limits
are always enforced.

`SelectionMode::Precision` usually selects less auxiliary content;
`SelectionMode::Recall` usually selects more plausible content.

`UrlPolicy` controls links and images emitted in content. By default it permits
only HTTP and HTTPS URLs, rejects credentials and control characters, and limits
URLs to 4,096 bytes:

```rust
let mut options = Options::default();
options.url_policy.allowed_schemes = vec!["https".into()];
options.url_policy.strip_tracking = true;
```

## Safety

Pagemark removes scripts, forms, widgets, and raw HTML from generated content.
It does not make extracted words safe or true. Treat extracted content as
untrusted data; hostile pages can contain prompt injection.

## Development

Initialize the optional Readability corpus:

```sh
git submodule update --init --recursive
```

Run the checks:

```sh
cargo fmt --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test --all-features
cargo doc --no-deps
```

See [the extraction contract](docs/contract.md) for the stable behavior and
safety guarantees.
