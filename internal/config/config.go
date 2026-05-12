package config

import "os"

type Config struct {
	OllamaURL     string
	AiModel       string
	AllowedOrigin string
	ModelsDir     string
}

func Load() *Config {
	cfg := defaults()
	if ou := os.Getenv("OLLAMA_URL"); ou != "" {
		cfg.OllamaURL = ou
	}
	if am := os.Getenv("AI_MODEL"); am != "" {
		cfg.AiModel = am
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
		OllamaURL:     "http://localhost:11434/api/generate",
		AiModel:       "gemma:2b",
		AllowedOrigin: "*",
		ModelsDir:     "models",
	}
}
