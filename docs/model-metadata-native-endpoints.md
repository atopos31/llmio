# Provider-native model-listing endpoints: capability / context metadata survey

Research question: which native `GET /models`-style endpoints return usable **capability** and
**context-window** metadata, versus a bare `{id, object, created, owned_by}` list?

Status legend: **VERIFIED-DOC** = confirmed against official documentation fetched during this
research; **VERIFIED-SOURCE** = confirmed from upstream source code; **UNVERIFIED** = no official
documentation found.

## Summary table

| Provider | Endpoint | Auth | Capability fields? | Context / output tokens? | Pricing? | Verification method |
|---|---|---|---|---|---|---|
| OpenAI | `GET /v1/models` | `Authorization: Bearer` | ❌ none | ❌ none | ❌ | VERIFIED-DOC (official OpenAPI spec via GitHub API) |
| Anthropic | `GET /v1/models` | `x-api-key` + `anthropic-version` | ✅ `capabilities` (image_input, pdf_input, structured_outputs, thinking, effort, batch, citations, code_execution, context_management) | ✅ `max_input_tokens`, `max_tokens` | ❌ | VERIFIED-DOC (`.md` doc variant) |
| Google Gemini | `GET /v1beta/models` | `?key=` / `x-goog-api-key` | ⚠️ only `thinking` bool + `supportedGenerationMethods` | ✅ `inputTokenLimit`, `outputTokenLimit` | ❌ | VERIFIED-DOC (ai.google.dev REST ref) |
| Mistral | `GET /v1/models` | `Authorization: Bearer` | ✅ `capabilities.{completion_chat,completion_fim,function_calling,fine_tuning,vision,classification}` | ✅ `max_context_length` | ❌ | VERIFIED-DOC (docs.mistral.ai/api) |
| Groq | `GET /openai/v1/models` | `Authorization: Bearer` | ❌ none (`active` only) | ⚠️ `context_window` (optional) | ❌ | VERIFIED-DOC (console.groq.com markdown) |
| DeepSeek | `GET /models` | `Authorization: Bearer` | ⚠️ `input_modalities`/`output_modalities`, `effort` | ✅ `context_window`, `max_output_tokens` | ❌ | VERIFIED-DOC (api-docs.deepseek.com) |
| Together AI | `GET /v1/models` | `Authorization: Bearer` | ⚠️ coarse `type` enum only | ✅ `context_length` | ✅ `pricing.{input,output,cached_input,base,finetune,hourly}` | VERIFIED-DOC (docs.together.ai markdown) |
| Fireworks | `GET /v1/models` | `Authorization: Bearer` | ✅ `supportsTools`, `supportsImageInput`, `supportsServerless`, `supportsLora` | ✅ `contextLength`, `trainingContextLength` | ❌ (separate `sku_infos`) | VERIFIED-DOC (docs.fireworks.ai markdown) |
| Ollama | `GET /api/tags` | none local / Bearer cloud | ❌ none | ❌ none | ❌ | VERIFIED-DOC (docs.ollama.com/openapi.yaml) |
| Ollama | `POST /api/show` | none local / Bearer cloud | ✅ `capabilities: ["completion","thinking","vision","tools"]` | ✅ `model_info.<arch>.context_length`; `parameters` (`num_ctx`) | ❌ | VERIFIED-DOC (docs.ollama.com/openapi.yaml) |
| Ollama | `GET /api/ps` | none local | ❌ | ✅ `context_length` (running models only) | ❌ | VERIFIED-DOC |
| vLLM | `GET /v1/models` | none by default | ❌ none | ✅ `max_model_len` | ❌ | VERIFIED-SOURCE (vLLM source via jsDelivr) |
| vLLM | `GET /metrics` | none by default | ❌ | ❌ (`vllm:cache_config_info` labels only) | ❌ | VERIFIED-DOC (docs.vllm.ai metrics) |
| llama.cpp | `GET /v1/models` | none by default | ✅ `architecture.input_modalities` / `output_modalities` | ✅ `meta.n_ctx_train` | ❌ | VERIFIED-DOC (llama.cpp server README) |
| llama.cpp | `GET /props` | none by default | ❌ | ✅ `default_generation_settings.n_ctx` (runtime) | ❌ | VERIFIED-DOC |
| Moonshot / Kimi | `GET /v1/models` | `Authorization: Bearer` | ✅ `supports_image_in`, `supports_video_in`, `supports_reasoning` | ✅ `context_length` | ❌ | VERIFIED-DOC (platform.moonshot.ai + .cn) |
| Alibaba DashScope | `GET /compatible-mode/v1/models` | `Authorization: Bearer` | ❌ undocumented | ❌ undocumented | ❌ | endpoint live (401); schema UNVERIFIED |
| Alibaba DashScope | `GET /api/v1/models` (native) | `Authorization: Bearer` | ✅ `features` (`function-calling`, `structured-outputs`, ...), `capabilities` (TG, Reasoning, VU, ...) | ✅ `model_info.{context_window,max_input_tokens,max_output_tokens,max_reasoning_tokens}` | ✅ `prices[]` (tiered) | VERIFIED-DOC (alibabacloud.com help) |
| SiliconFlow | `GET /v1/models` | `Authorization: Bearer` | UNVERIFIED | UNVERIFIED | UNVERIFIED | endpoint live (401); docs are a JS SPA |
| MiniMax | `GET /v1/models` | `Authorization: Bearer` | ❌ none | ❌ none | ❌ | VERIFIED-DOC (platform.minimax.io) |
| StepFun | `GET /v1/models` | `Authorization: Bearer` | ❌ none | ❌ none | ❌ | VERIFIED-DOC (platform.stepfun.com) |
| Zhipu GLM | `GET /api/paas/v4/models` | `Authorization: Bearer` | UNVERIFIED | UNVERIFIED | UNVERIFIED | endpoint live (401); no models-list docs exist |
| Baichuan | `GET /v1/models` | `Authorization: Bearer` | UNVERIFIED | UNVERIFIED | UNVERIFIED | endpoint live (401); docs are a JS app |

