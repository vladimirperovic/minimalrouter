.PHONY: all build build-linux build-linux-arm64 test clean run-routerd run-applyd dist dist-arm64 dist-amd64

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

build-linux-arm64:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/routerd ./cmd/routerd
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd ./cmd/router-applyd

test:
	go test -v ./...

run-routerd:
	go run ./cmd/routerd

run-applyd:
	go run ./cmd/router-applyd

iso:
	sh packaging/alpine/build-iso.sh

# Build distributable tarball for arm64 (Apple Silicon / Raspberry Pi)
dist-arm64: build-linux-arm64
	@echo "=== Building Minimal Router OS distribution (arm64) ==="
	@mkdir -p build/dist/minimalrouter-linux-arm64/{bin,web/dist,init.d,sysctl,modules}
	@cp bin/routerd-linux-arm64 build/dist/minimalrouter-linux-arm64/bin/routerd-arm64
	@cp bin/router-applyd-linux-arm64 build/dist/minimalrouter-linux-arm64/bin/router-applyd-arm64
	@cp -R web/dist/. build/dist/minimalrouter-linux-arm64/web/dist/
	@cp packaging/alpine/routerd.initd build/dist/minimalrouter-linux-arm64/init.d/routerd
	@cp packaging/alpine/router-applyd.initd build/dist/minimalrouter-linux-arm64/init.d/router-applyd
	@cp packaging/alpine/pppoe-wan.initd build/dist/minimalrouter-linux-arm64/init.d/pppoe-wan
	@cp packaging/alpine/99-minimalrouter.conf build/dist/minimalrouter-linux-arm64/sysctl/99-minimalrouter.conf
	@cp packaging/alpine/minimalrouter.modules build/dist/minimalrouter-linux-arm64/modules/minimalrouter.conf
	@cp packaging/alpine/install-dist.sh build/dist/minimalrouter-linux-arm64/install.sh
	@chmod +x build/dist/minimalrouter-linux-arm64/install.sh
	@tar czf build/minimalrouter-linux-arm64.tar.gz -C build/dist minimalrouter-linux-arm64
	@echo "=== Distribution: build/minimalrouter-linux-arm64.tar.gz ==="
	@ls -lh build/minimalrouter-linux-arm64.tar.gz

# Build distributable tarball for amd64 (x86_64 servers)
dist-amd64: build-linux
	@echo "=== Building Minimal Router OS distribution (amd64) ==="
	@mkdir -p build/dist/minimalrouter-linux-amd64/{bin,web/dist,init.d,sysctl,modules}
	@cp bin/routerd build/dist/minimalrouter-linux-amd64/bin/routerd-amd64
	@cp bin/router-applyd build/dist/minimalrouter-linux-amd64/bin/router-applyd-amd64
	@cp -R web/dist/. build/dist/minimalrouter-linux-amd64/web/dist/
	@cp packaging/alpine/routerd.initd build/dist/minimalrouter-linux-amd64/init.d/routerd
	@cp packaging/alpine/router-applyd.initd build/dist/minimalrouter-linux-amd64/init.d/router-applyd
	@cp packaging/alpine/pppoe-wan.initd build/dist/minimalrouter-linux-amd64/init.d/pppoe-wan
	@cp packaging/alpine/99-minimalrouter.conf build/dist/minimalrouter-linux-amd64/sysctl/99-minimalrouter.conf
	@cp packaging/alpine/minimalrouter.modules build/dist/minimalrouter-linux-amd64/modules/minimalrouter.conf
	@cp packaging/alpine/install-dist.sh build/dist/minimalrouter-linux-amd64/install.sh
	@chmod +x build/dist/minimalrouter-linux-amd64/install.sh
	@tar czf build/minimalrouter-linux-amd64.tar.gz -C build/dist minimalrouter-linux-amd64
	@echo "=== Distribution: build/minimalrouter-linux-amd64.tar.gz ==="
	@ls -lh build/minimalrouter-linux-amd64.tar.gz

# Build both architectures
dist: dist-arm64 dist-amd64

clean:
	rm -rf bin/ build/
