# Developer commands for fly. Run "make help" for the list.
# Keep this file compatible with GNU Make 3.81, the version on macOS.

.PHONY: build test vet lint vuln fmt fmt-check check release dev-version dev-release clean help

BINARY := fly
PKG := github.com/flywp/server-cli

# VERSION defaults to the git version. Override it for a release:
#   make release VERSION=v0.2.0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%d)

LDFLAGS := -X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.CommitHash=$(COMMIT) \
	-X $(PKG)/internal/version.BuildDate=$(BUILD_DATE)

# Dev pre-releases are named <next minor version>-dev.<short commit>, for
# example v0.2.0-dev.1a2b3c4. DEV_BASE defaults to the minor version after the
# latest release tag; override it, for example make dev-release DEV_BASE=v0.1.2.
DEV_BASE ?= $(shell latest=$$(git describe --tags --abbrev=0 --exclude '*-*' 2>/dev/null || echo v0.0.0); \
	echo "$$latest" | awk -F. '{ sub(/^v/, "", $$1); printf "v%d.%d.0", $$1, $$2 + 1 }')
DEV_VERSION = $(DEV_BASE)-dev.$(shell git rev-parse --short=7 HEAD)

# Release platforms. Installed CLIs download fly-<os>-<arch>.tar.gz and look
# for the binary fly-<os>-<arch> in it, so do not change these names.
RELEASE_PLATFORMS := linux/amd64 linux/arm64

# "go run pkg@version" builds each tool with the Go version of this module,
# so a tool cannot be older than go.mod.
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@v1.8.0

build: ## Build bin/fly for this platform
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

test: ## Run the tests with the race detector
	go test ./... -race -count=1

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run

vuln: ## Scan for known vulnerabilities
	$(GOVULNCHECK) ./...

fmt: ## Format the code
	gofmt -w .

fmt-check: ## Fail if the code is not formatted
	@files=$$(gofmt -l .); if [ -n "$$files" ]; then echo "Run make fmt for:"; echo "$$files"; exit 1; fi

check: fmt-check vet lint test vuln ## Run all checks (the gate before each merge)

release: ## Build the static release archives and checksums.txt in build/
	rm -rf build
	mkdir -p build
	@for platform in $(RELEASE_PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; out=$(BINARY)-$$os-$$arch; \
		echo "Building $$out ($(VERSION))"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o build/$$out . || exit 1; \
		COPYFILE_DISABLE=1 tar -czf build/$$out.tar.gz -C build $$out || exit 1; \
	done
	cd build && (command -v sha256sum >/dev/null 2>&1 && sha256sum *.tar.gz || shasum -a 256 *.tar.gz) > checksums.txt

dev-version: ## Print the dev pre-release version of HEAD
	@echo $(DEV_VERSION)

# The Release workflow publishes the tag as a pre-release. A tag on a commit
# whose release workflow does not know pre-releases would become the latest
# release and reach every installed CLI, so dev-release refuses such commits.
dev-release: ## Tag HEAD as a dev pre-release and push the tag (CI publishes it)
	@set -e; \
	if [ -n "$$(git status --porcelain --untracked-files=no)" ]; then \
		echo "Commit or stash your changes first: the pre-release is built from the commit."; exit 1; fi; \
	if [ -z "$$(git branch -r --contains HEAD)" ]; then \
		echo "Push this commit to a branch first."; exit 1; fi; \
	if ! git show HEAD:.github/workflows/build.yml | grep -q -- '--prerelease'; then \
		echo "The release workflow at this commit does not publish pre-releases. Do not tag it."; exit 1; fi; \
	git tag -a "$(DEV_VERSION)" -m "Dev pre-release $(DEV_VERSION)"; \
	git push origin "$(DEV_VERSION)"; \
	echo "Pushed $(DEV_VERSION). The Release workflow publishes it as a pre-release:"; \
	echo "  https://github.com/flywp/server-cli/releases/tag/$(DEV_VERSION)"

clean: ## Remove bin/ and build/
	rm -rf bin/ build/

help: ## Show the targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'