---

## Anthropic — `GET /v1/models`

- Auth: `x-api-key: <key>` + `anthropic-version: 2023-06-01`
- Docs: https://docs.anthropic.com/en/api/models-list (clean markdown at
  https://docs.anthropic.com/en/api/models-list.md — 9 KB, ideal for automated fetching)
- Verification: VERIFIED-DOC (fetched the `.md` variant and read the response schema + example)

Top-level response: `{ data: [ModelInfo], first_id, has_more, last_id }` — it is a **paginated
envelope**, not an OpenAI-style `{object:"list", data:[...]}`.

`ModelInfo` fields (all confirmed present in the documented example):

| field | type | notes |
|---|---|---|
| `id` | string | e.g. `claude-opus-5` |
| `display_name` | string | human-readable name |
| `created_at` | string (RFC3339) | e.g. `2026-07-24T00:00:00Z` |
| `type` | string | literally `"model"` |
| `line` | `"haiku" \| "sonnet" \| "opus" \| "fable" \| "mythos" \| null` | model family/line; **do not infer from id** |
| `max_input_tokens` | number \| null | "Maximum input context window size in tokens for this model." |
| `max_tokens` | number \| null | "Maximum value for the `max_tokens` parameter when using this model." |
| `capabilities` | `ModelCapabilities \| null` | see below |

**`capabilities` is a real, rich capability object.** Doc: "Object mapping capability names to
their support details. Keys are always present for all known capabilities." Each key maps to
`{ "supported": bool }`, sometimes with nested detail.

Capability keys observed in the documented response example:

- `batch`, `citations`, `code_execution`, `image_input`, `pdf_input`, `structured_outputs`
  (`{supported: bool}` each)
- `thinking` → `{ supported, types: { adaptive: {supported}, enabled: {supported} } }`
- `effort` → `{ supported, low, medium, high, xhigh, max }` (each `{supported}`)
- `context_management` → `{ supported, clear_thinking_20251015, clear_tool_uses_20250919,
  compact_20260112 }` (each `{supported}`)

Caveat: the documented example shows `"max_input_tokens": 0, "max_tokens": 0`, and the field type
is `number or null`. So the *schema* supports context length, but real-world population must be
checked against the live API before relying on it.

**Verdict: strongly useful.** Anthropic is the best-behaved native endpoint surveyed — real
capability booleans (vision/pdf/structured outputs/thinking/tools-adjacent) *and* nominal
context/output limits.

---

## OpenAI — `GET /v1/models`

- Auth: `Authorization: Bearer <key>` (optionally `OpenAI-Organization`, `OpenAI-Project`)
- Verification: VERIFIED-DOC via the official OpenAPI spec, fetched through the GitHub contents
  API (`Accept: application/vnd.github.raw`) from
  `https://api.github.com/repos/openai/openai-openapi/contents/openapi.yaml` — direct
  `platform.openai.com` docs pages return a JS/HTML shell, not clean markdown.
- Path definition (`/models` → `listModels`) description: "Lists the currently available models,
  and provides basic information about each one such as **the owner and availability**."

`ListModelsResponse` = `{ object: "list", data: [Model] }`. The `Model` schema has exactly five
properties, with `required: [id, object, created, owned_by]`:

