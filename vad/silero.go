package vad

import (
	"errors"
	"math"
)

// The Silero architecture is fixed. The window counts below derive from
// (inputLength + 2*padding - kernel)/stride + 1 for each layer.
const (
	x1Rows = 4  // STFT: 640 input rows (64 past + 512 audio + 64 reflect)
	x3Rows = 4  // conv1
	x4Rows = 2  // conv2
	x5Rows = 1  // conv3
	x6Rows = 1  // conv4
)

type Silero struct {
	stft_conv Conv1d
	conv1      Conv1d
	conv2      Conv1d
	conv3      Conv1d
	conv4      Conv1d

	lstm_cell LSTMCell
	final_conv Conv1d

	setup bool

	// Scratch buffers, allocated once in Setup and reused on every frame.
	x      []float32 // 640: 64 past + 512 audio + 64 reflected tail
	x1     []float32 // 4 x 258
	x2     []float32 // 4 x 129
	x3     []float32 // 4 x 128
	x4     []float32 // 2 x 64
	x5     []float32 // 1 x 64
	x6     []float32 // 1 x 128
	x8     []float32 // 1 x 128
	x9     []float32 // 1 x 1
	gates  []float32 // 512 gate preactivations
}

type SileroState struct {
	pastAudio [64]float32
	lstmstate LSTMCellState
}

type SileroInput struct {
	audio [512]float32
}

func (s *SileroState) Reset() {
	clear(s.pastAudio[:])
	s.lstmstate.Reset(128) // channels out
}

func (s *Silero) Setup(path string) (SileroState, error) {
	s.stft_conv.Setup(1, 258, 256, 128, 0);
	s.conv1.Setup(129, 128, 3, 1, 1);
	s.conv2.Setup(128, 64, 3, 2, 1);
	s.conv3.Setup(64, 64, 3, 2, 1);
	s.conv4.Setup(64, 128, 3, 1, 1);

	s.lstm_cell.Setup(128, 128)
	s.final_conv.Setup(128, 1, 1, 1, 0)

	var state SileroState
	state.Reset()

	weights, err := LoadSileroWeights(path)
	if err != nil {
		return state, err
	}

	if err := s.stft_conv.LoadWeights(weights.STFT, nil); err != nil {
		return state, err
	}
	if err := s.conv1.LoadWeights(weights.Conv1W, weights.Conv1B); err != nil {
		return state, err
	}
	if err := s.conv2.LoadWeights(weights.Conv2W, weights.Conv2B); err != nil {
		return state, err
	}
	if err := s.conv3.LoadWeights(weights.Conv3W, weights.Conv3B); err != nil {
		return state, err
	}
	if err := s.conv4.LoadWeights(weights.Conv4W, weights.Conv4B); err != nil {
		return state, err
	}

	if err := s.lstm_cell.LoadWeights(
		weights.LSTMInput,
		weights.LSTMHidden,
		weights.LSTMBiasInput,
		weights.LSTMBiasHidden,
	); err != nil {
		return state, err
	}

	if err := s.final_conv.LoadWeights(weights.FinalW, weights.FinalB); err != nil {
		return state, err
	}

	// Repack conv kernels once, at setup time, into [out][t][in] order.
	s.conv1.kernel = repackKernel(s.conv1.kernel, 128, 129, 3)
	s.conv2.kernel = repackKernel(s.conv2.kernel, 64, 128, 3)
	s.conv3.kernel = repackKernel(s.conv3.kernel, 64, 64, 3)
	s.conv4.kernel = repackKernel(s.conv4.kernel, 128, 64, 3)

	// Allocate flat scratch buffers once; reused on every frame.
	s.x = make([]float32, 640)
	s.x1 = make([]float32, x1Rows*258)
	s.x2 = make([]float32, x1Rows*129)
	s.x3 = make([]float32, x3Rows*128)
	s.x4 = make([]float32, x4Rows*64)
	s.x5 = make([]float32, x5Rows*64)
	s.x6 = make([]float32, x6Rows*128)
	s.x8 = make([]float32, 128)
	s.x9 = make([]float32, 1)
	s.gates = make([]float32, 4*128)

	s.setup = true
	return state, nil
}

