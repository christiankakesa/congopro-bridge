package templates

import (
	"context"
	"io"
	"strings"
	"testing"

	"congopro-bridge/internal/data"
)

// /account/promote tells the customer the badge appears "sur sa fiche et
// dans les résultats". The profile half was missing for the whole life of
// the paid feature, so both surfaces are pinned here.
func TestPromotedBadgeOnBothSurfaces(t *testing.T) {
	render := func(c interface {
		Render(context.Context, io.Writer) error
	}) string {
		var b strings.Builder
		if err := c.Render(context.Background(), &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	co := data.Company{ID: "c1", Name: "Rawbank", NameSeo: "banque-rawbank", Activity: "Banque"}

	// the badge text follows an icon, so match the word rather than ">Promu"
	profileOn := render(CompanyPage("R", "https://x/", "N", CSSVersion, &co, true, false))
	if !strings.Contains(profileOn, "Promu") {
		t.Error("promoted company: profile page must show the Promu badge")
	}
	profileOff := render(CompanyPage("R", "https://x/", "N", CSSVersion, &co, false, false))
	if strings.Contains(profileOff, "Promu") {
		t.Error("unpromoted company: profile page must not show the Promu badge")
	}

	resultsOn := render(SearchResultsFragment("rawbank",
		[]data.SearchResult{{Company: co}, {Company: data.Company{Name: "Other", NameSeo: "other"}}},
		2, "", map[string]bool{"c1": true}))
	if !strings.Contains(resultsOn, "Promu") {
		t.Error("promoted company: result row must show the Promu badge")
	}
}
