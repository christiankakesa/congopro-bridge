package mail

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"mime/quotedprintable"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func testConfig(host string, port int) Config {
	return Config{
		Host:        host,
		Port:        port,
		TLSMode:     TLSStartTLS,
		Username:    "ops@congopro.com",
		Password:    "s3cret",
		FromAddress: "noreply@congopro.com",
		FromName:    "Congopro Bridge",
	}
}

func TestBuildMessage(t *testing.T) {
	raw, err := buildMessage(testConfig("h", 587), Message{
		To:      "client@example.cd",
		Subject: "Votre code de vérification",
		Body:    "Bonjour,\n\nVotre code est 451 290. Il expire dans 10 minutes.\n",
	})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	s := string(raw)

	for _, want := range []string{
		// net/mail quotes display names containing spaces — correct RFC 5322.
		"From: \"Congopro Bridge\" <noreply@congopro.com>\r\n",
		"To: <client@example.cd>\r\n",
		"Subject: =?utf-8?q?", // accented subject must be an encoded-word
		"Message-ID: <",
		"@congopro.com>\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Content-Transfer-Encoding: quoted-printable\r\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("message missing %q\n---\n%s", want, s)
		}
	}

	// Every header must sit before the first blank line; the body starts
	// right after it. (The body itself may contain blank lines — paragraphs —
	// so counting separators overall would be wrong.)
	headerEnd := strings.Index(s, "\r\n\r\n")
	if headerEnd < 0 || !strings.HasPrefix(s[headerEnd+4:], "Bonjour") {
		t.Fatalf("headers/body split not found where expected:\n%s", s)
	}

	// Body roundtrip: bare \n converted to CRLF, then QP-decodes to the
	// original text with accents intact.
	dec, err := quotedPrintableDecode(s[headerEnd+4:])
	if err != nil {
		t.Fatalf("body is not valid quoted-printable: %v", err)
	}
	if want := "Bonjour,\r\n\r\nVotre code est 451 290. Il expire dans 10 minutes.\r\n"; dec != want {
		t.Errorf("decoded body = %q, want %q", dec, want)
	}
}

func TestBuildMessage_MessageIDUniqueness(t *testing.T) {
	cfg := testConfig("h", 587)
	m1, _ := buildMessage(cfg, Message{To: "a@b.co", Subject: "s", Body: "x"})
	m2, _ := buildMessage(cfg, Message{To: "a@b.co", Subject: "s", Body: "x"})
	id := func(m []byte) string {
		s := string(m)
		i := strings.Index(s, "Message-ID: <")
		return s[i : i+40]
	}
	if id(m1) == id(m2) {
		t.Errorf("Message-IDs should differ even for identical messages:\n%s\n%s", m1, m2)
	}
}

func TestMessageValidate_RejectsHeaderInjection(t *testing.T) {
	err := Message{To: "a@b.com\r\nBcc: victim@x.co", Subject: "s", Body: "x"}.Validate()
	if err == nil {
		t.Fatal("To with embedded CRLF must be rejected (header injection)")
	}
	if err := (Message{To: "a@b.com", Body: "  "}).Validate(); err == nil {
		t.Fatal("empty body must be rejected")
	}
}

func TestConfigValidate(t *testing.T) {
	if err := testConfig("ssl0.ovh.net", 587).Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := testConfig("", 587).Validate(); err == nil {
		t.Fatal("missing host must be rejected")
	}
	if err := testConfig("h", 587).withFrom("not-an-address").Validate(); err == nil {
		t.Fatal("invalid FromAddress must be rejected")
	}
	if err := testConfig("h", 587).withMode("wep").Validate(); err == nil {
		t.Fatal("unknown TLS mode must be rejected")
	}

	// starttls/implicit without complete credentials.
	half := testConfig("h", 587)
	half.Password = ""
	if err := half.Validate(); err == nil {
		t.Fatal("starttls without password must be rejected")
	}

	// none must not carry credentials — never send a password in the clear.
	local := testConfig("localhost", 1025).withMode(TLSNone)
	local.Username, local.Password = "", ""
	if err := local.Validate(); err != nil {
		t.Fatalf("none without credentials is the Mailpit setup, must pass: %v", err)
	}
	withCreds := local
	withCreds.Password = "hunter2"
	if err := withCreds.Validate(); err == nil {
		t.Fatal("none with a password must be rejected")
	}
}

func (c Config) withFrom(addr string) Config { c.FromAddress = addr; return c }
func (c Config) withMode(m TLSMode) Config   { c.TLSMode = m; return c }

