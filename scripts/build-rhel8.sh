#!/bin/bash
# Run from the repository root on RHEL 8 or inside a UBI 8 build container.
set -euo pipefail

if [ "$(getconf GNU_LIBC_VERSION)" != "glibc 2.28" ]; then
    echo "Build on RHEL 8 / UBI 8 (glibc 2.28) to preserve RHEL 8 compatibility." >&2
    exit 1
fi

export CGO_ENABLED=1
export GOOS=linux
export GOARCH=amd64
export CC=/usr/bin/gcc

mkdir -p dist
go build -mod=readonly -trimpath -o dist/drone-server ./cmd/drone-server
go version -m dist/drone-server
file dist/drone-server

# Fail the build if a newer glibc requirement ever enters the artifact.
glibc_versions=$(readelf --version-info --wide dist/drone-server |
    grep -oE 'GLIBC_[0-9]+\.[0-9]+(\.[0-9]+)?' | sort -Vu)
echo "$glibc_versions"
if ! echo "$glibc_versions" | awk -F '[_.]' '
    $2 > 2 || ($2 == 2 && $3 > 28) { exit 1 }
'; then
    echo "The binary requires a glibc version newer than RHEL 8 provides." >&2
    exit 1
fi

# Exercise the dynamic loader without starting the configured server.
ldd dist/drone-server
dist/drone-server -h
