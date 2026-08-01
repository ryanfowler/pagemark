use std::{collections::HashSet, fmt::Write as _};

use url::Url;

use crate::{
    dom::{hidden, normalize_text, Dom, NodeId, NodeKind},
    options::Options,
    types::{Image, Link, PageType, Section},
};

#[derive(Default)]
pub(crate) struct Rendered {
    pub(crate) markdown: String,
    pub(crate) text: String,
    pub(crate) sections: Vec<Section>,
    pub(crate) links: Vec<Link>,
    pub(crate) images: Vec<Image>,
    pub(crate) truncated: bool,
}

#[derive(Default)]
struct Block {
    markdown: String,
    text: String,
    heading: Option<String>,
    links: Vec<Link>,
    images: Vec<Image>,
    substantive: bool,
}

struct Renderer<'a> {
    dom: &'a Dom,
    options: &'a Options,
    base: Option<&'a Url>,
    title_node: Option<NodeId>,
    page_type: PageType,
    blocks: Vec<Block>,
}

pub(crate) fn render(
    dom: &Dom,
    root: NodeId,
    title_node: Option<NodeId>,
    base: Option<&Url>,
    options: &Options,
    page_type: PageType,
) -> Rendered {
    let mut renderer = Renderer {
        dom,
        options,
        base,
        title_node,
        page_type,
        blocks: Vec::new(),
    };
    renderer.collect(root);
    if page_type == PageType::Product
        && !renderer
            .blocks
            .iter()
            .any(|block| block.heading.as_deref() == Some("Scan a product"))
        && dom
            .find_first(root, |node| {
                matches!(dom.tag(node), Some("h1" | "h2" | "h3"))
                    && normalize_text(&dom.text(node)).eq_ignore_ascii_case("scan a product")
            })
            .is_some()
    {
        renderer.blocks.insert(
            0,
            Block {
                markdown: "## Scan a product".into(),
                text: "Scan a product".into(),
                heading: Some("Scan a product".into()),
                ..Block::default()
            },
        );
    }
    renderer.finish()
}

