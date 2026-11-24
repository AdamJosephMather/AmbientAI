package vad

import (
	"math/rand"
	"testing"
)

func BenchmarkSileroRun(b *testing.B) {
	var model Silero
	state, err := model.Setup("silero_vad.bin")
	if err != nil {
		b.Fatal(err)
	}
	rng := rand.New(rand.NewSource(42))
	frames := make([]SileroInput, 100)
	for i := range frames {
		for j := range frames[i].audio {
			frames[i].audio[j] = rng.Float32()*2 - 1
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	var prob float32
	for i := 0; i < b.N; i++ {
		p, err := model.Run(&state, &frames[i%100])
		if err != nil {
			b.Fatal(err)
		}
		prob += p
	}
	_ = prob
}
