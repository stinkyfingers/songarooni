# Cross-builds everything needed to run on a Raspberry Pi 3 (64-bit,
# ARM64/aarch64 Raspberry Pi OS) using Docker's QEMU-based emulation: the
# whole build stage runs inside a real ARM64 Linux container and does
# ordinary *native* builds (`go build`, whisper.cpp's own cmake build),
# so there's no cross-compiler to get right for either of them.
#
# whisper-cli (C++, dynamically linked) also gets its own glibc bundled
# (the dynamic loader, libc.so.6, libstdc++.so.6, the ggml/whisper
# libs, ...) behind a small generic wrapper script (docker/pi-wrapper.sh)
# that runs the real binary against those bundled libs explicitly,
# instead of whatever glibc is installed on the Pi. That's what avoids
# "GLIBC_x.xx not found" on a Raspberry Pi OS release with an older
# glibc than this build image's (Bookworm, glibc 2.36): glibc is
# forward-compatible (a newer libc.so.6 still satisfies programs built
# against older symbol versions), so shipping ours and forcing its use
# works regardless of the Pi's own OS version.
#
# The songarooni binary itself needs none of this: it's pure Go (no CGO
# — microphone capture shells out to `arecord` rather than linking
# against ALSA, see audio/mic_linux.go for why) and so builds fully
# statically linked, with zero runtime library dependencies of its own.
#
# Note: the ELF interpreter path (PT_INTERP) can't be relative/
# $ORIGIN-based — the kernel resolves it literally, before any dynamic
# linking happens — which is why this ships wrapper scripts instead of
# patching each real binary's own interpreter.
#
# Build (see `make build-pi-docker`, which wraps this):
#   docker buildx build --platform linux/arm64 --target export \
#       --output type=local,dest=bin .
#
# Deploy: copy the whole bin/ directory to the Pi as one unit (see
# package.sh/songarooni.sh) — everything it needs lives under there.
FROM debian:bookworm-slim AS build

# Pin to match go.mod's `go` directive.
ARG GO_VERSION=1.24.2
# Must match songarooni.sh's --model flag.
ARG WHISPER_MODEL=tiny.en

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl gcc libc6-dev cmake git build-essential \
    && rm -rf /var/lib/apt/lists/*

# whisper.cpp: built from source, natively for this container's own
# architecture (same reasoning as the Go build below — no cross
# toolchain to get right). Also fetches the ggml model songarooni.sh
# expects to find bundled.
#
# GGML_NATIVE=OFF is required here: ggml's default (-march=native)
# auto-detects CPU features via QEMU's emulated /proc/cpuinfo, which can
# report an FP16-NEON feature combination this GCC can't actually
# generate correct code for under emulation (fails with "inlining failed
# ... target specific option mismatch" on vfmaq_f16/vaddq_f16). Disabling
# it uses safe, portable flags instead — this only affects *this build*,
# not runtime performance-critical native compilation on a real Pi.
RUN git clone --depth 1 https://github.com/ggerganov/whisper.cpp.git /whisper.cpp
RUN cmake -S /whisper.cpp -B /whisper.cpp/build \
        -DWHISPER_BUILD_TESTS=OFF \
        -DWHISPER_BUILD_EXAMPLES=ON \
        -DGGML_NATIVE=OFF \
    && cmake --build /whisper.cpp/build -j2
RUN bash /whisper.cpp/models/download-ggml-model.sh "${WHISPER_MODEL}"

# Official Go release for ARM64.
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-arm64.tar.gz" -o /tmp/go.tar.gz \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Statically linked (no CGO — see above), so this is the final artifact
# as-is; no wrapper/bundled libs needed for it.
RUN go build -o /out/songarooni-linux-arm64 ./cmd

# Flatten whisper.cpp's output into the same directory as everything
# else, rather than preserving its internal build/bin/ nesting — that
# keeps the wrapper script's "the bundled lib/ is my sibling" assumption
# simple, and matches the flat paths songarooni.sh expects.
RUN cp /whisper.cpp/build/bin/whisper-cli /out/whisper-cli.bin
RUN mkdir -p /out/models \
    && cp "/whisper.cpp/models/ggml-${WHISPER_MODEL}.bin" /out/models/

# Bundle every shared library whisper-cli needs (this skips the
# kernel-provided linux-vdso.so.1, which `ldd` lists but isn't a real
# file to copy) plus the dynamic loader itself.
RUN set -eux; \
    mkdir -p /out/lib; \
    ldd /out/whisper-cli.bin \
        | awk '{ for (i = 1; i <= NF; i++) if ($i ~ /^\//) print $i }' \
        | sort -u \
        | xargs -I{} cp -L {} /out/lib/

# Install whisper-cli behind the generic wrapper script, which execs
# "<its own name>.bin" against the bundled lib/ beside it.
COPY docker/pi-wrapper.sh /out/whisper-cli
RUN chmod +x /out/whisper-cli

# Minimal stage so `docker buildx --output type=local` can export just
# the build's output, without pulling the whole build image along with it.
FROM scratch AS export
COPY --from=build /out/ /