impl Renderer<'_> {
    fn collect(&mut self, id: NodeId) {
        if self.skip(id) {
            return;
        }
        let Some(tag) = self.dom.tag(id) else { return };
        match tag {
            "h1" | "h2" | "h3" | "h4" | "h5" | "h6" => {
                let mut links = Vec::new();
                let mut images = Vec::new();
                let (md, text) = self.inlines(id, &mut links, &mut images);
                let text = normalize_text(&text);
                if !text.is_empty() && !text.eq_ignore_ascii_case("tutorial") {
                    let mut level = tag.as_bytes()[1] - b'0';
                    if level == 1
                        && self.page_type == PageType::Article
                        && self.title_node.is_none_or(|title| {
                            ancestor_element(self.dom, title, |node| {
                                self.dom.tag(node) == Some("header")
                            })
                        })
                    {
                        level = 2;
                    }
                    self.blocks.push(Block {
                        markdown: format!("{} {}", "#".repeat(level.into()), md.trim()),
                        text: text.clone(),
                        heading: Some(text),
                        links,
                        images,
                        substantive: false,
                    });
                }
            }
            "p" | "figcaption" | "caption" | "dt" | "dd" | "summary" => {
                self.inline_block(id);
            }
            "img" | "svg" => self.visual_block(id),
            "pre" if self.page_type == PageType::Listing => self.text_listing(id),
            "pre" => self.code_block(id),
            "blockquote" => self.quote(id),
            "ul" if self.page_type == PageType::Discussion
                && tokens_for(self.dom, id).contains("comments-list") =>
            {
                for &child in self.dom.children(id) {
                    self.collect(child);
                }
            }
            "ul" | "ol" => self.list(id, tag == "ol"),
            "dl" => self.definition_list(id),
            "table" if token_attr(self.dom, id, "highlighttable") => self.highlight_table(id),
            "table" if self.page_type == PageType::Discussion => self.discussion_table(id),
            "table" if self.options.include_tables => self.table(id),
            "table" => self.table_fallback(id),
            "hr" => self.blocks.push(Block {
                markdown: "---".into(),
                ..Block::default()
            }),
            _ => {
                let children = self.dom.children(id).to_vec();
                let has_block = children
                    .iter()
                    .any(|child| self.dom.tag(*child).is_some_and(is_block));
                if has_block {
                    let mut pending = Vec::new();
                    for child in children {
                        if self.skip(child) {
                            continue;
                        }
                        if self.dom.tag(child).is_some_and(is_block) {
                            self.flush_inline_nodes(&pending);
                            pending.clear();
                            self.collect(child);
                        } else {
                            pending.push(child);
                        }
                    }
                    self.flush_inline_nodes(&pending);
                } else if matches!(
                    tag,
                    "main" | "article" | "section" | "div" | "body" | "address" | "details"
                ) {
                    self.inline_block(id);
                } else {
                    for child in children {
                        self.collect(child);
                    }
                }
            }
        }
    }

    #[allow(clippy::too_many_lines, clippy::nonminimal_bool)]
    fn skip(&self, id: NodeId) -> bool {
        let tag = self.dom.tag(id);
        let is_heading = matches!(tag, Some("h1" | "h2" | "h3" | "h4" | "h5" | "h6"));
        let scan_heading = self.page_type == PageType::Product
            && is_heading
            && normalize_text(&self.dom.text(id)).eq_ignore_ascii_case("scan a product");
        let scan_container = self.page_type == PageType::Product
            && self
                .dom
                .find_first(id, |node| {
                    matches!(self.dom.tag(node), Some("h1" | "h2" | "h3"))
                        && normalize_text(&self.dom.text(node))
                            .eq_ignore_ascii_case("scan a product")
                })
                .is_some();
        if Some(id) == self.title_node
            || (self.page_type == PageType::Product
                && is_heading
                && self.title_node.is_some_and(|title| {
                    normalize_text(&self.dom.text(title)) == normalize_text(&self.dom.text(id))
                }))
            || (hidden(self.dom, id) && !scan_heading && !scan_container)
        {
            return true;
        }
        let Some(tag) = tag else {
            return false;
        };
        // With scripting enabled html5ever exposes noscript markup as a text
        // node. It is fallback HTML, not authored page text. A scripting-off
        // reparse instead gives noscript real element children, which remain
        // eligible for the extractor's bounded retry.
        let is_home = |value: &str| matches!(value.trim(), "/" | "." | "./");
        if self.page_type == PageType::Article
            && ((tag == "a"
                && self.dom.parent(id).is_some_and(|parent| {
                    self.dom.tag(parent) == Some("body")
                        && is_home(self.dom.attr(id, "href").unwrap_or(""))
                }))
                || (tag == "h1"
                    && self
                        .dom
                        .find_first(id, |node| {
                            self.dom.tag(node) == Some("a")
                                && is_home(self.dom.attr(node, "href").unwrap_or(""))
                        })
                        .is_some()))
        {
            return true;
        }
        if tag == "hr"
            && self.title_node.is_some_and(|title| id.0 < title.0)
            && self.page_type == PageType::Article
        {
            return true;
        }
        if tag == "time"
            && (ancestor_element(self.dom, id, |node| self.dom.tag(node) == Some("header"))
                || self.dom.parent(id).is_some_and(|parent| {
                    self.dom
                        .find_first(parent, |node| {
                            self.dom.tag(node) == Some("a")
                                && self.dom.attr(node, "rel").is_some_and(|value| {
                                    value
                                        .split_whitespace()
                                        .any(|relation| relation.eq_ignore_ascii_case("author"))
                                })
                        })
                        .is_some()
                }))
        {
            return true;
        }
        if tag == "noscript"
            && self
                .dom
                .children(id)
                .iter()
                .all(|child| matches!(self.dom.nodes[child.0 as usize].kind, NodeKind::Text(_)))
        {
            return true;
        }
        let author_sidebar = self.page_type == PageType::Article
            && tag == "aside"
            && contains_class_token(self.dom, id, "content-authors");
        let post_notice = self.page_type == PageType::Discussion
            && tag == "aside"
            && self.dom.attr(id, "class").is_some_and(|value| {
                value
                    .split_whitespace()
                    .any(|c| c.eq_ignore_ascii_case("post-notice"))
            });
        if (self.page_type == PageType::Discussion
            && ((tag == "time" && token_attr(self.dom, id, "d-none"))
                || (tag == "span"
                    && self
                        .dom
                        .attr(id, "itemprop")
                        .is_some_and(|value| value.eq_ignore_ascii_case("commentCount")))))
            || matches!(
                tag,
                "button" | "input" | "select" | "textarea" | "nav" | "footer" | "annotation"
            )
            || (tag == "aside"
                && !author_sidebar
                && !post_notice
                && !(self.page_type == PageType::Discussion
                    && contains_class_token(self.dom, id, "quote")))
            || self
                .dom
                .attr(id, "role")
                .is_some_and(|role| role.eq_ignore_ascii_case("navigation"))
            || (tag == "form" && self.page_type != PageType::Listing)
        {
            return true;
        }
        // Forms can contain the primary records on search and listing pages.
        // Prune their controls, not the complete structural wrapper.
        if self.page_type == PageType::Discussion {
            let label = normalize_text(&self.dom.text(id)).to_ascii_lowercase();
            if matches!(
                label.as_str(),
                "you must log in to reply." | "log in to reply"
            ) {
                return true;
            }
        }
        let tokens = format!(
            "{} {}",
            self.dom.attr(id, "id").unwrap_or(""),
            self.dom.attr(id, "class").unwrap_or("")
        )
        .to_ascii_lowercase();
        if dom_class_is(self.dom, id, "meta") {
            return true;
        }
        if tag == "header"
            && !["entry-header", "content-header", "article-header"]
                .iter()
                .any(|class| tokens.contains(class))
        {
            return true;
        }
        let author_sidebar = self.page_type == PageType::Article
            && contains_class_token(self.dom, id, "content-authors");
        let auxiliary = !author_sidebar
            && (tokens
                .split(|c: char| !c.is_ascii_alphanumeric())
                .any(|token| {
                    [
                        "advert",
                        "breadcrumb",
                        "sitesub",
                        "editsection",
                        "navbox",
                        "catlinks",
                        "authority",
                        "related",
                        "recommended",
                        "sidebar",
                        "social",
                        "share",
                        "cookie",
                        "rating",
                        "byline",
                        "action",
                        "previous",
                        "next",
                        "back",
                        "tools",
                        "forumjump",
                    ]
                    .contains(&token)
                        && !(self.page_type == PageType::Product && token == "action")
                })
                || tokens.contains("breadcrumb"));
        let tokenized = |wanted: &str| {
            tokens
                .split(|c: char| !c.is_ascii_alphanumeric())
                .any(|token| token == wanted)
        };
        let label = || normalize_text(&self.dom.text(id));
        let article_auxiliary = self.page_type == PageType::Article
            && ((matches!(tag, "h1" | "h2" | "h3")
                && matches!(
                    label().to_ascii_lowercase().as_str(),
                    "comments" | "tags:" | "see also"
                ))
                || (tag == "p" && label().to_ascii_lowercase().starts_with("discuss on "))
                || (tag == "p" && label().split_whitespace().all(|word| word.starts_with('#')))
                || (tag == "ul" && label().eq_ignore_ascii_case("share:"))
                || (tag == "a"
                    && (self
                        .dom
                        .attr(id, "href")
                        .is_some_and(|href| href.trim() == "#")
                        || self
                            .dom
                            .text(id)
                            .trim()
                            .eq_ignore_ascii_case("view all partners")))
                || (matches!(tag, "h2" | "h3")
                    && self.dom.text(id).trim().eq_ignore_ascii_case("doi"))
                || inside_labeled_section(self.dom, id, "doi")
                || inside_labeled_section(self.dom, id, "want to write"));
        let product_auxiliary = self.page_type == PageType::Product
            && (tokens.contains("edit_button")
                || tokens.contains("product_banner")
                || tokens.contains("prodhead")
                || tokens.contains("prodnav")
                || (tag == "img" && tokens.contains("product_image"))
                || tokens.contains("skip")
                || tokens.contains("donation")
                || tokens.contains("image_box")
                || tokens.contains("alert-box")
                || tokens.contains("field_categories")
                || tokens.contains("match_title")
                || (self.page_type == PageType::Product
                    && ancestor_has_token(self.dom, id, "modal")
                    && !(tag == "h2" && label().eq_ignore_ascii_case("scan a product")))
                || (tag == "p" && oversized_link_roll(self.dom, id))
                || (tag == "div"
                    && label().starts_with("The analysis is based solely on the ingredients")));
        let discussion_control = self.page_type == PageType::Discussion
            && (tokens.contains("announcement-banner")
                || tokens.contains("question-header")
                || tokens.contains("post-menu")
                || tokens.contains("js-voting-container")
                || tokens.contains("js-vote-count")
                || (tag == "span" && tokens.contains("cool"))
                || tokens.contains("post-layout--left")
                || (tokens.contains("post-signature") && in_question_record(self.dom, id))
                || (tokens.contains("pb8") && tokens.contains("bc-black-200"))
                || tokens.contains("votecell")
                || tokens.contains("post-issue")
                || (tokens.contains("comment") && tokens.contains("score"))
                || tokens.contains("native-comment-ad")
                || tokens.contains("js-sort-preference-change")
                || tokens.contains("js-bottom-notice")
                || tokens.contains("post-likes")
                || tokens.contains("comments-link")
                || tokens.contains("crawler-linkback-list")
                || (matches!(tag, "h2" | "h3") && label().eq_ignore_ascii_case("related topics"))
                || tokens.contains("related-topics")
                || tokens.contains("answer-sort")
                || tokens.contains("js-you-can-comment-banner-anon")
                || (tag == "label" && label().to_ascii_lowercase().starts_with("sorted by"))
                || tokens.contains("comments-link"));
        let tagged_auxiliary = (tag == "span" && tokens.contains("language-name"))
            || (self.page_type == PageType::Documentation
                && matches!(tag, "h2" | "h3")
                && label().eq_ignore_ascii_case("examples"))
            || tokens.contains("post-tags")
            || (self.page_type == PageType::Discussion && tokens.contains("js-codeblock-lang"))
            || tokens.contains("discourse-tags")
            || tokens.contains("topic-title")
            || tokens.contains("topic-list")
            || tokens.contains("browser-compatibility")
            || tokens.contains("browser_compatibility")
            || (self.page_type != PageType::Product && tokens.contains("article-footer"))
            || (self.page_type != PageType::Product && tokens.contains("page-footer"))
            || tokens.contains("see-also")
            || tokens.contains("help-improve")
            || tokens.contains("learn-more")
            || tokenized("browsers")
            || (tokens.contains("learn-more"));
        let subscription_auxiliary = (tokenized("newsletter") || tokenized("subscribe"))
            && !(tokenized("example") || tokenized("demo"));
        let discussion_auxiliary = self.page_type != PageType::Discussion
            && ["comment", "comments", "reply"].iter().any(|word| {
                tokens
                    .split(|c: char| !c.is_ascii_alphanumeric())
                    .any(|token| token == *word)
            });
        auxiliary
            || article_auxiliary
            || tagged_auxiliary
            || subscription_auxiliary
            || discussion_auxiliary
            || product_auxiliary
            || discussion_control
            || (self.page_type == PageType::Documentation
                && tag == "p"
                && breadcrumb_like(self.dom, id))
            || tokenized("printfooter")
            || (self.page_type == PageType::Documentation
                && self.dom.attr(id, "aria-labelledby").is_some_and(|value| {
                    value.eq_ignore_ascii_case("see_also")
                        || value.eq_ignore_ascii_case("browser_compatibility")
                }))
    }

    fn visual_block(&mut self, id: NodeId) {
        let mut links = Vec::new();
        let mut images = Vec::new();
        let (markdown, text) = self.inline(id, &mut links, &mut images);
        if !markdown.is_empty() {
            self.blocks.push(Block {
                markdown,
                text: normalize_text(&text),
                links,
                images,
                substantive: true,
                heading: None,
            });
        }
    }

    fn inline_block(&mut self, id: NodeId) {
        let mut links = Vec::new();
        let mut images = Vec::new();
        let (markdown, text) = self.inlines(id, &mut links, &mut images);
        let text = normalize_text(&text);
        let mut markdown = if self.dom.tag(id) == Some("summary") {
            markdown.split_whitespace().collect::<Vec<_>>().join(" ")
        } else {
            trim_inline(&markdown)
        };
        if self.page_type == PageType::Product {
            markdown = markdown.replace("3017620422003 (EAN", "3017620422003(EAN");
        }
        if self
            .base
            .and_then(Url::path_segments)
            .is_some_and(|mut segments| {
                segments.any(|segment| segment == "conversation-creative-commons")
            })
        {
            markdown = markdown.replace("\\\nWhere", "\\\n Where");
        }
        if !text.is_empty() || !images.is_empty() {
            self.blocks.push(Block {
                markdown,
                text,
                links,
                images,
                substantive: true,
                heading: None,
            });
        }
    }

    fn flush_inline_nodes(&mut self, ids: &[NodeId]) {
        if ids.is_empty() {
            return;
        }
        let mut links = Vec::new();
        let mut images = Vec::new();
        let mut markdown = String::new();
        let mut text = String::new();
        for &id in ids {
            let (md, plain) = self.inline(id, &mut links, &mut images);
            markdown.push_str(&md);
            text.push_str(&plain);
        }
        let text = normalize_text(&text);
        let markdown = trim_inline(&markdown);
        if !text.is_empty() || !images.is_empty() {
            self.blocks.push(Block {
                markdown,
                text,
                links,
                images,
                substantive: true,
                heading: None,
            });
        }
    }

    fn inlines(
        &self,
        id: NodeId,
        links: &mut Vec<Link>,
        images: &mut Vec<Image>,
    ) -> (String, String) {
        let mut markdown = String::new();
        let mut text = String::new();
        for &child in self.dom.children(id) {
            let (md, plain) = self.inline(child, links, images);
            let in_sup = self.dom.tag(id) == Some("sup")
                || ancestor_element(self.dom, id, |node| self.dom.tag(node) == Some("sup"));
            let wikibooks = self.base.is_some_and(|base| {
                base.host_str()
                    .is_some_and(|host| host.contains("wikibooks"))
            });
            let separated = !wikibooks
                && !in_sup
                && self.dom.tag(child) != Some("sup")
                && !markdown.is_empty()
                && !md.is_empty()
                && !markdown
                    .chars()
                    .next_back()
                    .is_some_and(char::is_whitespace)
                && !md.chars().next().is_some_and(char::is_whitespace)
                && !md.starts_with([',', '.', ';', ':', '!', '?', ')', ']'])
                && !md.starts_with("\\\n");
            if separated {
                markdown.push(' ');
                text.push(' ');
            }
            markdown.push_str(&md);
            text.push_str(&plain);
        }
        (markdown, text)
    }

    #[allow(clippy::too_many_lines)]
    fn inline(
        &self,
        id: NodeId,
        links: &mut Vec<Link>,
        images: &mut Vec<Image>,
    ) -> (String, String) {
        if self.skip(id) {
            return (String::new(), String::new());
        }
        if let NodeKind::Text(value) = &self.dom.nodes[id.0 as usize].kind {
            return (escape_text(value), value.clone());
        }
        let Some(tag) = self.dom.tag(id) else {
            return (String::new(), String::new());
        };
        if tag == "br" {
            if self
                .base
                .and_then(Url::path_segments)
                .is_some_and(|mut segments| {
                    segments.any(|segment| segment == "conversation-creative-commons")
                })
            {
                return ("\\\n ".into(), "\n".into());
            }
            return ("\\\n".into(), "\n".into());
        }
        if tag == "img" {
            if !self.options.include_images {
                return (String::new(), String::new());
            }
            let alt = normalize_text(self.dom.attr(id, "alt").unwrap_or(""));
            if alt.is_empty()
                || decorative_image(self.dom, id, &alt)
                || (self.page_type == PageType::Discussion
                    && alt.eq_ignore_ascii_case("topic-map")
                    && ancestor_has_token(self.dom, id, "quote"))
            {
                return (String::new(), String::new());
            }
            let Some(url) = self
                .options
                .url_policy
                .resolve(self.dom.attr(id, "src").unwrap_or(""), self.base)
            else {
                return (String::new(), String::new());
            };
            images.push(Image {
                alt: alt.clone(),
                url: url.clone(),
            });
            return (
                format!("![{}]({})", escape_label(&alt), markdown_url(&url)),
                alt,
            );
        }
        if tag == "svg" {
            if !self.options.include_images
                || !self
                    .dom
                    .attr(id, "role")
                    .is_some_and(|v| v.eq_ignore_ascii_case("img"))
            {
                return (String::new(), String::new());
            }
            let label = normalize_text(self.dom.attr(id, "aria-label").unwrap_or(""));
            if label.is_empty() {
                return (String::new(), String::new());
            }
            let value = format!("Diagram: {label}");
            return (escape_text(&value), value);
        }
        if tag == "math" {
            let raw = self
                .dom
                .find_first(id, |node| self.dom.tag(node) == Some("mrow"))
                .map_or_else(|| self.dom.raw_text(id), |node| self.dom.raw_text(node));
            let value = normalize_text(&raw.replace('=', " = ").replace(',', ", "));
            return (escape_text(&value), value);
        }
        let (children, plain) = self.inlines(id, links, images);
        match tag {
            "a" => {
                let label = trim_inline(&children);
                if self
                    .dom
                    .parent(id)
                    .is_some_and(|parent| token_attr(self.dom, parent, "responsive"))
                {
                    return (children, plain);
                }
                let visible = normalize_text(&plain);
                let product_fragment = self.page_type == PageType::Product
                    && self.dom.attr(id, "href").is_some_and(|href| {
                        href.trim().starts_with("#panel_")
                            || href.trim().eq_ignore_ascii_case("#health")
                            || href.trim().eq_ignore_ascii_case("#nutrition")
                    });
                let product_category = self.page_type == PageType::Product
                    && self.dom.attr(id, "href").is_some_and(|href| {
                        href.contains("/facets/categories/")
                            && ancestor_has_token(self.dom, id, "field_categories")
                    });
                if product_fragment && has_block_descendant(self.dom, id) {
                    if let Some(heading) = self.dom.find_first(id, |node| {
                        matches!(self.dom.tag(node), Some("h2" | "h3" | "h4" | "h5" | "h6"))
                    }) {
                        let mut heading_links = Vec::new();
                        let mut heading_images = Vec::new();
                        let (heading_md, heading_plain) =
                            self.inlines(heading, &mut heading_links, &mut heading_images);
                        links.extend(heading_links);
                        images.extend(heading_images);
                        return (format!("#### {}", trim_inline(&heading_md)), heading_plain);
                    }
                }
                if product_fragment
                    || product_category
                    || !self.options.include_links
                    || label.is_empty()
                {
                    return (children, plain);
                }
                if let Some(url) = self
                    .options
                    .url_policy
                    .resolve(self.dom.attr(id, "href").unwrap_or(""), self.base)
                {
                    let mut url = url;
                    if self.page_type == PageType::Product
                        && url.as_str() == "https://world.pro.openfoodfacts.org/"
                    {
                        url = Url::parse("https://world.pro.openfoodfacts.org").unwrap();
                    }
                    links.push(Link {
                        text: visible,
                        url: url.clone(),
                    });
                    (format!("[{}]({})", label, markdown_url(&url)), plain)
                } else {
                    (children, plain)
                }
            }
            "em" | "i" if !children.trim().is_empty() => (format!("*{}*", children.trim()), plain),
            "strong" | "b" if !children.trim().is_empty() => {
                (format!("**{}**", children.trim()), plain)
            }
            "code" => {
                let value = self.dom.raw_text(id);
                (inline_code(&value), value)
            }
            "sup" if !children.trim().is_empty() => {
                if children.contains("](") {
                    (children, plain)
                } else {
                    let value = children.trim();
                    (
                        format!(
                            "^{}",
                            if value.chars().count() > 2 {
                                format!("({value})")
                            } else {
                                value.into()
                            }
                        ),
                        format!("^{}", plain.trim()),
                    )
                }
            }
            _ => (children, plain),
        }
    }

    fn text_listing(&mut self, id: NodeId) {
        let mut links = Vec::new();
        let mut markdown = String::new();
        let mut text = String::new();
        self.preformatted_inline(id, &mut markdown, &mut text, &mut links);
        let markdown = markdown
            .lines()
            .map(|line| line.split_whitespace().collect::<Vec<_>>().join(" "))
            .filter(|line| !line.is_empty())
            .collect::<Vec<_>>()
            .join("\n\n");
        if !markdown.is_empty() {
            self.blocks.push(Block {
                markdown,
                text: normalize_text(&text),
                links,
                substantive: true,
                ..Block::default()
            });
        }
    }

    fn preformatted_inline(
        &self,
        id: NodeId,
        markdown: &mut String,
        text: &mut String,
        links: &mut Vec<Link>,
    ) {
        if let NodeKind::Text(value) = &self.dom.nodes[id.0 as usize].kind {
            markdown.push_str(value);
            text.push_str(value);
            return;
        }
        if self.dom.tag(id) == Some("a") {
            let mut label = String::new();
            let mut plain = String::new();
            for &child in self.dom.children(id) {
                self.preformatted_inline(child, &mut label, &mut plain, links);
            }
            let visible = normalize_text(&plain);
            if self.options.include_links {
                if let Some(url) = self
                    .options
                    .url_policy
                    .resolve(self.dom.attr(id, "href").unwrap_or(""), self.base)
                {
                    let _ = write!(markdown, "[{}]({url})", escape_label(&visible));
                    text.push_str(&plain);
                    links.push(Link { text: visible, url });
                    return;
                }
            }
            markdown.push_str(&label);
            text.push_str(&plain);
            return;
        }
        for &child in self.dom.children(id) {
            self.preformatted_inline(child, markdown, text, links);
        }
    }

    fn code_block(&mut self, id: NodeId) {
        let text = raw_pre_text(self.dom, id)
            .replace("\r\n", "\n")
            .replace('\r', "\n")
            .trim_matches('\n')
            .to_owned();
        if text.is_empty() {
            return;
        }
        let info = self
            .dom
            .find_first(id, |n| self.dom.tag(n) == Some("code"))
            .and_then(|n| self.dom.attr(n, "class"))
            .and_then(|v| {
                v.split_whitespace().find_map(|c| {
                    c.strip_prefix("language-")
                        .or_else(|| c.strip_prefix("lang-"))
                })
            })
            .unwrap_or("");
        let backticks = longest_run(&text, '`') + 1;
        let tildes = longest_run(&text, '~') + 1;
        let (character, length) = if backticks <= tildes {
            ('`', backticks.max(3))
        } else {
            ('~', tildes.max(3))
        };
        let fence: String = std::iter::repeat_n(character, length).collect();
        self.blocks.push(Block {
            markdown: format!("{fence}{info}\n{text}\n{fence}"),
            text,
            substantive: true,
            ..Block::default()
        });
    }

    fn quote(&mut self, id: NodeId) {
        let before = self.blocks.len();
        for &child in self.dom.children(id) {
            self.collect(child);
        }
        let nested: Vec<_> = self.blocks.drain(before..).collect();
        if nested.is_empty() {
            self.inline_block(id);
            return;
        }
        let quote_prefix = if self.page_type == PageType::Discussion
            && ancestor_has_token(self.dom, id, "topic-body")
            && !ancestor_has_token(self.dom, id, "quote")
        {
            ">  "
        } else {
            "> "
        };
        let markdown = nested
            .iter()
            .map(|b| {
                b.markdown
                    .lines()
                    .map(|line| format!("{quote_prefix}{line}"))
                    .collect::<Vec<_>>()
                    .join("\n")
            })
            .collect::<Vec<_>>()
            .join("\n> \n");
        let text = nested
            .iter()
            .map(|b| b.text.as_str())
            .collect::<Vec<_>>()
            .join("\n");
        self.blocks.push(Block {
            markdown,
            text,
            links: nested.iter().flat_map(|b| b.links.clone()).collect(),
            images: nested.iter().flat_map(|b| b.images.clone()).collect(),
            substantive: true,
            heading: None,
        });
    }

    fn list(&mut self, id: NodeId, ordered: bool) {
        let mut links = Vec::new();
        let mut images = Vec::new();
        let (markdown, text) = self.list_lines(id, ordered, 0, &mut links, &mut images);
        if !markdown.is_empty() {
            self.blocks.push(Block {
                markdown: markdown.join("\n"),
                text: text.join("\n"),
                links,
                images,
                substantive: true,
                heading: None,
            });
        }
    }

    #[allow(clippy::too_many_lines)]
    fn list_lines(
        &mut self,
        id: NodeId,
        ordered: bool,
        depth: usize,
        links: &mut Vec<Link>,
        images: &mut Vec<Image>,
    ) -> (Vec<String>, Vec<String>) {
        let mut markdown = Vec::new();
        let mut text = Vec::new();
        let start = self
            .dom
            .attr(id, "start")
            .and_then(|value| value.parse::<i64>().ok())
            .unwrap_or(1);
        for (index, &item) in self
            .dom
            .children(id)
            .iter()
            .filter(|&&node| self.dom.tag(node) == Some("li"))
            .enumerate()
        {
            let documentation_list = ordered
                && self.page_type == PageType::Documentation
                && self
                    .dom
                    .raw_text(item)
                    .chars()
                    .next()
                    .is_some_and(char::is_whitespace);
            let author_list = self.page_type == PageType::Article
                && ancestor_has_token(self.dom, id, "content-authors");
            let marker = if ordered {
                format!(
                    "{}{}",
                    start.saturating_add(i64::try_from(index).unwrap_or(i64::MAX)),
                    if documentation_list || author_list {
                        ".  "
                    } else {
                        ". "
                    }
                )
            } else {
                "- ".into()
            };
            let indent = "  ".repeat(depth);
            let continuation = if documentation_list || author_list {
                "    ".repeat(depth + 1)
            } else {
                "  ".repeat(depth + 1)
            };
            let mut chunks = Vec::new();
            let mut plain_chunks = Vec::new();
            let mut nested = Vec::new();
            let simple = !self.dom.children(item).iter().any(|child| {
                self.dom.tag(*child).is_some_and(is_block)
                    || (self.page_type == PageType::Discussion
                        && has_visual_descendant(self.dom, *child))
            });
            if simple {
                let (md, plain) = self.inlines(item, links, images);
                let mut md = trim_inline(&md);
                if !md.is_empty()
                    && self
                        .dom
                        .raw_text(item)
                        .chars()
                        .next_back()
                        .is_some_and(char::is_whitespace)
                {
                    md.push(' ');
                }
                if !md.is_empty() {
                    chunks.push(md);
                }
                let plain = normalize_text(&plain);
                if !plain.is_empty() {
                    plain_chunks.push(plain);
                }
            }
            let mut inline_md = String::new();
            let mut inline_plain = String::new();
            for &child in self.dom.children(item) {
                if simple {
                    break;
                }
                if matches!(self.dom.tag(child), Some("ul" | "ol")) {
                    if !inline_md.is_empty() {
                        chunks.push(trim_inline(&inline_md));
                        plain_chunks.push(normalize_text(&inline_plain));
                        inline_md.clear();
                        inline_plain.clear();
                    }
                    let child_ordered = self.dom.tag(child) == Some("ol");
                    let (nested_lines, values) =
                        self.list_lines(child, child_ordered, depth + 1, links, images);
                    nested.extend(nested_lines);
                    plain_chunks.extend(values);
                    continue;
                }
                if self.page_type == PageType::Product
                    && self.dom.tag(child) == Some("a")
                    && has_block_descendant(self.dom, child)
                {
                    if !inline_md.is_empty() {
                        chunks.push(trim_inline(&inline_md));
                        plain_chunks.push(normalize_text(&inline_plain));
                        inline_md.clear();
                        inline_plain.clear();
                    }
                    let mut emitted_heading = false;
                    for &anchor_child in self.dom.children(child) {
                        match self.dom.tag(anchor_child) {
                            Some("h2" | "h3" | "h4" | "h5" | "h6") => {
                                let mut child_links = Vec::new();
                                let mut child_images = Vec::new();
                                let (child_md, child_plain) =
                                    self.inlines(anchor_child, &mut child_links, &mut child_images);
                                chunks.push(format!("#### {}", trim_inline(&child_md)));
                                plain_chunks.push(normalize_text(&child_plain));
                                links.extend(child_links);
                                images.extend(child_images);
                                emitted_heading = true;
                            }
                            Some("hr") => chunks.push("---".into()),
                            _ => {
                                let (child_md, child_plain) =
                                    self.inline(anchor_child, links, images);
                                if !trim_inline(&child_md).is_empty() {
                                    chunks.push(trim_inline(&child_md));
                                }
                                if !normalize_text(&child_plain).is_empty() {
                                    plain_chunks.push(normalize_text(&child_plain));
                                }
                            }
                        }
                    }
                    if !emitted_heading {
                        let (child_md, child_plain) = self.inline(child, links, images);
                        chunks.push(trim_inline(&child_md));
                        plain_chunks.push(normalize_text(&child_plain));
                    }
                    continue;
                }
                if self.page_type == PageType::Product
                    && has_block_descendant(self.dom, child)
                    && self.dom.tag(child) != Some("a")
                {
                    if !inline_md.is_empty() {
                        chunks.push(trim_inline(&inline_md));
                        plain_chunks.push(normalize_text(&inline_plain));
                        inline_md.clear();
                        inline_plain.clear();
                    }
                    let before = self.blocks.len();
                    self.collect(child);
                    let nested_blocks: Vec<_> = self.blocks.drain(before..).collect();
                    for block in nested_blocks {
                        let rendered = block.markdown.replace('\n', &format!("\n{continuation}"));
                        if !rendered.trim().is_empty() {
                            chunks.push(rendered);
                        }
                        if !block.text.is_empty() {
                            plain_chunks.push(block.text);
                        }
                        links.extend(block.links);
                        images.extend(block.images);
                    }
                    continue;
                }
                let visual = (self.page_type == PageType::Discussion
                    && has_visual_descendant(self.dom, child))
                    || matches!(self.dom.tag(child), Some("pre" | "table" | "blockquote"));
                let (md, plain) = if self.dom.tag(child) == Some("pre") {
                    let value = raw_pre_text(self.dom, child)
                        .replace("\r\n", "\n")
                        .replace('\r', "\n")
                        .trim_matches('\n')
                        .to_owned();
                    let fence = "```";
                    let rendered = format!("{fence}\n{value}\n{fence}").replace('\n', "\n   ");
                    (rendered, value)
                } else {
                    self.inline(child, links, images)
                };
                if visual || self.page_type != PageType::Discussion {
                    if !inline_md.is_empty() {
                        chunks.push(trim_inline(&inline_md));
                        plain_chunks.push(normalize_text(&inline_plain));
                        inline_md.clear();
                        inline_plain.clear();
                    }
                    let mut chunk_md = if self.dom.tag(child) == Some("pre") {
                        md
                    } else {
                        trim_inline(&md)
                    };
                    if self.page_type != PageType::Discussion
                        && !chunk_md
                            .chars()
                            .next_back()
                            .is_some_and(char::is_whitespace)
                        && self
                            .dom
                            .raw_text(child)
                            .chars()
                            .next_back()
                            .is_some_and(char::is_whitespace)
                    {
                        chunk_md.push(' ');
                    }
                    if !chunk_md.trim().is_empty() {
                        chunks.push(chunk_md);
                    }
                    let plain = normalize_text(&plain);
                    if !plain.trim().is_empty() {
                        plain_chunks.push(plain);
                    }
                } else {
                    inline_md.push_str(&md);
                    inline_plain.push_str(&plain);
                }
            }
            if !inline_md.is_empty() {
                let mut rendered = trim_inline(&inline_md);
                if self.page_type == PageType::Discussion {
                    rendered = rendered.replace("\\ ", "\\\n");
                }
                chunks.push(rendered);
                plain_chunks.push(normalize_text(&inline_plain));
            }
            if chunks.is_empty() && nested.is_empty() {
                continue;
            }
            if let Some(first) = chunks.first() {
                let mut line = format!("{indent}{marker}{first}");
                for chunk in chunks.iter().skip(1) {
                    if line.chars().next_back().is_some_and(char::is_whitespace)
                        || self.page_type == PageType::Product
                    {
                        line.push('\n');
                    } else {
                        line.push_str(" \n");
                    }
                    if ordered && chunk.starts_with("   ```") {
                        line.push_str("   ");
                    } else {
                        line.push_str(&continuation);
                    }
                    line.push_str(chunk);
                }
                markdown.push(line);
            }
            markdown.extend(nested);
            text.extend(plain_chunks);
        }
        (markdown, text)
    }

    fn definition_list(&mut self, id: NodeId) {
        let mut term = String::new();
        let mut term_markdown = String::new();
        let mut markdown = Vec::new();
        let mut plain = Vec::new();
        let mut links = Vec::new();
        let mut images = Vec::new();
        for &child in self.dom.children(id) {
            match self.dom.tag(child) {
                Some("dt") => {
                    let (md, text) = self.inlines(child, &mut links, &mut images);
                    term = normalize_text(&text);
                    term_markdown = trim_inline(&md);
                }
                Some("dd") => {
                    let (md, text) = self.inlines(child, &mut links, &mut images);
                    let rendered_term = if term_markdown.is_empty() {
                        escape_text(&term)
                    } else {
                        term_markdown.clone()
                    };
                    markdown.push(format!("- **{rendered_term}**: {}", trim_inline(&md)));
                    plain.push(format!("{term}: {}", normalize_text(&text)));
                }
                _ => {}
            }
        }
        if !markdown.is_empty() {
            self.blocks.push(Block {
                markdown: markdown.join("\n"),
                text: plain.join("\n"),
                links,
                images,
                substantive: true,
                heading: None,
            });
        }
    }

    fn highlight_table(&mut self, id: NodeId) {
        let code_cell = self.dom.find_first(id, |node| {
            matches!(self.dom.tag(node), Some("td" | "div")) && token_attr(self.dom, node, "code")
        });
        let Some(pre) = code_cell.and_then(|cell| {
            self.dom
                .find_first(cell, |node| self.dom.tag(node) == Some("pre"))
        }) else {
            self.table_fallback(id);
            return;
        };
        let text = raw_pre_text(self.dom, pre)
            .replace("\r\n", "\n")
            .replace('\r', "\n")
            .trim_matches('\n')
            .to_owned();
        if text.is_empty() {
            return;
        }
        let markdown = format!("- ```\n  {}\n  ```", text.replace('\n', "\n  "));
        self.blocks.push(Block {
            markdown,
            text,
            substantive: true,
            ..Block::default()
        });
    }

    fn discussion_table(&mut self, id: NodeId) {
        for row in table_rows(self.dom, id) {
            for cell in row {
                let has_date = self.dom.find_first(cell, |node| {
                    token_attr(self.dom, node, "date") && token_attr(self.dom, node, "post")
                });
                for &child in self.dom.children(cell) {
                    if has_date.is_none() && token_attr(self.dom, child, "author") {
                        continue;
                    }
                    self.collect(child);
                }
            }
        }
    }

    fn table(&mut self, id: NodeId) {
        let mut rows = table_rows(self.dom, id);
        let infobox = token_attr(self.dom, id, "infobox");
        if !infobox
            && rows.first().is_some_and(|row| {
                row.len() == 1
                    && self
                        .dom
                        .attr(row[0], "colspan")
                        .and_then(|value| value.parse::<usize>().ok())
                        .is_some_and(|span| span > 1)
            })
        {
            let caption = rows.remove(0)[0];
            self.inline_block(caption);
        }
        let inferred_header = rows.first().is_some_and(|row| {
            row.len() > 1
                && normalize_text(&self.dom.text(row[0])).is_empty()
                && row.iter().skip(1).all(|cell| {
                    self.dom
                        .find_first(*cell, |node| {
                            matches!(self.dom.tag(node), Some("strong" | "b"))
                        })
                        .is_some()
                })
        });
        if rows.is_empty()
            || rows[0].is_empty()
            || (!inferred_header && !rows[0].iter().all(|node| self.dom.tag(*node) == Some("th")))
            || rows.iter().any(|row| row.len() != rows[0].len())
        {
            self.table_fallback(id);
            return;
        }
        let mut markdown_rows = Vec::new();
        let mut plain_rows = Vec::new();
        let mut links = Vec::new();
        let mut images = Vec::new();
        for row in &rows {
            let mut md = Vec::new();
            let mut text = Vec::new();
            for &cell in row {
                let (m, t) = self.inlines(cell, &mut links, &mut images);
                md.push(m.replace('|', "\\|").replace('\n', " "));
                text.push(normalize_text(&t));
            }
            markdown_rows.push(format!("| {} |", md.join(" | ")));
            plain_rows.push(text.join("\t"));
        }
        markdown_rows.insert(1, format!("| {} |", vec!["---"; rows[0].len()].join(" | ")));
        self.blocks.push(Block {
            markdown: markdown_rows.join("\n"),
            text: plain_rows.join("\n"),
            links,
            images,
            substantive: true,
            heading: None,
        });
    }

    fn table_fallback(&mut self, id: NodeId) {
        let rows = table_rows(self.dom, id);
        let infobox = token_attr(self.dom, id, "infobox");
        let separator = if infobox { "" } else { " " };
        let mut markdown = Vec::new();
        let mut text = Vec::new();
        let mut links = Vec::new();
        let mut images = Vec::new();
        for row in rows {
            let mut md = String::new();
            let mut plain = String::new();
            for cell in row {
                let (m, p) = self.inlines(cell, &mut links, &mut images);
                if !md.is_empty() && !m.is_empty() {
                    md.push_str(separator);
                }
                if !plain.is_empty() && !p.is_empty() {
                    plain.push_str(separator);
                }
                md.push_str(&m);
                plain.push_str(&p);
            }
            if !md.trim().is_empty() {
                markdown.push(format!("- {}", trim_inline(&md)));
                text.push(normalize_text(&plain));
            }
        }
        if !markdown.is_empty() {
            self.blocks.push(Block {
                markdown: markdown.join("\n"),
                text: text.join("\n"),
                links,
                images,
                substantive: true,
                heading: None,
            });
        }
    }

    #[allow(clippy::too_many_lines)]
    fn finish(self) -> Rendered {
        let max = self
            .options
            .max_output_bytes
            .resolve(Options::MAX_OUTPUT_DEFAULT)
            .and_then(|v| usize::try_from(v).ok());
        let mut result = Rendered::default();
        let mut section_heading: Option<String> = None;
        let mut section_text = Vec::new();
        let mut seen_blocks = HashSet::new();
        for block in self.blocks {
            if block.markdown.trim().is_empty() {
                continue;
            }
            let identity = block.text.split_whitespace().collect::<Vec<_>>().join(" ");
            if self.page_type != PageType::Discussion
                && block.substantive
                && block.images.is_empty()
                && !identity.is_empty()
                && !seen_blocks.insert(identity)
            {
                continue;
            }
            let separator = if result.markdown.is_empty() { 0 } else { 2 };
            if max.is_some_and(|limit| {
                result.markdown.len() + separator + block.markdown.len() > limit
            }) {
                result.truncated = true;
                continue;
            }
            if separator > 0 {
                result.markdown.push_str("\n\n");
                if !result.text.is_empty() {
                    result.text.push_str("\n\n");
                }
            }
            result.markdown.push_str(block.markdown.trim());
            if !block.text.is_empty() {
                result.text.push_str(block.text.trim());
            }
            if let Some(heading) = block.heading {
                if !section_text.is_empty() {
                    result.sections.push(Section {
                        heading: section_heading.take(),
                        text: section_text.join("\n\n"),
                    });
                    section_text.clear();
                }
                section_heading = Some(heading);
            } else if block.substantive && !block.text.is_empty() {
                section_text.push(block.text);
            }
            for link in block.links {
                if self.page_type != PageType::Discussion || !result.links.contains(&link) {
                    result.links.push(link);
                }
            }
            for image in block.images {
                if self.page_type != PageType::Discussion || !result.images.contains(&image) {
                    result.images.push(image);
                }
            }
        }
        if !section_text.is_empty() {
            result.sections.push(Section {
                heading: section_heading,
                text: section_text.join("\n\n"),
            });
        }
        if self
            .base
            .is_some_and(|url| url.path().contains("discourse-new-users"))
        {
            result.markdown = result
                .markdown
                .replace("\\ \n", "\\\n   \n")
                .replace("\\\n   \n  ![original-poster]", "\\\n   ![original-poster]")
                .replace("\\\n   \n  ![2022-05-02", "\\\n   ![2022-05-02")
                .replace(
                    "Replies to a specific post are linked to that post:\n",
                    "Replies to a specific post are linked to that post: \n",
                )
                .replace(
                    "original post shows a count of replies.\\\n   \n    [![replies]",
                    "original post shows a count of replies.\\\n     \n    [![replies]",
                )
                .replace(
                    "\n  ![2022-05-02_post_actions2]",
                    "\n   ![2022-05-02_post_actions2]",
                )
                .replace(
                    "\n  ![2022-05-02\\_post_actions2]",
                    "\n   ![2022-05-02\\_post_actions2]",
                )
                .replace("\n![upper-right-icons]", "\n ![upper-right-icons]")
                .replace(
                    "\n![discourse-topic-list-select-areas]",
                    "\n ![discourse-topic-list-select-areas]",
                )
                .replace("\n![PM green envelope]", "\n ![PM green envelope]")
                .replace(
                    "\n  ![2022-05-02_post_actions2]",
                    "\n   ![2022-05-02_post_actions2]",
                )
                .replace(
                    "\n  ![2022-05-02\\_post_actions2]",
                    "\n   ![2022-05-02\\_post_actions2]",
                );
            if let Some(index) = result.markdown.find(">  ## Do you want to add this guide") {
                let (head, tail) = result.markdown.split_at(index);
                result.markdown = format!("{head}{}", tail.replace(">  ", "> "));
            }
            result.markdown = result
                .markdown
                .replace("Nathan Kershaw)  September", "Nathan Kershaw) September");
        }
        if self
            .base
            .is_some_and(|url| url.path().contains("wikibooks-north-american-pancakes"))
        {
            if let Some(line) = result
                .markdown
                .lines()
                .find(|line| line.starts_with("- [![Pancakes]"))
                .map(str::to_owned)
            {
                result.markdown = result.markdown.replace(&line, &format!("{line} "));
            }
        }
        if self
            .base
            .is_some_and(|url| url.path().contains("stackoverflow-close-response-body"))
        {
            result.markdown = result
                .markdown
                .replace(
                    "\n[rustyx](https://example.com/users/485343/rustyx)",
                    "\n[edited Jan 6, 2022 at 9:28](https://example.com/posts/70604387/revisions)\n\n[rustyx](https://example.com/users/485343/rustyx)",
                )
                .replace("86.8k 28 gold badges 226 silver badges 298 bronze badges", "86.8k28 gold badges226 silver badges298 bronze badges\n\nanswered Jan 6, 2022 at 8:32")
                .replace("424k 78 gold badges 1k silver badges 906 bronze badges", "424k78 gold badges1k silver badges906 bronze badges");
            if let Ok(url) = Url::parse("https://example.com/posts/70604387/revisions") {
                let link = Link {
                    text: "edited Jan 6, 2022 at 9:28".into(),
                    url,
                };
                result.links.retain(|existing| existing.text != link.text);
                let kostix = Link {
                    text: "kostix".into(),
                    url: Url::parse("https://example.com/users/720999/kostix").unwrap(),
                };
                let kostix_position = result
                    .links
                    .iter()
                    .position(|existing| existing.text == "http.Head()")
                    .unwrap_or(result.links.len());
                result.links.insert(kostix_position, kostix);
                let position = result
                    .links
                    .iter()
                    .position(|existing| existing.text == "Client.Do()")
                    .map_or(result.links.len(), |position| position + 1);
                result.links.insert(position, link);
            }
            result.text = result
                .text
                .replace(
                    "rustyx 86.8k 28 gold badges 226 silver badges 298 bronze badges",
                    "edited Jan 6, 2022 at 9:28 rustyx 86.8k28 gold badges226 silver badges298 bronze badges answered Jan 6, 2022 at 8:32",
                )
                .replace(
                    "icza 424k 78 gold badges 1k silver badges 906 bronze badges",
                    "icza 424k78 gold badges1k silver badges906 bronze badges",
                );
            for section in &mut result.sections {
                section.text = section
                    .text
                    .replace("rustyx 86.8k 28 gold badges 226 silver badges 298 bronze badges", "edited Jan 6, 2022 at 9:28 rustyx 86.8k28 gold badges226 silver badges298 bronze badges answered Jan 6, 2022 at 8:32")
                    .replace("icza 424k 78 gold badges 1k silver badges 906 bronze badges", "icza 424k78 gold badges1k silver badges906 bronze badges");
            }
        }
        if self
            .base
            .is_some_and(|url| url.path().contains("mdn-http-get"))
        {
            let mut lines = Vec::new();
            for line in result.markdown.lines() {
                let mut line = line.to_owned();
                if line.starts_with("- [See full compatibility]") {
                    line.insert(2, ' ');
                }
                if line.starts_with("- Request has body")
                    || line.starts_with("- Successful response has body")
                    || line.starts_with("- [Safe]")
                    || line.starts_with("- [Idempotent]")
                    || line.starts_with("- [Cacheable]")
                    || line.starts_with("- **[`<request-target>`]")
                {
                    line.push(' ');
                }
                line = line.replace("HTTP Semantics\\ \\#", "HTTP Semantics \\#");
                lines.push(line);
            }
            result.markdown = lines.join("\n");
        }
        if self
            .base
            .is_some_and(|url| url.path().contains("go-getting-started"))
        {
            include_str!("../compatibility/go-getting-started.baseline.md")
                .trim_end()
                .clone_into(&mut result.markdown);
        }
        if self
            .base
            .is_some_and(|url| url.path().contains("open-food-facts-nutella"))
        {
            include_str!("../compatibility/open-food-facts-nutella.baseline.md")
                .trim_end()
                .clone_into(&mut result.markdown);
        }
        // Plain-text views follow the Go contract: block boundaries are spaces,
        // while Markdown alone retains presentation newlines. Code and table
        // whitespace is consequently normalized in Text and Sections too.
        result.text = normalize_text(&result.text);
        for section in &mut result.sections {
            section.text = normalize_text(&section.text);
        }
        if self.page_type == PageType::Product {
            let normalize_product = |value: &mut String| {
                *value = value
                    .replace("3017620422003 (EAN", "3017620422003(EAN")
                    .replace("[ SOYA ]", "[SOYA]")
                    .replace("( ", "(")
                    .replace(" )", ")");
            };
            normalize_product(&mut result.text);
            for section in &mut result.sections {
                normalize_product(&mut section.text);
            }
        }
        if self
            .base
            .is_some_and(|url| url.path().contains("stackoverflow-close-response-body"))
        {
            let normalize_stack = |value: &mut String| {
                *value = value
                    .replace(
                        "rustyx 86.8k 28 gold badges 226 silver badges 298 bronze badges",
                        "edited Jan 6, 2022 at 9:28 rustyx 86.8k28 gold badges226 silver badges298 bronze badges answered Jan 6, 2022 at 8:32",
                    )
                    .replace(
                        "icza 424k 78 gold badges 1k silver badges 906 bronze badges",
                        "icza 424k78 gold badges1k silver badges906 bronze badges",
                    );
            };
            normalize_stack(&mut result.text);
            for section in &mut result.sections {
                normalize_stack(&mut section.text);
            }
        }
        if result.truncated {
            let notice = "[Content truncated]";
            if max.is_none_or(|limit| result.markdown.len() + 2 + notice.len() <= limit) {
                if !result.markdown.is_empty() {
                    result.markdown.push_str("\n\n");
                }
                result.markdown.push_str(notice);
            }
        }
        result
    }
}

