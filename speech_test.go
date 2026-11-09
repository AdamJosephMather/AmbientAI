package main

import (
	"AmbientAI/openrouter"
	"context"
	"reflect"
	"testing"
)

func TestSpeechSentencesPlayExactlyOnce(t *testing.T) {
	var synthesized, played, started, finished []string
	err := speakResponseWithProgress(context.Background(), "The first few words stay here. This is the second sentence!",
		func(_ context.Context, text string) (*openrouter.Speech, error) {
			synthesized = append(synthesized, text)
			return &openrouter.Speech{Audio: []byte(text)}, nil
		}, func(_ context.Context, audio []byte) error {
			played = append(played, string(audio))
			return nil
		}, func(text string) { started = append(started, text) }, func(text string) { finished = append(finished, text) })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"The first few words stay here.", "This is the second sentence!"}
	for name, got := range map[string][]string{"synthesized": synthesized, "played": played, "started": started, "finished": finished} {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
