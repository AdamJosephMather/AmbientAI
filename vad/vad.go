package vad

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/vcaesar/audio-go"
)

type audioFrame struct {
	input SileroInput
	seq   uint64
}

type Vad struct {
	model Silero
	state SileroState

	ctx    *audiogo.AllocatedContext
	device *audiogo.Device

	frames chan audioFrame
	stop   chan struct{}
	done   chan struct{}

	dropped atomic.Uint64

	enabled    atomic.Bool
	generation atomic.Uint64
	speaking   atomic.Bool

	OnProbability func(float32)
	OnAudio       func(float32)
	OnSpeech      func([]float32)
	OnError       func(error)
}

func (v *Vad) Enable() {
	v.enabled.Store(true)
	v.generation.Add(1)
}

func (v *Vad) Disable() {
	v.enabled.Store(false)
	v.generation.Add(1)
}

func (v *Vad) IsEnabled() bool {
	return v.enabled.Load()
}

func (v *Vad) IsSpeaking() bool {
	return v.speaking.Load()
}

func (v *Vad) Setup(path string) error {
	state, err := v.model.Setup(path)
	if err != nil {
		return err
	}

	v.state = state
	return nil
}

func (v *Vad) BeginListening() error {
	if !v.model.setup {
		return errors.New("VAD model not initialized")
	}
	if v.device != nil {
		return errors.New("already listening")
	}

	v.state.Reset()
	v.dropped.Store(0)

	ctx, err := audiogo.InitContext(
		nil,
		audiogo.ContextConfig{},
		nil,
	)
	if err != nil {
		return err
	}

	// The pure-Go capture library can fall back to a silent null device.
	// Treat unavailable hardware as an error instead of appearing to listen.
	if ctx.Backend() == audiogo.BackendNull {
		_ = ctx.Uninit()
		ctx.Free()
		return errors.New("no native microphone capture backend available")
	}

	config := audiogo.DefaultDeviceConfig(audiogo.Capture)
	config.Capture.Format = audiogo.FormatS16
	config.Capture.Channels = 1
	config.SampleRate = 16000
	config.PeriodSizeInFrames = 512

	// Buffer several seconds, but don't block the audio callback.
	frames := make(chan audioFrame, 128)
	stop := make(chan struct{})
	done := make(chan struct{})

	var partial SileroInput
	count := 0
	var sequence uint64

	onAudio := func(
		output []byte,
		input []byte,
		framecount uint32,
	) {
		// Input is mono signed 16-bit PCM.
		samples := len(input) / 2

		for i := 0; i < samples; i++ {
			offset := i * 2

			sample := int16(binary.LittleEndian.Uint16(
				input[offset : offset+2],
			))

			partial.audio[count] = float32(sample) / 32768.0
			count++

			if count == 512 {
				sequence++

				frame := audioFrame{
					input: partial,
					seq:   sequence,
				}

				select {
				case frames <- frame:
				default:
					// Inference is falling behind.
					// Never block the audio callback.
					v.dropped.Add(1)
				}

				count = 0
			}
		}
	}

	device, err := audiogo.InitDevice(
		ctx.Context,
		config,
		audiogo.DeviceCallbacks{
			Data: onAudio,
		},
	)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return err
	}

	v.ctx = ctx
	v.device = device
	v.frames = frames
	v.stop = stop
	v.done = done

	go v.processFrames(frames, stop, done)

	if err := device.Start(); err != nil {
		close(stop)
		<-done

		device.Uninit()
		_ = ctx.Uninit()
		ctx.Free()

		v.device = nil
		v.ctx = nil
		return err
	}

	return nil
}

func (v *Vad) processFrames(
	frames <-chan audioFrame,
	stop <-chan struct{},
	done chan<- struct{},
) {
	defer close(done)

	var segmenter speechSegmenter
	var lastSequence uint64
	var lastGeneration uint64

	for {
		select {
		case <-stop:
			return

		case frame := <-frames:
			generation := v.generation.Load()

			// New session, or enable/disable transition.
			if generation != lastGeneration {
				segmenter.Reset()
				v.state.Reset()
				v.speaking.Store(false)
				lastSequence = 0
				lastGeneration = generation
			}
			
			if v.OnAudio != nil {
				v.OnAudio(frame.input.audio[0])
			}

			if !v.enabled.Load() {
				continue
			}

			// If the input stream skipped frames, the
			// recording and recurrent state are invalid.
			if lastSequence != 0 &&
				frame.seq != lastSequence+1 {
				segmenter.Reset()
				v.state.Reset()
				v.speaking.Store(false)
			}

			lastSequence = frame.seq

			probability, err := v.model.Run(
				&v.state,
				&frame.input,
			)

			if err != nil {
				segmenter.Reset()
				v.state.Reset()
				v.speaking.Store(false)

				if v.OnError != nil {
					v.OnError(err)
				}
				continue
			}

			// Disable may have been called during inference.
			// Don't process or emit a result from that session.
			if generation != v.generation.Load() ||
				!v.enabled.Load() {
				continue
			}

			if v.OnProbability != nil {
				v.OnProbability(probability)
			}

			audio := segmenter.Push(
				&frame.input,
				probability,
			)
			
			v.speaking.Store(segmenter.speaking)
			
			if audio != nil &&
				v.enabled.Load() &&
				generation == v.generation.Load() &&
				v.OnSpeech != nil {
				v.OnSpeech(audio)
			}
		}
	}
}

func (v *Vad) StopListening() error {
	if v.device == nil {
		return nil
	}

	// Stop microphone callbacks before destroying resources.
	err := v.device.Stop()

	close(v.stop)
	<-v.done

	v.device.Uninit()

	if ctxErr := v.ctx.Uninit(); ctxErr != nil {
		err = errors.Join(err, ctxErr)
	}
	v.ctx.Free()

	v.device = nil
	v.ctx = nil
	v.frames = nil
	v.stop = nil
	v.done = nil

	return err
}

func (v *Vad) DroppedFrames() uint64 {
	return v.dropped.Load()
}

// Optional: useful for debugging microphone performance.
func (v *Vad) Status() string {
	return fmt.Sprintf(
		"listening=%t, dropped=%d",
		v.device != nil,
		v.DroppedFrames(),
	)
}
