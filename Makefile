.PHONY: help all build build-mcp firmware-sign-tool build-linux build-linux-amd64 build-linux-arm64 web-build fmt fmt-check vet test check clean run-routerd run-applyd iso dist dist-arm64 dist-amd64 signed-dist-amd64 signed-dist-arm64

CARGO ?= cargo
RUST_MANIFEST := rust/Cargo.toml
# router-update/recovery and the installed router-setup firstboot verifier
# run outside the A/B slot, and every A/B activation requires the candidate's
# copy to be byte-identical to the installed one (router-update's runtime
# layout check). scripts/build-rust.sh therefore builds them without version,
# commit or date; traceability for these binaries is carried by the signed
# release manifest, not by an embedded commit.
BUILD_VERSION ?= $(shell cat VERSION 2>/dev/null || echo dev)
BUILD_COMMIT ?= unknown
BUILD_DATE ?= unknown
RELEASE_VERSION ?=
RELEASE_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null)
FIRMWARE_SIGNING_KEY ?=
export BUILD_VERSION BUILD_COMMIT BUILD_DATE
FIRMWARE_SIGN := rust/target/release/firmware-sign

help:
	@printf '%s\n' \
		'Minimal Router OS development targets:' \
		'  make build               Build the router binaries for the host' \
		'  make build-mcp           Build the optional local MCP bridge' \
		'  make build-linux-amd64   Cross-build router binaries for Linux x86-64' \
		'  make build-linux-arm64   Cross-build router binaries for Linux ARM64' \
		'  make web-build           Build the React dashboard' \
		'  make fmt                 Format Rust source files' \
		'  make fmt-check           Fail when Rust source is not formatted' \
		'  make vet                 Run clippy' \
		'  make test                Run the Rust workspace tests' \
		'  make check               Run format, tests, vet, and dashboard build' \
		'  make dist-amd64          Build the self-contained x86-64 archive' \
		'  make dist-arm64          Build the self-contained ARM64 archive' \
		'  make signed-dist-amd64   Build, sign, and repack x86-64 archive + manifest' \
		'  make signed-dist-arm64   Build, sign, and repack ARM64 archive + manifest' \
		'  make dist                Build both distribution archives' \
		'  make iso                 Prepare the Alpine image overlay (not a signed release ISO)' \
		'  make clean               Remove local build output'

all: build

build:
	mkdir -p bin
	MINIMALROUTER_VERSION='$(BUILD_VERSION)' MINIMALROUTER_COMMIT='$(BUILD_COMMIT)' MINIMALROUTER_BUILD_DATE='$(BUILD_DATE)' \
		$(CARGO) build --release --locked --manifest-path $(RUST_MANIFEST) \
		--bin routerd --bin router-applyd --bin router-recovery --bin router-update --bin router-setup \
		--bin firmware-sign --bin firmware-keygen
	for bin in routerd router-applyd router-recovery router-update router-setup firmware-sign firmware-keygen; do \
		cp rust/target/release/$$bin bin/$$bin; \
	done

build-mcp:
	mkdir -p bin
	$(CARGO) build --release --locked --manifest-path $(RUST_MANIFEST) --bin minimalrouter-mcp
	cp rust/target/release/minimalrouter-mcp bin/minimalrouter-mcp

# Static musl binaries; see scripts/build-rust.sh for the toolchain.
build-linux: build-linux-amd64

build-linux-amd64:
	mkdir -p bin
	sh scripts/build-rust.sh amd64 all bin

build-linux-arm64:
	mkdir -p bin
	sh scripts/build-rust.sh arm64 all bin

# The release signer is built for the build host, not for the appliance.
firmware-sign-tool:
	$(CARGO) build --release --locked --manifest-path $(RUST_MANIFEST) --bin firmware-sign

web-build:
	pnpm --dir web build

fmt:
	$(CARGO) fmt --manifest-path $(RUST_MANIFEST) --all

fmt-check:
	$(CARGO) fmt --manifest-path $(RUST_MANIFEST) --all --check

vet:
	$(CARGO) clippy --manifest-path $(RUST_MANIFEST) --workspace --all-targets --locked

test:
	$(CARGO) test --manifest-path $(RUST_MANIFEST) --workspace --locked

check: fmt-check test vet web-build

run-routerd:
	$(CARGO) run --manifest-path $(RUST_MANIFEST) --bin routerd

run-applyd:
	$(CARGO) run --manifest-path $(RUST_MANIFEST) --bin router-applyd

