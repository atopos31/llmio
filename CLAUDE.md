# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LLMIO is a Go-based LLM proxy service that provides unified API access to multiple language model providers (OpenAI, Anthropic) with intelligent load balancing and a React-based management interface.

## Key Commands

### Backend Development
```bash
# Run the application (includes formatting and dependency management)
make run

# Build the binary
go build -o llmio .

# Run tests
go test ./...

# Format code
go fmt ./...

# Tidy dependencies
go mod tidy
```

### Frontend Development
```bash
# Navigate to frontend directory first
cd webui

# Install dependencies
pnpm install

# Run development server
pnpm run dev

# Build for production
pnpm run build

# Run linting
pnpm run lint

# Run tests
pnpm test
```

### Docker Development
```bash
# Build and run with Docker
docker build -t llmio .
docker run -p 8080:8080 -e TOKEN=your_token llmio
```

## Architecture Overview

### Backend Structure (Go)
The application follows a layered architecture pattern:

1. **Handlers** (`/handler`) - HTTP request processing and routing
   - `api.go` - Main API routes and LLM endpoint compatibility
   - `chat.go` - Chat completion endpoints
   - `home.go` - Static file serving and embedded frontend

2. **Services** (`/service`) - Business logic layer
   - `chat.go` - Core chat processing and provider orchestration
   - `balancer.go` - Load balancing algorithms and provider selection

3. **Providers** (`/providers`) - LLM provider implementations
   - `provider.go` - Provider interface definition
   - `openai.go`, `anthropic.go` - Specific provider implementations

4. **Middleware** (`/middleware`) - Cross-cutting concerns
   - Authentication, rate limiting, CORS

5. **Models** (`/models`) - Data layer with GORM
   - Provider configurations stored as JSON
   - Chat history and usage tracking

### Frontend Structure (React/TypeScript)
Modern React application with TypeScript:

1. **Components** (`/webui/src/components`) - Reusable UI components
   - Radix UI-based components in `ui/`
   - Chart components for monitoring
   - Form components with React Hook Form + Zod

2. **Routes** (`/webui/src/routes`) - Page components
   - Layout components for navigation
   - Feature-specific pages (providers, logs, settings)

3. **State Management** - React Context + local state
   - No external state management library
   - SWR for data fetching

### Key Design Patterns

1. **Provider Pattern**: Interface-based provider system allowing easy addition of new LLM providers
2. **Weighted Load Balancing**: Capability-aware provider selection with health monitoring
3. **Embedded Frontend**: Single binary deployment with embedded React build
4. **Layered Architecture**: Clean separation between HTTP handling, business logic, and data access

### API Design

- **LLM Compatibility**: `/v1/chat/completions` - OpenAI-compatible endpoint
- **Management APIs**: `/api/*` - Provider management, monitoring, configuration
- **Authentication**: Bearer token via `Authorization` header
- **Response Format**: Standardized JSON with `success`, `data`, `message`, `error` fields

### Environment Configuration

Key environment variables:
- `TOKEN` - API authentication token (required)
- `GIN_MODE` - Gin framework mode (debug/test/release)
- `OPENAI_API_KEY`, `ANTHROPIC_API_KEY` - Provider API keys
- Database stored in `/db/` directory

### Development Notes

1. **Database**: SQLite with GORM, automatic migration on startup
2. **Frontend Build**: Embedded into Go binary via `//go:embed`
3. **Provider Configuration**: JSON-based, supports multiple instances per provider type
4. **Error Handling**: Structured logging with context, standardized API responses
5. **Testing**: Limited test coverage currently, focus on manual testing

### Common Development Tasks

When modifying providers:
1. Implement the `Provider` interface in `/providers/`
2. Add provider type to constants in `/consts/`
3. Update frontend provider configuration forms

When adding API endpoints:
1. Add handler in appropriate `/handler/` file
2. Register route in `main.go`
3. Update frontend API client in `/webui/src/lib/api.ts`

When modifying the database:
1. Update model in `/models/`
2. GORM will auto-migrate on startup
3. Consider data migration for production deployments