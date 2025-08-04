# Makefile

# Output binary name
PACKAGE_NAME ?= gospy
GOOS ?= linux
GOARCH ?= amd64
CGO_ENABLED ?= 0

GO_CMD = go
MAIN_PKG = ./cmd/gospy

# Default versioning variables
VERSION ?= dev

# Linker flags to inject version information
LDFLAGS = -X github.com/hakastein/gospy/internal/version.version=$(VERSION)

# Environment variables for Go build
GO_ENV_VARS = CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH)

# Build function
define build_app
	$(GO_ENV_VARS) $(GO_CMD) build -ldflags "$(LDFLAGS)" -o $(PACKAGE_NAME) $(MAIN_PKG)
endef

# Build target
build:
	$(call build_app)

test:
	go clean -testcache && go test ./cmd/... ./internal/...

bench:
	go test -race -bench=. -benchmem -run=^$ ./cmd/... ./internal/...

coverage:
	go test -v -coverprofile coverage.out ./cmd/... ./internal/...

coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.html && open coverage.html

# Clean target
clean:
	$(GO_CMD) clean
	rm -f $(PACKAGE_NAME)

# Download dependencies
download-deps:
	$(GO_CMD) mod download

# Display version information
version:
	@echo "Version: $(VERSION)"

# Dev target: build with dev versioning
dev:
	@TAG=$$(git describe --tags --exact-match 2>/dev/null) ; \
	if [ -n "$$TAG" ]; then \
		VERSION="$$TAG-dev"; \
	else \
		VERSION="dev"; \
	fi ; \
	echo "Building with VERSION=$$VERSION" ; \
	make build VERSION=$$VERSION

vet:
	go vet ./cmd/... ./internal/...

fmt:
	go fmt ./cmd/... ./internal/...

.PHONY: build clean download-deps version dev vet fmt