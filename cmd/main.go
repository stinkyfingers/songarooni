// Command songlistener listens to a microphone (or a WAV file, or a
// single piece of text) and identifies which song from a supplied list is
// being announced. See plans/init.md for the full design.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"songarooni/app"
	"songarooni/audio"
	"songarooni/matcher"
	"songarooni/slideshow"
	"songarooni/songs"
	"songarooni/speech"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type cliConfig struct {
	songsPath  string
	modelPath  string
	whisperBin string
	language   string
	threads    int
	extraArgs  string

	text  string
	audio string

	sampleRate int

	vadFrameMS      int
	vadEnergy       float64
	vadMinSpeechMS  int
	vadSilenceMS    int
	vadMaxSegmentMS int
	vadPreRollMS    int
	vadPostRollMS   int

	scoreThreshold  float64
	marginThreshold float64

	slideShowParentDir   string
	defaultLogoFrequency int
	defaultInterval      int

	quiet bool
}

func parseFlags(args []string) (*cliConfig, error) {
	fs := flag.NewFlagSet("songlistener", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `songlistener identifies a song title spoken near a microphone, from a
supplied list of known titles, using whisper.cpp for offline speech
recognition and fuzzy text matching to tolerate imperfect transcription.

Usage:

  songlistener --songs songs.txt --model models/ggml-tiny.en.bin

  # Development modes, no microphone or whisper.cpp required for --text:
  songlistener --songs songs.txt --text "let's play honky tonk woman"
  songlistener --songs songs.txt --model models/ggml-tiny.en.bin --audio test.wav

Flags:`)
		fs.PrintDefaults()
	}

	cfg := &cliConfig{}
	fs.StringVar(&cfg.songsPath, "songs", "songs.txt", "path to the song list file (one title per line)")
	fs.StringVar(&cfg.modelPath, "model", "", "path to a whisper.cpp ggml model file (required unless --text is used)")
	fs.StringVar(&cfg.whisperBin, "whisper-bin", "whisper-cli", "path to the whisper.cpp CLI binary")
	fs.StringVar(&cfg.language, "language", "en", "spoken language passed to whisper.cpp")
	fs.IntVar(&cfg.threads, "threads", 0, "threads passed to whisper.cpp (0 = whisper.cpp's own default)")
	fs.StringVar(&cfg.extraArgs, "whisper-args", "", "extra arguments passed through to whisper.cpp, space-separated")

	fs.StringVar(&cfg.text, "text", "", "run only the matcher against this text and exit (no audio/whisper needed)")
	fs.StringVar(&cfg.audio, "audio", "", "process a single prerecorded 16kHz mono WAV file and exit, instead of listening live")

	fs.IntVar(&cfg.sampleRate, "sample-rate", 44100, "microphone capture sample rate, in Hz (whisper.cpp expects 16kHz)")

	fs.IntVar(&cfg.vadFrameMS, "vad-frame-ms", 30, "VAD analysis frame size, in milliseconds")
	fs.Float64Var(&cfg.vadEnergy, "vad-energy-threshold", 0.02, "RMS energy above which a frame is considered speech")
	fs.IntVar(&cfg.vadMinSpeechMS, "vad-min-speech-ms", 250, "minimum speech duration to keep a segment, in milliseconds")
	fs.IntVar(&cfg.vadSilenceMS, "vad-silence-ms", 500, "trailing silence duration required to end a segment, in milliseconds")
	fs.IntVar(&cfg.vadMaxSegmentMS, "vad-max-segment-ms", 8000, "maximum segment duration, in milliseconds")
	fs.IntVar(&cfg.vadPreRollMS, "vad-pre-roll-ms", 200, "audio kept before detected speech onset, in milliseconds")
	fs.IntVar(&cfg.vadPostRollMS, "vad-post-roll-ms", 300, "audio kept after detected speech offset, in milliseconds")

	fs.Float64Var(&cfg.scoreThreshold, "score-threshold", matcher.DefaultConfig().ScoreThreshold, "minimum match score required, in [0,1]")
	fs.Float64Var(&cfg.marginThreshold, "margin-threshold", matcher.DefaultConfig().MarginThreshold, "minimum score margin over the second-best candidate required")

	// slide show flags
	fs.StringVar(&cfg.slideShowParentDir, "slideshow-dir", path.Join("/", "media", "admin", "8.0 GB Volume", "CEAI"), "parent directory for slideshow images")
	fs.IntVar(&cfg.defaultLogoFrequency, "slideshow-logo-frequency", 5, "insert logo after every X images")
	fs.IntVar(&cfg.defaultInterval, "slideshow-interval", 7, "seconds between slides")

	fs.BoolVar(&cfg.quiet, "quiet", false, "suppress progress logging; only print SONG_MATCH lines")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return cfg, nil
}

