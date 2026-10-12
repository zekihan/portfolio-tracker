#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
version=$(cat VERSION)
sh ./scripts/validate_version.sh "$version"
if [ "${1:-}" != "v$version" ]; then
    echo "Release tag ${1:-<missing>} must match VERSION (v$version)" >&2
    exit 1
fi
