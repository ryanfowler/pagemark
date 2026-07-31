package engine

import "testing"

func TestMemoizedBool(t *testing.T) {
	var cached memoizedBool
	if value, known := cached.value(); known || value {
		t.Fatalf("zero value = (%t, %t), want unknown", value, known)
	}

	cached.store(false)
	if value, known := cached.value(); !known || value {
		t.Fatalf("stored false = (%t, %t), want (false, true)", value, known)
	}

	cached.store(true)
	if value, known := cached.value(); !known || !value {
		t.Fatalf("stored true = (%t, %t), want (true, true)", value, known)
	}
}

func TestMemoizedCount(t *testing.T) {
	var cached memoizedCount
	if value, known := cached.value(); known || value != 0 {
		t.Fatalf("zero value = (%d, %t), want unknown", value, known)
	}

	tests := []struct {
		name  string
		store int
		want  int
	}{
		{name: "count zero", store: 0, want: 0},
		{name: "maximum retained count", store: memoizedCountMax, want: memoizedCountMax},
		{name: "saturation", store: memoizedCountMax + 10, want: memoizedCountMax},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cached memoizedCount
			cached.store(tt.store)
			if value, known := cached.value(); !known || value != tt.want {
				t.Fatalf("stored %d = (%d, %t), want (%d, true)", tt.store, value, known, tt.want)
			}
		})
	}
}
