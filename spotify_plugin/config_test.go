package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDefaultsAndRelativeTokenPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`client_id = "my-app"`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPOTIFY_PLUGIN_CONFIG", path)
	t.Setenv("SPOTIFY_CLIENT_ID", "")
	cfg, err := loadConfig("unused")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SendSongUpdates || cfg.TokenFile != filepath.Join(filepath.Dir(path), "tokens.json") || cfg.CallTimeoutSeconds <= cfg.DeviceWaitSeconds {
		t.Fatalf("bad defaults: %+v", cfg)
	}
	t.Setenv("SPOTIFY_CLIENT_ID", "override")
	cfg, err = loadConfig("unused")
	if err != nil || cfg.ClientID != "override" {
		t.Fatal("client ID override failed")
	}
}

func TestConfigRejectsInvalidSettingsWithoutLeakingSource(t *testing.T) {
	t.Setenv("SPOTIFY_CLIENT_ID", "")
	for _, extra := range []string{
		`redirect_uri = "http://localhost:8765/callback"`,
		`redirect_uri = "http://example.com:8765/callback"`,
		`redirect_uri = "http://127.0.0.1:8765/callback?bad=true"`,
		`poll_interval_seconds = 0`, `device_wait_seconds = -1`,
		`request_timeout_seconds = 999999999`, `call_timeout_seconds = 20`,
		`token_file = ""`, `unexpected = true`, `client_secret = "PRIVATE_VALUE`,
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte("client_id = \"app\"\n"+extra), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SPOTIFY_PLUGIN_CONFIG", path)
		_, err := loadConfig("unused")
		if err == nil || strings.Contains(err.Error(), "PRIVATE_VALUE") {
			t.Fatalf("bad config accepted or leaked: %s: %v", extra, err)
		}
	}
}
