package pagemark_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ryanfowler/pagemark"
)

func TestDocumentTruncatedJSON(t *testing.T) {
	data, err := json.Marshal(pagemark.Document{Truncated: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"truncated":true`) {
		t.Fatalf("truncated document JSON = %s", data)
	}

	data, err = json.Marshal(pagemark.Document{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"truncated"`) {
		t.Fatalf("untruncated document JSON = %s", data)
	}
}
