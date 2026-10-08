#!/usr/bin/env bash
# Isolated test clients only. The old libcurl build is HTTP-only and is never
# distributed with Drone or installed over the host's Git / curl libraries.
set -Eeuo pipefail
clone_test_prefix=${1:?Usage: build-test-clone-clients.sh /absolute/install/path}
case "$clone_test_prefix" in /*) ;; *) echo 'Install path must be absolute' >&2; exit 1 ;; esac
clone_test_build=$(mktemp -d)
trap 'rm -rf "$clone_test_build"' EXIT
cd "$clone_test_build"
download() {
  curl --fail --silent --show-error --location --retry 2 --max-time 180 "$1" --output "$2"
  printf '%s  %s\n' "$3" "$2" | sha256sum --check -
  tar --no-same-owner -xzf "$2"
}
download https://curl.se/download/archeology/curl-7.19.7.tar.gz curl-7.19.7.tar.gz 98d247406d2b5ef7621d7a5475dfcf48836b2b454e22c954cfbb379e6dbb44c7
download https://codeload.github.com/git/git/tar.gz/refs/tags/v2.8.0 git-2.8.0.tar.gz 8a9b7494294f0d108c9f6c3cfa5747804b9d2b0c63edefc48fb4c81f0e5bcccf
download https://codeload.github.com/git/git/tar.gz/refs/tags/v2.43.0 git-2.43.0.tar.gz 4e1599231f77d64f01d46a773e7741218fb8d98f2a05ccd2b8f35fc7ecf62040
build_clients() {
  (
    cd curl-7.19.7
    ./configure --prefix="$clone_test_prefix/curl-7.19.7" --without-ssl --without-libssh2 --disable-ldap --disable-ldaps --disable-shared
    make -j2
    make install
  )
  make -C git-2.8.0 -j2 prefix="$clone_test_prefix/git-2.8.0" \
    CURL_LIBCURL="$clone_test_prefix/curl-7.19.7/lib/libcurl.a -lz" \
    CURLDIR="$clone_test_prefix/curl-7.19.7" CURL_CONFIG="$clone_test_prefix/curl-7.19.7/bin/curl-config" \
    NO_OPENSSL=YesPlease NO_GETTEXT=YesPlease NO_TCLTK=YesPlease \
    NO_PERL=YesPlease NO_PYTHON=YesPlease CFLAGS='-O2 -fcommon' install
  make -C git-2.43.0 -j2 prefix="$clone_test_prefix/git-2.43.0" \
    NO_OPENSSL=YesPlease NO_GETTEXT=YesPlease NO_TCLTK=YesPlease \
    NO_PERL=YesPlease NO_PYTHON=YesPlease CFLAGS='-O2' install
}
# Keep compiler noise out of successful CI logs, but retain errors on failure.
trap 'cat "$clone_test_build/build.log" >&2' ERR
build_clients > build.log 2>&1
trap - ERR
"$clone_test_prefix/curl-7.19.7/bin/curl" --version
"$clone_test_prefix/git-2.8.0/bin/git" --version
"$clone_test_prefix/git-2.43.0/bin/git" --version
