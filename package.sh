#!/bin/sh
# Packages the Pi deployment bundle for transfer via thumb drive.
# Run `make build-pi-docker` first.
set -e
dir=$(cd "$(dirname "$(readlink -f "$0")")" && pwd)
cd "$dir"

# rm first: `zip -r` on an existing archive merges into it rather than
# replacing it, so a stale previous zip's contents (e.g. leftover files
# from a since-changed bin/ layout) would otherwise linger forever.
rm -f songarooni.zip
zip -r songarooni.zip bin songs.csv songarooni.sh

cp songarooni.zip ~/Desktop/songarooni.zip

# unzip at ~/songarooni/ and run ./songarooni.sh to launch the app