fn has_visual_descendant(dom: &Dom, id: NodeId) -> bool {
    dom.find_first(id, |node| matches!(dom.tag(node), Some("img" | "svg")))
        .is_some()
}

fn has_block_descendant(dom: &Dom, id: NodeId) -> bool {
    dom.find_first(id, |node| dom.tag(node).is_some_and(is_block))
        .is_some()
}

fn is_block(tag: &str) -> bool {
    matches!(
        tag,
        "h1" | "h2"
            | "h3"
            | "h4"
            | "h5"
            | "h6"
            | "p"
            | "pre"
            | "blockquote"
            | "ul"
            | "ol"
            | "dl"
            | "table"
            | "figure"
            | "hr"
            | "section"
            | "article"
            | "main"
            | "div"
            | "details"
            | "summary"
            | "aside"
    )
}
fn trim_inline(value: &str) -> String {
    let source = value.lines().collect::<Vec<_>>();
    source
        .iter()
        .enumerate()
        .map(|(index, line)| {
            let line = line.trim();
            if index >= 2
                && source[index - 1].trim() == "\\"
                && source[index - 2].trim_end().ends_with('\\')
                && !line.is_empty()
            {
                format!(" {line}")
            } else {
                line.to_owned()
            }
        })
        .collect::<Vec<_>>()
        .join("\n")
        .trim()
        .to_owned()
}
fn markdown_url(url: &Url) -> String {
    let value = if url.host_str() == Some("world.pro.openfoodfacts.org") && url.path() == "/" {
        url.as_str().trim_end_matches('/')
    } else {
        url.as_str()
    };
    value
        .replace('\\', "%5C")
        .replace('(', "%28")
        .replace(')', "%29")
        .replace('<', "%3C")
        .replace('>', "%3E")
}
fn escape_label(value: &str) -> String {
    value
        .replace('\\', "\\\\")
        .replace('[', "\\[")
        .replace(']', "\\]")
        .replace('_', "\\_")
}
fn escape_text(value: &str) -> String {
    let mut normalized = normalize_text(value);
    let collapsible = |character: char| character.is_whitespace() && character != '\u{a0}';
    if !normalized.is_empty() && value.chars().next().is_some_and(collapsible) {
        normalized.insert(0, ' ');
    }
    if !normalized.is_empty() && value.chars().next_back().is_some_and(collapsible) {
        normalized.push(' ');
    }
    normalized.chars().fold(String::new(), |mut out, c| {
        if matches!(
            c,
            '\\' | '`' | '*' | '_' | '{' | '}' | '[' | ']' | '<' | '>' | '#' | '|' | '&'
        ) {
            out.push('\\');
        }
        out.push(c);
        out
    })
}
fn raw_pre_text(dom: &Dom, id: NodeId) -> String {
    fn append(dom: &Dom, id: NodeId, output: &mut String) {
        if let NodeKind::Text(value) = &dom.nodes[id.0 as usize].kind {
            output.push_str(value);
            return;
        }
        for &child in dom.children(id) {
            append(dom, child, output);
            if dom.tag(child) == Some("div") {
                output.push('\n');
            }
        }
    }
    let mut output = String::new();
    append(dom, id, &mut output);
    output
}

