package pagemark

import (
	"math"
	"strings"

	"golang.org/x/net/html"
)

type scoringProfile uint8

const (
	scoringPrimary scoringProfile = iota
	scoringRelaxedLabels
	scoringRelaxedThreshold
)

func (p scoringProfile) name() string {
	switch p {
	case scoringRelaxedLabels:
		return "relaxed-labels"
	case scoringRelaxedThreshold:
		return "relaxed-threshold"
	default:
		return "primary"
	}
}

type extractionAttempt struct {
	profile              string
	nodes                []*html.Node
	quality              float64
	chars, links, blocks int
	hardExcluded         bool
	state                []blockAttemptState
}

type blockAttemptState struct {
	score    float64
	selected bool
	reasons  []string
}

// nodeState combines the per-node memoization used by classification passes.
// Keeping one map avoids paying for several independent hash tables containing
// the same DOM pointers on large pages.
func (a *analysis) score(pt PageType, profile scoringProfile) {
	seen := make(map[string]struct{}, len(a.blocks))
	for i := range a.blocks {
		b := &a.blocks[i]
		// Scoring is non-destructive, but its result state is not. Every profile
		// starts from the segmented block evidence rather than the previous pass.
		b.score = 0
		b.selected = false
		b.reasons = nil
		length := b.textChars()
		score := 0.0
		switch b.kind {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			score = 1.8
		case "p":
			score = .7 + math.Min(2, float64(length)/180)
		case "pre", "table", "dl":
			score = 2.4
		case "ul", "ol":
			score = 1.3
		case "blockquote", "figure":
			score = 1.1
		case "image":
			score = .7
		case "generic":
			score = 0.4 + math.Min(2, float64(length)/250)
		}
		a.addReason(b, "content shape")
		if b.imageOnly {
			// Descriptive image-only paragraphs have no text length with which to
			// earn the normal prose score. The remaining ancestry and boilerplate
			// signals still decide whether this is primary content.
			score += .4
			a.addReason(b, "descriptive image")
		}
		if a.hasIrrelevantAncestor(b.node) {
			score -= 8
			a.addReason(b, "auxiliary content")
		}
		links, total := b.linkChars(), max(1, length)
		density := float64(links) / float64(total)
		for p := b.node.Parent; p != nil; p = p.Parent {
			if p.Type != html.ElementNode {
				continue
			}
			tag := strings.ToLower(p.Data)
			tokens := elementTokens(p)
			if tag == "main" {
				score += 2
				a.addReason(b, "inside main")
			}
			if tag == "article" {
				score += 1.3
			}
			if tag == "header" || tag == "footer" || tag == "nav" {
				score -= 5
				a.addReason(b, "inside page chrome")
			}
			if tag == "aside" {
				score -= 1.5
			}
			// Feature flags and global UI state are often stored as classes on
			// the document shell (for example, "toc-available"). They describe
			// available chrome, not every descendant's content region.
			if tag != "html" && tag != "body" && hasBoilerplateToken(p) &&
				!(tag == "article" && a.substantialArticleScope(p)) {
				// This is only a weak class/id signal. Structural auxiliary decisions
				// above remain absolute in every profile.
				if profile == scoringPrimary || !a.strongArticleProseEvidence(b) {
					score -= 3
					a.addReason(b, "boilerplate label")
				} else {
					a.addReason(b, "article evidence overrides weak label")
				}
			}
			if pt == PageTypeDiscussion && containsAny(tokens, "comment", "comments", "answer", "post", "thread") {
				score += 2
			}
			if (pt == PageTypeListing || pt == PageTypeCollection) && containsAny(tokens, "card", "item", "result", "product") {
				score += 1.5
			}
		}
		if b.kind == "p" && (pt == PageTypeArticle || pt == PageTypeDocumentation || pt == PageTypeDiscussion || pt == PageTypeProduct || pt == PageTypeService) {
			score += 0.35
		}
		if b.kind == "generic" && hasStatusUpdateContext(b.node) {
			// Status and changelog systems commonly render each update as direct
			// text in a generic div next to a heading. The explicit nested update
			// convention is stronger than generic prose length alone.
			score += 1
			a.addReason(b, "structured status update")
		}
		if pt == PageTypeDiscussion && isDiscussionBodyContainer(b.node) {
			score += 3
			a.addReason(b, "discussion post body")
		}
		if pt == PageTypeDiscussion && a.hasStandaloneMessageAncestor(b.node) {
			// A standalone .message is a UI notice, not a message-body convention.
			// Make this absolute rather than relative: deeply nested thread/main
			// context must not raise it above the selection threshold.
			score = -8
			a.addReason(b, "discussion notice")
		}
		if pt == PageTypeDiscussion && !isDiscussionBodyContainer(b.node) && isDiscussionControlBlock(b.node) {
			score -= 6
			a.addReason(b, "discussion controls")
		}
		if density > .75 && pt != PageTypeListing && pt != PageTypeCollection && pt != PageTypeDocumentation {
			score -= 2
			a.addReason(b, "high link density")
		}
		if b.controls() > 2 {
			score -= 2
		}
		// Segmentation stores normalized text, so normalizing it again only
		// rescans the complete block string.
		hash := strings.ToLower(b.text)
		_, duplicate := seen[hash]
		if duplicate && len(hash) > 30 {
			score -= 4
			a.addReason(b, "duplicate")
		}
		seen[hash] = struct{}{}
		if a.o.selectionMode == SelectionPrecision {
			score -= .35
		}
		if a.o.selectionMode == SelectionRecall {
			score += .35
		}
		b.score = score
		b.selected = score >= 1.0
		if profile == scoringRelaxedThreshold && !b.selected && score >= .65 &&
			b.kind == "p" && length >= 40 &&
			a.strongArticleProseEvidence(b) && !a.hasIrrelevantAncestor(b.node) &&
			b.controls() == 0 && links*2 < total {
			b.selected = true
			a.addReason(b, "relaxed article prose threshold")
		}
	}

	// Independent scores are deliberately conservative, but article prose is a
	// region rather than a sequence that ends at the first rejected sibling.
	// Once a container has established itself with multiple strong paragraphs,
	// retain its other substantive paragraphs across isolated auxiliary nodes.
	// Hard auxiliary classification still wins, so the interruption itself is
	// not pulled into the output.
	a.strengthenArticleContinuity(pt)

}

