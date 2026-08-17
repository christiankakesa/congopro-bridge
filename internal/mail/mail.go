// Package mail sends transactional email (OTP codes, account notices).
//
// The transport implements the SMTP_TLS env contract from .env.template:
//
//	starttls — plain TCP, upgraded via STARTTLS before AUTH (OVH EmailPro
//	           submission on port 587, the usual setup).
//	implicit — TLS from the first byte (port 465). Go's net/smtp has no
//	           one-liner for this; the TLS dial is done here.
//	none     — no TLS, and AUTH is refused: credentials are never sent over
//	           an unencrypted link. This mode exists for local capture
//	           (Mailpit on :1025) where no credentials are configured.
//
// AUTH is PLAIN with a LOGIN fallback, negotiated from what the server
// advertises. OVH's submission servers reject AUTH PLAIN with
// "504 5.7.4 Unrecognized authentication type" and only accept LOGIN, so
// both are implemented and credentials are only ever transmitted on an
// encrypted connection.
//
// Deliverability depends on the domain, not this code: SPF, DKIM and DMARC
// must be configured for the sending domain (see docs/BACKEND_PROPOSAL.md,
// "Decisions").
package mail

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// TLSMode selects how (and whether) the connection is encrypted.
type TLSMode string

const (
	TLSStartTLS TLSMode = "starttls"
	TLSImplicit TLSMode = "implicit"
	TLSNone     TLSMode = "none"
)

func (m TLSMode) Valid() bool {
	return m == TLSStartTLS || m == TLSImplicit || m == TLSNone
}

// Config describes one SMTP account. For OVH EmailPro: Host ssl0.ovh.net,
// TLSMode starttls on 587 (or implicit on 465), Username is the FULL mailbox
// address, From must be a mailbox actually owned on the domain or the relay
// rejects the send.
type Config struct {
	Host        string
	Port        int
	TLSMode     TLSMode
	Username    string
	Password    string
	FromAddress string
	FromName    string
	// HeloDomain is sent in the EHLO greeting. Defaults to the domain part
	// of FromAddress. Some relays reject the Go default ("localhost").
	HeloDomain string
}

func (c Config) Validate() error {
	switch {
	case c.Host == "":
		return errors.New("mail: host is required")
	case c.Port <= 0:
		return errors.New("mail: port is required")
	case !c.TLSMode.Valid():
		return fmt.Errorf("mail: TLS mode must be starttls, implicit or none, got %q", c.TLSMode)
	}
	if _, err := mail.ParseAddress(c.FromAddress); err != nil {
		return fmt.Errorf("mail: invalid FromAddress %q: %w", c.FromAddress, err)
	}
	switch c.TLSMode {
	case TLSStartTLS, TLSImplicit:
		// An account that authenticates needs both halves — a password
		// without a username (or vice versa) only fails later, when a
		// customer is waiting for a code.
		if c.Username == "" || c.Password == "" {
			return fmt.Errorf("mail: SMTP_TLS=%s requires both username and password", c.TLSMode)
		}
	case TLSNone:
		// Never send credentials in the clear — this is a no against a
		// misconfigured production server, and "leave them empty" for
		// Mailpit.
		if c.Username != "" || c.Password != "" {
			return errors.New("mail: SMTP_TLS=none sends unencrypted traffic and must not carry credentials — clear SMTP_USERNAME/SMTP_PASSWORD")
		}
	}
	return nil
}

func (c Config) heloDomain() string {
	if c.HeloDomain != "" {
		return c.HeloDomain
	}
	return domainOf(c.FromAddress)
}

func domainOf(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}
	return "localhost"
}

// Message is one transactional email: UTF-8 plain text only. HTML is a
// deliberate non-goal until a feature actually needs it.
type Message struct {
	To      string
	Subject string
	Body    string
}

func (m Message) Validate() error {
	if _, err := mail.ParseAddress(m.To); err != nil {
		// ParseAddress also rejects CR/LF, which kills header injection
		// ("a@b\r\nBcc: ...") before it ever reaches the wire.
		return fmt.Errorf("mail: invalid To %q: %w", m.To, err)
	}
	if strings.TrimSpace(m.Body) == "" {
		return errors.New("mail: body is required")
	}
	return nil
}

// Mailer is the seam the OTP flow will depend on: production wires an
// SMTPSender, tests and local dev wire a capture or log implementation.
type Mailer interface {
	Send(msg Message) error
}

type SMTPSender struct {
	Config Config
}

var _ Mailer = SMTPSender{}

const (
	dialTimeout = 10 * time.Second
	deadline    = 30 * time.Second
)

