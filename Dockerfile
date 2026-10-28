# syntax=docker/dockerfile:1.7

# Build stage for the frontend
# 前端产物与目标架构无关，固定跑在构建机原生平台，
# 避免在 arm64 目标下经由 QEMU 模拟重复构建一遍
FROM --platform=$BUILDPLATFORM node:20 AS frontend-build
WORKDIR /app
# pnpm-workspace.yaml 定义 ignoredBuiltDependencies，必须与 lockfile 一起拷贝，
# 否则 pnpm 会因未批准的构建脚本而中断安装
COPY webui/package.json webui/pnpm-lock.yaml webui/pnpm-workspace.yaml ./
RUN npm install -g pnpm@10
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile
COPY webui/ .
RUN pnpm run build

# Build stage for the backend
# 同样跑在原生平台：Go 可直接交叉编译出目标架构的二进制，
# 无需在 QEMU 模拟环境中执行
FROM --platform=$BUILDPLATFORM golang:1.26.6 AS backend-build
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOPROXY='https://goproxy.io|direct' go mod download
COPY . .
# Copy the built frontend from frontend build stage
COPY --from=frontend-build /app/dist ./webui/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X github.com/atopos31/llmio/consts.Version=${VERSION}" -o llmio .

# Final stage
FROM alpine:latest

WORKDIR /app

# Copy the binary from backend build stage
COPY --from=backend-build /app/llmio .

EXPOSE 7070

# Command to run the application
CMD ["./llmio"]
