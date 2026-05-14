package config

import (
	"os"
)

type Config struct {
	OllamaURL      string
	AiModel        string
	EmbeddingModel string
	AllowedOrigin  string
	ModelsDir      string
}

func Load() *Config {
	cfg := defaults()
	if ou := os.Getenv("OLLAMA_URL"); ou != "" {
		cfg.OllamaURL = ou
	}
	if am := os.Getenv("GENERATIVE_MODEL"); am != "" {
		cfg.AiModel = am
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

	return cfg
}

func defaults() *Config {
	return &Config{
		OllamaURL:      "http://localhost:11434",
		AiModel:        "gemma3:1b",
		EmbeddingModel: "nomic-embed-text",
		AllowedOrigin:  "*",
		ModelsDir:      "models",
	}
}