// ─────────────────────────────────────────────────────────────────────────────
// Fake SMTP server: exercises the real net/smtp client against the exact
// submission sequences — STARTTLS upgrade or implicit TLS, optional AUTH
// PLAIN, then the envelope. Records the command order and payloads for
// assertions.
// ─────────────────────────────────────────────────────────────────────────────

type fakeSMTPServer struct {
	tls            bool   // wrap connections in TLS immediately (implicit mode)
	startTLS       bool   // advertise + accept STARTTLS upgrade
	authMechs      string // what EHLO advertises after AUTH ("" = "PLAIN")
	rejectPlain504 bool   // answer AUTH PLAIN with 504 like OVH does

	loginUser string // credentials received via AUTH LOGIN
	loginPass string

	ln   net.Listener
	cert tls.Certificate

	quitOnce sync.Once

	mu       sync.Mutex
	order    []string
	authArg  string
	fromArg  string
	rcptArg  string
	data     bytes.Buffer
	quitDone chan struct{}
}

func newFakeSMTPServer(t *testing.T, startTLS, implicitTLS bool) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTPServer{
		tls: implicitTLS, startTLS: startTLS,
		ln: ln, cert: selfSignedCert(t), quitDone: make(chan struct{}),
	}
	go s.acceptLoop()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakeSMTPServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeSMTPServer) record(cmd string) {
	s.mu.Lock()
	s.order = append(s.order, cmd)
	s.mu.Unlock()
}

func (s *fakeSMTPServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serve(conn)
	}
}

func (s *fakeSMTPServer) serve(conn net.Conn) {
	defer conn.Close()
	if s.tls {
		tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{s.cert}})
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		conn = tlsConn
	}

	r := bufio.NewReader(conn)
	w := conn
	say := func(msg string) { w.Write([]byte(msg)) }

	say("220 fake ESMTP ready\r\n")
	upgraded := s.tls // already encrypted?
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		verb := strings.ToUpper(fields[0])

		switch verb {
		case "EHLO", "HELO":
			s.record(verb)
			mechs := s.authMechs
			if mechs == "" {
				mechs = "PLAIN"
			}
			if !upgraded && s.startTLS {
				say("250-fake greets you\r\n250-STARTTLS\r\n250 AUTH " + mechs + "\r\n")
			} else {
				// Post-upgrade EHLO: no STARTTLS offer (already encrypted).
				say("250-fake greets you\r\n250 AUTH " + mechs + "\r\n")
			}
		case "STARTTLS":
			s.record("STARTTLS")
			say("220 go ahead\r\n")
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{s.cert}})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn, r, w = tlsConn, bufio.NewReader(tlsConn), tlsConn
			upgraded = true
		case "AUTH":
			s.record("AUTH")
			if strings.HasPrefix(line, "AUTH PLAIN") && s.rejectPlain504 {
				// The exact OVH reply that forces the LOGIN fallback.
				say("504 5.7.4 Unrecognized authentication type\r\n")
				continue
			}
			if strings.HasPrefix(line, "AUTH PLAIN") {
				s.mu.Lock()
				s.authArg = strings.TrimSpace(strings.TrimPrefix(line, "AUTH "))
				s.mu.Unlock()
				say("235 2.7.0 authenticated\r\n")
				continue
			}
			if strings.HasPrefix(line, "AUTH LOGIN") {
				// Interactive challenge/response: "Username:" then "Password:".
				say("334 " + b64("Username:") + "\r\n")
				user, err := r.ReadString('\n')
				if err != nil {
					return
				}
				say("334 " + b64("Password:") + "\r\n")
				pass, err := r.ReadString('\n')
				if err != nil {
					return
				}
				s.mu.Lock()
				s.loginUser = unb64(strings.TrimSpace(user))
				s.loginPass = unb64(strings.TrimSpace(pass))
				s.authArg = "LOGIN"
				s.mu.Unlock()
				say("235 2.7.0 authenticated\r\n")
				continue
			}
			say("504 5.7.4 Unrecognized authentication type\r\n")
		case "MAIL":
			s.record("MAIL")
			s.mu.Lock()
			s.fromArg = strings.TrimSuffix(strings.TrimPrefix(line, "MAIL FROM:<"), ">")
			s.mu.Unlock()
			say("250 ok\r\n")
		case "RCPT":
			s.record("RCPT")
			s.mu.Lock()
			s.rcptArg = strings.TrimSuffix(strings.TrimPrefix(line, "RCPT TO:<"), ">")
			s.mu.Unlock()
			say("250 ok\r\n")
		case "DATA":
			s.record("DATA")
			say("354 end with <CRLF>.<CRLF>\r\n")
			s.mu.Lock()
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					s.mu.Unlock()
					return
				}
				if l == ".\r\n" {
					break
				}
				s.data.WriteString(l)
			}
			s.mu.Unlock()
			say("250 ok queued\r\n")
		case "QUIT":
			s.record("QUIT")
			say("221 bye\r\n")
			s.quitOnce.Do(func() { close(s.quitDone) })
			return
		case "NOOP", "RSET":
			say("250 ok\r\n")
		default:
			say("500 unknown\r\n")
		}
	}
}