| field | type |
|---|---|
| `id` | string |
| `object` | string, always `"model"` |
| `created` | integer (unix time) |
| `owned_by` | string |
| `shutdown_date` | date or null — only when a shutdown has been announced |

**Verdict: BARE LIST — confirmed negative.** No context length, no capabilities (no vision /
tools / structured-output flags), no pricing, no modality. The only non-OpenAI-common field is
`shutdown_date`. This is the baseline bare shape the task describes.

## Google Gemini — `GET /v1beta/models`

- Auth: `?key=<API_KEY>` query param (also `x-goog-api-key`)
- Docs: https://ai.google.dev/api/models (markdown variant at https://ai.google.dev/api/models.md
  works and is 114 KB; extracted field ids only)
- Verification: VERIFIED-DOC

`ListModelsResponse` = `{ models: [Model], nextPageToken: string }`.
Doc: "Response from ListModel containing a paginated list of Models."

`Model` fields (field ids read directly from the REST reference):

| field | notes |
|---|---|
| `name` | resource name, e.g. `models/gemini-2.0-flash` |
| `baseModelId` | |
| `version` | |
| `displayName` | |
| `description` | |
| `inputTokenLimit` | **context window** |
| `outputTokenLimit` | **max output tokens** |
| `supportedGenerationMethods[]` | e.g. `generateContent`, `embedContent`, `countTokens` |
| `thinking` | boolean — "Whether the model supports thinking." |
| `temperature`, `maxTemperature`, `topP`, `topK` | sampling defaults/limits |

**Verdict: USEFUL for context/output limits.** `inputTokenLimit` + `outputTokenLimit` +
`supportedGenerationMethods` are exactly the auto-fill data needed. Capability coverage is thin
though: there is **no explicit tools / function-calling / vision flag** — you must infer those
from `supportedGenerationMethods` and the model name. `thinking` (bool) is the one real
capability boolean.

## Groq — `GET /openai/v1/models`

- Auth: `Authorization: Bearer <GROQ_API_KEY>`
- Docs: https://console.groq.com/docs/api-reference (markdown variant
  https://console.groq.com/docs/api-reference.md works)
- Verification: VERIFIED-DOC

Response object is documented as only `{ data: array, object: "list" }`. **But the documented
example response includes extra fields not in the schema table:**

```json
{ "object": "list", "data": [ {
  "id": "gemma2-9b-it", "object": "model", "created": 1693721698,
  "owned_by": "Google", "active": true, "context_window": 8192,
  "public_apps": null } ] }
```

Note the second example object (`llama3-8b-8192`) shows `active`, `context_window` and
`public_apps` — but in the doc rendering `context_window` is absent for that entry, so
`context_window` appears to be **optional/possibly-null**.

- `GET /openai/v1/models/{model}` (retrieve one) is documented with only the bare four fields
  (`created`, `id`, `object`, `owned_by`).

**Verdict: PARTIALLY USEFUL.** `context_window` gives context length when present (example shows
8192). **No capability flags at all** — no tools/vision/JSON-mode flags. `active` is an
availability flag, not a capability.

## DeepSeek — `GET /models`

- Auth: `Authorization: Bearer <key>` (live probe with a bogus token returns
  `{"error":{"message":"Authentication Fails...","type":"authentication_error"}}` — endpoint
  exists and is reachable, but the schema below is from docs)
- Docs: https://api-docs.deepseek.com/api/list-models (Docusaurus HTML — no clean `.md`;
  extracted by stripping tags from the fetched page)
- Verification: VERIFIED-DOC

**This is the surprise positive result.** The doc explicitly says:

> "Lists the currently available models, and provides metadata about each one, such as its
> display name, context window, output limit, supported input/output modalities, supported effort
> levels, and per-protocol API capabilities."

Fields on each `data[]` element:

| field | type | notes |
|---|---|---|
| `id` | string, required | model identifier |
| `object` | string, required | always `"model"` |
| `owned_by` | string, required | e.g. `deepseek` |
| `name` | string | display name for pickers/discovery tools |
| `context_window` | integer | **total token capacity, input+output** |
| `max_output_tokens` | integer | **max accepted `max_tokens`** |
| `input_modalities` | string[] | enum: `text`, `image` |
| `output_modalities` | string[] | enum: `text` |
| `effort` | object | `{ supported_levels: string[], default_level: string }` — thinking-mode effort levels |
| `api_capabilities` | object | per-protocol behaviour; `anthropic_messages.system_prompt_update` enum `leading-only` / `in-history` |

Documented example entry: `deepseek-flash` with `context_window: 1048576`,
`max_output_tokens: 393216`, `input_modalities: ["text","image"]`.

