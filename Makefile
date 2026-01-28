.PHONY: build run clean test dev

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

build:
	go build $(LDFLAGS) -o terminus-pty .

run: build
	./terminus-pty

dev:
	go run . --host 0.0.0.0

test:
	go test -v ./...

clean:
	rm -f terminus-pty

install: build
	sudo cp terminus-pty /usr/local/bin/

deps:
	go mod tidy
	go mod download
