package speech

import (
	"reflect"
	"testing"
)

func TestParseTranscript(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "plain text, no timestamps",
			output: "hey guys let's play honky tonk woman next\n",
			want:   "hey guys let's play honky tonk woman next",
		},
		{
			name:   "with timestamps",
			output: "[00:00:00.000 --> 00:00:02.500]   hey guys let's play honky tonk woman next\n",
			want:   "hey guys let's play honky tonk woman next",
		},
		{
			name: "multiple timestamped lines joined",
			output: "[00:00:00.000 --> 00:00:01.000]   hey guys\n" +
				"[00:00:01.000 --> 00:00:02.500]   let's play honky tonk woman\n",
			want: "hey guys let's play honky tonk woman",
		},
		{
			name: "interleaved banner/log lines are dropped",
			output: "whisper_init_from_file_no_state: loading model\n" +
				"system_info: n_threads = 4\n" +
				"[00:00:00.000 --> 00:00:02.000]  honky tonk women\n" +
				"main: processing took 100ms\n",
			want: "honky tonk women",
		},
		{
			name:   "empty output",
			output: "\n\n",
			want:   "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseTranscript(c.output)
			if got != c.want {
				t.Errorf("parseTranscript(%q) = %q, want %q", c.output, got, c.want)
			}
		})
	}
}

func TestBuildArgs(t *testing.T) {
	r := &WhisperCppRecognizer{cfg: Config{
		BinaryPath: "whisper-cli",
		ModelPath:  "models/ggml-tiny.en.bin",
		Language:   "en",
		Threads:    4,
		ExtraArgs:  []string{"--beam-size", "1"},
	}}

	got := r.buildArgs("/tmp/segment.wav")
	want := []string{
		"-m", "models/ggml-tiny.en.bin",
		"-f", "/tmp/segment.wav",
		"-nt",
		"-l", "en",
		"-t", "4",
		"--beam-size", "1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildArgs() = %v, want %v", got, want)
	}
}

func TestNewWhisperCppRecognizer_RequiresPaths(t *testing.T) {
	if _, err := NewWhisperCppRecognizer(Config{}); err == nil {
		t.Fatal("expected error for empty config")
	}
	if _, err := NewWhisperCppRecognizer(Config{BinaryPath: "whisper-cli"}); err == nil {
		t.Fatal("expected error for missing model path")
	}
	if _, err := NewWhisperCppRecognizer(Config{BinaryPath: "whisper-cli", ModelPath: "m.bin"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
