package data

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

//go:embed cleaned_c.json
var CompaniesJSON []byte

// ─────────────────────────────────────────────────────────────────────────────
// MongoDB JSON helpers — only needed to parse the legacy embedded export.
// ─────────────────────────────────────────────────────────────────────────────

type MongoOID struct{ Value string }

func (m *MongoOID) UnmarshalJSON(b []byte) error {
	var w struct {
		OID string `json:"$oid"`
	}
	if err := json.Unmarshal(b, &w); err == nil && w.OID != "" {
		m.Value = w.OID
		return nil
	}
	return json.Unmarshal(b, &m.Value)
}

type MongoDate struct{ Value time.Time }

func (m *MongoDate) UnmarshalJSON(b []byte) error {
	var w struct {
		Date string `json:"$date"`
	}
	if err := json.Unmarshal(b, &w); err == nil && w.Date != "" {
		t, err := time.Parse(time.RFC3339Nano, w.Date)
		if err != nil {
			return fmt.Errorf("MongoDate parse %q: %w", w.Date, err)
		}
		m.Value = t
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("MongoDate fallback parse %q: %w", s, err)
	}
	m.Value = t
	return nil
}

type rawCompany struct {
	ID           MongoOID  `json:"_id"`
	Name         string    `json:"name"`
	NameSeo      string    `json:"name_seo"`
	Activity     string    `json:"activity"`
	City         string    `json:"city"`
	Country      string    `json:"country"`
	Description  string    `json:"description"`
	Slogan       string    `json:"slogan"`
	Website      string    `json:"website"`
	Email        string    `json:"email"`
	MainPhone    string    `json:"main_phone"`
	AddressLine  string    `json:"address_line_1"`
	AddressLine2 string    `json:"address_line_2"`
	Twitter      string    `json:"twitter"`
	Facebook     string    `json:"facebook"`
	LinkedIn     string    `json:"linkedin"`
	Instagram    string    `json:"instagram"`
	TikTok       string    `json:"tiktok"`
	Whatsapp     string    `json:"whatsapp"`
	Youtube      string    `json:"youtube"`
	Published    bool      `json:"published"`
	UpdatedAt    MongoDate `json:"updated_at"`
	StatsShow    int       `json:"stats_show"`
	Geo          []float64 `json:"geo"`
}

// ImportFromEmbeddedJSON parses the legacy embedded MongoDB export and
// upserts every record into Postgres, keyed by the original Mongo hex id.
// Safe to re-run: existing rows are updated in place, never duplicated.
// This is the one-time migration off the JSON file — once it has run against
// production, new companies are created through the admin CMS, not this.
func ImportFromEmbeddedJSON(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var raws []rawCompany
	if err := json.Unmarshal(CompaniesJSON, &raws); err != nil {
		return 0, fmt.Errorf("unmarshal companies: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	seenIDs := make(map[string]struct{}, len(raws))
	imported := 0
	for i, r := range raws {
		if r.Name == "" {
			continue
		}

		id := r.ID.Value
		if id == "" {
			id = fmt.Sprintf("gen-%d", i)
		}
		if _, exists := seenIDs[id]; exists {
			continue
		}
		seenIDs[id] = struct{}{}

		status := "draft"
		if r.Published {
			status = "published"
		}

		var lon, lat any
		if len(r.Geo) == 2 {
			lon, lat = r.Geo[0], r.Geo[1]
		}

		updatedAt := r.UpdatedAt.Value
		if updatedAt.IsZero() {
			updatedAt = time.Now()
		}

		_, err := tx.Exec(ctx, `
			INSERT INTO companies (
				id, name, name_seo, activity, city, country, description, slogan,
				website, email, phone, address_line_1, address_line_2, twitter,
				facebook, linkedin, instagram, tiktok, whatsapp, youtube,
				stats_show, status, location, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
				$16, $17, $18, $19, $20, $21, $22,
				CASE WHEN $23::float8 IS NULL THEN NULL
				     ELSE ST_SetSRID(ST_MakePoint($23::float8, $24::float8), 4326)::geography END,
				$25
			)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				name_seo = EXCLUDED.name_seo,
				activity = EXCLUDED.activity,
				city = EXCLUDED.city,
				country = EXCLUDED.country,
				description = EXCLUDED.description,
				slogan = EXCLUDED.slogan,
				website = EXCLUDED.website,
				email = EXCLUDED.email,
				phone = EXCLUDED.phone,
				address_line_1 = EXCLUDED.address_line_1,
				address_line_2 = EXCLUDED.address_line_2,
				twitter = EXCLUDED.twitter,
				facebook = EXCLUDED.facebook,
				linkedin = EXCLUDED.linkedin,
				instagram = EXCLUDED.instagram,
				tiktok = EXCLUDED.tiktok,
				whatsapp = EXCLUDED.whatsapp,
				youtube = EXCLUDED.youtube,
				stats_show = EXCLUDED.stats_show,
				status = EXCLUDED.status,
				location = EXCLUDED.location,
				updated_at = EXCLUDED.updated_at
		`,
			id, r.Name, r.NameSeo, r.Activity, r.City, r.Country, stripHTML(r.Description), r.Slogan,
			r.Website, r.Email, r.MainPhone, r.AddressLine, r.AddressLine2, r.Twitter,
			r.Facebook, r.LinkedIn, r.Instagram, r.TikTok, r.Whatsapp, r.Youtube,
			r.StatsShow, status, lon, lat, updatedAt,
		)
		if err != nil {
			return imported, fmt.Errorf("upsert company %s: %w", id, err)
		}
		imported++
	}

	if err := tx.Commit(ctx); err != nil {
		return imported, fmt.Errorf("commit transaction: %w", err)
	}

	log.Info().Msgf("[import] upserted %d companies into postgres", imported)
	return imported, nil
}
