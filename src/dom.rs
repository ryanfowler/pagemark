use std::{
    borrow::Cow,
    sync::{
        atomic::{AtomicUsize, Ordering},
        OnceLock,
    },
};

use html5ever::{
    interface::{Attribute, ElementFlags, NodeOrText, QuirksMode, TreeSink},
    parse_document,
    tendril::{StrTendril, TendrilSink},
    tree_builder::TreeBuilderOpts,
    ExpandedName, ParseOpts, QualName,
};

use crate::{Error, LimitResource};

const MAX_TEXT_CACHE_BYTES: usize = 1024 * 1024;

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub(crate) struct NodeId(pub(crate) u32);

#[derive(Debug)]
pub(crate) enum NodeKind {
    Document,
    Doctype,
    Element {
        name: QualName,
        attrs: Vec<Attribute>,
    },
    Text(String),
    Comment,
    ProcessingInstruction,
}

#[derive(Debug)]
pub(crate) struct Node {
    pub(crate) parent: Option<NodeId>,
    pub(crate) children: Vec<NodeId>,
    pub(crate) kind: NodeKind,
    // Text is queried repeatedly by classification and rendering. Cache short
    // subtree values so those probes do not rebuild the same String.
    text_cache: OnceLock<String>,
}

#[derive(Debug)]
pub(crate) struct Dom {
    pub(crate) nodes: Vec<Node>,
    document: NodeId,
    parse_errors: Vec<Cow<'static, str>>,
    text_cache_bytes: AtomicUsize,
}

impl Dom {
    fn new() -> Self {
        Self {
            nodes: vec![Node {
                parent: None,
                children: Vec::new(),
                kind: NodeKind::Document,
                text_cache: OnceLock::new(),
            }],
            document: NodeId(0),
            parse_errors: Vec::new(),
            text_cache_bytes: AtomicUsize::new(0),
        }
    }

    fn add(&mut self, kind: NodeKind) -> NodeId {
        let id = NodeId(u32::try_from(self.nodes.len()).unwrap_or(u32::MAX));
        self.nodes.push(Node {
            parent: None,
            children: Vec::new(),
            kind,
            text_cache: OnceLock::new(),
        });
        id
    }

    fn detach(&mut self, child: NodeId) {
        if let Some(parent) = self.nodes[child.0 as usize].parent.take() {
            self.nodes[parent.0 as usize]
                .children
                .retain(|id| *id != child);
        }
    }

    fn append_node(&mut self, parent: NodeId, child: NodeId) {
        self.detach(child);
        self.nodes[child.0 as usize].parent = Some(parent);
        self.nodes[parent.0 as usize].children.push(child);
    }

    pub(crate) fn tag(&self, id: NodeId) -> Option<&str> {
        match &self.nodes[id.0 as usize].kind {
            NodeKind::Element { name, .. } => Some(name.local.as_ref()),
            _ => None,
        }
    }

    pub(crate) fn attr(&self, id: NodeId, key: &str) -> Option<&str> {
        match &self.nodes[id.0 as usize].kind {
            NodeKind::Element { attrs, .. } => attrs
                .iter()
                .find(|a| a.name.local.as_ref().eq_ignore_ascii_case(key))
                .map(|a| a.value.as_ref()),
            _ => None,
        }
    }

    pub(crate) fn has_attr(&self, id: NodeId, key: &str) -> bool {
        self.attr(id, key).is_some()
    }

    pub(crate) fn children(&self, id: NodeId) -> &[NodeId] {
        &self.nodes[id.0 as usize].children
    }

    pub(crate) fn parent(&self, id: NodeId) -> Option<NodeId> {
        self.nodes[id.0 as usize].parent
    }

