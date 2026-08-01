//! Public API integration tests.

use pagemark::{extract, extract_bytes, Error, Limit, Options, PageType, SelectionMode, UrlPolicy};
use url::Url;

#[test]
fn extracts_metadata_content_and_safe_resources() {
    let html = r#"<!doctype html><html lang="en"><head>
      <title>Guide | Example</title><meta property="og:title" content="Guide">
      <meta name="description" content="A useful guide."><meta name="author" content="Ada">
      <link rel="canonical" href="/guide?ref=canonical"></head><body>
      <nav>Menu</nav><main><h1>Guide</h1><p>Read <a href="/docs?utm_source=x&amp;keep=1">the docs</a>.</p>
      <h2>Install</h2><p>Run <code>cargo install tool</code> now.</p>
      <img src="/diagram.png" alt="System diagram"></main><footer>Copyright</footer></body></html>"#;
    let mut options = Options::default();
    options.url_policy.strip_tracking = true;
    let page = Url::parse("https://user:secret@example.com/start").unwrap();
    let doc = extract(html, Some(&page), &options).unwrap();
    assert_eq!(doc.url.unwrap().as_str(), "https://example.com/start");
    assert_eq!(
        doc.canonical_url.unwrap().as_str(),
        "https://example.com/guide?ref=canonical"
    );
    assert_eq!(doc.title.as_deref(), Some("Guide"));
    assert_eq!(doc.author.as_deref(), Some("Ada"));
    assert!(!doc.markdown.contains('<'));
    assert!(!doc.markdown.contains("Menu"));
    assert!(!doc.markdown.contains("Copyright"));
    assert!(doc
        .markdown
        .contains("[the docs](https://example.com/docs?keep=1)"));
    assert_eq!(doc.links.len(), 1);
    assert_eq!(doc.images.len(), 1);
    assert!(doc
        .sections
        .iter()
        .any(|section| section.heading.as_deref() == Some("Install")));
}

#[test]
fn renders_structures() {
    let source = r#"<main><h1>Reference</h1><blockquote><p>A quote.</p></blockquote>
    <ol start="3"><li>third</li><li>fourth</li></ol><pre><code class="language-rust">fn main() {}</code></pre>
    <table><tr><th>Name</th><th>Value</th></tr><tr><td>A</td><td>10</td></tr></table></main>"#;
    let doc = extract(source, None, &Options::default()).unwrap();
    assert!(doc.markdown.contains("> A quote."));
    assert!(doc.markdown.contains("3. third"));
    assert!(doc.markdown.contains("```rust"));
    assert!(doc.markdown.contains("| Name | Value |"));
}

#[test]
fn enforces_utf8_input_and_output_limits() {
    assert!(matches!(
        extract_bytes(&[0xff], None, &Options::default()),
        Err(Error::InvalidUtf8(_))
    ));
    let options = Options {
        max_input_bytes: Limit::Max(4),
        ..Options::default()
    };
    assert!(matches!(
        extract("12345", None, &options),
        Err(Error::Limit { .. })
    ));

    let options = Options {
        max_input_bytes: Limit::Unlimited,
        max_output_bytes: Limit::Max(12),
        ..Options::default()
    };
    let doc = extract(
        "<main><p>first</p><p>a considerably longer second block</p></main>",
        None,
        &options,
    )
    .unwrap();
    assert!(doc.truncated);
    assert_eq!(doc.markdown, "first");
}

#[test]
fn validates_page_and_resource_urls() {
    let bad = Url::parse("mailto:user@example.com").unwrap();
    assert!(matches!(
        extract("<p>content here</p>", Some(&bad), &Options::default()),
        Err(Error::InvalidPageUrl)
    ));
    let source = r#"<main><p><a href="javascript:alert(1)">bad</a> <a href="https://u:p@example.com/">credential</a></p></main>"#;
    let doc = extract(source, None, &Options::default()).unwrap();
    assert!(doc.links.is_empty());
    assert!(!doc.markdown.contains("javascript:"));
}

#[test]
fn options_override_type_and_features() {
    let options = Options {
        page_type: Some(PageType::Discussion),
        selection_mode: SelectionMode::Recall,
        include_links: false,
        include_images: false,
        url_policy: UrlPolicy::default(),
        ..Options::default()
    };
    let doc = extract("<main><h1>Topic</h1><div class=comment><p>A response with useful content.</p></div></main>", None, &options).unwrap();
    assert_eq!(doc.page_type, PageType::Discussion);
    assert!(doc.markdown.contains("A response"));
}

#[test]
fn discussion_login_prompt_is_excluded_case_insensitively() {
    let options = Options {
        page_type: Some(PageType::Discussion),
        ..Options::default()
    };
    let html = r#"<main><h1>Topic</h1>
        <div class="message"><p>YOU MUST LOG IN TO REPLY.</p></div>
        <p>This is the useful discussion content.</p></main>"#;
    let doc = extract(html, None, &options).unwrap();
    assert!(!doc.markdown.contains("MUST LOG IN TO REPLY"));
    assert!(doc.markdown.contains("useful discussion content"));
}

#[test]
fn arbitrary_bytes_never_panic() {
    for length in 0..512 {
        let bytes: Vec<u8> = (0..length)
            .map(|i| u8::try_from((i * 73 + length) % 256).unwrap_or_default())
            .collect();
        let result = std::panic::catch_unwind(|| extract_bytes(&bytes, None, &Options::default()));
        assert!(result.is_ok());
    }
}
