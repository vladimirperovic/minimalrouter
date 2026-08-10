.PHONY: help all build build-mcp build-linux build-linux-amd64 build-linux-arm64 web-build fmt fmt-check vet test check clean run-routerd run-applyd iso dist dist-arm64 dist-amd64

GO_BUILD_FLAGS := -trimpath
GO_LDFLAGS := -s -w -buildid=

help:
	@printf '%s\n' \
		'Minimal Router OS development targets:' \
		'  make build               Build routerd and router-applyd for the host' \
		'  make build-mcp           Build the optional local MCP bridge' \
		'  make build-linux-amd64   Cross-build router binaries for Linux x86-64' \
		'  make build-linux-arm64   Cross-build router binaries for Linux ARM64' \
		'  make web-build           Build the React dashboard' \
		'  make fmt                 Format Go source files' \
		'  make fmt-check           Fail when Go source is not formatted' \
		'  make vet                 Run go vet' \
		'  make test                Run Go tests with the race detector' \
		'  make check               Run format, tests, vet, and dashboard build' \
		'  make dist-amd64          Build the self-contained x86-64 archive' \
		'  make dist-arm64          Build the self-contained ARM64 archive' \
		'  make dist                Build both distribution archives' \
		'  make iso                 Prepare the Alpine image overlay (not a signed release ISO)' \
		'  make clean               Remove local build output'

all: build

build:
	mkdir -p bin
	go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/routerd ./cmd/routerd
	go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd ./cmd/router-applyd
	go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-recovery ./cmd/router-recovery
	go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-update ./cmd/router-update
	go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/firmware-sign ./cmd/firmware-sign
	go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/firmware-keygen ./cmd/firmware-keygen

build-mcp:
	mkdir -p bin
	go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/minimalrouter-mcp ./cmd/minimalrouter-mcp

build-linux:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/routerd-linux-amd64 ./cmd/routerd
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd-linux-amd64 ./cmd/router-applyd
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-recovery-linux-amd64 ./cmd/router-recovery
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-update-linux-amd64 ./cmd/router-update

build-linux-amd64: build-linux

build-linux-arm64:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/routerd-linux-arm64 ./cmd/routerd
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd-linux-arm64 ./cmd/router-applyd
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-recovery-linux-arm64 ./cmd/router-recovery
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-update-linux-arm64 ./cmd/router-update

web-build:
	pnpm --dir web build

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f)

fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || { \
		echo 'Go files require gofmt:' >&2; \
		gofmt -l cmd internal >&2; \
		exit 1; \
	}

vet:
	go vet ./...

test:
	go test -race ./...

check: fmt-check test vet web-build

run-routerd:
	go run ./cmd/routerd

run-applyd:
	go run ./cmd/router-applyd

iso: web-build
	sh packaging/alpine/build-iso.sh

