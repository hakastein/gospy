# Define the name of the output binary
BINARY_NAME = gospy

# Define the Go command and flags
GO_CMD = go
GO_BUILD = $(GO_CMD) build
GO_CLEAN = $(GO_CMD) clean

# Define the main package for the build
MAIN_PKG = ./cmd/gospy

# Set environment variables for static build
# CGO_ENABLED=0 disables CGO (C bindings), ensuring a statically linked binary
# GOOS and GOARCH can be set to target specific OS and architecture
GO_ENV_VARS = CGO_ENABLED=0 GOOS=linux GOARCH=amd64

# Default target: build the binary
build:
	$(GO_ENV_VARS) $(GO_BUILD) -o $(BINARY_NAME) $(MAIN_PKG)

# Clean up binaries and build artifacts
clean:
	$(GO_CLEAN)
	rm -f $(BINARY_NAME)

# Run the binary
run: build
	./$(BINARY_NAME)

# Define a PHONY target for each command to avoid conflicts with files named the same
.PHONY: build test lint clean run
