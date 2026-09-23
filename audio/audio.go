// Package audio provides audio input sources and voice-activity
// segmentation. Sources produce mono float32 PCM samples so that
// microphone capture, WAV file playback, and tests can share the same
// downstream segmentation and recognition code.
package audio

import "context"

// Source streams mono PCM audio, one chunk of samples at a time, until ctx
// is cancelled or the source is exhausted (e.g. end of a WAV file).
type Source interface {
	// Stream returns a channel of sample chunks. Samples are in [-1, 1].
	// The channel is closed when the source has no more audio.
	Stream(ctx context.Context) (<-chan []float32, error)
	// SampleRate is the sample rate, in Hz, of samples sent on the
	// channel returned by Stream.
	SampleRate() int
	// Close releases any resources held by the source (e.g. a mic
	// device or open file).
	Close() error
}

// Segment is a single contiguous span of detected speech, ready to be
// handed to a speech recognizer.
type Segment struct {
	Samples    []float32
	SampleRate int
}
