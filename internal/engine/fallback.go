package engine

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

func (a *analysis) semanticFallback() []*html.Node {
	var main *html.Node
	walk(a.root, func(n *html.Node) bool {
		if main == nil && n.Type == html.ElementNode && (strings.EqualFold(n.Data, "main") || strings.EqualFold(attrValue(n, "role"), "main")) {
			main = n
		}
		return main == nil
	})
	if main == nil {
		return nil
	}
	return []*html.Node{main}
}

// semanticArticleFallback recovers short articles whose paragraphs are useful
// but score poorly because most of their text is linked. Restrict candidates to
// semantic articles supported by segmented, non-auxiliary content so cards,
// comments, and other article-shaped records do not become page content. Its
// quality uses the same eligible blocks, matching the text the converter sees.
func (a *analysis) semanticArticleFallback() (*html.Node, float64) {
	type candidate struct {
		n      *html.Node
		chars  int
		links  int
		blocks int
	}
	var candidates []candidate
	indexes := make(map[*html.Node]int)
	for i := range a.blocks {
		b := &a.blocks[i]
		article := a.primaryArticleAncestor(b.node)
		if article == nil || a.hasIrrelevantAncestor(b.node) {
			continue
		}
		// A nested article may represent an embedded post, comment, or related
		// record within the primary article. Group its blocks under the outermost
		// eligible article so a longer nested record cannot replace its container.
		for p := article.Parent; p != nil; p = p.Parent {
			if p.Type == html.ElementNode && strings.EqualFold(p.Data, "article") &&
				!elementContainsAny(p, "card") && !a.inferenceAuxiliaryBlock(p) {
				article = p
			}
		}
		index, ok := indexes[article]
		if !ok {
			index = len(candidates)
			indexes[article] = index
			candidates = append(candidates, candidate{n: article})
		}
		candidates[index].chars += b.textChars()
		candidates[index].links += b.linkChars()
		candidates[index].blocks++
	}
	var best candidate
	for _, candidate := range candidates {
		if candidate.chars > best.chars {
			best = candidate
		}
	}
	if best.chars <= 100 {
		return nil, 0
	}
	return best.n, qualityFromEvidence(best.chars, best.links, best.blocks)
}

// nodeSetBlockEvidence measures only eligible segmented blocks. In particular,
// auxiliary descendants inside a selected ancestor do not make a reconstructed
// region appear better than the content the converter will retain.
func (a *analysis) nodeSetBlockEvidence(nodes []*html.Node) (chars, links, blocks int) {
	if len(nodes) == 0 {
		return 0, 0, 0
	}
	// The selected set is often made of many sibling blocks. Scanning every
	// selected root for every block turns this accounting pass quadratic. Index
	// the roots once, then walk the (usually shallow) ancestry of each block.
	roots := make(map[*html.Node]struct{}, len(nodes))
	for _, root := range nodes {
		if root != nil {
			roots[root] = struct{}{}
		}
	}
	for i := range a.blocks {
		b := &a.blocks[i]
		inside := false
		for current := b.node; current != nil; current = current.Parent {
			if _, ok := roots[current]; ok {
				inside = true
				break
			}
		}
		if !inside || !a.plausibleRegionBlock(b) {
			continue
		}
		chars += b.textChars()
		links += b.linkChars()
		blocks++
	}
	return
}

func (a *analysis) plausibleRegionBlock(b *block) bool {
	if b == nil || a.hasIrrelevantAncestor(b.node) || b.controls() > 2 {
		return false
	}
	switch b.kind {
	case "p", "blockquote", "generic", "pre":
	default:
		return false
	}
	length := b.textChars()
	if length < 12 || b.linkChars()*2 >= max(1, length) ||
		isArticleAuxiliaryLabel(normalizedLabel(b.text)) {
		return false
	}
	for p := b.node; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if hardHidden(p) || isListingRecordElement(p) || a.repeatedUnmarkedListingRecord(p) ||
			elementContainsAny(p, "comment", "reply", "newsletter", "subscribe", "related", "recommended") {
			return false
		}
	}
	return true
}

