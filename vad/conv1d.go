package vad

import "fmt"

type Conv1d struct {
	channels_in  int
	channels_out int
	kernel       [][][]float32 // features out, features in, time
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

	c.kernel = make([][][]float32, c.channels_out)
	for i := range c.kernel {
		c.kernel[i] = make([][]float32, c.channels_in)
		for j := range c.kernel[i] {
			c.kernel[i][j] = make([]float32, c.kernel_size)
		}
	}

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
	idx := 0
	for o := 0; o < c.channels_out; o++ {
		for i := 0; i < c.channels_in; i++ {
			copy(c.kernel[o][i], weights[idx:idx+c.kernel_size])
			idx += c.kernel_size
		}
	}

	if bias != nil {
		copy(c.bias, bias)
	}

	return nil
}

func (c *Conv1d) Run(x [][]float32) [][]float32 {
	window_count := (len(x)+2*c.padding-c.kernel_size)/c.stride + 1

	out_matrix := make([][]float32, window_count)

	for w := 0; w < window_count; w++ {
		out_matrix[w] = make([]float32, c.channels_out)

		for o := 0; o < c.channels_out; o++ {
			out := c.bias[o]

			for t := 0; t < c.kernel_size; t++ {
				in_t := w*c.stride + t - c.padding

				if in_t < 0 || in_t >= len(x) {
					continue // zero padding
				}

				for i := 0; i < c.channels_in; i++ {
					out += c.kernel[o][i][t] * x[in_t][i]
				}
			}

			out_matrix[w][o] = out
		}
	}

	return out_matrix
}