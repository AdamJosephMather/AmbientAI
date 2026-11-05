package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

const maxAudioBytes = 64 << 20 // 64 MiB safety cap

type TTSConfig struct {
	APIKey         string `toml:"api_key"`
	Model          string `toml:"model"`
	URL            string `toml:"url"`
	Voice          string `toml:"voice"`
	ResponseFormat string `toml:"response_format"` // "mp3" or "pcm"
}

type ttsRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	Voice          string `json:"voice,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

type Speech struct {
	Audio        []byte
	ContentType  string // "audio/mpeg" or "audio/pcm"
	GenerationID string
}

type TTS struct {
	Config TTSConfig
	Client *http.Client
}

func (t *TTS) Synthesize(ctx context.Context, text string) (*Speech, error) {
	if utf8.RuneCountInString(text) > 5000 {
		return nil, errors.New("text exceeds 5000 character limit; split it into smaller segments")
	}
	
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("empty text")
	}

	body, err := json.Marshal(ttsRequest{
		Model:          t.Config.Model,
		Input:          text,
		Voice:          t.Config.Voice,
		ResponseFormat: t.Config.ResponseFormat,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal tts request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Config.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.Config.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tts request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAudioBytes+1))
	if err != nil {
		return nil, err
	}

	// Non-2xx responses are JSON error bodies, not audio.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tts failed (%s): %s", resp.Status, string(data))
	}
	if len(data) > maxAudioBytes {
		return nil, errors.New("audio exceeds size limit")
	}
	if len(data) == 0 {
		return nil, errors.New("tts returned empty audio")
	}

	return &Speech{
		Audio:        data,
		ContentType:  resp.Header.Get("Content-Type"),
		GenerationID: resp.Header.Get("X-Generation-Id"),
	}, nil
}