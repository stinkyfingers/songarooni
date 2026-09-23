package audio

import (
	"context"
	"math"
	"path/filepath"
	"testing"
)

func TestWriteReadWAVRoundTrip(t *testing.T) {
	const sampleRate = 16000
	samples := make([]float32, sampleRate/2) // 0.5s
	for i := range samples {
		samples[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / sampleRate))
	}

	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := WriteWAVFile(path, samples, sampleRate); err != nil {
		t.Fatalf("WriteWAVFile: %v", err)
	}

	got, rate, err := ReadWAVFile(path)
	if err != nil {
		t.Fatalf("ReadWAVFile: %v", err)
	}
	if rate != sampleRate {
		t.Fatalf("sample rate = %d, want %d", rate, sampleRate)
	}
	if len(got) != len(samples) {
		t.Fatalf("got %d samples, want %d", len(got), len(samples))
	}
	for i := range samples {
		if diff := math.Abs(float64(got[i] - samples[i])); diff > 1e-3 {
			t.Fatalf("sample %d = %v, want %v (diff %v)", i, got[i], samples[i], diff)
		}
	}
}

func TestWAVSourceStream(t *testing.T) {
	const sampleRate = 16000
	samples := make([]float32, 1000)
	for i := range samples {
		samples[i] = 0.5
	}
	path := filepath.Join(t.TempDir(), "flat.wav")
	if err := WriteWAVFile(path, samples, sampleRate); err != nil {
		t.Fatalf("WriteWAVFile: %v", err)
	}

	src, err := NewWAVSource(path, 100)
	if err != nil {
		t.Fatalf("NewWAVSource: %v", err)
	}
	defer src.Close()

	ch, err := src.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var total int
	for chunk := range ch {
		total += len(chunk)
	}
	if total != len(samples) {
		t.Fatalf("streamed %d samples, want %d", total, len(samples))
	}
}
