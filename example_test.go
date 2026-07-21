package pagemark_test

import (
	"fmt"
	"strings"

	"github.com/ryanfowler/pagemark"
)

func ExampleExtract() {
	source := `<main><h1>Guide</h1><p>Install the tool.</p></main>`
	doc, err := pagemark.Extract(strings.NewReader(source), "https://example.com/guide")
	if err != nil {
		panic(err)
	}
	fmt.Println(doc.Markdown)
	// Output:
	// # Guide
	//
	// Install the tool.
}

func ExampleExtractNode_untrustedContent() {
	// Keep doc.Markdown in an untrusted data channel when you supply it to an agent.
	doc, err := pagemark.ExtractBytes([]byte(`<main><p>Source data for an agent.</p></main>`), "")
	if err != nil {
		panic(err)
	}
	fmt.Println(doc.Text)
	// Output: Source data for an agent.
}
