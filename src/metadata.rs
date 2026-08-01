use serde_json::Value;
use url::Url;

use crate::{
    dom::{normalize_text, Dom, NodeId},
    url_policy,
};

#[derive(Default)]
pub(crate) struct Metadata {
    pub(crate) canonical: Option<Url>,
    pub(crate) title: Option<String>,
    pub(crate) browser_title: Option<String>,
    pub(crate) social_title: Option<String>,
    pub(crate) description: Option<String>,
    pub(crate) author: Option<String>,
    pub(crate) site_name: Option<String>,
    pub(crate) language: Option<String>,
    pub(crate) published_time: Option<String>,
    pub(crate) schema_types: Vec<String>,
}

#[allow(clippy::too_many_lines)]
pub(crate) fn extract(dom: &Dom, base: Option<&Url>) -> Metadata {
    let mut result = Metadata::default();
    let mut title_priority = 0;
    let mut description_priority = 0;
    let mut author_priority = 0;
    let mut published_priority = 0;
    dom.walk(dom.document(), &mut |id| {
        let Some(tag) = dom.tag(id) else { return true };
        if result.author.is_none()
            && dom
                .attr(id, "itemprop")
                .is_some_and(|value| value.eq_ignore_ascii_case("author"))
        {
            result.author = dom
                .find_first(id, |node| {
                    dom.attr(node, "itemprop")
                        .is_some_and(|value| value.eq_ignore_ascii_case("name"))
                })
                .and_then(|node| cleaned(&dom.text(node)));
            if result.author.is_some() {
                author_priority = 2;
            }
        }
        match tag {
            "html" => result.language = dom.attr(id, "lang").and_then(cleaned),
            "title" => {
                let value = cleaned(&dom.text(id));
                if result.browser_title.is_none() {
                    result.browser_title.clone_from(&value);
                }
                set_priority(&mut result.title, &mut title_priority, value, 1);
            }
            "h1" if title_priority == 0 => set_priority(
                &mut result.title,
                &mut title_priority,
                cleaned(&dom.text(id)),
                1,
            ),
            "meta" => {
                let key = dom
                    .attr(id, "property")
                    .or_else(|| dom.attr(id, "name"))
                    .or_else(|| dom.attr(id, "itemprop"))
                    .unwrap_or("")
                    .to_ascii_lowercase();
                let value = cleaned(dom.attr(id, "content").unwrap_or(""));
                match key.as_str() {
                    "description" => set_priority(
                        &mut result.description,
                        &mut description_priority,
                        plausible_description(value),
                        if dom.has_attr(id, "itemprop") { 4 } else { 1 },
                    ),
                    "og:description" | "twitter:description" => set_priority(
                        &mut result.description,
                        &mut description_priority,
                        plausible_description(value),
                        3,
                    ),
                    "author" => set_priority(&mut result.author, &mut author_priority, value, 1),
                    "article:author" => {
                        set_priority(&mut result.author, &mut author_priority, value, 3);
                    }
                    "og:site_name" => result.site_name = value,
                    "og:title" | "twitter:title" => {
                        if result.social_title.is_none() {
                            result.social_title.clone_from(&value);
                        }
                        set_priority(&mut result.title, &mut title_priority, value, 3);
                    }
                    "article:published_time" => set_priority(
                        &mut result.published_time,
                        &mut published_priority,
                        value,
                        3,
                    ),
                    "datepublished" => set_priority(
                        &mut result.published_time,
                        &mut published_priority,
                        value,
                        1,
                    ),
                    "og:type" => {
                        if let Some(value) = value {
                            result.schema_types.push(value);
                        }
                    }
                    _ => {}
                }
            }
            "link"
                if dom.attr(id, "rel").is_some_and(|rel| {
                    rel.split_whitespace()
                        .any(|v| v.eq_ignore_ascii_case("canonical"))
                }) =>
            {
                if let Some(raw) = dom.attr(id, "href") {
                    result.canonical = url_policy::canonical(raw, base);
                }
            }
            "time" if result.published_time.is_none() => {
                result.published_time = dom
                    .attr(id, "datetime")
                    .and_then(cleaned)
                    .or_else(|| cleaned(&dom.text(id)));
            }
            "script"
                if dom.attr(id, "type").is_some_and(|v| {
                    v.split(';')
                        .next()
                        .unwrap_or("")
                        .trim()
                        .eq_ignore_ascii_case("application/ld+json")
                }) =>
            {
                read_json_ld(
                    &dom.raw_text(id),
                    &mut result,
                    &mut title_priority,
                    &mut description_priority,
                    &mut author_priority,
                    &mut published_priority,
                );
            }
            _ => {}
        }
        true
    });
    result
}

