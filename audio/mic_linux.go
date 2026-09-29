//go:build linux

package audio

/*
#cgo LDFLAGS: -lasound
#include <alsa/asoundlib.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>

static snd_pcm_t* open_capture(const char *device, unsigned int sample_rate) {
    snd_pcm_t *pcm = NULL;

    int err = snd_pcm_open(
        &pcm,
        device,
        SND_PCM_STREAM_CAPTURE,
        0
    );
    if (err < 0) {
        return NULL;
    }

    err = snd_pcm_set_params(
        pcm,
        SND_PCM_FORMAT_S16_LE,
        SND_PCM_ACCESS_RW_INTERLEAVED,
        1,                  // mono
        sample_rate,
        1,                  // allow software resampling
        500000              // 500ms latency
    );

    if (err < 0) {
        snd_pcm_close(pcm);
        return NULL;
    }

    return pcm;
}

static int read_capture(
    snd_pcm_t *pcm,
    int16_t *buffer,
    int frames
) {
    int total = 0;

    while (total < frames) {
        snd_pcm_sframes_t n = snd_pcm_readi(
            pcm,
            buffer + total,
            frames - total
        );

        if (n == -EPIPE) {
            snd_pcm_prepare(pcm);
            continue;
        }

        if (n < 0) {
            n = snd_pcm_recover(pcm, n, 1);
            if (n < 0) {
                return (int)n;
            }
            continue;
        }

        total += (int)n;
    }

    return total;
}

static void close_capture(snd_pcm_t *pcm) {
    if (pcm != NULL) {
        snd_pcm_drop(pcm);
        snd_pcm_close(pcm);
    }
}

typedef struct {
    int card;
    int device;
    char name[256];
} alsa_device_t;

// list_capture_devices walks every sound card's PCM devices (via ALSA's
// control API, not the higher-level "hint" API) and reports the ones
// that support capture, so the caller can offer a "1: ..., 2: ..." style
// picker and later open the chosen one as "plughw:<card>,<device>" —
// the same convention as this file's previous hardcoded device string.
static int list_capture_devices(alsa_device_t *out, int max_devices) {
    int count = 0;
    int card = -1;

    while (snd_card_next(&card) >= 0 && card >= 0) {
        char ctl_name[32];
        snprintf(ctl_name, sizeof(ctl_name), "hw:%d", card);

        snd_ctl_t *ctl;
        if (snd_ctl_open(&ctl, ctl_name, 0) < 0) {
            continue;
        }

        int device = -1;
        while (snd_ctl_pcm_next_device(ctl, &device) >= 0 && device >= 0) {
            if (count >= max_devices) {
                break;
            }

            snd_pcm_info_t *pcm_info;
            snd_pcm_info_alloca(&pcm_info);
            snd_pcm_info_set_device(pcm_info, device);
            snd_pcm_info_set_subdevice(pcm_info, 0);
            snd_pcm_info_set_stream(pcm_info, SND_PCM_STREAM_CAPTURE);

            if (snd_ctl_pcm_info(ctl, pcm_info) < 0) {
                continue; // this device doesn't support capture
            }

            out[count].card = card;
            out[count].device = device;

            char *card_name = NULL;
            snd_card_get_name(card, &card_name);
            const char *pcm_name = snd_pcm_info_get_name(pcm_info);

            snprintf(out[count].name, sizeof(out[count].name), "%s: %s",
                     card_name ? card_name : ctl_name,
                     pcm_name ? pcm_name : "");

            if (card_name != NULL) {
                free(card_name);
            }

            count++;
        }

        snd_ctl_close(ctl);
    }

    return count;
}
*/
import "C"

import (
	"context"
	"fmt"
	"sync"
	"unsafe"
)

// maxCaptureDevices caps how many ALSA capture devices list_capture_devices
// will report. A Pi has at most a handful of USB audio interfaces attached,
// so this is generous headroom rather than a real limit.
const maxCaptureDevices = 32

// DeviceInfo identifies one ALSA capture-capable PCM device.
type DeviceInfo struct {
	Card   int
	Device int
	name   string
}

// Name returns a human-readable label for the device, suitable for a
// "select a device" prompt.
func (d DeviceInfo) Name() string { return d.name }

type MicSource struct {
	sampleRate int

	mu         sync.Mutex
	deviceInfo *DeviceInfo
	pcm        *C.snd_pcm_t
}

// NewMicSource prepares a capture source at sampleRate, mono. No ALSA
// device is opened yet — call GetDevices/SetDeviceInfo to choose one
// before Stream, mirroring the non-Linux (malgo-based) MicSource.
func NewMicSource(sampleRate int) (*MicSource, error) {
	return &MicSource{sampleRate: sampleRate}, nil
}

// GetDevices lists ALSA PCM devices that support capture.
func (m *MicSource) GetDevices() ([]DeviceInfo, error) {
	cDevices := make([]C.alsa_device_t, maxCaptureDevices)
	n := C.list_capture_devices((*C.alsa_device_t)(unsafe.Pointer(&cDevices[0])), C.int(maxCaptureDevices))
	if n < 0 {
		return nil, fmt.Errorf("enumerate ALSA capture devices: error %d", int(n))
	}

	devices := make([]DeviceInfo, int(n))
	for i := range devices {
		devices[i] = DeviceInfo{
			Card:   int(cDevices[i].card),
			Device: int(cDevices[i].device),
			name:   C.GoString(&cDevices[i].name[0]),
		}
	}
	return devices, nil
}

// SetDeviceInfo selects which device Stream will open.
func (m *MicSource) SetDeviceInfo(deviceInfo *DeviceInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deviceInfo = deviceInfo
}

func (m *MicSource) SampleRate() int { return m.sampleRate }

func (m *MicSource) Stream(ctx context.Context) (<-chan []float32, error) {
	m.mu.Lock()
	deviceInfo := m.deviceInfo
	m.mu.Unlock()

	if deviceInfo == nil {
		return nil, fmt.Errorf("no capture device selected")
	}

	deviceName := fmt.Sprintf("plughw:%d,%d", deviceInfo.Card, deviceInfo.Device)
	cDevice := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDevice))

	pcm := C.open_capture(cDevice, C.uint(m.sampleRate))
	if pcm == nil {
		return nil, fmt.Errorf("could not open ALSA capture device %s", deviceName)
	}

	m.mu.Lock()
	m.pcm = pcm
	m.mu.Unlock()

	out := make(chan []float32, 64)

	go func() {
		defer close(out)
		defer C.close_capture(pcm)

		const framesPerRead = 1024

		buf := make([]int16, framesPerRead)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n := C.read_capture(
				pcm,
				(*C.int16_t)(unsafe.Pointer(&buf[0])),
				C.int(framesPerRead),
			)

			if n < 0 {
				// Don't panic on transient ALSA errors.
				// The C layer attempts recovery where possible.
				continue
			}

			if n == 0 {
				continue
			}

			samples := make([]float32, int(n))

			for i := 0; i < int(n); i++ {
				samples[i] = float32(buf[i]) / 32768.0
			}

			select {
			case out <- samples:
			default:
				// Consumer is behind. Drop this chunk rather than
				// blocking the capture loop.
			}
		}
	}()

	return out, nil
}

func (m *MicSource) Close() error {
	return nil
}
