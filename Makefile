## dbflow-validator — Cross-compile Makefile
##
## Targets:
##   make vendor      — Fetch the plugin jar from GitHub Packages (no-op if already present)
##   make build       — Build for the current platform (output: dist/<binary>)
##   make build-all   — Cross-compile for linux-amd64, darwin-amd64, darwin-arm64, windows-amd64
##   make clean       — Remove the dist/ directory
##
## The binary embeds the vendored Maven repository (~110 MB).
## Set VERSION to override the injected build version (default: git describe or "dev").
##
## GH_TOKEN: a GitHub personal access token with read:packages scope.
##   Required when the plugin jar is not already present locally (fresh clone).
##   Not needed when the jar already exists (make vendor is a no-op in that case).
##   Usage: GH_TOKEN=ghp_... make vendor
##          GH_TOKEN=ghp_... make build
##          GH_TOKEN=ghp_... make build-all

VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
# POSTGRES_IMAGE is the ephemeral Postgres image baked in as the no-flag default.
# Default ghcr.io/juanpa-reyest/dbflow-postgres-partman:17.7 (with pg_partman). Override to ship an area-specific build whose
# `dbflow-validator` (no flags) uses an image with extra extensions, e.g.:
#   POSTGRES_IMAGE=dbflow-postgres-partman:17.7 make build
# The --postgres-image flag and DBFLOW_POSTGRES_IMAGE env still override at runtime.
POSTGRES_IMAGE ?= ghcr.io/juanpa-reyest/dbflow-postgres-partman:17.7
BINARY       := dbflow-validator
PKG          := github.com/dbflow-validator/dbflow-validator/cmd/dbflow-validator
LDFLAGS      := -X main.buildVersion=$(VERSION) -X main.buildPostgresImage=$(POSTGRES_IMAGE) -s -w
# EXT_POSTGRES_IMAGE is baked into the `-ext` edition binaries — the variant for
# areas whose DB uses extra extensions (e.g. pg_partman). It MUST be a public,
# pullable image (GHCR) so a downloaded `-ext` binary fetches it with no login
# and no flags. The stock edition (default POSTGRES_IMAGE) stays extension-free.
EXT_POSTGRES_IMAGE ?= ghcr.io/juanpa-reyest/dbflow-postgres-partman:17.7
EXT_LDFLAGS  := -X main.buildVersion=$(VERSION) -X main.buildPostgresImage=$(EXT_POSTGRES_IMAGE) -s -w
DIST         := dist

# PLUGIN_VERSION is the single source of truth for the embedded plugin version.
# Bump it here (or override per-invocation: PLUGIN_VERSION=0.0.2 make build) and
# every path/URL below follows automatically.
PLUGIN_VERSION ?= 0.0.1
PLUGIN_DIR   := internal/embedrepo/mvn-vendor/repository/com/gs/ftt/coe-ds/relational-db-release-manager-plugin/$(PLUGIN_VERSION)
PLUGIN_JAR   := $(PLUGIN_DIR)/relational-db-release-manager-plugin-$(PLUGIN_VERSION).jar
PACKAGES_URL := https://maven.pkg.github.com/Juanpa-Reyest/dbflow-validator/com/gs/ftt/coe-ds/relational-db-release-manager-plugin/$(PLUGIN_VERSION)/relational-db-release-manager-plugin-$(PLUGIN_VERSION).jar

# VALIDATOR_VERSION is the single source of truth for the embedded SQL-rules
# validator jar. Bump it here (or override: VALIDATOR_VERSION=0.0.2 make build).
# The jar is fetched into the version-less path that embed.go //go:embed expects.
VALIDATOR_VERSION ?= 0.0.1
VALIDATOR_JAR     := internal/embedvalidator/jar/library-script-validator-postgresql.jar
VALIDATOR_URL     := https://maven.pkg.github.com/Juanpa-Reyest/dbflow-validator/com/gs/ftt/coe-ds/library-script-validator-postgresql-java/$(VALIDATOR_VERSION)/library-script-validator-postgresql-java-$(VALIDATOR_VERSION).jar