func (s *fakeSMTPServer) waitQuit(t *testing.T) {
	t.Helper()
	select {
	case <-s.quitDone:
	case <-time.After(3 * time.Second):
		t.Fatal("client never sent QUIT")
	}
}

// TestSMTPSender_StartTLSThenPlainAuth proves the OVH 587 flow: AUTH PLAIN
// is only sent AFTER the STARTTLS upgrade, and the payload is the standard
// identity\0username\0password encoding.
func TestSMTPSender_StartTLSThenPlainAuth(t *testing.T) {
	srv := newFakeSMTPServer(t, true /*startTLS*/, false)
	cfg := testConfig("127.0.0.1", srv.port())
	err := SMTPSender{Config: cfg}.Send(Message{
		To:      "client@example.cd",
		Subject: "Votre code",
		Body:    "Code: 451 290",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	srv.waitQuit(t)

	srv.mu.Lock()
	defer srv.mu.Unlock()

	iTLS, iAUTH := index(srv.order, "STARTTLS"), index(srv.order, "AUTH")
	if iTLS == -1 || iAUTH == -1 || iTLS > iAUTH {
		t.Fatalf("AUTH must come after STARTTLS, order was %v", srv.order)
	}
	if !strings.HasPrefix(srv.authArg, "PLAIN ") {
		t.Fatalf("expected AUTH PLAIN, got %q", srv.authArg)
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(srv.authArg, "PLAIN "))
	if err != nil {
		t.Fatalf("AUTH PLAIN payload is not base64: %v", err)
	}
	if want := "\x00ops@congopro.com\x00s3cret"; string(payload) != want {
		t.Errorf("AUTH PLAIN payload = %q, want %q", payload, want)
	}
	if srv.fromArg != "noreply@congopro.com" {
		t.Errorf("MAIL FROM = %q", srv.fromArg)
	}
	if srv.rcptArg != "client@example.cd" {
		t.Errorf("RCPT TO = %q", srv.rcptArg)
	}
	if !strings.Contains(srv.data.String(), "Subject:") || !strings.Contains(srv.data.String(), "quoted-printable") {
		t.Errorf("DATA payload missing expected headers:\n%s", srv.data.String())
	}
}

// TestSMTPSender_ImplicitTLS covers SMTP_TLS=implicit: TLS from the first
// byte, STARTTLS never used or offered.
func TestSMTPSender_ImplicitTLS(t *testing.T) {
	srv := newFakeSMTPServer(t, false /*startTLS*/, true /*implicit TLS*/)
	cfg := testConfig("127.0.0.1", srv.port()).withMode(TLSImplicit)

	if err := (SMTPSender{Config: cfg}).Send(Message{To: "x@y.z", Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("Send over implicit TLS: %v", err)
	}
	srv.waitQuit(t)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if index(srv.order, "STARTTLS") != -1 {
		t.Errorf("STARTTLS must not be used on implicit-TLS connections, order %v", srv.order)
	}
	if index(srv.order, "AUTH") == -1 {
		t.Fatal("AUTH PLAIN missing on implicit-TLS connection")
	}
}

// TestSMTPSender_NoneIsLocalCapture covers SMTP_TLS=none (Mailpit): no TLS,
// and crucially no AUTH — nothing is ever sent in the clear.
func TestSMTPSender_NoneIsLocalCapture(t *testing.T) {
	srv := newFakeSMTPServer(t, false /*startTLS*/, false /*implicit TLS*/)
	cfg := testConfig("127.0.0.1", srv.port()).withMode(TLSNone)
	cfg.Username, cfg.Password = "", ""

	if err := (SMTPSender{Config: cfg}).Send(Message{To: "dev@congopro.local", Subject: "capture", Body: "b"}); err != nil {
		t.Fatalf("Send without TLS: %v", err)
	}
	srv.waitQuit(t)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if index(srv.order, "AUTH") != -1 {
		t.Errorf("AUTH must never happen with SMTP_TLS=none, order %v", srv.order)
	}
	if index(srv.order, "STARTTLS") != -1 {
		t.Errorf("STARTTLS must not be attempted with SMTP_TLS=none, order %v", srv.order)
	}
	if !strings.Contains(srv.data.String(), "Subject: capture") {
		t.Errorf("message not delivered to capture server:\n%s", srv.data.String())
	}
}

// TestSMTPSender_StartTLSRequired proves the sender walks away when a
// starttls-mode server offers no STARTTLS — credentials never leave in the
// clear, not even to attempt them.
func TestSMTPSender_StartTLSRequired(t *testing.T) {
	srv := newFakeSMTPServer(t, false /*no STARTTLS offered*/, false)
	cfg := testConfig("127.0.0.1", srv.port())

	err := (SMTPSender{Config: cfg}).Send(Message{To: "x@y.z", Subject: "s", Body: "b"})
	if err == nil || !strings.Contains(err.Error(), "refusing to send credentials") {
		t.Fatalf("expected refusal when server lacks STARTTLS, got: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if index(srv.order, "AUTH") != -1 {
		t.Errorf("AUTH attempted on unencrypted connection, order %v", srv.order)
	}
}

func count(list []string, want string) int {
	n := 0
	for _, v := range list {
		if v == want {
			n++
		}
	}
	return n
}

func b64(s string) string   { return base64.StdEncoding.EncodeToString([]byte(s)) }
func unb64(s string) string { d, _ := base64.StdEncoding.DecodeString(s); return string(d) }

// TestSMTPSender_OVHLoginFallback mirrors the observed OVH behavior: the
// server advertises PLAIN but answers it with 504, and the sender must
// transparently fall back to AUTH LOGIN with the same credentials.
func TestSMTPSender_OVHLoginFallback(t *testing.T) {
	srv := newFakeSMTPServer(t, true /*startTLS*/, false)
	srv.authMechs = "PLAIN LOGIN"
	srv.rejectPlain504 = true

	cfg := testConfig("127.0.0.1", srv.port())
	if err := (SMTPSender{Config: cfg}).Send(Message{To: "x@y.z", Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("Send with LOGIN fallback: %v", err)
	}
	srv.waitQuit(t)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.loginUser != "ops@congopro.com" || srv.loginPass != "s3cret" {
		t.Errorf("LOGIN credentials = %q/%q, want ops@congopro.com/s3cret", srv.loginUser, srv.loginPass)
	}
	// Two full sessions: the first dies at the 504 (net/smtp aborts and
	// QUITs), the second authenticates with LOGIN and completes the send.
	if got := count(srv.order, "AUTH"); got != 2 {
		t.Fatalf("expected exactly 2 AUTH attempts (PLAIN then LOGIN), got %d, order %v", got, srv.order)
	}
	if n := len(srv.order); n < 2 || srv.order[n-2] != "DATA" || srv.order[n-1] != "QUIT" {
		t.Fatalf("second session must complete the send, order %v", srv.order)
	}
}

// TestSMTPSender_LoginOnly: server that never offered PLAIN — LOGIN is
// picked directly from the advertisement.
func TestSMTPSender_LoginOnly(t *testing.T) {
	srv := newFakeSMTPServer(t, true /*startTLS*/, false)
	srv.authMechs = "LOGIN"

	cfg := testConfig("127.0.0.1", srv.port())
	if err := (SMTPSender{Config: cfg}).Send(Message{To: "x@y.z", Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("Send with LOGIN only: %v", err)
	}
	srv.waitQuit(t)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.authArg != "LOGIN" || srv.loginUser != "ops@congopro.com" {
		t.Errorf("expected direct LOGIN auth, got arg=%q user=%q", srv.authArg, srv.loginUser)
	}
}

func index(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}

func quotedPrintableDecode(s string) (string, error) {
	dec, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(s)))
	if err != nil {
		return "", err
	}
	return string(dec), nil
}

// TestMain trusts the self-signed test certificate for the whole process:
// Go's x509 reads SSL_CERT_FILE when building the root pool, so the
// production TLS verification in SMTPSender stays untouched and still
// exercises real chain checking against our fake CA.
func TestMain(m *testing.M) {
	certOnce.Do(func() { testCert, certPEM = mustSelfSignedCert() })
	f, err := os.CreateTemp("", "mailtest-root-*.pem")
	if err != nil {
		panic(err)
	}
	if _, err := f.Write(certPEM); err != nil {
		panic(err)
	}
	f.Close()
	os.Setenv("SSL_CERT_FILE", f.Name())
	code := m.Run()
	os.Remove(f.Name())
	os.Exit(code)
}

var (
	certOnce sync.Once
	testCert tls.Certificate
	certPEM  []byte
)

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	certOnce.Do(func() { testCert, certPEM = mustSelfSignedCert() })
	return testCert
}

func mustSelfSignedCert() (tls.Certificate, []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pemBytes
}
