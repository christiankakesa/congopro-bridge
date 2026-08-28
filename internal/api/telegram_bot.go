package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/auth"
	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/telegram"
)

// Telegram bot v2: staff quick actions. The poller (internal/telegram) is
// transport-only and calls HandleUpdate for every update; everything with
// business meaning — authorization, claim resolution, replies — lives here,
// where AppEngine's DB, mailer and existing helpers already are.
//
// Logging in this file follows the telegram house rule: never above Warn,
// always the "[telegram]" prefix. Updates arrive from the outside world,
// and outside input must not be able to page the staff chat through the
// error-forwarding hook.

// telegramBase anchors deep links in bot replies — same constant choice as
// msgCompanyPublished: notifications are a production concern.
const telegramBase = "https://congopro.com"

// TelegramHandler routes bot updates: authorize → act → respond.
type TelegramHandler struct {
	App  *AppEngine
	Resp TelegramResponder
	// ChatID is the configured staff chat. Everything from any other chat
	// is ignored before a single query runs — the bot is not a public
	// surface, and a forwarded button or a DM must do nothing.
	ChatID int64
}

// respondCtx returns a context for the respond phase. Deliberately NOT the
// poller's ctx: a shutdown between the DB commit and the reply must not
// strand a committed decision without its toast/edit. Worst case the edit
// is lost and the next tap resolves to « Déjà traitée ».
func respondCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// HandleUpdate processes one update from the poller.
func (h *TelegramHandler) HandleUpdate(ctx context.Context, u telegram.Update) {
	switch {
	case u.CallbackQuery != nil:
		h.handleCallback(ctx, u.CallbackQuery)
	case u.Message != nil:
		h.handleMessage(ctx, u.Message)
	}
}

func (h *TelegramHandler) handleCallback(ctx context.Context, cq *telegram.CallbackQuery) {
	if cq.Message == nil || cq.Message.Chat.ID != h.ChatID {
		log.Warn().Msgf("[telegram] callback %s from unexpected chat — ignored", cq.ID)
		return
	}

	staff, ok := h.resolveStaff(ctx, cq.From.ID, func(text string) {
		rctx, cancel := respondCtx()
		defer cancel()
		if err := h.Resp.AnswerCallbackQuery(rctx, cq.ID, text, true); err != nil {
			log.Warn().Msgf("[telegram] answer callback: %v", err)
		}
	})
	if !ok {
		return
	}

	action, claimID, parsed := parseClaimCallback(cq.Data)
	if !parsed {
		log.Warn().Msgf("[telegram] unknown callback data %q", cq.Data)
		h.answer(cq.ID, "Action inconnue", false)
		return
	}
	approve := action == "a"

	var (
		claimantEmail, companyName string
		err                        error
	)
	if approve {
		claimantEmail, companyName, err = claims.Approve(ctx, h.App.DB, claimID, staff.ID, "")
	} else {
		claimantEmail, companyName, err = claims.Reject(ctx, h.App.DB, claimID, staff.ID, "")
	}

	switch {
	case errors.Is(err, claims.ErrAlreadyResolved):
		// Double-tap, a second /pending copy, or a redelivered update
		// after restart — all the same: acknowledge, strip the buttons.
		h.answer(cq.ID, "Déjà traitée", false)
		h.edit(cq.Message.MessageID, cq.Message.Text, nil)
		return
	case err != nil:
		log.Warn().Msgf("[telegram] resolve claim %s: %v", claimID, err)
		h.answer(cq.ID, "Erreur — réessayez", false)
		return
	}

	// Same off-request email as the admin UI path (no ctx by design).
	go h.App.sendClaimDecisionEmail(approve, claimantEmail, companyName, "")

	toast, outcome := "Réclamation approuvée", "✅ Approuvée par "
	if !approve {
		toast, outcome = "Réclamation refusée", "❌ Refusée par "
	}
	who := staff.Name
	if who == "" {
		who = staff.Email
	}
	h.answer(cq.ID, toast, false)
	// nil keyboard removes the buttons — the double-tap guard.
	h.edit(cq.Message.MessageID, cq.Message.Text+"\n\n"+outcome+who, nil)
}

