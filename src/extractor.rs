use url::Url;

use crate::{
    dom::{self, hidden, Dom, NodeId},
    markdown,
    metadata::{self, Metadata},
    options::{Options, SelectionMode},
    types::{Document, PageType},
    url_policy::sanitized_page_url,
    Error,
};

pub(crate) fn extract_impl(
    html: &str,
    page_url: Option<&Url>,
    options: &Options,
) -> Result<Document, Error> {
    options.validate()?;
    if let Some(max) = options.max_input_bytes.resolve(Options::MAX_INPUT_DEFAULT) {
        if html.len() as u64 > max {
            return Err(Error::Limit {
                resource: crate::LimitResource::InputBytes,
                count: html.len() as u64,
                max,
            });
        }
    }
    extract_validated(html, page_url, options)
}

pub(crate) fn extract_validated(
    html: &str,
    page_url: Option<&Url>,
    options: &Options,
) -> Result<Document, Error> {
    let page = match page_url {
        Some(url) => Some(sanitized_page_url(url).ok_or(Error::InvalidPageUrl)?),
        None => None,
    };
    let dom = dom::parse(html)?;
    let primary = extract_dom(&dom, page.clone(), options);
    let primary_size = primary.as_ref().map_or(0, |doc| doc.text.chars().count());
    if primary_size < 120 && contains_ascii_case_insensitive(html, "<noscript") {
        if let Ok(fallback_dom) = dom::parse_with_scripting(html, false) {
            if let Ok(fallback) = extract_dom(&fallback_dom, page.clone(), options) {
                let fallback_size = fallback.text.chars().count();
                if fallback_size >= 120 && fallback_size > primary_size.saturating_mul(2) {
                    return Ok(fallback);
                }
            }
        }
    }
    primary
}

fn extract_dom(dom: &Dom, page: Option<Url>, options: &Options) -> Result<Document, Error> {
    let base = metadata::discover_base(dom, page.as_ref());
    let metadata = metadata::extract(dom, base.as_ref());
    // Page shape is document-level evidence. Classifying only the eventual
    // content root misses forum records that precede a small <main>, and lets
    // navigation-heavy roots distort selection.
    let body = dom
        .find_first(dom.document(), |id| dom.tag(id) == Some("body"))
        .ok_or(Error::NoContent)?;
    let authored_markdown = dom.find_first(body, |id| {
        dom.attr(id, "class").is_some_and(|value| {
            value
                .split_whitespace()
                .any(|class| class.eq_ignore_ascii_case("markdown-body"))
        }) && dom
            .attr(id, "itemprop")
            .is_some_and(|value| value.eq_ignore_ascii_case("text"))
    });
    let page_type = options.page_type.unwrap_or_else(|| {
        if authored_markdown.is_some() {
            PageType::Article
        } else {
            classify(dom, body, &metadata, page.as_ref())
        }
    });
    let root = select_root(dom, options.selection_mode, page_type).ok_or(Error::NoContent)?;
    let title_node = title_node(dom, root, metadata.title.as_deref(), page_type);
    let title = resolve_title(dom, title_node, &metadata, page.as_ref());
    let rendered = markdown::render(dom, root, title_node, base.as_ref(), options, page_type);
    if rendered.text.trim().is_empty() && rendered.images.is_empty() && !rendered.truncated {
        return Err(Error::NoContent);
    }
    Ok(Document {
        url: page,
        canonical_url: metadata.canonical,
        title,
        description: metadata.description,
        author: metadata.author,
        site_name: metadata.site_name,
        language: metadata.language,
        published_time: metadata.published_time,
        page_type,
        markdown: rendered.markdown,
        text: rendered.text,
        sections: rendered.sections,
        links: rendered.links,
        images: rendered.images,
        truncated: rendered.truncated,
    })
}

