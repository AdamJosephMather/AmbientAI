package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"
	"github.com/BurntSushi/toml"
	"AmbientAI/openrouter"
)

type VADConfig struct {
	Path string `toml:"path"`
}

type AIConfig struct {
	AIName string `toml:"ai_name"`
	UserName string `toml:"user_name"`
	
	ModelFast string `toml:"model_fast"`
	ModelFastReasoning string `toml:"model_fast_reasoning"`
	ModelSmart string `toml:"model_smart"`
	ModelSmartReasoning string `toml:"model_smart_reasoning"`
}

type Config struct {
	Transcription  openrouter.TranscriptionConfig `toml:"transcription"`
	Chat           openrouter.ChatConfig          `toml:"chat"`
	TTS            openrouter.TTSConfig           `toml:"tts"`
	Vad            VADConfig                      `toml:"vad"`
	AI             AIConfig                       `toml:"ai"`
}

func ReadConfig(configPath string) (*VADConfig, *openrouter.Transcriber, *openrouter.Chat, *openrouter.TTS, *AIConfig, error) {
	var config Config

	if _, err := toml.DecodeFile(configPath, &config); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("load config: %w", err)
	}

	if config.Transcription.APIKey == "" {
		return nil, nil, nil, nil, nil, errors.New("transcription API key is empty")
	}
	
	if config.Chat.APIKey == "" {
		return nil, nil, nil, nil, nil, errors.New("chat API key is empty")
	}
	
	if config.TTS.APIKey == "" {
		return nil, nil, nil, nil, nil, errors.New("tts API key is empty")
	}
	
	if config.Vad.Path == "" {
		return nil, nil, nil, nil, nil, errors.New("VAD Model Path is empty")
	}

	if config.Transcription.Model == "" {
		config.Transcription.Model = "openai/whisper-large-v3-turbo"
	}

	if config.Transcription.URL == "" {
		config.Transcription.URL = "https://openrouter.ai/api/v1/audio/transcriptions"
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
	}, &config.AI, nil
}

