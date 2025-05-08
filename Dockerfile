FROM golang:1.24-alpine AS builder

# Add build arguments for version and git commit
ARG VERSION=dev
ARG GIT_COMMIT=unknown

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
COPY VERSION.txt ./

RUN CGO_ENABLED=0 go build -ldflags="-X 'github.com/jingkaihe/kodelet/pkg/version.Version=${VERSION}' -X 'github.com/jingkaihe/kodelet/pkg/version.GitCommit=${GIT_COMMIT}'" -o /kodelet ./cmd/kodelet

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates bash && rm -rf /var/lib/apt/lists/*

WORKDIR /

COPY --from=builder /kodelet /kodelet

ENTRYPOINT ["/kodelet"]

CMD ["--help"]
