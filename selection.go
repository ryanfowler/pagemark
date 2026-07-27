package pagemark

import "golang.org/x/net/html"

func (a *analysis) selectedNodes(pageType PageType) (nodes []*html.Node, excluded map[*html.Node]bool, dropped int) {
	// A large number of sibling blocks is normal in prose. Repetition limits
	// are only meaningful for records on pages identified as listings or
	// collections.
	limitRecords := a.o.maxRepeated > 0 && (pageType == PageTypeListing || pageType == PageTypeCollection)
	if !limitRecords {
		for i := range a.blocks {
			if a.blocks[i].selected {
				nodes = append(nodes, a.blocks[i].node)
			}
		}
		return nodes, nil, 0
	}

	excluded = make(map[*html.Node]bool)
	accepted := make(map[*html.Node]bool)
	rejected := make(map[*html.Node]bool)
	recordCounts := make(map[*html.Node]int)
	acceptRecord := func(record *html.Node) bool {
		if record == nil || record.Parent == nil {
			return true
		}
		if accepted[record] {
			return true
		}
		if rejected[record] {
			return false
		}
		if recordCounts[record.Parent] >= a.o.maxRepeated {
			rejected[record] = true
			dropped++
			return false
		}
		accepted[record] = true
		recordCounts[record.Parent]++
		return true
	}

	for i := range a.blocks {
		b := &a.blocks[i]
		if !b.selected {
			continue
		}
		if !acceptRecord(a.listingRecord(b.node)) {
			continue
		}
		// Lists and tables are segmented as single blocks. Limit their marked
		// li/tr records in place through the converter's exclusion hook.
		for _, record := range a.descendantListingRecords(b.node) {
			if !acceptRecord(record) {
				excluded[record] = true
			}
		}
		nodes = append(nodes, b.node)
	}
	return nodes, excluded, dropped
}

// listingRecord finds an explicitly marked record container. Restricting this
// to container elements avoids treating prose headings such as class=item-title
// as independent records.