// indexStrongArticleProse computes local paragraph cohorts once. Relaxed
// profiles consult this cache for every block, so rescoring remains linear
// rather than repeatedly scanning all sibling blocks.
func (a *analysis) indexStrongArticleProse() {
	if a.strongArticleProseIndexed {
		return
	}
	a.strongArticleProseIndexed = true
	a.strongArticleProse = make(map[*html.Node]bool, len(a.blocks))
	type cohortEvidence struct {
		paragraphs, chars int
	}
	cohorts := make(map[*html.Node]cohortEvidence)
	eligible := make([]bool, len(a.blocks))
	insideMain := make([]bool, len(a.blocks))
	insideArticle := make([]bool, len(a.blocks))
	for i := range a.blocks {
		b := &a.blocks[i]
		length := b.textChars()
		if b.kind != "p" || length < 40 || a.hasIrrelevantAncestor(b.node) ||
			b.controls() != 0 || b.linkChars()*2 >= max(1, length) {
			continue
		}
		eligible[i] = true
		cohort := cohorts[b.node.Parent]
		cohort.paragraphs++
		cohort.chars += length
		cohorts[b.node.Parent] = cohort
		for p := b.node.Parent; p != nil; p = p.Parent {
			if p.Type != html.ElementNode {
				continue
			}
			tag := strings.ToLower(p.Data)
			insideMain[i] = insideMain[i] || tag == "main" || strings.EqualFold(attrValue(p, "role"), "main")
			insideArticle[i] = insideArticle[i] || tag == "article"
		}
	}
	metadataEvidence := a.meta.articlePublished || a.meta.articleType || a.meta.headline
	for i := range a.blocks {
		if !eligible[i] {
			continue
		}
		cohort := cohorts[a.blocks[i].node.Parent]
		strong := insideArticle[i] || cohort.paragraphs >= 2 && cohort.chars >= 100 && (insideMain[i] || metadataEvidence)
		if strong {
			a.strongArticleProse[a.blocks[i].node] = true
			a.hasStrongArticleProse = true
		}
	}
}

// strongArticleProseEvidence can neutralize a weak boilerplate-looking class,
// but never a structural auxiliary decision.
func (a *analysis) strongArticleProseEvidence(b *block) bool {
	if b == nil {
		return false
	}
	a.indexStrongArticleProse()
	return a.strongArticleProse[b.node]
}

