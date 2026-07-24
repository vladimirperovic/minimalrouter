.PHONY: all build test clean run-routerd run-applyd

all: build

build:
	mkdir -p bin
	go build -o bin/routerd ./cmd/routerd
	go build -o bin/router-applyd ./cmd/router-applyd

test:
	go test -v ./...

run-routerd:
	go run ./cmd/routerd

run-applyd:
	go run ./cmd/router-applyd

iso:
	sh packaging/alpine/build-iso.sh

clean:
	rm -rf bin/ build/
