FROM golang:1.25-alpine AS builder
ENV CGO_ENABLED=0
# Install necessary tools
RUN apk update && \
    apk add --no-cache curl unzip

WORKDIR /installer
COPY installer/go.* .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
COPY installer/. .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=linux go build -trimpath -ldflags="-s -w" -o bin/installer-linux
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=darwin go build -trimpath -ldflags="-s -w" -o bin/installer-darwin
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=windows go build -trimpath -ldflags="-s -w" -o bin/installer-windows.exe

FROM --platform=$BUILDPLATFORM node:22.23.2-alpine3.24 AS client-builder
WORKDIR /ui
# cache packages in layer
COPY ui/package.json /ui/package.json
COPY ui/package-lock.json /ui/package-lock.json
RUN --mount=type=cache,target=/usr/src/app/.npm \
    npm set cache /usr/src/app/.npm && \
    npm ci
# install
COPY ui /ui
RUN npm run build

FROM alpine
LABEL org.opencontainers.image.title="AI Workbench" \
    org.opencontainers.image.description="AI Workbench extension" \
    org.opencontainers.image.vendor="SUSE LLC" \
    com.docker.desktop.extension.api.version="0.3.4" \
    com.docker.extension.screenshots="" \
    com.docker.desktop.extension.icon="https://raw.githubusercontent.com/rancher-sandbox/rancher-desktop-rdx-ai-workbench/refs/tags/v0.2.0/workbench.svg" \
    com.docker.extension.detailed-description="" \
    com.docker.extension.publisher-url="" \
    com.docker.extension.additional-urls="" \
    com.docker.extension.categories="" \
    com.docker.extension.changelog=""

COPY docker-compose.yaml .
COPY metadata.json .
COPY workbench.svg .
COPY --from=client-builder /ui/build ui
COPY --from=builder /installer/bin/installer-linux /linux/installer
COPY --from=builder /installer/bin/installer-darwin /darwin/installer
COPY --from=builder /installer/bin/installer-windows.exe /windows/installer.exe
COPY /searxng/limiter.toml /linux/searxng/limiter.toml
COPY /searxng/settings.yml /linux/searxng/settings.yml
COPY /searxng/uwsgi.ini /linux/searxng/uwsgi.ini
COPY /mcpo/config.json /linux/mcpo/config.json
COPY /mcpo/entrypoint.sh /linux/mcpo/entrypoint.sh
