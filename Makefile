.DEFAULT_GOAL := build

export CGO_ENABLED := 1

.PHONY: build
build:
	mkdir -p bin
	go build -o bin/drone-server ./cmd/drone-server
