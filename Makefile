VERSION=$(shell cat VERSION.txt)
GIT_COMMIT=$(shell git rev-parse --short HEAD)
NODE_VERSION=22.17.0
NPM_VERSION=10.9.2

VERSION_FLAG=-X 'github.com/jingkaihe/kodelet/pkg/version.Version=$(VERSION)' -X 'github.com/jingkaihe/kodelet/pkg/version.GitCommit=$(GIT_COMMIT)'
.PHONY: build build-dev cross-build cross-build-docker run test lint golangci-lint code-generation install-linters install-air install-npm install-deps format docker-build docker-run e2e-test e2e-test-docker eslint eslint-fix frontend-test frontend-test-watch frontend-test-ui frontend-test-coverage release github-release push-tag dev-server

# Build the application
build: code-generation
	mkdir -p bin
	@echo "Building kodelet binary..."
	CGO_ENABLED=0 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet ./cmd/kodelet/

# Build the application without frontend assets (for development)
build-dev:
	mkdir -p bin
	@echo "Building kodelet binary (without frontend)..."
	CGO_ENABLED=0 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet ./cmd/kodelet/

chat: build
	./bin/kodelet chat

# Run development server with auto-reload
dev-server: install-air
	@echo "Starting development server with auto-reload..."
	./bin/air

code-generation:
	go generate ./pkg/webui

# Run tests
test:
	go test ./pkg/... ./cmd/...

# Install linting tools
install-linters:
	@echo "Installing golangci-lint to ./bin..."
	@mkdir -p bin
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b ./bin

# Install air for development auto-reload
install-air:
	@echo "Installing air for development auto-reload..."
	@mkdir -p bin
	@GOBIN=$(shell pwd)/bin go install github.com/air-verse/air@latest

# Install npm dependencies for frontend
install-npm:
	@echo "Installing npm dependencies for frontend..."
	@cd pkg/webui/frontend && npm install

# Install all tooling dependencies
install-deps: install-linters install-air install-npm
	@echo "All tooling dependencies installed successfully!"

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

# Run frontend tests
frontend-test:
	@echo "Running frontend tests..."
	@cd pkg/webui/frontend && npm run test:run

# Run frontend tests in watch mode
frontend-test-watch:
	@echo "Running frontend tests in watch mode..."
	@cd pkg/webui/frontend && npm run test:watch

# Run frontend tests with UI
frontend-test-ui:
	@echo "Running frontend tests with UI..."
	@cd pkg/webui/frontend && npm run test:ui

# Run frontend tests with coverage
frontend-test-coverage:
	@echo "Running frontend tests with coverage..."
	@cd pkg/webui/frontend && npm run test:coverage

# Run e2e tests in Docker
e2e-test-docker:
	docker build --build-arg NODE_VERSION="$(NODE_VERSION)" --build-arg NPM_VERSION="$(NPM_VERSION)" -f tests/acceptance/Dockerfile.e2e -t kodelet-e2e-tests .
	docker run --rm -e ANTHROPIC_API_KEY -e OPENAI_API_KEY kodelet-e2e-tests

# Cross-compile for multiple platforms
cross-build: code-generation
	mkdir -p bin
	@echo "Cross-compiling for multiple platforms..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet-linux-amd64 ./cmd/kodelet/
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet-linux-arm64 ./cmd/kodelet/
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet-darwin-amd64 ./cmd/kodelet/
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet-darwin-arm64 ./cmd/kodelet/
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet-windows-amd64.exe ./cmd/kodelet/

# Cross-compile for multiple platforms using Docker
cross-build-docker:
	mkdir -p bin
	@echo "Cross-compiling for multiple platforms using Docker..."
	docker build --build-arg VERSION="$(VERSION)" --build-arg GIT_COMMIT="$(GIT_COMMIT)" --build-arg NODE_VERSION="$(NODE_VERSION)" --build-arg NPM_VERSION="$(NPM_VERSION)" -f Dockerfile.cross-build -t kodelet-cross-build .
	@echo "Extracting binaries from Docker container..."
	docker run --rm -v $(shell pwd)/bin:/output kodelet-cross-build cp /bin/kodelet-linux-amd64 /bin/kodelet-linux-arm64 /bin/kodelet-darwin-amd64 /bin/kodelet-darwin-arm64 /bin/kodelet-windows-amd64.exe /output/
	@echo "Cross-build complete. Binaries available in ./bin/"
	@ls -la ./bin/kodelet-*

