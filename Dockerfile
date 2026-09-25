# Cross-builds the Raspberry Pi (armv7) binary using Docker's QEMU-based
# emulation: the whole build stage runs as an ordinary *native* `go build`
# inside a real armv7 Linux container, so there's no macOS-hosted cross
# compiler or glibc-version-matching to get right (see README.md for why
# that matters and what the alternative brew-based cross toolchain
# requires).
#
# IMPORTANT: this image's glibc must be no newer than your actual
# Raspberry Pi OS's glibc — older is fine (forward compatible), newer is
# not (the binary won't run: "GLIBC_x.xx not found"). Check your Pi with
# `cat /etc/os-release` and change the base image tag below to match if
# it's not Debian Bookworm-based.
#
# Build (see `make build-pi-docker`, which wraps this):
#   docker buildx build --platform linux/arm/v7 --target export \
#       --output type=local,dest=bin .
FROM debian:bookworm-slim AS build

# Pin to match go.mod's `go` directive.
ARG GO_VERSION=1.24.2

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*

# Official Go release for 32-bit ARM. Go's "armv6l" build covers both
# armv6 (Pi 1/Zero) and armv7 (Pi 2/3/4) hardware.
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-armv6l.tar.gz" -o /tmp/go.tar.gz \
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
RUN go build -o /out/songarooni-linux-arm ./cmd

# Minimal stage so `docker buildx --output type=local` can export just
# the built binary, without pulling the whole build image along with it.
FROM scratch AS export
COPY --from=build /out/songarooni-linux-arm /songarooni-linux-arm
