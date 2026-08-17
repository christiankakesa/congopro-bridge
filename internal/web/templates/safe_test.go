package templates

import (
	"context"
	"strings"
	"testing"

	"congopro-bridge/internal/data"
)

func TestLocationPillAddressHTML(t *testing.T) {
	tests := []struct {
		name                  string
		a1, a2, city, country string
		want                  string
	}{
		{
			name: "all parts",
			a1:   "12 Av. du Commerce", a2: "Gombe", city: "Kinshasa", country: "RD Congo",
			want: `<span itemprop="streetAddress">12 Av. du Commerce</span>, ` +
				`<span itemprop="streetAddress">Gombe</span>, ` +
				`<span itemprop="addressLocality">Kinshasa</span>, ` +
				`<span itemprop="addressCountry">RD Congo</span>`,
		},
		{
			name: "empty parts skipped, no leading/trailing separators",
			a1:   "", a2: "", city: "Kinshasa", country: "",
			want: `<span itemprop="addressLocality">Kinshasa</span>`,
		},
		{
			name: "all empty",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LocationPillAddressHTML(tt.a1, tt.a2, tt.city, tt.country); got != tt.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

// Regression: the visible location pill ran address parts together with no
// separator ("GombeKinshasa"). Render one full row the way the search page
// does and assert the pill separates its spans.
func TestResultRowLocationPillSeparatesParts(t *testing.T) {
	sr := data.SearchResult{
		Company: data.Company{
			Name:         "Test Sprl",
			NameSeo:      "test-sprl",
			Address:      "12 Av. du Commerce",
			AddressLine2: "Gombe",
			City:         "Kinshasa",
		},
	}
	var buf strings.Builder
	if err := resultRow(sr).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := buf.String()

	if !strings.Contains(got, `<span itemprop="streetAddress">12 Av. du Commerce</span>, `+
		`<span itemprop="streetAddress">Gombe</span>, <span itemprop="addressLocality">Kinshasa</span>`) {
		t.Errorf("location pill parts not comma-separated in rendered row:\n%s", got)
	}
}
