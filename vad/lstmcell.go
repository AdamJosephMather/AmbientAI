package vad

import (
	"fmt"
	"math"
)

type LSTMCell struct {
	channels_in  int
	channels_out int

	// Weights for all four gates, ordered i, f, g, o.
	// Flat [4*channels_out][channels_in]
	weight_ih []float32

	// Flat [4*channels_out][channels_out]
	weight_hh []float32

	// [4*channels_out]
	bias_ih []float32
	bias_hh []float32
}

type LSTMCellState struct {
	H []float32 // Hidden state
	C []float32 // Cell state
}

func (l *LSTMCell) LoadWeights(weightIH, weightHH, biasIH, biasHH []float32) error {
	gates := 4 * l.channels_out

	if len(weightIH) != gates*l.channels_in ||
		len(weightHH) != gates*l.channels_out ||
		len(biasIH) != gates ||
		len(biasHH) != gates {
		return fmt.Errorf("LSTMCell: weight dimensions do not match")
	}

	// PyTorch layout: [4*channels_out][channels_in]
	copy(l.weight_ih, weightIH)
	copy(l.weight_hh, weightHH)
	copy(l.bias_ih, biasIH)
	copy(l.bias_hh, biasHH)

	return nil
}

func (c *LSTMCell) Setup(channels_in, channels_out int) {
	c.channels_in = channels_in
	c.channels_out = channels_out

	gates := 4 * channels_out

	c.weight_ih = make([]float32, gates*channels_in)
	c.weight_hh = make([]float32, gates*channels_out)
	c.bias_ih = make([]float32, gates)
	c.bias_hh = make([]float32, gates)
}

func (c *LSTMCellState) Reset(hidden_size int) {
	if len(c.H) != hidden_size || len(c.C) != hidden_size {
		c.H = make([]float32, hidden_size)
		c.C = make([]float32, hidden_size)
	}
	clear(c.H)
	clear(c.C)
}

func sigmoid(x float32) float32 {
	return 1 / (1 + float32(math.Exp(float64(-x))))
}

func tanh(x float32) float32 {
	return float32(math.Tanh(float64(x)))
}
