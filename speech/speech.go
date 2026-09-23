// Package speech transcribes short audio segments to text using
// whisper.cpp.
//
// whisper.cpp integration approach: this package shells out to the
// whisper.cpp CLI binary (built separately, e.g. as "whisper-cli") rather
// than using CGO bindings or a C wrapper. Trade-off, briefly:
//
//   - Go bindings / CGO wrapper: no per-call process-start overhead, but
//     ties this Go module's build to CGO and to a whisper.cpp checkout
//     that's ABI-compatible with the bindings, on both macOS and
//     Raspberry Pi. Ordinary `go build`/cross-compilation stops working;
//     every target needs a matched native (or carefully cross-compiled)
//     build of libwhisper alongside the bindings.
//   - Subprocess: the Go program itself stays pure Go (fast, ordinary
//     GOOS/GOARCH cross-compilation, no linking headaches), and the
//     whisper.cpp binary/model can be swapped or upgraded independently by
//     just replacing a file on the Pi. Cost: one process start per speech
//     segment (segments are 1-5s and infrequent, so on a Pi 3 this is
//     small next to the transcription time itself) and transcript
//     exchange via a temp WAV file + stdout instead of an in-process call.
//
// Given the reliability and deployment-simplicity goals in
// plans/init.md, the subprocess approach is used. Recognizer is an
// interface specifically so this choice can be swapped later (or faked in
// tests) without touching callers.
package speech

import (
	"context"
	"fmt"
)

// Recognizer transcribes a WAV file to text.
type Recognizer interface {
	Transcribe(ctx context.Context, wavPath string) (string, error)
}

// ErrEmptyTranscript is returned by implementations when whisper.cpp ran
// successfully but produced no text (e.g. the segment was noise the VAD
// mistook for speech).
var ErrEmptyTranscript = fmt.Errorf("empty transcript")
