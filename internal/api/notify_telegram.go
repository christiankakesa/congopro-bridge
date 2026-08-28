package api

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/telegram"
)

// Staff notifications to Telegram. Everything here is best-effort by
// design: a notification is worth nothing if it can delay a customer
// response, break a Stripe webhook acknowledgement, or fail a page render.
// Call sites therefore always use `go a.notifyTelegram(...)` and never look
// at the outcome.

// notifyTelegram sends one message to the staff chat. Called on its own
// goroutine with NO request context — same rationale as
// sendClaimDecisionEmail (admin_claims.go): a request-scoped context would
// be cancelled the moment the response is written, killing the send it was
// meant to protect. Failures are logged at Warn with the [telegram] marker
// so the error-forwarding hook never sees them (loop protection).
func (a *AppEngine) notifyTelegram(text string) {
	if a.Telegram == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Telegram.Send(ctx, text); err != nil {
		log.Warn().Msgf("[telegram] notify failed: %v", err)
	}
}

// ── Message builders ─────────────────────────────────────────────────────
// Plain text (the client sends no parse_mode) — user-supplied names and
// subjects need no escaping, and bare URLs auto-link in Telegram.

// TelegramResponder is the api-side seam for the bot's rich operations,
// satisfied by *telegram.Client and faked in tests. Consumer-side on
// purpose: the telegram package stays transport-only.
type TelegramResponder interface {
	SendMessage(ctx context.Context, text string, opts telegram.SendOptions) error
	AnswerCallbackQuery(ctx context.Context, callbackID, text string, showAlert bool) error
	EditMessageText(ctx context.Context, messageID int64, text string, keyboard *telegram.InlineKeyboardMarkup) error
}

// claimKeyboard builds the approve/reject buttons for one claim.
// callback_data is "clm:a:<uuid>" / "clm:r:<uuid>" — 42 bytes, safely
// under Telegram's 64-byte cap.
func claimKeyboard(claimID string) *telegram.InlineKeyboardMarkup {
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{{
		{Text: "✅ Approuver", CallbackData: "clm:a:" + claimID},
		{Text: "❌ Refuser", CallbackData: "clm:r:" + claimID},
	}}}
}

// notifyTelegramNewClaim is the buttoned variant of notifyTelegram for new
// claims: same fire-and-forget contract, falling back to the plain
// Notifier when the rich client isn't wired (v1-only tests, disabled bot).
func (a *AppEngine) notifyTelegramNewClaim(companyName, customerEmail, adminBase, claimID string) {
	if a.TelegramBot == nil {
		a.notifyTelegram(msgNewClaim(companyName, customerEmail, adminBase))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.TelegramBot.SendMessage(ctx, msgNewClaim(companyName, customerEmail, adminBase),
		telegram.SendOptions{Keyboard: claimKeyboard(claimID)}); err != nil {
		log.Warn().Msgf("[telegram] notify new claim failed: %v", err)
	}
}

func msgNewClaim(companyName, customerEmail, adminBase string) string {
	return "📋 Nouvelle réclamation — " + companyName +
		"\nPar : " + customerEmail +
		"\nTraiter : " + adminBase + "/admin/claims"
}

func msgContactMessage(name, subject string) string {
	return "✉️ Message de contact — " + name +
		"\nSujet : " + subject
}

func msgPromotionActivated(companyName string) string {
	return "⭐ Mise en avant activée — " + companyName
}

func msgPromotionPastDue(companyName string) string {
	return "⏳ Paiement en retard — " + companyName
}

func msgPromotionCanceled(companyName string) string {
	return "🚫 Mise en avant résiliée — " + companyName
}

func msgCompanyPublished(companyName, slug string) string {
	return "🏢 Nouvelle entreprise publiée — " + companyName +
		"\nhttps://congopro.com/company/" + slug
}