func (s SMTPSender) Send(msg Message) error {
	if err := s.Config.Validate(); err != nil {
		return err
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	raw, err := buildMessage(s.Config, msg)
	if err != nil {
		return err
	}

	c, err := s.session()
	if err != nil {
		return err
	}
	defer c.Close()

	if s.Config.TLSMode != TLSNone {
		if err := s.authWithFallback(&c); err != nil {
			return err
		}
	}

	if err := c.Mail(s.Config.FromAddress); err != nil {
		return fmt.Errorf("mail: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(msg.To); err != nil {
		return fmt.Errorf("mail: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("mail: write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: close DATA: %w", err)
	}
	return c.Quit()
}

// session dials and brings the connection to the post-TLS, pre-auth state:
// implicit TLS at dial or plain TCP, EHLO, then the STARTTLS upgrade for
// TLSMode=starttls. Also used to redial for the AUTH LOGIN fallback —
// net/smtp aborts AND QUITS the session on any auth failure, so a mechanism
// retry needs a fresh connection.
func (s SMTPSender) session() (*smtp.Client, error) {
	addr := net.JoinHostPort(s.Config.Host, fmt.Sprint(s.Config.Port))
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("mail: dial %s: %w", addr, err)
	}
	if err := conn.SetDeadline(time.Now().Add(deadline)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("mail: set deadline: %w", err)
	}

	// implicit speaks TLS from the first byte; the other modes start plain.
	if s.Config.TLSMode == TLSImplicit {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: s.Config.Host})
		if err := tlsConn.Handshake(); err != nil {
			tlsConn.Close()
			return nil, fmt.Errorf("mail: TLS handshake with %s: %w", addr, err)
		}
		conn = tlsConn
	}

	c, err := smtp.NewClient(conn, s.Config.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mail: SMTP greeting from %s: %w", addr, err)
	}

	if err := c.Hello(s.Config.heloDomain()); err != nil {
		c.Close()
		return nil, fmt.Errorf("mail: EHLO: %w", err)
	}

	if s.Config.TLSMode == TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			c.Close()
			return nil, fmt.Errorf("mail: %s does not advertise STARTTLS, refusing to send credentials in the clear", addr)
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.Config.Host}); err != nil {
			c.Close()
			return nil, fmt.Errorf("mail: STARTTLS with %s: %w", addr, err)
		}
	}
	return c, nil
}

// authWithFallback negotiates the mechanism: PLAIN where the server honors
// it, LOGIN otherwise. OVH's submission servers advertise PLAIN but answer
// it with "504 5.7.4 Unrecognized authentication type" over an established
// TLS session and only accept LOGIN — on that 504 (a mechanism rejection,
// explicitly not a bad-credential 535, so the retry cannot lock the
// account) the session is redialed and LOGIN is used.
func (s SMTPSender) authWithFallback(c **smtp.Client) error {
	_, mechsStr := (*c).Extension("AUTH")
	mechs := strings.FieldsFunc(mechsStr, func(r rune) bool { return r == ' ' || r == ',' })
	has := func(m string) bool {
		for _, x := range mechs {
			if strings.EqualFold(x, m) {
				return true
			}
		}
		return false
	}

	if !has("PLAIN") {
		if has("LOGIN") {
			return s.authLogin(*c)
		}
		return fmt.Errorf("mail: server offers no AUTH PLAIN and no AUTH LOGIN (advertised: %q)", mechsStr)
	}

	err := (*c).Auth(smtp.PlainAuth("", s.Config.Username, s.Config.Password, s.Config.Host))
	if err == nil {
		return nil
	}
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) && tpErr.Code == 504 && has("LOGIN") {
		// The failed session was quit by net/smtp — start a fresh one.
		*c, err = s.session()
		if err != nil {
			return err
		}
		return s.authLogin(*c)
	}
	return fmt.Errorf("mail: AUTH PLAIN (check the mailbox credentials): %w", err)
}

func (s SMTPSender) authLogin(c *smtp.Client) error {
	if err := c.Auth(loginAuth{s.Config.Username, s.Config.Password}); err != nil {
		return fmt.Errorf("mail: AUTH LOGIN (check the mailbox credentials): %w", err)
	}
	return nil
}

// loginAuth implements AUTH LOGIN over net/smtp's Auth interface — the
// standard library ships PLAIN and CRAM-MD5 but not LOGIN, and OVH's
// submission servers only accept LOGIN.
type loginAuth struct {
	username, password string
}

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	// Same rule as smtp.PlainAuth: credentials never leave in the clear.
	if !server.TLS && !isLocalhostName(server.Name) {
		return "", nil, errors.New("mail: unencrypted connection")
	}
	return "LOGIN", nil, nil
}

// Next answers the server's base64-decoded challenges ("Username:",
// "Password:").
func (a loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch c := strings.ToLower(string(challenge)); {
	case strings.Contains(c, "username"):
		return []byte(a.username), nil
	case strings.Contains(c, "password"):
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("mail: unexpected AUTH LOGIN challenge %q", challenge)
	}
}

func isLocalhostName(name string) bool {
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}
