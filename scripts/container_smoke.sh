#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
image=${PORTFOLIO_SMOKE_IMAGE:-portfolio-tracker:smoke}
docker build --tag "$image" .
container=$(docker run --detach --network none "$image")
cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    if [ "$status" -ne 0 ]; then
        docker logs "$container" >&2
    fi
    docker rm --force --volumes "$container" >/dev/null
    exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
wait_ready() {
    attempts=0
    until docker exec "$container" wget -q -O /dev/null http://127.0.0.1:8080/api/settings; do
        attempts=$((attempts + 1))
        test "$attempts" -lt 30
        sleep 1
    done
}
wait_ready
docker exec "$container" sh -ec '
    test "$(id -u)" = 25110
    test -s /data/portfolio.db
    test -r /usr/share/licenses/portfolio-tracker/LICENSE
    wget -q -O - http://127.0.0.1:8080/ | grep -q "<!DOCTYPE html>"
    wget -q -O /dev/null http://127.0.0.1:8080/static/style.css
    wget -q -O /dev/null http://127.0.0.1:8080/static/app.js
'
docker restart "$container" >/dev/null
wait_ready
echo "Container smoke test passed"