**Verdict: STRONGLY USEFUL.** Real context window, real max output, real modality array
(→ vision detection via `image` in `input_modalities`), plus reasoning-effort levels. No explicit
`tools`/`function_calling` boolean, and no pricing.

## Mistral — `GET /v1/models`

- Auth: `Authorization: Bearer <MISTRAL_API_KEY>`
- Docs: https://docs.mistral.ai/api/endpoint/models (huge single-page API reference, 1.6 MB —
  no clean `.md` variant; extracted by stripping tags and locating the `200` example)
- Verification: VERIFIED-DOC

The documented 200 example is **an array, not an object** — `[ { ...ModelCard } ]` (no
`{object:"list", data:[...]}` wrapper on this endpoint).

Fields:

| field | notes |
|---|---|
| `id` | model id |
| `capabilities` | **object of booleans**: `completion_chat`, `completion_fim`, `function_calling`, `fine_tuning`, `vision`, `classification` |
| `max_context_length` | integer — example `32768` |
| `object` | `"model"` |
| `created` | unix ts |
| `owned_by` | owner id |
| `root` | base model for fine-tunes, e.g. `open-mistral-7b` |
| `job` | fine-tune job id |
| `aliases` | array |
| `TYPE` | e.g. `"fine-tuned"` (note: uppercase `TYPE`) |
| `archived` | boolean |

`GET /v1/models/{model_id}` returns `BaseModelCard | FTModelCard` (same shape, single object).

**Verdict: STRONGLY USEFUL.** `capabilities.function_calling` and `capabilities.vision` are
exactly the booleans needed, plus `max_context_length`. No pricing.

## Together AI — `GET /v1/models`

- Auth: `Authorization: Bearer <TOGETHER_API_KEY>`
- Docs: https://docs.together.ai/reference/models (markdown variant works and is only 6 KB —
  it embeds the OpenAPI fragment)
- Base URL: `https://api.together.ai/v1` (also `https://api-inference.together.ai/v2`)
- Verification: VERIFIED-DOC

Doc summary: "Lists all of Together's open-source models and metadata including **pricing, chat
template, and context**."

`ModelInfoList` is **a bare array** (`type: array, items: ModelInfo`) — again no
`{object:"list"}` wrapper. `ModelInfo`:

| field | notes |
|---|---|
| `id` | required |
| `object` | required, const `model` |
| `created` | required, integer |
| `type` | required — enum: `chat`, `language`, `code`, `image`, `embedding`, `moderation`, `rerank` |
| `display_name` | |
| `organization` | |
| `link` | |
| `license` | |
| `context_length` | **integer, example 2048** |
| `pricing` | **object** `{ base, finetune, hourly, input, output, cached_input }` (all numbers, USD per 1M tokens) |

**Verdict: USEFUL.** `context_length` **and** real `pricing` (the only provider surveyed that
documents pricing inline), plus a coarse `type` enum (chat/code/image/embedding/rerank) which
partly substitutes for capability flags. **No** tools / vision / structured-output booleans.

## Fireworks — `GET /v1/models`

- Auth: `Authorization: Bearer <FIREWORKS_API_KEY>`
- Docs: https://docs.fireworks.ai/api-reference/list-models (markdown variant works)
- Verification: VERIFIED-DOC

Two different schemas appear in the docs; the **list** endpoint is the gateway one:

`gatewayListModelsResponse` = `{ models: [gatewayModel], nextPageToken: string, totalSize: integer }`.

`gatewayModel` fields (full property list read from the schema):

| field | notes |
|---|---|
| `name` | resource name, e.g. `accounts/my-account/models/my-model` |
| `displayName` | human-readable name |
| `description` | |
| `createTime`, `updateTime` | date-time |
| `state`, `status`, `kind` | lifecycle |
| `contextLength` | integer — "The maximum context length supported by the model." |
| `supportsImageInput` | **boolean** — "If set, images can be provided as input to the model." |
| `supportsTools` | **boolean** — "If set, tools (i.e. functions) can be provided as input to the model, and the model may respond with one or more tool calls." |
| `supportsServerless` | boolean — has a serverless deployment |
| `supportsLora`, `tunable`, `rlTunable`, `supervisedFullParameterTunable`, ... | fine-tuning capability flags |
| `conversationConfig` | `{ style, system, template }` — chat template; "If set, the Chat Completions API will be enabled" |
| `trainingContextLength` | context length for tuning |
| `defaultSamplingParams`, `defaultDraftModel`, `deprecationDate`, `serverlessModes`, ... | misc |

**Verdict: USEFUL.** `contextLength` + real `supportsTools` / `supportsImageInput` booleans.
Caveat: this is the *gateway/control-plane* model resource (resource names like
`accounts/.../models/...`), which is broader than the serverless inference catalogue — so it
describes your own uploaded/deployed models as much as public ones. No pricing in this schema
(pricing lives in a separate `sku_infos`/Orb-sourced field elsewhere in the API).

