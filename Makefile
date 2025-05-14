VERSION=$(shell cat VERSION.txt)
GIT_COMMIT=$(shell git rev-parse --short HEAD)

VERSION_FLAG=-X 'github.com/jingkaihe/kodelet/pkg/version.Version=$(VERSION)' -X 'github.com/jingkaihe/kodelet/pkg/version.GitCommit=$(GIT_COMMIT)'
.PHONY: build cross-build run test lint format docker-build docker-run

# Build the application
build:
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="$(VERSION_FLAG)" -o ./bin/kodelet ./cmd/kodelet/

chat: build
	./bin/kodelet chat

# Run tests
test:
	go test ./...

# Run linter
lint:
	go vet ./...

# Format code
format:
	go fmt ./...

# Cross-compile for multiple platforms
cross-build:
	mkdir -p bin
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
	./bin/kodelet run "diff against the main branch and write the release note in RELEASE.md"

# Display help information
help:
	@echo "Available targets:"
	@echo "  build        - Build the application"
	@echo "  cross-build  - Cross-compile for multiple platforms (linux, macOS, Windows)"
	@echo "  run          - Run in one-shot mode (use: make run query='your query')"
	@echo "  chat         - Run in interactive chat mode"
	@echo "  test         - Run tests"
	@echo "  lint         - Run linter"
	@echo "  format       - Format code"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run   - Run with Docker (use: make docker-run query='your query')"

release: cross-build
	gh release create v$(VERSION)
	gh release upload v$(VERSION) ./bin/kodelet-linux-amd64
	gh release upload v$(VERSION) ./bin/kodelet-linux-arm64
	gh release upload v$(VERSION) ./bin/kodelet-darwin-amd64
	gh release upload v$(VERSION) ./bin/kodelet-darwin-arm64
	gh release upload v$(VERSION) ./bin/kodelet-windows-amd64.exe
