package app

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
)

func TestLogAudioLevels_Passthrough(t *testing.T) {
	in := make(chan []float32, 2)
	in <- []float32{0.1, 0.2, 0.3}
	in <- []float32{0.4, 0.5}
	close(in)

	logger := log.New(&bytes.Buffer{}, "", 0)
	out := logAudioLevels(context.Background(), logger, in)

	var got []float32
	for chunk := range out {
		got = append(got, chunk...)
	}

	want := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLogAudioLevels_ReportsRMSOnClose(t *testing.T) {
	in := make(chan []float32, 1)
	// Constant amplitude 0.5 => RMS of 0.5.
	in <- []float32{0.5, -0.5, 0.5, -0.5}
	close(in)

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	out := logAudioLevels(context.Background(), logger, in)
	for range out {
		// Drain; the report is logged once the input channel closes.
	}

	got := buf.String()
	if !strings.Contains(got, "rms=0.5000") {
		t.Fatalf("log output = %q, want it to contain %q", got, "rms=0.5000")
	}
}

func TestLogAudioLevels_ReportsNoSamplesOnEmptyClose(t *testing.T) {
	in := make(chan []float32)
	close(in)

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	out := logAudioLevels(context.Background(), logger, in)
	for range out {
	}

	got := buf.String()
	if !strings.Contains(got, "no samples received") {
		t.Fatalf("log output = %q, want it to mention no samples received", got)
	}
}
