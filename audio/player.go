package audio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

const (
	OutputSampleRate = 48000
	OutputChannels   = 2
)

// Match the device buffer with a silent tail so returning from PlayPCM means
// the final speech samples have also had time to leave the hardware queue.
const deviceBufferDuration = 50 * time.Millisecond

type pcmPlayer interface {
	Play()
	IsPlaying() bool
	PauseAndStopReading()
	Err() error
}

type Player struct {
	mu         sync.Mutex
	newPlayer  func(io.Reader) pcmPlayer
	frameBytes int
	drainBytes int
}

func NewPlayer(sampleRate, channels int) (*Player, error) {
	if sampleRate <= 0 || channels <= 0 {
		return nil, fmt.Errorf("invalid PCM format: sample rate %d, channels %d", sampleRate, channels)
	}
	c, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: channels,
		Format:       oto.FormatSignedInt16LE,
		BufferSize:   deviceBufferDuration,
	})
	if err != nil {
		return nil, fmt.Errorf("init audio device: %w", err)
	}
	<-ready

	if err := c.Err(); err != nil {
		return nil, fmt.Errorf("init audio device: %w", err)
	}
	return &Player{
		newPlayer:  func(source io.Reader) pcmPlayer { return c.NewPlayer(source) },
		frameBytes: channels * 2,
		drainBytes: (sampleRate * int(deviceBufferDuration) / int(time.Second)) * channels * 2,
	}, nil
}

// PlayPCM plays raw 16-bit little-endian PCM once, including draining the
// device's final speech samples, or stops promptly when ctx is cancelled.
func (p *Player) PlayPCM(ctx context.Context, pcm []byte) error {
	// Keep whole interleaved frames, including for multi-channel output.
	pcm = pcm[:len(pcm)-len(pcm)%p.frameBytes]
	if len(pcm) == 0 {
		return ctx.Err()
	}
	return p.PlayReader(ctx, bytes.NewReader(pcm))
}

// PlayReader plays incremental PCM through one device player. The caller must
// unblock source on cancellation (for HTTP audio, close the response body).
func (p *Player) PlayReader(ctx context.Context, pcm io.Reader) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	source := io.MultiReader(pcm, bytes.NewReader(make([]byte, p.drainBytes)))
	player := p.newPlayer(source)
	// Oto's Close is a no-op. Explicitly detach this source on every exit.
	defer player.PauseAndStopReading()
	player.Play()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for player.IsPlaying() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return player.Err()
}
