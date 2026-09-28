#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

exe=""
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) exe=".exe" ;;
esac

case "${1:-build}" in
    build)
        mkdir -p dist
        rm -f "dist/backend$exe"
        go build -ldflags="-s -w" -trimpath -o "dist/whatzap$exe" ./cmd/whatzap
        ;;
    test)
        go test ./...
        ;;
    run)
        go run ./cmd/whatzap
        ;;
    clean)
        rm -rf dist
        ;;
    *)
        echo "usage: $0 [build|test|run|clean]" >&2
        exit 2
        ;;
esac