func run() error {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	logger := log.New(os.Stderr, "", 0)
	if cfg.quiet {
		logger.SetOutput(discardWriter{})
	}

	if cfg.defaultLogoFrequency <= 0 {
		return fmt.Errorf("--slideshow-logo-frequency must be > 0")
	}
	if cfg.defaultInterval <= 0 {
		return fmt.Errorf("--slideshow-interval must be > 0")
	}

	logger.Println("Loading song list...")
	songList, err := songs.LoadFile(cfg.songsPath)
	if err != nil {
		return fmt.Errorf("load song list: %w", err)
	}

	m := matcher.New(songList, matcher.Config{
		ScoreThreshold:  cfg.scoreThreshold,
		MarginThreshold: cfg.marginThreshold,
	})

	if cfg.text != "" {
		return runTextMode(cfg, m)
	}

	if cfg.modelPath == "" {
		return fmt.Errorf("--model is required unless --text is used")
	}

	logger.Println("Loading Whisper model...")
	recognizer, err := speech.NewWhisperCppRecognizer(speech.Config{
		BinaryPath: cfg.whisperBin,
		ModelPath:  cfg.modelPath,
		Language:   cfg.language,
		Threads:    cfg.threads,
		ExtraArgs:  splitArgs(cfg.extraArgs),
	})
	if err != nil {
		return fmt.Errorf("configure whisper.cpp: %w", err)
	}

	slideshowChan := make(chan string, 1)
	quit := make(chan struct{})
	syscallChan := make(chan os.Signal, 1)
	signal.Notify(syscallChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-syscallChan
		close(quit)
	}()

	go func() {
		slideShowErr := slideshow.Run(cfg.slideShowParentDir, cfg.defaultLogoFrequency, cfg.defaultInterval, slideshowChan, quit)
		if slideShowErr != nil {
			logger.Printf("Slideshow error: %v", slideShowErr)
		}
	}()

	matchHandler := func(event app.MatchEvent) {
		if event.Title != "" {
			logger.Printf("KABOOM MATCH: %s (score %.2f)", event.Title, event.Score)
		}
		// Non-blocking: the slideshow is a best-effort side effect and
		// must never be able to stall song recognition (or shutdown) —
		// e.g. if its goroutine exited (missing --slideshow-dir) or is
		// still busy handling the previous match.
		select {
		case slideshowChan <- event.Title:
		default:
			logger.Printf("slideshow busy or unavailable; dropped match for %q", event.Title)
		}
	}

	if cfg.audio != "" {
		if err = runAudioFileMode(cfg, m, recognizer, logger, matchHandler); err != nil {
			return fmt.Errorf("audio file mode error: %w", err)
		}
		time.Sleep(time.Second)
		return nil
	}

	if err = runLiveMode(cfg, m, recognizer, logger, matchHandler); err != nil {
		return fmt.Errorf("live mode error: %w", err)
	}
	time.Sleep(time.Second)
	return nil
}

// runTextMode implements `songlistener --text "..."`, running only the
// matcher: useful for developing/tuning the matcher without whisper.cpp
// or a microphone.
func runTextMode(cfg *cliConfig, m *matcher.Matcher) error {
	result := m.Match(cfg.text)
	printResult(log.New(os.Stdout, "", 0), result)
	if !result.Matched {
		os.Exit(1)
	}
	return nil
}

// runAudioFileMode implements `songlistener --audio test.wav`: processes
// one prerecorded WAV file through VAD + whisper.cpp + the matcher, then
// exits. Useful for development and regression testing without a live
// microphone.
func runAudioFileMode(cfg *cliConfig, m *matcher.Matcher, recognizer speech.Recognizer, logger *log.Logger, matchHandler func(app.MatchEvent)) error {
	src, err := audio.NewWAVSource(cfg.audio, 0)
	if err != nil {
		return fmt.Errorf("load audio file: %w", err)
	}
	defer src.Close()

	if src.SampleRate() != cfg.sampleRate {
		logger.Printf("warning: %s is %dHz, not the expected %dHz; whisper.cpp expects 16kHz mono audio",
			cfg.audio, src.SampleRate(), cfg.sampleRate)
	}

	pipeline := &app.Pipeline{
		Source:     src,
		Segmenter:  audio.NewSegmenter(vadConfigFrom(cfg, src.SampleRate())),
		Recognizer: recognizer,
		Matcher:    m,
		Logger:     logger,
		OnMatch:    matchHandler,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return pipeline.Run(ctx)
}

// runLiveMode implements the default, continuous microphone-listening
// behavior.
func runLiveMode(cfg *cliConfig, m *matcher.Matcher, recognizer speech.Recognizer, logger *log.Logger, matchHandler func(app.MatchEvent)) error {
	logger.Println("Opening microphone...")
	mic, err := audio.NewMicSource(cfg.sampleRate)
	if err != nil {
		return fmt.Errorf("open microphone: %w", err)
	}
	defer mic.Close()

	pipeline := &app.Pipeline{
		Source:     mic,
		Segmenter:  audio.NewSegmenter(vadConfigFrom(cfg, cfg.sampleRate)),
		Recognizer: recognizer,
		Matcher:    m,
		Logger:     logger,
		OnMatch:    matchHandler,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return pipeline.Run(ctx)
}

func vadConfigFrom(cfg *cliConfig, sampleRate int) audio.VADConfig {
	return audio.VADConfig{
		SampleRate:      sampleRate,
		FrameMS:         cfg.vadFrameMS,
		EnergyThreshold: cfg.vadEnergy,
		MinSpeechMS:     cfg.vadMinSpeechMS,
		SilenceMS:       cfg.vadSilenceMS,
		MaxSegmentMS:    cfg.vadMaxSegmentMS,
		PreRollMS:       cfg.vadPreRollMS,
		PostRollMS:      cfg.vadPostRollMS,
	}
}

func printResult(logger *log.Logger, result matcher.Result) {
	logger.Printf("Transcript: %q\n", result.Transcript)
	logger.Println()
	logger.Println("Candidate matches:")
	if result.Best.Title != "" {
		logger.Printf("  %-22s %.2f\n", result.Best.Title, result.Best.Score)
	}
	if result.Second.Title != "" {
		logger.Printf("  %-22s %.2f\n", result.Second.Title, result.Second.Score)
	}
	logger.Println()
	if result.Matched {
		logger.Printf("MATCH: %s\n", result.Best.Title)
		fmt.Printf("SONG_MATCH: %s\n", result.Best.Title)
		return
	}
	logger.Println("No confident song match.")
}

func splitArgs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Fields(s)
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
