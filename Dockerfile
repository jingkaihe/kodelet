FROM golang:1.25-bookworm AS builder

# Add build arguments for version and git commit
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

# Add Node.js and npm version arguments with defaults
ARG NODE_VERSION=22.17.0
ARG NPM_VERSION=11.8.0

# Install curl and setup nvm for exact Node.js/npm versions
RUN apt-get update && apt-get install -y curl && \
    curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.39.7/install.sh | bash && \
    export NVM_DIR="/root/.nvm" && \
    [ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh" && \
    nvm install ${NODE_VERSION} && \
    nvm use ${NODE_VERSION} && \
    npm install -g npm@${NPM_VERSION} && \
    nvm alias default ${NODE_VERSION} && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
COPY VERSION.txt ./

RUN bash -c 'export NVM_DIR="/root/.nvm" && \
    [ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh" && \
    nvm use ${NODE_VERSION} && \
    go generate ./pkg/webui && \
    VERSION_PKG="github.com/jingkaihe/kodelet/pkg/version" && \
    CGO_ENABLED=0 go build -ldflags="-X '\''${VERSION_PKG}.Version=${VERSION}'\'' -X '\''${VERSION_PKG}.GitCommit=${GIT_COMMIT}'\'' -X '\''${VERSION_PKG}.BuildTime=${BUILD_TIME}'\''" -o /kodelet ./cmd/kodelet'

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates bash && rm -rf /var/lib/apt/lists/*

WORKDIR /

COPY --from=builder /kodelet /kodelet

ENTRYPOINT ["/kodelet"]

CMD ["--help"]
