// Package ads serves advertising campaigns. Campaigns live in Postgres
// (ads + ads_settings tables) and are edited through /admin/ads; the
// Store keeps an in-memory snapshot loaded at startup and reloaded after
// every admin write, so the request path never touches the database.
//
// The legacy ads.yml stays embedded solely as the one-time import source
// for the -import-ads cutover flag.
package ads

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed ads.yml
var adsYAML []byte

// AdWire is the wire form of one campaign — exactly what the /api/v1/ads
// JSON contract and the htmx fragments render. Deliberately excludes the
// editorial (status, period) and sales (sold_by, customer, price) fields.
type AdWire struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	DisplayURL  string `json:"display_url"`
	Label       string `json:"label"`
	Color       string `json:"color"`
	Weight      int    `json:"weight"`
	Placement   string `json:"placement"`
}

// AdResponse is the JSON body of GET /api/v1/ads. Ads is the raw weighted
// pool (duplicates included); MaxPerPage carries the per-request 75/25 roll
// — both quirks preserved from the pre-CMS contract.
type AdResponse struct {
	Active      bool     `json:"active"`
	RotationSec int      `json:"rotation_sec"`
	MaxPerPage  int      `json:"max_per_page"`
	Ads         []AdWire `json:"ads"`
}

// LabelPresets are the design tokens behind /api/v1/ads-preview-data and
// the admin form's label picker. Static by design: they are styling
// vocabulary, not campaign data. Order matters for the preview gallery
// (the first entry renders as the premium example).
var LabelPresets = []struct {
	Key   string
	Label string
	Color string
}{
	{"home", "Partenaire d'excellence", "#F59E0B"},
	{"sponsored", "Sponsored", "#1a73e8"},
	{"promote", "Promote", "#137333"},
	{"featured", "Featured", "#ea8600"},
	{"recommended", "Recommended", "#9c27b0"},
	{"trending", "Trending", "#e65100"},
	{"verified", "Verified", "#00838f"},
	{"top_choice", "Top Choice", "#d32f2f"},
	{"popular", "Popular", "#8e24aa"},
	{"premium", "Premium", "#5e35b1"},
	{"new", "New", "#2e7d32"},
	{"business", "Business", "#3949ab"},
	{"local", "Local", "#00695c"},
	{"partner", "Partner", "#5d4037"},
	{"official", "Official", "#1565c0"},
	{"hot", "Hot", "#d84315"},
	{"exclusive", "Exclusive", "#ad1457"},
	{"top_rated", "Top Rated", "#1b5e20"},
	{"ad", "Ad", "#5f6368"},
}

// GetAdPreviews builds the /api/v1/ads-preview-data entries — one fake ad
// per label preset. Behavior identical to the YAML x-labels-references era.
func GetAdPreviews() []AdWire {
	previews := make([]AdWire, 0, len(LabelPresets))
	for _, p := range LabelPresets {
		previews = append(previews, AdWire{
			ID:          "preview-" + p.Key,
			Title:       p.Label + " Example Business",
			Description: "This is a placeholder description to showcase the " + p.Label + " style. It demonstrates how standard body copy wraps in the ad slot.",
			URL:         "https://congopro.com",
			DisplayURL:  "congopro.com",
			Label:       p.Label,
			Color:       p.Color,
			Weight:      1,
		})
	}
	return previews
}

// ─────────────────────────────────────────────────────────────────────────────
// Legacy YAML import source (-import-ads). Used once at cutover; the file
// no longer drives serving.
// ─────────────────────────────────────────────────────────────────────────────

type AdPeriod struct {
	Start string `yaml:"start"`
	End   string `yaml:"end"`
}

type legacyAdConfig struct {
	ID          string   `yaml:"id"`
	Active      bool     `yaml:"active"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	URL         string   `yaml:"url"`
	DisplayURL  string   `yaml:"display_url"`
	Label       string   `yaml:"label"`
	Color       string   `yaml:"color"`
	Period      AdPeriod `yaml:"period"`
	Weight      int      `yaml:"weight"`
	Placement   string   `yaml:"placement"`
	Keywords    []string `yaml:"keywords"`
}

type legacyAdsFile struct {
	Active      bool             `yaml:"active"`
	RotationSec int              `yaml:"rotation_sec"`
	MaxPerPage  int              `yaml:"max_per_page"`
	Ads         []legacyAdConfig `yaml:"ads"`
}

// ParseLegacyYAML decodes the embedded ads.yml for the one-time import.
func ParseLegacyYAML() (settings Settings, campaigns []Campaign, err error) {
	var f legacyAdsFile
	if err := yaml.Unmarshal(adsYAML, &f); err != nil {
		return Settings{}, nil, err
	}
	settings = Settings{Active: f.Active, RotationSec: f.RotationSec, MaxPerPage: f.MaxPerPage}
	campaigns = make([]Campaign, 0, len(f.Ads))
	for _, a := range f.Ads {
		status := "draft"
		if a.Active {
			status = "active"
		}
		campaigns = append(campaigns, Campaign{
			ID: a.ID, Title: a.Title, Description: a.Description,
			URL: a.URL, DisplayURL: a.DisplayURL, Label: a.Label, Color: a.Color,
			PeriodStart: a.Period.Start, PeriodEnd: a.Period.End,
			Weight: a.Weight, Placement: a.Placement,
			Keywords: a.Keywords, Status: status,
		})
		// YAML "keywords: []" decodes to a nil slice, which pgx would
		// write as SQL NULL — the column is NOT NULL DEFAULT '{}'.
		if campaigns[len(campaigns)-1].Keywords == nil {
			campaigns[len(campaigns)-1].Keywords = []string{}
		}
	}
	return settings, campaigns, nil
}
