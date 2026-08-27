package api

import (
	"fmt"
	"net/http"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/mail"
	"congopro-bridge/internal/web/templates"
)

// Admin claim queue: pending first, staff approve or reject, the claimant
// is notified by email (best effort — a mailer failure never fails the
// admin action).

// GET /admin/claims?status=
func (a *AppEngine) AdminClaimsListHandler(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "pending" && status != "approved" && status != "rejected" {
		status = ""
	}
	list, err := claims.ListForAdmin(r.Context(), a.DB, status)
	if err != nil {
		log.Error().Msgf("[admin] list claims: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templates.AdminClaimsList(nonceFrom(r), a.adminNav(r), status, list).Render(r.Context(), w)
}

// POST /admin/claims/{id}/approve
func (a *AppEngine) AdminClaimApproveHandler(w http.ResponseWriter, r *http.Request) {
	a.resolveClaim(w, r, true)
}

// POST /admin/claims/{id}/reject
func (a *AppEngine) AdminClaimRejectHandler(w http.ResponseWriter, r *http.Request) {
	a.resolveClaim(w, r, false)
}

func (a *AppEngine) resolveClaim(w http.ResponseWriter, r *http.Request, approve bool) {
	id := r.PathValue("id")
	note := ""
	if err := r.ParseForm(); err == nil {
		note = truncate(r.FormValue("note"), 2000)
	}

	var (
		claimantEmail, companyName string
		err                        error
	)
	if approve {
		claimantEmail, companyName, err = claims.Approve(r.Context(), a.DB, id, a.staffUserID(r), note)
	} else {
		claimantEmail, companyName, err = claims.Reject(r.Context(), a.DB, id, a.staffUserID(r), note)
	}
	if err != nil {
		if err == claims.ErrAlreadyResolved {
			// Stale tab / double click — nothing to do, back to the queue.
			if isHTMXRequest(r) {
				w.Header().Set("HX-Redirect", "/admin/claims")
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Redirect(w, r, "/admin/claims", http.StatusSeeOther)
			return
		}
		log.Error().Msgf("[admin] resolve claim: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Fire and forget: the decision is already committed, and OVH SMTP takes
	// ~2-3s to accept a message — long enough to make the admin wait on
	// something the outcome doesn't depend on. Same shape as
	// reloadEngineAsync. The trade is a small window where a process restart
	// between here and the send loses the notification; the claimant still
	// sees the decision on /account, and this was already best-effort (a
	// failed send was only ever logged).
	go a.sendClaimDecisionEmail(approve, claimantEmail, companyName, note)

	flash := "rejected"
	if approve {
		flash = "approved"
	}
	if isHTMXRequest(r) {
		// Swap the pending row for a confirmation strip, refresh the nav
		// badge and push a toast — all in one fragment response.
		pending, cerr := claims.CountPending(r.Context(), a.DB)
		if cerr != nil {
			log.Error().Msgf("[admin] count pending claims: %v", cerr)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		templates.AdminClaimResolvedRow(companyName, approve).Render(r.Context(), w)
		templates.AdminToastOOB(templates.AdminFlashText(flash)).Render(r.Context(), w)
		templates.AdminClaimsBadgeOOB(pending).Render(r.Context(), w)
		return
	}
	http.Redirect(w, r, "/admin/claims?flash="+flash, http.StatusSeeOther)
}

func (a *AppEngine) staffUserID(r *http.Request) string {
	if u := staffUser(r); u != nil {
		return u.ID
	}
	return ""
}

// sendClaimDecisionEmail is best-effort: logged, never fatal. Called on its
// own goroutine (see resolveClaim), so it deliberately takes no context — a
// request-scoped one would be cancelled the moment the response is written,
// which is exactly when this starts running.
func (a *AppEngine) sendClaimDecisionEmail(approve bool, to, companyName, note string) {
	if !a.MailEnabled || a.Mailer == nil || to == "" {
		return
	}
	subject := "Votre réclamation Congopro — " + companyName
	body := fmt.Sprintf(
		"Bonjour,\n\n"+
			"Votre réclamation sur « %s » a été %s.\n"+
			"%s%s"+
			"L'équipe Congopro — https://congopro.com\n",
		companyName,
		map[bool]string{true: "approuvée : l'entreprise est désormais rattachée à votre compte", false: "refusée"}[approve],
		func() string {
			if note == "" {
				return ""
			}
			return "\nNote de l'équipe : " + note + "\n"
		}(),
		// An approval with no next step leaves the owner nowhere: the promote
		// page is otherwise only reachable by typing the URL.
		func() string {
			if !approve {
				return ""
			}
			return "\nVous pouvez maintenant la mettre en avant :\nhttps://congopro.com/account/promote\n"
		}(),
	)
	if err := a.Mailer.Send(mail.Message{To: to, Subject: subject, Body: body}); err != nil {
		log.Error().Msgf("[admin] send claim decision email to %s: %v", to, err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
