## dbflow-validator — Cross-compile Makefile
##
## Targets:
##   make build       — Build for the current platform (output: dist/<binary>)
##   make build-all   — Cross-compile for linux-amd64, darwin-amd64, darwin-arm64, windows-amd64
##   make clean       — Remove the dist/ directory
##
## The binary embeds the vendored Maven repository (~110 MB).
## Set VERSION to override the injected build version (default: git describe or "dev").

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BINARY   := dbflow-validator
PKG      := github.com/dbflow-validator/dbflow-validator/cmd/dbflow-validator
LDFLAGS  := -X main.buildVersion=$(VERSION) -s -w
DIST     := dist

.PHONY: build build-all clean

## build — Build for the current host platform.
build:
	@mkdir -p $(DIST)
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) ./cmd/dbflow-validator/
	@echo "Built $(DIST)/$(BINARY) (version=$(VERSION))"

## build-all — Build the native binary AND cross-compile for all supported platforms.
## Includes `build` so the suffixless dist/dbflow-validator never goes stale.
build-all: build build-linux-amd64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64

build-linux-amd64:
	@mkdir -p $(DIST)
	GOOS=linux  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" \
		-o $(DIST)/$(BINARY)-linux-amd64 ./cmd/dbflow-validator/
	@echo "Built $(DIST)/$(BINARY)-linux-amd64"

build-darwin-amd64:
	@mkdir -p $(DIST)
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" \
		-o $(DIST)/$(BINARY)-darwin-amd64 ./cmd/dbflow-validator/
	@echo "Built $(DIST)/$(BINARY)-darwin-amd64"

build-darwin-arm64:
	@mkdir -p $(DIST)
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" \
		-o $(DIST)/$(BINARY)-darwin-arm64 ./cmd/dbflow-validator/
	@echo "Built $(DIST)/$(BINARY)-darwin-arm64"

build-windows-amd64:
	@mkdir -p $(DIST)
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" \
		-o $(DIST)/$(BINARY)-windows-amd64.exe ./cmd/dbflow-validator/
	@echo "Built $(DIST)/$(BINARY)-windows-amd64.exe"

## clean — Remove compiled binaries.
clean:
	rm -rf $(DIST)
	@echo "Cleaned $(DIST)/"
