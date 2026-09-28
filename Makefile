.PHONY: build test run clean

ifeq ($(OS),Windows_NT)
EXE := .exe
endif

build:
	mkdir -p dist
	rm -f dist/backend$(EXE)
	go build -ldflags="-s -w" -trimpath -o dist/whatzap$(EXE) ./cmd/whatzap

test:
	go test ./...

run:
	go run ./cmd/whatzap

clean:
	rm -rf dist
