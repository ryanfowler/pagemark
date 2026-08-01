package engine

import "testing"

func TestClassifyPage(t *testing.T) {
	tests := []struct {
		name     string
		evidence pageEvidence
		want     PageType
	}{
		{name: "generic default", want: PageTypeGeneric},
		{
			name:     "strong article metadata and prose",
			evidence: pageEvidence{articleType: true, proseBlocks: 5, proseChars: 700},
			want:     PageTypeArticle,
		},
		{
			name:     "documentation path and context",
			evidence: pageEvidence{documentationPath: true, documentationContext: true},
			want:     PageTypeDocumentation,
		},
		{
			name:     "repeated substantive discussion records",
			evidence: pageEvidence{discussionRecords: 3, discussionProseChars: 120},
			want:     PageTypeDiscussion,
		},
		{
			name:     "product schema",
			evidence: pageEvidence{schemaProduct: true},
			want:     PageTypeProduct,
		},
		{
			name:     "listing schema",
			evidence: pageEvidence{schemaListing: true},
			want:     PageTypeListing,
		},
		{
			name:     "service schema",
			evidence: pageEvidence{schemaService: true},
			want:     PageTypeService,
		},
		{
			name:     "text archive listing",
			evidence: pageEvidence{hasTextListing: true},
			want:     PageTypeListing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyPage(tt.evidence, false)
			if got.pageType != tt.want {
				t.Fatalf("page type = %q, want %q", got.pageType, tt.want)
			}
			if got.candidates != nil {
				t.Fatal("ordinary classification allocated candidates")
			}
		})
	}
}

func TestRankPageTypesDeterministicTieBreaking(t *testing.T) {
	scores := map[PageType]float64{
		PageTypeGeneric: 2,
		PageTypeArticle: 2,
		PageTypeProduct: 1,
	}
	for i := 0; i < 20; i++ {
		got := rankPageTypes(scores, true)
		if got.pageType != PageTypeArticle {
			t.Fatalf("page type = %q, want %q", got.pageType, PageTypeArticle)
		}
		if len(got.candidates) != len(scores) || got.candidates[0].Type != PageTypeArticle || got.candidates[1].Type != PageTypeGeneric {
			t.Fatalf("candidates = %#v, want article then generic", got.candidates)
		}
	}
}