## Ollama — `/api/tags` vs `/api/show`

- Auth: none for a local server (`http://localhost:11434`); `Authorization: Bearer <OLLAMA_API_KEY>`
  for `https://ollama.com/api`
- Docs: https://docs.ollama.com/openapi.yaml (full 75 KB OpenAPI spec — the cleanest source;
  per-page markdown also exists at `/api/tags.md`)
- Verification: VERIFIED-DOC

### `GET /api/tags` — BARE

`ListResponse` = `{ models: [ModelSummary] }`, and `ModelSummary` is:

| field | notes |
|---|---|
| `name`, `model` | model name |
| `remote_model`, `remote_host` | only for remote models |
| `modified_at` | ISO 8601 |
| `size` | bytes on disk |
| `digest` | SHA256 |
| `details` | `{ format, family, families[], parameter_size, quantization_level }` |

No context length, no capabilities. `details` is about the *file* (gguf/quantisation), not
capabilities.

### `POST /api/show` — RICH (this is the one to use)

Request: `{ "model": "<name>", "verbose": true }`. `ShowResponse` fields:

| field | notes |
|---|---|
| `capabilities` | **array of strings** — "List of supported features". Documented example: `["completion", "thinking", "vision"]`. (Other known values include `tools`, `insert`.) |
| `parameters` | text blob of settings, e.g. `"temperature 0.7\nnum_ctx 2048"` |
| `template` | prompt template |
| `license` | |
| `modified_at` | |
| `details` | `{ parent_model, format, family, families[], parameter_size, quantization_level }` |
| `model_info` | object — architecture metadata; keys are namespaced, e.g. `gemma4.context_length: 131072` |
| `thinking` | `{ values: [false,true], default: true }` |

**Context length**: with `verbose: true`, `model_info` carries `<arch>.context_length` (e.g.
`gemma4.context_length: 131072`). Note this is the *trained* context; the effective runtime
`num_ctx` also appears in `parameters`.

**Bonus**: `GET /api/ps` (running models) exposes `context_length` — "Context length for the
running model" (example `4096`) — but only for models currently loaded.

**Verdict: USEFUL, but only via `POST /api/show`.** `capabilities` is exactly the
`["completion","tools","vision"]` array the task asked about, and `model_info.<arch>.context_length`
gives the context window. `/api/tags` alone is a bare list.

## vLLM OpenAI server — `GET /v1/models`, `/metrics`

- Auth: none by default (optional `--api-key`)
- Verification: VERIFIED-SOURCE — the docs site only lists `/v1/models` as "List available
  models"; the actual schema was read from upstream source via the **jsDelivr CDN**
  (`https://cdn.jsdelivr.net/gh/vllm-project/vllm@main/...`), which works even though
  `raw.githubusercontent.com` and the GitHub contents API are blocked/rate-limited here.

`vllm/entrypoints/serve/engine/protocol.py`:

```python
class ModelCard(OpenAIBaseModel):
    id: str
    object: str = "model"
    created: int = Field(default_factory=lambda: int(time.time()))
    owned_by: str = "vllm"
    root: str | None = None
    parent: str | None = None
    max_model_len: int | None = None
    permission: list[ModelPermission] = Field(default_factory=list)
```

`vllm/entrypoints/openai/models/serving.py` populates it:

```python
max_model_len = self.model_config.max_model_len
ModelCard(id=..., max_model_len=max_model_len, root=..., permission=[ModelPermission()])
```

**Verdict: USEFUL for context length.** `max_model_len` **is** the served context window and is
included for every entry. LoRA adapters get `root`/`parent` instead (no `max_model_len`).
**No capability flags** (no tools/vision booleans) — modality support is not in this schema.

### vLLM `/metrics`

`vllm:cache_config_info` is an Info-style gauge whose **labels** carry static engine config:
`block_size`, `cache_dtype`, `cpu_offload_gb`, `enable_prefix_caching`, `gpu_memory_utilization`,
... It does **not** carry `max_model_len` (grep of the official metrics doc returns zero hits).
So `/metrics` is not a context-length source — use `/v1/models`.

## llama.cpp server — `/v1/models`, `/props`, `/metrics`

