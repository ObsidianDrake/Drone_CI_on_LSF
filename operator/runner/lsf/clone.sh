# Internal POSIX sh script. Keep commands compatible with Git 1.8.3.1.
set -eu

fail() {
    printf '[clone] ERROR: %s\n' "$*" >&2
    exit 1
}

# Match ordinary command steps. Call only for commands about to run, and
# display variable names rather than expanding potentially sensitive values.
trace() {
    printf '\033[32m+ %s\033[0;37m\n' "$1"
}

printf '[clone] Host: %s\n' "$(hostname)"
if [ -r /etc/redhat-release ]; then
    cat /etc/redhat-release
elif [ -r /etc/os-release ]; then
    cat /etc/os-release
fi
printf '[clone] Git executable: %s\n' "$(command -v git)"
trace 'git --version'
git --version

sha=${DRONE_COMMIT_SHA:-}
case "$sha" in
    ''|*[!0-9a-fA-F]*) fail 'DRONE_COMMIT_SHA must be a full hexadecimal commit ID' ;;
esac
case ${#sha} in
    40|64) ;;
    *) fail 'DRONE_COMMIT_SHA must be a full hexadecimal commit ID' ;;
esac
sha=$(printf '%s' "$sha" | tr 'A-F' 'a-f')
ref=${DRONE_COMMIT_REF:-}
printf '[clone] Expected commit: %s\n' "$sha"

require_ref() {
    case "$ref" in
        refs/*) ;;
        *) fail 'Fallback requires a full DRONE_COMMIT_REF (refs/...)' ;;
    esac
    git check-ref-format "$ref" || fail 'Invalid DRONE_COMMIT_REF'
}

has_commit() {
    [ "$(git cat-file -t "$sha" 2>/dev/null)" = commit ]
}

trace 'git init .'
git init .
trace 'git remote add origin "$DRONE_REMOTE_URL"'
git remote add origin "$DRONE_REMOTE_URL"

# Positional arguments avoid word splitting or shell evaluation of input.
set -- --no-tags
fetch_display='git fetch --no-tags'
if [ "$clone_depth" -gt 0 ]; then
    set -- "$@" "--depth=$clone_depth"
    fetch_display="$fetch_display --depth=$clone_depth"
fi
printf '[clone] Fetching requested SHA\n'
trace "$fetch_display origin \"\$DRONE_COMMIT_SHA\""
if git fetch "$@" origin "$sha"; then
    printf '[clone] SHA fetch succeeded\n'
else
    fetch_status=$?
    printf '[clone] SHA fetch exited %s; trying the build ref\n' "$fetch_status"
    require_ref
    printf '[clone] Fetching ref: %s\n' "$ref"
    trace "$fetch_display origin \"\$DRONE_COMMIT_REF\""
    if git fetch "$@" origin "$ref"; then
        printf '[clone] Ref fetch succeeded\n'
    else
        fetch_status=$?
        printf '[clone] ERROR: ref fetch exited %s\n' "$fetch_status" >&2
        exit "$fetch_status"
    fi
fi

# A branch may have advanced beyond this build's SHA. Expand history once,
# only when a shallow fetch did not provide the expected commit.
if ! has_commit && [ "$clone_depth" -gt 0 ] && [ -s .git/shallow ]; then
    require_ref
    printf '[clone] Commit missing from shallow history; fetching full history of %s (may download more data)\n' "$ref"
    trace 'git fetch --no-tags --unshallow origin "$DRONE_COMMIT_REF"'
    if git fetch --no-tags --unshallow origin "$ref"; then
        printf '[clone] Full-history fetch succeeded\n'
    else
        fetch_status=$?
        printf '[clone] ERROR: full-history fetch exited %s\n' "$fetch_status" >&2
        exit "$fetch_status"
    fi
fi

has_commit || fail "Expected commit $sha is unavailable; refusing to checkout a different commit (the ref may have moved or been deleted)"
trace 'git -c advice.detachedHead=false checkout --force --detach "$DRONE_COMMIT_SHA"'
git -c advice.detachedHead=false checkout --force --detach "$sha"
trace 'git rev-parse --verify HEAD'
actual=$(git rev-parse --verify HEAD)
[ "$actual" = "$sha" ] || fail "HEAD $actual does not match expected commit $sha"
printf '[clone] Verified HEAD: %s\n' "$actual"
