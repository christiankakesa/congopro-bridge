package ads

import (
	"testing"
	"time"
)

// The eligibility semantics are a preserved behavioral contract from the
// YAML era — these tests lock them down so the DB refactor can't drift.

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(dateLayout, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func testStore(settings Settings, campaigns ...Campaign) *Store {
	for i := range campaigns {
		campaigns[i].prepare()
	}
	return &Store{settings: settings, campaigns: campaigns}
}

func ids(pool []AdWire) map[string]int {
	counts := map[string]int{}
	for _, a := range pool {
		counts[a.ID]++
	}
	return counts
}

func TestEligibleAds_MasterSwitch(t *testing.T) {
	s := testStore(Settings{Active: false},
		Campaign{ID: "a", Status: "active", Placement: "homepage"})
	if got := s.EligibleAds("", time.Now()); got != nil {
		t.Fatalf("inactive master switch must serve nothing, got %v", got)
	}
}

func TestEligibleAds_HomepageSearchRouting(t *testing.T) {
	now := time.Now()
	s := testStore(Settings{Active: true},
		Campaign{ID: "home", Status: "active", Placement: "homepage"},
		Campaign{ID: "search", Status: "active", Placement: ""}, // missing = search-only
		Campaign{ID: "search2", Status: "active", Placement: "search_results"},
	)

	home := ids(s.EligibleAds("", now))
	if len(home) != 1 || home["home"] == 0 {
		t.Fatalf("homepage pool = %v, want only the homepage ad", home)
	}
	search := ids(s.EligibleAds("restaurant", now))
	if search["home"] != 0 || search["search"] == 0 || search["search2"] == 0 {
		t.Fatalf("search pool = %v, homepage ad must not leak", search)
	}
}

func TestEligibleAds_StatusFiltering(t *testing.T) {
	now := time.Now()
	s := testStore(Settings{Active: true},
		Campaign{ID: "active", Status: "active"},
		Campaign{ID: "draft", Status: "draft"},
		Campaign{ID: "paused", Status: "paused"},
		Campaign{ID: "expired", Status: "expired"},
	)
	got := ids(s.EligibleAds("anything", now))
	if len(got) != 1 || got["active"] == 0 {
		t.Fatalf("only status=active may serve, got %v", got)
	}
}

func TestEligibleAds_PeriodBounds(t *testing.T) {
	now := mustDate(t, "2026-08-24")
	s := testStore(Settings{Active: true},
		Campaign{ID: "not-started", Status: "active", PeriodStart: "2026-08-25"},
		Campaign{ID: "started", Status: "active", PeriodStart: "2026-08-24"},
		Campaign{ID: "ended", Status: "active", PeriodEnd: "2026-08-23"},
		Campaign{ID: "ends-today", Status: "active", PeriodEnd: "2026-08-24"},
	)
	got := ids(s.EligibleAds("q", now))
	if got["not-started"] != 0 {
		t.Error("ad before its start date must not serve")
	}
	if got["started"] == 0 {
		t.Error("ad starting today must serve")
	}
	if got["ended"] != 0 {
		t.Error("ad past its end date must not serve")
	}
	if got["ends-today"] == 0 {
		t.Error("end date is INCLUSIVE through the whole day")
	}
}

func TestEligibleAds_KeywordMatching(t *testing.T) {
	now := time.Now()
	s := testStore(Settings{Active: true},
		Campaign{ID: "kw", Status: "active", Keywords: []string{"banque", "transfert d'argent"}},
		Campaign{ID: "global", Status: "active"},
	)

	// Exact word.
	if got := ids(s.EligibleAds("meilleure banque kinshasa", now)); got["kw"] == 0 {
		t.Error("exact keyword must match")
	}
	// Naive plural (s).
	if got := ids(s.EligibleAds("les banques de la ville", now)); got["kw"] == 0 {
		t.Error("plural 's' must match")
	}
	// Multi-word phrase as whole words.
	if got := ids(s.EligibleAds("service transfert d'argent rapide", now)); got["kw"] == 0 {
		t.Error("phrase keyword must match as whole words")
	}
	// No match: substring is not enough.
	if got := ids(s.EligibleAds("bananier", now)); got["kw"] != 0 {
		t.Error("substring must not match (word boundary required)")
	}
	// Keyword ads never serve on the empty homepage query…
	if got := ids(s.EligibleAds("", now)); got["kw"] != 0 {
		t.Error("keyword ad must not serve on homepage")
	}
	// …but global ads do (homepage needs placement=homepage though; use a
	// search query for the global check).
	if got := ids(s.EligibleAds("bananier", now)); got["global"] == 0 {
		t.Error("global ad serves when no keyword matches")
	}
}

func TestEligibleAds_KeywordPriority(t *testing.T) {
	now := time.Now()
	s := testStore(Settings{Active: true},
		Campaign{ID: "kw", Status: "active", Keywords: []string{"banque"}},
		Campaign{ID: "global", Status: "active"},
	)
	got := ids(s.EligibleAds("banque", now))
	if got["global"] != 0 {
		t.Fatal("keyword matches must suppress global ads entirely")
	}
	if got["kw"] == 0 {
		t.Fatal("keyword ad must be present")
	}
}

func TestEligibleAds_WeightingByDuplication(t *testing.T) {
	now := time.Now()
	s := testStore(Settings{Active: true},
		Campaign{ID: "heavy", Status: "active", Weight: 5},
		Campaign{ID: "light", Status: "active", Weight: 1},
	)
	got := ids(s.EligibleAds("q", now))
	if got["heavy"] != 5 {
		t.Fatalf("weight 5 must duplicate the ad 5 times in the pool, got %d", got["heavy"])
	}
	// weight <= 0 behaves as 1
	s2 := testStore(Settings{Active: true}, Campaign{ID: "zero", Status: "active", Weight: 0})
	if got := ids(s2.EligibleAds("q", now)); got["zero"] != 1 {
		t.Fatalf("weight 0 must behave as 1, got %d", got["zero"])
	}
}

func TestSettings_Snapshot(t *testing.T) {
	s := testStore(Settings{Active: true, RotationSec: 18, MaxPerPage: 2})
	if got := s.Settings(); !got.Active || got.RotationSec != 18 || got.MaxPerPage != 2 {
		t.Fatalf("Settings() = %+v", got)
	}
}

func TestGetAdPreviews(t *testing.T) {
	p := GetAdPreviews()
	if len(p) != len(LabelPresets) {
		t.Fatalf("previews = %d, want %d", len(p), len(LabelPresets))
	}
	if p[0].ID != "preview-home" {
		t.Fatalf("first preset must be 'home' (premium preview), got %q", p[0].ID)
	}
}
