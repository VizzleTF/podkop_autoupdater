BINARY      := podkop_updater
PKG         := ./cmd/podkop_updater
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
BUILD_FLAGS := -trimpath -ldflags="$(LDFLAGS)"
DIST        := dist

.PHONY: all build build-all clean test fmt vet tidy

all: build

build:
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -o $(DIST)/$(BINARY) $(PKG)
	@ls -lh $(DIST)/$(BINARY)

# Cross-compile matrix for OpenWrt-relevant architectures.
build-all: clean
	@mkdir -p $(DIST)
	@for target in \
	  "amd64::"           \
	  "arm64::"           \
	  "arm:GOARM=7"       \
	  "mipsle:GOMIPS=softfloat" \
	  "mips:GOMIPS=softfloat"   \
	  ; do \
	  arch=$$(echo $$target | cut -d: -f1); \
	  envs=$$(echo $$target | cut -d: -f2-); \
	  echo ">>> building $(BINARY)-$$arch ($$envs)"; \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $$envs \
	    go build $(BUILD_FLAGS) -o $(DIST)/$(BINARY)-$$arch $(PKG) || exit 1; \
	done
	@ls -lh $(DIST)/

# Optional: UPX pack the build-all matrix in place. Skips if upx is missing.
# Release CI does the same — install.sh and self-update fetch the packed file
# under its plain name (no .upx suffix), so this matches the production layout.
upx: build-all
	@command -v upx >/dev/null || { echo "upx not installed, skipping"; exit 0; }
	@for f in $(DIST)/$(BINARY)-*; do \
	  upx --best --lzma $$f 2>&1 | tail -1 || echo "upx failed for $$f"; \
	done
	@ls -lh $(DIST)/

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf $(DIST)
