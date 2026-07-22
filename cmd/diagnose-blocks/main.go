// Command diagnose-blocks prints scoring diagnostics for snippets in a local HTML file.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/ryanfowler/pagemark"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: diagnose-blocks HTML_FILE PAGE_URL SNIPPET...")
		os.Exit(2)
	}
	source, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	doc, err := pagemark.ExtractBytes(source, os.Args[2], pagemark.WithDiagnostics(true))
	if err != nil {
		panic(err)
	}
	for _, snippet := range os.Args[3:] {
		found := false
		for _, block := range doc.Diagnostics.Blocks {
			if strings.Contains(block.Text, snippet) {
				fmt.Printf("%q: id=%d kind=%s score=%.2f selected=%t reasons=%v\n", snippet, block.ID, block.Kind, block.Score, block.Selected, block.Reasons)
				found = true
			}
		}
		if !found {
			fmt.Printf("%q: no segmented block found\n", snippet)
		}
	}
}