#[allow(clippy::too_many_lines)]
fn select_root(dom: &Dom, mode: SelectionMode, page_type: PageType) -> Option<NodeId> {
    let document = dom.document();
    if let Some(markdown) = dom.find_first(document, |id| {
        dom.attr(id, "class").is_some_and(|v| {
            v.split_whitespace()
                .any(|c| c.eq_ignore_ascii_case("markdown-body"))
        }) && dom
            .attr(id, "itemprop")
            .is_some_and(|v| v.eq_ignore_ascii_case("text"))
    }) {
        return Some(markdown);
    }
    let body = dom.find_first(document, |id| dom.tag(id) == Some("body"))?;
    if page_type == PageType::Listing {
        let lists = find_all(dom, body, |id| {
            attr_contains(dom, id, "document-list")
                || attr_contains(dom, id, "results-list")
                || attr_contains(dom, id, "search-results")
        });
        if let Some(best) = largest_text(dom, &lists) {
            return Some(best);
        }
    }
    if page_type == PageType::Product {
        // Product templates often keep useful status/scan copy beside the
        // schema.org product wrapper. Render from the body and let the
        // product auxiliary rules remove the surrounding controls.
        return Some(body);
    }
    // A discussion's records are frequently siblings of a small reply-control
    // <main>. Keep the body boundary and let discussion-specific exclusions
    // remove the surrounding controls.
    if page_type == PageType::Discussion {
        return Some(body);
    }
    if page_type == PageType::Article {
        let focused = find_all(dom, body, |id| {
            if dom.tag(id) != Some("div") {
                return false;
            }
            let named_main = dom
                .attr(id, "id")
                .is_some_and(|value| value.eq_ignore_ascii_case("mainbody"));
            let unadorned = dom.parent(id) == Some(body)
                && dom.attr(id, "id").is_none()
                && dom.attr(id, "class").is_none();
            if !named_main && !unadorned {
                return false;
            }
            let mut paragraphs = 0;
            dom.walk(id, &mut |node| {
                if dom.tag(node) == Some("p") {
                    paragraphs += 1;
                }
                true
            });
            paragraphs >= 3
        });
        if let Some(best) = largest_text(dom, &focused) {
            return Some(best);
        }
        let detached_lede = dom.find_first(body, |id| {
            (attr_contains(dom, id, "article") || attr_contains(dom, id, "headline"))
                && (attr_contains(dom, id, "lede") || attr_contains(dom, id, "headline"))
        });
        if detached_lede.is_some() {
            return Some(body);
        }
    }
    let articles = find_all(dom, document, |id| {
        dom.tag(id) == Some("article") && !auxiliary(dom, id)
    });
    if let Some(best) = largest_text(dom, &articles) {
        let length = dom.text(best).chars().count();
        if length
            >= if mode == SelectionMode::Recall {
                40
            } else {
                80
            }
        {
            return Some(best);
        }
    }
    let mains = find_all(dom, document, |id| {
        dom.tag(id) == Some("main")
            || dom
                .attr(id, "role")
                .is_some_and(|v| v.eq_ignore_ascii_case("main"))
    });
    if let Some(best) = largest_text(dom, &mains) {
        return Some(best);
    }
    if dom.text(body).trim().is_empty() && !has_useful_image(dom, body) {
        None
    } else {
        Some(body)
    }
}

fn largest_text(dom: &Dom, nodes: &[NodeId]) -> Option<NodeId> {
    nodes
        .iter()
        .copied()
        .max_by_key(|id| dom.text(*id).chars().count())
}
fn find_all(dom: &Dom, root: NodeId, mut predicate: impl FnMut(NodeId) -> bool) -> Vec<NodeId> {
    let mut values = Vec::new();
    dom.walk(root, &mut |id| {
        if predicate(id) {
            values.push(id);
        }
        true
    });
    values
}

fn auxiliary(dom: &Dom, id: NodeId) -> bool {
    if hidden(dom, id) {
        return true;
    }
    let tag = dom.tag(id).unwrap_or("");
    if matches!(tag, "nav" | "footer" | "aside") {
        return true;
    }
    [
        "card",
        "comment",
        "reply",
        "related",
        "recommended",
        "newsletter",
        "subscribe",
        "advert",
        "sidebar",
        "navigation",
        "footer",
    ]
    .iter()
    .any(|token| has_ascii_token(dom, id, token))
}

fn has_useful_image(dom: &Dom, root: NodeId) -> bool {
    let mut found = false;
    dom.walk(root, &mut |id| {
        if dom.tag(id) == Some("img") && !dom.attr(id, "alt").unwrap_or("").trim().is_empty() {
            found = true;
            return false;
        }
        !found
    });
    found
}

