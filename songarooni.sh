#!/bin/sh
# Launcher for running from the Pi desktop. Resolves every path relative
# to this script's own location (not the caller's working directory),
# since a desktop launcher doesn't reliably run with cwd set to wherever
# this script lives. Deploy this alongside bin/, songs.txt, and
# whisper.cpp/ as siblings in one directory (e.g. ~/songarooni/).
#
# Any arguments passed to this script are forwarded to songarooni, after
# the defaults below — Go's flag package uses the last occurrence of a
# repeated flag, so passing e.g. --model here overrides the default one.
# Example: ./songarooni.sh --slideshow-dir ~/slides
set -e
dir=$(cd "$(dirname "$(readlink -f "$0")")" && pwd)
cd "$dir"

exec ./bin/songarooni-linux-arm64 \
    --songs songs.txt \
    --model ./bin/models/ggml-base.en.bin \
    --whisper-bin ./bin/whisper-cli \
    "$@"
