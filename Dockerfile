# syntax=docker/dockerfile:1
FROM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --legacy-peer-deps --registry=https://registry.npmjs.org
COPY web/ ./
RUN npm run build

FROM golang:1.24.2-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -o /out/panel ./cmd/panel && \
    CGO_ENABLED=0 go build -trimpath -o /out/rahctl ./cmd/rahctl && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/artifacts/rah-agent-linux-amd64 ./cmd/agent && \
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o /out/artifacts/rah-agent-linux-arm64 ./cmd/agent
RUN /out/rahctl artifacts --dir /out/artifacts

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* && useradd --system --uid 10001 rah
WORKDIR /app
COPY --from=build --chown=rah:rah /out/ /app/
COPY --from=web --chown=rah:rah /src/web/dist /app/web/dist
USER rah
ENV WEB_DIR=/app/web/dist HTTP_ADDR=:8080 AGENT_ADDR=:9443
EXPOSE 8080 9443
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=10 CMD ["/app/rahctl", "healthcheck"]
ENTRYPOINT ["/app/panel"]
