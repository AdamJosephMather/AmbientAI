package audio

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/ebitengine/oto/v3"
)

const (
	HeyGenSampleRate = 44100
	HeyGenChannels   = 1
)

type Player struct {
	otoCtx *oto.Context
}

func NewPlayer(sampleRate, channels int) (*Player, error) {
	c, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: channels,
		Format:       oto.FormatSignedInt16LE,
	})
	if err != nil {
		return nil, fmt.Errorf("init audio device: %w", err)
	}
	<-ready

	return &Player{otoCtx: c}, nil
}

// PlayPCM plays raw 16-bit little-endian PCM and blocks until it finishes
// or ctx is cancelled (playback is paused and ctx.Err() returned).
func (p *Player) PlayPCM(ctx context.Context, pcm []byte) error {
	// A stray odd byte would misalign samples; drop it.
	if len(pcm)%2 != 0 {
		pcm = pcm[:len(pcm)-1]
	}

	player := p.otoCtx.NewPlayer(bytes.NewReader(pcm))
	defer player.Close()
	player.Play()

	for player.IsPlaying() {
		select {
		case <-ctx.Done():
			player.Pause()
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}

	return player.Err()
}