// runStft computes the 258x1x256 STFT convolution. The output for each
// window is accumulated from kernel positions in ascending order, exactly as
// the reference implementation.
func (s *Silero) runStft(x, out []float32) {
	// 64 past + 512 audio = 576 rows of 1 channel; 4 windows at stride 128.
	for o := 0; o < 258; o++ {
		kBase := o * 256
		var sum0, sum1, sum2, sum3 float32
		for t := 0; t < 256; t++ {
			kw := s.stft_conv.kernel[kBase+t]
			sum0 += kw * x[t]
			sum1 += kw * x[128+t]
			sum2 += kw * x[256+t]
			sum3 += kw * x[384+t]
		}
		out[0*258+o] = sum0
		out[1*258+o] = sum1
		out[2*258+o] = sum2
		out[3*258+o] = sum3
	}
}

// runConv1 computes the 129x128x3 stride-1 padding-1 convolution. Kernel
// positions are accumulated in ascending order per output, exactly as the
// reference implementation (zero padding skips the out-of-range windows).
func (s *Silero) runConv1(x, out []float32) {
	for o := 0; o < 128; o++ {
		kBase := o * 387
		bias := s.conv1.bias[o]

		// window 0: inT = w+t-1; t=0 is padding
		{
			sum := bias
			for i := 0; i < 129; i++ {
				sum += s.conv1.kernel[kBase+129+i] * x[i]
			}
			for i := 0; i < 129; i++ {
				sum += s.conv1.kernel[kBase+258+i] * x[129+i]
			}
			out[0*128+o] = sum
		}
		// window 1
		{
			sum := bias
			for i := 0; i < 129; i++ {
				sum += s.conv1.kernel[kBase+i] * x[i]
			}
			for i := 0; i < 129; i++ {
				sum += s.conv1.kernel[kBase+129+i] * x[129+i]
			}
			for i := 0; i < 129; i++ {
				sum += s.conv1.kernel[kBase+258+i] * x[258+i]
			}
			out[1*128+o] = sum
		}
		// window 2
		{
			sum := bias
			for i := 0; i < 129; i++ {
				sum += s.conv1.kernel[kBase+i] * x[129+i]
			}
			for i := 0; i < 129; i++ {
				sum += s.conv1.kernel[kBase+129+i] * x[258+i]
			}
			for i := 0; i < 129; i++ {
				sum += s.conv1.kernel[kBase+258+i] * x[387+i]
			}
			out[2*128+o] = sum
		}
		// window 3: t=2 is padding (inT=4)
		{
			sum := bias
			for i := 0; i < 129; i++ {
				sum += s.conv1.kernel[kBase+i] * x[258+i]
			}
			for i := 0; i < 129; i++ {
				sum += s.conv1.kernel[kBase+129+i] * x[387+i]
			}
			out[3*128+o] = sum
		}
	}
}

// runConv2 computes the 128x64x3 stride-2 padding-1 convolution.
func (s *Silero) runConv2(x, out []float32) {
	for o := 0; o < 64; o++ {
		kBase := o * 384
		bias := s.conv2.bias[o]

		// window 0: t=0 is padding (inT=-1)
		{
			sum := bias
			for i := 0; i < 128; i++ {
				sum += s.conv2.kernel[kBase+128+i] * x[i]
			}
			for i := 0; i < 128; i++ {
				sum += s.conv2.kernel[kBase+256+i] * x[128+i]
			}
			out[0*64+o] = sum
		}
		// window 1: inT = w*2+t-1 → t=0→1, t=1→2, t=2→3 (all valid)
		{
			sum := bias
			for i := 0; i < 128; i++ {
				sum += s.conv2.kernel[kBase+i] * x[128+i]
			}
			for i := 0; i < 128; i++ {
				sum += s.conv2.kernel[kBase+128+i] * x[256+i]
			}
			for i := 0; i < 128; i++ {
				sum += s.conv2.kernel[kBase+256+i] * x[384+i]
			}
			out[1*64+o] = sum
		}
	}
}

// runConv3 computes the 64x64x3 stride-2 padding-1 convolution (single window).
func (s *Silero) runConv3(x, out []float32) {
	for o := 0; o < 64; o++ {
		kBase := o * 192
		// t=0 is padding (inT=-1)
		sum := s.conv3.bias[o]
		for i := 0; i < 64; i++ {
			sum += s.conv3.kernel[kBase+64+i] * x[i]
		}
		for i := 0; i < 64; i++ {
			sum += s.conv3.kernel[kBase+128+i] * x[64+i]
		}
		out[o] = sum
	}
}

