BINARY  := songarooni
CMD     := ./cmd
BIN_DIR := bin

.PHONY: all build test vet fmt clean build-pi-native build-pi-docker run-text

all: build test vet

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY)-osx $(CMD)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -rf $(BIN_DIR)

# Build directly on a Raspberry Pi (recommended if you're on the Pi
# already, or a fallback if build-pi-docker gives you trouble). Mic
# capture (audio/mic.go, via malgo/miniaudio) requires CGO, and Raspberry
# Pi OS ships a C compiler by default, so a native build is the most
# reliable option — it also needs no GOARCH/GOARM override, since it
# just builds for whatever architecture the Pi it's running on actually
# is (32-bit or 64-bit). Copy the repo to the Pi and run this there. Only
# produces the songarooni binary — see build-pi-docker for a bundle that
# also includes whisper.cpp and its model.
build-pi-native:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 go build -o $(BIN_DIR)/$(BINARY)-linux-arm $(CMD)

# Cross-build for a Raspberry Pi 3 (64-bit, ARM64) using Docker Buildx's
# QEMU emulation (see Dockerfile): runs ordinary native builds (`go
# build`, whisper.cpp's own cmake build) inside a real ARM64 Linux
# container, avoiding the cross-compiler naming issues above for either.
# It also bundles its own glibc for both the songarooni binary and
# whisper-cli, each behind a wrapper script (see docker/pi-wrapper.sh),
# so it runs regardless of which Raspberry Pi OS release/glibc version
# the Pi actually has. Requires Docker Desktop (buildx included).
#
# $(BIN_DIR) is wiped first: `docker buildx --output type=local` only
# adds/overwrites files, it never removes ones left over from a
# previous build — without this, switching architectures (as happened
# going from armv7 to arm64) silently leaves stale, wrong-architecture
# files mixed in with the new ones.
build-pi-docker:
	rm -rf $(BIN_DIR)
	mkdir -p $(BIN_DIR)
	docker buildx build --platform linux/arm64 --target export --output type=local,dest=$(BIN_DIR) .

# Convenience target for tuning the matcher: make run-text TEXT="..."
run-text:
	go run $(CMD) --songs songs.csv --text "$(TEXT)"
