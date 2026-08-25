package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"congopro-bridge/internal/mail"
)

type Config struct {
	OllamaURL string
	// OllamaEmbedderURL is the Ollama endpoint Meilisearch should call for
	// embeddings. Usually identical to OllamaURL, except when they live on
	// different sides of a container boundary: with `make dev` the app runs
	// natively (OllamaURL 127.0.0.1:11434, the published port) while
	// Meilisearch runs in docker and must use the compose-internal
	// http://ollama:11434 instead.
	OllamaEmbedderURL   string
	GenerativeModel     string
	EmbeddingModel      string
	AllowedOrigin       string
	OllamaAllowPublicIP bool
	OllamaAllowedHosts  []string
	MeiliURL            string
	MeiliMasterKey      string
	MeiliIndexName      string
	TrustedProxies      []string
	// DatabaseURL is a standard postgres:// connection string. No default —
	// deliberately not baked into defaults() since even a "local dev only"
	// placeholder credential doesn't belong hardcoded in source. Must be set
	// via the DATABASE_URL env var (the Makefile's db-* targets set it for
	// local dev; systemd sets it in production).
	DatabaseURL string

	// SMTP — an all-or-nothing block (see .env.template): SMTP_HOST empty
	// disables email entirely; a set host means a complete, coherent
	// account, enforced at boot by ValidateSMTP.
	SMTPHost     string
	SMTPPort     int
	SMTPPortRaw  string // unparsed SMTP_PORT, kept for a precise boot error
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPFromName string
	SMTPTLSMode  string // starttls | implicit | none (see internal/mail)

	// Stripe — all-or-nothing like SMTP: any key set means all three are
	// required (secret, webhook signing secret, price id for the promote
	// plan); none set disables promoted listings cleanly.
	StripeSecretKey     string
	StripeWebhookSecret string
	StripePriceID       string
}

// StripeEnabled reports whether promoted listings (Stripe) are configured.
func (c *Config) StripeEnabled() bool {
	return c.StripeSecretKey != ""
}

// MailConfig returns the SMTP account for sending, and whether email is
// enabled (SMTP_HOST set). Port defaults to 587 and TLS mode to starttls,
// the usual submission pairing.
func (c *Config) MailConfig() (mail.Config, bool) {
	if c.SMTPHost == "" {
		return mail.Config{}, false
	}
	mode := mail.TLSMode(c.SMTPTLSMode)
	if mode == "" {
		mode = mail.TLSStartTLS
	}
	port := c.SMTPPort
	if port == 0 {
		port = 587
	}
	return mail.Config{
		Host:        c.SMTPHost,
		Port:        port,
		TLSMode:     mode,
		Username:    c.SMTPUsername,
		Password:    c.SMTPPassword,
		FromAddress: c.SMTPFrom,
		FromName:    c.SMTPFromName,
	}, true
}