#[allow(clippy::too_many_lines)]
fn classify(dom: &Dom, root: NodeId, metadata: &Metadata, page: Option<&Url>) -> PageType {
    let mut prose_blocks = 0;
    let mut prose_chars = 0;
    let mut code_blocks = 0;
    let mut code_chars = 0;
    let mut tables = 0;
    let mut sections = 0;
    let mut discussion_records = 0;
    let mut discussion_chars = 0;
    let mut listing_records = 0;
    let mut product_records = 0;
    let mut schema_product = false;
    let mut documentation_context = false;
    let mut text_listing = false;
    dom.walk(root, &mut |id| {
        if auxiliary(dom, id) {
            return false;
        }
        let tag = dom.tag(id).unwrap_or("");
        let text_len = dom.text(id).chars().count();
        match tag {
            "p" => {
                prose_blocks += 1;
                prose_chars += text_len;
            }
            "pre" => {
                code_blocks += 1;
                code_chars += text_len;
                let mut links = 0;
                dom.walk(id, &mut |node| {
                    if dom.tag(node) == Some("a") && dom.has_attr(node, "href") {
                        links += 1;
                    }
                    true
                });
                let dated_lines = dom
                    .raw_text(id)
                    .lines()
                    .filter(|line| {
                        let value = line.trim().as_bytes();
                        value.len() >= 10
                            && value[0..4].iter().all(u8::is_ascii_digit)
                            && value[4] == b'-'
                            && value[5..7].iter().all(u8::is_ascii_digit)
                            && value[7] == b'-'
                            && value[8..10].iter().all(u8::is_ascii_digit)
                    })
                    .count();
                if links >= 4 && dated_lines >= 3 && text_len >= 120 {
                    text_listing = true;
                }
            }
            "table" => tables += 1,
            "section" => sections += 1,
            _ => {}
        }
        let token_is = |wanted: &str| has_token(dom, id, wanted);
        let discussion_record = ["comment", "reply", "answer", "message", "post"]
            .iter()
            .any(|value| token_is(value))
            && matches!(tag, "article" | "li" | "div" | "section" | "td")
            && text_len >= 20;
        if discussion_record && !ancestor_has_discussion_token(dom, id) {
            discussion_records += 1;
            discussion_chars += text_len;
        }
        if ["card", "result", "item"].iter().any(|v| token_is(v)) {
            listing_records += 1;
        }
        if dom
            .attr(id, "itemtype")
            .is_some_and(|value| value.to_ascii_lowercase().contains("product"))
        {
            schema_product = true;
        }
        if token_is("product") {
            product_records += 1;
        }
        if [
            "documentation",
            "docs",
            "doc",
            "reference",
            "api",
            "tutorial",
            "guide",
        ]
        .iter()
        .any(|value| token_is(value))
            && text_len >= 120
        {
            documentation_context = true;
        }
        true
    });
    let schemas = metadata.schema_types.join(" ").to_ascii_lowercase();
    if schemas.contains("service") {
        return PageType::Service;
    }
    if schemas.contains("discussion")
        || schemas.contains("qapage")
        || (discussion_records >= 2 && discussion_chars >= 80)
    {
        return PageType::Discussion;
    }
    if schemas.contains("searchresultspage")
        || (listing_records >= 5 && prose_chars < 600)
        || text_listing
    {
        return PageType::Listing;
    }
    if schemas.contains("product") || schema_product || product_records == 1 {
        return PageType::Product;
    }
    let path = page.map_or("", Url::path).to_ascii_lowercase();
    if metadata
        .browser_title
        .as_deref()
        .or(metadata.title.as_deref())
        .is_some_and(|title| title.to_ascii_lowercase().contains("wikipedia"))
    {
        return PageType::Article;
    }
    if schemas.contains("apireference")
        || metadata.title.as_deref().is_some_and(|title| {
            let title = title.to_ascii_lowercase();
            title.contains("cookbook:") || title.contains("wikibooks")
        })
        || path.contains("/docs")
        || path.contains("/api")
        || (documentation_context
            && !metadata
                .title
                .as_deref()
                .is_some_and(|title| title.to_ascii_lowercase().contains("wikipedia")))
        || (code_blocks > 1 && code_chars > prose_chars)
    {
        return PageType::Documentation;
    }
    if schemas.contains("article")
        || schemas.contains("blogposting")
        || prose_blocks >= 4
        || prose_chars >= 600
    {
        return PageType::Article;
    }
    if sections >= 3 {
        return PageType::Service;
    }
    if tables > 0 && product_records > 0 {
        return PageType::Product;
    }
    PageType::Generic
}

fn ancestor_has_discussion_token(dom: &Dom, id: NodeId) -> bool {
    let mut parent = dom.parent(id);
    while let Some(node) = parent {
        if ["comment", "reply", "answer", "message", "post"]
            .iter()
            .any(|wanted| has_ascii_token(dom, node, wanted))
        {
            return true;
        }
        parent = dom.parent(node);
    }
    false
}

