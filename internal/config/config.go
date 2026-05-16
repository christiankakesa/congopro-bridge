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

	return cfg
}

func defaults() *Config {
	return &Config{
		OllamaURL:           "http://127.0.0.1:11434",
		GenerativeModel:     "gemma3:1b",
		EmbeddingModel:      "nomic-embed-text",
		AllowedOrigin:       "*",
		OllamaAllowPublicIP: false,
		OllamaAllowedHosts:  nil,
		MeiliURL:            "http://127.0.0.1:7700",
		MeiliMasterKey:      "",
		MeiliIndexName:      "companies",
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
