# syntax=docker/dockerfile:1.7

ARG NODE_IMAGE=node:24.16.0-bookworm-slim
ARG GO_IMAGE=golang:1.26.5-bookworm
ARG RUNTIME_IMAGE=gcr.io/distroless/cc-debian13:nonroot

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS frontend

WORKDIR /src/pkg/webui/frontend

COPY pkg/webui/frontend/package.json pkg/webui/frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci

COPY pkg/webui/frontend/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
COPY --from=frontend /src/pkg/webui/dist/ ./pkg/webui/dist/

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    VERSION_PKG=github.com/jingkaihe/kodelet/pkg/version && \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -buildvcs=false -trimpath \
    -ldflags="-s -w -X '${VERSION_PKG}.Version=${VERSION}' -X '${VERSION_PKG}.GitCommit=${GIT_COMMIT}' -X '${VERSION_PKG}.BuildTime=${BUILD_TIME}'" \
    -o /out/kodelet ./cmd/kodelet

# Pre-create the state directory so Docker named volumes inherit non-root ownership.
RUN mkdir -p /out/home/nonroot/.kodelet && \
    chown -R 65532:65532 /out/home/nonroot && \
    chmod 0700 /out/home/nonroot /out/home/nonroot/.kodelet

# Exercise Kodelet's normal startup path once for each target platform. This
# downloads and checksum-verifies rg and fd without carrying the temporary
# SQLite database into the final image.
FROM --platform=$TARGETPLATFORM ${RUNTIME_IMAGE} AS runtime-dependencies

ENV HOME=/home/nonroot \
    KODELET_BASE_PATH=/home/nonroot/.kodelet

COPY --from=build --chown=65532:65532 /out/kodelet /kodelet
RUN ["/kodelet", "version"]

FROM ${RUNTIME_IMAGE}

ARG VERSION=dev
ARG GIT_COMMIT=unknown

LABEL org.opencontainers.image.title="Kodelet control plane" \
      org.opencontainers.image.description="Minimal multi-architecture Kodelet control-plane image" \
      org.opencontainers.image.source="https://github.com/jingkaihe/kodelet" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT}"

ENV HOME=/home/nonroot \
    KODELET_BASE_PATH=/home/nonroot/.kodelet

COPY --from=build --chown=65532:65532 /out/home/nonroot/ /home/nonroot/
COPY --from=build --chown=65532:65532 /out/kodelet /kodelet
COPY --from=runtime-dependencies --chown=65532:65532 /home/nonroot/.kodelet/bin/ /usr/libexec/kodelet/

WORKDIR /home/nonroot
USER 65532:65532

EXPOSE 8080
STOPSIGNAL SIGTERM

ENTRYPOINT ["/kodelet", "serve", "--host=0.0.0.0", "--disable-control-plane-workspace"]
