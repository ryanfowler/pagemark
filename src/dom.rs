use std::borrow::Cow;

use html5ever::{
    interface::{Attribute, ElementFlags, NodeOrText, QuirksMode, TreeSink},
    parse_document,
    tendril::{StrTendril, TendrilSink},
    tree_builder::TreeBuilderOpts,
    ExpandedName, ParseOpts, QualName,
};

use crate::{Error, LimitResource};

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
}

#[derive(Debug)]
pub(crate) struct Dom {
    pub(crate) nodes: Vec<Node>,
    document: NodeId,
    parse_errors: Vec<Cow<'static, str>>,
}

impl Dom {
    fn new() -> Self {
        Self {
            nodes: vec![Node {
                parent: None,
                children: Vec::new(),
                kind: NodeKind::Document,
            }],
            document: NodeId(0),
            parse_errors: Vec::new(),
        }
    }

    fn add(&mut self, kind: NodeKind) -> NodeId {
        let id = NodeId(u32::try_from(self.nodes.len()).unwrap_or(u32::MAX));
        self.nodes.push(Node {
            parent: None,
            children: Vec::new(),
            kind,
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

    pub(crate) fn text(&self, root: NodeId) -> String {
        let mut values = Vec::new();
        self.walk(root, &mut |id| {
            if let NodeKind::Text(value) = &self.nodes[id.0 as usize].kind {
                values.push(value.as_str());
            }
            true
        });
        normalize_text(&values.join(" "))
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
    let characters = value.chars().collect::<Vec<_>>();
    let mut output = String::with_capacity(value.len());
    let mut pending_space = false;
    for (index, &character) in characters.iter().enumerate() {
        let paired_nbsp = character == '\u{a0}'
            && (index > 0 && characters[index - 1] == '\u{a0}'
                || characters.get(index + 1) == Some(&'\u{a0}'));
        if character.is_whitespace() && !paired_nbsp {
            pending_space = !output.is_empty();
            continue;
        }
        if pending_space {
            output.push(' ');
            pending_space = false;
        }
        output.push(character);
    }
    output
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
