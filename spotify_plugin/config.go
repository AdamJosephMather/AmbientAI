package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type config struct {
	ClientID              string `toml:"client_id"`
	RedirectURI           string `toml:"redirect_uri"`
	TokenFile             string `toml:"token_file"`
	DeviceName            string `toml:"device_name"`
	DeviceID              string `toml:"device_id"`
	SendSongUpdates       bool   `toml:"send_song_updates"`
	PollIntervalSeconds   int    `toml:"poll_interval_seconds"`
	RequestTimeoutSeconds int    `toml:"request_timeout_seconds"`
	DeviceWaitSeconds     int    `toml:"device_wait_seconds"`
	CallTimeoutSeconds    int    `toml:"call_timeout_seconds"`
}

func loadConfig(executable string) (config, error) {
	path := os.Getenv("SPOTIFY_PLUGIN_CONFIG")
	if path == "" {
		path = filepath.Join(filepath.Dir(executable), "config.toml")
	}
	cfg := config{RedirectURI: "http://127.0.0.1:8765/callback", TokenFile: "tokens.json", PollIntervalSeconds: 5, RequestTimeoutSeconds: 10, DeviceWaitSeconds: 25, CallTimeoutSeconds: 50}
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return cfg, fmt.Errorf("cannot read Spotify config %s: create it from config_example.toml and check TOML syntax", path)
	}
	if len(meta.Undecoded()) != 0 {
		return cfg, fmt.Errorf("unknown Spotify config key %s", meta.Undecoded()[0])
	}
	if v := os.Getenv("SPOTIFY_CLIENT_ID"); v != "" {
		cfg.ClientID = v
	}
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.DeviceName = strings.TrimSpace(cfg.DeviceName)
	cfg.DeviceID = strings.TrimSpace(cfg.DeviceID)
	if cfg.ClientID == "" {
		return cfg, fmt.Errorf("set client_id to your own Spotify developer app's client ID")
	}
	u, err := url.Parse(cfg.RedirectURI)
	if err != nil || u.Scheme != "http" || !net.ParseIP(u.Hostname()).IsLoopback() || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" {
		return cfg, fmt.Errorf("redirect_uri must be an HTTP loopback IP URL with an explicit port and callback path, e.g. http://127.0.0.1:8765/callback")
	}
	if cfg.PollIntervalSeconds < 2 || cfg.PollIntervalSeconds > 3600 {
		return cfg, fmt.Errorf("poll_interval_seconds must be between 2 and 3600")
	}
	if cfg.RequestTimeoutSeconds < 1 || cfg.RequestTimeoutSeconds > 60 {
		return cfg, fmt.Errorf("request_timeout_seconds must be between 1 and 60")
	}
	if cfg.DeviceWaitSeconds < 1 || cfg.DeviceWaitSeconds > 120 {
		return cfg, fmt.Errorf("device_wait_seconds must be between 1 and 120")
	}
	if cfg.CallTimeoutSeconds <= cfg.DeviceWaitSeconds || cfg.CallTimeoutSeconds > 300 {
		return cfg, fmt.Errorf("call_timeout_seconds must exceed device_wait_seconds and be at most 300")
	}
	if strings.TrimSpace(cfg.TokenFile) == "" {
		return cfg, fmt.Errorf("token_file must not be empty")
	}
	if !filepath.IsAbs(cfg.TokenFile) {
		cfg.TokenFile = filepath.Join(filepath.Dir(path), cfg.TokenFile)
	}
	return cfg, nil
}
