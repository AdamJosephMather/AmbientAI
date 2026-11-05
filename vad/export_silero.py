#!/usr/bin/env python3
"""Export official Silero tinygrad-compatible checkpoint as little-endian f32."""
import argparse
import struct
import urllib.request
from pathlib import Path

import numpy as np
from safetensors.numpy import load_file

URL = ('https://raw.githubusercontent.com/snakers4/silero-vad/master/'
       'src/silero_vad/data/silero_vad_16k.safetensors')
MAGIC = b'SILVAD16'
VERSION = 1

# Shape in PyTorch convention: Conv1d [out,in,k], LSTM [4*hidden,in].
LAYOUT = [
    ('stft_conv.weight',      (258, 1, 256)),
    ('conv1.weight',          (128, 129, 3)),
    ('conv1.bias',            (128,)),
    ('conv2.weight',          (64, 128, 3)),
    ('conv2.bias',            (64,)),
    ('conv3.weight',          (64, 64, 3)),
    ('conv3.bias',            (64,)),
    ('conv4.weight',          (128, 64, 3)),
    ('conv4.bias',            (128,)),
    ('lstm_cell.weight_ih',   (512, 128)),
    ('lstm_cell.weight_hh',   (512, 128)),
    ('lstm_cell.bias_ih',     (512,)),
    ('lstm_cell.bias_hh',     (512,)),
    ('final_conv.weight',    (1, 128, 1)),
    ('final_conv.bias',      (1,)),
]

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--checkpoint', type=Path, default=Path('silero_vad_16k.safetensors'))
    ap.add_argument('--output', type=Path, default=Path('silero_vad.bin'))
    args = ap.parse_args()

    if not args.checkpoint.exists():
        print(f'Downloading {URL}')
        urllib.request.urlretrieve(URL, args.checkpoint)

    tensors = load_file(str(args.checkpoint))
    with args.output.open('wb') as f:
        f.write(MAGIC)
        f.write(struct.pack('<I', VERSION))
        for name, expected_shape in LAYOUT:
            if name not in tensors:
                raise KeyError(f'Missing tensor {name}; available: {sorted(tensors)}')
            array = tensors[name]
            if array.shape != expected_shape:
                raise ValueError(f'{name}: expected {expected_shape}, found {array.shape}')
            # Flatten in C order: output channel, input channel, kernel position.
            raw = np.ascontiguousarray(array, dtype='<f4').tobytes()
            f.write(raw)
            print(f'{name:<26} {str(array.shape):<18} {len(raw):>8} bytes')

    print(f'Wrote {args.output} ({args.output.stat().st_size} bytes)')

if __name__ == '__main__':
    main()
