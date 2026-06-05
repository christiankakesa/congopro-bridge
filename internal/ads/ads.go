package ads

import (
	"congopro-bridge/internal/util"
	_ "embed"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"gopkg.in/yaml.v3"
)

//go:embed ads.yml
var adsYAML []byte

type AdPeriod struct {
	Start string `yaml:"start"`
	End   string `yaml:"end"`
}

type AdConfig struct {
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

	parsedStart   time.Time `yaml:"-"`
	parsedEnd     time.Time `yaml:"-"`
	lowerKeywords []string  `yaml:"-"`
}

type AdLabelDef struct {
	Label string `yaml:"label"`
	Color string `yaml:"color"`
}

type AdsFile struct {
	Active      bool                    `yaml:"active"`
	RotationSec int                     `yaml:"rotation_sec"`
	MaxPerPage  int                     `yaml:"max_per_page"`
	Ads         []AdConfig              `yaml:"ads"`
	LabelRefs   []map[string]AdLabelDef `yaml:"x-labels-references"`
}

type AdResponse struct {
	Active      bool     `json:"active"`
	RotationSec int      `json:"rotation_sec"`
	MaxPerPage  int      `json:"max_per_page"`
	Ads         []AdWire `json:"ads"`
}

type AdWire struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	DisplayURL  string `json:"display_url"`
	Label       string `json:"label"`
	Color       string `json:"color"`
	Weight      int    `json:"weight"`
}

var AdsConfig AdsFile

const dateLayout = "2006-01-02"

func LoadAds() {
	if err := yaml.Unmarshal(adsYAML, &AdsConfig); err != nil {
		log.Error().Msgf("[ads] failed to parse ads.yaml: %v", err)
		return
	}

	if AdsConfig.RotationSec <= 0 {
		AdsConfig.RotationSec = 8
	}
	if AdsConfig.MaxPerPage <= 0 {
		AdsConfig.MaxPerPage = 2
	}
	if AdsConfig.MaxPerPage > 3 {
		AdsConfig.MaxPerPage = 3 // hard cap
	}

	active := 0

	for i := range AdsConfig.Ads {
		ad := &AdsConfig.Ads[i]

		if ad.Active {
			active++
		}

		if ad.Period.Start != "" {
			if t, err := time.Parse(dateLayout, ad.Period.Start); err == nil {
				ad.parsedStart = t
			} else {
				log.Error().Msgf("[ads] invalid start date for ad %q: %v", ad.ID, err)
			}
		}

		if ad.Period.End != "" {
			if t, err := time.Parse(dateLayout, ad.Period.End); err == nil {
				// adds the entire current day
				ad.parsedEnd = t.Add(24*time.Hour - time.Nanosecond)
			} else {
				log.Error().Msgf("[ads] invalid end date for ad %q: %v", ad.ID, err)
			}
		}

		ad.lowerKeywords = make([]string, len(ad.Keywords))
		for j, kw := range ad.Keywords {
			ad.lowerKeywords[j] = util.TextNormalize(kw)
		}
	}

	log.Info().Msgf("[ads] loaded %d ads (%d active)", len(AdsConfig.Ads), active)
}

func adInPeriod(ad *AdConfig, now time.Time) bool {
	if !ad.parsedStart.IsZero() && now.Before(ad.parsedStart) {
		return false
	}
	if !ad.parsedEnd.IsZero() && now.After(ad.parsedEnd) {
		return false
	}
	return true
}

func adMatchesQuery(ad *AdConfig, q string) bool {
	if len(ad.lowerKeywords) == 0 {
		return true // Pack Notoriété
	}
	if q == "" {
		return false
	}

	normalizedQuery := util.TextNormalize(q)
	queryWords := strings.Fields(normalizedQuery)

	for _, kw := range ad.lowerKeywords {
		for _, word := range queryWords {
			if word == kw || word == kw+"s" || word == kw+"x" {
				return true
			}
		}

		if kw != "" && strings.Contains(normalizedQuery, kw) {
			paddedQuery := " " + normalizedQuery + " "
			paddedKw := " " + kw + " "
			if strings.Contains(paddedQuery, paddedKw) {
				return true
			}
		}
	}

	return false
}

func EligibleAds(q string, now time.Time) []AdWire {
	if !AdsConfig.Active {
		return nil
	}

	q = strings.ToLower(strings.TrimSpace(q))
	isHomepage := q == "" // Easy homepage detection

	keywordMatches := make([]AdWire, 0, 8)
	globalMatches := make([]AdWire, 0, 8)

	for i := range AdsConfig.Ads {
		ad := &AdsConfig.Ads[i]

		if !ad.Active || !adInPeriod(ad, now) {
			continue
		}

		if isHomepage {
			if ad.Placement != "homepage" {
				continue
			}
		} else {
			if ad.Placement == "homepage" {
				continue
			}
		}

		if !adMatchesQuery(ad, q) {
			continue
		}

		w := ad.Weight
		if w <= 0 {
			w = 1
		}

		wire := AdWire{
			ID:          ad.ID,
			Title:       ad.Title,
			Description: ad.Description,
			URL:         ad.URL,
			DisplayURL:  ad.DisplayURL,
			Label:       ad.Label,
			Color:       ad.Color,
			Weight:      w,
		}

		if len(ad.lowerKeywords) > 0 {
			for j := 0; j < w; j++ {
				keywordMatches = append(keywordMatches, wire)
			}
		} else {
			for j := 0; j < w; j++ {
				globalMatches = append(globalMatches, wire)
			}
		}
	}

	if len(keywordMatches) > 0 {
		return keywordMatches
	}

	return globalMatches
}

func GetAdPreviews() []AdWire {
	var previews []AdWire

	for _, labelMap := range AdsConfig.LabelRefs {
		for key, def := range labelMap {
			previews = append(previews, AdWire{
				ID:          "preview-" + key,
				Title:       def.Label + " Example Business",
				Description: "This is a placeholder description to showcase the " + def.Label + " style. It demonstrates how standard body copy wraps in the ad slot.",
				URL:         "https://congopro.com",
				DisplayURL:  "congopro.com",
				Label:       def.Label,
				Color:       def.Color,
				Weight:      1,
			})
		}
	}

	return previews
}
