package main

import (
	"AmbientAI/openrouter"
	"AmbientAI/ui"
	"errors"
	"fmt"
	"github.com/BurntSushi/toml"
	"net/http"
	"time"
)

type VADConfig struct {
	Path string `toml:"path"`
}

type AIConfig struct {
	AIName   string `toml:"ai_name"`
	UserName string `toml:"user_name"`

	ModelFast           string `toml:"model_fast"`
	ModelFastReasoning  string `toml:"model_fast_reasoning"`
	ModelSmart          string `toml:"model_smart"`
	ModelSmartReasoning string `toml:"model_smart_reasoning"`
}

type DatabaseConfig struct {
	Path              string `toml:"path"`
	CompactAfterHours int    `toml:"compact_after_hours"`
	MaxCompactTokens  int    `toml:"max_compaction_tokens"`
}

type Config struct {
	Transcription openrouter.TranscriptionConfig `toml:"transcription"`
	Chat          openrouter.ChatConfig          `toml:"chat"`
	TTS           openrouter.TTSConfig           `toml:"tts"`
	Vad           VADConfig                      `toml:"vad"`
	UI            ui.UIConfig                    `toml:"ui"`
	AI            AIConfig                       `toml:"ai"`
	Plugins       PluginConfig                   `toml:"plugins"`
	Database      DatabaseConfig                 `toml:"database"`
}

func ReadConfig(configPath string) (*VADConfig, *openrouter.Transcriber, *openrouter.Chat, *openrouter.TTS, *AIConfig, *PluginConfig, *ui.UIConfig, *DatabaseConfig, error) {
	var config Config

	if _, err := toml.DecodeFile(configPath, &config); err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("load config: %w", err)
	}
	if config.Plugins.CallTimeoutSeconds < 0 || int64(config.Plugins.CallTimeoutSeconds) > int64((time.Duration(1<<63-1))/time.Second) {
		return nil, nil, nil, nil, nil, nil, nil, nil, errors.New("plugin call_timeout_seconds must be nonnegative and fit in a Go duration")
	}

	if config.Transcription.APIKey == "" {
		return nil, nil, nil, nil, nil, nil, nil, nil, errors.New("transcription API key is empty")
	}

	if config.Chat.APIKey == "" {
		return nil, nil, nil, nil, nil, nil, nil, nil, errors.New("chat API key is empty")
	}

	if config.TTS.APIKey == "" {
		return nil, nil, nil, nil, nil, nil, nil, nil, errors.New("tts API key is empty")
	}
	if _, err := config.TTS.StreamingEnabled(); err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, err
	}

	if config.Vad.Path == "" {
		return nil, nil, nil, nil, nil, nil, nil, nil, errors.New("VAD Model Path is empty")
	}

	if config.Transcription.Model == "" {
		config.Transcription.Model = "openai/whisper-large-v3-turbo"
	}

	if config.Transcription.URL == "" {
		config.Transcription.URL = "https://openrouter.ai/api/v1/audio/transcriptions"
	}

	if config.Database.Path == "" {
		config.Database.Path = "conversation.db"
	}
	if config.Database.CompactAfterHours <= 0 {
		config.Database.CompactAfterHours = 3
	}
	if config.Database.MaxCompactTokens <= 1024 {
		config.Database.MaxCompactTokens = 1024
	}

	return &config.Vad, &openrouter.Transcriber{
		Config: config.Transcription,
		Client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, &openrouter.Chat{
		Config: config.Chat,
		Client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, &openrouter.TTS{
		Config: config.TTS,
		Client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, &config.AI, &config.Plugins, &config.UI, &config.Database, nil
}
