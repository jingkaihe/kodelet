VERSION=$(shell cat VERSION.txt)
GIT_COMMIT=$(shell git rev-parse --short HEAD)

VERSION_FLAG=-X 'github.com/jingkaihe/kodelet/pkg/version.Version=$(VERSION)' -X 'github.com/jingkaihe/kodelet/pkg/version.GitCommit=$(GIT_COMMIT)'
.PHONY: build build-dev cross-build run test lint golangci-lint install-linters format docker-build docker-run e2e-test e2e-test-docker eslint eslint-fix

# Build the application
build:
	mkdir -p bin
	@echo "Building frontend assets..."
	go generate ./pkg/webui
	@echo "Building kodelet binary..."
	CGO_ENABLED=0 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet ./cmd/kodelet/

# Build the application without frontend assets (for development)
build-dev:
	mkdir -p bin
	@echo "Building kodelet binary (without frontend)..."
	CGO_ENABLED=0 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet ./cmd/kodelet/

chat: build
	./bin/kodelet chat

# Run tests
test:
	go test ./pkg/... ./cmd/...

# Install linting tools
install-linters:
	@echo "Installing golangci-lint to ./bin..."
	@mkdir -p bin
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b ./bin

# Run all linters
lint:
	go vet ./...
	@if [ -f ./bin/golangci-lint ]; then \
		echo "Running golangci-lint..."; \
		./bin/golangci-lint run; \
	else \
		echo "golangci-lint not found. Run 'make install-linters' to install it."; \
	fi

# Run golangci-lint
golangci-lint:
	@if [ -f ./bin/golangci-lint ]; then \
		./bin/golangci-lint run; \
	else \
		echo "golangci-lint not found. Run 'make install-linters' to install it."; \
		exit 1; \
	fi

# Format code
format:
	go fmt ./...

# Run eslint on frontend code
eslint:
	@echo "Running eslint on frontend code..."
	@cd pkg/webui/frontend && npm run lint

# Run eslint with auto-fix on frontend code
eslint-fix:
	@echo "Running eslint with auto-fix on frontend code..."
	@cd pkg/webui/frontend && npm run lint:fix

# Run e2e tests in Docker
e2e-test-docker:
	docker build -f tests/acceptance/Dockerfile.e2e -t kodelet-e2e-tests .
	docker run --rm -e ANTHROPIC_API_KEY -e OPENAI_API_KEY kodelet-e2e-tests

# Cross-compile for multiple platforms
cross-build:
	mkdir -p bin
	@echo "Building frontend assets..."
	go generate ./pkg/webui
	@echo "Cross-compiling for multiple platforms..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet-linux-amd64 ./cmd/kodelet/
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet-linux-arm64 ./cmd/kodelet/
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet-darwin-amd64 ./cmd/kodelet/
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet-darwin-arm64 ./cmd/kodelet/
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet-windows-amd64.exe ./cmd/kodelet/

# Build Docker image
docker-build:
	docker build --build-arg VERSION="$$(cat VERSION.txt)" --build-arg GIT_COMMIT="$$(git rev-parse --short HEAD)" -t kodelet .

# Run with Docker
docker-run:
	docker run -e ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY} kodelet "$(query)"

release-note: build
	./bin/kodelet run "diff against the previous release and write the release note in RELEASE.md. Keep the release note short, concise and to the point."

# Display help information
help:
	@echo "Available targets:"
	@echo "  build        - Build the application with embedded web UI"
	@echo "  build-dev    - Build the application without web UI (faster for development)"
	@echo "  cross-build  - Cross-compile for multiple platforms (linux, macOS, Windows)"
	@echo "  run          - Run in one-shot mode (use: make run query='your query')"
	@echo "  chat         - Run in interactive chat mode"
	@echo "  test         - Run tests"
	@echo "  e2e-test     - Run end-to-end acceptance tests"
	@echo "  e2e-test-docker - Run e2e tests in Docker"
	@echo "  lint         - Run all linters (go vet, golangci-lint)"
	@echo "  golangci-lint - Run golangci-lint"
	@echo "  install-linters - Install golangci-lint"
	@echo "  format       - Format code"
	@echo "  eslint       - Run eslint on frontend code"
	@echo "  eslint-fix   - Run eslint with auto-fix on frontend code"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run   - Run with Docker (use: make docker-run query='your query')"

release: cross-build
	gh release create v$(VERSION)
	gh release upload v$(VERSION) ./bin/kodelet-linux-amd64
	gh release upload v$(VERSION) ./bin/kodelet-linux-arm64
	gh release upload v$(VERSION) ./bin/kodelet-darwin-amd64
	gh release upload v$(VERSION) ./bin/kodelet-darwin-arm64
	gh release upload v$(VERSION) ./bin/kodelet-windows-amd64.exe
