package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
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

// Synthesize returns audio and billed usage. If only the usage lookup fails,
// it returns the audio alongside the error so callers can preserve playback.
func (t *TTS) Synthesize(ctx context.Context, text string) (*Speech, Usage, error) {
	speech, err := t.SynthesizeAudio(ctx, text)
	if err != nil {
		return nil, Usage{}, err
	}
	usage, err := t.SpeechUsage(ctx, speech.GenerationID)
	return speech, usage, err
}

// SynthesizeAudio returns audio immediately, allowing usage to be fetched in
// the background with SpeechUsage without delaying playback.
func (t *TTS) SynthesizeAudio(ctx context.Context, text string) (*Speech, error) {
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

	speech := &Speech{
		Audio:        data,
		ContentType:  resp.Header.Get("Content-Type"),
		GenerationID: resp.Header.Get("X-Generation-Id"),
	}
	return speech, nil
}

// SpeechUsage retries records that have not yet become visible. The timeout
// bounds both the retry delays and HTTP requests, even without a caller deadline.
func (t *TTS) SpeechUsage(ctx context.Context, id string) (Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	delay := 250 * time.Millisecond
	for {
		usage, err := t.generationUsage(ctx, id)
		var pending *generationPendingError
		if !errors.As(err, &pending) {
			return usage, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Usage{}, fmt.Errorf("tts usage for %s unavailable: %w (last response: %v)", id, ctx.Err(), err)
		case <-timer.C:
		}
		delay = min(2*delay, 2*time.Second)
	}
}

type generationPendingError struct {
	id     string
	detail string
}

func (e *generationPendingError) Error() string {
	return fmt.Sprintf("tts usage for %s not available yet (404): %s", e.id, e.detail)
}

func (t *TTS) generationUsage(ctx context.Context, id string) (Usage, error) {
	if id == "" {
		return Usage{}, errors.New("tts usage: missing generation ID")
	}
	endpoint, err := url.Parse(t.Config.URL)
	if err != nil {
		return Usage{}, fmt.Errorf("tts usage URL: %w", err)
	}
	endpoint.Path = path.Join(path.Dir(endpoint.Path), "..", "generation")
	endpoint.RawPath = ""
	query := url.Values{"id": {id}}
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Usage{}, fmt.Errorf("tts usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.Config.APIKey)
	resp, err := t.Client.Do(req)
	if err != nil {
		return Usage{}, fmt.Errorf("tts usage request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusNotFound {
			return Usage{}, &generationPendingError{id: id, detail: strings.TrimSpace(string(detail))}
		}
		return Usage{}, fmt.Errorf("tts usage for %s failed (%s): %s", id, resp.Status, detail)
	}
	var result struct {
		Data *struct {
			Cost             float64 `json:"total_cost"`
			PromptTokens     int     `json:"tokens_prompt"`
			CompletionTokens int     `json:"tokens_completion"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Usage{}, fmt.Errorf("decode tts usage: %w", err)
	}
	if result.Data == nil {
		return Usage{}, errors.New("tts usage response had no data")
	}
	return Usage{
		Cost:             result.Data.Cost,
		PromptTokens:     result.Data.PromptTokens,
		CompletionTokens: result.Data.CompletionTokens,
		TotalTokens:      result.Data.PromptTokens + result.Data.CompletionTokens,
	}, nil
}
