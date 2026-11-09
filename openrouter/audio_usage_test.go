package openrouter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTranscribeUsage(t *testing.T) {
	for _, includeUsage := range []bool{true, false} {
		t.Run(fmt.Sprint(includeUsage), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if includeUsage {
					fmt.Fprint(w, `{"text":"hello","usage":{"seconds":1.5,"input_tokens":12,"output_tokens":3,"total_tokens":15,"cost":0.002}}`)
				} else {
					fmt.Fprint(w, `{"text":"hello"}`)
				}
			}))
			defer server.Close()
			client := Transcriber{Config: TranscriptionConfig{URL: server.URL}, Client: server.Client()}
			text, usage, err := client.Transcribe(context.Background(), []float32{0})
			if err != nil || text != "hello" {
				t.Fatalf("Transcribe = %q, %v", text, err)
			}
			if includeUsage {
				if usage.Cost != 0.002 || usage.Seconds != 1.5 || usage.InputTokens != 12 || usage.OutputTokens != 3 || usage.TotalTokens != 15 {
					t.Fatalf("unexpected usage: %+v", usage)
				}
			} else if usage.Cost != 0 {
				t.Fatalf("missing usage returned cost %v", usage.Cost)
			}
		})
	}
}

func TestSynthesizeUsage(t *testing.T) {
	for _, lookupFails := range []bool{false, true} {
		t.Run(fmt.Sprint(lookupFails), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("missing authorization")
				}
				switch r.URL.Path {
				case "/api/v1/audio/speech":
					if r.Method != http.MethodPost {
						t.Error("speech request was not POST")
					}
					w.Header().Set("Content-Type", "audio/pcm")
					w.Header().Set("X-Generation-Id", "gen-test")
					fmt.Fprint(w, "audio")
				case "/api/v1/generation":
					if r.Method != http.MethodGet || r.URL.Query().Get("id") != "gen-test" {
						t.Error("incorrect generation lookup")
					}
					if lookupFails {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					fmt.Fprint(w, `{"data":{"total_cost":0.003,"tokens_prompt":5,"tokens_completion":8}}`)
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := TTS{Config: TTSConfig{URL: server.URL + "/api/v1/audio/speech", APIKey: "test-key"}, Client: server.Client()}
			speech, usage, err := client.Synthesize(context.Background(), "hello")
			if speech == nil || string(speech.Audio) != "audio" || speech.GenerationID != "gen-test" || speech.ContentType != "audio/pcm" {
				t.Fatalf("audio was not preserved: %+v", speech)
			}
			if lookupFails {
				if err == nil {
					t.Fatal("expected usage lookup error")
				}
			} else if err != nil || usage.Cost != 0.003 || usage.PromptTokens != 5 || usage.CompletionTokens != 8 || usage.TotalTokens != 13 {
				t.Fatalf("unexpected usage: %+v, error: %v", usage, err)
			}
		})
	}
}

func TestSpeechUsageRetriesPendingRecord(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, `{"error":{"message":"Generation not found"}}`, http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"data":{"total_cost":0.003}}`)
	}))
	defer server.Close()
	client := TTS{Config: TTSConfig{URL: server.URL + "/api/v1/audio/speech"}, Client: server.Client()}
	usage, err := client.SpeechUsage(context.Background(), "gen-late")
	if err != nil || usage.Cost != 0.003 || attempts != 3 {
		t.Fatalf("usage = %+v, err = %v, attempts = %d", usage, err, attempts)
	}
}

func TestSpeechUsagePendingRecordHonorsDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Generation not found", http.StatusNotFound)
	}))
	defer server.Close()
	client := TTS{Config: TTSConfig{URL: server.URL + "/api/v1/audio/speech"}, Client: server.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.SpeechUsage(ctx, "gen-pending")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "gen-pending") {
		t.Fatalf("expected deadline error with generation ID, got %v", err)
	}
}

func TestSynthesizeAudioDoesNotWaitForUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/audio/speech" {
			t.Error("SynthesizeAudio requested usage")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("X-Generation-Id", "gen-audio")
		fmt.Fprint(w, "audio")
	}))
	defer server.Close()
	client := TTS{Config: TTSConfig{URL: server.URL + "/api/v1/audio/speech"}, Client: server.Client()}
	speech, err := client.SynthesizeAudio(context.Background(), "hello")
	if err != nil || speech == nil || string(speech.Audio) != "audio" || speech.GenerationID != "gen-audio" {
		t.Fatalf("unexpected audio: %+v, err = %v", speech, err)
	}
}
