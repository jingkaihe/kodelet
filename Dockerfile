FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY pkg/ ./pkg/

RUN CGO_ENABLED=0 go build -o /kodelet ./cmd/kodelet

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates bash && rm -rf /var/lib/apt/lists/*

WORKDIR /

COPY --from=builder /kodelet /kodelet

ENTRYPOINT ["/kodelet"]

CMD ["--help"]
