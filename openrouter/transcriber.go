package openrouter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
)

type transcriptionRequest struct {
	Model      string     `json:"model"`
	InputAudio inputAudio `json:"input_audio"`
	Language   string     `json:"language,omitempty"`
}

type inputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type transcriptionResponse struct {
	Text string `json:"text"`
}

type TranscriptionConfig struct {
	APIKey   string `toml:"api_key"`
	Model    string `toml:"model"`
	URL      string `toml:"url"`
	Language string `toml:"language"`
}

type Transcriber struct {
	Config TranscriptionConfig
	Client *http.Client
}

func EncodeWAV(samples []float32, sampleRate uint32) ([]byte, error) {
	const (
		numChannels   = uint16(1)
		bitsPerSample = uint16(16)
		audioFormat   = uint16(1) // PCM
	)

	var pcm bytes.Buffer

	for _, sample := range samples {
		// Clamp in case microphone processing ever pushes outside [-1, 1].
		sample = max(-1.0, min(1.0, sample))

		var value int16

		if sample < 0 {
			value = int16(math.Round(float64(sample * 32768.0)))
		} else {
			value = int16(math.Round(float64(sample * 32767.0)))
		}

		if err := binary.Write(&pcm, binary.LittleEndian, value); err != nil {
			return nil, err
		}
	}

	data := pcm.Bytes()

	byteRate := sampleRate *
		uint32(numChannels) *
		uint32(bitsPerSample/8)

	blockAlign := numChannels * (bitsPerSample / 8)

	var wav bytes.Buffer

	// RIFF header
	wav.WriteString("RIFF")
	binary.Write(
		&wav,
		binary.LittleEndian,
		uint32(36+len(data)),
	)
	wav.WriteString("WAVE")

	// fmt chunk
	wav.WriteString("fmt ")
	binary.Write(&wav, binary.LittleEndian, uint32(16))
	binary.Write(&wav, binary.LittleEndian, audioFormat)
	binary.Write(&wav, binary.LittleEndian, numChannels)
	binary.Write(&wav, binary.LittleEndian, sampleRate)
	binary.Write(&wav, binary.LittleEndian, byteRate)
	binary.Write(&wav, binary.LittleEndian, blockAlign)
	binary.Write(&wav, binary.LittleEndian, bitsPerSample)

	// data chunk
	wav.WriteString("data")
	binary.Write(
		&wav,
		binary.LittleEndian,
		uint32(len(data)),
	)
	wav.Write(data)

	return wav.Bytes(), nil
}

func (t *Transcriber) Transcribe(
	ctx context.Context,
	samples []float32,
) (string, error) {
	if len(samples) == 0 {
		return "", errors.New("no audio samples")
	}

	wav, err := EncodeWAV(samples, 16000)
	if err != nil {
		return "", fmt.Errorf("encode wav: %w", err)
	}

	requestData := transcriptionRequest{
		Model: t.Config.Model,
		InputAudio: inputAudio{
			Data:   base64.StdEncoding.EncodeToString(wav),
			Format: "wav",
		},
		Language: t.Config.Language,
	}

	body, err := json.Marshal(requestData)
	if err != nil {
		return "", fmt.Errorf("marshal transcription request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		t.Config.URL,
		bytes.NewReader(body),
	)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+t.Config.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcription request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf(
			"transcription failed (%s): %s",
			resp.Status,
			string(responseBody),
		)
	}

	var result transcriptionResponse

	if err := json.Unmarshal(responseBody, &result); err != nil {
		return "", fmt.Errorf("decode transcription response: %w", err)
	}

	return result.Text, nil
}