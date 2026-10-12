#!/bin/sh
# Validate a stable semantic version, optionally requiring it to exceed $2.
set -eu
valid() {
    case "$1" in
        *[!0-9.]*|'') return 1 ;;
    esac
    printf '%s\n' "$1" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
}
if ! valid "${1:-}"; then
    echo "Invalid version: ${1:-}" >&2
    exit 1
fi
if [ "$#" -gt 1 ]; then
    if ! valid "$2"; then
        echo "Invalid current version: $2" >&2
        exit 1
    fi
    # Compare decimal components as strings so very large versions stay exact.
    if ! awk -v next_version="$1" -v current="$2" 'BEGIN {
        split(next_version, n, "."); split(current, c, ".")
        for (i = 1; i <= 3; i++) {
            if (length(n[i]) != length(c[i])) exit !(length(n[i]) > length(c[i]))
            if ("x" n[i] != "x" c[i]) exit !("x" n[i] > "x" c[i])
        }
        exit 1
    }'; then
        echo "Version $1 must be newer than $2" >&2
        exit 1
    fi
fi