// ValidateStripe enforces the all-or-nothing Stripe contract at boot:
// any key set requires all three; none set is valid (feature disabled).
func (c *Config) ValidateStripe() error {
	n := 0
	for _, v := range []string{c.StripeSecretKey, c.StripeWebhookSecret, c.StripePriceID} {
		if v != "" {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	if n != 3 {
		return fmt.Errorf("config: Stripe requires STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET and STRIPE_PRICE_ID together (%d of 3 set)", n)
	}
	return nil
}

// ValidateSMTP enforces the all-or-nothing SMTP contract at boot: a
// half-configured account only fails later, when a customer is waiting for
// a code. An empty SMTP_HOST (email disabled) is always valid.
func (c *Config) ValidateSMTP() error {
	if c.SMTPHost == "" {
		return nil
	}
	if c.SMTPPort == 0 && c.SMTPPortRaw != "" {
		return fmt.Errorf("config: SMTP_PORT %q is not a number", c.SMTPPortRaw)
	}
	mc, _ := c.MailConfig()
	return mc.Validate()
}

// unquote strips one layer of matching surrounding quotes. See the SMTP
// block in Load for why this is needed.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// loadDotEnv reads KEY=VALUE pairs from path into the process environment —
// only for keys not already set, so the real environment always wins
// (production sets secrets via systemd/compose, never via this file).
//
// It exists because the Makefile deliberately does NOT include .env: make
// expands `$` sequences inside values, silently corrupting anything with a
// dollar in it (observed here: a 12-char SMTP password reaching the app as
// 4 chars, and the mail server answering a misleading 535). Parsing here is
// literal — first `=` splits, one layer of matching quotes is stripped,
// comments and blanks are skipped, and nothing is ever expanded.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // no .env (e.g. production) — environment only, as before
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); k == "" {
			continue
		}
		v = unquote(strings.TrimSpace(v))
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

func Load() *Config {
	// .env fills in keys the real environment doesn't define (local dev);
	// systemd's EnvironmentFile and docker compose keep precedence. See
	// loadDotEnv for why this lives here and not in the Makefile.
	loadDotEnv(".env")

	cfg := defaults()
	if ou := os.Getenv("OLLAMA_URL"); ou != "" {
		cfg.OllamaURL = ou
	}
	if eu := os.Getenv("OLLAMA_EMBEDDER_URL"); eu != "" {
		cfg.OllamaEmbedderURL = eu
	}
	if gm := os.Getenv("GENERATIVE_MODEL"); gm != "" {
		cfg.GenerativeModel = gm
	}
	if em := os.Getenv("EMBEDDING_MODEL"); em != "" {
		cfg.EmbeddingModel = em
	}
	if ao := os.Getenv("ALLOWED_ORIGIN"); ao != "" {
		cfg.AllowedOrigin = ao
	}
	if os.Getenv("OLLAMA_ALLOW_PUBLIC_IP") == "true" {
		cfg.OllamaAllowPublicIP = true
	}
	if ah := os.Getenv("OLLAMA_ALLOWED_HOSTS"); ah != "" {
		cfg.OllamaAllowedHosts = splitTrimmed(ah, ",")
	}
	if mu := os.Getenv("MEILI_URL"); mu != "" {
		cfg.MeiliURL = mu
	}
	if mk := os.Getenv("MEILI_MASTER_KEY"); mk != "" {
		cfg.MeiliMasterKey = mk
	}
	if mi := os.Getenv("MEILI_INDEX_NAME"); mi != "" {
		cfg.MeiliIndexName = mi
	}
	if tp := os.Getenv("TRUSTED_PROXIES"); tp != "" {
		cfg.TrustedProxies = splitTrimmed(tp, ",")
	}
	if du := os.Getenv("DATABASE_URL"); du != "" {
		cfg.DatabaseURL = du
	}

	// SMTP — all-or-nothing block. Values pass through unquote(): the
	// Makefile's `-include .env` keeps surrounding quotes as literal
	// characters (SMTP_PASSWORD='' would otherwise be the two-char string
	// '' in every make-spawned process), while docker compose and systemd
	// strip them before we ever see the value.
	cfg.SMTPHost = unquote(os.Getenv("SMTP_HOST"))
	if p := unquote(os.Getenv("SMTP_PORT")); p != "" {
		cfg.SMTPPortRaw = p
		if n, err := strconv.Atoi(p); err == nil {
			cfg.SMTPPort = n
		}
	}
	cfg.SMTPUsername = unquote(os.Getenv("SMTP_USERNAME"))
	cfg.SMTPPassword = unquote(os.Getenv("SMTP_PASSWORD"))
	cfg.SMTPFrom = unquote(os.Getenv("SMTP_FROM"))
	cfg.SMTPFromName = unquote(os.Getenv("SMTP_FROM_NAME"))
	cfg.SMTPTLSMode = unquote(os.Getenv("SMTP_TLS"))

	cfg.StripeSecretKey = unquote(os.Getenv("STRIPE_SECRET_KEY"))
	cfg.StripeWebhookSecret = unquote(os.Getenv("STRIPE_WEBHOOK_SECRET"))
	cfg.StripePriceID = unquote(os.Getenv("STRIPE_PRICE_ID"))

	// Separate embedder endpoint only makes sense when explicitly set;
	// otherwise Meilisearch uses the same Ollama as the app itself.
	if cfg.OllamaEmbedderURL == "" {
		cfg.OllamaEmbedderURL = cfg.OllamaURL
	}

	return cfg
}

func defaults() *Config {
	return &Config{
		OllamaURL:       "http://127.0.0.1:11434",
		GenerativeModel: "gemma3:1b",
		EmbeddingModel:  "nomic-embed-text",
		// Empty disables cross-origin access by default (WithCORS then sends no
		// Access-Control-* headers). The shipped frontend only ever calls the API
		// same-origin, so this has no effect on it — only third-party cross-origin
		// consumers need ALLOWED_ORIGIN set explicitly.
		AllowedOrigin:       "",
		OllamaAllowPublicIP: false,
		OllamaAllowedHosts:  nil,
		MeiliURL:            "http://127.0.0.1:7700",
		MeiliMasterKey:      "",
		MeiliIndexName:      "companies",
		// Only these peers are trusted to set X-Forwarded-For/X-Real-IP (e.g. a local
		// reverse proxy like Traefik). Requests from anyone else have their client-supplied
		// forwarding headers ignored, so the rate limiter can't be bypassed by spoofing them.
		TrustedProxies: []string{"127.0.0.1/32", "::1/128"},
	}
}

func splitTrimmed(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