func (a *analysis) hasStrongArticleEvidence() bool {
	a.indexStrongArticleProse()
	return a.hasStrongArticleProse
}

func (a *analysis) makeExtractionAttempt(profile scoringProfile, nodes []*html.Node) extractionAttempt {
	attempt := extractionAttempt{profile: profile.name(), nodes: append([]*html.Node(nil), nodes...)}
	attempt.state = make([]blockAttemptState, len(a.blocks))
	for i := range a.blocks {
		b := &a.blocks[i]
		attempt.state[i] = blockAttemptState{score: b.score, selected: b.selected, reasons: b.reasons}
		if !b.selected {
			continue
		}
		if a.hasIrrelevantAncestor(b.node) || hardHidden(b.node) {
			attempt.hardExcluded = true
			continue
		}
		length := b.textChars()
		attempt.chars += length
		attempt.links += b.linkChars()
		attempt.blocks++
	}
	attempt.quality = qualityFromEvidence(attempt.chars, attempt.links, attempt.blocks)
	return attempt
}

func (a *analysis) restoreExtractionAttempt(attempt extractionAttempt) {
	for i := range a.blocks {
		a.blocks[i].score = attempt.state[i].score
		a.blocks[i].selected = attempt.state[i].selected
		a.blocks[i].reasons = attempt.state[i].reasons
	}
}

func (a *analysis) shouldRetryArticle(pt PageType, nodes []*html.Node) bool {
	eligible := pt == PageTypeArticle || pt == PageTypeGeneric && !a.pageTypeExplicit && a.hasStrongArticleEvidence()
	if !eligible {
		return false
	}
	if len(nodes) == 0 {
		return true
	}
	chars, links, blocks := 0, 0, 0
	for i := range a.blocks {
		b := &a.blocks[i]
		if !b.selected || a.hasIrrelevantAncestor(b.node) || hardHidden(b.node) {
			continue
		}
		chars += b.textChars()
		links += b.linkChars()
		blocks++
	}
	if qualityFromEvidence(chars, links, blocks) < .42 {
		return true
	}
	metadataEvidence := a.meta.articlePublished || a.meta.articleType || a.meta.headline
	return metadataEvidence && chars < 120 && a.hasStrongArticleEvidence()
}

func betterArticleAttempt(current, candidate extractionAttempt) bool {
	if candidate.hardExcluded || len(candidate.nodes) == 0 || candidate.chars == 0 {
		return false
	}
	if candidate.links*2 >= candidate.chars {
		return false
	}
	// With no primary content there is no meaningful relative growth baseline.
	// Still enforce absolute safety and link-density checks above.
	if len(current.nodes) == 0 {
		return true
	}
	if candidate.blocks > current.blocks*3+12 {
		return false
	}
	// Quality already combines useful prose, link density, and block count. A
	// close relaxed result must additionally recover a material amount of prose.
	if candidate.quality > current.quality+.05 && candidate.chars >= current.chars {
		return true
	}
	added := candidate.chars - current.chars
	material := added >= 120 && candidate.chars*4 >= current.chars*5 ||
		current.chars < 120 && added >= 40 && candidate.chars*3 >= current.chars*4
	return material && candidate.quality >= current.quality-.03
}

// addReason avoids allocating diagnostic-only reason slices on the normal
// extraction path.
func (a *analysis) addReason(b *block, reason string) {
	if a.diag != nil {
		b.reasons = append(b.reasons, reason)
	}
}

func (a *analysis) populateBlockDiagnostics() {
	if a.diag == nil {
		return
	}
	for i := range a.blocks {
		b := &a.blocks[i]
		text := b.text
		if len(text) > 160 {
			text = text[:160]
		}
		a.diag.Blocks = append(a.diag.Blocks, BlockDiagnostic{ID: b.id, Kind: b.kind, Text: text, Score: b.score, Selected: b.selected, Reasons: append([]string(nil), b.reasons...)})
	}
}

