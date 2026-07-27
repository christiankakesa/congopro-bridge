package config

import (
	"os"
	"strings"
)

type Config struct {
	OllamaURL           string
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
}

func Load() *Config {
	cfg := defaults()
	if ou := os.Getenv("OLLAMA_URL"); ou != "" {
		cfg.OllamaURL = ou
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
