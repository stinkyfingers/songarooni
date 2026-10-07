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
	"runtime/debug"
	"strconv"
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
	// Last-resort safety net: an unexpected panic anywhere in setup
	// (segment-handling panics are already recovered per-segment in
	// app.Pipeline) still exits cleanly with a message and stack trace,
	// rather than Go's raw, less legible default panic output.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "error: unexpected panic: %v\n%s", r, debug.Stack())
			os.Exit(1)
		}
	}()

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type cliConfig struct {
	songsPath      string
	modelPath      string
	whisperBin     string
	language       string
	threads        int
	extraArgs      string
	whisperTimeout time.Duration

	text       string
	audio      string
	audioDebug bool

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

  songlistener --songs songs.csv --model models/ggml-tiny.en.bin

  # Development modes, no microphone or whisper.cpp required for --text:
  songlistener --songs songs.csv --text "let's play honky tonk woman"
  songlistener --songs songs.csv --model models/ggml-tiny.en.bin --audio test.wav

Flags:`)
		fs.PrintDefaults()
	}

	cfg := &cliConfig{}
	fs.StringVar(&cfg.songsPath, "songs", "songs.csv", "path to the song list CSV (title,path header + rows; path is the slideshow subdirectory for that title)")
	fs.StringVar(&cfg.modelPath, "model", "", "path to a whisper.cpp ggml model file (required unless --text is used)")
	fs.StringVar(&cfg.whisperBin, "whisper-bin", "whisper-cli", "path to the whisper.cpp CLI binary")
	fs.StringVar(&cfg.language, "language", "en", "spoken language passed to whisper.cpp")
	fs.IntVar(&cfg.threads, "threads", 0, "threads passed to whisper.cpp (0 = whisper.cpp's own default)")
	fs.StringVar(&cfg.extraArgs, "whisper-args", "", "extra arguments passed through to whisper.cpp, space-separated")
	fs.DurationVar(&cfg.whisperTimeout, "whisper-timeout", speech.DefaultTimeout, "max time to wait for a single whisper.cpp invocation before giving up on that segment")

	fs.StringVar(&cfg.text, "text", "", "run only the matcher against this text and exit (no audio/whisper needed)")
	fs.StringVar(&cfg.audio, "audio", "", "process a single prerecorded 16kHz mono WAV file and exit, instead of listening live")
	fs.BoolVar(&cfg.audioDebug, "audio-debug", false, "log audio levels and prevent slideshow from starting, for debugging microphone input")

	fs.IntVar(&cfg.sampleRate, "sample-rate", 16000, "microphone capture sample rate, in Hz (whisper.cpp expects 16kHz)")

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
	fs.StringVar(&cfg.slideShowParentDir, "slideshow-dir", path.Join("/", "media", "admin", "D576-4AA5", "songarooni-images"), "parent directory for slideshow images")
	fs.IntVar(&cfg.defaultLogoFrequency, "slideshow-logo-frequency", 11, "insert logo after every X images")
	fs.IntVar(&cfg.defaultInterval, "slideshow-interval", 7, "seconds between slides")

	fs.BoolVar(&cfg.quiet, "quiet", false, "suppress progress logging; only print SONG_MATCH lines")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	// validate slideshow flags
	if cfg.defaultLogoFrequency <= 0 {
		return nil, fmt.Errorf("--slideshow-logo-frequency must be > 0")
	}
	if cfg.defaultInterval <= 0 {
		return nil, fmt.Errorf("--slideshow-interval must be > 0")
	}
	if cfg.modelPath == "" && cfg.text == "" {
		return nil, fmt.Errorf("--model is required unless --text is used")
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

	logger.Println("Loading song list...")
	songMap, err := songs.LoadFile(cfg.songsPath)
	if err != nil {
		return fmt.Errorf("load song list: %w", err)
	}

	titles := make([]string, 0, len(songMap))
	for title := range songMap {
		titles = append(titles, title)
	}

	m := matcher.New(titles, matcher.Config{
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
		Timeout:    cfg.whisperTimeout,
	})
	if err != nil {
		return fmt.Errorf("configure whisper.cpp: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var matchHandler func(app.MatchEvent)
	sh, err := slideshow.New(cfg.slideShowParentDir, songMap, cfg.defaultLogoFrequency, cfg.defaultInterval)
	if err != nil {
		return fmt.Errorf("slideshow unavailable: %w", err)
	} else {
		// debug; no slideshow if audio debug is enabled, to avoid the user discovering
		if cfg.audioDebug {
			logger.Println("audio debug mode: slideshow disabled")
			matchHandler = func(event app.MatchEvent) { logger.Printf("match: %s", event.Title) }
		} else {
			go func() {
				if err := sh.Run(ctx); err != nil {
					logger.Printf("slideshow error: %v", err)
				}
			}()
			matchHandler = func(event app.MatchEvent) { sh.Show(event.Title) }
		}
	}

	if cfg.audio != "" {
		sh.Start() // start slideshow even in audio file mode, so it can show the match
		if err = runAudioFileMode(ctx, cfg, m, recognizer, logger, matchHandler); err != nil {
			return fmt.Errorf("audio file mode error: %w", err)
		}
		time.Sleep(time.Second)
		return nil
	}

	if err = runLiveMode(ctx, cfg, m, recognizer, logger, matchHandler, sh.Start); err != nil {
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
func runAudioFileMode(ctx context.Context, cfg *cliConfig, m *matcher.Matcher, recognizer speech.Recognizer, logger *log.Logger, matchHandler func(app.MatchEvent)) error {
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
		AudioDebug: cfg.audioDebug,
	}

	return pipeline.Run(ctx)
}

// runLiveMode implements the default, continuous microphone-listening
// behavior.
func runLiveMode(ctx context.Context, cfg *cliConfig, m *matcher.Matcher, recognizer speech.Recognizer, logger *log.Logger, matchHandler func(app.MatchEvent), startSlideshow func()) error {
	logger.Println("Opening microphone...")
	mic, err := audio.NewMicSource(cfg.sampleRate)
	if err != nil {
		return fmt.Errorf("open microphone: %w", err)
	}
	defer mic.Close()

	// device selection
	err = selectDevice(mic, logger)
	if err != nil {
		return fmt.Errorf("select device: %w", err)
	}
	startSlideshow()

	pipeline := &app.Pipeline{
		Source:     mic,
		Segmenter:  audio.NewSegmenter(vadConfigFrom(cfg, cfg.sampleRate)),
		Recognizer: recognizer,
		Matcher:    m,
		Logger:     logger,
		OnMatch:    matchHandler,
		AudioDebug: cfg.audioDebug,
	}

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

func selectDevice(mic *audio.MicSource, logger *log.Logger) error {
	// device selection
	devices, err := mic.GetDevices()
	if err != nil {
		return fmt.Errorf("get devices: %w", err)
	}
	if len(devices) == 0 {
		return fmt.Errorf("no capture devices found")
	}
	if len(devices) == 1 {
		// Nothing to choose between, and this also means the program
		// doesn't need an interactive terminal to start when there's
		// only one capture device attached (e.g. an unattended
		// restart after a crash, or a non-interactive launch).
		logger.Printf("Using device 0: %s (only one found)", devices[0].Name())
		mic.SetDeviceInfo(&devices[0])
		return nil
	}

	logger.Println("Select device:")
	for i, d := range devices {
		logger.Printf("  %d: %s", i, d.Name())
	}
	// get user input
	deviceIndex, err := userInput("Enter device number: ")
	if err != nil {
		return fmt.Errorf("read device number: %w", err)
	}
	if deviceIndex < 0 || deviceIndex >= len(devices) {
		return fmt.Errorf("invalid device number %d", deviceIndex)
	}
	logger.Printf("Using device %d: %s", deviceIndex, devices[deviceIndex].Name())
	mic.SetDeviceInfo(&devices[deviceIndex])
	return nil
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

func userInput(prompt string) (int, error) {
	fmt.Print(prompt)
	var input string
	_, err := fmt.Scanln(&input)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(input)
	if err != nil {
		return 0, err
	}
	return value, nil
}
