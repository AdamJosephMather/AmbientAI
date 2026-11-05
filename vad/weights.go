package vad

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// Each tensor is C-order, float32, in PyTorch's native layout.
// Conv1d: [outChannels][inChannels][kernelSize].
// LSTM: [4*hiddenSize][inputSize or hiddenSize]; gates I,F,G,O.
type SileroWeights struct {
	STFT                                                 []float32
	Conv1W, Conv1B                                       []float32
	Conv2W, Conv2B                                       []float32
	Conv3W, Conv3B                                       []float32
	Conv4W, Conv4B                                       []float32
	LSTMInput, LSTMHidden, LSTMBiasInput, LSTMBiasHidden []float32
	FinalW, FinalB                                       []float32
}

const sileroMagic = "SILVAD16"
const sileroVersion uint32 = 1

func LoadSileroWeights(path string) (*SileroWeights, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	header := make([]byte, 8)
	if _, err = io.ReadFull(f, header); err != nil {
		return nil, fmt.Errorf("read magic: %w", err)
	}
	if string(header) != sileroMagic {
		return nil, fmt.Errorf("invalid Silero weights magic %q", header)
	}
	var version uint32
	if err = binary.Read(f, binary.LittleEndian, &version); err != nil {
		return nil, fmt.Errorf("read version: %w", err)
	}
	if version != sileroVersion {
		return nil, fmt.Errorf("unsupported Silero weights version %d", version)
	}

	w := new(SileroWeights)
	entries := []struct {
		name  string
		count int
		dst   *[]float32
	}{
		{"stft_conv.weight", 258 * 1 * 256, &w.STFT},
		{"conv1.weight", 128 * 129 * 3, &w.Conv1W},
		{"conv1.bias", 128, &w.Conv1B},
		{"conv2.weight", 64 * 128 * 3, &w.Conv2W},
		{"conv2.bias", 64, &w.Conv2B},
		{"conv3.weight", 64 * 64 * 3, &w.Conv3W},
		{"conv3.bias", 64, &w.Conv3B},
		{"conv4.weight", 128 * 64 * 3, &w.Conv4W},
		{"conv4.bias", 128, &w.Conv4B},
		{"lstm_cell.weight_ih", 512 * 128, &w.LSTMInput},
		{"lstm_cell.weight_hh", 512 * 128, &w.LSTMHidden},
		{"lstm_cell.bias_ih", 512, &w.LSTMBiasInput},
		{"lstm_cell.bias_hh", 512, &w.LSTMBiasHidden},
		{"final_conv.weight", 1 * 128 * 1, &w.FinalW},
		{"final_conv.bias", 1, &w.FinalB},
	}
	for _, e := range entries {
		*e.dst = make([]float32, e.count)
		if err := binary.Read(f, binary.LittleEndian, *e.dst); err != nil {
			return nil, fmt.Errorf("read %s: %w", e.name, err)
		}
	}
	var extra [1]byte
	n, err := f.Read(extra[:])
	if n != 0 || err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing data in weights file")
		}
		return nil, fmt.Errorf("check end of weights file: %w", err)
	}
	return w, nil
}