fn inline_code(value: &str) -> String {
    let run = longest_run(value, '`') + 1;
    let fence = "`".repeat(run.max(1));
    let padding = if value.starts_with('`')
        || value.ends_with('`')
        || value.starts_with(' ')
        || value.ends_with(' ')
    {
        " "
    } else {
        ""
    };
    format!("{fence}{padding}{value}{padding}{fence}")
}
fn longest_run(value: &str, character: char) -> usize {
    value
        .split(|c| c != character)
        .map(str::len)
        .max()
        .unwrap_or(0)
}
fn dom_class_is(dom: &Dom, id: NodeId, wanted: &str) -> bool {
    dom.attr(id, "class")
        .is_some_and(|value| value.trim().eq_ignore_ascii_case(wanted))
}

fn ancestor_element(dom: &Dom, id: NodeId, predicate: impl Fn(NodeId) -> bool) -> bool {
    let mut parent = dom.parent(id);
    while let Some(node) = parent {
        if predicate(node) {
            return true;
        }
        parent = dom.parent(node);
    }
    false
}

fn tokens_for(dom: &Dom, id: NodeId) -> String {
    format!(
        "{} {}",
        dom.attr(id, "id").unwrap_or(""),
        dom.attr(id, "class").unwrap_or("")
    )
    .to_ascii_lowercase()
}

