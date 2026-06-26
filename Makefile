VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test install lint clean

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/goast ./cmd/goast

test:
	go test ./...

install:
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/goast

lint:
	go vet ./...

clean:
	rm -rf bin/
