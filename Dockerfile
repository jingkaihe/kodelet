FROM golang:1.24-bookworm AS builder

# Add build arguments for version and git commit
ARG VERSION=dev
ARG GIT_COMMIT=unknown

# Add Node.js and npm version arguments with defaults
ARG NODE_VERSION=22.17.0
ARG NPM_VERSION=10.9.2

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
    CGO_ENABLED=0 go build -ldflags="-X '\''github.com/jingkaihe/kodelet/pkg/version.Version=${VERSION}'\'' -X '\''github.com/jingkaihe/kodelet/pkg/version.GitCommit=${GIT_COMMIT}'\''" -o /kodelet ./cmd/kodelet'

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates bash && rm -rf /var/lib/apt/lists/*

WORKDIR /

COPY --from=builder /kodelet /kodelet

ENTRYPOINT ["/kodelet"]

CMD ["--help"]