fn cleaned(value: &str) -> Option<String> {
    let value = normalize_text(value);
    (!value.is_empty()).then_some(value)
}
fn plausible_description(value: Option<String>) -> Option<String> {
    value.filter(|v| {
        v.chars().count() <= 1000
            && !matches!(
                v.to_ascii_lowercase().as_str(),
                "article" | "other" | "page" | "post"
            )
    })
}
fn set_priority(
    slot: &mut Option<String>,
    priority: &mut u8,
    value: Option<String>,
    candidate: u8,
) {
    if value.is_some() && candidate > *priority {
        *slot = value;
        *priority = candidate;
    }
}

#[allow(clippy::items_after_statements, clippy::too_many_lines)]
fn read_json_ld(
    raw: &str,
    metadata: &mut Metadata,
    title_priority: &mut u8,
    description_priority: &mut u8,
    author_priority: &mut u8,
    published_priority: &mut u8,
) {
    let Ok(value) = serde_json::from_str::<Value>(raw) else {
        return;
    };
    fn visit(
        value: &Value,
        metadata: &mut Metadata,
        priorities: (&mut u8, &mut u8, &mut u8, &mut u8),
    ) {
        let (tp, dp, ap, pp) = priorities;
        match value {
            Value::Array(values) => {
                for value in values {
                    visit(value, metadata, (tp, dp, ap, pp));
                }
            }
            Value::Object(map) => {
                if let Some(types) = map.get("@type") {
                    match types {
                        Value::String(v) => metadata.schema_types.push(v.clone()),
                        Value::Array(v) => metadata
                            .schema_types
                            .extend(v.iter().filter_map(Value::as_str).map(str::to_owned)),
                        _ => {}
                    }
                }
                let entity_types = map
                    .get("@type")
                    .map(|value| match value {
                        Value::String(value) => value.to_ascii_lowercase(),
                        Value::Array(values) => values
                            .iter()
                            .filter_map(Value::as_str)
                            .collect::<Vec<_>>()
                            .join(" ")
                            .to_ascii_lowercase(),
                        _ => String::new(),
                    })
                    .unwrap_or_default();
                let named_page = [
                    "article",
                    "posting",
                    "webpage",
                    "qapage",
                    "product",
                    "service",
                    "recipe",
                    "techarticle",
                ]
                .iter()
                .any(|kind| entity_types.contains(kind));
                let title = map
                    .get("headline")
                    .or_else(|| named_page.then(|| map.get("name")).flatten())
                    .and_then(Value::as_str)
                    .and_then(cleaned);
                set_priority(&mut metadata.title, tp, title, 5);
                if named_page {
                    set_priority(
                        &mut metadata.description,
                        dp,
                        map.get("description")
                            .and_then(Value::as_str)
                            .and_then(cleaned)
                            .filter(|value| value.chars().count() <= 1000),
                        5,
                    );
                }
                let author = map
                    .get("author")
                    .and_then(|v| match v {
                        Value::String(v) => Some(v.as_str()),
                        Value::Object(m) => m.get("name").and_then(Value::as_str),
                        Value::Array(a) => {
                            a.iter().find_map(|v| v.get("name").and_then(Value::as_str))
                        }
                        _ => None,
                    })
                    .and_then(cleaned);
                set_priority(&mut metadata.author, ap, author, 5);
                set_priority(
                    &mut metadata.published_time,
                    pp,
                    map.get("datePublished")
                        .and_then(Value::as_str)
                        .and_then(cleaned),
                    5,
                );
                if let Some(main) = map.get("mainEntity") {
                    visit(main, metadata, (tp, dp, ap, pp));
                }
                if let Some(graph) = map.get("@graph") {
                    visit(graph, metadata, (tp, dp, ap, pp));
                }
            }
            _ => {}
        }
    }
    visit(
        &value,
        metadata,
        (
            title_priority,
            description_priority,
            author_priority,
            published_priority,
        ),
    );
}

pub(crate) fn discover_base(dom: &Dom, page: Option<&Url>) -> Option<Url> {
    let base = dom.find_first(dom.document(), |id: NodeId| {
        dom.tag(id) == Some("base") && dom.has_attr(id, "href")
    });
    let Some(base) = base else {
        return page.cloned();
    };
    let raw = dom.attr(base, "href").unwrap_or("").trim();
    let candidate = match page {
        Some(page) => page.join(raw).ok(),
        None => Url::parse(raw).ok(),
    };
    candidate
        .filter(|u| {
            matches!(u.scheme(), "http" | "https")
                && u.host_str().is_some()
                && !u.cannot_be_a_base()
        })
        .or_else(|| page.cloned())
}
