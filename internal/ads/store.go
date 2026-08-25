package ads

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/util"
)

// Settings mirrors the single ads_settings row.
type Settings struct {
	Active      bool
	RotationSec int
	MaxPerPage  int
}

// Campaign is one row of the ads table, with serving-time precomputation
// (parsed period bounds, normalized keywords) done at load.
type Campaign struct {
	ID          string
	Title       string
	Description string
	URL         string
	DisplayURL  string
	Label       string
	Color       string
	PeriodStart string // YYYY-MM-DD, "" = unbounded
	PeriodEnd   string // inclusive through end of day
	Weight      int
	Placement   string // "" | homepage | search_results
	Keywords    []string
	Status      string // draft | active | paused | expired

	// Sales attribution (display-only for now — never leaves the admin).
	SoldByUserID string
	CustomerID   string
	PriceCents   *int
	Currency     string

	parsedStart   time.Time
	parsedEnd     time.Time
	lowerKeywords []string
}

const dateLayout = "2006-01-02"

// Store is the in-memory serving snapshot: loaded once at startup, swapped
// atomically after every admin write. Reads are lock-free via the pointer;
// Reload builds a fresh snapshot before publishing it.
type Store struct {
	mu        sync.RWMutex
	settings  Settings
	campaigns []Campaign
}

// NewStore loads the initial snapshot from the database. The caller
// decides boot policy on error (main.go logs and continues with ads off —
// the site must not fail because campaigns are unavailable).
func NewStore(ctx context.Context, db *pgxpool.Pool) (*Store, error) {
	s := &Store{}
	if err := s.Reload(ctx, db); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload replaces the snapshot from Postgres. Called at startup and after
// admin writes; concurrent EligibleAds readers keep the old snapshot until
// the swap.
func (s *Store) Reload(ctx context.Context, db *pgxpool.Pool) error {
	settings := Settings{Active: true, RotationSec: 8, MaxPerPage: 2} // YAML-era defaults
	err := db.QueryRow(ctx, `SELECT active, rotation_sec, max_per_page FROM ads_settings WHERE id = 1`).Scan(
		&settings.Active, &settings.RotationSec, &settings.MaxPerPage)
	if err != nil {
		if err == pgx.ErrNoRows {
			// No settings row yet (pre-import): serve with ads off rather
			// than with uncontrolled defaults.
			settings = Settings{Active: false, RotationSec: 8, MaxPerPage: 2}
		} else {
			return err
		}
	}
	if settings.RotationSec <= 0 {
		settings.RotationSec = 8
	}
	if settings.MaxPerPage <= 0 {
		settings.MaxPerPage = 2
	}
	if settings.MaxPerPage > 3 {
		settings.MaxPerPage = 3 // hard cap, as before
	}

	rows, err := db.Query(ctx, `
		SELECT id, title, description, url, display_url, label, color,
		       COALESCE(period_start::text, ''), COALESCE(period_end::text, ''),
		       weight, placement, keywords, status,
		       COALESCE(sold_by_user_id::text, ''), COALESCE(customer_id::text, ''),
		       price_cents, currency
		FROM ads`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var campaigns []Campaign
	active := 0
	for rows.Next() {
		var c Campaign
		if err := rows.Scan(&c.ID, &c.Title, &c.Description, &c.URL, &c.DisplayURL,
			&c.Label, &c.Color, &c.PeriodStart, &c.PeriodEnd,
			&c.Weight, &c.Placement, &c.Keywords, &c.Status,
			&c.SoldByUserID, &c.CustomerID, &c.PriceCents, &c.Currency); err != nil {
			return err
		}
		c.prepare()
		if c.Status == "active" {
			active++
		}
		campaigns = append(campaigns, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	s.settings = settings
	s.campaigns = campaigns
	s.mu.Unlock()
	log.Info().Msgf("[ads] loaded %d campaigns (%d active), system active=%v", len(campaigns), active, settings.Active)
	return nil
}

// prepare mirrors the legacy LoadAds per-ad pass: parse period bounds
// (end extended through the whole day) and normalize keywords.
func (c *Campaign) prepare() {
	c.parsedStart, c.parsedEnd = time.Time{}, time.Time{}
	if c.PeriodStart != "" {
		if t, err := time.Parse(dateLayout, c.PeriodStart); err == nil {
			c.parsedStart = t
		} else {
			log.Error().Msgf("[ads] invalid start date for ad %q: %v", c.ID, err)
		}
	}
	if c.PeriodEnd != "" {
		if t, err := time.Parse(dateLayout, c.PeriodEnd); err == nil {
			// adds the entire current day
			c.parsedEnd = t.Add(24*time.Hour - time.Nanosecond)
		} else {
			log.Error().Msgf("[ads] invalid end date for ad %q: %v", c.ID, err)
		}
	}
	c.lowerKeywords = make([]string, len(c.Keywords))
	for j, kw := range c.Keywords {
		c.lowerKeywords[j] = util.TextNormalize(kw)
	}
}

// Settings returns the current snapshot of global settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// EligibleAds returns the weighted pool for a query — semantics identical
// to the pre-CMS implementation (which lived on the YAML global):
//
//   - master switch off → nil
//   - empty q = homepage: only placement "homepage" ads; otherwise only
//     non-homepage placements (missing placement = search-only)
//   - keyword filter: no keywords = matches everything except the empty
//     query; word match with naive s/x plurals + padded whole-phrase
//     containment on diacritic-stripped lowercase text
//   - keyword-priority: any keyword match suppresses global ads entirely
//   - weight <= 0 treated as 1; weighting via duplication
//
// Randomness stays in the handler, as before.
func (s *Store) EligibleAds(q string, now time.Time) []AdWire {
	s.mu.RLock()
	settings := s.settings
	campaigns := s.campaigns
	s.mu.RUnlock()

	if !settings.Active {
		return nil
	}

	q = strings.ToLower(strings.TrimSpace(q))
	isHomepage := q == "" // Easy homepage detection

	keywordMatches := make([]AdWire, 0, 8)
	globalMatches := make([]AdWire, 0, 8)

	for i := range campaigns {
		ad := &campaigns[i]

		if ad.Status != "active" || !adInPeriod(ad, now) {
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
			Placement:   ad.Placement,
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

func adInPeriod(ad *Campaign, now time.Time) bool {
	if !ad.parsedStart.IsZero() && now.Before(ad.parsedStart) {
		return false
	}
	if !ad.parsedEnd.IsZero() && now.After(ad.parsedEnd) {
		return false
	}
	return true
}

func adMatchesQuery(ad *Campaign, q string) bool {
	if len(ad.lowerKeywords) == 0 {
		return true
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
