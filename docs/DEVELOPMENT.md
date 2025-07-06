# Development Guide

This guide is for developers who want to contribute to Kodelet or build it from source.

For user documentation, see the [User Manual](MANUAL.md).

## Prerequisites

### Required
- Go 1.24 or higher
- Make (for using Makefile commands)
- Git

### Optional (for frontend development)
- Node.js 22.17.0
- npm 10.9.2

### Optional (for Docker-based builds)
- Docker (for containerized cross-compilation)

## Setting Up Development Environment

1. Clone the repository:
   ```bash
   git clone https://github.com/jingkaihe/kodelet.git
   cd kodelet
   ```

2. Install dependencies:
   ```bash
   go mod download
   ```

3. (Optional) Set up Node.js for frontend development:
   ```bash
   # Install Node.js 22.17.0 and npm 10.9.2
   # You can use nvm, Docker, or your preferred method

   # Install frontend dependencies
   cd pkg/webui/frontend
   npm install
   cd ../../..
   ```

4. Set up your API keys for testing:
   ```bash
   export ANTHROPIC_API_KEY="sk-ant-api..."
   # or
   export OPENAI_API_KEY="sk-..."
   ```

## Building from Source

### Local Build

```bash
make build
```

This creates the binary in `./bin/kodelet`.

### Cross-Platform Build

With the complex toolchain including Go, Node.js, and npm, Kodelet provides both local and Docker-based cross-compilation options:

#### Local Cross-Build
```bash
make cross-build
```

**Requirements:**
- Go 1.24+
- Node.js 22.17.0/npm 10.9.2 locally
- Runs `go generate ./pkg/webui` to build frontend assets
- Cross-compiles for all supported platforms

#### Docker Cross-Build (Recommended)
```bash
make cross-build-docker
```

**Benefits:**
- Uses containerized build environment with exact toolchain versions
- No local dependencies beyond Docker required
- Ensures consistent builds across different development machines
- Recommended for CI/CD and release builds

**How it works:**
1. Builds a Docker image with Go 1.24 + Node.js 22.17.0 + npm 10.9.2
2. Generates frontend assets inside the container
3. Cross-compiles for all platforms (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64)
4. Extracts binaries to local `./bin/` directory

Both approaches create binaries for multiple platforms in the `./bin/` directory.

### Docker Build

```bash
make docker-build
```

This builds a runtime Docker image using the regular `Dockerfile`.

#### Docker Files
- `Dockerfile` - Runtime Docker image for running Kodelet
- `Dockerfile.cross-build` - Specialized build environment for cross-compilation with complete toolchain (Go + Node.js + npm)

## Development Commands

### Testing

```bash
# Run all tests
make test

# Run tests with coverage
go test -v -cover ./pkg/... ./cmd/...

# Run tests for a specific package
go test -v ./pkg/llm/...

# Acceptance tests
make e2e-test-docker
```

### Code Quality

```bash
# Run Go linter
make lint

# Run frontend linter
make eslint

# Run frontend linter with auto-fix
make eslint-fix

# Format code
make format

# Check for formatting issues
gofmt -d .
```

### Frontend Development

The web UI is a React/TypeScript SPA built with Vite and embedded directly into the Go binary:

**Frontend Stack**: React 18, TypeScript, Tailwind CSS, DaisyUI, React Router, Vite

**Build Process**:
- `go generate ./pkg/webui` triggers `npm install && npm run build` in frontend directory
- Vite builds optimized assets to `pkg/webui/dist/` directory
- Go's `//go:embed dist/*` directive embeds all built assets into the binary at compile time

**Development**: Use `make build-dev` to skip frontend build for faster Go-only builds.

**Frontend Commands**:
```bash
# Run frontend tests
make frontend-test

# Run frontend tests in watch mode
make frontend-test-watch

# Run frontend tests with interactive UI
make frontend-test-ui

# Run frontend tests with coverage
make frontend-test-coverage
```

### Local Development

1. Build the development version:
   ```bash
   make build
   ```

2. Test your changes:
   ```bash
   ./bin/kodelet run "test query"
   ```

3. Run specific functionality:
   ```bash
   ./bin/kodelet chat
   ./bin/kodelet watch
   ```

## Project Structure

```
├── .github/             # GitHub configuration
│   └── workflows/       # GitHub Actions workflows (including release.yml)
├── cmd/kodelet/         # Application entry point (22+ command files)
├── docs/                # Documentation files
├── pkg/                 # Core packages
│   ├── conversations/   # Conversation storage and management
│   ├── llm/             # LLM client for AI interactions
│   │   ├── anthropic/   # Anthropic Claude API client
│   │   └── openai/      # OpenAI API client
│   ├── logger/          # Context-aware structured logging
│   ├── sysprompt/       # System prompt configuration
│   ├── telemetry/       # Telemetry components
│   ├── tools/           # Tool implementations (40+ tools)
│   ├── tui/             # Terminal UI components
│   ├── types/           # Common types
│   ├── utils/           # Utility functions
│   └── webui/           # Web UI server and React frontend
│       ├── frontend/    # React/TypeScript SPA with Vite build
│       └── dist/        # Built frontend assets (embedded in binary)
├── scripts/             # Build and release automation scripts
├── Dockerfile           # Docker configuration for runtime
├── Dockerfile.cross-build # Docker configuration for cross-compilation
├── Makefile             # Build automation
└── VERSION.txt          # Version information file
```

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/my-feature`
3. Make your changes
4. Run tests: `make test`
5. Run linter: `make lint`
6. Commit your changes: `git commit -am 'Add some feature'`
7. Push to the branch: `git push origin feature/my-feature`
8. Submit a pull request

## Release Process

### Manual Release
1. Update version in `VERSION.txt`
2. Update `RELEASE.md` with changelog for the new version at the top:
   ```markdown
   ## 0.0.XX.alpha (YYYY-MM-DD)

   ### Feature Category

   - **Feature Name**: Description of changes
   - **Another Feature**: More details about improvements
   ```
3. Create release using make:
   ```bash
   # Traditional release (requires local Node.js/npm)
   make release

   # GitHub release with RELEASE.md notes (recommended)
   make github-release
   ```

### Automated Release
The project includes a GitHub Actions workflow (`.github/workflows/release.yml`) that automatically:
- Triggers on version tags (`v*`)
- Uses Docker cross-build for consistent reproducible builds
- Extracts release notes from the top entry in `RELEASE.md`
- Uploads all platform binaries to GitHub releases

To trigger an automated release:
1. Update version in `VERSION.txt`
2. Add release notes to the top of `RELEASE.md` following the existing format:
   ```markdown
   ## 0.0.XX.alpha (YYYY-MM-DD)

   ### Your Feature Title

   Description of changes...
   ```
3. Create and push a version tag:
   ```bash
   git tag v$(cat VERSION.txt)
   git push origin v$(cat VERSION.txt)
   ```

The release notes will be automatically extracted from the top entry in `RELEASE.md` and used in the GitHub release.

## Available Make Commands

For a complete list of available commands:

```bash
make help
```

Common commands:
- `make build` - Build the application
- `make build-dev` - Build without frontend assets (faster for Go-only development)
- `make test` - Run tests
- `make lint` - Run linter
- `make format` - Format code
- `make docker-build` - Build Docker image
- `make cross-build` - Build for multiple platforms (requires local Node.js/npm)
- `make cross-build-docker` - Build for multiple platforms using Docker (recommended)
- `make release` - Create a release
- `make github-release` - Create GitHub release with RELEASE.md notes (recommended)