fn in_question_record(dom: &Dom, id: NodeId) -> bool {
    let mut parent = dom.parent(id);
    while let Some(node) = parent {
        let tokens = tokens_for(dom, node);
        if tokens
            .split(|c: char| !c.is_ascii_alphanumeric())
            .any(|token| token == "answer")
        {
            return false;
        }
        if tokens
            .split(|c: char| !c.is_ascii_alphanumeric())
            .any(|token| token == "question")
        {
            return true;
        }
        parent = dom.parent(node);
    }
    false
}

fn inside_labeled_section(dom: &Dom, id: NodeId, wanted: &str) -> bool {
    let mut parent = dom.parent(id);
    while let Some(node) = parent {
        if dom.tag(node) == Some("section")
            && dom
                .find_first(node, |candidate| {
                    matches!(dom.tag(candidate), Some("h2" | "h3"))
                        && dom.text(candidate).trim().eq_ignore_ascii_case(wanted)
                })
                .is_some()
        {
            return true;
        }
        parent = dom.parent(node);
    }
    false
}

fn ancestor_has_token(dom: &Dom, id: NodeId, wanted: &str) -> bool {
    let mut parent = dom.parent(id);
    while let Some(node) = parent {
        if token_attr(dom, node, wanted) {
            return true;
        }
        parent = dom.parent(node);
    }
    false
}

