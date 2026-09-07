package httpx

import (
	"net/http/httptest"
	"testing"
)

func pageFor(t *testing.T, query string, maxLimit int) Page {
	t.Helper()
	return ParsePage(httptest.NewRequest("GET", "/list?"+query, nil), maxLimit)
}

func TestParsePage(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		maxLimit   int
		wantNumber int
		wantLimit  int
		wantOffset int
	}{
		{
			name:  "no parameters means no pagination",
			query: "", maxLimit: 100,
			wantNumber: 1, wantLimit: 0, wantOffset: 0,
		},
		{
			name:  "first page",
			query: "page=1&limit=8", maxLimit: 100,
			wantNumber: 1, wantLimit: 8, wantOffset: 0,
		},
		{
			name:  "third page offsets by two full pages",
			query: "page=3&limit=8", maxLimit: 100,
			wantNumber: 3, wantLimit: 8, wantOffset: 16,
		},
		{
			name:  "limit alone starts at page one",
			query: "limit=20", maxLimit: 100,
			wantNumber: 1, wantLimit: 20, wantOffset: 0,
		},
		{
			name:  "page alone falls back to the default size",
			query: "page=2", maxLimit: 100,
			wantNumber: 2, wantLimit: DefaultPageLimit, wantOffset: DefaultPageLimit,
		},
		{
			name:  "an oversized limit is clamped, not rejected",
			query: "limit=100000", maxLimit: 100,
			wantNumber: 1, wantLimit: 100, wantOffset: 0,
		},
		{
			name:  "page zero is page one",
			query: "page=0&limit=8", maxLimit: 100,
			wantNumber: 1, wantLimit: 8, wantOffset: 0,
		},
		{
			name:  "a negative page is page one",
			query: "page=-4&limit=8", maxLimit: 100,
			wantNumber: 1, wantLimit: 8, wantOffset: 0,
		},
		{
			name:  "a junk limit means no pagination",
			query: "limit=abc", maxLimit: 100,
			wantNumber: 1, wantLimit: 0, wantOffset: 0,
		},
		{
			name:  "a negative limit means no pagination",
			query: "limit=-10", maxLimit: 100,
			wantNumber: 1, wantLimit: 0, wantOffset: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := pageFor(t, tc.query, tc.maxLimit)
			if p.Number != tc.wantNumber {
				t.Errorf("Number = %d, want %d", p.Number, tc.wantNumber)
			}
			if p.Limit != tc.wantLimit {
				t.Errorf("Limit = %d, want %d", p.Limit, tc.wantLimit)
			}
			if p.Offset != tc.wantOffset {
				t.Errorf("Offset = %d, want %d", p.Offset, tc.wantOffset)
			}
			if got, want := p.Requested(), tc.wantLimit > 0; got != want {
				t.Errorf("Requested() = %v, want %v", got, want)
			}
		})
	}
}

func TestPageHasMore(t *testing.T) {
	tests := []struct {
		name  string
		query string
		total int
		want  bool
	}{
		{"more rows after page one", "page=1&limit=8", 20, true},
		{"last page", "page=3&limit=8", 20, false},
		{"exactly one full page", "page=1&limit=8", 8, false},
		{"one row over a full page", "page=1&limit=8", 9, true},
		{"past the end", "page=9&limit=8", 20, false},
		{"unpaginated is never partial", "", 5000, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pageFor(t, tc.query, 100).HasMore(tc.total); got != tc.want {
				t.Errorf("HasMore(%d) = %v, want %v", tc.total, got, tc.want)
			}
		})
	}
}
