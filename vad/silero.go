package vad

import (
	"errors"
	"fmt"
	"math"
)

type Silero struct {
	stft_conv Conv1d
	conv1 Conv1d
	conv2 Conv1d
	conv3 Conv1d
	conv4 Conv1d
	
	lstm_cell LSTMCell
	final_conv Conv1d
	
	setup bool
}

type SileroState struct {
	pastAudio [64]float32
	lstmstate LSTMCellState
}

type SileroInput struct {
	audio [512]float32
}

func (s *SileroState) Reset() {
	for i := range(len(s.pastAudio)) {
		s.pastAudio[i] = 0;
	}
	s.lstmstate.Reset(128) // chanels out
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
	
	s.setup = true
	return state, nil
}

func (s *Silero) Run(state *SileroState, input *SileroInput) (float32, error) {
	if (!s.setup) {
		return 0, errors.New("Silero VAD not setup, call Setup before Run")
	}
	
	reflected := reflect(&input.audio);
	
	x := make([][]float32, 640)
	for i := range x {
		x[i] = make([]float32, 1);
	}
	
	// Copy the audio int x
	
	for i, val := range state.pastAudio {
		x[i][0] = val
	}
	
	for i, val := range input.audio {
		x[i+64][0] = val
	}
	
	for i, val := range reflected {
		x[i+64+512][0] = val
	}
	
	// Stft
	
	x1 := s.stft_conv.Run(x)
	
	fail := validate(x1, 4, 258, "X1")
	if (fail != nil) {
		return 0, fail
	}
	
	x2 := make([][]float32, len(x1))
	
	// extracts the imaginary and real components, and does a pythag length calc
	
	for t := range x1 {
		x2[t] = make([]float32, 129)
	
		for f := range 129 {
			real := x1[t][f]
			imag := x1[t][f+129]
	
			x2[t][f] = float32(math.Sqrt(
				float64(real*real + imag*imag),
			))
		}
	}
	
	fail = validate(x2, 4, 129, "X2")
	if (fail != nil) {
		return 0, fail
	}
	
	// conv layers
	
	x3 := relu(s.conv1.Run(x2))
	
	fail = validate(x3, 4, 128, "X3")
	if (fail != nil) {
		return 0, fail
	}
	
	x4 := relu(s.conv2.Run(x3))
	
	fail = validate(x4, 2, 64, "X4")
	if (fail != nil) {
		return 0, fail
	}
	
	x5 := relu(s.conv3.Run(x4))
	
	fail = validate(x5, 1, 64, "X5")
	if (fail != nil) {
		return 0, fail
	}
	
	x6 := relu(s.conv4.Run(x5))
	
	fail = validate(x6, 1, 128, "X6")
	if (fail != nil) {
		return 0, fail
	}
	
	// lstm
	
	x7 := s.lstm_cell.Run(x6, &state.lstmstate)
	
	fail = validate(x7, 1, 128, "X7")
	if (fail != nil) {
		return 0, fail
	}
	
	x8 := relu(x7)
	x9 := s.final_conv.Run(x8)
	
	fail = validate(x9, 1, 1, "X9")
	if (fail != nil) {
		return 0, fail
	}
	
	finalval := sigmoid(x9[0][0])
	
	// done! (copy state)
	
	copy(state.pastAudio[:], input.audio[448:])
	
	return finalval, nil
}

func validate(x [][]float32, dim1, dim2 int, at string) error {
	gotdim2 := 0
	if (len(x) != 0) {
		gotdim2 = len(x[0])
	}
	
	if (len(x) != dim1 || gotdim2 != dim2) {
		return fmt.Errorf("Dimentions don't match expected %d x %d (got %d x %d) at %s", dim1, dim2, len(x), gotdim2, at)
	}
	return nil
}

func relu(x [][]float32) [][]float32 { // consider inplace instead of this realloc
	out := make([][]float32, len(x))
	
	for j,row := range(x) {
		out[j] = make([]float32, len(row))
		for i,v := range(row) {
			if (v > 0) {
				out[j][i] = v;
			}
		}
	}
	
	
	return out
}

func reflect(inpt *[512]float32) [64]float32 {
	var out [64]float32
	
	for i := range 64 {
		out[i] = inpt[512-i-2] // ie from [0, 1, 2, 3, 4] we would get [3, 2, 1]
	}
	
	return out
}