    pub(crate) fn text(&self, root: NodeId) -> Cow<'_, str> {
        if let Some(value) = self.nodes[root.0 as usize].text_cache.get() {
            return Cow::Borrowed(value);
        }
        let value = self.compute_text(root);
        // Large subtree strings have a high memory cost and are usually queried
        // only once. Keep the cache bounded while retaining the common short
        // paragraph/label fast path.
        if value.len() <= 4096 {
            let cache = &self.nodes[root.0 as usize].text_cache;
            let cost = value.capacity();
            let mut used = self.text_cache_bytes.load(Ordering::Relaxed);
            loop {
                let Some(next) = used.checked_add(cost) else {
                    return Cow::Owned(value);
                };
                if next > MAX_TEXT_CACHE_BYTES {
                    return Cow::Owned(value);
                }
                match self.text_cache_bytes.compare_exchange_weak(
                    used,
                    next,
                    Ordering::Relaxed,
                    Ordering::Relaxed,
                ) {
                    Ok(_) => break,
                    Err(current) => used = current,
                }
            }
            if cache.set(value).is_err() {
                self.text_cache_bytes.fetch_sub(cost, Ordering::Relaxed);
            }
            return Cow::Borrowed(cache.get().expect("text cache was just initialized"));
        }
        Cow::Owned(value)
    }

    fn compute_text(&self, root: NodeId) -> String {
        let mut normalizer = TextNormalizer::new(64);
        let mut first = true;
        self.walk(root, &mut |id| {
            if let NodeKind::Text(text) = &self.nodes[id.0 as usize].kind {
                if !first {
                    normalizer.push(' ');
                }
                first = false;
                normalizer.push_str(text);
            }
            true
        });
        normalizer.finish()
    }

    pub(crate) fn raw_text(&self, root: NodeId) -> String {
        let mut value = String::new();
        self.walk(root, &mut |id| {
            if let NodeKind::Text(text) = &self.nodes[id.0 as usize].kind {
                value.push_str(text);
            }
            true
        });
        value
    }

    pub(crate) fn walk(&self, root: NodeId, visit: &mut impl FnMut(NodeId) -> bool) {
        if !visit(root) {
            return;
        }
        for &child in self.children(root) {
            self.walk(child, visit);
        }
    }

    pub(crate) fn find_first(
        &self,
        root: NodeId,
        mut predicate: impl FnMut(NodeId) -> bool,
    ) -> Option<NodeId> {
        fn visit(
            dom: &Dom,
            id: NodeId,
            predicate: &mut impl FnMut(NodeId) -> bool,
        ) -> Option<NodeId> {
            if predicate(id) {
                return Some(id);
            }
            for &child in dom.children(id) {
                if let Some(found) = visit(dom, child, predicate) {
                    return Some(found);
                }
            }
            None
        }
        visit(self, root, &mut predicate)
    }

    pub(crate) fn document(&self) -> NodeId {
        self.document
    }
}

impl TreeSink for Dom {
    type Handle = NodeId;
    type Output = Self;

