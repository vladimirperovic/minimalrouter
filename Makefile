.PHONY: all build build-linux test clean run-routerd run-applyd

GO_BUILD_FLAGS := -trimpath
GO_LDFLAGS := -s -w -buildid=

all: build

build:
	mkdir -p bin
	go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/routerd ./cmd/routerd
	go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd ./cmd/router-applyd

build-linux:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/routerd ./cmd/routerd
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd ./cmd/router-applyd

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
