package api

import (
	"testing"

	"congopro-bridge/internal/data"
)

func searchResults(ids ...string) []data.SearchResult {
	out := make([]data.SearchResult, 0, len(ids))
	for i, id := range ids {
		r := data.SearchResult{Score: 1 - float64(i)*0.1}
		r.Company.ID = id
		out = append(out, r)
	}
	return out
}

func resultIDs(results []data.SearchResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Company.ID)
	}
	return out
}

func TestPinPromoted(t *testing.T) {
	cases := []struct {
		name     string
		in       []string
		promoted map[string]bool
		want     []string
	}{
		{"nil map", []string{"a", "b", "c"}, nil, []string{"a", "b", "c"}},
		{"empty map", []string{"a", "b", "c"}, map[string]bool{}, []string{"a", "b", "c"}},
		{"none promoted", []string{"a", "b", "c"}, map[string]bool{"z": true}, []string{"a", "b", "c"}},
		{"all promoted", []string{"a", "b", "c"}, map[string]bool{"a": true, "b": true, "c": true}, []string{"a", "b", "c"}},
		{"single result", []string{"a"}, map[string]bool{"a": true}, []string{"a"}},
		{"empty results", nil, map[string]bool{"a": true}, nil},
		// The point of the feature: promoted rise, relevance order survives
		// inside each group.
		{"interleaved", []string{"a", "b", "c", "d", "e"}, map[string]bool{"b": true, "d": true}, []string{"b", "d", "a", "c", "e"}},
		{"promoted already first", []string{"a", "b", "c"}, map[string]bool{"a": true}, []string{"a", "b", "c"}},
		{"promoted last", []string{"a", "b", "c"}, map[string]bool{"c": true}, []string{"c", "a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resultIDs(pinPromoted(searchResults(tc.in...), tc.promoted))
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// Scores must travel with their rows — a pin that reorders IDs but not the
// embedded relevance scores would display the wrong percentages.
func TestPinPromotedKeepsScoresWithRows(t *testing.T) {
	in := searchResults("a", "b", "c")
	scoreOfB := in[1].Score
	out := pinPromoted(in, map[string]bool{"b": true})
	if out[0].Company.ID != "b" || out[0].Score != scoreOfB {
		t.Fatalf("row b lost its score: got id=%s score=%v, want score %v",
			out[0].Company.ID, out[0].Score, scoreOfB)
	}
}