- Auth: none by default (optional `--api-key`)
- Docs: https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md — the
  **raw** form works: `https://raw.githubusercontent.com/ggml-org/llama.cpp/master/tools/server/README.md`
  (116 KB; only this repo's raw path resolved — treat it as the exception, not the rule)
- Verification: VERIFIED-DOC

### `GET /v1/models` — richer than plain OpenAI

The list always has one element, and each entry has an `architecture` object and a `meta` object:

```json
{ "object": "list", "data": [ {
  "id": "../models/Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf",
  "object": "model",
  "architecture": { "input_modalities": ["text"], "output_modalities": ["text"] },
  "created": 1735142223,
  "owned_by": "llamacpp",
  "meta": { "vocab_type": 2, "n_vocab": 128256, "n_ctx_train": 131072,
            "n_embd": 4096, "n_params": 8030261312, "size": 4912898304 } } ] }
```

- `meta.n_ctx_train` = **the model's trained context length** (131072 in the example)
- `architecture.input_modalities` / `output_modalities` = **modality arrays** — `input_modalities`
  "always has `text`, plus each media type that the model supports", so **`"image"` in
  `input_modalities` is a reliable vision signal**
- `meta` can be `null` while the model is still loading; older servers may omit `architecture`

### `GET /props` — the runtime `n_ctx`

```json
{ "default_generation_settings": { "id": 0, "id_task": -1, "n_ctx": 1024, "speculative": false,
  "params": { "n_predict": -1, "temperature": 0.8, ... } } }
```

- **`default_generation_settings.n_ctx` = the actual runtime context size** (the `-c`/`--ctx-size`
  value), which is what you should trust over `n_ctx_train`
- GET `/props` is read-only by default; POST `/props` requires `--props`
- In router mode, select a model with `GET /props?model=<url-encoded>`

**Verdict: USEFUL — the best of the self-hosted options.** `n_ctx` (runtime) + `n_ctx_train`
(trained) from `/props` and `/v1/models`, plus `architecture.input_modalities` for vision.
`--metrics` is a separate Prometheus endpoint; it is not needed for context size here.

## CN providers (Moonshot/Kimi, Zhipu GLM, DashScope/Qwen, SiliconFlow, MiniMax, StepFun, Baichuan)

Method note: these hosts are reachable by `curl` but reject invalid keys with 401, so the
**success schema** had to come from official docs. All seven model-list endpoints were confirmed
to exist — each returned 401 with a provider-specific error body.

### Moonshot / Kimi — `GET /v1/models` — **USEFUL**

- Docs: https://platform.moonshot.ai/docs/api/list-models (EN) and
  https://platform.moonshot.cn/docs/api/list-models (CN) — both verified, identical schema
- Doc: "List all currently available models, including model ID, **context length, and capability
  flags**. We recommend querying this endpoint before creating a chat completion to verify the
  target model is available and supports the required capabilities."

| field | type | notes |
|---|---|---|
| `id` | string | e.g. `kimi-k3` |
| `object` | string | `"model"` |
| `created` | integer | unix ts |
| `owned_by` | string | `"moonshot"` |
| `context_length` | integer | **maximum context length (tokens)** |
| `supports_image_in` | boolean | image input |
| `supports_video_in` | boolean | video input |
| `supports_reasoning` | boolean | "deep thinking" |

**USEFUL** — context length plus three real capability booleans. No pricing, and no explicit
`tools`/`function_calling` flag.

### Zhipu / GLM (BigModel & Z.AI) — `GET /api/paas/v4/models` — **UNVERIFIED**

- Endpoint exists: `https://open.bigmodel.cn/api/paas/v4/models` → 401
  `{"error":{"code":"401","message":"令牌已过期或验证不正确"}}`
- **No official documentation of a models-list response schema was found.**
  `https://docs.bigmodel.cn/llms.txt` indexes the entire doc set and has no "list models" page;
  `https://docs.z.ai/llms.txt` lists every `/api-reference/*` page and likewise has **no
  models-list endpoint** (only chat-completion, tokenizer, web-search, image, video, agents).
- Cross-check: https://models.dev classifies `zhipuai` as `@ai-sdk/openai-compatible`
  (`api: https://open.bigmodel.cn/api/paas/v4`) — i.e. no native metadata adapter.

**UNVERIFIED — assume a bare OpenAI-compatible list.** Metadata is obtainable from models.dev as
a fallback, not from the vendor API.

### Alibaba DashScope / Qwen — **USEFUL, but only via the native endpoint**

Two endpoints behave differently:

**(a) `GET /compatible-mode/v1/models`** (OpenAI-compatible) — 401 with
`{"error":{"message":"Incorrect API key provided...","type":"invalid_request_error"}}`.
This is the OpenAI-compatible surface; **no documented extra metadata** → treat as BARE.

**(b) `GET /api/v1/models`** (native DashScope) — **rich and fully documented.** Also exists
(401 `{"code":"InvalidApiKey","message":"Invalid API-key provided."}`).
Docs: https://www.alibabacloud.com/help/en/model-studio/list-models

Response: `{ request_id, output: { total, page_no, page_size, models: [...] } }`. Each model:

| field | notes |
|---|---|
| `model` | model ID |
| `name` | display name |
| `description` | |
| `provider`, `inference_provider` | e.g. `qwen`, `aliyun-bailian` |
| `capabilities` | **Array[String] modality types** — `TG` (text generation), `Reasoning`, `VU` (visual understanding), `IG`, `VG`, `ASR`, `TTS`, `TR`, `ME`, `Multimodal-Omni`, ... |
| `features` | **Array[String] capabilities** — `function-calling`, `structured-outputs`, `web-search`, `prefix-completion`, `cache`, `batch`, `fine-tuning`, `model-experience` |
| `published_time` | `yyyy-MM-dd HH:mm:ss`, nullable |
| `inference_metadata` | `{ request_modality: [Text/Image/Audio/Video], response_modality: [...] }` |
| `model_info` | **`{ context_window, max_input_tokens, max_output_tokens, max_reasoning_tokens, reasoning_max_input_tokens, reasoning_max_output_tokens }`** (null = no limit / N/A) |
| `prices` | **Array[Object]** — `{ range_name, prices: [{ type, price, price_unit, ... }] }`, supports tiered pricing |

**USEFUL** — the richest CN response found: `features` literally contains `function-calling` and
`structured-outputs`, `capabilities` covers modality/reasoning, and `model_info` gives the full
context/output token breakdown, plus `prices`.
The same `capabilities` / `features` / `context_window` names also work as **query filters**
(e.g. `?capabilities=TG&capabilities=Reasoning`, `?features=function-calling`).

### SiliconFlow — `GET /v1/models` — **UNVERIFIED**

- Endpoint exists: 401 `{"code":30014,"data":null,"message":"Token is invalid."}`
- Docs: https://cloud.siliconflow.cn/docs/api-reference/models/get-model-list (and the `.com`
  mirror) — both returned a JS-rendered Ant Design SPA shell with no extractable response schema.
  `https://docs.siliconflow.cn/llms.txt` is 404.