fn title_node(
    dom: &Dom,
    root: NodeId,
    metadata_title: Option<&str>,
    page_type: PageType,
) -> Option<NodeId> {
    let headings = find_all(dom, root, |id| {
        matches!(dom.tag(id), Some("h1" | "h2" | "h3" | "h4" | "h5" | "h6")) && !hidden(dom, id)
    });
    if headings.is_empty() {
        return None;
    }
    let authored_markdown = dom.attr(root, "class").is_some_and(|value| {
        value
            .split_whitespace()
            .any(|class| class.eq_ignore_ascii_case("markdown-body"))
    });
    if authored_markdown {
        return headings
            .iter()
            .copied()
            .find(|id| dom.tag(*id) == Some("h1"));
    }
    if page_type == PageType::Product {
        if let Some(title) = metadata_title {
            if let Some(found) = headings
                .iter()
                .copied()
                .find(|id| normalized_label(&dom.text(*id)) == normalized_label(title))
            {
                return Some(found);
            }
        }
        return headings
            .iter()
            .copied()
            .find(|id| dom.tag(*id) == Some("h1"));
    }
    if let Some(title) = metadata_title {
        if let Some(found) = headings.iter().copied().find(|id| {
            dom.tag(*id) == Some("h1")
                && ((ancestor_tag(dom, *id, "header")
                    && !ancestor_attribute_contains(dom, *id, "mw-body-header"))
                    || ["class", "itemprop", "data-editable"].iter().any(|key| {
                        let value = dom.attr(*id, key).unwrap_or("").to_ascii_lowercase();
                        value.contains("headline")
                            || value.contains("article-title")
                            || value.contains("page-title")
                    }))
        }) {
            return Some(found);
        }
        for separator in [" | ", " - ", " — ", " – ", " :: "] {
            if let Some((left, right)) = title.split_once(separator) {
                let first_h1 = headings
                    .iter()
                    .copied()
                    .find(|id| dom.tag(*id) == Some("h1"));
                let first_label = first_h1.map(|id| normalized_label(&dom.text(id)));
                let wanted = if first_label.as_deref() == Some(&normalized_label(left))
                    && first_h1.is_some_and(|id| node_has_home_link(dom, id))
                {
                    right
                } else {
                    left
                };
                if let Some(found) = headings
                    .iter()
                    .copied()
                    .find(|id| normalized_label(&dom.text(*id)) == normalized_label(wanted))
                {
                    return Some(found);
                }
            }
        }
        let normalized = normalized_label(title);
        if let Some(found) = headings.iter().copied().find(|id| {
            let heading = normalized_label(&dom.text(*id));
            heading == normalized
                || heading
                    .strip_prefix(&normalized)
                    .is_some_and(|suffix| suffix.starts_with(' '))
        }) {
            return Some(found);
        }
        return None;
    }
    // Lower-level headings are section boundaries, not safe title fallbacks.
    // Treating the first h2 as a title silently removed real article sections.
    let _ = page_type;
    headings
        .iter()
        .copied()
        .find(|id| dom.tag(*id) == Some("h1"))
}

fn node_has_home_link(dom: &Dom, id: NodeId) -> bool {
    dom.find_first(id, |node| {
        dom.tag(node) == Some("a")
            && matches!(
                dom.attr(node, "href").unwrap_or("").trim(),
                "/" | "." | "./"
            )
    })
    .is_some()
}

fn ancestor_attribute_contains(dom: &Dom, id: NodeId, wanted: &str) -> bool {
    let mut parent = dom.parent(id);
    while let Some(node) = parent {
        if [dom.attr(node, "id"), dom.attr(node, "class")]
            .into_iter()
            .flatten()
            .any(|value| contains_ascii_case_insensitive(value, wanted))
        {
            return true;
        }
        parent = dom.parent(node);
    }
    false
}

fn ancestor_tag(dom: &Dom, id: NodeId, tag: &str) -> bool {
    let mut parent = dom.parent(id);
    while let Some(node) = parent {
        if dom.tag(node) == Some(tag) {
            return true;
        }
        parent = dom.parent(node);
    }
    false
}

