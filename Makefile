BINARY_NAME=reign
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS=-ldflags "-X github.com/reign/internal/version.Version=$(VERSION) -X github.com/reign/internal/version.GitCommit=$(GIT_COMMIT) -X github.com/reign/internal/version.BuildTime=$(BUILD_TIME)"

.PHONY: default build build-linux test test-verbose test-coverage clean install

default: build

build:
	go build $(LDFLAGS) -o $(BINARY_NAME) .

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_NAME) .

test:
	go test ./...

test-verbose:
	go test -v ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

clean:
	rm -f $(BINARY_NAME) coverage.out coverage.html

install: build
	sudo cp $(BINARY_NAME) /usr/local/bin/
	@echo "Installed $(BINARY_NAME) to /usr/local/bin/"

run:
	go run . -config config.json

fmt:
	go fmt ./...

vet:
	go vet ./...

lint: fmt vet
