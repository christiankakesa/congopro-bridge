package templates

import (
	"context"
	"strings"
	"testing"

	"congopro-bridge/internal/data"
)

// TestLayoutCanonicalHost pins the fix for the host-signal conflict: the
// canonical tag and og:url used to say www.congopro.com while the sitemap
// said congopro.com, so Google received two competing hosts for every page.
// Every URL the layout emits must live on the apex domain and og/twitter
// tags must reflect the page, not the homepage.
func TestLayoutCanonicalHost(t *testing.T) {
	var sb strings.Builder
	if err := HomePage("", "https://congopro.com/", "NONCE", "deadbeef", 1500).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	home := sb.String()

	for _, want := range []string{
		`<link rel="canonical" href="https://congopro.com/">`,
		`property="og:url" content="https://congopro.com/"`,
		`property="og:image" content="https://congopro.com/images/og-image.png"`,
		`application/ld+json`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("homepage missing %q", want)
		}
	}
	// www may only ever appear in the Google script hosts, never as a page URL.
	if strings.Contains(home, "www.congopro.com") {
		t.Error("homepage still references www.congopro.com")
	}
	// gtag.js must not load eagerly — it is injected after the load event so
	// it stays out of the LCP/TBT window PageSpeed measures.
	if strings.Contains(home, `src="https://www.googletagmanager.com`) {
		t.Error("gtag.js is loaded via a <script src>, expected post-load injection")
	}
}

// TestCompanyPageMeta pins per-company SEO meta: each profile gets its own
// title, description and og tags instead of inheriting the site-generic copy.
func TestCompanyPageMeta(t *testing.T) {
	c := &data.Company{Name: "Rawbank", Activity: "Banque commerciale", City: "Kinshasa"}
	var sb strings.Builder
	if err := CompanyPage("Rawbank | Congopro", "https://congopro.com/company/rawbank", "NONCE", "deadbeef", c, false, true).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	page := sb.String()

	for _, want := range []string{
		`<title>Rawbank | Congopro</title>`,
		`name="description" content="Rawbank — Banque commerciale à Kinshasa. Coordonnées, téléphone, email et site web sur Congopro."`,
		`property="og:title" content="Rawbank | Congopro"`,
		`property="og:url" content="https://congopro.com/company/rawbank"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("company page missing %q", want)
		}
	}
}

func TestCompanyMetaDescription(t *testing.T) {
	// A company's own description wins over the composed fallback.
	own := &data.Company{Name: "X", Description: "Fournisseur d'accès internet à Lubumbashi."}
	if got := companyMetaDescription(own); got != "Fournisseur d'accès internet à Lubumbashi." {
		t.Errorf("own description not used: %q", got)
	}

	// Long descriptions are cut near 160 runes on a word boundary.
	long := &data.Company{Name: "X", Description: strings.Repeat("Une très longue description répétée encore. ", 10)}
	got := companyMetaDescription(long)
	if n := len([]rune(got)); n > 160 {
		t.Errorf("description not truncated: %d runes", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated description missing ellipsis: %q", got)
	}
}