func articleRegionContainer(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "article", "main", "section", "div":
		return true
	case "td", "th":
		return hasNonCardArticleAncestor(n)
	}
	return false
}

func (a *analysis) unsafeArticleRegion(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || hardHidden(n) || a.hasIrrelevantAncestor(n) ||
		a.inferenceAuxiliaryBlock(n) || isAdvertisementRegion(n) {
		return true
	}
	tag := strings.ToLower(n.Data)
	if tag == "nav" || tag == "header" || tag == "footer" || tag == "aside" {
		return true
	}
	return elementContainsAny(n, "comment", "reply", "discussion", "newsletter", "subscribe", "related", "recommended") ||
		isListingRecordElement(n) || a.repeatedUnmarkedListingRecord(n) || a.articleCardCount(n) >= 2
}

func regionRank(e *articleRegionEvidence) float64 {
	if e == nil || e.proseChars == 0 {
		return 0
	}
	rank := e.score + 1.15*float64(e.strongParagraphs) + math.Min(2.5, float64(e.proseChars)/260)
	rank -= 3 * float64(e.linkedChars) / float64(e.proseChars)
	if e.node != nil {
		tag := strings.ToLower(e.node.Data)
		if tag == "article" {
			rank += 2
		} else if tag == "main" || strings.EqualFold(attrValue(e.node, "role"), "main") {
			rank += 1.25
		}
	}
	return rank
}

