package urlutil

import (
	"net/url"
	"testing"
)

func TestIsHierarchicalHTTP(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{raw: "http:foo", want: false},
		{raw: "http:///path", want: false},
		{raw: "http://:80/path", want: false},
		{raw: "HTTP://example.com/path", want: true},
		{raw: "HTTPS://example.com/path", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			u, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", tt.raw, err)
			}
			if got := IsHierarchicalHTTP(u); got != tt.want {
				t.Fatalf("IsHierarchicalHTTP(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
