#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
image=${PORTFOLIO_SMOKE_IMAGE:-portfolio-tracker:smoke}
probe=busybox:1.38.0@sha256:fd7dc98638c8e305f4dc34e979f1c0fdfdcaeb0fbf8fcff77ae834b6da3d7e6e
docker build --tag "$image" .
test "$(docker image inspect --format '{{.Config.User}}' "$image")" = 25110:25110
fixture=$(mktemp -d)
container=$(docker run --detach --network none "$image")
cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    if [ "$status" -ne 0 ]; then
        docker logs "$container" >&2
    fi
    docker rm --force --volumes "$container" >/dev/null
    rm -rf "$fixture"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
fetch() {
    docker run --rm --network "container:$container" "$probe" \
        wget -q -O - "http://127.0.0.1:8080$1"
}
wait_ready() {
    attempts=0
    until fetch /api/settings > /dev/null; do
        attempts=$((attempts + 1))
        test "$attempts" -lt 30
        sleep 1
    done
}
wait_ready
fetch / | grep -q '<!DOCTYPE html>'
fetch /static/style.css > /dev/null
fetch /static/app.js > /dev/null
# Change durable state: comparing defaults alone cannot detect a reset database.
printf 'PATCH /api/settings/currency HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 18\r\nConnection: close\r\n\r\n{"currency":"USD"}' |
    docker run --rm --interactive --network "container:$container" "$probe" \
        nc -w 10 127.0.0.1 8080 > "$fixture/update-response.txt"
grep -q '^HTTP/1.1 200 ' "$fixture/update-response.txt"
fetch /api/settings > "$fixture/settings-before.json"
grep -q '"display_currency":"USD"' "$fixture/settings-before.json"
docker cp "$container:/data/portfolio.db" "$fixture/portfolio.db"
test -s "$fixture/portfolio.db"
docker cp "$container:/usr/share/licenses/portfolio-tracker/LICENSE" "$fixture/LICENSE"
test -s "$fixture/LICENSE"
docker restart "$container" >/dev/null
wait_ready
fetch /api/settings > "$fixture/settings-after.json"
cmp "$fixture/settings-before.json" "$fixture/settings-after.json"
echo "Container smoke test passed"
