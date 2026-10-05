package audio

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

// WriteWAVFile writes mono float32 samples (each in [-1, 1]) to path as a
// 16-bit PCM WAV file at sampleRate. This is used to hand a detected
// speech segment to whisper.cpp, which is invoked as a subprocess reading
// a file (see internal/speech).
func WriteWAVFile(path string, samples []float32, sampleRate int) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create wav file %q: %w", path, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)

	const (
		bitsPerSample = 16
		numChannels   = 1
	)
	byteRate := sampleRate * numChannels * bitsPerSample / 8
	blockAlign := numChannels * bitsPerSample / 8
	dataSize := len(samples) * 2 // 2 bytes per sample

	writeString(w, "RIFF")
	binary.Write(w, binary.LittleEndian, uint32(36+dataSize))
	writeString(w, "WAVE")

	writeString(w, "fmt ")
	binary.Write(w, binary.LittleEndian, uint32(16)) // PCM fmt chunk size
	binary.Write(w, binary.LittleEndian, uint16(1))  // PCM format
	binary.Write(w, binary.LittleEndian, uint16(numChannels))
	binary.Write(w, binary.LittleEndian, uint32(sampleRate))
	binary.Write(w, binary.LittleEndian, uint32(byteRate))
	binary.Write(w, binary.LittleEndian, uint16(blockAlign))
	binary.Write(w, binary.LittleEndian, uint16(bitsPerSample))

	writeString(w, "data")
	binary.Write(w, binary.LittleEndian, uint32(dataSize))
	for _, s := range samples {
		binary.Write(w, binary.LittleEndian, floatToPCM16(s))
	}

	if err := w.Flush(); err != nil {
		return fmt.Errorf("write wav file %q: %w", path, err)
	}
	return nil
}

func writeString(w io.Writer, s string) {
	io.WriteString(w, s)
}

func floatToPCM16(s float32) int16 {
	v := float64(s) * 32767
	if v > 32767 {
		v = 32767
	}
	if v < -32768 {
		v = -32768
	}
	return int16(math.Round(v))
}

// wavFmt describes the fields we care about from a WAV "fmt " chunk.
type wavFmt struct {
	audioFormat   uint16
	numChannels   uint16
	sampleRate    uint32
	bitsPerSample uint16
}

// ReadWAVFile reads a PCM WAV file and returns mono float32 samples in
// [-1, 1] along with the file's sample rate. Only 16-bit and 8-bit
// integer PCM are supported, which covers whisper.cpp's expected input
// and typical recordings; stereo files are downmixed to mono by
// averaging channels.
// maxWAVFileBytes caps the size of a WAV file ReadWAVFile will load into
// memory. --audio is a dev/testing flag, not meant for arbitrarily large
// recordings, and a Raspberry Pi 3 has limited RAM — this is generous
// headroom for real test clips (a 200MB 16kHz mono WAV is over 3 hours)
// while still refusing to load something clearly unintended rather than
// risking an OOM.
const maxWAVFileBytes = 200 * 1024 * 1024

