package engine

import (
	"strings"

	"golang.org/x/net/html"
)

// evidenceIndex contains facts that depend only on the input DOM. Later passes
// can read the index, but they do not add page policy or classification results
// to it. This keeps immutable evidence separate from the memoized decisions in
// nodeState.
type evidenceIndex struct {
	nodes                         map[*html.Node]nodeEvidence
	elements, textBytes, maxDepth int
	sections                      int
}

// nodeEvidence contains subtree facts used by more than one extraction pass.
// Shape flags describe proper descendants. Form flags and controls include the
// indexed element itself.
type nodeEvidence struct {
	flags, controls uint16
}

const (
	evidenceHidden uint16 = 1 << iota
	evidenceDiscussionBodyDescendant
	evidenceSubscriptionForm
	evidenceEmailInput
	evidenceForm
	evidenceArticleBodyDescendant
	evidenceBlockDescendant
)

type subtreeEvidence struct {
	flags, controls uint16
}

const maxEvidenceCount = ^uint16(0)

// buildEvidence indexes resources, visibility, and reusable subtree shape in
// one post-order traversal. It traverses hidden branches for resource limits,
// but hidden branches do not contribute evidence to their ancestors.
func buildEvidence(root *html.Node, o options) (*evidenceIndex, error) {
	index := &evidenceIndex{nodes: make(map[*html.Node]nodeEvidence)}
	if _, err := index.visit(root, 0, o); err != nil {
		return nil, err
	}
	return index, nil
}

func (e *evidenceIndex) visit(n *html.Node, depth int, o options) (subtreeEvidence, error) {
	if depth > e.maxDepth {
		e.maxDepth = depth
	}
	if o.maxDepth > 0 && depth > o.maxDepth {
		return subtreeEvidence{}, &LimitError{Resource: LimitDepth, Count: int64(depth), Max: int64(o.maxDepth)}
	}
	if n.Type == html.ElementNode {
		e.elements++
		if o.maxElements > 0 && e.elements > o.maxElements {
			return subtreeEvidence{}, &LimitError{Resource: LimitElements, Count: int64(e.elements), Max: int64(o.maxElements)}
		}
		if strings.EqualFold(n.Data, "section") {
			e.sections++
		}
	}
	if n.Type == html.TextNode {
		e.textBytes += len(n.Data)
	}

	hidden := n.Type == html.ElementNode && hardHidden(n)
	var descendants subtreeEvidence
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		childEvidence, err := e.visit(child, depth+1, o)
		if err != nil {
			return subtreeEvidence{}, err
		}
		if !hidden {
			descendants.flags |= childEvidence.flags
			if int(descendants.controls)+int(childEvidence.controls) > int(maxEvidenceCount) {
				descendants.controls = maxEvidenceCount
			} else {
				descendants.controls += childEvidence.controls
			}
		}
	}
	if n.Type != html.ElementNode {
		return descendants, nil
	}

	if hidden {
		e.nodes[n] = nodeEvidence{flags: evidenceHidden}
		return subtreeEvidence{}, nil
	}

	result := descendants
	tag := strings.ToLower(n.Data)
	switch tag {
	case "button", "input", "select", "textarea":
		if result.controls != maxEvidenceCount {
			result.controls++
		}
	}
	if tag == "form" {
		result.flags |= evidenceForm
		if subscriptionAttributeMarker(n) || containsSubscriptionWord(attrValue(n, "action")) {
			result.flags |= evidenceSubscriptionForm
		}
	}
	if tag == "input" && strings.EqualFold(strings.TrimSpace(attrValue(n, "type")), "email") {
		result.flags |= evidenceEmailInput
	}
	if isDiscussionBodyContainer(n) {
		result.flags |= evidenceDiscussionBodyDescendant
	}
	semanticArticle := tag == "article" && !elementContainsAny(n, "card", "comment", "reply")
	if semanticArticle || isConventionalArticleBody(n) {
		result.flags |= evidenceArticleBodyDescendant
	}
	if isBlockTag(tag) {
		result.flags |= evidenceBlockDescendant
	}
	facts := nodeEvidence{flags: result.flags, controls: result.controls}
	// Shape flags describe proper descendants. The result returned to the parent
	// also contains facts contributed by this element.
	const descendantShape = evidenceDiscussionBodyDescendant | evidenceArticleBodyDescendant | evidenceBlockDescendant
	facts.flags = facts.flags&^descendantShape | descendants.flags&descendantShape
	e.nodes[n] = facts
	return result, nil
}

func (e *evidenceIndex) has(n *html.Node, flag uint16) bool {
	if e == nil || n == nil {
		return false
	}
	return e.nodes[n].flags&flag != 0
}

func (e *evidenceIndex) controlCount(n *html.Node) (int, bool) {
	if e == nil || n == nil || n.Type != html.ElementNode {
		return 0, false
	}
	facts, ok := e.nodes[n]
	return int(facts.controls), ok
}
