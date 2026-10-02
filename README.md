# songarooni

Listens to a microphone (or line input, via a USB audio interface) during
a live band performance, and identifies which song from a supplied list
is being announced, using [whisper.cpp](https://github.com/ggerganov/whisper.cpp)
for fully offline speech recognition and a fuzzy text matcher tolerant of
imperfect transcription. See [plans/init.md](plans/init.md) for the full
design brief this implements (phase 1).

Phase 2 (slideshow playback via `feh`) is also implemented — see the
`slideshow` package below — triggered off the same match event as phase 1.

## Architecture

```
microphone/line-in
  -> audio     (capture + energy-based VAD segmentation)
  -> speech    (whisper.cpp subprocess, per segment)
  -> matcher   (fuzzy match transcript against song list)
  -> app       (wiring, logging, match event)
       -> slideshow (feh, keyed off the matched title)
```

### Project Layout (and package summary)

- app - contains pipeline.go, which links audio, recognition, matcher together
  - option to log audio levels for debugging
- audio - the `Source` interface (`Stream()`, `SampleRate()`, `Close()`) for
  anything that produces mono PCM samples, plus VAD and WAV support
  - mic.go / mic_linux.go - the two `MicSource` implementations, chosen by
    build tag: mic.go (`!linux`, i.e. macOS) uses malgo/miniaudio;
    mic_linux.go uses ALSA directly via cgo. Both expose `GetDevices()` and
    `SetDeviceInfo()` so `cmd/main.go` can list capture devices and prompt
    for one before `Stream()` — useful on a multi-input interface (e.g. a
    Focusrite Clarett) where there's no sane "default" to just pick
  - vad.go - energy-based (RMS) voice-activity `Segmenter`; turns a raw
    sample stream into speech `Segment`s
  - wav.go - WAV read/write, plus `WAVSource` for running a prerecorded
    file through the same pipeline as the mic
- bin - builds
- cmd - main.go type program run command(s)
- docker - docker build utility scripts
- matcher - text matcher. Match() return candidate strings.
- slideshow - runs a feh slideshow
- songs - loads a file of song titles
- speech - Recognizer interface with Transcribe() method
  - whisper.go implements Transcribe with whisper program
- whisper.cpp - C/C++ port of OpenAI's automatic speech recognition model


## Dependencies

Kept intentionally minimal:

- [`github.com/gen2brain/malgo`](https://github.com/gen2brain/malgo) — Go
  bindings for miniaudio, used for microphone capture on macOS
  (`audio/mic.go`, build-tagged `!linux`). Chosen over alternatives like
  `portaudio` because miniaudio is vendored as C source with no external
  system library to install.
- On Linux (`audio/mic_linux.go`), capture is a small direct cgo wrapper
  around ALSA (`libasound`) instead of malgo — this needs `libasound2-dev`
  at build time (the Dockerfile installs it) and gives direct access to
  ALSA's card/device enumeration for the device-selection prompt.
- Both capture paths require CGO — see "Building for Raspberry Pi" below
  for what that means for deployment.
- Everything else (song loading, matching, WAV encode/decode, VAD, CLI) is
  standard library only.

whisper.cpp itself is a separate native dependency, built and run as a
subprocess — not a Go module dependency at all (see below).

## whisper.cpp integration: subprocess, not bindings

whisper.cpp has official Go bindings (CGO) as well as a plain CLI. This
project shells out to the CLI (`whisper-cli`) rather than using the
bindings or writing a CGO wrapper:

- **Go bindings / CGO wrapper**: no per-call process-start overhead, but
  ties the whole Go build to CGO and to a whisper.cpp checkout that's
  ABI-compatible with the bindings, on *both* macOS and the Pi. Ordinary
  `go build`/cross-compilation stops being an option; every target needs
  a matched native (or carefully cross-compiled) libwhisper build.
- **Subprocess (chosen)**: the Go program stays pure Go — fast, ordinary
  `GOOS`/`GOARCH` cross-compilation for the app itself, no linking
  headaches for *this* code. The whisper.cpp binary and model can be
  swapped or upgraded independently, by replacing a file on the Pi. Cost:
  one process start per speech segment, and the transcript is exchanged
  via a temp WAV file + stdout instead of an in-process call. Segments
  are 1-5 seconds and infrequent (only when someone talks near the mic),
  so this overhead is negligible next to transcription time itself, even
  on a Pi 3.

Given the plan's emphasis on reliability and simple deployment, the
subprocess approach wins. `speech.Recognizer` is an interface specifically
so this choice could be swapped later without touching `app` or `cmd`.

## Fuzzy matching

The matcher (`matcher`) never compares the whole transcript
against the whole title. Instead, for each known title it:

1. Normalizes both the title and the transcript (lowercase, expand a
   small set of contractions, strip punctuation, collapse whitespace).
   Deliberately *no* stemming — with only ~100 short titles, aggressive
   stemming risks making distinct titles collide.
2. Slides a word-window across the transcript, sized to the title's own
   word count (and one shorter/longer, to tolerate whisper dropping or
   inserting a word like a trailing "next").
3. Scores each window against the title with a blend of:
   - Jaro-Winkler similarity (rewards shared characters and a common
     prefix — good for single-letter mishears like "honky"/"hanky")
   - fuzzy token overlap (each title word is matched against its closest
     transcript word, not required to match exactly — "women" ≈ "woman")
   - normalized Levenshtein edit-distance similarity
   - a full-marks bonus if the normalized title appears verbatim as a
     contiguous run of words in the transcript
4. Takes the best score per title across all windows.

The best- and second-best-scoring titles are returned together. A match
is only reported "confident" if **both**:

- the best score clears `--score-threshold` (default 0.85), **and**
- the margin over the second-best clears `--margin-threshold` (default
  0.08)

Both are required because a false positive (announcing the wrong song) is
worse than reporting no match — see `matcher/matcher_test.go` for
a test built specifically around several similar "Honky Tonk ..." titles
to exercise this margin logic.

No phonetic algorithm (e.g. Soundex/Metaphone) is used: Jaro-Winkler plus
fuzzy token overlap already handles the example Whisper errors in the
design brief, and adding a phonetic dependency didn't seem to earn its
keep for ~100 short English titles.

## Raspberry Pi performance considerations

- **Model**: use `tiny.en` on a Pi 3 — it's the only model with headroom
  for real-time-ish transcription of 1-5s segments on that CPU. `--model`
  is just a path, so `base.en` etc. can be tried by pointing at a
  different `.bin` file, no rebuild needed.
- **VAD avoids unnecessary Whisper calls**: `audio.Segmenter` is
  a cheap energy-based (RMS) detector that only emits a segment — and
  only then does whisper.cpp get invoked — once real speech is seen and
  enough trailing silence ends it. Background music/chatter/applause
  that never crosses the energy threshold costs nothing beyond the RMS
  calculation on incoming frames.
- **Short segments**: `--vad-max-segment-ms` (default 8000) bounds worst
  case segment length, so a stuck-open segment (e.g. continuous loud
  background music) can't turn into a huge, slow transcription job.
- **whisper.cpp threads**: whisper-cli's own default is already 4 threads,
  which matches a Pi 3's core count, so `--threads` doesn't need to be set
  explicitly unless you're on a board with a different core count.
- **Quantized models**: whisper.cpp's `tiny.en-q5_1`/`q8_0` quantized
  models trade a small amount of accuracy for meaningfully less CPU and
  memory; worth trying if `tiny.en` isn't fast enough in practice.
- **Avoid unnecessary allocations**: the VAD/segmenter path reuses a
  frame-sized buffer while scanning and only allocates once per completed
  segment; the matcher does no per-title heap-heavy work beyond small
  string comparisons across ~100 titles, which is trivial even on a Pi 3.

## Mac development setup

```
brew install cmake  # whisper.cpp's build uses cmake
git clone https://github.com/ggerganov/whisper.cpp
cd whisper.cpp
cmake -B build
cmake --build build -j --config Release
```

This produces `build/bin/whisper-cli`. Either add it to your `PATH` or
pass its path via `--whisper-bin`.

Download a model (from inside the whisper.cpp checkout):

```
sh ./models/download-ggml-model.sh tiny.en
```

This places a model at `whisper.cpp/models/ggml-tiny.en.bin` — pass that
path via `--model`.

Build songarooni:

```
make build     # -> bin/songarooni-osx
```

## Running the text-only matcher (no audio, no whisper.cpp)

Useful while tuning the matcher or song list:

```
bin/songarooni-osx --songs songs.txt --text "hey guys let's play honky tonk woman next"
```

Or via `make run-text TEXT="..."`. Exits 0 on a confident match, 1
otherwise, so it's scriptable for regression checks.

Run the unit tests (matcher, VAD, WAV encode/decode, whisper.cpp arg/output
parsing, and full-pipeline wiring with fakes — none require real audio
hardware or a whisper.cpp binary):

```
make test
```

## Running against a prerecorded WAV file

For development/regression testing without a live microphone. The WAV
file should be 16kHz mono (whisper.cpp's expected input); other formats
may decode but will likely transcribe poorly.

```
bin/songarooni-osx \
  --songs songs.txt \
  --model /path/to/whisper.cpp/models/ggml-tiny.en.bin \
  --whisper-bin /path/to/whisper.cpp/build/bin/whisper-cli \
  --audio test.wav
```

This runs the file through VAD segmentation exactly like the live path,
then exits.

## Microphone setup and running live

Connect the line/mic source to a USB audio interface, plug that into the
Mac/Pi, and confirm the OS sees it as an input device (e.g. macOS Sound
settings, or `arecord -l` on Linux).

```
bin/songarooni-osx \
  --songs songs.txt \
  --model /path/to/whisper.cpp/models/ggml-tiny.en.bin \
  --whisper-bin /path/to/whisper.cpp/build/bin/whisper-cli
```

Live mode lists capture devices and prompts you to pick one by number
before it starts listening — there's no "just use the default" here,
since on a multi-input interface (e.g. a Focusrite Clarett) the default
is rarely the right one. If nothing seems to reach the matcher after
that, the pipeline logs a periodic captured audio level (RMS) you can
compare against `--vad-energy-threshold` — see `app/pipeline.go`'s
`logAudioLevels` (currently enabled by uncommenting one line there); a
level that stays near zero even while talking points at gain/routing on
the interface rather than anything in this codebase — ALSA input gain is
a common culprit, separate from the interface's own physical gain knobs.

This runs continuously until Ctrl-C (or SIGTERM). On each confident
match it prints a machine-readable line to stdout:

```
SONG_MATCH: Honky Tonk Women
```

which is the natural hook for a future output sink (HTTP, MQTT, GPIO,
writing a JSON event, driving a stage display, etc.) — not implemented
here beyond this stdout line, per the design brief.

If the VAD is too sensitive/insensitive for your venue's noise floor,
tune `--vad-energy-threshold` (and `--vad-min-speech-ms`,
`--vad-silence-ms`) — run with a real recording of venue background noise
via `--audio` to dial these in before the show.

## Building for Raspberry Pi

**CGO is required** for this project, because microphone capture
(`audio/mic.go`) uses malgo/miniaudio, which is C. Plain
`GOOS=linux GOARCH=arm64 go build` will *not* work for a full build
without a matching cross-compiling C toolchain — pretending otherwise
would just fail on the Pi.

Target: a 64-bit Raspberry Pi OS (arm64). Two options:

### Option A — cross-build everything via Docker (recommended)

Requires only Docker Desktop (with buildx, included by default) — no
locally-installed cross toolchain at all. It uses Docker's QEMU-based
emulation to run an actual ARM64 Linux container and do ordinary
*native* builds inside it — both `go build` and whisper.cpp's own cmake
build (see [Dockerfile](Dockerfile)) — so there's no cross-compiler to
get right for either one.

It also **builds whisper.cpp from source and bundles it**, model
included, alongside its own glibc (the dynamic loader, `libc.so.6`, and
`libm.so.6`, copied from the build image, plus whisper.cpp's own
`libggml*.so`/`libwhisper.so`). Both the `songarooni` binary and
`whisper-cli` are installed behind a small generic wrapper script
([docker/pi-wrapper.sh](docker/pi-wrapper.sh)) that runs the real binary
against those bundled libraries explicitly, instead of whatever glibc is
installed on the Pi. That's what avoids `GLIBC_x.xx not found` on a
Raspberry Pi OS release with an older glibc than the build image's —
glibc is forward-compatible (a newer `libc.so.6` still satisfies programs
built against older symbol versions), so this works regardless of which
Raspberry Pi OS release/glibc version your Pi actually has, with no need
to match the Dockerfile's base image to it.

```
make build-pi-docker
```

produces, in `bin/` (wiped and rewritten fresh on every run, so a
previous build's files never linger):

- `songarooni-linux-arm64` — wrapper script; this is what you run
- `songarooni-linux-arm64.bin` — the real binary
- `whisper-cli` / `whisper-cli.bin` — same wrapper pattern, for whisper.cpp
- `models/ggml-tiny.en.bin` — the bundled model (`ARG WHISPER_MODEL` in
  the Dockerfile controls which one; must match `songarooni.sh`'s
  `--model` flag)
- `lib/` — the shared glibc all of the above run against

Trade-off: QEMU emulation makes the build itself slower (whisper.cpp's
own build included, a couple of minutes total), which is a fine trade for
an occasional deploy build.

### Option B — build natively on the Pi (fallback)

Raspberry Pi OS ships a C compiler, so this "just works" and avoids any
cross-toolchain/emulation fragility, at the cost of needing to also build
whisper.cpp yourself on the Pi (see the Mac dev setup section above — the
steps are the same, just run there instead):

```
# on the Pi, with Go installed and this repo copied over:
make build-pi-native   # -> bin/songarooni-linux-arm
```

## Deploying to the Raspberry Pi

The easiest path is [package.sh](package.sh) end to end:

1. `make build-pi-docker` (Option A above).
2. `./package.sh` — zips `bin/`, `songs.txt`, and `songarooni.sh` into
   `songarooni.zip` and copies it to your Desktop.
3. Move the zip to the Pi (thumb drive, `scp`, etc.) and unzip it at
   `$HOME` (e.g. `~/songarooni/`) — `bin/`, `songs.txt`, and
   `songarooni.sh` should end up as siblings there.
4. Run `./songarooni.sh` (see its comments for passing extra flags, e.g.
   `--slideshow-dir`).

If you built with Option B instead, copy `bin/songarooni-linux-arm` plus
your own whisper.cpp build/model to the Pi and invoke it directly:

```
./songarooni-linux-arm \
  --songs songs.txt \
  --model ~/whisper.cpp/models/ggml-tiny.en.bin \
  --whisper-bin ~/whisper.cpp/build/bin/whisper-cli \
  --threads 4
```

## Example CLI commands

```
songarooni --help

songarooni --songs songs.txt --text "this one's take it easy"

songarooni --songs songs.txt --model models/ggml-tiny.en.bin --audio test.wav

songarooni --songs songs.txt --model models/ggml-tiny.en.bin \
  --whisper-bin whisper.cpp/build/bin/whisper-cli \
  --vad-energy-threshold 0.03 --vad-silence-ms 400 \
  --score-threshold 0.8 --margin-threshold 0.1
```

## Things to test first on the Raspberry Pi 3

- **Real transcription latency**: time a single `--audio` run against a
  representative 1-5s recording made *on the actual venue's mic/interface*,
  with `tiny.en`, to see how much headroom exists before a live segment
  finishes speaking. If it's too slow, try a quantized `tiny.en` model or
  reduce `--vad-max-segment-ms`.
- **VAD threshold in the real room**: capture a few minutes of real
  soundcheck audio (crowd noise, band tuning, talkback chatter) via
  `--audio` against a recording, and tune `--vad-energy-threshold`/
  `--vad-min-speech-ms`/`--vad-silence-ms` so it doesn't fire on
  applause/music but does reliably catch talkback speech.
- **Microphone/USB interface device selection**: live mode lists capture
  devices and prompts for one by number at startup — confirm your
  interface shows up (cross-check against `arecord -l`) and that you're
  selecting the right one if more than one is listed.
- **Input gain**: confirmed directly on a Clarett 4Pre — ALSA's own mixer
  levels (`alsamixer -c <card>`) and the interface's *physical* gain knobs
  are independent, and a mac-side Focusrite Control gain setting doesn't
  carry over to the Pi. If the captured audio level (see "Microphone
  setup and running live" above) stays near zero even while talking,
  check both before assuming it's a code/routing problem.
- **CPU/thermal headroom under sustained use**: run songarooni for the
  duration of a full set/show and watch CPU temp and throttling
  (`vcgencmd measure_temp`), since repeated whisper.cpp invocations over
  hours is a different load profile than a short test.
- **Memory usage over a long run**: confirm no slow leak across many
  segments (each segment's temp WAV is removed after transcription;
  worth double-checking `/tmp` isn't accumulating files if a run is ever
  killed mid-segment).
- **False-positive rate against real crowd noise**: play back a recording
  of a noisy room (with no song announcements) through `--audio` and
  confirm `--score-threshold`/`--margin-threshold` are conservative
  enough that nothing fires — false positives are worse than silence.