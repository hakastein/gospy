# Define the name of the output binary
PACKAGE_NAME ?= gospy
GOOS ?= linux
GOARCH ?= amd64
CGO_ENABLED ?= 0

GO_CMD = go
GO_BUILD = $(GO_CMD) build
GO_CLEAN = $(GO_CMD) clean
MAIN_PKG = ./cmd/gospy

GO_ENV_VARS = CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH)

build:
	$(GO_ENV_VARS) $(GO_BUILD) -o $(PACKAGE_NAME) $(MAIN_PKG)

clean:
	$(GO_CLEAN)
	rm -f $(PACKAGE_NAME)

.PHONY: build test lint clean run
