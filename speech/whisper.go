package speech

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Config configures the whisper.cpp CLI subprocess.
type Config struct {
	// BinaryPath is the whisper.cpp CLI executable, e.g. "whisper-cli"
	// (found on PATH) or an absolute path.
	BinaryPath string
	// ModelPath is the ggml model file, e.g. models/ggml-tiny.en.bin.
	// Kept configurable per plans/init.md so tiny.en/base.en/etc can be
	// swapped without a rebuild.
	ModelPath string
	// Language is passed to whisper.cpp's -l flag (e.g. "en").
	Language string
	// Threads sets whisper.cpp's -t flag. 0 leaves it at the binary's
	// own default.
	Threads int
	// ExtraArgs are appended verbatim, for flags this package doesn't
	// expose directly (e.g. constrained/grammar-guided decoding).
	ExtraArgs []string
}

// DefaultConfig returns a Config with a bare binary name (expected on
// PATH) and English transcription. ModelPath must still be set by the
// caller.
func DefaultConfig() Config {
	return Config{
		BinaryPath: "whisper-cli",
		Language:   "en",
	}
}

// WhisperCppRecognizer transcribes audio by invoking the whisper.cpp CLI
// as a subprocess. See the package doc comment for why this approach was
// chosen over CGO bindings.
type WhisperCppRecognizer struct {
	cfg Config
}

// NewWhisperCppRecognizer validates cfg and returns a Recognizer backed
// by whisper.cpp.
func NewWhisperCppRecognizer(cfg Config) (*WhisperCppRecognizer, error) {
	if cfg.BinaryPath == "" {
		return nil, fmt.Errorf("whisper.cpp binary path is required")
	}
	if cfg.ModelPath == "" {
		return nil, fmt.Errorf("whisper.cpp model path is required")
	}
	return &WhisperCppRecognizer{cfg: cfg}, nil
}

// Transcribe runs whisper.cpp against wavPath and returns the recognized
// text.
func (r *WhisperCppRecognizer) Transcribe(ctx context.Context, wavPath string) (string, error) {
	args := r.buildArgs(wavPath)

	cmd := exec.CommandContext(ctx, r.cfg.BinaryPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("run whisper.cpp (%s %s): %w: %s",
			r.cfg.BinaryPath, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	text := parseTranscript(stdout.String())
	if text == "" {
		return "", ErrEmptyTranscript
	}
	return text, nil
}

func (r *WhisperCppRecognizer) buildArgs(wavPath string) []string {
	args := []string{"-m", r.cfg.ModelPath, "-f", wavPath, "-nt"}
	if r.cfg.Language != "" {
		args = append(args, "-l", r.cfg.Language)
	}
	if r.cfg.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(r.cfg.Threads))
	}
	args = append(args, r.cfg.ExtraArgs...)
	return args
}

// timestampPrefix strips a leading "[00:00:00.000 --> 00:00:02.000]"
// style timestamp, in case -nt isn't honored by a particular whisper.cpp
// build/version.
var timestampPrefix = regexp.MustCompile(`^\[[^\]]*\]\s*`)

// logLinePrefixes are whisper.cpp's own status/banner lines, which are
// sometimes written to stdout rather than stderr depending on build
// options. They're filtered out so only actual transcript text remains.
var logLinePrefixes = []string{"whisper_", "system_info", "main:", "ggml_"}

// parseTranscript extracts transcript text from raw whisper.cpp stdout,
// tolerating timestamp lines and interleaved log/banner lines.
func parseTranscript(output string) string {
	var parts []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		skip := false
		for _, prefix := range logLinePrefixes {
			if strings.HasPrefix(line, prefix) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		line = timestampPrefix.ReplaceAllString(line, "")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, " ")
}