# Build Docker image
docker-build:
	docker build --build-arg VERSION="$$(cat VERSION.txt)" --build-arg GIT_COMMIT="$$(git rev-parse --short HEAD)" --build-arg NODE_VERSION="$(NODE_VERSION)" --build-arg NPM_VERSION="$(NPM_VERSION)" -t kodelet .

# Run with Docker
docker-run:
	docker run -e ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY} kodelet "$(query)"

release-note: build
	./bin/kodelet run -r release-note

# Display help information
help:
	@echo "Available targets:"
	@echo "  build        - Build the application with embedded web UI"
	@echo "  build-dev    - Build the application without web UI (faster for development)"
	@echo "  cross-build  - Cross-compile for multiple platforms (linux, macOS, Windows)"
	@echo "  cross-build-docker - Cross-compile using Docker (includes complete toolchain)"
	@echo "  code-generation - Generate frontend assets"
	@echo "  run          - Run in one-shot mode (use: make run query='your query')"
	@echo "  chat         - Run in interactive chat mode"
	@echo "  dev-server   - Run development server with auto-reload using air"
	@echo "  test         - Run tests"
	@echo "  e2e-test     - Run end-to-end acceptance tests"
	@echo "  e2e-test-docker - Run e2e tests in Docker"
	@echo "  lint         - Run all linters (go vet, golangci-lint)"
	@echo "  golangci-lint - Run golangci-lint"
	@echo "  install-linters - Install golangci-lint"
	@echo "  install-air  - Install air for development auto-reload"
	@echo "  install-npm  - Install npm dependencies for frontend"
	@echo "  install-deps - Install all tooling dependencies (golangci-lint, air, npm packages)"
	@echo "  format       - Format code"
	@echo "  eslint       - Run eslint on frontend code"
	@echo "  eslint-fix   - Run eslint with auto-fix on frontend code"
	@echo "  frontend-test - Run frontend tests"
	@echo "  frontend-test-watch - Run frontend tests in watch mode"
	@echo "  frontend-test-ui - Run frontend tests with UI"
	@echo "  frontend-test-coverage - Run frontend tests with coverage"
	@echo "  docker-build - Build Docker image (use NODE_VERSION/NPM_VERSION vars to override)"
	@echo "  docker-run   - Run with Docker (use: make docker-run query='your query')"
	@echo "  release      - Create GitHub release with cross-compiled binaries"
	@echo "  github-release - Create GitHub release with release notes from RELEASE.md (recommended)"
	@echo ""
	@echo "Node.js/npm versions can be overridden: make docker-build NODE_VERSION=20.0.0 NPM_VERSION=9.0.0"

release: cross-build
	gh release create v$(VERSION)
	gh release upload v$(VERSION) ./bin/kodelet-linux-amd64
	gh release upload v$(VERSION) ./bin/kodelet-linux-arm64
	gh release upload v$(VERSION) ./bin/kodelet-darwin-amd64
	gh release upload v$(VERSION) ./bin/kodelet-darwin-arm64
	gh release upload v$(VERSION) ./bin/kodelet-windows-amd64.exe

# Create GitHub release with release notes from RELEASE.md
github-release: cross-build-docker
	@echo "Creating GitHub release v$(VERSION)..."
	@./scripts/extract-release-notes.sh > /tmp/release-notes.md
	@gh release create v$(VERSION) \
		--title "v$(VERSION)" \
		--notes-file /tmp/release-notes.md \
		./bin/kodelet-linux-amd64 \
		./bin/kodelet-linux-arm64 \
		./bin/kodelet-darwin-amd64 \
		./bin/kodelet-darwin-arm64 \
		./bin/kodelet-windows-amd64.exe
	@rm -f /tmp/release-notes.md
	@echo "GitHub release v$(VERSION) created successfully!"

# Push version tag to trigger automated GitHub Actions release
push-tag:
	@echo "Creating and pushing tag v$(VERSION)..."
	@if git rev-parse "v$(VERSION)" >/dev/null 2>&1; then \
		echo "Tag v$(VERSION) already exists locally. Pushing to origin..."; \
	else \
		echo "Creating new tag v$(VERSION)..."; \
		git tag v$(VERSION); \
	fi
	@git push origin v$(VERSION)
	@echo "Tag v$(VERSION) pushed successfully!"
	@echo "GitHub Actions will automatically create a release with binaries and release notes."
