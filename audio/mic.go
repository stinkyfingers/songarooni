package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"

	"github.com/gen2brain/malgo"
)

// MicSource captures mono audio from the system's default input device
// (a USB audio interface fed from the talkback mic/line source, in the
// deployed setup).
//
// It uses malgo, Go bindings for the miniaudio C library. miniaudio is
// vendored as C source (no external system library to install) and
// supports both macOS and Linux/ARM, which is why it was chosen over
// alternatives like portaudio that require a separately installed system
// library. This does mean MicSource requires CGO; see the README for what
// that implies for cross-compiling to the Raspberry Pi.
type MicSource struct {
	sampleRate int

	mu       sync.Mutex
	malgoCtx *malgo.AllocatedContext
	device   *malgo.Device
}

// NewMicSource opens the default capture device at sampleRate, mono.
func NewMicSource(sampleRate int) (*MicSource, error) {
	malgoCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}
	return &MicSource{sampleRate: sampleRate, malgoCtx: malgoCtx}, nil
}

func (m *MicSource) SampleRate() int { return m.sampleRate }

// Stream starts capture and returns a channel of sample chunks. The
// channel is closed, and the underlying device stopped, when ctx is
// cancelled.
func (m *MicSource) Stream(ctx context.Context) (<-chan []float32, error) {
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = 1
	deviceConfig.SampleRate = uint32(m.sampleRate)
	deviceConfig.Alsa.NoMMap = 1

	out := make(chan []float32, 64)

	onRecvFrames := func(_, input []byte, frameCount uint32) {
		samples := make([]float32, frameCount)
		for i := 0; i < int(frameCount); i++ {
			v := int16(binary.LittleEndian.Uint16(input[i*2 : i*2+2]))
			samples[i] = float32(v) / 32768
		}
		select {
		case out <- samples:
		default:
			// Consumer (VAD/recognizer) is behind; drop this chunk rather
			// than block the realtime audio callback.
		}
	}

	// get devices
	devices, err := malgo.Context(m.malgoCtx.Context).Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("enumerate capture devices: %w", err)
	}

	// find clarett 4pre
	var clarett *malgo.DeviceInfo
	for _, d := range devices {
		if strings.Contains(strings.ToLower(d.Name()), "clarett 4pre") {
			clarett = &d
			break
		}
	}

	if clarett == nil {
		return nil, fmt.Errorf("Clarett 4Pre capture device not found")
	}

	deviceConfig.Capture.DeviceID = clarett.ID.Pointer()

	device, err := malgo.InitDevice(m.malgoCtx.Context, deviceConfig, malgo.DeviceCallbacks{Data: onRecvFrames})
	if err != nil {
		return nil, fmt.Errorf("init capture device: %w", err)
	}
	if err := device.Start(); err != nil {
		device.Uninit()
		return nil, fmt.Errorf("start capture device: %w", err)
	}

	m.mu.Lock()
	m.device = device
	m.mu.Unlock()

	go func() {
		<-ctx.Done()
		device.Uninit()
		close(out)
	}()

	return out, nil
}

// Close releases the audio context and, if capture is running, the
// device.
func (m *MicSource) Close() error {
	m.mu.Lock()
	device := m.device
	m.device = nil
	m.mu.Unlock()

	if device != nil {
		device.Uninit()
	}
	err := m.malgoCtx.Uninit()
	m.malgoCtx.Free()
	return err
}
