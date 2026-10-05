package app

import (
	"context"
	"log"
	"testing"
	"time"

	"songarooni/audio"
	"songarooni/matcher"
	"songarooni/speech"
)

const testSampleRate = 16000

// fakeSource streams a fixed set of samples once, then closes.
type fakeSource struct {
	samples   []float32
	chunkSize int
}

func (s *fakeSource) SampleRate() int { return testSampleRate }
func (s *fakeSource) Close() error    { return nil }

func (s *fakeSource) Stream(ctx context.Context) (<-chan []float32, error) {
	out := make(chan []float32, 1024)
	for start := 0; start < len(s.samples); start += s.chunkSize {
		end := start + s.chunkSize
		if end > len(s.samples) {
			end = len(s.samples)
		}
		out <- s.samples[start:end]
	}
	close(out)
	return out, nil
}

func tone(ms int, amplitude float32) []float32 {
	n := ms * testSampleRate / 1000
	s := make([]float32, n)
	for i := range s {
		s[i] = amplitude
	}
	return s
}

// fakeRecognizer returns a canned transcript regardless of the audio it's
// given, so the pipeline's wiring can be tested without whisper.cpp.
type fakeRecognizer struct {
	transcript string
	err        error
	calls      int
}

func (f *fakeRecognizer) Transcribe(ctx context.Context, wavPath string) (string, error) {
	f.calls++
	return f.transcript, f.err
}

func newTestPipeline(t *testing.T, samples []float32, rec *fakeRecognizer) (*Pipeline, *[]MatchEvent) {
	t.Helper()

	titles := []string{"Honky Tonk Women", "Folsom Prison Blues"}
	m := matcher.New(titles, matcher.DefaultConfig())

	vadCfg := audio.DefaultVADConfig(testSampleRate)
	vadCfg.MinSpeechMS = 100
	vadCfg.SilenceMS = 200
	vadCfg.PreRollMS = 0
	vadCfg.PostRollMS = 0

	var events []MatchEvent
	p := &Pipeline{
		Source:     &fakeSource{samples: samples, chunkSize: 320},
		Segmenter:  audio.NewSegmenter(vadCfg),
		Recognizer: rec,
		Matcher:    m,
		TempDir:    t.TempDir(),
		OnMatch:    func(e MatchEvent) { events = append(events, e) },
		Logger:     log.New(&discard{}, "", 0),
	}
	return p, &events
}

type discard struct{}

func (*discard) Write(p []byte) (int, error) { return len(p), nil }

func speechSamples() []float32 {
	var s []float32
	s = append(s, tone(300, 0)...)
	s = append(s, tone(600, 0.5)...)
	s = append(s, tone(500, 0)...)
	return s
}

func runPipeline(t *testing.T, p *Pipeline) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Run(ctx); err != nil && err != context.DeadlineExceeded {
		t.Fatalf("Pipeline.Run: %v", err)
	}
}

func TestPipeline_ConfidentMatchEmitsEvent(t *testing.T) {
	rec := &fakeRecognizer{transcript: "hey guys let's play honky tonk woman next"}
	p, events := newTestPipeline(t, speechSamples(), rec)

	runPipeline(t, p)

	if rec.calls != 1 {
		t.Fatalf("recognizer called %d times, want 1", rec.calls)
	}
	if len(*events) != 1 {
		t.Fatalf("got %d match events, want 1: %+v", len(*events), *events)
	}
	if (*events)[0].Title != "Honky Tonk Women" {
		t.Fatalf("matched title = %q, want %q", (*events)[0].Title, "Honky Tonk Women")
	}
}

func TestPipeline_NoMatchEmitsNoEvent(t *testing.T) {
	rec := &fakeRecognizer{transcript: "thanks everybody good night"}
	p, events := newTestPipeline(t, speechSamples(), rec)

	runPipeline(t, p)

	if len(*events) != 0 {
		t.Fatalf("got %d match events, want 0: %+v", len(*events), *events)
	}
}

func TestPipeline_EmptyTranscriptSkipped(t *testing.T) {
	rec := &fakeRecognizer{err: speech.ErrEmptyTranscript}
	p, events := newTestPipeline(t, speechSamples(), rec)

	runPipeline(t, p)

	if len(*events) != 0 {
		t.Fatalf("got %d match events, want 0: %+v", len(*events), *events)
	}
}
