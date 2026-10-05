//go:build linux

// Linux microphone capture shells out to `arecord` (part of alsa-utils)
// rather than talking to ALSA directly via cgo.
//
// This project originally had a hand-rolled cgo wrapper around libasound
// here. Against a Focusrite Clarett 4Pre it worked fine; against a
// Behringer UMC22 on the same Raspberry Pi, every read failed with a
// persistent ALSA -EIO, reproducible with the exact same device string,
// format, channel count, and sample rate that `arecord` itself used
// successfully in every single test — with every environmental
// variable (buffer/period size, PipeWire, USB autosuspend, Go's async
// goroutine preemption, a libasound version mismatch) ruled out by
// direct testing, and the same persistent failure reproduced even
// running the raw binary unwrapped against the Pi's own system
// libasound. Rather than keep debugging a hand-rolled ALSA client
// blind, this switches to the one thing that was 100% reliable
// throughout that entire investigation: `arecord` itself. Also matches
// this project's existing precedent for whisper.cpp (subprocess over
// bindings) — reuse a small, mature, battle-tested external tool
// instead of re-implementing what it already does reliably.
//
// malgo/miniaudio (used on macOS, see mic.go) was considered and
// rejected for Linux: it had its own prior problems here.
package audio

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// DeviceInfo identifies one ALSA capture-capable PCM device, as reported
// by `arecord -l`.
type DeviceInfo struct {
	Card   int
	Device int
	name   string
}

// Name returns a human-readable label for the device, suitable for a
// "select a device" prompt.
func (d DeviceInfo) Name() string { return d.name }

// arecordDeviceLine matches lines like:
//
//	card 2: CODEC [USB Audio CODEC], device 0: USB Audio [USB Audio]
var arecordDeviceLine = regexp.MustCompile(`^card (\d+): .*?\[(.*?)\], device (\d+): .*?\[(.*?)\]`)

// MicSource captures mono audio by running `arecord` as a subprocess and
// reading its raw PCM output.
type MicSource struct {
	sampleRate int

	mu         sync.Mutex
	deviceInfo *DeviceInfo
	cmd        *exec.Cmd
}

// NewMicSource prepares a capture source at sampleRate, mono. No
// recording starts yet — call GetDevices/SetDeviceInfo to choose a
// device before Stream, mirroring the non-Linux (malgo-based)
// MicSource.
func NewMicSource(sampleRate int) (*MicSource, error) {
	return &MicSource{sampleRate: sampleRate}, nil
}

// GetDevices lists ALSA PCM devices that support capture, by parsing
// `arecord -l` rather than querying ALSA directly.
func (m *MicSource) GetDevices() ([]DeviceInfo, error) {
	out, err := exec.Command("arecord", "-l").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			// Never ran at all (e.g. arecord not installed) — no
			// output to fall back on.
			return nil, fmt.Errorf("list capture devices (arecord -l): %w", err)
		}
		// arecord exits non-zero in some cases (e.g. a mix of capture
		// and non-capture-capable cards) but may still have printed a
		// usable device list before that; parse what it did output
		// instead of discarding it.
		log.Printf("arecord -l exited with an error (%v); parsing its output anyway", err)
	}
	return parseArecordDeviceList(string(out)), nil
}

// parseArecordDeviceList extracts capture devices from `arecord -l`
// output, e.g. a line like:
//
//	card 2: CODEC [USB Audio CODEC], device 0: USB Audio [USB Audio]
//
// Non-matching lines (headers, "Subdevices:" lines, "no soundcards
// found" when nothing's attached, ...) are silently skipped.
func parseArecordDeviceList(output string) []DeviceInfo {
	var devices []DeviceInfo
	for _, line := range strings.Split(output, "\n") {
		match := arecordDeviceLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		card, _ := strconv.Atoi(match[1])
		device, _ := strconv.Atoi(match[3])
		devices = append(devices, DeviceInfo{
			Card:   card,
			Device: device,
			name:   fmt.Sprintf("%s: %s", match[2], match[4]),
		})
	}
	return devices
}

// SetDeviceInfo selects which device Stream will capture from.
func (m *MicSource) SetDeviceInfo(deviceInfo *DeviceInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deviceInfo = deviceInfo
}

func (m *MicSource) SampleRate() int { return m.sampleRate }

// Stream starts `arecord` against the selected device and returns a
// channel of sample chunks, converted from its raw S16_LE output. The
// subprocess is killed (via exec.CommandContext) when ctx is cancelled.
func (m *MicSource) Stream(ctx context.Context) (<-chan []float32, error) {
	m.mu.Lock()
	deviceInfo := m.deviceInfo
	m.mu.Unlock()

	if deviceInfo == nil {
		return nil, fmt.Errorf("no capture device selected")
	}

	deviceName := fmt.Sprintf("plughw:%d,%d", deviceInfo.Card, deviceInfo.Device)
	log.Printf("songarooni: capturing via arecord on %s (%s)", deviceName, deviceInfo.Name())

	cmd := exec.CommandContext(ctx, "arecord",
		"-D", deviceName,
		"-f", "S16_LE",
		"-r", strconv.Itoa(m.sampleRate),
		"-c", "1",
		"-t", "raw",
		"-",
	)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create arecord stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start arecord: %w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.mu.Unlock()

	out := make(chan []float32, 64)

	go func() {
		defer close(out)
		defer cmd.Wait()

		const bytesPerFrame = 2 // S16_LE, mono
		const framesPerRead = 1024

		reader := bufio.NewReader(stdout)
		buf := make([]byte, framesPerRead*bytesPerFrame)

		for {
			n, err := io.ReadFull(reader, buf)
			if n > 0 {
				samples := make([]float32, n/bytesPerFrame)
				for i := range samples {
					v := int16(binary.LittleEndian.Uint16(buf[i*2 : i*2+2]))
					samples[i] = float32(v) / 32768
				}
				select {
				case out <- samples:
				default:
					// Consumer (VAD/recognizer) is behind; drop this
					// chunk rather than block the capture loop.
				}
			}
			if err != nil {
				if err != io.EOF && err != io.ErrUnexpectedEOF && ctx.Err() == nil {
					log.Printf("songarooni: arecord read error: %v", err)
				}
				return
			}
		}
	}()

	return out, nil
}

// Close stops capture, if running.
func (m *MicSource) Close() error {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return nil
}
