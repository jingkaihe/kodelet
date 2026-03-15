# Development Guide

This guide is for developers who want to contribute to Kodelet or build it from source.

For user documentation, see the [User Manual](MANUAL.md).

## Prerequisites

### Required
- [mise](https://mise.jdx.dev/) (for tool management and task automation)
- Git

### Automatically Managed by mise
- Go 1.24.2 (exact version automatically installed)
- Node.js 22.17.0 (exact version automatically installed)
- npm 11.8.0 (exact version automatically installed)
- golangci-lint (latest version)
- air (for development auto-reload)
- gh CLI (for GitHub releases)
- goreleaser (for release packaging)

### Optional (for Docker-based builds)
- Docker (for containerized cross-compilation)

## Setting Up Development Environment

1. Install mise (if not already installed):
   ```bash
   curl https://mise.jdx.dev/install.sh | sh
   ```

2. Clone the repository:
   ```bash
   git clone https://github.com/jingkaihe/kodelet.git
   cd kodelet
   ```

3. Install all tools and dependencies:
   ```bash
   # Install all required tools (Go, Node.js, npm, etc.) and dependencies
   mise install

   # Install Go modules and npm dependencies
   mise run install
   ```

4. Set up your API keys for testing:
   ```bash
   export ANTHROPIC_API_KEY="sk-ant-api..."
   # or
   export OPENAI_API_KEY="sk-..."
   ```

5. (Optional) Set up MCP tools configuration:
   ```bash
   # Copy the sample configuration to enable MCP tools
   cp ./kodelet-config.sample.yaml ./kodelet-config.yaml
   ```

   Adjust the configuration in `kodelet-config.yaml` based on your requirements and usage. In most cases you don't want to enable all the tools as it will bloat the context window

That's it! mise automatically manages all tool versions and ensures everyone on the team uses the same versions of Go, Node.js, npm, and other development tools.

## Building from Source

### Local Build

```bash
mise run build
```

This creates the binary in `./bin/kodelet`.

### Cross-Platform Build

Kodelet provides both direct cross-build tasks and a GoReleaser-based packaging path:

#### Local Cross-Build
```bash
mise run cross-build
```

This produces raw binaries in `./bin/` for the supported platforms.

#### GoReleaser Snapshot Build
```bash
mise run release
```

This runs GoReleaser in snapshot mode and emits release-style artifacts into `./dist/`, including:
- raw platform binaries
- `checksums.txt`
- Linux `.deb` and `.rpm` packages

The Linux packages also bundle `rg` and `fd` into `/usr/libexec/kodelet/` so packaged installs do not need to download those search binaries into the user's home directory.

### Docker Build

```bash
mise run docker-build
```

This builds a runtime Docker image using the regular `Dockerfile`.

#### Docker Files
- `Dockerfile` - Runtime Docker image for running Kodelet

## Development Commands

### Testing

```bash
# Run all tests
mise run test

# Run tests with coverage
go test -v -cover ./pkg/... ./cmd/...

# Run tests for a specific package
go test -v ./pkg/llm/...

# Acceptance tests
mise run e2e-test-docker
```

### Code Quality

```bash
# Run Go linter
mise run lint

# Run frontend linter
mise run eslint

# Run frontend linter with auto-fix
mise run eslint-fix

# Format code
mise run format

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

**Development**: Use `mise run build-dev` to skip frontend build for faster Go-only builds.

**Frontend Commands**:
```bash
# Run frontend tests
mise run frontend-test

# Run frontend tests in watch mode
mise run frontend-test-watch

# Run frontend tests with interactive UI
mise run frontend-test-ui

# Run frontend tests with coverage
mise run frontend-test-coverage
```

### Local Development

1. Build the development version:
   ```bash
   mise run build
   ```

2. Test your changes:
   ```bash
   ./bin/kodelet run "test query"
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
├── mise.toml            # Tool management and task automation
└── VERSION.txt          # Version information file
```

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/my-feature`
3. Make your changes
4. Run tests: `mise run test`
5. Run linter: `mise run lint`
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
3. Build release artifacts using mise:
   ```bash
   # Build snapshot artifacts locally with GoReleaser
   mise run release

   # Build a full tagged release (used in CI)
   mise run github-release
   ```

### Automated Release
The project includes a GitHub Actions workflow (`.github/workflows/release.yml`) that automatically:
- Triggers on version tags (`v*`)
- Runs GoReleaser for release packaging
- Extracts release notes from the top entry in `RELEASE.md`
- Uploads all platform binaries, checksums, and Linux packages to GitHub releases

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

## Available mise Tasks

For a complete list of available tasks:

```bash
mise run help
# or view all tasks
mise tasks
```

Common tasks:
- `mise run build` - Build the application
- `mise run build-dev` - Build without frontend assets (faster for Go-only development)
- `mise run test` - Run tests
- `mise run lint` - Run linter
- `mise run format` - Format code
- `mise run docker-build` - Build Docker image
- `mise run cross-build` - Build for multiple platforms
- `mise run release-snapshot` - Build GoReleaser snapshot artifacts into `dist/`
- `mise run release` - Create a release
- `mise run github-release` - Create GitHub release with RELEASE.md notes (recommended)

## Tool Management

mise automatically manages all development tools:

```bash
# Install all required tools
mise install

# Check current tool versions
mise list

# Show which tools would be installed
mise list --outdated
```

All team members automatically get the same versions of Go, Node.js, npm, and other tools as defined in `mise.toml`.
