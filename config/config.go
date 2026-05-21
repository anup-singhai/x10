package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	AnthropicKey string `json:"anthropic_key,omitempty"`
	OpenAIKey    string `json:"openai_key,omitempty"`
	GroqKey      string `json:"groq_key,omitempty"`
	TogetherKey  string `json:"together_key,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
	OllamaURL    string `json:"ollama_url,omitempty"`   // default: http://localhost:11434/v1
	LMStudioURL  string `json:"lmstudio_url,omitempty"` // default: http://localhost:1234/v1
}

func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".x10")
}

func path() string {
	return filepath.Join(Dir(), "config.json")
}

func Load() (*Config, error) {
	cfg := &Config{
		AnthropicKey: os.Getenv("ANTHROPIC_API_KEY"),
		OpenAIKey:    os.Getenv("OPENAI_API_KEY"),
		GroqKey:      os.Getenv("GROQ_API_KEY"),
		TogetherKey:  os.Getenv("TOGETHER_API_KEY"),
		DefaultModel: "claude-sonnet-4-6",
	}

	data, err := os.ReadFile(path())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	var fileCfg Config
	if err := json.Unmarshal(data, &fileCfg); err != nil {
		return nil, err
	}

	if cfg.AnthropicKey == "" {
		cfg.AnthropicKey = fileCfg.AnthropicKey
	}
	if cfg.OpenAIKey == "" {
		cfg.OpenAIKey = fileCfg.OpenAIKey
	}
	if cfg.GroqKey == "" {
		cfg.GroqKey = fileCfg.GroqKey
	}
	if cfg.TogetherKey == "" {
		cfg.TogetherKey = fileCfg.TogetherKey
	}
	if fileCfg.OllamaURL != "" {
		cfg.OllamaURL = fileCfg.OllamaURL
	}
	if fileCfg.LMStudioURL != "" {
		cfg.LMStudioURL = fileCfg.LMStudioURL
	}
	if fileCfg.DefaultModel != "" {
		cfg.DefaultModel = fileCfg.DefaultModel
	}

	return cfg, nil
}

func Set(key, value string) error {
	p := path()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}

	data, _ := os.ReadFile(p)
	var cfg Config
	json.Unmarshal(data, &cfg)

	switch key {
	case "anthropic-key":
		cfg.AnthropicKey = value
	case "openai-key":
		cfg.OpenAIKey = value
	case "default-model":
		cfg.DefaultModel = value
	case "groq-key":
		cfg.GroqKey = value
	case "together-key":
		cfg.TogetherKey = value
	case "ollama-url":
		cfg.OllamaURL = value
	case "lmstudio-url":
		cfg.LMStudioURL = value
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, out, 0600)
}
