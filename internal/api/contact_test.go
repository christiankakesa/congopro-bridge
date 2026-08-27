package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"congopro-bridge/internal/constants"
	"congopro-bridge/internal/mail"
)

type captureMailer struct {
	sent []mail.Message
	err  error
}

func (c *captureMailer) Send(m mail.Message) error {
	if c.err != nil {
		return c.err
	}
	c.sent = append(c.sent, m)
	return nil
}

func postContact(t *testing.T, a *AppEngine, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "https://congopro.com/contact", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), constants.NonceKey, "N"))
	w := httptest.NewRecorder()
	a.ContactSubmitHandler(w, r)
	return w
}

func validForm() url.Values {
    return url.Values{
        "name":    {"Christian"},
        "email":   {"someone@example.cd"},
        "subject": {"Correction de fiche"},
        "message": {"Bonjour, une information est erronée sur ma fiche."},
    }
}

func TestContact_SendsToConfiguredAddressOnly(t *testing.T) {
	m := &captureMailer{}
	a := &AppEngine{Mailer: m, MailEnabled: true, ContactTo: "ask@congopro.com"}

	form := validForm()
	form.Set("to", "attacker@evil.com") // must be ignored entirely
	w := postContact(t, a, form)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(m.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(m.sent))
	}
	if m.sent[0].To != "ask@congopro.com" {
		t.Errorf("To = %q — recipient must never come from the request", m.sent[0].To)
	}
	if !strings.Contains(m.sent[0].Body, "someone@example.cd") {
		t.Error("submitter address should appear in the body so staff can reply")
	}
	if !strings.Contains(w.Body.String(), "Message envoyé") {
		t.Error("success state not rendered")
	}
}

func TestContact_HoneypotSilentlyDiscards(t *testing.T) {
	m := &captureMailer{}
	a := &AppEngine{Mailer: m, MailEnabled: true, ContactTo: "ask@congopro.com"}

	form := validForm()
	form.Set("website_url", "http://spam.example")
	w := postContact(t, a, form)

	if len(m.sent) != 0 {
		t.Error("honeypot submission must not send mail")
	}
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Message envoyé") {
		t.Error("honeypot must look identical to success so bots learn nothing")
	}
}

func TestContact_ValidationKeepsInputAndFlagsFields(t *testing.T) {
	m := &captureMailer{}
	a := &AppEngine{Mailer: m, MailEnabled: true, ContactTo: "ask@congopro.com"}

	form := url.Values{"name": {""}, "email": {"not-an-email"}, "message": {"court"}}
	w := postContact(t, a, form)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", w.Code)
	}
	if len(m.sent) != 0 {
		t.Error("invalid submission must not send mail")
	}
	body := w.Body.String()
	for _, want := range []string{"Votre nom est obligatoire.", "Adresse e-mail invalide.", "trop court", "not-an-email"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q (typed values must survive a validation error)", want)
		}
	}
}

func TestContact_MailDisabledHidesFormInsteadOfFailingSilently(t *testing.T) {
	a := &AppEngine{MailEnabled: false, ContactTo: "ask@congopro.com"}
	r := httptest.NewRequest(http.MethodGet, "https://congopro.com/contact", nil)
	r = r.WithContext(context.WithValue(r.Context(), constants.NonceKey, "N"))
	w := httptest.NewRecorder()

	a.ContactPageHandler(w, r)

	body := w.Body.String()
	if strings.Contains(body, `action="/contact"`) {
		t.Error("form should be hidden when mail is disabled")
	}
	if !strings.Contains(body, "t.me/") {
		t.Error("Telegram must still be offered when the form is unavailable")
	}
	// The support mailbox must never be printed on this indexed page —
	// publishing it hands the address to harvesters, which none of the
	// form's spam defences would mitigate.
	if strings.Contains(body, "@congopro.com") || strings.Contains(body, "mailto:") {
		t.Error("support address must not be published on the contact page")
	}
}
