// Package app wires audio capture/segmentation, speech recognition, and
// song matching together, and defines the "song identified" event so new
// output sinks (HTTP, MQTT, GPIO, ...) can be added later without
// touching the pipeline itself.
package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

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

	Logger *log.Logger
}

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
	segments := p.Segmenter.Run(ctx, samples)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case seg, ok := <-segments:
			if !ok {
				return nil
			}
			p.handleSegment(ctx, seg, logger)
		}
	}
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
	fmt.Printf("SONG_MATCH: %s\n", result.Best.Title)

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
