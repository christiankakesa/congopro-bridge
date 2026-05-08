package ads

import (
	_ "embed"
	"log"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ─────────────────────────────────────────────
// Embed ads.yml at compile time
// ─────────────────────────────────────────────

//go:embed ads.yml
var adsYAML []byte

// ─────────────────────────────────────────────
// YAML model & Internal State
// ─────────────────────────────────────────────

// AdPeriod defines optional start/end date bounds for an ad.
type AdPeriod struct {
	Start string `yaml:"start"` // "YYYY-MM-DD" or empty
	End   string `yaml:"end"`   // "YYYY-MM-DD" or empty
}

// AdConfig is one advertisement entry in ads.yaml.
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
	Keywords    []string `yaml:"keywords"`

	// Champs internes (ignorés par YAML) pour le cache de performance.
	// Calculés une seule fois au démarrage pour soulager le CPU et le GC.
	parsedStart   time.Time `yaml:"-"`
	parsedEnd     time.Time `yaml:"-"`
	lowerKeywords []string  `yaml:"-"`
}

// AdsFile is the top-level ads.yaml structure.
type AdsFile struct {
	Active      bool       `yaml:"active"`
	RotationSec int        `yaml:"rotation_sec"`
	MaxPerPage  int        `yaml:"max_per_page"`
	Ads         []AdConfig `yaml:"ads"`
}

// ─────────────────────────────────────────────
// Wire-format sent to the frontend
// ─────────────────────────────────────────────

// AdResponse is what the /ads endpoint returns.
type AdResponse struct {
	Active      bool     `json:"active"`
	RotationSec int      `json:"rotation_sec"`
	MaxPerPage  int      `json:"max_per_page"`
	Ads         []AdWire `json:"ads"`
}

// AdWire is a single ad in the JSON response.
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

// ─────────────────────────────────────────────
// Loader
// ─────────────────────────────────────────────

var AdsConfig AdsFile

const dateLayout = "2006-01-02"

func LoadAds() {
	if err := yaml.Unmarshal(adsYAML, &AdsConfig); err != nil {
		log.Printf("[ads] failed to parse ads.yaml: %v", err)
		return
	}

	// Defaults
	if AdsConfig.RotationSec <= 0 {
		AdsConfig.RotationSec = 8
	}
	if AdsConfig.MaxPerPage <= 0 {
		AdsConfig.MaxPerPage = 2
	}
	if AdsConfig.MaxPerPage > 5 {
		AdsConfig.MaxPerPage = 5 // hard cap — don't overwhelm users
	}

	active := 0

	// Pré-calcul (Parsing des dates et des strings) pour éviter de le faire à chaque requête HTTP
	for i := range AdsConfig.Ads {
		ad := &AdsConfig.Ads[i] // Utilisation d'un pointeur pour modifier la structure en place

		if ad.Active {
			active++
		}

		// Pré-parsing des dates
		if ad.Period.Start != "" {
			if t, err := time.Parse(dateLayout, ad.Period.Start); err == nil {
				ad.parsedStart = t
			} else {
				log.Printf("[ads] invalid start date for ad %q: %v", ad.ID, err)
			}
		}

		if ad.Period.End != "" {
			if t, err := time.Parse(dateLayout, ad.Period.End); err == nil {
				// On étend la fin de la période jusqu'à la dernière seconde de la journée
				ad.parsedEnd = t.Add(24*time.Hour - time.Nanosecond)
			} else {
				log.Printf("[ads] invalid end date for ad %q: %v", ad.ID, err)
			}
		}

		// Pré-lowercasing des mots-clés
		ad.lowerKeywords = make([]string, len(ad.Keywords))
		for j, kw := range ad.Keywords {
			ad.lowerKeywords[j] = strings.ToLower(strings.TrimSpace(kw))
		}
	}

	log.Printf("[ads] loaded %d ads (%d active)", len(AdsConfig.Ads), active)
}

// ─────────────────────────────────────────────
// Filtering helpers
// ─────────────────────────────────────────────

// adInPeriod utilise les dates pré-calculées (Ultra-rapide)
func adInPeriod(ad *AdConfig, now time.Time) bool {
	if !ad.parsedStart.IsZero() && now.Before(ad.parsedStart) {
		return false
	}
	if !ad.parsedEnd.IsZero() && now.After(ad.parsedEnd) {
		return false
	}
	return true
}

// adMatchesQuery utilise les mots-clés pré-calculés (Zéro allocation)
func adMatchesQuery(ad *AdConfig, q string) bool {
	if len(ad.lowerKeywords) == 0 {
		return true // no keyword filter → show on all queries
	}
	if q == "" {
		return false // keyword-targeted ads don't show on empty query
	}

	// 'q' a déjà été trim et mis en minuscules dans EligibleAds
	for _, kw := range ad.lowerKeywords {
		if strings.Contains(q, kw) {
			return true
		}
	}
	return false
}

// EligibleAds returns ads that pass all filters for the given query and time.
// The result is already expanded by weight for weighted-round-robin rotation.
func EligibleAds(q string, now time.Time) []AdWire {
	if !AdsConfig.Active {
		return nil
	}

	// Normalisation de la requête UNE SEULE FOIS pour toutes les vérifications
	q = strings.ToLower(strings.TrimSpace(q))

	// Optimisation : pré-allouer le slice avec une capacité estimée
	// (Moins de redimensionnements par le Garbage Collector)
	result := make([]AdWire, 0, 8)

	for i := range AdsConfig.Ads {
		ad := &AdsConfig.Ads[i] // Évite la copie lourde de la structure

		if !ad.Active {
			continue
		}
		if !adInPeriod(ad, now) {
			continue
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

		// Expand by weight so the frontend can simply rotate through the slice.
		for j := 0; j < w; j++ {
			result = append(result, wire)
		}
	}
	return result
}
