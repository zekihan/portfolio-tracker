#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ -n "$(git status --porcelain)" ]; then
    echo "Working directory dirty" >&2
    exit 1
fi
current=$(cat VERSION)
sh ./scripts/validate_version.sh "$current"
next=${1:-$(printf '%s\n' "$current" | awk -F. '{printf "%d.%d.%d", $1, $2, $3 + 1}')}
sh ./scripts/validate_version.sh "$next" "$current"
if git rev-parse --verify --quiet "refs/tags/v$next" >/dev/null; then
    echo "Tag v$next already exists" >&2
    exit 1
fi
make lint test
git diff --exit-code
printf '%s\n' "$next" > VERSION
git add VERSION
git commit -m "Bump version to $next"
git tag -s -a "v$next" -m "Version $next"
printf 'Created local version %s and tag v%s\n' "$next" "$next"
