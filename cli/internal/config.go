package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DefaultRegistry = "https://reg.lumenfx.dev"

// Config is the credentials a command runs on: which registry to talk to and
// the API token that authenticates publishing there. `lpm login` saves one.
type Config struct {
	Registry string `json:"registry"`
	Token    string `json:"token"`

	// Whether a config file was read at all. `lpm login` records the registry
	// it signed in to, and that choice outranks the environment.
	saved bool
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

// EnvToken is the token LPM_TOKEN supplies, or "" when it names none. It is
// trimmed, because a secret handed to a CI job often arrives with a newline
// on the end.
func EnvToken() string {
	return strings.TrimSpace(os.Getenv("LPM_TOKEN"))
}

// LoadConfig returns the saved registry and token, falling back to the
// default registry and to LPM_TOKEN for whatever the file does not supply.
// Every command that needs credentials reads them here, so LPM_TOKEN
// authenticates all of them: a CI job holds the secret and cannot run an
// interactive login.
//
// A token saved by `lpm login` outranks LPM_TOKEN, the way a saved registry
// outranks LPM_REGISTRY.
func LoadConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{Registry: DefaultRegistry}
	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		// Nothing saved, so the default registry and the environment stand
		// on their own.
	case err != nil:
		return Config{}, fmt.Errorf("read config: %w", err)
	default:
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", path, err)
		}
		if cfg.Registry == "" {
			cfg.Registry = DefaultRegistry
		}
		cfg.saved = true
	}

	if cfg.Token == "" {
		cfg.Token = EnvToken()
	}
	return cfg, nil
}

// ResolveRegistry picks the registry to talk to: what --registry names, then
// what `lpm login` saved, then LPM_REGISTRY, then the default.
func ResolveRegistry(flag string) (string, error) {
	if flag != "" {
		return strings.TrimSuffix(flag, "/"), nil
	}

	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}
	if cfg.saved && cfg.Registry != "" {
		return strings.TrimSuffix(cfg.Registry, "/"), nil
	}
	if env := os.Getenv("LPM_REGISTRY"); env != "" {
		return strings.TrimSuffix(env, "/"), nil
	}
	return DefaultRegistry, nil
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
