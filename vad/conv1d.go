package vad

import "fmt"

type Conv1d struct {
	channels_in  int
	channels_out int
	kernel       []float32 // flat; PyTorch [channels_out][channels_in][kernel_size]
	bias         []float32
	kernel_size  int
	stride       int
	padding      int
}

func (c *Conv1d) Setup(c_in int, c_out int, k_size int, stride int, padding int) {
	c.channels_in = c_in
	c.channels_out = c_out
	c.kernel_size = k_size
	c.stride = stride
	c.padding = padding

	c.kernel = make([]float32, c.channels_out*c.channels_in*c.kernel_size)
	c.bias = make([]float32, c.channels_out)
}

func (c *Conv1d) LoadWeights(weights, bias []float32) error {
	expected := c.channels_out * c.channels_in * c.kernel_size

	if len(weights) != expected {
		return fmt.Errorf(
			"Conv1d: expected %d weights, got %d",
			expected, len(weights),
		)
	}

	if bias != nil && len(bias) != c.channels_out {
		return fmt.Errorf(
			"Conv1d: expected %d biases, got %d",
			c.channels_out, len(bias),
		)
	}

	// PyTorch layout: [channels_out][channels_in][kernel_size]
	copy(c.kernel, weights)

	if bias != nil {
		copy(c.bias, bias)
	}

	return nil
}

// repackKernel converts a kernel buffer from the PyTorch layout
// [channels_out][channels_in][kernel_size] to [channels_out][kernel_size][channels_in]
// so that the innermost loops walk contiguously.
func repackKernel(kernel []float32, channelsOut, channelsIn, kernelSize int) []float32 {
	out := make([]float32, len(kernel))
	size := channelsIn
	for o := 0; o < channelsOut; o++ {
		for t := 0; t < kernelSize; t++ {
			for i := 0; i < channelsIn; i++ {
				out[o*size*kernelSize+t*size+i] = kernel[o*size*kernelSize+i*kernelSize+t]
			}
		}
	}
	return out
}
