package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type config struct {
	APIKey                string `toml:"api_key"`
	RequestTimeoutSeconds int    `toml:"request_timeout_seconds"`
	Country               string `toml:"country"`
	SearchLanguage        string `toml:"search_lang"`
	SafeSearch            string `toml:"safesearch"`
}

func loadConfig(executable string) (config, error) {
	cfg := config{RequestTimeoutSeconds: 20, SafeSearch: "moderate"}
	path := os.Getenv("BRAVE_SEARCH_CONFIG")
	explicit := path != ""
	if !explicit {
		path = filepath.Join(filepath.Dir(executable), "config.toml")
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil && (explicit || !os.IsNotExist(err)) {
		// Do not echo TOML parsing errors, which can contain a secret source line.
		return cfg, fmt.Errorf("could not read Brave config %s: check that it exists and contains valid TOML", path)
	}
	if key := os.Getenv("BRAVE_SEARCH_API_KEY"); key != "" {
		cfg.APIKey = key
	}
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.RequestTimeoutSeconds <= 0 || int64(cfg.RequestTimeoutSeconds) > int64((time.Duration(1<<63-1))/time.Second) {
		return cfg, fmt.Errorf("request_timeout_seconds must be positive and fit in a Go duration")
	}
	switch cfg.SafeSearch {
	case "off", "moderate", "strict":
	default:
		return cfg, fmt.Errorf("safesearch must be off, moderate, or strict")
	}
	return cfg, nil
}
