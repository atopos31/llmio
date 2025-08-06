# Build stage for the frontend
FROM node:20 AS frontend-build
WORKDIR /app
COPY webui/package.json webui/pnpm-lock.yaml ./
RUN npm install -g pnpm
RUN pnpm install
COPY webui/ .
RUN pnpm run build

# Build stage for the backend
FROM golang:latest AS backend-build
WORKDIR /app
COPY go.mod go.sum ./
RUN GOPROXY=https://goproxy.io,direct go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o llmio .

# Final stage
FROM scratch

WORKDIR /app/db
WORKDIR /app

# Copy the binary from backend build stage
COPY --from=backend-build /app/llmio .

# Copy the built frontend from frontend build stage
COPY --from=frontend-build /app/dist ./webui/dist

EXPOSE 7070

# Command to run the application
CMD ["./llmio"]