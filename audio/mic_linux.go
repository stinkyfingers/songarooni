//go:build linux

package audio

/*
#cgo LDFLAGS: -lasound
#include <alsa/asoundlib.h>
#include <stdlib.h>
#include <stdint.h>

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
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
)

type MicSource struct {
	sampleRate int
	pcm        *C.snd_pcm_t
}

func NewMicSource(sampleRate int) (*MicSource, error) {
	const device = "plughw:2,0" // sounus C4Pre Clarett

	cDevice := C.CString(device)
	defer C.free(unsafe.Pointer(cDevice))

	pcm := C.open_capture(
		cDevice,
		C.uint(sampleRate),
	)

	if pcm == nil {
		return nil, fmt.Errorf(
			"could not open ALSA capture device %s",
			device,
		)
	}

	return &MicSource{
		sampleRate: sampleRate,
		pcm:        pcm,
	}, nil
}

func (m *MicSource) SampleRate() int { return m.sampleRate }

func (m *MicSource) Stream(ctx context.Context) (<-chan []float32, error) {
	out := make(chan []float32, 64)

	go func() {
		defer close(out)
		defer C.close_capture(m.pcm)

		const framesPerRead = 1024

		buf := make([]int16, framesPerRead)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n := C.read_capture(
				m.pcm,
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
