package openrouter

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTranscriptionNormalization(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []float32
		want  []float32
	}{
		{"quiet speech", []float32{0.01, -0.01}, []float32{0.1, -0.1}},
		{"gain cap", []float32{0.002, -0.002}, []float32{0.02, -0.02}},
		{"near silence", []float32{0.00001, -0.00001}, []float32{0.00001, -0.00001}},
		{"silence", []float32{0, 0}, []float32{0, 0}},
		{"invalid samples", []float32{float32(math.NaN()), float32(math.Inf(1))}, []float32{0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeTranscriptionAudio(tc.input)
			for i := range got {
				if math.Abs(float64(got[i]-tc.want[i])) > 1e-6 {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
	// A loud transient limits gain even when the rest of the clip is quiet.
	samples := make([]float32, 16000)
	samples[0] = 0.5
	got := normalizeTranscriptionAudio(samples)
	if math.Abs(float64(got[0])-0.89) > 1e-6 || samples[0] != 0.5 {
		t.Fatalf("peak limit or input preservation failed: %v, %v", got[0], samples[0])
	}
}

func TestTranscribeSendsNormalizedWAV(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request transcriptionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		wav, err := base64.StdEncoding.DecodeString(request.InputAudio.Data)
		if err != nil || len(wav) != 48 {
			t.Errorf("invalid WAV: %v, length %d", err, len(wav))
			w.WriteHeader(400)
			return
		}
		if sample := int16(binary.LittleEndian.Uint16(wav[44:46])); sample < 3275 || sample > 3278 {
			t.Errorf("quiet sample was not normalized: %d", sample)
		}
		w.Write([]byte(`{"text":"hello"}`))
	}))
	defer server.Close()
	tr := Transcriber{Config: TranscriptionConfig{URL: server.URL}, Client: server.Client()}
	samples := []float32{0.01, -0.01}
	text, _, err := tr.Transcribe(context.Background(), samples)
	if err != nil || text != "hello" || samples[0] != 0.01 {
		t.Fatalf("transcribe failed: %q, %v, %v", text, err, samples)
	}
}
