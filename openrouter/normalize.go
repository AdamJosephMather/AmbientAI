package openrouter

import "math"

// normalizeTranscriptionAudio adjusts a completed utterance, never the VAD
// stream. One constant gain preserves dynamics; the peak limit avoids clipping.
func normalizeTranscriptionAudio(samples []float32) []float32 {
	var peak, energy float64
	for _, sample := range samples {
		value := float64(sample)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		peak = math.Max(peak, math.Abs(value))
		energy += value * value
	}
	if len(samples) == 0 {
		return nil
	}
	rms := math.Sqrt(energy / float64(len(samples)))
	gain := 1.0
	// Avoid turning near-silence into loud background noise. Target -20 dBFS
	// RMS with at most 20 dB of gain and roughly 1 dB of peak headroom.
	if rms >= 0.001 {
		gain = math.Min(10, 0.1/rms)
	}
	if peak > 0 {
		gain = math.Min(gain, 0.89/peak)
	}
	result := make([]float32, len(samples))
	for i, sample := range samples {
		if !math.IsNaN(float64(sample)) && !math.IsInf(float64(sample), 0) {
			result[i] = float32(float64(sample) * gain)
		}
	}
	return result
}