// reconstructArticleRegion propagates prose evidence to a bounded set of
// ancestors, then optionally joins near-tied sibling regions. It returns source
// nodes only; no node is detached, cloned, or otherwise changed.
func (a *analysis) reconstructArticleRegion() []*html.Node {
	evidence := make(map[*html.Node]*articleRegionEvidence)
	for i := range a.blocks {
		b := &a.blocks[i]
		if !a.plausibleRegionBlock(b) {
			continue
		}
		length, linked := b.textChars(), b.linkChars()
		weight := 1.0
		depth := 0
		for p := b.node.Parent; p != nil && depth < 4; p = p.Parent {
			if p.Type != html.ElementNode {
				continue
			}
			tag := strings.ToLower(p.Data)
			if tag == "body" || tag == "html" || tag == "nav" || tag == "footer" || tag == "header" || tag == "aside" || a.unsafeArticleRegion(p) {
				break
			}
			if articleRegionContainer(p) {
				e := evidence[p]
				if e == nil {
					e = &articleRegionEvidence{node: p}
					evidence[p] = e
				}
				e.score += math.Max(.1, b.score) * weight
				e.proseChars += int(float64(length) * weight)
				e.linkedChars += int(float64(linked) * weight)
				if b.kind == "p" && length >= 60 {
					e.strongParagraphs++
				}
				if b.selected {
					e.selectedBlocks++
				}
			}
			depth++
			weight *= .5
		}
	}
	var candidates []*articleRegionEvidence
	for _, e := range evidence {
		if e.proseChars >= 40 && !a.unsafeArticleRegion(e.node) {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	// Record order only on candidate containers. A document-wide pointer map is
	// expensive on large pages and the rank tie-break needs only these ordinals.
	ordinal := 0
	walk(a.root, func(n *html.Node) bool {
		if n.Type == html.ElementNode {
			if e := evidence[n]; e != nil {
				e.documentOrder = ordinal
			}
		}
		ordinal++
		return true
	})
	sort.SliceStable(candidates, func(i, j int) bool {
		ri, rj := regionRank(candidates[i]), regionRank(candidates[j])
		if ri == rj {
			return candidates[i].documentOrder < candidates[j].documentOrder
		}
		return ri > rj
	})
	primary := candidates[0]
	root := primary.node
	bestRank := regionRank(primary)
	near := nonOverlappingNearCandidates(candidates, bestRank)
	if len(near) >= 3 {
		if common := nearestCommonArticleAncestor(near); common != nil && !a.unsafeArticleRegion(common) {
			prose := a.uniqueRegionProseChars(near)
			all := utf8.RuneCountInString(normalizeText(nodeText(common)))
			// Permit modest furniture (bylines and adverts), but not an entire page
			// shell or a card collection around the candidates. Count each eligible
			// block once: ancestor/descendant candidates share propagated evidence.
			if all <= prose*2+500 && a.articleCardCount(common) < 2 {
				root = common
			}
		}
	}

	primaryEvidence := evidence[root]
	if primaryEvidence == nil {
		primaryEvidence = primary
	}
	if root.Parent == nil {
		return []*html.Node{root}
	}

	// All additional roots are siblings of the primary root. Walking that sibling
	// list emits unique, non-overlapping roots in document order without sorting.
	primaryRank := regionRank(primaryEvidence)
	roots := make([]*html.Node, 0, 4)
	for sibling := root.Parent.FirstChild; sibling != nil; sibling = sibling.NextSibling {
		if sibling == root {
			roots = append(roots, root)
			continue
		}
		if sibling.Type != html.ElementNode || a.unsafeArticleRegion(sibling) {
			continue
		}
		if a.qualifyingArticleSibling(sibling, root, evidence[sibling], primaryRank) {
			roots = append(roots, sibling)
		}
	}
	return roots
}

// nonOverlappingNearCandidates keeps the highest-ranked candidate from each
// DOM branch. candidates is already rank ordered by reconstructArticleRegion;
// accepting an ancestor and its descendant as two regions would manufacture a
// near tie from the same prose.
func nonOverlappingNearCandidates(candidates []*articleRegionEvidence, bestRank float64) []*articleRegionEvidence {
	var near []*articleRegionEvidence
	accepted := make(map[*html.Node]bool, len(candidates))
	// acceptedBelow marks ancestors of accepted candidates. Together with the
	// upward walk below this detects overlap in either direction without comparing
	// every candidate against every accepted region.
	acceptedBelow := make(map[*html.Node]bool, len(candidates))
	for _, candidate := range candidates {
		if regionRank(candidate) < bestRank*.75 || candidate.node == nil {
			continue
		}
		overlaps := acceptedBelow[candidate.node]
		for p := candidate.node; p != nil && !overlaps; p = p.Parent {
			overlaps = accepted[p]
		}
		if overlaps {
			continue
		}
		near = append(near, candidate)
		accepted[candidate.node] = true
		for p := candidate.node.Parent; p != nil; p = p.Parent {
			acceptedBelow[p] = true
		}
	}
	return near
}

func (a *analysis) uniqueRegionProseChars(regions []*articleRegionEvidence) int {
	regionNodes := make(map[*html.Node]bool, len(regions))
	for _, region := range regions {
		if region != nil && region.node != nil {
			regionNodes[region.node] = true
		}
	}
	chars := 0
	for i := range a.blocks {
		b := &a.blocks[i]
		if !a.plausibleRegionBlock(b) {
			continue
		}
		for p := b.node; p != nil; p = p.Parent {
			if regionNodes[p] {
				chars += b.textChars()
				break
			}
		}
	}
	return chars
}

func nearestCommonArticleAncestor(candidates []*articleRegionEvidence) *html.Node {
	if len(candidates) == 0 {
		return nil
	}
	for p := candidates[0].node.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode || strings.EqualFold(p.Data, "body") || !articleRegionContainer(p) {
			continue
		}
		all := true
		for _, candidate := range candidates[1:] {
			if !nodeWithin(candidate.node, p) {
				all = false
				break
			}
		}
		if all {
			return p
		}
	}
	return nil
}

func (a *analysis) qualifyingArticleSibling(sibling, primary *html.Node, e *articleRegionEvidence, primaryRank float64) bool {
	text := normalizeText(nodeText(sibling))
	length, links := utf8.RuneCountInString(text), linkTextLength(sibling)
	if length == 0 || links*2 >= length || controls(sibling) > 2 {
		return false
	}
	if e != nil && regionRank(e) >= primaryRank*.25 {
		return true
	}
	if meaningfulSharedClass(primary, sibling) {
		return true
	}
	if strings.EqualFold(sibling.Data, "p") {
		return length > 80 || links == 0 && length >= 12 && strings.ContainsAny(text, ".!?;:")
	}
	heading, prose := false, 0
	for i := range a.blocks {
		b := &a.blocks[i]
		if !nodeWithin(b.node, sibling) {
			continue
		}
		if isHeadingTag(b.kind) {
			heading = true
		} else if a.plausibleRegionBlock(b) {
			prose += b.textChars()
		}
	}
	return heading && prose >= 80
}

func meaningfulSharedClass(aNode, bNode *html.Node) bool {
	generic := func(s string) bool {
		s = strings.ToLower(s)
		return s == "container" || s == "wrapper" || s == "content" || s == "section" || s == "row" || s == "column" || s == "block" ||
			containsAny(s, "card", "related", "recommended", "comment", "newsletter", "subscribe", "advert")
	}
	classes := make(map[string]bool)
	for class := range strings.FieldsSeq(attrValue(aNode, "class")) {
		if len(class) >= 4 && !generic(class) {
			classes[strings.ToLower(class)] = true
		}
	}
	for class := range strings.FieldsSeq(attrValue(bNode, "class")) {
		if classes[strings.ToLower(class)] && !generic(class) {
			return true
		}
	}
	return false
}

func (a *analysis) highRecall() []*html.Node {
	var out []*html.Node
	for i := range a.blocks {
		b := &a.blocks[i]
		heroHeader := recoverableHeaderProse(b)
		if heroHeader != nil {
			// A clean, non-interactive header can be a homepage hero rather than
			// a masthead. Reclassify that exact region so normal exclusion checks
			// remain authoritative for every other irrelevant ancestor.
			a.overrideIrrelevant(heroHeader, false)
		}
		bad := a.hasIrrelevantAncestor(b.node)
		for p := b.node; p != nil; p = p.Parent {
			if p.Type == html.ElementNode {
				t := strings.ToLower(p.Data)
				if t == "footer" || t == "nav" || t == "header" && p != heroHeader || a.hasBoilerplateTokenNode(p) {
					bad = true
					break
				}
			}
		}
		if !bad {
			out = append(out, b.node)
		}
	}
	return out
}

// recoverableHeaderProse admits a descriptive homepage hero only after normal
// selection and semantic fallbacks have failed. Navigation labels, linked
// promos, controls, and short slogans remain excluded.
func recoverableHeaderProse(b *block) *html.Node {
	if b == nil || (b.kind != "p" && b.kind != "blockquote" && b.kind != "generic") {
		return nil
	}
	chars := b.textChars()
	if chars < 80 || b.linkChars()*3 >= chars || b.controls() != 0 ||
		isArticleAuxiliaryLabel(normalizedLabel(b.text)) {
		return nil
	}
	for p := b.node.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		tag := strings.ToLower(p.Data)
		if tag == "nav" || tag == "footer" || hasBoilerplateToken(p) || isAdvertisementRegion(p) {
			return nil
		}
		if tag == "header" {
			if controls(p) != 0 || hasBoilerplateToken(p) || isAdvertisementRegion(p) {
				return nil
			}
			return p
		}
		if tag == "body" || tag == "html" {
			return nil
		}
	}
	return nil
}
func (a *analysis) quality(nodes []*html.Node) float64 {
	if len(nodes) == 0 {
		return 0
	}
	chars := 0
	links := 0
	for _, n := range nodes {
		t := normalizeText(nodeText(n))
		chars += utf8.RuneCountInString(t)
		links += linkTextLength(n)
	}
	return qualityFromEvidence(chars, links, len(nodes))
}

func qualityFromEvidence(chars, links, blocks int) float64 {
	q := .35 + math.Min(.4, float64(chars)/1500)
	if chars > 0 && float64(links)/float64(chars) > .8 {
		q -= .25
	}
	if blocks > 100 {
		q -= .1
	}
	return clamp(q)
}