func (a *analysis) strengthenArticleContinuity(pt PageType) {
	if pt != PageTypeArticle && pt != PageTypeGeneric {
		return
	}
	regionFor := func(n *html.Node) *html.Node {
		if article := a.primaryArticleAncestor(n); article != nil {
			return article
		}
		// Generic publishers commonly put sibling paragraphs in an entry/content
		// div without semantic article markup. Keep this local; using main or body
		// would let unrelated trailing regions inherit article confidence.
		for p := n.Parent; p != nil; p = p.Parent {
			if p.Type == html.ElementNode && elementContainsAny(p, "article", "content", "entry", "post-body", "story-body") {
				return p
			}
		}
		return n.Parent
	}

	strong := make(map[*html.Node]int)
	regions := make([]*html.Node, len(a.blocks))
	for i := range a.blocks {
		b := &a.blocks[i]
		regions[i] = regionFor(b.node)
		length := b.textChars()
		if b.kind == "p" && b.selected && length >= 60 && b.linkChars()*2 < max(1, length) {
			strong[regions[i]]++
		}
	}

	// Retain the established-region behavior for ordinary paragraphs, including
	// a final paragraph before sources or other article furniture. A second,
	// bounded bridge below handles short transitions only when selected prose is
	// present on both sides.
	for i := range a.blocks {
		b := &a.blocks[i]
		if b.selected || b.kind != "p" || b.textChars() < 40 ||
			strong[regions[i]] < 2 || !a.plausibleArticleBridge(b, regions[i]) {
			continue
		}
		b.score = math.Max(b.score, 1)
		b.selected = true
		a.addReason(b, "article region continuity")
	}

	const maxBridgeBlocks = 12
	for i := range a.blocks {
		b := &a.blocks[i]
		region := regions[i]
		length := b.textChars()
		if b.selected || b.kind != "p" || length < 12 || strong[region] < 2 ||
			!a.plausibleArticleBridge(b, region) {
			continue
		}
		before, after := false, false
		for distance := 1; distance <= maxBridgeBlocks; distance++ {
			if j := i - distance; j >= 0 && regions[j] == region && selectedArticleProse(&a.blocks[j]) {
				before = true
				break
			}
		}
		for distance := 1; distance <= maxBridgeBlocks; distance++ {
			if j := i + distance; j < len(a.blocks) && regions[j] == region && selectedArticleProse(&a.blocks[j]) {
				after = true
				break
			}
		}
		if !before || !after {
			continue
		}
		b.score = math.Max(b.score, 1)
		b.selected = true
		a.addReason(b, "article prose bridge")
	}
}

func hasStatusUpdateContext(n *html.Node) bool {
	body := false
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if hasExactClass(p, "update-body") {
			body = true
		}
		if hasExactClass(p, "update-container") && !body {
			walk(p, func(x *html.Node) bool {
				if x.Type == html.ElementNode && hasExactClass(x, "update-body") {
					body = true
					return false
				}
				return !body
			})
		}
		if body && (hasExactClass(p, "update-container") || hasExactClass(p, "update-row") ||
			elementContainsAny(p, "incident-updates", "status-updates")) {
			return true
		}
	}
	return false
}

func selectedArticleProse(b *block) bool {
	if b == nil || !b.selected {
		return false
	}
	switch b.kind {
	case "p", "blockquote", "generic":
		length := b.textChars()
		return length >= 20 && b.linkChars()*2 < max(1, length)
	}
	return false
}

// plausibleArticleBridge rechecks local auxiliary signals before continuity
// can override a block's independent score. The dominant region itself is not
// checked for boilerplate tokens: newsletter/article wrappers often carry such
// names, which is precisely why otherwise valid paragraphs need continuity.
func (a *analysis) plausibleArticleBridge(b *block, region *html.Node) bool {
	if b == nil || region == nil || a.hasIrrelevantAncestor(b.node) {
		return false
	}
	length := b.textChars()
	label := normalizedLabel(b.text)
	if length == 0 || b.linkChars()*2 >= length || b.controls() > 0 ||
		isArticleAuxiliaryLabel(label) || auxiliaryLabels[label] {
		return false
	}
	for p := b.node; p != nil && p != region; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		tag := strings.ToLower(p.Data)
		if tag == "aside" || tag == "header" || tag == "footer" || tag == "nav" ||
			isAdvertisementRegion(p) || hasBoilerplateToken(p) || isListingRecordElement(p) {
			return false
		}
	}
	return nodeWithin(b.node, region)
}

// separateDocumentTitle applies the existing structural title recovery, then
// removes the resolved title heading before Markdown conversion. Resolving at
// the HTML-node level keeps Markdown, plain text, sections, retained media, and
// byte limits consistent; rendered Markdown must not be post-processed as text.
