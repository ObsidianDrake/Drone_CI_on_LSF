#!/usr/bin/env bash
# Test-only Git client: never install this over the system Git.
set -euo pipefail

test_git_prefix=${1:?Usage: build-test-git-1.8.3.1.sh /absolute/install/path}
case "$test_git_prefix" in
  /*) ;;
  *) echo 'Install path must be absolute' >&2; exit 1 ;;
esac
test_git_build=$(mktemp -d)
trap 'rm -rf "$test_git_build"' EXIT
cd "$test_git_build"
curl --fail --location --retry 2 --max-time 180 \
  https://www.kernel.org/pub/software/scm/git/git-1.8.3.1.tar.xz \
  --output git-1.8.3.1.tar.xz
printf '%s\n' '40f785ec3e9e2f476f592ce3310344fc03e8d4acce8ce2a39c11441297de3d75  git-1.8.3.1.tar.xz' | sha256sum --check -
tar --no-same-owner -xJf git-1.8.3.1.tar.xz
# Use bundled SHA-1 instead of the obsolete OpenSSL API; HTTP still uses curl.
# -fcommon supports this legacy source on current GCC versions.
if ! make -C git-1.8.3.1 -j2 prefix="$test_git_prefix" \
  NO_OPENSSL=YesPlease NO_GETTEXT=YesPlease NO_TCLTK=YesPlease \
  NO_PERL=YesPlease NO_PYTHON=YesPlease CFLAGS='-O2 -fcommon' \
  install > build.log 2>&1; then
  cat build.log
  exit 1
fi
"$test_git_prefix/bin/git" --version
