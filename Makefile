BINARY  := songarooni
CMD     := ./cmd
BIN_DIR := bin

.PHONY: all build test vet fmt clean build-pi-native build-pi-cross build-pi-docker run-text

all: build test vet

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) $(CMD)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -rf $(BIN_DIR)

# Build directly on a Raspberry Pi (recommended deployment path). Mic
# capture (internal/audio, via malgo/miniaudio) requires CGO, and
# Raspberry Pi OS ships a C compiler by default, so a native build is the
# most reliable option. Copy the repo to the Pi and run this there.
build-pi-native:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 go build -o $(BIN_DIR)/$(BINARY)-linux-arm $(CMD)

# Best-effort cross-compile from macOS to a 32-bit Raspberry Pi OS
# (Raspberry Pi 3, armv7/GOARM=7). Requires an arm-linux-gnueabihf CGO
# cross toolchain on PATH, e.g.:
#   brew install messense/macos-cross-toolchains/arm-unknown-linux-gnueabihf
# and CC below pointed at its gcc. If this toolchain gives you trouble,
# use build-pi-native instead: it's the officially supported path.
build-pi-cross:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=7 \
		CC=arm-linux-gnueabihf-gcc \
		go build -o $(BIN_DIR)/$(BINARY)-linux-arm $(CMD)

# Cross-build for a Raspberry Pi 3 (32-bit, armv7) using Docker Buildx's
# QEMU emulation (see Dockerfile): runs an ordinary native `go build`
# inside a real armv7 Linux container, avoiding both the cross-compiler
# naming issues above and the risk of linking against a newer glibc than
# your actual Pi OS ships. Requires Docker Desktop (buildx included).
# Produces $(BIN_DIR)/songlistener-linux-arm.
build-pi-docker:
	mkdir -p $(BIN_DIR)
	docker buildx build --platform linux/arm/v7 --target export --output type=local,dest=$(BIN_DIR) .

# Convenience target for tuning the matcher: make run-text TEXT="..."
run-text:
	go run $(CMD) --songs songs.txt --text "$(TEXT)"
