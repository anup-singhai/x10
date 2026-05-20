package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	AnthropicKey string `json:"anthropic_key,omitempty"`
	OpenAIKey    string `json:"openai_key,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
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
		DefaultModel: "claude-haiku-4-5-20251001",
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
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, out, 0600)
}
