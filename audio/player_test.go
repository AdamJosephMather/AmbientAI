package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type testPCMPlayer struct {
	source  io.Reader
	output  []byte
	playing bool
	stopped bool
	err     error
	onPlay  func()
}

func (p *testPCMPlayer) Play() {
	p.playing = true
	if p.onPlay != nil {
		p.onPlay()
	}
}
func (p *testPCMPlayer) IsPlaying() bool {
	if p.source != nil {
		p.output, p.err = io.ReadAll(p.source)
		p.playing = false
	}
	return p.playing
}
func (p *testPCMPlayer) PauseAndStopReading() { p.stopped = true; p.playing = false }
func (p *testPCMPlayer) Err() error           { return p.err }

func TestPlayPCMPlaysOnceAndDrains(t *testing.T) {
	device := &testPCMPlayer{}
	p := Player{frameBytes: 4, drainBytes: 8, newPlayer: func(source io.Reader) pcmPlayer {
		device.source = source
		return device
	}}
	// An incomplete stereo frame is discarded, then only silence follows.
	input := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if err := p.PlayPCM(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte(nil), input[:8]...), make([]byte, 8)...)
	if !bytes.Equal(device.output, want) {
		t.Fatalf("output = %v, want %v", device.output, want)
	}
	if !device.stopped {
		t.Fatal("completed source was not detached")
	}
}

func TestPlayPCMStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	device := &testPCMPlayer{onPlay: cancel}
	p := Player{frameBytes: 2, newPlayer: func(io.Reader) pcmPlayer { return device }}
	if err := p.PlayPCM(ctx, []byte{1, 2}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if !device.stopped {
		t.Fatal("cancelled source was not detached")
	}
}

func TestPlayPCMDoesNotStartCancelledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := Player{frameBytes: 2, newPlayer: func(io.Reader) pcmPlayer {
		t.Fatal("cancelled request started playback")
		return nil
	}}
	if err := p.PlayPCM(ctx, []byte{1, 2}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