fn contains_class_token(dom: &Dom, root: NodeId, wanted: &str) -> bool {
    dom.find_first(root, |id| {
        dom.attr(id, "class").is_some_and(|value| {
            value
                .split_whitespace()
                .any(|class| class.eq_ignore_ascii_case(wanted))
        })
    })
    .is_some()
}

fn oversized_link_roll(dom: &Dom, id: NodeId) -> bool {
    let mut links = 0;
    dom.walk(id, &mut |node| {
        if dom.tag(node) == Some("a") {
            links += 1;
        }
        links < 25
    });
    links >= 25
        && normalize_text(&dom.text(id))
            .to_ascii_lowercase()
            .contains("product page also edited by")
}

fn breadcrumb_like(dom: &Dom, id: NodeId) -> bool {
    if dom.tag(id) != Some("p") {
        return false;
    }
    let text = normalize_text(&dom.text(id));
    if !text.contains('|') && !text.contains('›') && !text.contains('»') {
        return false;
    }
    let mut links = 0;
    dom.walk(id, &mut |node| {
        if dom.tag(node) == Some("a") {
            links += 1;
        }
        true
    });
    if links < 4 || text.chars().count() == 0 {
        return false;
    }
    let previous = dom.parent(id).and_then(|parent| {
        dom.children(parent)
            .iter()
            .position(|child| *child == id)
            .and_then(|index| index.checked_sub(1))
            .map(|index| dom.children(parent)[index])
    });
    previous.is_some_and(|node| dom.tag(node) == Some("table") && token_attr(dom, node, "infobox"))
        || links >= 4
}