// runConv4 computes the 64x128x3 stride-1 padding-1 convolution (single window).
func (s *Silero) runConv4(x, out []float32) {
	for o := 0; o < 128; o++ {
		kBase := o * 192
		// t=0 is padding (inT=-1); t=2 is padding (inT=1)
		sum := s.conv4.bias[o]
		for i := 0; i < 64; i++ {
			sum += s.conv4.kernel[kBase+64+i] * x[i]
		}
		out[o] = sum
	}
}

// runFinal computes the 128x1x1 final convolution (single window).
func (s *Silero) runFinal(x, out []float32) {
	sum := s.final_conv.bias[0]
	for i := 0; i < 128; i++ {
		sum += s.final_conv.kernel[i] * x[i]
	}
	out[0] = sum
}

// runLSTM advances the 128-channel LSTM for a single timestep. gates is the
// persistent scratch buffer for the 512 gate preactivations.
func (s *Silero) runLSTM(x, H, C []float32) {
	gates := s.gates
	for g := 0; g < 512; g++ {
		sum := s.lstm_cell.bias_ih[g] + s.lstm_cell.bias_hh[g]

		ihBase := g * 128
		for i := 0; i < 128; i++ {
			sum += s.lstm_cell.weight_ih[ihBase+i] * x[i]
		}

		hhBase := g * 128
		for i := 0; i < 128; i++ {
			sum += s.lstm_cell.weight_hh[hhBase+i] * H[i]
		}

		gates[g] = sum
	}

	// Update cell and hidden states.
	for i := 0; i < 128; i++ {
		input_gate := sigmoid(gates[i])
		forget_gate := sigmoid(gates[128+i])
		candidate := tanh(gates[256+i])
		output_gate := sigmoid(gates[384+i])

		C[i] = forget_gate*C[i] + input_gate*candidate

		H[i] = output_gate * tanh(C[i])
	}
}

func (s *Silero) Run(state *SileroState, input *SileroInput) (float32, error) {
	if (!s.setup) {
		return 0, errors.New("Silero VAD not setup, call Setup before Run")
	}

	// Copy the audio into x: 64 past samples, 512 audio samples, then the
	// reflection-padded tail (64 samples).
	x := s.x

	copy(x[0:64], state.pastAudio[:])
	copy(x[64:64+512], input.audio[:])

	// Reflection pad: from [0, 1, 2, 3, 4] we would get [3, 2, 1].
	for i := 0; i < 64; i++ {
		x[64+512+i] = input.audio[512-i-2]
	}

	// STFT
	s.runStft(x, s.x1)

	// Extract the imaginary and real components, and compute Pythagorean length.
	x2 := s.x2
	x1 := s.x1
	for t := 0; t < x1Rows; t++ {
		base := t * 258
		outBase := t * 129
		for f := 0; f < 129; f++ {
			real := x1[base+f]
			imag := x1[base+f+129]

			x2[outBase+f] = float32(math.Sqrt(
				float64(real*real + imag*imag),
			))
		}
	}

	// Conv layers
	s.runConv1(s.x2, s.x3)
	reluInPlace(s.x3)

	s.runConv2(s.x3, s.x4)
	reluInPlace(s.x4)

	s.runConv3(s.x4, s.x5)
	reluInPlace(s.x5)

	s.runConv4(s.x5, s.x6)
	reluInPlace(s.x6)

	// LSTM
	if len(state.lstmstate.H) != 128 || len(state.lstmstate.C) != 128 {
		state.lstmstate.Reset(128)
	}
	s.runLSTM(s.x6, state.lstmstate.H, state.lstmstate.C)

	// ReLU of the hidden state must not mutate the hidden state used by the
	// next frame, so write the relu output into a separate buffer.
	hx := state.lstmstate.H
	x8 := s.x8
	for i := 0; i < 128; i++ {
		if hx[i] > 0 {
			x8[i] = hx[i]
		} else {
			x8[i] = 0
		}
	}
	s.runFinal(x8, s.x9)

	finalval := sigmoid(s.x9[0])

	// done! (copy state)
	copy(state.pastAudio[:], input.audio[448:])

	return finalval, nil
}

// reluInPlace applies max(0, v) to every element of x.
func reluInPlace(x []float32) {
	for i, v := range x {
		if v <= 0 {
			x[i] = 0
		}
	}
}
