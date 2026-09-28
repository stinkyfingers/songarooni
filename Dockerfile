# Cross-builds the Raspberry Pi (ARM64) binary using Docker's QEMU-based
# emulation. The whole build stage runs inside a real ARM64 Linux
# container, so Go, CGO, miniaudio, and whisper.cpp are all built for
# Linux ARM64.
#
# Build:
#   docker buildx build --platform linux/arm64 --target export \
#       --output type=local,dest=bin .
FROM debian:bookworm-slim AS build
RUN uname -m && dpkg --print-architecture

# Pin to match go.mod's `go` directive.
ARG GO_VERSION=1.24.2

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl gcc libc6-dev libasound2-dev cmake git build-essential \
    && rm -rf /var/lib/apt/lists/*

## whisper

RUN git clone --depth 1 https://github.com/ggerganov/whisper.cpp.git /whisper.cpp

RUN cmake -S /whisper.cpp -B /whisper.cpp/build \
    -DWHISPER_BUILD_TESTS=OFF \
    -DWHISPER_BUILD_EXAMPLES=ON

RUN cmake --build /whisper.cpp/build -j2

RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-arm64.tar.gz" \
    -o /tmp/go.tar.gz \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# CGO is required (mic capture uses malgo/miniaudio) and is on by default
# here since gcc is present; this is a native build for the container's
# own architecture, so no CC/GOARM cross-compile flags are needed.
RUN go build -o /out/songarooni-linux-arm.bin ./cmd

# Bundle the shared libraries the binary actually needs (this skips the
# kernel-provided linux-vdso.so.1, which `ldd` lists but isn't a real
# file to copy) plus the dynamic loader itself.
RUN set -eux; \
    mkdir -p /out/lib; \
    ldd /out/songarooni-linux-arm.bin \
        | awk '{ for (i = 1; i <= NF; i++) if ($i ~ /^\//) print $i }' \
        | sort -u \
        | xargs -I{} cp -L {} /out/lib/


# Minimal stage so `docker buildx --output type=local` can export just
# the build's output (wrapper script + binary + bundled libs), without
# pulling the whole build image along with it.

# Put whisper-cli where the Go program expects it.
RUN mkdir -p /out/whisper.cpp/build/bin \
    && cp /whisper.cpp/build/bin/whisper-cli \
       /out/whisper.cpp/build/bin/whisper-cli

# Bundle the shared libraries required by both the Go binary and whisper-cli.
RUN set -eux; \
    mkdir -p /out/lib; \
    ( \
        ldd /out/songarooni-linux-arm.bin; \
        ldd /out/whisper.cpp/build/bin/whisper-cli; \
    ) \
        | awk '{ for (i = 1; i <= NF; i++) if ($i ~ /^\//) print $i }' \
        | sort -u \
        | xargs -I{} cp -L {} /out/lib/

COPY docker/pi-wrapper.sh /out/songarooni-linux-arm64
RUN chmod +x /out/songarooni-linux-arm64

FROM scratch AS export
COPY --from=build /out/ /