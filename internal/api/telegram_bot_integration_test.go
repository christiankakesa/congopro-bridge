//go:build integration

// Telegram bot v2 handler tests — run via `make dev-test-integration`.
// Updates are hand-built and fed straight to HandleUpdate (the poller is
// transport-only and has its own tests); the responder and mailer are
// fakes, so no network and no Telegram account are needed.
package api

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/customers"
	"congopro-bridge/internal/mail"
	"congopro-bridge/internal/telegram"
)

const botTestChatID int64 = -10042

// chanMailer delivers each message on a channel so the test can wait on
// the decision-email goroutine with a timeout. (Unique name — shared
// package across tagged and untagged test files.)
type chanMailer struct {
	sent chan mail.Message
}

func (m *chanMailer) Send(msg mail.Message) error {
	m.sent <- msg
	return nil
}

type botFixture struct {
	handler   *TelegramHandler
	resp      *fakeResponder
	mailer    *chanMailer
	pool      *pgxpool.Pool
	claimID   string
	companyID string
	tgID      int64
}

// newBotFixture: a published company, a customer with a pending claim on
// it, and a staff row linked to a Telegram id.
func newBotFixture(t *testing.T) *botFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("DATABASE_URL not set — run via make dev-test-integration")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()

	suffix := time.Now().Format("150405.000000000")
	email := "bot-cust-" + suffix + "@test.congopro.local"
	cust, err := customers.CreateOrGetByEmail(ctx, pool, email)
	if err != nil {
		t.Fatal(err)
	}
	companyID := "bot-co-" + suffix
	if _, err := pool.Exec(ctx,
		`INSERT INTO companies (id, name, name_seo, status) VALUES ($1, 'Bot SARL', $1, 'published')`,
		companyID); err != nil {
		t.Fatal(err)
	}
	claimID, err := claims.Submit(ctx, pool, companyID, cust.ID, cust.Email,
		"+243900000009", claims.RelationshipOwner, "Je suis le fondateur de cette entreprise, test bot v2.")
	if err != nil {
		t.Fatal(err)
	}

	tgID := time.Now().UnixNano() % 1_000_000_000 // unique enough per test run
	var staffID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, name, role, status, password_hash, totp_secret, telegram_user_id)
		 VALUES ($1, 'Bot Staff', 'support', 'active', 'x', 'x', $2) RETURNING id`,
		"bot-staff-"+suffix+"@test.congopro.local", tgID).Scan(&staffID); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM company_claims WHERE company_id = $1`, companyID)
		pool.Exec(ctx, `UPDATE companies SET claimed_by_customer_id = NULL WHERE id = $1`, companyID)
		pool.Exec(ctx, `DELETE FROM companies WHERE id = $1`, companyID)
		pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, cust.ID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, staffID)
	})

	resp := &fakeResponder{}
	mailer := &chanMailer{sent: make(chan mail.Message, 4)}
	app := &AppEngine{DB: pool, Mailer: mailer, MailEnabled: true}
	return &botFixture{
		handler:   &TelegramHandler{App: app, Resp: resp, ChatID: botTestChatID},
		resp:      resp,
		mailer:    mailer,
		pool:      pool,
		claimID:   claimID,
		companyID: companyID,
		tgID:      tgID,
	}
}

func (f *botFixture) callback(fromID int64, data string) telegram.Update {
	return telegram.Update{CallbackQuery: &telegram.CallbackQuery{
		ID:      "cb-test",
		From:    telegram.User{ID: fromID},
		Data:    data,
		Message: &telegram.Message{MessageID: 7, Chat: telegram.Chat{ID: botTestChatID}, Text: "📋 Nouvelle réclamation — Bot SARL"},
	}}
}

