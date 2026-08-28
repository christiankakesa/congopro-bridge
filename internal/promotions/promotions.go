// Package promotions tracks promoted-listing subscriptions: a customer
// with an approved claim pays via Stripe to promote their company. The
// local rows are the source of truth for everything user-visible; Stripe
// webhooks drive the lifecycle through the Apply* functions, which are
// idempotent by Stripe ids because webhooks replay.
package promotions

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrAlreadyPromoted: this company already has a live promotion
	// (pending checkout, active, or past due).
	ErrAlreadyPromoted = errors.New("cette entreprise est déjà mise en avant")
	// ErrLiveExists is the raw unique-index violation meaning — used by the
	// checkout gate; the friendly form is ErrAlreadyPromoted.
	ErrLiveExists = errors.New("live promotion exists")
)

// Promotion is one row; CompanyName is joined for display.
type Promotion struct {
	ID                   string
	CompanyID            string
	CompanyName          string
	CompanyNameSeo       string
	CustomerID           string
	CustomerEmail        string // joined for the admin revenue view; empty elsewhere
	StripeCustomerID     string
	StripeSubscriptionID string
	StripeSessionID      string
	Status               string
	CurrentPeriodEnd     *time.Time
	CreatedAt            time.Time
}

// EligibleCompany is a company the customer owns (approved claim) and may
// promote right now.
type EligibleCompany struct {
	ID       string
	Name     string
	NameSeo  string
	City     string
	Activity string
}

// CreatePending opens the checkout flow: a promotions row in pending, kept
// unique per company by the partial index. staleSweep removes pending rows
// abandoned for over 24h so an abandoned cart doesn't lock the company.
func CreatePending(ctx context.Context, db *pgxpool.Pool, companyID, customerID, sessionID string) (*Promotion, error) {
	// Opportunistic sweep — cheap, piggybacked, no janitor needed.
	db.Exec(ctx, `DELETE FROM promotions WHERE status = 'pending' AND created_at < now() - interval '24 hours'`)

	var p Promotion
	err := db.QueryRow(ctx, `
		INSERT INTO promotions (company_id, customer_id, stripe_session_id)
		VALUES ($1, $2, $3)
		RETURNING id, company_id, customer_id, stripe_session_id, status`,
		companyID, customerID, sessionID,
	).Scan(&p.ID, &p.CompanyID, &p.CustomerID, &p.StripeSessionID, &p.Status)
	if err != nil {
		if isLiveUnique(err) {
			return nil, ErrAlreadyPromoted
		}
		return nil, err
	}
	return &p, nil
}

// SetSessionID re-points a pending promotion at its real Checkout Session
// once Stripe created it (CreatePending records the promotion id first;
// the session id arrives with the session).
func SetSessionID(ctx context.Context, db *pgxpool.Pool, promotionID, sessionID string) error {
	_, err := db.Exec(ctx, `
		UPDATE promotions SET stripe_session_id = $2, updated_at = now()
		WHERE id = $1 AND status = 'pending'`, promotionID, sessionID)
	return err
}

