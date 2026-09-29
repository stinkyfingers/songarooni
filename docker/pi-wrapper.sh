#!/bin/sh
# Generic wrapper: runs "<this script's own name>.bin" against the glibc
# bundled alongside it in lib/, instead of whatever glibc happens to be
# installed on this Pi. Used for both songarooni-linux-arm64 and
# whisper-cli — install this same file under each binary's name (see
# ../Dockerfile) and it finds its own paired .bin and the shared lib/.
#
# See ../Dockerfile for why this wrapper exists: the ELF interpreter path
# (PT_INTERP) can't be relative/$ORIGIN-based — the kernel resolves it
# literally, before any dynamic linking happens — so the bundled loader
# can't just be patched into each binary's own header. Invoking it
# explicitly here, with an explicit --library-path, sidesteps that.
set -e
dir=$(cd "$(dirname "$(readlink -f "$0")")" && pwd)
self=$(basename "$0")
exec "$dir/lib/ld-linux-aarch64.so.1" --library-path "$dir/lib" "$dir/$self.bin" "$@"
