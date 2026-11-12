package audio

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"mime"
	"strconv"

	ebaudio "github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/audio/wav"
	"github.com/hajimehoshi/go-mp3"
)

const maxDecodedBytes = 128 << 20

// DecodePCM normalizes a provider response to signed 16-bit LE stereo PCM at
// sampleRate. Raw PCM needs explicit format metadata; guessing changes pitch.
// WAV and MP3 carry their own sample rate and channel information.
func DecodePCM(data []byte, contentType string, sampleRate int) ([]byte, error) {
	source, err := PCMReader(bytes.NewReader(data), contentType, sampleRate)
	if err != nil {
		return nil, err
	}
	pcm, err := io.ReadAll(source)
	if err != nil {
		return nil, fmt.Errorf("read decoded TTS audio: %w", err)
	}
	return pcm, nil
}

// PCMReader decodes and resamples incrementally. It never reads the entire clip
// up front and does not close source; its owner must close it on cancellation.
func PCMReader(input io.Reader, contentType string, sampleRate int) (io.Reader, error) {
	if sampleRate <= 0 || sampleRate > 192000 {
		return nil, fmt.Errorf("invalid output sample rate %d", sampleRate)
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("invalid TTS Content-Type %q: %w", contentType, err)
	}
	var source io.Reader
	buffered := bufio.NewReader(input)
	header, peekErr := buffered.Peek(12)
	if peekErr != nil && peekErr != io.EOF {
		return nil, peekErr
	}
	switch {
	case len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WAVE",
		mediaType == "audio/wav", mediaType == "audio/x-wav", mediaType == "audio/wave":
		source, err = wav.DecodeWithSampleRate(sampleRate, buffered)
	case mediaType == "audio/mpeg", mediaType == "audio/mp3":
		// A non-seekable source prevents the MP3 decoder scanning to EOF to
		// calculate clip length. ResampleReader uses 0 for unknown length.
		var decoder *mp3.Decoder
		decoder, err = mp3.NewDecoder(buffered)
		if err == nil {
			source = decoder
			if decoder.SampleRate() != sampleRate {
				source = ebaudio.ResampleReader(source, 0, decoder.SampleRate(), sampleRate)
			}
		}
	case mediaType == "audio/pcm":
		rate, rateErr := strconv.Atoi(params["rate"])
		channels, channelsErr := strconv.Atoi(params["channels"])
		if rateErr != nil || rate <= 0 || rate > 192000 || channelsErr != nil || (channels != 1 && channels != 2) {
			return nil, fmt.Errorf("raw TTS PCM requires rate (1–192000) and channels (1 or 2) in Content-Type, got %q", contentType)
		}
		if bits := params["bits"]; bits != "" && bits != "16" {
			return nil, fmt.Errorf("unsupported PCM bit depth %q; expected signed 16-bit little-endian", bits)
		}
		source = &stereoPCMReader{source: buffered, channels: channels}
		if rate != sampleRate {
			source = ebaudio.ResampleReader(source, 0, rate, sampleRate)
		}
	default:
		return nil, fmt.Errorf("unsupported TTS audio Content-Type %q; expected PCM, MP3, or WAV", contentType)
	}
	if err != nil {
		return nil, fmt.Errorf("decode TTS audio: %w", err)
	}
	return &checkedPCMReader{source: source}, nil
}

// Keep incomplete network reads away from the stereo resampler and preserve
// sample boundaries even when an HTTP chunk ends in the middle of a sample.
type stereoPCMReader struct {
	source   io.Reader
	channels int
	pending  []byte
	err      error
}

func (r *stereoPCMReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.pending) == 0 && r.err == nil {
		var buf [2048]byte
		n, err := io.ReadFull(r.source, buf[:])
		if err == io.ErrUnexpectedEOF && n%(r.channels*2) == 0 {
			err = io.EOF
		}
		if n%(r.channels*2) != 0 {
			err = fmt.Errorf("TTS PCM contains an incomplete audio frame")
		}
		r.err = err
		n -= n % (r.channels * 2)
		if r.channels == 2 {
			r.pending = append(r.pending, buf[:n]...)
		} else {
			r.pending = make([]byte, n*2)
			for i := 0; i < n; i += 2 {
				v := binary.LittleEndian.Uint16(buf[i:])
				binary.LittleEndian.PutUint16(r.pending[i*2:], v)
				binary.LittleEndian.PutUint16(r.pending[i*2+2:], v)
			}
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	if n > 0 {
		return n, nil
	}
	return 0, r.err
}

type checkedPCMReader struct {
	source io.Reader
	read   int64
}

func (r *checkedPCMReader) Read(p []byte) (int, error) {
	if r.read > maxDecodedBytes {
		return 0, fmt.Errorf("decoded TTS audio exceeds size limit")
	}
	if int64(len(p)) > maxDecodedBytes-r.read+1 {
		p = p[:maxDecodedBytes-r.read+1]
	}
	n, err := r.source.Read(p)
	r.read += int64(n)
	if r.read > maxDecodedBytes {
		return n, fmt.Errorf("decoded TTS audio exceeds size limit")
	}
	if err == io.EOF && (r.read == 0 || r.read%4 != 0) {
		return n, fmt.Errorf("decoded TTS audio is empty or contains an incomplete stereo frame")
	}
	return n, err
}
