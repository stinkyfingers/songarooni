package audio

import (
	"context"
	"testing"
	"time"
)

const testSampleRate = 16000

// tone generates n samples of constant amplitude, standing in for speech
// energy without needing a real recording.
func tone(n int, amplitude float32) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = amplitude
	}
	return s
}

// feed sends samples to a channel in fixed-size chunks and closes it.
func feed(t *testing.T, samples []float32, chunkSize int) <-chan []float32 {
	t.Helper()
	out := make(chan []float32, 1024)
	for start := 0; start < len(samples); start += chunkSize {
		end := min(start+chunkSize, len(samples))
		out <- samples[start:end]
	}
	close(out)
	return out
}

func collect(t *testing.T, ch <-chan Segment) []Segment {
	t.Helper()
	var segs []Segment
	timeout := time.After(2 * time.Second)
	for {
		select {
		case seg, ok := <-ch:
			if !ok {
				return segs
			}
			segs = append(segs, seg)
		case <-timeout:
			t.Fatal("timed out waiting for segments")
			return nil
		}
	}
}

func TestSegmenter_DetectsOneSpeechSegment(t *testing.T) {
	cfg := DefaultVADConfig(testSampleRate)
	cfg.MinSpeechMS = 100
	cfg.SilenceMS = 200
	cfg.PostRollMS = 0
	cfg.PreRollMS = 0
	seg := NewSegmenter(cfg)

	var samples []float32
	samples = append(samples, tone(msSamples(500, testSampleRate), 0.0)...) // leading silence
	samples = append(samples, tone(msSamples(600, testSampleRate), 0.5)...) // speech
	samples = append(samples, tone(msSamples(800, testSampleRate), 0.0)...) // trailing silence

	in := feed(t, samples, 320)
	segs := collect(t, seg.Run(context.Background(), in))

	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1", len(segs))
	}
	if segs[0].SampleRate != testSampleRate {
		t.Errorf("segment sample rate = %d, want %d", segs[0].SampleRate, testSampleRate)
	}
	if len(segs[0].Samples) == 0 {
		t.Errorf("segment has no samples")
	}
}

func TestSegmenter_IgnoresShortBlip(t *testing.T) {
	cfg := DefaultVADConfig(testSampleRate)
	cfg.MinSpeechMS = 300
	cfg.SilenceMS = 150
	seg := NewSegmenter(cfg)

	var samples []float32
	samples = append(samples, tone(msSamples(300, testSampleRate), 0.0)...)
	samples = append(samples, tone(msSamples(60, testSampleRate), 0.5)...) // too short to count
	samples = append(samples, tone(msSamples(500, testSampleRate), 0.0)...)

	in := feed(t, samples, 320)
	segs := collect(t, seg.Run(context.Background(), in))

	if len(segs) != 0 {
		t.Fatalf("got %d segments for a too-short blip, want 0", len(segs))
	}
}

func TestSegmenter_MaxSegmentDuration(t *testing.T) {
	cfg := DefaultVADConfig(testSampleRate)
	cfg.MinSpeechMS = 100
	cfg.MaxSegmentMS = 1000
	cfg.SilenceMS = 100000 // effectively disabled, force max-duration cutoff
	seg := NewSegmenter(cfg)

	samples := tone(msSamples(3000, testSampleRate), 0.5) // continuous "speech"

	in := feed(t, samples, 320)
	segs := collect(t, seg.Run(context.Background(), in))

	if len(segs) < 2 {
		t.Fatalf("got %d segments for continuous speech, want >= 2 (max-duration should split it)", len(segs))
	}
}

func msSamples(ms, sampleRate int) int {
	return ms * sampleRate / 1000
}