    fn finish(self) -> Self::Output {
        self
    }
    fn parse_error(&mut self, msg: Cow<'static, str>) {
        self.parse_errors.push(msg);
    }
    fn get_document(&mut self) -> Self::Handle {
        self.document
    }
    fn elem_name<'a>(&'a self, target: &'a Self::Handle) -> ExpandedName<'a> {
        match &self.nodes[target.0 as usize].kind {
            NodeKind::Element { name, .. } => name.expanded(),
            _ => panic!("elem_name called for non-element"),
        }
    }
    fn create_element(
        &mut self,
        name: QualName,
        attrs: Vec<Attribute>,
        _: ElementFlags,
    ) -> Self::Handle {
        self.add(NodeKind::Element { name, attrs })
    }
    fn create_comment(&mut self, _: StrTendril) -> Self::Handle {
        self.add(NodeKind::Comment)
    }
    fn create_pi(&mut self, _: StrTendril, _: StrTendril) -> Self::Handle {
        self.add(NodeKind::ProcessingInstruction)
    }
    fn append(&mut self, parent: &Self::Handle, child: NodeOrText<Self::Handle>) {
        match child {
            NodeOrText::AppendNode(node) => self.append_node(*parent, node),
            NodeOrText::AppendText(text) => {
                if let Some(last) = self.nodes[parent.0 as usize].children.last().copied() {
                    if let NodeKind::Text(value) = &mut self.nodes[last.0 as usize].kind {
                        value.push_str(&text);
                        return;
                    }
                }
                let node = self.add(NodeKind::Text(text.to_string()));
                self.append_node(*parent, node);
            }
        }
    }
    fn append_before_sibling(&mut self, sibling: &Self::Handle, child: NodeOrText<Self::Handle>) {
        let Some(parent) = self.parent(*sibling) else {
            return;
        };
        let index = self.nodes[parent.0 as usize]
            .children
            .iter()
            .position(|id| id == sibling)
            .unwrap_or(0);
        let node = match child {
            NodeOrText::AppendNode(node) => node,
            NodeOrText::AppendText(text) => self.add(NodeKind::Text(text.to_string())),
        };
        self.detach(node);
        self.nodes[node.0 as usize].parent = Some(parent);
        self.nodes[parent.0 as usize].children.insert(index, node);
    }
    fn append_based_on_parent_node(
        &mut self,
        element: &Self::Handle,
        prev: &Self::Handle,
        child: NodeOrText<Self::Handle>,
    ) {
        if self.parent(*element).is_some() {
            self.append_before_sibling(element, child);
        } else {
            self.append(prev, child);
        }
    }
    fn append_doctype_to_document(&mut self, _: StrTendril, _: StrTendril, _: StrTendril) {
        let node = self.add(NodeKind::Doctype);
        self.append_node(self.document, node);
    }
    fn get_template_contents(&mut self, target: &Self::Handle) -> Self::Handle {
        *target
    }
    fn same_node(&self, x: &Self::Handle, y: &Self::Handle) -> bool {
        x == y
    }
    fn set_quirks_mode(&mut self, _: QuirksMode) {}
    fn add_attrs_if_missing(&mut self, target: &Self::Handle, attrs: Vec<Attribute>) {
        if let NodeKind::Element {
            attrs: existing, ..
        } = &mut self.nodes[target.0 as usize].kind
        {
            for attr in attrs {
                if !existing.iter().any(|a| a.name == attr.name) {
                    existing.push(attr);
                }
            }
        }
    }
    fn remove_from_parent(&mut self, target: &Self::Handle) {
        self.detach(*target);
    }
    fn reparent_children(&mut self, node: &Self::Handle, new_parent: &Self::Handle) {
        let children = std::mem::take(&mut self.nodes[node.0 as usize].children);
        for child in children {
            self.nodes[child.0 as usize].parent = Some(*new_parent);
            self.nodes[new_parent.0 as usize].children.push(child);
        }
    }
    fn mark_script_already_started(&mut self, _: &Self::Handle) {}
}

pub(crate) fn parse(html: &str) -> Result<Dom, Error> {
    parse_with_scripting(html, true)
}

pub(crate) fn parse_with_scripting(html: &str, scripting_enabled: bool) -> Result<Dom, Error> {
    let options = ParseOpts {
        tree_builder: TreeBuilderOpts {
            scripting_enabled,
            ..TreeBuilderOpts::default()
        },
        ..ParseOpts::default()
    };
    let dom = parse_document(Dom::new(), options).one(html);
    enforce_bounds(&dom)?;
    Ok(dom)
}