# Build distributable tarball for arm64 (Apple Silicon / Raspberry Pi)
dist-arm64: build-linux-arm64 web-build
	@echo "=== Building Minimal Router OS distribution (arm64) ==="
	@rm -rf build/dist/minimalrouter-linux-arm64
	@mkdir -p \
		build/dist/minimalrouter-linux-arm64/bin \
		build/dist/minimalrouter-linux-arm64/web/dist \
		build/dist/minimalrouter-linux-arm64/init.d \
		build/dist/minimalrouter-linux-arm64/sysctl \
		build/dist/minimalrouter-linux-arm64/modules \
		build/dist/minimalrouter-linux-arm64/logrotate
	@cp bin/routerd-linux-arm64 build/dist/minimalrouter-linux-arm64/bin/routerd-arm64
	@cp bin/router-applyd-linux-arm64 build/dist/minimalrouter-linux-arm64/bin/router-applyd-arm64
	@cp bin/router-recovery-linux-arm64 build/dist/minimalrouter-linux-arm64/bin/router-recovery-arm64
	@cp bin/router-update-linux-arm64 build/dist/minimalrouter-linux-arm64/bin/router-update-arm64
	@cp -R web/dist/. build/dist/minimalrouter-linux-arm64/web/dist/
	@cp packaging/alpine/slot-exec build/dist/minimalrouter-linux-arm64/slot-exec
	@cp packaging/alpine/compatibility.json build/dist/minimalrouter-linux-arm64/compatibility.json
	@cp packaging/alpine/routerd.initd build/dist/minimalrouter-linux-arm64/init.d/routerd
	@cp packaging/alpine/router-applyd.initd build/dist/minimalrouter-linux-arm64/init.d/router-applyd
	@cp packaging/alpine/pppoe-wan.initd build/dist/minimalrouter-linux-arm64/init.d/pppoe-wan
	@cp packaging/alpine/99-minimalrouter.conf build/dist/minimalrouter-linux-arm64/sysctl/99-minimalrouter.conf
	@cp packaging/alpine/minimalrouter.modules build/dist/minimalrouter-linux-arm64/modules/minimalrouter.conf
	@cp packaging/alpine/minimalrouter.logrotate build/dist/minimalrouter-linux-arm64/logrotate/minimalrouter
	@cp packaging/alpine/ip-up.d-minimalrouter-qos build/dist/minimalrouter-linux-arm64/ip-up.d-minimalrouter-qos
	@cp packaging/alpine/install-dist.sh build/dist/minimalrouter-linux-arm64/install.sh
	@chmod +x build/dist/minimalrouter-linux-arm64/install.sh build/dist/minimalrouter-linux-arm64/slot-exec build/dist/minimalrouter-linux-arm64/init.d/* build/dist/minimalrouter-linux-arm64/ip-up.d-minimalrouter-qos
	@tar czf build/minimalrouter-linux-arm64.tar.gz -C build/dist minimalrouter-linux-arm64
	@sh scripts/checksum-file.sh build/minimalrouter-linux-arm64.tar.gz build/minimalrouter-linux-arm64.tar.gz.sha256
	@echo "=== Distribution: build/minimalrouter-linux-arm64.tar.gz ==="
	@ls -lh build/minimalrouter-linux-arm64.tar.gz build/minimalrouter-linux-arm64.tar.gz.sha256

# Build distributable tarball for amd64 (x86_64 servers)
dist-amd64: build-linux-amd64 web-build
	@echo "=== Building Minimal Router OS distribution (amd64) ==="
	@rm -rf build/dist/minimalrouter-linux-amd64
	@mkdir -p \
		build/dist/minimalrouter-linux-amd64/bin \
		build/dist/minimalrouter-linux-amd64/web/dist \
		build/dist/minimalrouter-linux-amd64/init.d \
		build/dist/minimalrouter-linux-amd64/sysctl \
		build/dist/minimalrouter-linux-amd64/modules \
		build/dist/minimalrouter-linux-amd64/logrotate
	@cp bin/routerd-linux-amd64 build/dist/minimalrouter-linux-amd64/bin/routerd-amd64
	@cp bin/router-applyd-linux-amd64 build/dist/minimalrouter-linux-amd64/bin/router-applyd-amd64
	@cp bin/router-recovery-linux-amd64 build/dist/minimalrouter-linux-amd64/bin/router-recovery-amd64
	@cp bin/router-update-linux-amd64 build/dist/minimalrouter-linux-amd64/bin/router-update-amd64
	@cp -R web/dist/. build/dist/minimalrouter-linux-amd64/web/dist/
	@cp packaging/alpine/slot-exec build/dist/minimalrouter-linux-amd64/slot-exec
	@cp packaging/alpine/compatibility.json build/dist/minimalrouter-linux-amd64/compatibility.json
	@cp packaging/alpine/routerd.initd build/dist/minimalrouter-linux-amd64/init.d/routerd
	@cp packaging/alpine/router-applyd.initd build/dist/minimalrouter-linux-amd64/init.d/router-applyd
	@cp packaging/alpine/pppoe-wan.initd build/dist/minimalrouter-linux-amd64/init.d/pppoe-wan
	@cp packaging/alpine/99-minimalrouter.conf build/dist/minimalrouter-linux-amd64/sysctl/99-minimalrouter.conf
	@cp packaging/alpine/minimalrouter.modules build/dist/minimalrouter-linux-amd64/modules/minimalrouter.conf
	@cp packaging/alpine/minimalrouter.logrotate build/dist/minimalrouter-linux-amd64/logrotate/minimalrouter
	@cp packaging/alpine/ip-up.d-minimalrouter-qos build/dist/minimalrouter-linux-amd64/ip-up.d-minimalrouter-qos
	@cp packaging/alpine/install-dist.sh build/dist/minimalrouter-linux-amd64/install.sh
	@chmod +x build/dist/minimalrouter-linux-amd64/install.sh build/dist/minimalrouter-linux-amd64/slot-exec build/dist/minimalrouter-linux-amd64/init.d/* build/dist/minimalrouter-linux-amd64/ip-up.d-minimalrouter-qos
	@if [ -f firmware-signing.pub ]; then cp firmware-signing.pub build/dist/minimalrouter-linux-amd64/firmware-signing.pub; fi
	@tar czf build/minimalrouter-linux-amd64.tar.gz -C build/dist minimalrouter-linux-amd64
	@sh scripts/checksum-file.sh build/minimalrouter-linux-amd64.tar.gz build/minimalrouter-linux-amd64.tar.gz.sha256
	@echo "=== Distribution: build/minimalrouter-linux-amd64.tar.gz ==="
	@ls -lh build/minimalrouter-linux-amd64.tar.gz build/minimalrouter-linux-amd64.tar.gz.sha256

# Build both architectures
dist: dist-arm64 dist-amd64

clean:
	rm -rf bin/ build/
