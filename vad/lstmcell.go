package vad

import (
	"fmt"
	"math"
)

type LSTMCell struct {
	channels_in  int
	channels_out int

	// Weights for all four gates, ordered i, f, g, o.
	// [4*channels_out][channels_in]
	weight_ih [][]float32

	// [4*channels_out][channels_out]
	weight_hh [][]float32

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
	for g := 0; g < gates; g++ {
		start := g * l.channels_in
		copy(l.weight_ih[g], weightIH[start:start+l.channels_in])

		start = g * l.channels_out
		copy(l.weight_hh[g], weightHH[start:start+l.channels_out])
	}

	copy(l.bias_ih, biasIH)
	copy(l.bias_hh, biasHH)

	return nil
}

func (c *LSTMCell) Setup(channels_in, channels_out int) {
	c.channels_in = channels_in
	c.channels_out = channels_out

	gates := 4 * channels_out

	c.weight_ih = make([][]float32, gates)
	c.weight_hh = make([][]float32, gates)

	for i := 0; i < gates; i++ {
		c.weight_ih[i] = make([]float32, channels_in)
		c.weight_hh[i] = make([]float32, channels_out)
	}

	c.bias_ih = make([]float32, gates)
	c.bias_hh = make([]float32, gates)
}

func (c *LSTMCellState) Reset(hidden_size int) {
	c.H = make([]float32, hidden_size)
	c.C = make([]float32, hidden_size)
}

func sigmoid(x float32) float32 {
	return 1 / (1 + float32(math.Exp(float64(-x))))
}

func tanh(x float32) float32 {
	return float32(math.Tanh(float64(x)))
}

func (c *LSTMCell) Run(x [][]float32, state *LSTMCellState) [][]float32 {
	// LSTMCell processes one timestep at a time.
	// x shape: [1][channels_in]

	if len(state.H) != c.channels_out || len(state.C) != c.channels_out {
		state.Reset(c.channels_out)
	}

	hidden := c.channels_out

	// Compute all four gate preactivations:
	// gates = W_ih*x + b_ih + W_hh*h + b_hh
	gates := make([]float32, 4*hidden)

	for g := 0; g < 4*hidden; g++ {
		sum := c.bias_ih[g] + c.bias_hh[g]

		for i := 0; i < c.channels_in; i++ {
			sum += c.weight_ih[g][i] * x[0][i]
		}

		for i := 0; i < hidden; i++ {
			sum += c.weight_hh[g][i] * state.H[i]
		}

		gates[g] = sum
	}

	// Update cell and hidden states.
	for i := 0; i < hidden; i++ {
		input_gate := sigmoid(gates[i])
		forget_gate := sigmoid(gates[hidden+i])
		candidate := tanh(gates[2*hidden+i])
		output_gate := sigmoid(gates[3*hidden+i])

		state.C[i] = forget_gate*state.C[i] +
			input_gate*candidate

		state.H[i] = output_gate * tanh(state.C[i])
	}

	// Return output with shape [1][channels_out].
	return [][]float32{state.H}
}