fn enforce_bounds(dom: &Dom) -> Result<(), Error> {
    const MAX_ELEMENTS: u64 = 200_000;
    const MAX_DEPTH: u64 = 256;
    let elements = dom
        .nodes
        .iter()
        .filter(|n| matches!(n.kind, NodeKind::Element { .. }))
        .count() as u64;
    if elements > MAX_ELEMENTS {
        return Err(Error::Limit {
            resource: LimitResource::Elements,
            count: elements,
            max: MAX_ELEMENTS,
        });
    }
    // Use an explicit stack: this validation runs before the depth invariant has
    // been established and therefore must not recurse over hostile nesting.
    let mut stack = vec![(dom.document, 0_u64)];
    let mut maximum = 0;
    while let Some((node, depth)) = stack.pop() {
        maximum = maximum.max(depth);
        if maximum > MAX_DEPTH {
            return Err(Error::Limit {
                resource: LimitResource::Depth,
                count: maximum,
                max: MAX_DEPTH,
            });
        }
        stack.extend(dom.children(node).iter().map(|child| (*child, depth + 1)));
    }
    Ok(())
}

pub(crate) fn normalize_text(value: &str) -> String {
    let mut normalizer = TextNormalizer::new(value.len());
    normalizer.push_str(value);
    normalizer.finish()
}

struct TextNormalizer {
    output: String,
    pending_space: bool,
    previous: Option<char>,
}

impl TextNormalizer {
    fn new(capacity: usize) -> Self {
        Self {
            output: String::with_capacity(capacity),
            pending_space: false,
            previous: None,
        }
    }

    fn push(&mut self, character: char) {
        let paired_nbsp = character == '\u{a0}' && self.previous == Some('\u{a0}');
        if character.is_whitespace() && !paired_nbsp {
            self.pending_space = !self.output.is_empty();
            self.previous = Some(character);
            return;
        }
        if self.pending_space {
            self.output.push(' ');
            self.pending_space = false;
        }
        self.output.push(character);
        self.previous = Some(character);
    }

    fn push_str(&mut self, value: &str) {
        let mut characters = value.chars();
        while let Some(character) = characters.next() {
            let paired_nbsp = character == '\u{a0}'
                && (self.previous == Some('\u{a0}') || characters.clone().next() == Some('\u{a0}'));
            if character.is_whitespace() && !paired_nbsp {
                self.pending_space = !self.output.is_empty();
                self.previous = Some(character);
                continue;
            }
            if self.pending_space {
                self.output.push(' ');
                self.pending_space = false;
            }
            self.output.push(character);
            self.previous = Some(character);
        }
    }

    fn finish(self) -> String {
        self.output
    }
}

pub(crate) fn hidden(dom: &Dom, id: NodeId) -> bool {
    let Some(tag) = dom.tag(id) else { return false };
    if matches!(
        tag,
        "script" | "style" | "template" | "canvas" | "svg" | "iframe" | "object" | "embed"
    ) {
        return true;
    }
    if dom.has_attr(id, "hidden")
        || dom.has_attr(id, "inert")
        || dom
            .attr(id, "aria-hidden")
            .is_some_and(|v| v.trim().eq_ignore_ascii_case("true"))
        || dom
            .attr(id, "aria-modal")
            .is_some_and(|v| v.trim().eq_ignore_ascii_case("true"))
    {
        return true;
    }
    if tag == "dialog" && !dom.has_attr(id, "open") {
        return true;
    }
    if dom.attr(id, "class").is_some_and(|v| {
        v.split_whitespace()
            .any(|c| c.eq_ignore_ascii_case("hidden"))
            && !v
                .split_whitespace()
                .any(|c| c.contains(':') && !c.ends_with(":hidden"))
    }) {
        return true;
    }
    dom.attr(id, "style").is_some_and(|style| {
        style.split(';').any(|decl| {
            let Some((name, value)) = decl.split_once(':') else {
                return false;
            };
            (name.trim().eq_ignore_ascii_case("display")
                && value
                    .trim()
                    .trim_end_matches("!important")
                    .trim()
                    .eq_ignore_ascii_case("none"))
                || (name.trim().eq_ignore_ascii_case("visibility")
                    && value
                        .trim()
                        .trim_end_matches("!important")
                        .trim()
                        .eq_ignore_ascii_case("hidden"))
        })
    })
}
