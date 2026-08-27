package api

import (
	"fmt"
	"net/http"
	"net/mail"
	"strings"

	"github.com/rs/zerolog/log"

	mailer "congopro-bridge/internal/mail"
	"congopro-bridge/internal/web/templates"
)

// Public contact form. Two rules keep an unauthenticated "send us mail"
// endpoint from becoming a spam relay: the recipient is always the
// configured ContactTo (never anything from the request), and the visitor's
// address only ever appears inside the body — it is never used as a header,
// so there is nothing to inject into.

const (
	contactMaxName    = 200
	contactMaxSubject = 200
	contactMaxMessage = 5000
)

// GET /contact
func (a *AppEngine) ContactPageHandler(w http.ResponseWriter, r *http.Request) {
	a.renderContact(w, r, templates.ContactForm{}, nil, false, http.StatusOK)
}

// POST /contact
func (a *AppEngine) ContactSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	form := templates.ContactForm{
		Name:    truncate(strings.TrimSpace(r.FormValue("name")), contactMaxName),
		Email:   truncate(strings.TrimSpace(r.FormValue("email")), 320),
		Subject: truncate(strings.TrimSpace(r.FormValue("subject")), contactMaxSubject),
		Message: truncate(strings.TrimSpace(r.FormValue("message")), contactMaxMessage),
	}

	// Honeypot: a bot fills every field it finds. Answer exactly like a
	// success so it learns nothing, but send nothing.
	if strings.TrimSpace(r.FormValue("website_url")) != "" {
		log.Info().Msg("[contact] honeypot triggered — discarded")
		a.renderContact(w, r, templates.ContactForm{}, nil, true, http.StatusOK)
		return
	}

	errs := map[string]string{}
	if form.Name == "" {
		errs["name"] = "Votre nom est obligatoire."
	}
	if form.Email == "" {
		errs["email"] = "Votre e-mail est obligatoire."
	} else if _, err := mail.ParseAddress(form.Email); err != nil {
		errs["email"] = "Adresse e-mail invalide."
	}
	if form.Message == "" {
		errs["message"] = "Le message est obligatoire."
	} else if len([]rune(form.Message)) < 10 {
		errs["message"] = "Votre message est trop court — donnez-nous un peu de contexte."
	}
	if len(errs) > 0 {
		a.renderContact(w, r, form, errs, false, http.StatusUnprocessableEntity)
		return
	}

	if !a.MailEnabled || a.Mailer == nil {
		a.renderContact(w, r, form, map[string]string{
			"message": "L'envoi est momentanément indisponible — écrivez-nous directement par e-mail ou Telegram.",
		}, false, http.StatusServiceUnavailable)
		return
	}

	subject := form.Subject
	if subject == "" {
		subject = "(sans sujet)"
	}
	body := fmt.Sprintf(
		"Nouveau message depuis le formulaire de contact congopro.com\n\n"+
			"Nom     : %s\n"+
			"E-mail  : %s\n"+
			"Sujet   : %s\n\n"+
			"--- Message ---\n%s\n",
		form.Name, form.Email, subject, form.Message,
	)
	if err := a.Mailer.Send(mailer.Message{
		To:      a.ContactTo,
		Subject: "[Contact] " + subject,
		Body:    body,
	}); err != nil {
		log.Error().Msgf("[contact] send to %s: %v", a.ContactTo, err)
		a.renderContact(w, r, form, map[string]string{
			"message": "L'envoi a échoué — réessayez, ou écrivez-nous par e-mail ou Telegram.",
		}, false, http.StatusBadGateway)
		return
	}

	a.renderContact(w, r, templates.ContactForm{}, nil, true, http.StatusOK)
}

func (a *AppEngine) renderContact(w http.ResponseWriter, r *http.Request, form templates.ContactForm, fieldErrors map[string]string, sent bool, status int) {
	nonce := nonceFrom(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := templates.ContactPage(
		canonicalURL(r), nonce, cssHash,
		form, fieldErrors, sent, a.MailEnabled && a.Mailer != nil,
	).Render(r.Context(), w); err != nil {
		log.Error().Msgf("[templates] render contact page: %v", err)
	}
}
