package audio

import (
	"context"
	"math"
)

// VADConfig controls the energy-based voice-activity segmenter. All
// durations are in milliseconds so they read naturally as CLI flags.
type VADConfig struct {
	// SampleRate must match the sample rate of the incoming audio.
	SampleRate int
	// FrameMS is the analysis frame size used to decide speech/silence.
	FrameMS int
	// EnergyThreshold is the RMS energy (samples in [-1, 1]) above which
	// a frame is considered speech. Needs tuning per microphone/room.
	EnergyThreshold float64
	// MinSpeechMS is the minimum accumulated speech duration before a
	// segment is considered worth keeping.
	MinSpeechMS int
	// SilenceMS is how much continuous silence ends a segment.
	SilenceMS int
	// MaxSegmentMS caps segment length so a stuck-open segment (e.g.
	// continuous background music) can't grow unbounded.
	MaxSegmentMS int
	// PreRollMS is how much audio before speech onset is kept, so the
	// first word isn't clipped.
	PreRollMS int
	// PostRollMS is how much trailing audio after speech offset is kept.
	PostRollMS int
}

// DefaultVADConfig returns reasonable starting values for a talkback mic
// at 16kHz. These are intentionally conservative; tune EnergyThreshold
// for the actual venue/microphone.
func DefaultVADConfig(sampleRate int) VADConfig {
	return VADConfig{
		SampleRate:      sampleRate,
		FrameMS:         30,
		EnergyThreshold: 0.02,
		MinSpeechMS:     250,
		SilenceMS:       500,
		MaxSegmentMS:    8000,
		PreRollMS:       200,
		PostRollMS:      300,
	}
}

// Segmenter consumes a stream of raw sample chunks and emits complete
// speech segments once enough trailing silence is seen. It is a small,
// dependency-free energy-based VAD: on a Raspberry Pi 3 this is cheap
// enough to run continuously, avoiding sending non-speech audio to
// Whisper. whisper.cpp's own (Silero-based) VAD is a possible future
// upgrade if this proves too coarse.
type Segmenter struct {
	cfg       VADConfig
	frameSize int
}

// NewSegmenter builds a Segmenter for cfg.
func NewSegmenter(cfg VADConfig) *Segmenter {
	frameSize := cfg.SampleRate * cfg.FrameMS / 1000
	if frameSize < 1 {
		frameSize = 1
	}
	return &Segmenter{cfg: cfg, frameSize: frameSize}
}

// Run reads chunks from in and writes completed speech segments to the
// returned channel, which is closed once in is closed/drained or ctx is
// cancelled.
func (s *Segmenter) Run(ctx context.Context, in <-chan []float32) <-chan Segment {
	out := make(chan Segment)
	go func() {
		defer close(out)
		s.process(ctx, in, out)
	}()
	return out
}

type segState int

const (
	stateSilence segState = iota
	stateSpeech
)

func (s *Segmenter) process(ctx context.Context, in <-chan []float32, out chan<- Segment) {
	cfg := s.cfg
	frameMS := cfg.FrameMS
	if frameMS <= 0 {
		frameMS = 30
	}

	preRollFrames := cfg.PreRollMS / frameMS
	silenceFramesToEnd := max(1, cfg.SilenceMS/frameMS)
	minSpeechFrames := max(1, cfg.MinSpeechMS/frameMS)
	maxSegmentFrames := max(1, cfg.MaxSegmentMS/frameMS)
	postRollFrames := cfg.PostRollMS / frameMS

	var (
		state          = stateSilence
		preRoll        = newRingBuffer(preRollFrames)
		segment        []float32
		speechFrames   int
		silenceFrames  int
		framesSinceEnd int // frames accumulated for post-roll once speech ends
	)

	// buf accumulates raw samples until a full analysis frame is ready;
	// input chunk sizes (e.g. from a mic driver) need not align with
	// frameSize.
	var buf []float32

	flush := func() {
		if speechFrames >= minSpeechFrames {
			out <- Segment{Samples: segment, SampleRate: cfg.SampleRate}
		}
		segment = nil
		speechFrames = 0
		silenceFrames = 0
		framesSinceEnd = 0
		state = stateSilence
	}

	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-in:
			if !ok {
				if state == stateSpeech {
					flush()
				}
				return
			}
			buf = append(buf, chunk...)

			for len(buf) >= s.frameSize {
				frame := buf[:s.frameSize]
				buf = buf[s.frameSize:]

				isSpeech := rms(frame) >= cfg.EnergyThreshold

				switch state {
				case stateSilence:
					preRoll.push(frame)
					if isSpeech {
						state = stateSpeech
						segment = preRoll.drain()
						segment = append(segment, frame...)
						speechFrames = 1
						silenceFrames = 0
					}

				case stateSpeech:
					segment = append(segment, frame...)
					if isSpeech {
						speechFrames++
						silenceFrames = 0
						framesSinceEnd = 0
					} else {
						silenceFrames++
						framesSinceEnd++
					}

					reachedSilenceEnd := silenceFrames >= silenceFramesToEnd
					reachedMax := speechFrames+silenceFrames >= maxSegmentFrames
					reachedPostRoll := reachedSilenceEnd && framesSinceEnd >= postRollFrames

					if reachedMax || reachedPostRoll {
						flush()
					}
				}
			}
		}
	}
}

func rms(frame []float32) float64 {
	if len(frame) == 0 {
		return 0
	}
	var sum float64
	for _, s := range frame {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(frame)))
}

// ringBuffer holds the most recent N frames for pre-roll, so audio just
// before speech onset is not lost.
type ringBuffer struct {
	frames [][]float32
	cap    int
}

func newRingBuffer(capFrames int) *ringBuffer {
	if capFrames < 0 {
		capFrames = 0
	}
	return &ringBuffer{cap: capFrames}
}

func (r *ringBuffer) push(frame []float32) {
	if r.cap == 0 {
		return
	}
	cp := make([]float32, len(frame))
	copy(cp, frame)
	r.frames = append(r.frames, cp)
	if len(r.frames) > r.cap {
		r.frames = r.frames[len(r.frames)-r.cap:]
	}
}

// drain returns the buffered pre-roll audio as one flat slice and clears
// the buffer.
func (r *ringBuffer) drain() []float32 {
	var out []float32
	for _, f := range r.frames {
		out = append(out, f...)
	}
	r.frames = nil
	return out
}
