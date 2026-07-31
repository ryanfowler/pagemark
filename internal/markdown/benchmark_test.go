package markdown

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

var benchmarkResult Result

// BenchmarkConvertNeutralInlineContainers measures traversal of transparent
// inline wrappers. Such wrappers are common in syntax-highlighted and generated
// HTML and must not require a temporary child slice for each container.
func BenchmarkConvertNeutralInlineContainers(b *testing.B) {
	const count = 200
	root, err := html.Parse(strings.NewReader("<p>" + strings.Repeat("<span>word</span>", count) + "</p>"))
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		benchmarkResult = Convert([]*html.Node{root}, Config{})
	}
}