// EligibleForCustomer lists companies claimed (approved) by the customer
// with no live promotion.
func EligibleForCustomer(ctx context.Context, db *pgxpool.Pool, customerID string) ([]EligibleCompany, error) {
	rows, err := db.Query(ctx, `
		SELECT co.id, co.name, co.name_seo, co.city, co.activity
		FROM companies co
		WHERE co.claimed_by_customer_id::text = $1
		  AND co.status = 'published'
		  AND NOT EXISTS (
		      SELECT 1 FROM promotions p
		      WHERE p.company_id = co.id
		        AND p.status IN ('pending', 'active', 'past_due'))
		ORDER BY co.name`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EligibleCompany
	for rows.Next() {
		var e EligibleCompany
		if err := rows.Scan(&e.ID, &e.Name, &e.NameSeo, &e.City, &e.Activity); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ForCustomer lists the customer's promotions (any status), newest first.
func ForCustomer(ctx context.Context, db *pgxpool.Pool, customerID string) ([]Promotion, error) {
	rows, err := db.Query(ctx, `
		SELECT p.id, p.company_id, co.name, co.name_seo, p.customer_id,
		       p.stripe_customer_id, p.stripe_subscription_id, p.stripe_session_id,
		       p.status, p.current_period_end, p.created_at
		FROM promotions p JOIN companies co ON co.id = p.company_id
		WHERE p.customer_id::text = $1
		ORDER BY p.created_at DESC
		LIMIT 50`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Promotion
	for rows.Next() {
		var p Promotion
		if err := rows.Scan(&p.ID, &p.CompanyID, &p.CompanyName, &p.CompanyNameSeo,
			&p.CustomerID, &p.StripeCustomerID, &p.StripeSubscriptionID, &p.StripeSessionID,
			&p.Status, &p.CurrentPeriodEnd, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AllForAdmin lists every promotion for the revenue page, newest first,
// with company name and customer email joined for display. LIMIT 200 keeps
// the page bounded; refine with filters if the business ever outgrows it.
func AllForAdmin(ctx context.Context, db *pgxpool.Pool) ([]Promotion, error) {
	rows, err := db.Query(ctx, `
		SELECT p.id, p.company_id, co.name, co.name_seo, p.customer_id, cu.email,
		       p.stripe_customer_id, p.stripe_subscription_id, p.stripe_session_id,
		       p.status, p.current_period_end, p.created_at
		FROM promotions p
		JOIN companies co ON co.id = p.company_id
		JOIN customers cu ON cu.id = p.customer_id
		ORDER BY p.created_at DESC
		LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Promotion
	for rows.Next() {
		var p Promotion
		if err := rows.Scan(&p.ID, &p.CompanyID, &p.CompanyName, &p.CompanyNameSeo,
			&p.CustomerID, &p.CustomerEmail, &p.StripeCustomerID, &p.StripeSubscriptionID,
			&p.StripeSessionID, &p.Status, &p.CurrentPeriodEnd, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PromotedCompanyIDs answers "which of these companies are promoted now" —
// one batched, index-backed query; the backbone of badge rendering.
func PromotedCompanyIDs(ctx context.Context, db *pgxpool.Pool, companyIDs []string) (map[string]bool, error) {
	if len(companyIDs) == 0 {
		return map[string]bool{}, nil
	}
	rows, err := db.Query(ctx, `
		SELECT DISTINCT company_id FROM promotions
		WHERE status IN ('active', 'past_due') AND company_id = ANY($1)`, companyIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool, len(companyIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// IsPromoted is the single-company lookup for profile pages.
func IsPromoted(ctx context.Context, db *pgxpool.Pool, companyID string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM promotions
		WHERE company_id = $1 AND status IN ('active', 'past_due'))`, companyID).Scan(&exists)
	return exists, err
}

// ─────────────────────────────────────────────────────────────────────────────
// Webhook lifecycle appliers — idempotent by Stripe ids.
// ─────────────────────────────────────────────────────────────────────────────

// ApplyCheckoutCompleted activates the promotion behind a finished
// checkout session: stamps the Stripe customer + subscription ids and the
// first period end. Safe to replay. activated reports whether a row really
// moved pending→active — RowsAffected 0 on replay or unknown session, so a
// replayed webhook can never re-trigger a notification.
func ApplyCheckoutCompleted(ctx context.Context, db *pgxpool.Pool, sessionID, stripeCustomerID, stripeSubscriptionID string, periodEnd time.Time) (activated bool, err error) {
	tag, err := db.Exec(ctx, `
		UPDATE promotions SET
			stripe_customer_id = $2,
			stripe_subscription_id = $3,
			status = 'active',
			current_period_end = $4,
			updated_at = now()
		WHERE stripe_session_id = $1 AND status = 'pending'`,
		sessionID, stripeCustomerID, stripeSubscriptionID, periodEnd)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// MapSubscriptionStatus converts a Stripe subscription status to ours.
func MapSubscriptionStatus(stripeStatus string) string {
	switch stripeStatus {
	case "active", "trialing":
		return "active"
	case "past_due", "unpaid":
		return "past_due"
	default: // canceled, incomplete, incomplete_expired, unpaid…
		return "canceled"
	}
}

// ApplySubscriptionUpdated syncs status and period end from Stripe.
// Idempotent; unknown subscription ids are ignored (e.g. subscriptions not
// created through this flow). oldStatus/newStatus report the transition for
// notifications — both empty when no row matched, equal when nothing moved.
func ApplySubscriptionUpdated(ctx context.Context, db *pgxpool.Pool, stripeSubscriptionID, stripeStatus string, periodEnd time.Time) (oldStatus, newStatus string, err error) {
	status := MapSubscriptionStatus(stripeStatus)
	// UPDATE … FROM a self-select captures the pre-update status in the
	// same statement — no second query, no race with a concurrent event.
	err = db.QueryRow(ctx, `
		UPDATE promotions p SET
			status = $2,
			current_period_end = $3,
			updated_at = now()
		FROM (SELECT id, status AS old FROM promotions WHERE stripe_subscription_id = $1) prev
		WHERE p.id = prev.id
		RETURNING prev.old, p.status`,
		stripeSubscriptionID, status, periodEnd).Scan(&oldStatus, &newStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil // unknown subscription — not ours, nothing to do
	}
	return oldStatus, newStatus, err
}

// ApplySubscriptionDeleted cancels. Idempotent; canceled reports whether a
// row really transitioned (the status guard makes RowsAffected a true
// signal, so replays never re-notify).
func ApplySubscriptionDeleted(ctx context.Context, db *pgxpool.Pool, stripeSubscriptionID string) (canceled bool, err error) {
	tag, err := db.Exec(ctx, `
		UPDATE promotions SET status = 'canceled', updated_at = now()
		WHERE stripe_subscription_id = $1 AND status <> 'canceled'`,
		stripeSubscriptionID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func isLiveUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		strings.Contains(pgErr.ConstraintName, "promotions_one_live_per_company")
}

// CancelPending releases the per-company slot when checkout creation
// fails or the customer abandons deliberately. Idempotent.
func CancelPending(ctx context.Context, db *pgxpool.Pool, promotionID string) error {
	_, err := db.Exec(ctx, `
		UPDATE promotions SET status = 'canceled', updated_at = now()
		WHERE id = $1 AND status = 'pending'`, promotionID)
	return err
}
