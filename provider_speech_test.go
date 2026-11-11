package main

import (
	"context"
	"encoding/binary"
	"os"
	"strings"
	"testing"
	"unicode"
)

func TestProviderSpeechProbe(t *testing.T) {
	if os.Getenv("AMBIENT_TEST_TTS") != "1" {
		t.Skip("live provider probe")
	}
	_, tr, _, tts, _, err := ReadConfig("config.toml")
	if err != nil {
		t.Fatal(err)
	}
	inputs := []string{
		"The blue lantern is on the kitchen table. I will check the weather for you now.",
		"Your reminder is ready. The meeting starts tomorrow morning.",
		"I found the document you asked for. It contains the latest project notes.",
	}
	for i, input := range inputs {
		sp, err := tts.SynthesizeAudio(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		// Resample the generated 44.1 kHz mono PCM to the transcriber's 16 kHz input.
		n := len(sp.Audio) / 2
		samples := make([]float32, n*16000/44100)
		for j := range samples {
			src := j * 44100 / 16000
			samples[j] = float32(int16(binary.LittleEndian.Uint16(sp.Audio[src*2:]))) / 32768
		}
		text, _, err := tr.Transcribe(context.Background(), samples)
		if err != nil {
			t.Fatal(err)
		}
		normalize := func(s string) string {
			return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
		}
		if normalize(text) != normalize(input) {
			t.Errorf("raw provider audio transcript=%q, want %q", text, input)
		}
		t.Logf("model=%s sample=%d bytes=%d transcript=%q", tts.Config.Model, i+1, len(sp.Audio), text)
	}
}
