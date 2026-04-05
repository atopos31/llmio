# syntax=docker/dockerfile:1.7

# Build stage for the frontend
FROM node:20 AS frontend-build
WORKDIR /app
COPY webui/package.json webui/pnpm-lock.yaml ./
RUN npm install -g pnpm
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install
COPY webui/ .
RUN pnpm run build

# Build stage for the backend
FROM golang:latest AS backend-build
ARG VERSION=dev
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
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/atopos31/llmio/consts.Version=${VERSION}" -o llmio .

# Final stage
FROM alpine:latest

WORKDIR /app

# Copy the binary from backend build stage
COPY --from=backend-build /app/llmio .

EXPOSE 7070

# Command to run the application
CMD ["./llmio"]
