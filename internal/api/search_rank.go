package api

import "congopro-bridge/internal/data"

// pinPromoted stably partitions results: companies with an active promotion
// first, Meilisearch's relevance order preserved within each group. This is
// the whole "Mise en avant" ranking story — a Go-side pin over ≤30 results,
// chosen over a Meilisearch ranking rule so a subscription takes effect the
// moment the webhook lands (no reindex) and lapses just as instantly.
// A nil or empty promoted map returns results untouched.
func pinPromoted(results []data.SearchResult, promoted map[string]bool) []data.SearchResult {
	if len(promoted) == 0 || len(results) < 2 {
		return results
	}
	pinned := make([]data.SearchResult, 0, len(results))
	for _, r := range results {
		if promoted[r.Company.ID] {
			pinned = append(pinned, r)
		}
	}
	if len(pinned) == 0 || len(pinned) == len(results) {
		return results
	}
	for _, r := range results {
		if !promoted[r.Company.ID] {
			pinned = append(pinned, r)
		}
	}
	return pinned
}
