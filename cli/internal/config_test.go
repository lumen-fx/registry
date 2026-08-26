package internal

import "testing"

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