iso: web-build
	sh packaging/alpine/build-iso.sh

# Keep architecture-specific builds explicit; only packaging is shared.
dist-arm64: build-linux-arm64 web-build
	@sh scripts/package-dist.sh arm64 "$(BUILD_VERSION)"

dist-amd64: build-linux-amd64 web-build
	@sh scripts/package-dist.sh amd64 "$(BUILD_VERSION)"

signed-dist-amd64: firmware-sign-tool
	@test -n "$(RELEASE_VERSION)" || { echo 'ERROR: RELEASE_VERSION is required (for example 0.1.3)' >&2; exit 1; }
	@test -n "$(FIRMWARE_SIGNING_KEY)" || { echo 'ERROR: FIRMWARE_SIGNING_KEY is required' >&2; exit 1; }
	@test -f "$(FIRMWARE_SIGNING_KEY)" || { echo 'ERROR: firmware signing key file does not exist' >&2; exit 1; }
	@set -eu; \
		version='$(RELEASE_VERSION)'; version="$${version#v}"; \
		commit='$(RELEASE_COMMIT)'; \
		build_date="$$(git show -s --format=%cI "$$commit")"; \
		$(MAKE) BUILD_VERSION="$$version" BUILD_COMMIT="$$commit" BUILD_DATE="$$build_date" dist-amd64; \
		$(FIRMWARE_SIGN) \
			--dir build/dist/minimalrouter-linux-amd64 \
			--key "$(FIRMWARE_SIGNING_KEY)" \
			--version "$$version" \
			--commit "$$commit" \
			--public-key-output build/dist/minimalrouter-linux-amd64/firmware-signing.pub \
			--output build/minimalrouter-linux-amd64.manifest.json; \
		cp build/minimalrouter-linux-amd64.manifest.json build/dist/minimalrouter-linux-amd64/release-manifest.json; \
		COPYFILE_DISABLE=1 tar czf build/minimalrouter-linux-amd64.tar.gz -C build/dist minimalrouter-linux-amd64; \
		sh scripts/checksum-file.sh build/minimalrouter-linux-amd64.tar.gz build/minimalrouter-linux-amd64.tar.gz.sha256; \
		echo "=== Signed distribution ready ==="; \
		ls -lh build/minimalrouter-linux-amd64.tar.gz build/minimalrouter-linux-amd64.manifest.json build/minimalrouter-linux-amd64.tar.gz.sha256

signed-dist-arm64: firmware-sign-tool
	@test -n "$(RELEASE_VERSION)" || { echo 'ERROR: RELEASE_VERSION is required (for example 0.1.3)' >&2; exit 1; }
	@test -n "$(FIRMWARE_SIGNING_KEY)" || { echo 'ERROR: FIRMWARE_SIGNING_KEY is required' >&2; exit 1; }
	@test -f "$(FIRMWARE_SIGNING_KEY)" || { echo 'ERROR: firmware signing key file does not exist' >&2; exit 1; }
	@set -eu; \
		version='$(RELEASE_VERSION)'; version="$${version#v}"; \
		commit='$(RELEASE_COMMIT)'; \
		build_date="$$(git show -s --format=%cI "$$commit")"; \
		$(MAKE) BUILD_VERSION="$$version" BUILD_COMMIT="$$commit" BUILD_DATE="$$build_date" dist-arm64; \
		$(FIRMWARE_SIGN) \
			--dir build/dist/minimalrouter-linux-arm64 \
			--key "$(FIRMWARE_SIGNING_KEY)" \
			--version "$$version" \
			--commit "$$commit" \
			--public-key-output build/dist/minimalrouter-linux-arm64/firmware-signing.pub \
			--output build/minimalrouter-linux-arm64.manifest.json; \
		cp build/minimalrouter-linux-arm64.manifest.json build/dist/minimalrouter-linux-arm64/release-manifest.json; \
		COPYFILE_DISABLE=1 tar czf build/minimalrouter-linux-arm64.tar.gz -C build/dist minimalrouter-linux-arm64; \
		sh scripts/checksum-file.sh build/minimalrouter-linux-arm64.tar.gz build/minimalrouter-linux-arm64.tar.gz.sha256; \
		echo "=== Signed distribution ready ==="; \
		ls -lh build/minimalrouter-linux-arm64.tar.gz build/minimalrouter-linux-arm64.manifest.json build/minimalrouter-linux-arm64.tar.gz.sha256

# Build both architectures
dist: dist-arm64 dist-amd64

clean:
	rm -rf bin/ build/
