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
	ModelsDir           string
	OllamaAllowPublicIP bool
	OllamaAllowedHosts  []string
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
	if md := os.Getenv("MODELS_DIR"); md != "" {
		cfg.ModelsDir = md
	}
	if os.Getenv("OLLAMA_ALLOW_PUBLIC_IP") == "true" {
		cfg.OllamaAllowPublicIP = true
	}
	if ah := os.Getenv("OLLAMA_ALLOWED_HOSTS"); ah != "" {
		cfg.OllamaAllowedHosts = splitTrimmed(ah, ",")
	}

	return cfg
}

func defaults() *Config {
	return &Config{
		OllamaURL:           "http://localhost:11434",
		GenerativeModel:     "gemma3:1b",
		EmbeddingModel:      "nomic-embed-text",
		AllowedOrigin:       "*",
		ModelsDir:           "models",
		OllamaAllowPublicIP: false,
		OllamaAllowedHosts:  nil,
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