- Cross-check: models.dev classifies `siliconflow` as `@ai-sdk/openai-compatible`; its per-model
  records do carry `limit.context`, `tool_call`, `reasoning`, `modalities`, but that is models.dev's
  curated data, not proof of the vendor response.

**UNVERIFIED — assume a bare OpenAI-compatible list.** Note SiliconFlow's public `/models`
catalogue page is a separate marketing/docs artefact, not the API response.

### MiniMax — `GET /v1/models` — **BARE**

- Docs: https://platform.minimax.io/docs/api-reference/models (host `api.minimaxi.com`)
- Documented response, verbatim:

```json
{ "object": "list", "data": [
  { "id": "MiniMax-M3",   "object": "model", "created": 1780272000, "owned_by": "minimax" },
  { "id": "MiniMax-M2.7", "object": "model", "created": 1773799200, "owned_by": "minimax" },
  { "id": "MiniMax-M2.5", "object": "model", "created": 1770948000, "owned_by": "minimax" } ] }
```

Doc labels it "OpenAI Compatible List Models ... Returns a list of all available models
compatible with OpenAI API specification."

**BARE LIST** — only the four standard fields. No context, no capabilities, no pricing.

### StepFun — `GET /v1/models` — **BARE**

- Docs: https://platform.stepfun.com/docs/zh/api-reference/models/list
- Documented response, verbatim:

```json
{ "object": "list", "data": [
  { "id": "step-5-preview", "object": "model", "created": 1789747200, "owned_by": "stepai" },
  { "id": "step-3.7-flash", "object": "model", "created": 1713196800, "owned_by": "stepai" },
  { "id": "step-1o-turbo-vision", "object": "model", "created": 1711015200, "owned_by": "stepai" } ] }
```

**BARE LIST** — only `id`/`object`/`created`/`owned_by`. Note the vision capability is visible
only in the *model name* (`step-1o-turbo-vision`), never as a field.

### Baichuan — `GET /v1/models` — **UNVERIFIED**

- Endpoint exists: 401 `{"error":{"code":"invalid_api_key",...,"message":"Incorrect API key provided. You can find your API key at https://platform.baichuan-ai.com/console/apikey"}}`
- Docs: https://platform.baichuan-ai.com/docs/api — a JS app; only an `object` token was
  extractable, no models-list response schema. `llms.txt` is 404.
- **Absent from models.dev** as well, so there is no fallback source either.

**UNVERIFIED — no evidence either way; assume a bare OpenAI-compatible list.**

### models.dev as a cross-check / fallback

