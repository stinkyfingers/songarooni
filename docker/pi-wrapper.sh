#!/bin/sh
# Runs the bundled binary against the glibc shipped alongside it in
# lib/, instead of whatever glibc happens to be installed on this Pi.
# See ../Dockerfile for why this wrapper exists: the ELF interpreter
# path (PT_INTERP) can't be relative/$ORIGIN-based — the kernel resolves
# it literally, before any dynamic linking happens — so the bundled
# loader can't just be patched into the binary's own header. Invoking it
# explicitly here, with an explicit --library-path, sidesteps that.
set -e
dir=$(cd "$(dirname "$(readlink -f "$0")")" && pwd)
exec "$dir/lib/ld-linux-armhf.so.3" --library-path "$dir/lib" "$dir/songarooni-linux-arm.bin" "$@"
