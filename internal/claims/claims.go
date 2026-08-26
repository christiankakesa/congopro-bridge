// Package claims implements the company claim/dispute workflow: a customer
// (verified email) claims a company, staff approve or reject in an admin
// queue, approval durably links the company to its owner. Concurrency is
// arbitrated by partial unique indexes in Postgres — one pending claim and
// one approved claim per company, ever — this package just translates the
// constraint violations into friendly errors.
package claims

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	RelationshipOwner    = "owner"
	RelationshipManager  = "manager"
	RelationshipEmployee = "employee"
	RelationshipOther    = "other"
)

var (
	// ErrAlreadyPending: someone else's claim on this company awaits review.
	ErrAlreadyPending = errors.New("une réclamation est déjà en cours pour cette entreprise")
	// ErrAlreadyClaimed: this company already has an approved owner.
	ErrAlreadyClaimed = errors.New("cette entreprise est déjà revendiquée")
	// ErrAlreadyResolved: the claim was handled between render and submit
	// (admin double-click, stale tab).
	ErrAlreadyResolved = errors.New("réclamation déjà traitée")

	// User-input validation errors — messages are French and safe to render.
	ErrInvalidRelationship = errors.New("relation invalide")
	ErrEvidenceTooShort    = errors.New("décrivez votre lien avec cette entreprise (au moins 20 caractères)")
	ErrEvidenceTooLong     = errors.New("justification trop longue (4000 caractères maximum)")
	ErrCompanyNotFound     = errors.New("entreprise introuvable")
)

// IsUserError reports whether err is one of the user-input errors whose
// message is safe to show in the form (as opposed to infrastructure errors,
// which get a generic 500 message).
func IsUserError(err error) bool {
	switch err {
	case ErrAlreadyPending, ErrAlreadyClaimed, ErrInvalidRelationship,
		ErrEvidenceTooShort, ErrEvidenceTooLong, ErrCompanyNotFound:
		return true
	}
	return false
}

// ValidRelationships backs the form's select options.
var ValidRelationships = []string{RelationshipOwner, RelationshipManager, RelationshipEmployee, RelationshipOther}

// Claim is one row; CompanyName/ResolvedByEmail are joined for display.
type Claim struct {
	ID             string
	CompanyID      string
	CompanyName    string
	CompanyNameSeo string
	CustomerID     string
	ClaimantEmail  string
	ContactPhone   string
	Relationship   string
	Evidence       string
	Status         string
	AdminNote      string
	CreatedAt      time.Time
	ResolvedAt     *time.Time
}

// ValidateRelationship guards the CHECK constraint from producing 500s.
func ValidateRelationship(r string) bool {
	for _, v := range ValidRelationships {
		if v == r {
			return true
		}
	}
	return false
}

func isUniqueViolation(err error, index string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		strings.Contains(pgErr.ConstraintName, index)
}

