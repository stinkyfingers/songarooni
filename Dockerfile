# Cross-builds the Raspberry Pi (armv7) binary using Docker's QEMU-based
# emulation: the whole build stage runs as an ordinary *native* `go build`
# inside a real armv7 Linux container, so there's no macOS-hosted cross
# compiler to get right (see README.md for the alternative brew-based
# cross toolchain).
#
# The output also bundles its own glibc (the dynamic loader, libc.so.6,
# and libm.so.6, copied from this build image) alongside a small wrapper
# script that runs the real binary against those bundled libraries
# explicitly, instead of whatever glibc happens to be installed on the
# target Pi. That's what avoids "GLIBC_x.xx not found" on a Raspberry Pi
# OS release with an older glibc than this build image's (Bookworm,
# glibc 2.36): glibc is forward-compatible (a newer libc.so.6 still
# satisfies programs built against older symbol versions), so shipping
# ours and forcing its use works regardless of the Pi's own OS version.
# Verified by running the resulting bundle inside a `debian:buster-slim`
# (glibc 2.28) container with no Go/build tooling installed at all.
#
# Note: the ELF interpreter path (PT_INTERP) can't be relative/
# $ORIGIN-based — the kernel resolves it literally, before any dynamic
# linking happens — which is why this ships a wrapper script that
# invokes the bundled loader explicitly (see docker/pi-wrapper.sh)
# instead of patching the real binary's own interpreter.
#
# Build (see `make build-pi-docker`, which wraps this):
#   docker buildx build --platform linux/arm/v7 --target export \
#       --output type=local,dest=bin .
#
# Deploy: copy the whole output directory to the Pi (the wrapper
# script, the .bin binary, and lib/, kept together) and run the wrapper
# script — it finds its own lib/ directory next to itself.
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

COPY docker/pi-wrapper.sh /out/songarooni-linux-arm
RUN chmod +x /out/songarooni-linux-arm

# Minimal stage so `docker buildx --output type=local` can export just
# the build's output (wrapper script + binary + bundled libs), without
# pulling the whole build image along with it.
FROM scratch AS export
COPY --from=build /out/ /
