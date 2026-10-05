// Package app wires audio capture/segmentation, speech recognition, and
// song matching together, and defines the "song identified" event so new
// output sinks (HTTP, MQTT, GPIO, ...) can be added later without
// touching the pipeline itself.
package app

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"time"

	"songarooni/audio"
	"songarooni/matcher"
	"songarooni/speech"
)

// MatchEvent is emitted whenever the matcher confidently identifies a
// song. It's the seed of a machine-readable event a future output sink
// (HTTP, MQTT, GPIO, a JSON log, ...) would consume.
type MatchEvent struct {
	Title      string
	Score      float64
	Transcript string
}

// Pipeline connects an audio Source through VAD segmentation, speech
// recognition, and song matching.
type Pipeline struct {
	Source     audio.Source
	Segmenter  *audio.Segmenter
	Recognizer speech.Recognizer
	Matcher    *matcher.Matcher

	// TempDir is where per-segment WAV files are written before being
	// handed to Recognizer. Defaults to os.TempDir() if empty.
	TempDir string

	// OnMatch is called for every confident song match. Additional sinks
	// (HTTP, MQTT, GPIO, ...) can be layered on by wrapping or extending
	// this callback; it is not implemented here beyond stdout logging,
	// per plans/init.md.
	OnMatch func(MatchEvent)

	Logger     *log.Logger
	AudioDebug bool
}

var (
	logAudioLevelFreq = time.Second * 5
)

// Run streams audio from Source, and for every speech segment the
// Segmenter emits, transcribes it and attempts a song match, logging
// progress and emitting SONG_MATCH lines to stdout as described in
// plans/init.md. Run blocks until ctx is cancelled or the source is
// exhausted.
func (p *Pipeline) Run(ctx context.Context) error {
	logger := p.Logger
	if logger == nil {
		logger = log.Default()
	}

	samples, err := p.Source.Stream(ctx)
	if err != nil {
		return fmt.Errorf("start audio source: %w", err)
	}

	logger.Println("Listening...")
	if p.AudioDebug {
		samples = logAudioLevels(ctx, logger, samples) // enable to log levels
	}
	segments := p.Segmenter.Run(ctx, samples)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case seg, ok := <-segments:
			if !ok {
				return nil
			}
			p.handleSegmentSafely(ctx, seg, logger)
		}
	}
}

// handleSegmentSafely recovers from a panic processing a single segment,
// so one bad segment (or a bug anywhere in the Recognizer/Matcher/
// OnMatch chain) logs and moves on to the next one instead of taking
// down an otherwise fine, long-running live show.
func (p *Pipeline) handleSegmentSafely(ctx context.Context, seg audio.Segment, logger *log.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Printf("recovered from panic handling segment: %v", r)
		}
	}()
	p.handleSegment(ctx, seg, logger)
}

func (p *Pipeline) handleSegment(ctx context.Context, seg audio.Segment, logger *log.Logger) {
	logger.Println()
	logger.Println("Speech detected")

	wavPath, err := p.writeSegment(seg)
	if err != nil {
		logger.Printf("failed to write segment audio: %v", err)
		return
	}
	defer os.Remove(wavPath)

	logger.Println("Transcribing...")
	transcript, err := p.Recognizer.Transcribe(ctx, wavPath)
	if err != nil {
		if err == speech.ErrEmptyTranscript {
			logger.Println("No speech recognized in segment.")
			return
		}
		logger.Printf("transcription failed: %v", err)
		return
	}
	logger.Printf("Transcript: %q\n", transcript)

	result := p.Matcher.Match(transcript)
	logger.Println()
	logger.Println("Candidate matches:")
	if result.Best.Title != "" {
		logger.Printf("  %-22s %.2f\n", result.Best.Title, result.Best.Score)
	}
	if result.Second.Title != "" {
		logger.Printf("  %-22s %.2f\n", result.Second.Title, result.Second.Score)
	}

	if !result.Matched {
		logger.Println()
		logger.Println("No confident song match.")
		return
	}

	logger.Println()
	logger.Printf("MATCH: %s\n", result.Best.Title)

	if p.OnMatch != nil {
		p.OnMatch(MatchEvent{
			Title:      result.Best.Title,
			Score:      result.Best.Score,
			Transcript: transcript,
		})
	}
}

func (p *Pipeline) writeSegment(seg audio.Segment) (string, error) {
	dir := p.TempDir
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "songlistener-segment-*.wav")
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()

	if err := audio.WriteWAVFile(path, seg.Samples, seg.SampleRate); err != nil {
		os.Remove(path)
		return "", err
	}
	return filepath.Clean(path), nil
}

// logAudioLevels passes samples through unchanged, but once a second logs
// the RMS level captured over that second. It exists to make a "no
// speech ever detected" report diagnosable without extra tooling: a
// level that stays near zero points at a routing/gain problem upstream
// of this program (wrong ALSA channel/device, muted or zero mixer gain);
// a small but nonzero, consistent level that never trips VAD points at
// --vad-energy-threshold being set too high for the real signal instead.
func logAudioLevels(ctx context.Context, logger *log.Logger, in <-chan []float32) <-chan []float32 {
	out := make(chan []float32)

	go func() {
		defer close(out)

		var sumSquares float64
		var count int
		report := func() {
			if count == 0 {
				logger.Println("audio level: no samples received in the last second")
				return
			}
			rms := math.Sqrt(sumSquares / float64(count))
			logger.Printf("audio level: rms=%.4f (compare against --vad-energy-threshold)", rms)
			sumSquares, count = 0, 0
		}

		ticker := time.NewTicker(logAudioLevelFreq)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-in:
				if !ok {
					report()
					return
				}
				for _, s := range chunk {
					sumSquares += float64(s) * float64(s)
				}
				count += len(chunk)
				select {
				case out <- chunk:
				case <-ctx.Done():
					return
				}
			case <-ticker.C:
				report()
			}
		}
	}()

	return out
}