func (f *botFixture) claimStatus(t *testing.T) string {
	t.Helper()
	var status string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT status FROM company_claims WHERE id = $1`, f.claimID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestBotApproveViaCallback(t *testing.T) {
	f := newBotFixture(t)

	f.handler.HandleUpdate(context.Background(), f.callback(f.tgID, "clm:a:"+f.claimID))

	if got := f.claimStatus(t); got != "approved" {
		t.Fatalf("claim status = %q, want approved", got)
	}
	if len(f.resp.answers) != 1 || f.resp.answers[0] != "Réclamation approuvée" {
		t.Errorf("answers = %v", f.resp.answers)
	}
	if len(f.resp.edits) != 1 ||
		!strings.Contains(f.resp.edits[0], "✅ Approuvée par Bot Staff") ||
		f.resp.editKBs[0] != nil || f.resp.editIDs[0] != 7 {
		t.Errorf("edit = %+v kb=%v id=%v", f.resp.edits, f.resp.editKBs, f.resp.editIDs)
	}
	// The decision email goes out on its own goroutine.
	select {
	case msg := <-f.mailer.sent:
		if !strings.Contains(msg.Subject, "Bot SARL") {
			t.Errorf("email subject = %q", msg.Subject)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("decision email never sent")
	}

	// Double-tap (same update redelivered): acknowledged as already
	// resolved, keyboard still stripped, no second email.
	f.handler.HandleUpdate(context.Background(), f.callback(f.tgID, "clm:a:"+f.claimID))
	if got := f.resp.answers[len(f.resp.answers)-1]; got != "Déjà traitée" {
		t.Errorf("double-tap answer = %q", got)
	}
	if kb := f.resp.editKBs[len(f.resp.editKBs)-1]; kb != nil {
		t.Error("double-tap must still strip the keyboard")
	}
	select {
	case <-f.mailer.sent:
		t.Fatal("double-tap sent a second decision email")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestBotRejectViaCallback(t *testing.T) {
	f := newBotFixture(t)

	f.handler.HandleUpdate(context.Background(), f.callback(f.tgID, "clm:r:"+f.claimID))

	if got := f.claimStatus(t); got != "rejected" {
		t.Fatalf("claim status = %q, want rejected", got)
	}
	if !strings.Contains(f.resp.edits[0], "❌ Refusée par Bot Staff") {
		t.Errorf("edit = %q", f.resp.edits[0])
	}
	select {
	case <-f.mailer.sent:
	case <-time.After(3 * time.Second):
		t.Fatal("decision email never sent")
	}
}

func TestBotUnlinkedUserGetsDiscoveryAlert(t *testing.T) {
	f := newBotFixture(t)
	stranger := f.tgID + 1 // no users row

	f.handler.HandleUpdate(context.Background(), f.callback(stranger, "clm:a:"+f.claimID))

	if got := f.claimStatus(t); got != "pending" {
		t.Fatalf("claim status = %q — an unlinked tap must change nothing", got)
	}
	if len(f.resp.answers) != 1 || !f.resp.alerts[0] {
		t.Fatalf("want one modal alert, got answers=%v alerts=%v", f.resp.answers, f.resp.alerts)
	}
	// The alert must carry the numeric id — it's how linking happens.
	if !strings.Contains(f.resp.answers[0], "Compte non lié") ||
		!strings.Contains(f.resp.answers[0], strconv.FormatInt(stranger, 10)) {
		t.Errorf("alert = %q, must contain the id %d", f.resp.answers[0], stranger)
	}
}

func TestBotPendingCommand(t *testing.T) {
	f := newBotFixture(t) // fixture has exactly one pending claim of ours

	f.handler.HandleUpdate(context.Background(), telegram.Update{Message: &telegram.Message{
		MessageID: 9,
		From:      &telegram.User{ID: f.tgID},
		Chat:      telegram.Chat{ID: botTestChatID},
		Text:      "/pending@CongoproBot",
	}})

	if len(f.resp.sends) < 2 {
		t.Fatalf("sends = %v — want a count line plus claim message(s)", f.resp.sends)
	}
	if !strings.Contains(f.resp.sends[0], "réclamation(s) en attente") ||
		!strings.Contains(f.resp.sends[0], "/admin/claims") {
		t.Errorf("count line = %q", f.resp.sends[0])
	}
	// Our claim appears somewhere in the buttoned messages with its keyboard.
	found := false
	for i, msg := range f.resp.sends[1:] {
		if strings.Contains(msg, "Bot SARL") {
			found = true
			kb := f.resp.sendOpts[i+1].Keyboard
			if kb == nil || kb.InlineKeyboard[0][0].CallbackData != "clm:a:"+f.claimID {
				t.Errorf("claim message keyboard = %+v", kb)
			}
		}
	}
	if !found {
		t.Error("fixture claim not listed by /pending")
	}
}

func TestBotStatsCommand(t *testing.T) {
	f := newBotFixture(t)

	f.handler.HandleUpdate(context.Background(), telegram.Update{Message: &telegram.Message{
		MessageID: 9,
		From:      &telegram.User{ID: f.tgID},
		Chat:      telegram.Chat{ID: botTestChatID},
		Text:      "/stats",
	}})

	if len(f.resp.sends) != 1 {
		t.Fatalf("sends = %v", f.resp.sends)
	}
	msg := f.resp.sends[0]
	if !strings.Contains(msg, "📊 Congopro — bilan du") ||
		!strings.Contains(msg, "MRR : indisponible") { // nil StripeSubs on the fixture
		t.Errorf("stats = %q", msg)
	}
}
