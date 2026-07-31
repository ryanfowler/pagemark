package engine

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

func (a *analysis) hasArticleBodyDescendant(root *html.Node) bool {
	if root == nil || hardHidden(root) {
		return false
	}
	if a.evidence != nil {
		if _, indexed := a.evidence.nodes[root]; indexed {
			return a.evidence.has(root, evidenceArticleBodyDescendant)
		}
	}
	found := false
	for child := root.FirstChild; child != nil && !found; child = child.NextSibling {
		walk(child, func(current *html.Node) bool {
			if hardHidden(current) {
				return false
			}
			if current.Type != html.ElementNode {
				return true
			}
			semanticArticle := strings.EqualFold(current.Data, "article") &&
				!elementContainsAny(current, "card", "comment", "reply")
			found = semanticArticle || isConventionalArticleBody(current)
			return !found
		})
	}
	return found
}

// isTrailingArticleCardRegion catches unlabeled recommendation and newsletter
// grids after an article. Their summaries can contain enough prose to defeat
// ordinary boilerplate penalties. Requiring multiple explicitly marked cards
// and an earlier/containing semantic article avoids treating a single useful
// card or a listing page as auxiliary content.
func hasNonCardArticleAncestor(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, "article") && !elementContainsAny(p, "card") {
			return true
		}
	}
	return false
}

func (a *analysis) isTrailingArticleCardRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "div", "section", "aside", "ul":
	default:
		return false
	}
	if a.articleCardCount(n) < 2 {
		return false
	}
	// A layout wrapper can contain both the article body and a final card grid.
	// The cards are still classified when their narrower region is visited;
	// marking the shared wrapper would make every selected prose block vanish
	// through hasIrrelevantAncestor.
	if hasSubstantiveContentBeforeDescendant(n, isMarkedArticleCard) {
		return false
	}
	return hasSemanticArticleBeforeOrAround(n)
}

func isMarkedCard(n *html.Node) bool {
	return n != nil && n.Type == html.ElementNode && elementContainsAny(n, "card")
}

func isMarkedArticleCard(n *html.Node) bool {
	if !isMarkedCard(n) {
		return false
	}
	return strings.EqualFold(n.Data, "article") || elementContainsAny(n, "article", "post", "story", "newsletter")
}

// hasSubstantiveContentBeforeDescendant protects a shared ancestor from tail
// classification. The target must be a proper descendant, and prose must occur
// before it in document order; prose inside the promotional target therefore
// cannot protect the target itself.
func hasSubstantiveContentBeforeDescendant(root *html.Node, target func(*html.Node) bool) bool {
	if root == nil {
		return false
	}
	paragraphs, chars, longest := 0, 0, 0
	foundTarget := false
	walk(root, func(n *html.Node) bool {
		if foundTarget || hardHidden(n) {
			return false
		}
		if n != root && target(n) {
			foundTarget = true
			return false
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "p") {
			length := utf8.RuneCountInString(normalizeText(nodeText(n)))
			paragraphs++
			chars += length
			if length > longest {
				longest = length
			}
			return false
		}
		return true
	})
	return foundTarget && (longest >= 120 || (paragraphs >= 2 && chars >= 120))
}

func isPromotionalCardRegion(n *html.Node) bool {
	if elementContainsAny(n, "promo", "promotion", "promotions", "promotional", "related", "recommended", "recommendations") {
		return true
	}
	return isArticleAuxiliaryLabel(firstRegionHeading(n))
}

func countMarkedCards(root *html.Node, limit int) int {
	count := 0
	var visit func(*html.Node)
	visit = func(parent *html.Node) {
		for ch := parent.FirstChild; ch != nil && count < limit; ch = ch.NextSibling {
			if hardHidden(ch) || ch.Type != html.ElementNode {
				continue
			}
			if elementContainsAny(ch, "card") {
				count++
				continue
			}
			visit(ch)
		}
	}
	visit(root)
	return count
}

// articleCardCount returns the number of top-level marked article cards in a
// subtree, capped at two. Caching turns repeated candidate-region checks from
// overlapping subtree walks into one bottom-up pass.
func (a *analysis) articleCardCount(root *html.Node) int {
	if root == nil || hardHidden(root) {
		return 0
	}
	if value, known := a.nodeStates[root].articleCardCount.value(); known {
		return value
	}
	count := 0
	for ch := root.FirstChild; ch != nil && count < 2; ch = ch.NextSibling {
		if hardHidden(ch) || ch.Type != html.ElementNode {
			continue
		}
		if isMarkedArticleCard(ch) {
			count++
			continue // Do not count nested wrappers belonging to the same card.
		}
		count += a.articleCardCount(ch)
		if count > 2 {
			count = 2
		}
	}
	state := a.nodeStates[root]
	state.articleCardCount.store(count)
	a.nodeStates[root] = state
	return count
}

func hasSemanticArticleBeforeOrAround(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, "article") && !elementContainsAny(p, "card") {
			return true
		}
	}
	// At each ancestor level, previous siblings are entirely before n in
	// document order. Search them for the primary semantic article.
	for branch := n; branch != nil && branch.Parent != nil; branch = branch.Parent {
		for sibling := branch.PrevSibling; sibling != nil; sibling = sibling.PrevSibling {
			found := false
			walk(sibling, func(x *html.Node) bool {
				if found || hardHidden(x) {
					return false
				}
				if x.Type == html.ElementNode && strings.EqualFold(x.Data, "article") && !elementContainsAny(x, "card") {
					found = true
					return false
				}
				return true
			})
			if found {
				return true
			}
		}
	}
	return false
}
