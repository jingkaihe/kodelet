.PHONY: build run test lint format docker-build docker-run

# Build the application
build:
	mkdir -p bin
	go build -ldflags="-X 'github.com/jingkaihe/kodelet/pkg/version.Version=$$(cat VERSION.txt)' -X 'github.com/jingkaihe/kodelet/pkg/version.GitCommit=$$(git rev-parse --short HEAD)'" -o ./bin/kodelet ./cmd/kodelet/

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

# Build Docker image
docker-build:
	docker build --build-arg VERSION="$$(cat VERSION.txt)" --build-arg GIT_COMMIT="$$(git rev-parse --short HEAD)" -t kodelet .

# Run with Docker
docker-run:
	docker run -e ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY} kodelet "$(query)"

# Display help information
help:
	@echo "Available targets:"
	@echo "  build        - Build the application"
	@echo "  run          - Run in one-shot mode (use: make run query='your query')"
	@echo "  chat         - Run in interactive chat mode"
	@echo "  test         - Run tests"
	@echo "  lint         - Run linter"
	@echo "  format       - Format code"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run   - Run with Docker (use: make docker-run query='your query')"
