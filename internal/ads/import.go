package ads

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// ImportLegacyYAML is the one-time cutover path: decode the embedded
// ads.yml and upsert every campaign plus the settings row. Idempotent —
// re-running updates existing rows in place (same convention as the
// companies -import). Sales attribution fields are left NULL (house ads).
func ImportLegacyYAML(ctx context.Context, db *pgxpool.Pool) (int, error) {
	settings, campaigns, err := ParseLegacyYAML()
	if err != nil {
		return 0, err
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Seed the settings row only — an admin may have tuned it since; import
	// must not clobber live settings on a re-run.
	if _, err := tx.Exec(ctx, `
		INSERT INTO ads_settings (id, active, rotation_sec, max_per_page)
		VALUES (1, $1, $2, $3) ON CONFLICT (id) DO NOTHING`,
		settings.Active, settings.RotationSec, settings.MaxPerPage); err != nil {
		return 0, err
	}

	for _, c := range campaigns {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ads (id, title, description, url, display_url, label, color,
			                 period_start, period_end, weight, placement, keywords, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,
			        NULLIF($8,'')::date, NULLIF($9,'')::date,
			        $10,$11,$12,$13)
			ON CONFLICT (id) DO UPDATE SET
				title=EXCLUDED.title, description=EXCLUDED.description,
				url=EXCLUDED.url, display_url=EXCLUDED.display_url,
				label=EXCLUDED.label, color=EXCLUDED.color,
				period_start=EXCLUDED.period_start, period_end=EXCLUDED.period_end,
				weight=EXCLUDED.weight, placement=EXCLUDED.placement,
				keywords=EXCLUDED.keywords, status=EXCLUDED.status,
				updated_at=now()`,
			c.ID, c.Title, c.Description, c.URL, c.DisplayURL, c.Label, c.Color,
			c.PeriodStart, c.PeriodEnd, c.Weight, c.Placement, c.Keywords, c.Status); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	log.Info().Msgf("[ads] legacy import: %d campaigns, settings seeded", len(campaigns))
	return len(campaigns), nil
}

// DisabledStore is the fallback when the initial DB load fails at startup:
// ads explicitly off, no campaigns. The site runs; ad slots render their
// inactive variants.
func DisabledStore() *Store {
	return &Store{settings: Settings{Active: false, RotationSec: 8, MaxPerPage: 2}}
}