func (h *TelegramHandler) handleMessage(ctx context.Context, m *telegram.Message) {
	if m.Chat.ID != h.ChatID {
		return // DMs and other chats: silently nothing
	}
	cmd := strings.Fields(m.Text)
	if len(cmd) == 0 {
		return
	}
	// Group form is "/pending@BotName" — strip the mention.
	name, _, _ := strings.Cut(cmd[0], "@")
	if name != "/pending" && name != "/stats" {
		return // ordinary chatter — the bot stays quiet
	}
	if m.From == nil {
		return
	}

	if _, ok := h.resolveStaff(ctx, m.From.ID, func(text string) {
		h.send(text, telegram.SendOptions{})
	}); !ok {
		return
	}

	switch name {
	case "/pending":
		h.handlePending(ctx)
	case "/stats":
		h.handleStats(ctx)
	}
}

// maxPendingMessages caps how many buttoned claim messages /pending sends —
// the count line carries the full number and the deep link.
const maxPendingMessages = 5

func (h *TelegramHandler) handlePending(ctx context.Context) {
	list, err := claims.ListForAdmin(ctx, h.App.DB, "pending")
	if err != nil {
		log.Warn().Msgf("[telegram] list pending: %v", err)
		h.send("Erreur — réessayez.", telegram.SendOptions{})
		return
	}
	if len(list) == 0 {
		h.send("Aucune réclamation en attente.", telegram.SendOptions{})
		return
	}
	h.send(fmt.Sprintf("%d réclamation(s) en attente — %s/admin/claims", len(list), telegramBase),
		telegram.SendOptions{})
	n := min(maxPendingMessages, len(list))
	for _, cl := range list[:n] {
		h.send(msgPendingClaim(cl), telegram.SendOptions{Keyboard: claimKeyboard(cl.ID)})
	}
}

func (h *TelegramHandler) handleStats(ctx context.Context) {
	d, err := GatherDigest(ctx, h.App.DB, h.App.StripeSubs)
	if err != nil {
		log.Warn().Msgf("[telegram] stats: %v", err)
		h.send("Erreur — statistiques indisponibles.", telegram.SendOptions{})
		return
	}
	h.send(FormatDigest(d), telegram.SendOptions{})
}

// resolveStaff maps a Telegram user to an active staff account. On any
// authorization miss it calls tell with the discovery text (the person's
// numeric id, so an admin can link it) and returns ok=false.
func (h *TelegramHandler) resolveStaff(ctx context.Context, tgID int64, tell func(string)) (*auth.User, bool) {
	staff, err := auth.UserByTelegramID(ctx, h.App.DB, tgID)
	switch {
	case errors.Is(err, auth.ErrUserNotFound) || (err == nil && staff.Status != "active"):
		tell(fmt.Sprintf(
			"Compte non lié — votre identifiant Telegram est %d ; demandez à un admin de le lier (make prod-staff-telegram-link).", tgID))
		return nil, false
	case err != nil:
		log.Warn().Msgf("[telegram] staff lookup for %d: %v", tgID, err)
		tell("Erreur — réessayez.")
		return nil, false
	}
	return staff, true
}

// answer / edit / send are respond-phase helpers on the background ctx.
func (h *TelegramHandler) answer(callbackID, text string, alert bool) {
	ctx, cancel := respondCtx()
	defer cancel()
	if err := h.Resp.AnswerCallbackQuery(ctx, callbackID, text, alert); err != nil {
		log.Warn().Msgf("[telegram] answer callback: %v", err)
	}
}

func (h *TelegramHandler) edit(messageID int64, text string, kb *telegram.InlineKeyboardMarkup) {
	ctx, cancel := respondCtx()
	defer cancel()
	if err := h.Resp.EditMessageText(ctx, messageID, text, kb); err != nil {
		log.Warn().Msgf("[telegram] edit message: %v", err)
	}
}

func (h *TelegramHandler) send(text string, opts telegram.SendOptions) {
	ctx, cancel := respondCtx()
	defer cancel()
	if err := h.Resp.SendMessage(ctx, text, opts); err != nil {
		log.Warn().Msgf("[telegram] send: %v", err)
	}
}

// parseClaimCallback splits "clm:a:<uuid>" → ("a", uuid, true) and
// "clm:r:<uuid>" → ("r", uuid, true); anything else is not ours.
func parseClaimCallback(data string) (action, claimID string, ok bool) {
	rest, found := strings.CutPrefix(data, "clm:")
	if !found {
		return "", "", false
	}
	action, claimID, found = strings.Cut(rest, ":")
	if !found || (action != "a" && action != "r") || claimID == "" {
		return "", "", false
	}
	return action, claimID, true
}

func msgPendingClaim(cl claims.Claim) string {
	return "📋 Réclamation en attente — " + cl.CompanyName +
		"\nPar : " + cl.ClaimantEmail +
		"\nDéposée : " + cl.CreatedAt.Format("02/01/2006")
}