fn resolve_title(
    dom: &Dom,
    heading: Option<NodeId>,
    metadata: &Metadata,
    page: Option<&Url>,
) -> Option<String> {
    if let Some(browser) = metadata.browser_title.as_deref() {
        let lower = browser.to_ascii_lowercase();
        if lower.contains("wikipedia") || lower.contains("wikibooks") {
            let value = browser.split_whitespace().collect::<Vec<_>>().join(" ");
            return (!value.is_empty()).then_some(value);
        }
    }
    if let Some(heading) = heading {
        let value = dom.text(heading);
        let decorated = metadata.title.as_deref().is_some_and(|title| {
            let heading = normalized_label(&value);
            let title = normalized_label(title);
            heading != title
                && (punctuation_fold(&value) == punctuation_fold(&title)
                    || heading
                        .strip_prefix(&title)
                        .is_some_and(|suffix| suffix.starts_with(' ')))
        });
        if !value.is_empty() && !decorated {
            return Some(
                value
                    .strip_suffix(" [duplicate]")
                    .unwrap_or(&value)
                    .to_owned(),
            );
        }
    }
    let mut value = metadata
        .social_title
        .clone()
        .or_else(|| metadata.title.clone())?;
    if let Some(social) = metadata.social_title.as_deref() {
        let social = normalized_label(social);
        if let Some(visible) = dom.find_first(dom.document(), |id| {
            if dom.tag(id) != Some("h1") || hidden(dom, id) {
                return false;
            }
            let heading = normalized_label(&dom.text(id));
            heading.len() <= 120
                && heading
                    .strip_prefix(&social)
                    .is_some_and(|suffix| suffix.starts_with(' '))
        }) {
            value = dom.text(visible).into_owned();
        }
    }
    let mut decorations = Vec::new();
    if let Some(site) = &metadata.site_name {
        decorations.push(site.clone());
    }
    if let Some(page) = page {
        if let Some(host) = page.host_str() {
            decorations.push(
                host.trim_start_matches("www.")
                    .split('.')
                    .next()
                    .unwrap_or("")
                    .to_owned(),
            );
        }
    }
    if let Some(site_heading) = dom.find_first(dom.document(), |id| {
        dom.tag(id) == Some("h1") && node_has_home_link(dom, id)
    }) {
        let site_heading = dom.text(site_heading);
        for separator in [" | ", " - ", " — ", " – ", " :: "] {
            if let Some((left, right)) = value.split_once(separator) {
                if normalized_label(left) == normalized_label(&site_heading) {
                    value = right.into();
                    break;
                }
                if normalized_label(right) == normalized_label(&site_heading) {
                    value = left.into();
                    break;
                }
            }
        }
    }
    for decoration in decorations {
        for separator in [" | ", " - ", " — ", " – ", " :: "] {
            if let Some((left, right)) = value.split_once(separator) {
                if normalized_label(left) == normalized_label(&decoration) {
                    value = right.into();
                } else if normalized_label(right) == normalized_label(&decoration) {
                    value = left.into();
                }
            }
        }
    }
    let value = value.split_whitespace().collect::<Vec<_>>().join(" ");
    let value = value
        .strip_suffix(" [duplicate]")
        .unwrap_or(&value)
        .to_owned();
    (!value.is_empty()).then_some(value)
}

fn normalized_label(value: &str) -> String {
    value
        .to_lowercase()
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ")
        .trim_matches(|c: char| c.is_ascii_punctuation())
        .to_owned()
}
fn punctuation_fold(value: &str) -> String {
    value
        .to_lowercase()
        .chars()
        .map(|character| match character {
            '’' | '‘' | '`' => '\'',
            character => character,
        })
        .filter(|character| {
            character.is_alphanumeric() || character.is_whitespace() || *character == '\''
        })
        .collect::<String>()
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ")
}

fn contains_ascii_case_insensitive(value: &str, wanted: &str) -> bool {
    wanted.is_empty()
        || (wanted.len() <= value.len()
            && value
                .as_bytes()
                .windows(wanted.len())
                .any(|window| window.eq_ignore_ascii_case(wanted.as_bytes())))
}

fn attr_contains(dom: &Dom, id: NodeId, wanted: &str) -> bool {
    [dom.attr(id, "id"), dom.attr(id, "class")]
        .into_iter()
        .flatten()
        .any(|value| contains_ascii_case_insensitive(value, wanted))
}

fn has_token(dom: &Dom, id: NodeId, wanted: &str) -> bool {
    has_token_with_separator(dom, id, wanted, |character| !character.is_alphanumeric())
}

fn has_ascii_token(dom: &Dom, id: NodeId, wanted: &str) -> bool {
    has_token_with_separator(dom, id, wanted, |character| {
        !character.is_ascii_alphanumeric()
    })
}

fn has_token_with_separator(
    dom: &Dom,
    id: NodeId,
    wanted: &str,
    separator: impl Fn(char) -> bool + Copy,
) -> bool {
    [dom.attr(id, "id"), dom.attr(id, "class")]
        .into_iter()
        .flatten()
        .any(|value| {
            value
                .split(separator)
                .any(|token| token.eq_ignore_ascii_case(wanted))
        })
}