func ReadWAVFile(path string) ([]float32, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open wav file %q: %w", path, err)
	}
	defer f.Close()

	if info, err := f.Stat(); err == nil && info.Size() > maxWAVFileBytes {
		return nil, 0, fmt.Errorf("%q is %d bytes, over the %d byte limit for --audio", path, info.Size(), maxWAVFileBytes)
	}

	r := bufio.NewReader(f)

	var riffHeader [12]byte
	if _, err := io.ReadFull(r, riffHeader[:]); err != nil {
		return nil, 0, fmt.Errorf("read wav header %q: %w", path, err)
	}
	if string(riffHeader[0:4]) != "RIFF" || string(riffHeader[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("%q is not a RIFF/WAVE file", path)
	}

	var format *wavFmt
	for {
		var chunkID [4]byte
		if _, err := io.ReadFull(r, chunkID[:]); err != nil {
			if err == io.EOF {
				break
			}
			return nil, 0, fmt.Errorf("read chunk header %q: %w", path, err)
		}
		var chunkSize uint32
		if err := binary.Read(r, binary.LittleEndian, &chunkSize); err != nil {
			return nil, 0, fmt.Errorf("read chunk size %q: %w", path, err)
		}

		if chunkSize > maxWAVFileBytes {
			return nil, 0, fmt.Errorf("%q: %s chunk claims %d bytes, over the %d byte limit", path, string(chunkID[:]), chunkSize, maxWAVFileBytes)
		}

		switch string(chunkID[:]) {
		case "fmt ":
			// A real fmt chunk is at least 16 bytes (the fixed fields
			// read below); a shorter one is truncated/malformed. Without
			// this check, a chunk size under 16 would panic on the
			// buf[14:16] read a few lines down instead of erroring.
			if chunkSize < 16 {
				return nil, 0, fmt.Errorf("%q: fmt chunk is %d bytes, too short to be valid (need at least 16)", path, chunkSize)
			}
			buf := make([]byte, chunkSize)
			if _, err := io.ReadFull(r, buf); err != nil {
				return nil, 0, fmt.Errorf("read fmt chunk %q: %w", path, err)
			}
			format = &wavFmt{
				audioFormat:   binary.LittleEndian.Uint16(buf[0:2]),
				numChannels:   binary.LittleEndian.Uint16(buf[2:4]),
				sampleRate:    binary.LittleEndian.Uint32(buf[4:8]),
				bitsPerSample: binary.LittleEndian.Uint16(buf[14:16]),
			}
		case "data":
			if format == nil {
				return nil, 0, fmt.Errorf("%q: data chunk before fmt chunk", path)
			}
			samples, err := decodePCM(r, chunkSize, *format)
			if err != nil {
				return nil, 0, fmt.Errorf("decode wav data %q: %w", path, err)
			}
			return samples, int(format.sampleRate), nil
		default:
			if _, err := io.CopyN(io.Discard, r, int64(chunkSize)); err != nil {
				return nil, 0, fmt.Errorf("skip chunk %q %q: %w", string(chunkID[:]), path, err)
			}
		}
		if chunkSize%2 == 1 {
			// Chunks are padded to even sizes.
			r.Discard(1)
		}
	}

	return nil, 0, fmt.Errorf("%q has no data chunk", path)
}

func decodePCM(r io.Reader, size uint32, format wavFmt) ([]float32, error) {
	if format.audioFormat != 1 {
		return nil, fmt.Errorf("unsupported wav audio format %d (only PCM is supported)", format.audioFormat)
	}
	channels := int(format.numChannels)
	if channels < 1 {
		return nil, fmt.Errorf("invalid channel count %d", channels)
	}

	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}

	var perSample int
	var decode func([]byte) float32
	switch format.bitsPerSample {
	case 16:
		perSample = 2
		decode = func(b []byte) float32 {
			return float32(int16(binary.LittleEndian.Uint16(b))) / 32768
		}
	case 8:
		perSample = 1
		decode = func(b []byte) float32 {
			return (float32(b[0]) - 128) / 128
		}
	default:
		return nil, fmt.Errorf("unsupported bits per sample %d", format.bitsPerSample)
	}

	frameSize := perSample * channels
	numFrames := len(buf) / frameSize
	mono := make([]float32, numFrames)
	for i := 0; i < numFrames; i++ {
		var sum float32
		for c := 0; c < channels; c++ {
			off := i*frameSize + c*perSample
			sum += decode(buf[off : off+perSample])
		}
		mono[i] = sum / float32(channels)
	}
	return mono, nil
}

// WAVSource streams the samples of a WAV file as if it were a live source,
// in fixed-size chunks. It exists so the same VAD/segmenter/matcher
// pipeline used for the microphone can be exercised in development and
// tests without any hardware.
type WAVSource struct {
	samples    []float32
	sampleRate int
	chunkSize  int
}

// NewWAVSource loads path and prepares it for streaming in chunkFrames
// sized chunks (e.g. 320 frames = 20ms at 16kHz).
func NewWAVSource(path string, chunkFrames int) (*WAVSource, error) {
	samples, rate, err := ReadWAVFile(path)
	if err != nil {
		return nil, err
	}
	if chunkFrames <= 0 {
		chunkFrames = rate / 50 // default to 20ms chunks
	}
	return &WAVSource{samples: samples, sampleRate: rate, chunkSize: chunkFrames}, nil
}

func (s *WAVSource) SampleRate() int { return s.sampleRate }

func (s *WAVSource) Close() error { return nil }

func (s *WAVSource) Stream(ctx context.Context) (<-chan []float32, error) {
	out := make(chan []float32)
	go func() {
		defer close(out)
		for start := 0; start < len(s.samples); start += s.chunkSize {
			end := min(start+s.chunkSize, len(s.samples))
			chunk := s.samples[start:end]
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
