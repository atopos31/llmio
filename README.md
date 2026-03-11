# LLMIO

English | [中文](README_cn.md)

LLMIO is a Go-based LLM load‑balancing gateway that provides a unified REST API, weighted scheduling, logging, and a modern admin UI for LLM clients (claude code / codex / gemini cli / cherry studio / open webui). It helps you integrate OpenAI, Anthropic, Gemini, and other model capabilities in a single service.

## Architecture

![LLMIO Architecture](./docs/llmio.svg)

## Features
- **Unified API**: Compatible with OpenAI Chat Completions, OpenAI Responses, Gemini Native, and Anthropic Messages. Supports both streaming and non‑streaming passthrough.
- **Weighted scheduling**: `balancers/` provides two strategies (random by weight / priority by weight). You can route based on tool calling, structured output, and multimodal capability.
- **Admin Web UI**: React + TypeScript + Tailwind + Vite console for providers, models, associations, logs, and metrics.
- **Rate limiting & failure handling**: Built‑in rate‑limit fallback and provider connectivity checks for fault isolation.
- **Local persistence**: Pure Go SQLite (`db/llmio.db`) for config and request logs, ready to use out of the box.

## Deployment

### Docker Compose (Recommended)
```yaml
services:
  llmio:
    image: atopos31/llmio:latest
    ports:
      - 7070:7070
    volumes:
      - ./db:/app/db
    environment:
      - GIN_MODE=release
      - TOKEN=<YOUR_TOKEN>
      - TZ=Asia/Shanghai
```
```bash
docker compose up -d
```

### Docker
```bash
docker run -d \
  --name llmio \
  -p 7070:7070 \
  -v $(pwd)/db:/app/db \
  -e GIN_MODE=release \
  -e TOKEN=<YOUR_TOKEN> \
  -e TZ=Asia/Shanghai \
  atopos31/llmio:latest
```

### Local Run
Download the release package for your OS/arch from [releases](https://github.com/atopos31/llmio/releases) (version > 0.5.13). Example for linux amd64:
```bash
wget https://github.com/atopos31/llmio/releases/download/v0.5.13/llmio_0.5.13_linux_amd64.tar.gz
```
Extract:
```bash
tar -xzf ./llmio_0.5.13_linux_amd64.tar.gz
```
Start:
```bash
GIN_MODE=release TOKEN=<YOUR_TOKEN> ./llmio
```
The service will create `./db/llmio.db` in the current directory as the SQLite persistence file.

## Environment Variables

| Variable | Description | Default | Notes |
|---|---|---|---|
| `TOKEN` | Console login and API auth for `/openai` `/anthropic` `/gemini` `/v1` | None | Required for public access |
| `GIN_MODE` | Gin runtime mode | `debug` | Use `release` in production |
| `LLMIO_SERVER_PORT` | Server listen port | `7070` | Service listen port |
| `TZ` | Timezone for logs and scheduling | Host default | Recommend explicit setting in containers (e.g. `Asia/Shanghai`) |
| `DB_VACUUM` | Run SQLite VACUUM on startup | Disabled | Set to `true` to reclaim space |

## Development

Clone:
```bash
git clone https://github.com/atopos31/llmio.git
cd llmio
```
Build frontend (pnpm required):
```bash
make webui
```
Run backend (Go > 1.25.0):
```bash
TOKEN=<YOUR_TOKEN> make run
```
Web UI: `http://localhost:7070/`

## API Endpoints

LLMIO provides a multi‑provider REST API with the following endpoints:

| Provider | Path | Method | Description | Auth |
|---|---|---|---|---|
| OpenAI | `/openai/v1/models` | GET | List available models | Bearer Token |
| OpenAI | `/openai/v1/chat/completions` | POST | Create chat completion | Bearer Token |
| OpenAI | `/openai/v1/responses` | POST | Create response | Bearer Token |
| Anthropic | `/anthropic/v1/models` | GET | List available models | x-api-key |
| Anthropic | `/anthropic/v1/messages` | POST | Create message | x-api-key |
| Anthropic | `/anthropic/v1/messages/count_tokens` | POST | Count tokens | x-api-key |
| Gemini | `/gemini/v1beta/models` | GET | List available models | x-goog-api-key |
| Gemini | `/gemini/v1beta/models/{model}:generateContent` | POST | Generate content | x-goog-api-key |
| Gemini | `/gemini/v1beta/models/{model}:streamGenerateContent` | POST | Stream content | x-goog-api-key |
| Generic | `/v1/models` | GET | List models (compat) | Bearer Token |
| Generic | `/v1/chat/completions` | POST | Create chat completion (compat) | Bearer Token |
| Generic | `/v1/responses` | POST | Create response (compat) | Bearer Token |
| Generic | `/v1/messages` | POST | Create message (compat) | x-api-key |
| Generic | `/v1/messages/count_tokens` | POST | Count tokens (compat) | x-api-key |

### Authentication

LLMIO uses different auth headers depending on the endpoint:

#### 1. OpenAI‑style endpoints (Bearer Token)
Applies to `/openai/v1/*` and OpenAI‑compatible endpoints under `/v1/*`.
```bash
curl -H "Authorization: Bearer YOUR_TOKEN" http://localhost:7070/openai/v1/models
```

#### 2. Anthropic‑style endpoints (x-api-key)
Applies to `/anthropic/v1/*` and Anthropic‑compatible endpoints under `/v1/*`.
```bash
curl -H "x-api-key: YOUR_TOKEN" http://localhost:7070/anthropic/v1/messages
```

#### 3. Gemini Native endpoints (x-goog-api-key)
Applies to `/gemini/v1beta/*` endpoints.
```bash
curl -H "x-goog-api-key: YOUR_TOKEN" http://localhost:7070/gemini/v1beta/models
```

For claude code or codex, use these environment variables:
```bash
export OPENAI_API_KEY=<YOUR_TOKEN>
export ANTHROPIC_API_KEY=<YOUR_TOKEN>
export GEMINI_API_KEY=<YOUR_TOKEN>
```
> **Note**: `/v1/*` paths are kept for compatibility. Prefer the provider‑specific routes.

## Project Structure

```
.
├─ main.go              # HTTP server entry and routes
├─ handler/             # REST handlers
├─ service/             # Business logic and load‑balancing
├─ middleware/          # Auth, rate limit, streaming middleware
├─ providers/           # Provider adapters
├─ balancers/           # Weight and scheduling strategies
├─ models/              # GORM models and DB init
├─ common/              # Shared helpers
├─ webui/               # React + TypeScript admin UI
└─ docs/                # Ops & usage docs
```

## Screenshots

![Dashboard](./docs/home.jpeg)

![Associations](./docs/with.jpeg)

![Logs](./docs/log.jpeg)

## License

This project is released under the MIT License.

## Star History

[![Stargazers over time](https://starchart.cc/atopos31/llmio.svg?variant=adaptive)](https://starchart.cc/atopos31/llmio)
