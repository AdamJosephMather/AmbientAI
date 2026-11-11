package vad

import (
	stdreflect "reflect"
	"testing"
)

func TestThinkingKeepsCurrentRecording(t *testing.T) {
	var v Vad
	v.Enable()
	generation := v.generation.Load()
	var segmenter speechSegmenter
	var frame SileroInput
	var events []string
	v.OnSpeechStart = func() {
		if !v.IsSpeaking() {
			t.Error("start event occurred before speaking state")
		}
		events = append(events, "start")
	}
	v.OnSpeech = func([]float32) { events = append(events, "end") }
	for i := 0; i < 10; i++ {
		v.processSpeechFrame(&segmenter, &frame, 0.9, generation)
		v.Enable() // Listening/thinking transitions must not reset the utterance.
	}
	for i := 0; i < endFrames; i++ {
		v.processSpeechFrame(&segmenter, &frame, 0, generation)
	}
	if v.generation.Load() != generation {
		t.Fatal("repeated enable reset the capture session")
	}
	if !stdreflect.DeepEqual(events, []string{"start", "end"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestPlaybackFramesCannotBecomeUserSpeech(t *testing.T) {
	var v Vad
	v.Enable()
	previous := v.generation.Load()
	v.Disable()
	v.Enable()
	var segmenter speechSegmenter
	var frame SileroInput
	v.OnSpeechStart = func() { t.Error("old playback audio triggered speech") }
	for i := 0; i < 10; i++ {
		v.processSpeechFrame(&segmenter, &frame, 0.99, previous)
	}
	if v.IsSpeaking() {
		t.Fatal("old capture session changed speaking state")
	}
}