https://models.dev/api.json (5.3 MB single JSON, works with `curl`) covers these vendors and, per
model, gives `limit.context` / `limit.output`, `tool_call`, `reasoning`, `modalities.input` /
`modalities.output`, and pricing. For the CN vendors whose native endpoints are bare or
undocumented (Zhipu, SiliconFlow, Baichuan, MiniMax, StepFun), this is the practical way to
auto-fill capability and context metadata. It is a third-party curated database, **not** a
provider-native endpoint, so it needs its own refresh/versioning story.

---

# Bottom line

## Native endpoints that are actually useful for auto-filling capabilities / context

| Rank | Provider | Endpoint | What you get | Verdict |
|---|---|---|---|---|
| 1 | **Anthropic** | `GET /v1/models` | `max_input_tokens`, `max_tokens`, rich `capabilities` (image_input, pdf_input, structured_outputs, thinking, effort) | best capability coverage |
| 2 | **Alibaba DashScope** | `GET /api/v1/models` (native, **not** `/compatible-mode`) | `features` incl. `function-calling` + `structured-outputs`, `capabilities` modalities, `model_info.{context_window,max_input_tokens,max_output_tokens}`, `prices` | richest CN response |
| 3 | **Mistral** | `GET /v1/models` | `capabilities.function_calling` / `.vision`, `max_context_length` | clean booleans |
| 4 | **DeepSeek** | `GET /models` | `context_window`, `max_output_tokens`, `input_modalities`, `effort` | rich, no tools flag |
| 5 | **Moonshot / Kimi** | `GET /v1/models` | `context_length`, `supports_image_in`, `supports_video_in`, `supports_reasoning` | context + 3 capability booleans |
| 6 | **Google Gemini** | `GET /v1beta/models` | `inputTokenLimit`, `outputTokenLimit`, `supportedGenerationMethods`, `thinking` | limits only, no tools/vision flag |
| 7 | **Ollama** | `POST /api/show` | `capabilities: ["completion","tools","vision"]`, `model_info.<arch>.context_length` | `/api/tags` alone is bare |
| 8 | **Fireworks** | `GET /v1/models` | `contextLength`, `supportsTools`, `supportsImageInput` | control-plane resource |
| 9 | **vLLM** | `GET /v1/models` | `max_model_len` (context), `root` | context only |
| 10 | **llama.cpp** | `GET /props` (+ `/v1/models`) | runtime `n_ctx`, `n_ctx_train`, `architecture.input_modalities` (vision) | local only |
| 11 | **Groq** | `GET /openai/v1/models` | `context_window` when present | context only, no caps |
| 12 | **Together** | `GET /v1/models` | `context_length`, `pricing`, coarse `type` | no capability booleans |

## Native endpoints that are NOT useful (bare list)

- **OpenAI** `GET /v1/models` — confirmed negative: exactly `id`, `object`, `created`, `owned_by`
  (+ optional `shutdown_date`). No context, no capabilities, no pricing.
- **MiniMax** `GET /v1/models` — documented bare four-field list.
- **StepFun** `GET /v1/models` — documented bare four-field list.
- **Together** — useful only for context/pricing, zero capability flags.
- **Groq** — context only, no capability flags.
- **Zhipu GLM, SiliconFlow, Baichuan** — undocumented; treat as bare (UNVERIFIED).

## Providers I could NOT verify

- **Zhipu / GLM** (`open.bigmodel.cn/api/paas/v4/models`) — endpoint live (401), but no vendor
  documentation of the list response exists, and z.ai's full API index has no models-list page.
- **SiliconFlow** (`api.siliconflow.cn/v1/models`) — endpoint live (401); docs are a JS SPA with
  no extractable schema; no `llms.txt`.
- **Baichuan** (`api.baichuan-ai.com/v1/models`) — endpoint live (401); docs are a JS app; also
  absent from models.dev, so there is no fallback source at all.
- **DashScope `/compatible-mode/v1/models`** — the *native* `/api/v1/models` is fully documented,
  but the OpenAI-compatible alias's own response schema was not documented.

## Practical conclusion

Native endpoints alone **cannot** fill llmio's three capability booleans for every provider:
only Anthropic, DashScope (native), Mistral, DeepSeek, Moonshot, Fireworks, Ollama and
(partially) Gemini expose capability signals, and the single most-used provider (OpenAI) exposes
**nothing**. Coverage is also inconsistent in field naming (`capabilities.vision` vs
`supportsImageInput` vs `input_modalities` vs `supports_image_in` vs `features`), so a
per-provider adapter is required rather than one generic parser. The aggregated datasets
(models.dev / LiteLLM / Vercel AI Gateway) remain the only way to get uniform coverage across all
target fields at once — and are the sole option for Zhipu, SiliconFlow and Baichuan.
