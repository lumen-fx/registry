package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	t.Setenv("LPM_CONFIG_DIR", t.TempDir())

	// Nothing saved yet: the default registry and no token.
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registry != DefaultRegistry || cfg.Token != "" {
		t.Errorf("empty config = %+v, want the default registry", cfg)
	}

	saved := Config{Registry: "https://registry.example.test", Token: "lpm_secret"}
	if err := SaveConfig(saved); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded != saved {
		t.Errorf("loaded = %+v, want %+v", loaded, saved)
	}

	if err := DeleteConfig(); err != nil {
		t.Fatal(err)
	}
	if err := DeleteConfig(); err != nil {
		t.Errorf("second delete = %v, want nil", err)
	}
	cfg, err = LoadConfig()
	if err != nil || cfg.Token != "" {
		t.Errorf("after delete: cfg = %+v, err = %v, want empty", cfg, err)
	}
}

func TestConfigPathFallsBackToTheUserDir(t *testing.T) {
	t.Setenv("LPM_CONFIG_DIR", "")

	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "config.json" || filepath.Base(filepath.Dir(path)) != "lpm" {
		t.Errorf("path = %q, want .../lpm/config.json", path)
	}
}

func TestConfigErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LPM_CONFIG_DIR", dir)

	// Unparseable file.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(); err == nil {
		t.Error("LoadConfig accepted garbage")
	}

	// A saved config without a registry falls back to the default.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"token":"lpm_x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil || cfg.Registry != DefaultRegistry {
		t.Errorf("cfg = %+v, %v, want the default registry", cfg, err)
	}

	// The config path cannot be created below a file.
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LPM_CONFIG_DIR", filepath.Join(blocked, "sub"))
	if err := SaveConfig(Config{Registry: "x", Token: "y"}); err == nil {
		t.Error("SaveConfig wrote below a file")
	}

	// Deleting fails when config.json is a directory with contents.
	t.Setenv("LPM_CONFIG_DIR", dir)
	if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "config.json", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := DeleteConfig(); err == nil {
		t.Error("DeleteConfig removed a non-empty directory")
	}
}
