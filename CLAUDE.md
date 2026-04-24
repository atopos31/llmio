# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LLMIO is a Go-based LLM proxy service that provides unified API access to multiple language model providers (OpenAI, Anthropic, Gemini) with intelligent load balancing and a React-based management interface.

## Key Commands

### Backend Development
```bash
# Run the application (formats, tidies, creates db dir, then starts)
TOKEN=<YOUR_TOKEN> make run

# Build the binary
go build -o llmio .

# Run all tests
go test ./...

# Run a single package's tests
go test ./balancers/...

# Run a single test
go test -run TestWeightedList ./balancers/

# Format code
go fmt ./...

# Tidy dependencies
go mod tidy
```

### Frontend Development
```bash
cd webui

# Install dependencies
pnpm install

# Run development server (proxies /api to localhost:7070)
pnpm run dev

# Build for production
pnpm run build

# Build frontend and embed into Go binary
make webui

# Run linting
pnpm run lint
```

### Docker Development
```bash
docker build -t llmio .
docker run -p 7070:7070 -e TOKEN=your_token llmio
docker-compose up -d
```

## Architecture Overview

### Backend Structure

The application follows a layered architecture:

1. **Handlers** (`/handler`) — HTTP request processing and routing. Each API area gets its own file: `chat.go` (LLM completion endpoints), `api.go` (management APIs), `models.go` (model CRUD), `auth_keys.go` (API key management), `event_logging.go` (Claude Code event ingestion), `home.go` (static file serving), `count_tokens.go` (Anthropic token counting), `test.go` (provider connectivity tests), `version.go`.

2. **Services** (`/service`) — Business logic layer.
   - `chat.go` — Core chat processing: balancing, request dispatch, retry orchestration
   - `before.go` — Pre-processing pipeline that parses incoming requests to detect capabilities (tool calling, structured output, image input) for routing decisions. Has per-provider-style parsers (OpenAI, OpenAIRes, Anthropic, Gemini)
   - `models.go` — Provider/model association management
   - `auth.go` — AuthKey management
   - `process.go` — Request processing utilities
   - `provider_error_matcher.go` — Error classification for rate limits and failures
   - `log_cleanup.go` — Scheduled log retention cleanup

3. **Providers** (`/providers`) — LLM provider adapters implementing the `Provider` interface (`provider.go`): `openai.go`, `openai_res.go`, `anthropic.go`, `gemini.go`. Each provider handles building upstream HTTP requests and listing available models.

4. **Middleware** (`/middleware`) — `auth.go` (TOKEN/AuthKey/Bearer/x-api-key/x-goog-api-key), `cors.go`

5. **Models** (`/models`) — GORM data layer: `model.go` (Provider, Model, ChatLog, ChatIO, AuthKey, Config entities), `init.go` (DB init and auto-migration), `config.go`

6. **Balancers** (`/balancers`) — Load balancing strategies: `balancers.go` (Lottery/weighted random, Rotor/sequential) and `breaker.go` (circuit breaker wrapper with Closed→Open→HalfOpen states)

7. **Common** (`/common`) — Shared helpers: pagination, standardized API response format

8. **Consts** (`/consts`) — Constants: provider styles, balancer strategies, auth key prefixes, context keys, version

9. **Pkg** (`/pkg`) — Internal utility packages: `env/` (env var helpers), `token/` (token utilities)

### Frontend Structure

React 19 + TypeScript + Vite with SWC. Key libraries: Tailwind CSS v4, Radix UI primitives, React Router v7, React Hook Form + Zod, Recharts, i18next, SWR for data fetching. Components in `webui/src/components/` (Radix UI-based primitives in `ui/`), pages in `webui/src/routes/`, API client in `webui/src/lib/api.ts`.

### Key Design Patterns

1. **Provider Pattern**: Interface-based (`providers/provider.go`) with factory function `New()` — add a new provider by implementing `BuildReq()` and `Models()`, then registering in the factory switch and `consts/`
2. **Before Pipeline**: Request pre-processors (`service/before.go`) parse incoming bodies to detect tool calling, structured output, and image capabilities. These flags drive model-provider routing (co-occurrence filtering) and balancing decisions
3. **Weighted Load Balancing**: Lottery (random by weight) and Rotor (sequential with weight decrement) strategies, both wrapped with an optional circuit breaker
4. **Circuit Breaker**: Wraps any balancer with Closed→Open→HalfOpen state machine per provider; on repeated failure, the provider is excluded from selection for a cooldown window
5. **Embedded Frontend**: Single binary deployment — React build embedded via `//go:embed` into the Go binary
6. **Layered Architecture**: Handlers → Services → Providers/Models, with middleware for cross-cutting auth

### Key Flows

**Chat request lifecycle** (`service/chat.go` → `handler/chat.go`):
1. Request arrives at provider-specific route (e.g., `/openai/v1/chat/completions`)
2. `Before` parser extracts model name, capabilities (tool call, structured output, image)
3. Query DB for model-provider associations matching those capabilities, sorted by priority
4. Build weighted items, select balancer (Lottery/Rotor), optionally wrap with circuit breaker
5. Pop a provider from balancer, build upstream request via provider adapter
6. Stream/proxy response, record ChatLog + ChatIO
7. On failure: classify error, report to breaker, retry with next provider (up to max retries)

### Routing Structure

All routes are defined in `main.go`. Three provider-specific groups (`/openai`, `/anthropic`, `/gemini`) plus compatibility aliases under `/v1`. Management APIs under `/api` (auth required).

- OpenAI auth: `Authorization: Bearer <TOKEN>`
- Anthropic auth: `x-api-key: <TOKEN>`
- Gemini auth: `x-goog-api-key: <TOKEN>`
- Event logging: `/anthropic/api/event_logging/batch` (Claude Code batch events)

### Environment Variables

| Variable | Description | Default |
|---|---|---|
| `TOKEN` | Auth token for API and console login | (required) |
| `GIN_MODE` | Gin runtime mode (`debug`/`release`) | `debug` |
| `LLMIO_SERVER_PORT` | Server listen port | `7070` |
| `TZ` | Timezone for logs/scheduling | Host default |
| `DB_VACUUM` | Run SQLite VACUUM on startup | disabled |

### Database

SQLite via GORM at `./db/llmio.db` (auto-created). Auto-migration on startup. Key tables: Provider, Model, ModelProvider (join), ChatLog, ChatIO, AuthKey, Config.

### When Modifying Code

- **New provider**: Implement `Provider` interface → register in factory switch and `consts/consts.go` → add `Before` parser in `service/before.go` → update frontend forms
- **New API endpoint**: Add handler → register route in `main.go` → update `webui/src/lib/api.ts`
- **Database changes**: Update model in `models/` → GORM auto-migrates on startup
