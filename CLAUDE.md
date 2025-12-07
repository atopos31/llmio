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

# Create database directory
make mkdb
```

### Frontend Development
```bash
# Navigate to frontend directory first
cd webui

# Install dependencies
pnpm install

# Run development server (proxies /api to localhost:7070)
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
docker run -p 7070:7070 -e TOKEN=your_token llmio

# Using docker-compose
docker-compose up -d
```

## Architecture Overview

### Backend Structure (Go)
The application follows a layered architecture pattern:

1. **Handlers** (`/handler`) - HTTP request processing and routing
   - `api.go` - Main API routes and LLM endpoint compatibility
   - `chat.go` - Chat completion endpoints
   - `home.go` - Static file serving and embedded frontend
   - `count_tokens.go` - Anthropic token counting interface
   - `test.go` - Provider testing endpoints

2. **Services** (`/service`) - Business logic layer
   - `chat.go` - Core chat processing and provider orchestration
   - `balancer.go` - Load balancing algorithms and provider selection
   - `auth.go` - AuthKey management
   - `process.go` - Request processing utilities

3. **Providers** (`/providers`) - LLM provider implementations
   - `provider.go` - Provider interface definition
   - `openai.go` - OpenAI provider
   - `openai_res.go` - OpenAI responses provider
   - `anthropic.go` - Anthropic provider
   - `cache.go` - Provider caching

4. **Middleware** (`/middleware`) - Cross-cutting concerns
   - `auth.go` - Authentication (TOKEN validation, AuthKey support)

5. **Models** (`/models`) - Data layer with GORM
   - `model.go` - Provider, Model, ChatLog, ChatIO, AuthKey entities
   - `init.go` - Database initialization
   - `config.go` - Configuration storage

6. **Balancers** (`/balancers`) - Load balancing strategies
   - `balancers.go` - Lottery (weighted random) and Rotor (sequential) algorithms

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
2. **Weighted Load Balancing**: Lottery (random weighted) and Rotor (sequential) strategies
3. **Embedded Frontend**: Single binary deployment with embedded React build
4. **Layered Architecture**: Clean separation between HTTP handling, business logic, and data access

### API Design

- **LLM Compatibility**: `/v1/chat/completions` - OpenAI-compatible endpoint
- **Management APIs**: `/api/*` - Provider management, monitoring, configuration
- **Authentication**: Bearer token via `Authorization` header
- **Response Format**: Standardized JSON with `success`, `data`, `message`, `error` fields

### Environment Configuration

Key environment variables:
- `TOKEN` - API authentication token (required for secure mode)
- `GIN_MODE` - Gin framework mode (debug/test/release)
- `OPENAI_API_KEY`, `ANTHROPIC_API_KEY` - Provider API keys
- Database stored in `/db/` directory

### Authentication System

- **Single TOKEN mode**: Environment variable `TOKEN` controls all API access
- **AuthKey support**: Database-based per-project tokens with:
  - Model restrictions (AllowAll, AllowModels)
  - Expiry dates
  - Usage tracking via AuthKeyID in ChatLog

### Database

- **Type**: SQLite with GORM
- **Path**: `./db/llmio.db` (auto-created)
- **Auto-migration**: On startup via `models.Init()`
- **Key Tables**: Provider, Model, ChatLog, ChatIO, AuthKey, Config

### Development Notes

1. **Port**: Application runs on port 7070 (hardcoded in main.go)
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

### Recent Features

- AuthKey ID tracking in chat logs
- Anthropic token counting interface
- OpenAI responses provider support
- WeightedList balancer implementation with comprehensive tests