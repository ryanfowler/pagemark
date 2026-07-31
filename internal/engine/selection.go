package engine

import "golang.org/x/net/html"

func (a *analysis) selectedNodes() (nodes []*html.Node) {
	for i := range a.blocks {
		if a.blocks[i].selected {
			nodes = append(nodes, a.blocks[i].node)
		}
	}
	return nodes
}
