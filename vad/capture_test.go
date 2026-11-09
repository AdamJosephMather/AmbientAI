package vad

import (
	"os"
	"testing"
	"time"
)

// Opt in on a machine with a microphone: AMBIENT_TEST_MICROPHONE=1 go test ./vad.
// Samples are discarded; this does not save or transmit recorded audio.
func TestMicrophoneCaptureLifecycle(t *testing.T) {
	if os.Getenv("AMBIENT_TEST_MICROPHONE") != "1" {
		t.Skip("set AMBIENT_TEST_MICROPHONE=1 to exercise the default microphone")
	}
	var v Vad
	// Leave inference disabled so capture can be tested without model weights.
	v.model.setup = true
	frames := make(chan struct{}, 128)
	v.OnAudio = func(float32) {
		select {
		case frames <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { _ = v.StopListening() })
	for cycle := 0; cycle < 2; cycle++ {
		if err := v.BeginListening(); err != nil {
			t.Fatal(err)
		}
		if err := v.BeginListening(); err == nil {
			t.Fatal("starting an active microphone succeeded")
		}
		select {
		case <-frames:
		case <-time.After(3 * time.Second):
			t.Fatal("microphone delivered no complete 512-sample frames")
		}
		if err := v.StopListening(); err != nil {
			t.Fatal(err)
		}
		if err := v.StopListening(); err != nil {
			t.Fatalf("repeated stop: %v", err)
		}
		for len(frames) > 0 {
			<-frames
		}
	}
}