.PHONY: build build-all build-all-ext clean vendor

## vendor — Fetch the embedded jars (Maven plugin + SQL-rules validator) from
## GitHub Packages if not already present. Both are required by //go:embed at build
## time. Requires GH_TOKEN (read:packages) when a jar is missing; no-op when present.
vendor:
	@if [ -f "$(PLUGIN_JAR)" ]; then \
		echo "vendor: plugin jar already present — skipping download"; \
	else \
		echo "vendor: downloading plugin jar from GitHub Packages..."; \
		mkdir -p "$(dir $(PLUGIN_JAR))"; \
		curl -fsSL \
			-H "Authorization: Bearer $$GH_TOKEN" \
			-o "$(PLUGIN_JAR)" \
			"$(PACKAGES_URL)"; \
		echo "vendor: downloaded $(PLUGIN_JAR)"; \
	fi
	@if [ -f "$(VALIDATOR_JAR)" ]; then \
		echo "vendor: validator jar already present — skipping download"; \
	else \
		echo "vendor: downloading validator jar from GitHub Packages..."; \
		mkdir -p "$(dir $(VALIDATOR_JAR))"; \
		curl -fsSL \
			-H "Authorization: Bearer $$GH_TOKEN" \
			-o "$(VALIDATOR_JAR)" \
			"$(VALIDATOR_URL)"; \
		echo "vendor: downloaded $(VALIDATOR_JAR)"; \
	fi

## build — Build for the current host platform.
build: vendor
	@mkdir -p $(DIST)
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) ./cmd/dbflow-validator/
	@echo "Built $(DIST)/$(BINARY) (version=$(VERSION))"

## build-all — Build the native binary AND cross-compile for all supported platforms.
## Includes `build` so the suffixless dist/dbflow-validator never goes stale.
build-all: vendor build build-linux-amd64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64 build-all-ext

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

## build-all-ext — Cross-compile the `-ext` edition (extensions image baked in)
## for all supported platforms. A downloaded `-ext` binary pulls
## $(EXT_POSTGRES_IMAGE) from GHCR automatically — no flags, no login.
build-all-ext: vendor build-ext-linux-amd64 build-ext-darwin-amd64 build-ext-darwin-arm64 build-ext-windows-amd64

build-ext-linux-amd64:
	@mkdir -p $(DIST)
	GOOS=linux  GOARCH=amd64 go build -ldflags "$(EXT_LDFLAGS)" \
		-o $(DIST)/$(BINARY)-ext-linux-amd64 ./cmd/dbflow-validator/
	@echo "Built $(DIST)/$(BINARY)-ext-linux-amd64 (image=$(EXT_POSTGRES_IMAGE))"

build-ext-darwin-amd64:
	@mkdir -p $(DIST)
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(EXT_LDFLAGS)" \
		-o $(DIST)/$(BINARY)-ext-darwin-amd64 ./cmd/dbflow-validator/
	@echo "Built $(DIST)/$(BINARY)-ext-darwin-amd64 (image=$(EXT_POSTGRES_IMAGE))"

build-ext-darwin-arm64:
	@mkdir -p $(DIST)
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(EXT_LDFLAGS)" \
		-o $(DIST)/$(BINARY)-ext-darwin-arm64 ./cmd/dbflow-validator/
	@echo "Built $(DIST)/$(BINARY)-ext-darwin-arm64 (image=$(EXT_POSTGRES_IMAGE))"

build-ext-windows-amd64:
	@mkdir -p $(DIST)
	GOOS=windows GOARCH=amd64 go build -ldflags "$(EXT_LDFLAGS)" \
		-o $(DIST)/$(BINARY)-ext-windows-amd64.exe ./cmd/dbflow-validator/
	@echo "Built $(DIST)/$(BINARY)-ext-windows-amd64.exe (image=$(EXT_POSTGRES_IMAGE))"

## clean — Remove compiled binaries.
clean:
	rm -rf $(DIST)
	@echo "Cleaned $(DIST)/"
