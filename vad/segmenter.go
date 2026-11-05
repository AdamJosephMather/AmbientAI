
package vad

const (
	frameSamples = 512
	sampleRate   = 16000

	startThreshold = 0.60
	endThreshold   = 0.35

	startFrames     = 2  // 64 ms
	endFrames       = 16 // 512 ms
	minSpeechFrames = 7  // 224 ms

	preRollSamples  = 2560   // 160 ms
	tailSamples     = 2560   // 160 ms
	maxAudioSamples = 480000 // 30 seconds
)

type speechSegmenter struct {
	speaking bool

	preRoll []float32
	audio   []float32

	startCount  int
	silentCount int
	speechCount int
}

func (s *speechSegmenter) Reset() {
	s.speaking = false
	s.preRoll = s.preRoll[:0]
	s.audio = s.audio[:0]

	s.startCount = 0
	s.silentCount = 0
	s.speechCount = 0
}

func (s *speechSegmenter) IsSpeaking() bool {
	return s.speaking
}

// Push returns a completed utterance, or nil.
// Must be called once for each consecutive 512-sample frame.
func (s *speechSegmenter) Push(
	frame *SileroInput,
	probability float32,
) []float32 {

	if !s.speaking {
		// Maintain a rolling buffer of recent input.
		s.preRoll = append(s.preRoll, frame.audio[:]...)

		if len(s.preRoll) > preRollSamples {
			excess := len(s.preRoll) - preRollSamples
			copy(s.preRoll, s.preRoll[excess:])
			s.preRoll = s.preRoll[:preRollSamples]
		}

		if probability >= startThreshold {
			s.startCount++
		} else {
			s.startCount = 0
		}

		if s.startCount < startFrames {
			return nil
		}

		// Speech starts. Copy pre-roll including the current
		// frame, so we don't duplicate the newest samples.
		s.speaking = true
		s.audio = append(s.audio[:0], s.preRoll...)

		s.preRoll = s.preRoll[:0]
		s.startCount = 0
		s.silentCount = 0
		s.speechCount = startFrames

		return nil
	}

	// Already speaking: keep recording.
	s.audio = append(s.audio, frame.audio[:]...)

	if probability >= endThreshold {
		s.silentCount = 0
		s.speechCount++
	} else {
		s.silentCount++
	}

	// End on sustained silence or recording length limit.
	timedOut := s.silentCount >= endFrames
	tooLong := len(s.audio) >= maxAudioSamples

	if !timedOut && !tooLong {
		return nil
	}

	// Discard recordings without enough detected speech.
	if s.speechCount < minSpeechFrames {
		s.Reset()
		return nil
	}

	end := len(s.audio)

	if timedOut {
		// Remove excessive trailing silence, leaving a
		// little after the final detected speech.
		trim := s.silentCount*frameSamples - tailSamples

		if trim > 0 {
			end -= trim
		}
	}

	if end < 0 {
		end = 0
	}

	// Copy because Reset reuses the underlying buffer.
	result := make([]float32, end)
	copy(result, s.audio[:end])

	s.Reset()

	return result
}
