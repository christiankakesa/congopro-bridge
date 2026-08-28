package templates

import (
	"context"
	"strings"
	"testing"

	"congopro-bridge/internal/data"
)

func TestLocationPillAddressHTML(t *testing.T) {
	tests := []struct {
		name         string
		a1, a2, city string
		want         string
	}{
		{
			name: "all parts",
			a1:   "12 Av. du Commerce", a2: "Gombe", city: "Kinshasa",
			want: `<span itemprop="streetAddress">12 Av. du Commerce</span>, ` +
				`<span itemprop="streetAddress">Gombe</span>, ` +
				`<span itemprop="addressLocality">Kinshasa</span>`,
		},
		{
			name: "empty parts skipped, no leading/trailing separators",
			a1:   "", a2: "", city: "Kinshasa",
			want: `<span itemprop="addressLocality">Kinshasa</span>`,
		},
		{
			name: "all empty",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LocationPillAddressHTML(tt.a1, tt.a2, tt.city); got != tt.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

// Regression tests for the visible location pill, rendered through the full
// resultRow component the way the search page does:
//  1. parts must be separated (they used to run together: "GombeKinshasa");
//  2. country must NOT appear in the pill (DRC-only site, long raw value) —
//     but must survive in the hidden schema.org block as a <meta>, so the
//     structured data stays complete.
func TestResultRowLocationPill(t *testing.T) {
	sr := data.SearchResult{
		Company: data.Company{
			Name:         "Test Sprl",
			NameSeo:      "test-sprl",
			Address:      "12 Av. du Commerce",
			AddressLine2: "Gombe",
			City:         "Kinshasa",
			Country:      "Democratic Republic of the Congo",
		},
	}
	var buf strings.Builder
	if err := resultRow(sr, false).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := buf.String()

	if !strings.Contains(got, `<span itemprop="streetAddress">12 Av. du Commerce</span>, `+
		`<span itemprop="streetAddress">Gombe</span>, <span itemprop="addressLocality">Kinshasa</span>`) {
		t.Errorf("location pill parts not comma-separated in rendered row:\n%s", got)
	}
	if strings.Contains(got, `<span itemprop="addressCountry">`) {
		t.Errorf("country must not render in the visible pill:\n%s", got)
	}
	if !strings.Contains(got, `<meta itemprop="addressCountry" content="Democratic Republic of the Congo"/>`) {
		t.Errorf("country missing from the hidden schema.org block:\n%s", got)
	}
}

// TestAdCTATextColor pins the WCAG AA text-color choice for admin-chosen ad
// backgrounds: mid-tones like #ea8600 fail with white (Lighthouse measured
// 2.65:1), dark colors keep white, and non-hex input falls back to white.
func TestAdCTATextColor(t *testing.T) {
	cases := []struct{ bg, want string }{
		{"#ea8600", "#111827"}, // the flagged orange: white fails at 2.65:1
		{"#F59E0B", "#111827"}, // default premium-ad amber
		{"#be0203", "#ffffff"}, // brand red: white passes
		{"#0A66C2", "#ffffff"}, // dark blue: white passes at 5.7:1
		{"#fff", "#111827"},    // 3-digit form
		{"rebeccapurple", "#ffffff"}, // non-hex → default
	}
	for _, c := range cases {
		if got := AdCTATextColor(c.bg); got != c.want {
			t.Errorf("AdCTATextColor(%q) = %q, want %q", c.bg, got, c.want)
		}
	}
}