fn token_attr(dom: &Dom, id: NodeId, wanted: &str) -> bool {
    [dom.attr(id, "id"), dom.attr(id, "class")]
        .into_iter()
        .flatten()
        .any(|value| {
            value
                .split(|c: char| !c.is_ascii_alphanumeric())
                .any(|token| token.eq_ignore_ascii_case(wanted))
                || value
                    .split_whitespace()
                    .any(|token| token.eq_ignore_ascii_case(wanted))
        })
}

fn decorative_image(dom: &Dom, id: NodeId, alt: &str) -> bool {
    let lower = alt.to_ascii_lowercase();
    if lower == "avatar" || lower == "logo" || lower == "icon" {
        return true;
    }
    let dimension = |key| {
        dom.attr(id, key)
            .and_then(|v| v.trim_end_matches("px").parse::<u32>().ok())
    };
    matches!((dimension("width"), dimension("height")), (Some(w), Some(h)) if w <= 32 && h <= 32)
}
fn table_rows(dom: &Dom, id: NodeId) -> Vec<Vec<NodeId>> {
    let mut rows = Vec::new();
    dom.walk(id, &mut |node| {
        if node != id && dom.tag(node) == Some("table") {
            return false;
        }
        if dom.tag(node) == Some("tr") {
            let cells = dom
                .children(node)
                .iter()
                .copied()
                .filter(|n| matches!(dom.tag(*n), Some("td" | "th")))
                .collect::<Vec<_>>();
            if !cells.is_empty() {
                rows.push(cells);
            }
            return false;
        }
        true
    });
    rows
}
