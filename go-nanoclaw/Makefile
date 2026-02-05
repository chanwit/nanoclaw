# NanoClaw Go - Makefile

.PHONY: all build clean test run auth deps lint fmt

# Binary names
BINARY_NAME=nanoclaw
AUTH_BINARY=nanoclaw-auth

# Build directory
BUILD_DIR=./bin

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOCLEAN=$(GOCMD) clean
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
GOLINT=golangci-lint

# Build flags
LDFLAGS=-ldflags "-s -w"
CGO_ENABLED=CGO_ENABLED=1

all: deps build

# Download dependencies
deps:
	$(GOMOD) download
	$(GOMOD) tidy

# Build all binaries
build: build-main build-auth

build-main:
	@mkdir -p $(BUILD_DIR)
	$(CGO_ENABLED) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/nanoclaw

build-auth:
	@mkdir -p $(BUILD_DIR)
	$(CGO_ENABLED) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(AUTH_BINARY) ./cmd/auth

# Run the main application
run: build-main
	$(BUILD_DIR)/$(BINARY_NAME)

# Run the auth command
auth: build-auth
	$(BUILD_DIR)/$(AUTH_BINARY)

# Clean build artifacts
clean:
	$(GOCLEAN)
	rm -rf $(BUILD_DIR)

# Run tests
test:
	$(GOTEST) -v ./...

# Run tests with coverage
test-coverage:
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

# Format code
fmt:
	$(GOFMT) -s -w .

# Lint code
lint:
	$(GOLINT) run ./...

# Install golangci-lint if not present
install-lint:
	@which golangci-lint > /dev/null || curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin

# Development: build and run with hot reload (requires air)
dev:
	@which air > /dev/null || go install github.com/air-verse/air@latest
	air

# Install development tools
install-tools: install-lint
	go install github.com/air-verse/air@latest

# Build for multiple platforms
build-all:
	@mkdir -p $(BUILD_DIR)
	# Linux AMD64
	GOOS=linux GOARCH=amd64 $(CGO_ENABLED) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/nanoclaw
	GOOS=linux GOARCH=amd64 $(CGO_ENABLED) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(AUTH_BINARY)-linux-amd64 ./cmd/auth
	# Linux ARM64
	GOOS=linux GOARCH=arm64 $(CGO_ENABLED) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/nanoclaw
	GOOS=linux GOARCH=arm64 $(CGO_ENABLED) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(AUTH_BINARY)-linux-arm64 ./cmd/auth
	# macOS AMD64
	GOOS=darwin GOARCH=amd64 $(CGO_ENABLED) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/nanoclaw
	GOOS=darwin GOARCH=amd64 $(CGO_ENABLED) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(AUTH_BINARY)-darwin-amd64 ./cmd/auth
	# macOS ARM64
	GOOS=darwin GOARCH=arm64 $(CGO_ENABLED) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/nanoclaw
	GOOS=darwin GOARCH=arm64 $(CGO_ENABLED) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(AUTH_BINARY)-darwin-arm64 ./cmd/auth

# Help
help:
	@echo "NanoClaw Go - Available targets:"
	@echo ""
	@echo "  make build      - Build all binaries"
	@echo "  make run        - Build and run the main application"
	@echo "  make auth       - Build and run the auth command"
	@echo "  make test       - Run tests"
	@echo "  make clean      - Clean build artifacts"
	@echo "  make deps       - Download dependencies"
	@echo "  make fmt        - Format code"
	@echo "  make lint       - Lint code"
	@echo "  make dev        - Run with hot reload (requires air)"
	@echo "  make build-all  - Build for all platforms"
	@echo ""
