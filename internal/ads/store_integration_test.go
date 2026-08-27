//go:build integration

// Ads CMS DB roundtrip — run via `make dev-test-integration`.
package ads

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func adsPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Fatal("DATABASE_URL not set — run via make dev-test-integration")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// The legacy import is the real cutover path: idempotent upsert of the 17
// campaigns + settings seeding.
func TestAds_ImportLegacyYAML(t *testing.T) {
	pool := adsPool(t)
	n, err := ImportLegacyYAML(context.Background(), pool)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 16 {
		t.Fatalf("imported %d campaigns, want the 16 legacy ones", n)
	}
	// Idempotent re-run.
	if _, err := ImportLegacyYAML(context.Background(), pool); err != nil {
		t.Fatalf("re-import: %v", err)
	}
}

func TestAds_StoreLoadAndReload(t *testing.T) {
	ctx := context.Background()
	pool := adsPool(t)

	if _, err := ImportLegacyYAML(ctx, pool); err != nil {
		t.Fatalf("import: %v", err)
	}
	store, err := NewStore(ctx, pool)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Imported settings are live: master switch on, YAML rotation (18s).
	st := store.Settings()
	if !st.Active || st.RotationSec != 18 || st.MaxPerPage != 2 {
		t.Fatalf("settings after import = %+v", st)
	}

	// A campaign inserted after startup is invisible until Reload…
	const id = "itest-ad-campaign"
	_, err = pool.Exec(ctx, `
		INSERT INTO ads (id, title, url, placement, keywords, status)
		VALUES ($1, 'Test Campaign', 'https://congopro.com', 'homepage', '{}', 'active')
		ON CONFLICT (id) DO UPDATE SET status = 'active', updated_at = now()`, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM ads WHERE id = $1`, id) })

	if got := ids(store.EligibleAds("", time.Now())); got[id] != 0 {
		t.Fatal("campaign visible before any reload — snapshot leak")
	}

	// …and visible right after one (the admin write path).
	if err := store.Reload(ctx, pool); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := ids(store.EligibleAds("", time.Now())); got[id] == 0 {
		t.Fatal("campaign missing after reload")
	}

	// Pausing it via SQL + reload removes it — the no-redeploy kill switch
	// at campaign level.
	if _, err := pool.Exec(ctx, `UPDATE ads SET status = 'paused' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := store.Reload(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if got := ids(store.EligibleAds("", time.Now())); got[id] != 0 {
		t.Fatal("paused campaign still serving")
	}

	// The master switch: settings row off → nothing serves, instantly.
	if _, err := pool.Exec(ctx, `UPDATE ads_settings SET active = false WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `UPDATE ads_settings SET active = true WHERE id = 1`) })
	if err := store.Reload(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if got := store.EligibleAds("banque", time.Now()); got != nil {
		t.Fatalf("master switch off must serve nothing, got %d ads", len(got))
	}
}

func TestAds_NoSettingsRowServesOff(t *testing.T) {
	// A fresh database (migrations applied, no import yet) must serve ads
	// OFF, not with rogue defaults.
	ctx := context.Background()
	pool := adsPool(t)

	// Remove the settings row for the duration of the test; the cleanup
	// restores the exact saved values (the CHECK(id=1) constraint rules out
	// renaming tricks).
	var active bool
	var rotation, maxPerPage int
	if err := pool.QueryRow(ctx,
		`SELECT active, rotation_sec, max_per_page FROM ads_settings WHERE id = 1`).Scan(&active, &rotation, &maxPerPage); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM ads_settings WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `INSERT INTO ads_settings (id, active, rotation_sec, max_per_page) VALUES (1, $1, $2, $3)`, active, rotation, maxPerPage)
	})

	store, err := NewStore(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if store.Settings().Active {
		t.Fatal("missing settings row must default to ads OFF")
	}
}