// Submit records a new pending claim for companyID by customerID. Friendly
// errors come both from pre-checks and from the unique indexes (the latter
// is what actually wins races).
func Submit(ctx context.Context, db *pgxpool.Pool, companyID, customerID, claimantEmail, phone, relationship, evidence string) error {
	if !ValidateRelationship(relationship) {
		return ErrInvalidRelationship
	}
	evidence = strings.TrimSpace(evidence)
	if len(evidence) < 20 {
		return ErrEvidenceTooShort
	}
	if len(evidence) > 4000 {
		return ErrEvidenceTooLong
	}

	var claimed bool
	if err := db.QueryRow(ctx,
		`SELECT claimed_by_customer_id IS NOT NULL FROM companies WHERE id = $1`, companyID,
	).Scan(&claimed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCompanyNotFound
		}
		return err
	}
	if claimed {
		return ErrAlreadyClaimed
	}

	_, err := db.Exec(ctx, `
		INSERT INTO company_claims (company_id, customer_id, claimant_email, contact_phone, relationship, evidence)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		companyID, customerID, claimantEmail,
		strings.TrimSpace(phone), relationship, evidence)
	switch {
	case err == nil:
		return nil
	case isUniqueViolation(err, "one_pending_per_company"):
		return ErrAlreadyPending
	case isUniqueViolation(err, "one_approved_per_company"):
		return ErrAlreadyClaimed
	default:
		return err
	}
}

const listColumns = `
	c.id, c.company_id, co.name, co.name_seo, c.customer_id, c.claimant_email,
	c.contact_phone, c.relationship, c.evidence, c.status, c.admin_note,
	c.created_at, c.resolved_at`

func scanClaims(rows pgx.Rows) ([]Claim, error) {
	defer rows.Close()
	var out []Claim
	for rows.Next() {
		var cl Claim
		if err := rows.Scan(&cl.ID, &cl.CompanyID, &cl.CompanyName, &cl.CompanyNameSeo,
			&cl.CustomerID, &cl.ClaimantEmail, &cl.ContactPhone, &cl.Relationship,
			&cl.Evidence, &cl.Status, &cl.AdminNote, &cl.CreatedAt, &cl.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, cl)
	}
	return out, rows.Err()
}

// ListByCustomer backs the dashboard's "Mes réclamations".
func ListByCustomer(ctx context.Context, db *pgxpool.Pool, customerID string) ([]Claim, error) {
	rows, err := db.Query(ctx, `
		SELECT `+listColumns+`
		FROM company_claims c JOIN companies co ON co.id = c.company_id
		WHERE c.customer_id = $1
		ORDER BY c.created_at DESC
		LIMIT 50`, customerID)
	if err != nil {
		return nil, err
	}
	return scanClaims(rows)
}

// ListForAdmin backs the admin queue: pending first, newest first, optional
// status filter.
func ListForAdmin(ctx context.Context, db *pgxpool.Pool, status string) ([]Claim, error) {
	q := `
		SELECT ` + listColumns + `
		FROM company_claims c JOIN companies co ON co.id = c.company_id
		WHERE ($1 = '' OR c.status = $1)
		ORDER BY (c.status = 'pending') DESC, c.created_at DESC
		LIMIT 200`
	rows, err := db.Query(ctx, q, status)
	if err != nil {
		return nil, err
	}
	return scanClaims(rows)
}

// Approve resolves a pending claim and links the company to its owner, in
// one transaction. Returns the claimant's email + company name so the
// caller can send the decision email.
func Approve(ctx context.Context, db *pgxpool.Pool, claimID, staffUserID, note string) (claimantEmail, companyName string, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)

	var customerID string
	err = tx.QueryRow(ctx, `
		UPDATE company_claims
		SET status = 'approved', admin_note = $3, resolved_by = $2, resolved_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING customer_id`, claimID, staffUserID, note).Scan(&customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrAlreadyResolved
		}
		return "", "", err
	}

	if err := tx.QueryRow(ctx, `
		SELECT c.claimant_email, co.name
		FROM company_claims c JOIN companies co ON co.id = c.company_id
		WHERE c.id = $1`, claimID).Scan(&claimantEmail, &companyName); err != nil {
		return "", "", err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE companies SET claimed_by_customer_id = $2
		WHERE id = (SELECT company_id FROM company_claims WHERE id = $1)`,
		claimID, customerID); err != nil {
		return "", "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return claimantEmail, companyName, nil
}

// Reject resolves a pending claim without touching the company.
func Reject(ctx context.Context, db *pgxpool.Pool, claimID, staffUserID, note string) (claimantEmail, companyName string, err error) {
	var claimant string
	err = db.QueryRow(ctx, `
		UPDATE company_claims
		SET status = 'rejected', admin_note = $3, resolved_by = $2, resolved_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING claimant_email`, claimID, staffUserID, note,
	).Scan(&claimant)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrAlreadyResolved
		}
		return "", "", err
	}
	if err := db.QueryRow(ctx, `
		SELECT co.name FROM company_claims c
		JOIN companies co ON co.id = c.company_id WHERE c.id = $1`, claimID,
	).Scan(&companyName); err != nil {
		return "", "", err
	}
	return claimant, companyName, nil
}

// CompanyState is what the claim page needs to render the right variant:
// the form, "already claimed", or "review in progress".
type CompanyState struct {
	Claimed     bool // an approved claim exists (may be this customer's)
	ClaimedByMe bool
	Pending     bool // a claim awaits review (may be this customer's)
	PendingByMe bool
}

// StateForCompany resolves the current claim state for the claim page.
func StateForCompany(ctx context.Context, db *pgxpool.Pool, companyID, customerID string) (CompanyState, error) {
	var st CompanyState
	err := db.QueryRow(ctx, `
		SELECT
			claimed_by_customer_id IS NOT NULL,
			-- COALESCE: NULL::text = $2 is NULL (three-valued logic), not
			-- false — without the coalesce every unclaimed company fails
			-- the scan instead of rendering the claim form.
			COALESCE(claimed_by_customer_id::text = $2, false),
			EXISTS (SELECT 1 FROM company_claims WHERE company_id = $1 AND status = 'pending'),
			EXISTS (SELECT 1 FROM company_claims WHERE company_id = $1 AND status = 'pending' AND customer_id::text = $2)
		FROM companies WHERE id = $1`, companyID, customerID,
	).Scan(&st.Claimed, &st.ClaimedByMe, &st.Pending, &st.PendingByMe)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, ErrCompanyNotFound
	}
	return st, err
}

// CountPending returns the number of claims awaiting a decision — feeds the
// admin nav badge and the dashboard tile.
func CountPending(ctx context.Context, db *pgxpool.Pool) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM company_claims WHERE status = 'pending'`).Scan(&n)
	return n, err
}

// IsClaimed reports whether a company has an approved owner — the signal
// behind the public "Fiche vérifiée" badge.
func IsClaimed(ctx context.Context, db *pgxpool.Pool, companyID string) (bool, error) {
	var claimed bool
	err := db.QueryRow(ctx,
		`SELECT claimed_by_customer_id IS NOT NULL FROM companies WHERE id = $1`,
		companyID,
	).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return claimed, err
}
