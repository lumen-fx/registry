package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultRegistry = "https://reg.lumenfx.dev"

// Config is what `lpm login` stores: which registry to talk to and the API
// token that authenticates publishing there.
type Config struct {
	Registry string `json:"registry"`
	Token    string `json:"token"`
}

// LPM_CONFIG_DIR overrides the platform default.
func configPath() (string, error) {
	if dir := os.Getenv("LPM_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "config.json"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config dir: %w", err)
	}
	return filepath.Join(dir, "lpm", "config.json"), nil
}

// LoadConfig returns an empty config when none was saved yet.
func LoadConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Config{Registry: DefaultRegistry}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Registry == "" {
		cfg.Registry = DefaultRegistry
	}
	return cfg, nil
}

// SaveConfig writes the file readable by the owner alone; it holds a token.
func SaveConfig(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// DeleteConfig forgets the stored token. A missing file is already deleted.
func DeleteConfig() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove config: %w", err)
	}
	return nil
}
