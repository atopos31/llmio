# Aggregated LLM Model-Metadata Sources — Research Report

**Scope:** Which aggregated / community sources can auto-fill **capabilities** (tool calling, vision, structured output) and **context window / max output** for many providers at once, for a self-hostable Go LLM proxy (llmio).
**Research date:** 2026-10-06 (all "now" references are relative to this date).
**Method:** every claim below was verified by actually fetching the URL with `curl.exe` from this machine. `web_search` was unavailable (HTTP 401). GitHub REST API was used until it hit the unauthenticated 60 req/hr rate limit; `data.jsdelivr.com` and the jsDelivr/ghproxy raw mirrors were used afterwards.

**Evidence classes used throughout:**
- **(a) documented** — stated in a README, docs page, schema, or ToS that I fetched.
- **(b) observed live** — I fetched the endpoint/file and counted the values myself.
- **(c) inferred** — my reasoning from (a)+(b), not directly stated by the source.

**Network notes for reproducibility:** `raw.githubusercontent.com` was *intermittently* reachable (it worked for `anomalyco/models.dev` but reset connections for `BerriAI/litellm`). Working mirrors observed: `https://fastly.jsdelivr.net/gh/<owner>/<repo>@<ref>/<path>`, `https://gcore.jsdelivr.net/gh/...`, `https://ghproxy.net/https://raw.githubusercontent.com/...`. `https://cdn.jsdelivr.net` (non-fastly) failed with connection reset. `api.github.com` works unauthenticated but rate-limits quickly.

---

## 1. Comparison table

Coverage numbers are **counted by me from the fetched payloads** (evidence class (b)), not taken from marketing copy.

| Source | Endpoint / file | Format | Auth | License | Coverage (counted) | Last update (evidence) | Context | Max out | Tools | Vision | Structured out | Pricing | Redistribute into self-hosted app? |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **models.dev** | `https://models.dev/api.json` | JSON | none | **MIT** | **226 providers / 8,389 models** | commit `2026-10-06T03:33:46Z`; hourly CI | ✅ 98% | ✅ 97% | ✅ 87% | ✅ 59% (modalities) | ⚠️ 56% | ✅ 95% | ✅ **Yes — MIT** |
| **LiteLLM** | `.../litellm/main/model_prices_and_context_window.json` | JSON | none | **MIT** | **138 providers / 4,479 entries** | PyPI 1.104.0 `2026-10-03`; 913 releases | ✅ 95%* | ✅ 89%* | ✅ 73%* | ⚠️ 42%* | ✅ 50%* | ✅ 95%* | ✅ **Yes — MIT** |
| **OpenRouter** | `https://openrouter.ai/api/v1/models` | JSON | none | ⚠️ **ToS forbids scraping** | **464 models** | live; `Cache-Control: max-age=120` | ✅ 464/464 | ✅ 456/464 | ✅ 396 | ✅ 293 | ✅ 380 | ✅ 464/464 | ❌ **No — ToS §prohibited** |
| **Vercel AI Gateway** | `https://ai-gateway.vercel.sh/v1/models` | JSON | none | Apache-2.0 (repo) / service ToS | **409 models** | live; `X-Vercel-Cache: HIT` | ✅ | ✅ | ✅ (`tags`) | ✅ 189 | ✅ (`structured-output`) | ✅ | ⚠️ **Unclear — service endpoint, no data license** |
| **go-llm-specs** | `models_gen.go` / `data/models.json` | Go + JSON/YAML | none | **Apache-2.0** | **581 models** | commit `2026-10-05`; daily CI | ✅ 581/581 | ✅ 581/581 | ✅ 384 | ✅ 254 | ✅ 409 (JSON mode) | ❌ **none** | ✅ **Yes — Apache-2.0** |
| **modelscan/registry** | `models.json` (2.2 MB) | JSON | none | **CC-BY-4.0** | **1,520 models** | commit `2026-09-07` (~1 mo stale) | ✅ 73% | ✅ 62% | ✅ 669 true | ✅ 631 | ⚠️ via `other_parameters` | ✅ per-source | ⚠️ Yes **with attribution** |
| **Helicone** | `packages/cost/models/**` | **TypeScript source** | n/a | **Apache-2.0** | 16 authors / 25 providers | `packages/cost` last commit `2026-04-04` | ✅ | ✅ | ✅ (`tools`) | ✅ | ✅ (`structured_outputs`) | ✅ rich | ⚠️ Yes, but **TS source, must transpile** |
| **Portkey** | `src/data/models.json` | JSON | none (repo) | **MIT** | 1,928 models | repo pushed `2026-05-25` (~4.5 mo stale) | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ⚠️ Yes but **useless — 4 fields only** |
| **llm-prices.com** | `https://www.llm-prices.com/current-v1.json` | JSON | none | ❌ **no LICENSE file** | 170 prices / 12 vendors | `updated_at: 2026-09-29` | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ input/output/cached | ❌ **No — no license granted** |
| **InterwebAlchemy/model-metadata-central** | `models/*.yaml` | YAML | none | ❌ **no LICENSE file** | ~100+ models | push `2026-09-18` | ✅ | ✅ | ✅ (`tuning: function`) | ✅ | ⚠️ (`tuning`) | ✅ | ❌ **No — no license granted** |
| **modelfax** | `bytebrujo.github.io/modelfax/data/*.json` | JSON | none | MIT code / **CC-BY-4.0 data** | **25 models only** | commit `2026-09-04` | ✅ | ✅ | ❌ | ✅ | ❌ | ✅ + batch | ⚠️ Yes w/ attribution, but **tiny** |
| **Ollama registry** | `registry.ollama.ai/v2/library/{m}/manifests/{t}` | OCI manifest | none | MIT (client) | — | live | ❌ **blobs only** | ❌ | ❌ | ❌ | ❌ | ❌ | ⚠️ Nothing to redistribute |
| **HuggingFace config.json** | `huggingface.co/{repo}/raw/main/config.json` | JSON | none (401 if gated) | per-repo | per-repo | live | ⚠️ **unreliable** | ❌ | ❌ | ❌ | ❌ | ❌ | ⚠️ per-repo model licenses |
| **tokscale** | npm `tokscale` | — | — | MIT | **n/a** | npm 4.18.0 `2026-10-05` | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | n/a — **not a metadata registry** |
| **aiprice** | — | — | — | — | **does not exist** | — | — | — | — | — | — | — | n/a |
| **llm-model-info** | — | — | — | — | **does not exist** | — | — | — | — | — | — | — | n/a |

\* LiteLLM percentages are over the **chat/completion/responses subset (3,554 entries)**, not all 4,479 — image-generation/embedding/audio entries have no chat capabilities and would drag the numbers down misleadingly.

**Legend:** ✅ = populated for the majority · ⚠️ = partial or caveated · ❌ = absent.

---

## 2. Per-source notes

### 2.1 models.dev — **the strongest single source**

- **URLs (all verified 200):**
  - `https://models.dev/api.json` — 5,315,044 B, 226 providers, 8,389 provider-model entries *(observed live)*
  - `https://models.dev/api.json?type=all` — 5,321,100 B, 8,401 entries (12 extra "decision"-type models) *(observed live)*
  - `https://models.dev/models.json` — 408,187 B, **446 provider-agnostic canonical models** *(observed live)*
  - `https://models.dev/catalog.json` — 5,730,197 B, `{providers: {…226}, models: {…446}}` *(observed live)*
  - `https://models.dev/model-schema.json` — 356,092 B *(observed live)*
  - `https://models.dev/logos/{provider}.svg` *(documented in README)*
- **Who runs it:** created by **OpenCode** (the `opencode.ai` coding agent), hosted by the **`anomalyco`** GitHub org. The README states: *"We started Models.dev as a community-contributed project… We also use it internally in opencode."* *(documented)*
- **GitHub repo:** `https://github.com/anomalyco/models.dev` — 7,124 stars, 1,766 forks, created 2025-06-04. The older `https://github.com/sst/models.dev` **redirects** to `anomalyco/models.dev` (verified via API: `full_name` returns `anomalyco/models.dev`). *(observed live)*
- **LICENSE:** **MIT**, `Copyright (c) 2025 models.dev`. Verified by fetching `LICENSE` through the GitHub license API (base64-decoded). *(observed live)*
- **Default branch is `dev`, not `main`** — this matters for raw-file URLs. `https://fastly.jsdelivr.net/gh/anomalyco/models.dev@dev/...` works; `@main` returns 404. *(observed live)*
- **Data model:** TOML files in the repo — `providers/{id}/provider.toml`, `providers/{id}/models/{model}.toml` (provider-specific serving facts + pricing), and `models/{lab}/{model}.toml` (provider-agnostic facts). Provider TOMLs can use `base_model = "lab/model"` to inherit and override. The generated JSON exposes `canonical_model_id` so a provider-specific model can be attributed to its originating lab. *(documented in README)*
- **Schema:** `model-schema.json` is declared as JSON Schema draft 2020-12, **but** its only definition is `$defs.Model = {"type":"string","enum":[...all model IDs...]}` — it is a **model-ID enum, not a field-level schema**. There is no published JSON Schema describing the `limit`/`cost`/`modalities` objects. *(observed live — important correction to the common assumption)*
- **Per-provider endpoint: NO.** `https://models.dev/api.json?provider=anthropic` **ignores the parameter** and returns the full 5,315,044 B payload. There is no `/api/{provider}.json`. Per-provider granularity exists only as TOML files in the repo (fetchable individually via jsDelivr, but that is repo access, not an API). *(observed live)*
- **Update cadence — very high:** `.github/workflows/sync-models.yml` *"runs on an hourly schedule and manually through `workflow_dispatch`"*, checks out `dev`, and runs **one provider per matrix job**, creating a provider-specific PR only when files changed. A separate recovery workflow runs **every six hours**. Last commit observed: `2026-10-06T03:33:46Z chore(sync): update Kilo model catalog (#8857)`, preceded 7 seconds earlier by an OpenRouter sync — i.e. multiple automated sync commits per hour. *(documented + observed live)*
- **Sync provenance:** it pulls from OpenRouter, Kilo, Vercel AI Gateway, Cloudflare Workers AI / AI Gateway, Google, xAI, DigitalOcean, Ollama Cloud, GitHub Copilot, Tinfoil, Fireworks, DeepInfra, etc. The `vercel:generate` script *"reads `reasoning_options` from the public Vercel AI Gateway `/v1/models` catalog"*. *(documented in sync.md)*
- **SDK:** npm `@opencode-ai/models`, **MIT**, v0.0.95 published `2026-10-05`, 95 versions since `2026-07-03` — actively released. *(observed live)*
- **HTTP caching:** `Cache-Control: public, max-age=0, must-revalidate` with an `ETag` — cheap to poll conditionally. *(observed live)*
- **Field coverage I counted over all 8,389 models:**
  - `limit.context` 8,232 (**98%**), `limit.output` 8,164 (**97%**), `limit.input` only 1,457 (**17%**)
  - `cost.input`/`cost.output` 7,952 (**95%**), `cost.cache_read` 5,412 (65%), `cost.cache_write` 1,809 (22%)
  - `tool_call` 7,327 (**87%**), `structured_output` 4,683 (**56%**)
  - input modalities containing `image` 4,957 (**59%**), plus `pdf` 2,060, `video` 1,415, `audio` 762
  - **4,486 models** have context + output + tool_call + image + cost all populated
  - `attachment`, `reasoning`, `temperature`, `release_date`, `last_updated`, `open_weights` are present on **100%**
- **⚠️ Caveat:** `structured_output` and `tool_call` are `true`/`false`/absent. Absence is **not** `false` — treat missing as unknown. Same for `limit.input` (the 17% field is the *input* cap, distinct from the *context* cap).
- **Redistribution:** ✅ MIT — clean to bundle into a self-hosted Go binary.

### 2.2 OpenRouter — richest live data, but **restrictive ToS**

- **URL:** `https://openrouter.ai/api/v1/models` — 772,730 B, `total_count: 464` *(observed live)*
- **Auth: none.** Verified with a bare `curl` — HTTP 200, no `Authorization` header. `Access-Control-Allow-Origin: *`. *(observed live)*
- **Caching:** `Cache-Control: public, max-age=120, stale-while-revalidate=3600, stale-if-error=3600`. *(observed live)*
- **Schema fields useful for this project** *(observed live)*:
  - `id`, `canonical_slug`, `hugging_face_id`, `name`, `created`, `description`
  - `context_length` (int)
  - `architecture`: `{modality, input_modalities[], output_modalities[], tokenizer, instruct_type}` — `modality` is a compact string like `"text+image+file->text"`
  - `pricing`: `{prompt, completion, input_cache_read, input_cache_write, input_cache_write_1h, web_search, internal_reasoning, image, audio, image_output, audio_output, overrides}` (all **strings**, per-token USD)
  - `top_provider`: `{context_length, max_completion_tokens, is_moderated}` ← **this is where max output lives**
  - `supported_parameters[]` ← **the capability signal**: contains `tools`, `tool_choice`, `response_format`, `structured_outputs`, `reasoning`, `parallel_tool_calls`, `web_search_options`, `prediction`
  - `per_request_limits`, `default_parameters`, `supported_voices`, `knowledge_cutoff`, `expiration_date`, `links`, `benchmarks`, `reasoning`
- **Coverage I counted (464 models):** `context_length` **464/464**; `top_provider.max_completion_tokens` **456/464** (8 missing); `tools` **396**; `tool_choice` **389**; `response_format` **397**; `structured_outputs` **380**; image input **293**; `pricing.prompt` **464/464**. `context_length` ranged 4,095 → 2,000,000. *(observed live)*
- **Rate limits** *(documented at `https://openrouter.ai/docs/api-reference/limits`)*: OpenRouter enforces (1) **credit limits** and (2) **rate limits** — free-model variants (`:free` suffix) get per-minute and per-day request caps tied to lifetime credits purchased; plus global DDoS protection. Governed **globally per account**, so extra API keys do not help. `X-RateLimit-*` headers are returned. A `GET /api/v1/key` endpoint reports `limit`, `limit_reset`, `limit_remaining`, `free_model_daily_requests`. **The models list endpoint itself required no key when I fetched it**, but the ToS clause below makes automated fetching legally risky regardless.
- **⚠️ ToS — RESTRICTIVE, this is the headline finding.** `https://openrouter.ai/terms` (**Last Updated: August 31, 2026**) prohibits, verbatim:
  > *"access the Site or Service for purposes of reselling API access to Models or otherwise **developing a competing service**; develop, support or use software, devices, scripts, robots or any other means or processes (such as crawlers, browser plugins, add-ons or any other automated technology) to **scrape or copy any information on the Site or the Services**; bypass any technical measures implemented by OpenRouter that are designed to prevent scraping…"*
- **Interpretation (c) inferred):** a self-hosted LLM proxy that bundles a snapshot of OpenRouter's model list is arguably *"developing a competing service"* and definitely *"copying information via automated technology."* **This is a real redistribution risk.** Note the tension: models.dev syncs **from** OpenRouter **hourly** and republishes under MIT — which does not cure the upstream restriction, it only moves it. If you bundle models.dev data, you inherit a chain-of-title question. Practically, models.dev is the safer consumption path because you are not the one scraping OpenRouter.
- **Redistribution:** ❌ Do not scrape-and-bundle directly.

### 2.3 LiteLLM — the deepest capability flags, with one important trap

- **Primary URL:** `https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json` — **3,051,890 B** *(observed live via `fastly.jsdelivr.net` and `ghproxy.net`; `raw.githubusercontent.com` itself reset the connection)*
- **Backup file — corrected location:** `model_prices_and_context_window_backup.json` at the **repo root is 404**. The real file is `https://.../litellm/main/litellm/model_prices_and_context_window_backup.json` (inside the `litellm/` package dir), and it is **byte-identical** to the main dataset (`identical bytes: True 3051890 3051890`). *(observed live)*
- **Per-provider JSON in the repo: none.** The repo root contains exactly two data files: `model_prices_and_context_window.json` and `model_prices_and_context_window.schema.json`. There is no `providers/*.json` split. `provider_endpoints_support.json` (103,191 B) is a separate 4-key file (`_comment`, `_schema`, `providers`, `endpoints`) describing which API endpoints each provider supports. *(observed live)*
- **Schema — genuinely documented:** `model_prices_and_context_window.schema.json` (40,667 B), `"$schema": "https://json-schema.org/draft/2020-12/schema"`, title `LiteLLM model_prices_and_context_window.json`, with **`$defs.modelEntry` documenting 213 properties**. Top level has `sample_spec` (a documentation placeholder, *"not a real model and not schema-conformant"*) and `fallback_generalizations` (regex rules) — **both must be excluded from any parse**. *(observed live)*
- **Entry count: 4,479** (excluding `sample_spec`), across **138 distinct `litellm_provider` values**. *(observed live)*
- **Top providers by entry count:** `openrouter` 490, `fireworks_ai` 335, `azure` 305, `bedrock` 293, `bedrock_converse` 222, `fal_ai` 210, `openai` 209, `azure_ai` 142, `novita` 135, `deepinfra` 134, `vercel_ai_gateway` 99, `together_ai` 90, `perplexity` 85, `gemini` 83, `aihubmix` 72, `mistral` 67, `databricks` 62, `nebius` 61, `xai` 58. *(observed live)*
- **`mode` distribution:** `chat` 3,348, `image_generation` 408, `responses` 170, `embedding` 151, `audio_transcription` 93, `realtime` 52, `video_generation` 46, `audio_speech` 42, `completion` 36, `image_edit` 31, `rerank` 29, `search` 23, `ocr` 20, `evaluation` 16, `moderation` 3, `guardrail` 1, `vector_store` 1, `None` 9. *(observed live)*
- **The target fields, exactly as named in the schema** *(documented)*: `max_input_tokens` ("Maximum prompt/context tokens the model accepts"), `max_output_tokens` ("Maximum tokens the model can generate in one response"), `max_tokens` ("Legacy field: max output tokens if the provider specifies it, else max input tokens" — **do not use**), `supports_function_calling`, `supports_tool_choice`, `supports_vision`, `supports_image_input`, `supports_response_schema`, `supports_native_structured_output`, `input_cost_per_token`, `output_cost_per_token`, `cache_read_input_token_cost`, `cache_creation_input_token_cost`, `litellm_provider`, `mode`, `source` (URL of the provider page the entry came from), `supported_endpoints`, `supported_modalities`, `deprecation_date`, `rpm`, `tpm`.
- **Coverage over the chat/completion/responses subset (3,554 entries)** *(observed live, counted by me)*:
  | Field | Present / 3,554 |
  |---|---|
  | `max_input_tokens` | 3,367 (**95%**) |
  | `max_output_tokens` | 3,166 (**89%**) |
  | `input_cost_per_token` | 3,380 (**95%**) |
  | `output_cost_per_token` | 3,384 (**95%**) |
  | `supports_function_calling` | 2,601 (**73%**) |
  | `supports_tool_choice` | 2,272 (64%) |
  | `supports_response_schema` | 1,791 (**50%**) |
  | `supports_vision` | 1,500 (**42%**) |
  | `cache_read_input_token_cost` | 1,619 (46%) |
  | `cache_creation_input_token_cost` | 525 (15%) |
  | `supports_native_structured_output` | 123 (**3%**) |
  - **1,386 chat entries** have context + tools + vision all set.
- **⚠️ The trap:** `supports_vision` is present on only 42% of chat entries, and of those present, **1,761 are `true` and 460 are explicitly `false`** (over the full file). **Absence means "unknown", not "false".** If you map `supports_vision == false` → "no vision" and treat missing as false, you will wrongly mark ~2,000 models as text-only. Same pattern for every `supports_*` flag. This is the single biggest integration pitfall in this dataset.
- **Cadence — extremely active:** PyPI shows **913 releases**; latest `1.104.0` uploaded `2026-10-03`, `1.105.0rc1` on `2026-10-04`. Repo pushed `2026-10-06`. *(observed live)*
- **LICENSE:** **MIT**, `Copyright (c) 2023 Berri AI`, with the header noting *"All content that resides under the `enterprise/` directory … is licensed under the license defined in `enterprise/LICENSE`"*. The model-prices JSON is outside `enterprise/`, so **MIT applies**. *(observed live)*
- **Redistribution:** ✅ MIT — clean.

### 2.4 Vercel AI Gateway / AI SDK

- **URL:** `https://ai-gateway.vercel.sh/v1/models` — 462,112 B, **409 models** *(observed live)*
- **Auth: none** — documented (*"This endpoint requires no authentication and returns model IDs, context windows, pricing, and reasoning controls"*) and confirmed by a bare fetch. *(documented + observed live)*
- **Fields** *(observed live)*: `id`, `object`, `created`, `released`, `owned_by`, `name`, `description`, `context_window`, `max_tokens`, `type`, `zdr`, `no_training`, `tags[]`, `supported_specifications[]`, `modalities{input[],output[]}`, `supported_parameters[]`, `temperature`, `reasoning_options[]`, `knowledge`, `pricing{input, output}`.
- **Capability encoding:** via `tags` (e.g. `["reasoning","tool-use","structured-output"]`) and `supported_parameters` (`tools`, `tool_choice`, `response_format`, `structured_outputs`). I counted **189/409 with image input**. *(observed live)*
- **Extra endpoint:** `GET /v1/models/{creator}/{model}/endpoints` returns **per-provider pricing, supported parameters, uptime, throughput, and latency** for models served by multiple providers. *(documented)*
- **Provenance:** models.dev's `vercel:generate` script consumes this catalog. *(documented in models.dev sync.md)*
- **Repo:** `https://github.com/vercel/ai` — 27,129 stars, pushed `2026-10-06`. The `LICENSE` file text is **Apache License 2.0** (`Copyright 2023 Vercel, Inc.`), though the GitHub API reports `NOASSERTION` because the file lacks the standard header block. *(observed live)*
- **⚠️ License/ToS status: unclear.** I fetched `https://vercel.com/legal/terms` (803,995 B) and extracted 52,636 chars of text; searching for `scrap`, `crawl`, `redistribut`, `automated`, `data min` produced **zero matches**. So there is **no explicit anti-scraping clause like OpenRouter's** — but the AI Gateway `/v1/models` is a **service endpoint, not a licensed dataset**, and the Apache-2.0 license covers the `vercel/ai` **code**, not the catalog contents. *(observed live + inferred)*
- **Redistribution:** ⚠️ Probably tolerable for fetching at runtime; **not clearly licensed for bundling** a snapshot.

### 2.5 go-llm-specs — a Go-native registry (found via GitHub search; most directly relevant to llmio)

- **Repo:** `https://github.com/kingfs/go-llm-specs` — 5 stars, **Apache-2.0**, pushed `2026-10-05`. *(observed live)*
- **Consumption:** `go get github.com/kingfs/go-llm-specs`. The registry is **compiled into the binary** (`models_gen.go`, 987,494 B) — *"运行时只做内存查询，不访问网络"* (runtime queries are in-memory, no network). *(documented in README)*
- **Coverage: 581 models.** I counted `IDVal:` 581, `ContextLenVal:` 581, `MaxOutputVal:` 581, `CapFunctionCall` 384, `CapJsonMode` 409, `ModalityImageIn` 254, `AliasList` 581. *(observed live)*
- **Fields:** `ID`, `Name`, `Provider`, `Developer`, `OfficialURL`, `ModelCardURL`, `Description`, `DescriptionCN`, `Family`, `Series`, `Summary`, `Tags`, `ContextLength`, `MaxOutput`, `Features` (bitmask: `CapChat | CapFunctionCall | CapJsonMode | ModalityTextIn | ModalityTextOut | …`), `Aliases`. Query API: `Get(idOrAlias)`, `GetMany`, `Search`, `Query().Has(...).Provider(...).List()`, `KnownTags()`, `Card()`. *(documented + observed live)*
- **❌ No pricing.** Not a field in the schema.
- **Data sources & cadence:** *"OpenRouter 继续作为高覆盖率的主要发现入口，厂商官方页面和已订阅的官方 Hugging Face 组织用于补全和发现"* — OpenRouter is the primary discovery channel, supplemented by official pages and subscribed official HF orgs. **Daily GitHub Actions** makes **one** OpenRouter request plus full pagination over HF orgs. *(documented in README)*
- **Raw data available:** `data/models.json` (759,829 B — the cached OpenRouter upstream response), `models/**/*.yaml` (848 files, hand-maintained facts), `providers/` (54 files), plus `dist/codex/third-party-models.json`. *(observed live)*
- **⚠️ Risks:** only **5 stars** — a single-maintainer project with a real bus-factor risk. Also, its upstream is OpenRouter, so it carries the same chain-of-title concern (though you would be consuming *its* Apache-2.0 artifact, not scraping OpenRouter yourself).
- **Redistribution:** ✅ Apache-2.0.

### 2.6 modelscan/registry — best "merged with provenance" design, but stale and CC-BY

- **Repo:** `https://github.com/modelscan/registry` — 4 stars, **CC-BY-4.0**, pushed `2026-09-07`. Site: `https://modelscan.io/`. *(observed live)*
- **File:** `https://fastly.jsdelivr.net/gh/modelscan/registry@main/models.json` — 2,238,376 B. Shape: `{schema_version: 2, generated_at, count, models[]}`. **1,520 models.** *(observed live)*
- **Fields:** `id`, `model`, `alias_id[]`, `author`, `author_id`, `context_length`, `max_input_tokens`, `max_output_tokens`, `release_timestamp`, `last_updated`, `reasoning`, `tool_calling`, `input_modalities[]`, `output_modalities[]`, `endpoints[]`, `open_weights_url`, `deprecation`, `other_parameters`, and **`offers[]`** — one per source, each carrying `{source, currency, prices[{input,output}{amount,unit}], other_params}`. *(observed live)*
- **Coverage I counted (1,520 models):** `context_length` 1,106 (**73%**), `max_output_tokens` 937 (**62%**), `max_input_tokens` 814 (54%), `tool_calling` present on 1,201 with **669 `true`**, `input_modalities` **100%**, `offers` **100%**, image input **631**. *(observed live)*
- **Design strengths (c) inferred):** *"Facts vs offers"* — top-level fields are source-agnostic facts merged per-field, while commercial data lives in `offers[]` **with the originating source as provenance**. Two currencies (USD from OpenRouter/LiteLLM, CNY from Alibaba Bailian / Volcengine Ark) kept unconverted. Delisted models are marked `deprecation: {status: "delisted", since}` and **never deleted**. *(documented in README)*
- **Cadence:** commits are **daily** (`data: sync models.json (1520 models)` on 2026-09-06 ×2 and 2026-09-07). **Last commit `2026-09-07T23:05:10Z`** — roughly **one month stale** as of 2026-10-06. *(observed live)*
- **⚠️ Staleness verdict:** the sync is daily but the last observed commit is ~29 days old. Either the repo went quiet or the model set stopped changing; either way it is **materially less fresh than models.dev or LiteLLM**.
- **⚠️ Chain of title:** it merges OpenRouter + LiteLLM + Bailian + Volcengine. The OpenRouter component inherits OpenRouter's anti-scraping ToS.
- **Redistribution:** ⚠️ CC-BY-4.0 — **allowed, including commercially**, but **requires attribution** to *modelscan registry* (`https://modelscan.io/`). That means a visible attribution notice in your self-hosted app.

### 2.7 Helicone model registry — richest schema, worst packaging

- **Repo:** `https://github.com/Helicone/helicone` — 6,199 stars, **Apache-2.0**, repo pushed `2026-09-16`. *(observed live)*
- **Location:** `packages/cost/models/` — *"The model registry provides a comprehensive, type-safe database of AI models with their metadata, pricing, and endpoint configurations. It supports O(1) lookups."* Structure: `authors/{author}/{model}/{models.ts, endpoints.ts}`, plus `registry.ts`, `build-indexes.ts`, `types.ts`. **16 authors** (`anthropic, openai, google, meta-llama, mistral, amazon, microsoft, nvidia, deepseek, qwen, xai, moonshotai, perplexity, alibaba, zai, baidu`) and **25 provider modules**. *(documented + observed live)*
- **⚠️ Format: TypeScript source, not JSON.** E.g. `packages/cost/models/authors/openai/gpt-4o/models.ts` exports `export const models = { "gpt-4o": { name, author, description, contextLength: 128000, maxOutputTokens: 16384, created, modality: {inputs: ["text","image"], outputs: ["text"]}, tokenizer: "GPT" }, … } satisfies Record<string, ModelConfig>`. There is **no published JSON artifact**. *(observed live)*
- **Fields available** (from `models/types.ts`) *(observed live)*:
  - `ModelConfig`: `contextLength`, `maxOutputTokens`, `modality{inputs,outputs}`, `tokenizer`, `created`, `name`, `author`, `description`
  - `ModelProviderConfig`: `pricing[]` (`{threshold, input, output, cacheMultipliers{cachedInput, write5m, write1h}, cacheStoragePerHour, thinking, request, image/audio/video/file: ModalityPricing, web_search}`), `contextLength`, `maxCompletionTokens`, `supportedParameters[]`, `rateLimits{rpm,tpm,tpd}`, `ptbEnabled`, `endpointConfigs`
  - `StandardParameter` union **explicitly includes** `tools`, `tool_choice`, `functions`, `function_call`, `response_format`, `json_mode`, `structured_outputs` — so tools/structured-output are directly derivable
- **❌ No public API:** `https://api.helicone.ai/v1/model/registry` → **502**, `https://api.helicone.ai/v1/models` → **502**, `https://www.helicone.ai/api/models` → **404**. The npm package `@helicone-package/cost` → **404** (not published); `@helicone/cost` → 404. *(observed live)*
- **⚠️ Staleness:** the last commits touching `packages/cost` were `2026-04-04` (*"fix: fix anthropic response conversion token mappings"*), `2026-03-26`, `2026-03-19` — i.e. the **cost/model registry has not been touched in ~6 months**, even though the repo as a whole was pushed `2026-09-16`. Also note `packages/cost/README.md` documents a **v2 registry** while `providers/*/index.ts` files still carry the *"DO NOT EDIT THIS FILE UNLESS IT IS IN /costs"* header, suggesting an in-progress migration. *(observed live)*
- **⚠️ Extra:** a `packages/buildPricesOpenRouter.py` script in the repo fetches `https://openrouter.ai/api/v1/models` and maps it into Helicone's cost format — so parts of Helicone's data are **also derived from OpenRouter**. *(observed live)*
- **Redistribution:** ⚠️ Apache-2.0 permits it, but you would be **transpiling TypeScript at build time** and inheriting the OpenRouter derivation.

### 2.8 Portkey model catalog — **not usable for this purpose**

- **Repo:** `https://github.com/Portkey-AI/gateway` — 13,126 stars, **MIT**, repo pushed `2026-05-25` (**~4.5 months stale**). *(observed live)*
- **Files:** `src/data/models.json` (325,524 B) and `src/data/providers.json` (22,455 B). *(observed live)*
- **⚠️ Content — the decisive problem:** `models.json` is `{object: "list", version: "1.0.0", data: [...]}` with **1,928 models**, but each entry has **only four keys**: `id`, `object`, `provider: {id}`, `name`. There is **no context window, no max output, no tool/vision/structured-output flag, and no pricing**. I verified this by unioning the keys across all 1,928 entries. *(observed live)*
- **API:** `https://api.portkey.ai/v1/models` → **401** (auth required). *(observed live)*
- **Staleness verdict:** repo pushed `2026-05-25`; last commits were routine (`formatting`, `redact provider options in logs`), no recent model-data churn. *(observed live)*
- **Verdict:** ❌ **Excluded.** It is a name/id catalog only and cannot populate any of the six target fields.

### 2.9 llm-prices.com — pricing only, **and unlicensed**

- **URLs:** `https://www.llm-prices.com/current-v1.json` (29,519 B) and `https://www.llm-prices.com/historical-v1.json`. Note `https://www.llm-prices.com/api/current-v1.json` → **404**. *(observed live)*
- **Shape:** `{updated_at: "2026-09-29", prices: [{id, vendor, name, input, output, input_cached}]}` — **170 prices across 12 vendors** (openai 61, google 26, xai 26, anthropic 21, mistral 14, meta-ai 5, moonshot-ai 5, amazon 4, deepseek 4, qwen 2, minimax 1, typesafe 1). Values are **USD per million tokens**. *(observed live)*
- **Repo:** `https://github.com/simonw/llm-prices` — 190 stars, data in `data/{vendor}.json`, built by `scripts/build.py` to Cloudflare Pages. By Simon Willison. *(observed live)*
- **❌ Fields:** pricing only — **no context window, no max output, no tools, no vision, no structured output.** *(observed live)*
- **⚠️ LICENSE: none.** `LICENSE` → 404, `LICENSE.md` → 404, `package.json` → 404. The GitHub repo license field is `null`. The site's HTML contains **no** license or copyright statement (I searched the rendered text for `licen`, `copyright`, `©`, `CC BY`, `MIT` — no matches). **Default copyright applies: no license is granted, so redistributing or bundling the data is not permitted.** *(observed live)*
- **Redistribution:** ❌ **Blocked by absence of a license.**
- **Verdict:** fails on both field coverage and licensing.

### 2.10 modelfax — good design, negligible coverage, high abandonment risk

- **URLs:** `https://bytebrujo.github.io/modelfax/data/{openai,anthropic,google}.json` — verified 200 (5,429 / 13,424 / 10,692 B). Schema at `/schema/model.schema.json`. *(observed live)*
- **⚠️ Coverage: 25 models total** (openai **5**, anthropic **11**, google **9**). *(observed live)*
- **Fields:** `context_window`, `max_output_tokens`, `modalities{input,output}`, `pricing{input_per_mtok, output_per_mtok, cached_input_per_mtok, batch_input_per_mtok, batch_output_per_mtok}`, `dates{released,deprecated,retired}`, `status`, `sources[]`, `last_verified`, `notes`. *(observed live)*
- **⚠️ README self-admits the weakness:** *"Context windows, max output tokens and modalities are only published in a scrapeable table by one of the three providers, so those three fields are **maintained by hand** and everything else is scraped."* *(documented)*
- **Cadence:** *"A scheduled job scrapes every provider daily at 06:00 UTC."* *(documented)*
- **⚠️ Staleness / abandonment risk (c) inferred):** `MAINTENANCE_LOG.md` contains exactly **one entry — "run 0 (build)" dated 2026-09-04** — describing the entire project being built in a single session with 4 human interventions (including a locked GitHub billing account). Last commit `2026-09-04T08:39:52Z`, i.e. **~1 month with no activity**. **1 star.** Also `modelfax.dev` **does not resolve** (DNS failure) — only the `github.io` URL works. *(observed live)*
- **Licenses:** MIT (code) / **CC-BY-4.0** (`data/`), attribute "modelfax". *(documented)*
- **Verdict:** ❌ 25 models is not meaningful coverage for a multi-provider proxy.

### 2.11 InterwebAlchemy/model-metadata-central — good schema, **unlicensed**

- **Repo:** `https://github.com/InterwebAlchemy/model-metadata-central` — 6 stars, pushed `2026-09-18`. *(observed live)*
- **Layout:** 165 files — `models/*.yaml` (~100+), `model-metadata.schema.json` (8,105 B), `provider.schema.json` (1,802 B), `packages/` (TypeScript + Python), 8 release tags (`typescript-v0.5.0` …). *(observed live)*
- **Fields** (from `models/claude-sonnet-5.yaml`) *(observed live)*: `model_id`, `model_name`, `model_provider`, `model_description`, `model_info`, `model_type`, `context_window`, `max_tokens`, `cost_per_million_tokens{input, cached_input, cache_write_input, output}`, `tuning: [function, reasoning]`, `input_type: [text, image]`, `output_type`, `providers[]{provider_id, model_id_on_provider, model_info}`.
- **⚠️ LICENSE: none detected.** `LICENSE` → 404. GitHub API reported no license. *(observed live)*
- **Redistribution:** ❌ **No license granted.**
- **Verdict:** ❌ blocked on licensing; also only ~100 models.

### 2.12 OpenRouter-derived mirrors — avoid

- Example: `https://github.com/kj-9/openrouter-models-json` — 3 stars, **no license**, pushed `2026-10-05`, README 427 B, and jsDelivr reports `"tags": {}, "versions": []` (no tagged releases). *(observed live)*
- **Verdict:** ❌ These add **no license clarity** (typically unlicensed), inherit OpenRouter's ToS problem transitively, and have no maintenance guarantee. Prefer consuming OpenRouter directly, or better, models.dev.

### 2.13 Ollama model library — **no remote capability/context metadata**

- **Registry manifest** `https://registry.ollama.ai/v2/library/llama3.2/manifests/latest` → 200, but the body is a plain **OCI/Docker manifest**: `{schemaVersion, mediaType, config{mediaType,digest,size}, layers[{mediaType: "application/vnd.ollama.image.model", digest, size}, …template, …license, …params]}`. **It contains only blob digests and sizes — no capabilities, no context length.** *(observed live)*
- **`https://ollama.com/api/tags`** → 200 JSON, `{models:[{name, model, modified_at, size, digest, details{parent_model, format, family, families, parameter_size, quantization_level}}]}`. **No capabilities, no context length.** *(observed live)*
- **`https://ollama.com/library/llama3.2`** (HTML) *does* render `"2.0GB · 128K context window · Text · 2 years ago"` and a `tools` badge — **scrapeable but not a documented API**, and it is a page, not a data source. *(observed live)*
- **Local Ollama only:** `ollama/api/types.go` defines `ShowResponse{ …, ModelInfo map[string]any, Capabilities []model.Capability, … }` and `ProcessModelResponse{ …, ContextLength int, … }`. So **`POST /api/show` on a running local Ollama** returns `capabilities` and context — but this requires a local Ollama instance and does not scale to hosted providers. *(observed live, source inspection)*
- **License:** `ollama/ollama` is MIT (182,276 stars, pushed `2026-10-06`). *(observed live)*
- **Verdict:** ❌ **Not a viable remote metadata source** for a proxy. Useful only if llmio wants to introspect a *local* Ollama backend, in which case `/api/show` is the right call.

### 2.14 HuggingFace `config.json` — usable fallback for open-weights, with a serious caveat

- **Raw file:** `https://huggingface.co/{repo}/raw/main/config.json` — **200 for public repos, 401 for gated**. `meta-llama/Llama-3.2-3B-Instruct` returned `401 "Access to model … is restricted. You must have access to it and be authenticated to access it."` *(observed live)*
- **⚠️ The API is NOT a substitute for gated repos:** `https://huggingface.co/api/models/meta-llama/Llama-3.2-3B-Instruct` returns **200** even when gated, and does include a `config` key — **but the `config` object is truncated** to `{architectures, model_type, tokenizer_config}` with **no `max_position_embeddings`**. So for gated models you get nothing useful unauthenticated. *(observed live)*
- **Observed values** *(observed live)*:
  | Repo | `max_position_embeddings` | `rope_scaling` / `sliding_window` |
  |---|---|---|
  | `Qwen/Qwen2.5-7B-Instruct` | 32,768 | `sliding_window: 131072` |
  | `mistralai/Mistral-7B-Instruct-v0.3` | 32,768 | `sliding_window: null` |
  | `Qwen/Qwen3-8B` | 40,960 | `rope_scaling: null` |
  | `unsloth/Llama-3.2-3B-Instruct` | **131,072** | `rope_scaling: {original_max_position_embeddings: **8192**, factor: 32, rope_type: "llama3"}` |
- **⚠️ The caveat you asked about — confirmed:** `max_position_embeddings` is **not** a reliable proxy for the *served* context window. Llama-3.2-3B reports `131072`, but `rope_scaling.original_max_position_embeddings` is **`8192`** — the native window is 8K and 128K is only reachable via RoPE scaling. Providers cap far lower (Ollama advertises 128K, but many hosts serve 8K–32K). You must read `rope_scaling.original_max_position_embeddings` when present, and even then it is the *architectural* maximum, not the *served* limit. *(observed live + inferred)*
- **Rate limits** *(documented at `https://huggingface.co/docs/hub/rate-limits`, values "in September '25", per 5-minute windows)*: Anonymous (per IP) **500 API / 3,000 resolvers / 100 pages**; Free user **1,000 / 5,000 / 200**; PRO **2,500 / 12,000 / 400**; Team **3,000 / 20,000 / 400**; Enterprise **6,000 / 50,000 / 600**. Standard `RateLimit` / `RateLimit-Policy` headers, 429 on exceed.
- **No bulk endpoint:** there is no "give me all configs" call — you must fetch per repo. *(inferred)*
- **Licenses:** per-repo (varies; some are gated or non-commercial).
- **Verdict:** ⚠️ **Viable only as a last-resort enrichment step** for open-weights models that no aggregated source covers, and only with the `rope_scaling` correction applied.

### 2.15 Sources that turned out **not to exist** or not to be registries

| Named source | Finding | Evidence |
|---|---|---|
| **tokscale** | `https://github.com/junhoyeo/tokscale` — 5,627 stars, MIT, pushed `2026-10-05`; npm `tokscale` 4.18.0 (`2026-10-05`, alias for `@tokscale/cli`). It is *"Track token usage across AI coding agents from your terminal… Global leaderboard"* — a **usage/cost tracker, not a model-metadata registry**. PyPI `tokscale` → 404. | observed live |
| **aiprice** | npm `aiprice` → **404**; PyPI `aiprice` → not found. GitHub search surfaced only unrelated projects (an AliExpress scraper, a 1-star `makalin/AIPriceIndex` GPU price/perf tracker last pushed `2025-02-05`). **No usable LLM metadata dataset.** | observed live |
| **llm-model-info** | PyPI `llm-model-info` → **404**; npm `llm-model-info` → **404**. **Does not exist** as a published package. | observed live |
| **"awesome-llm style JSON registries"** | GitHub search (`llm+models+json+pricing`, `llm+pricing+json`, `model+metadata+llm+registry`) surfaced mostly single-digit-star, unlicensed, brand-new repos (e.g. `BenchGecko/llm-pricing` 3★, `llerandi/llm-price-tracker` 3★, `Leolionel221/aicostcalc` 2★, `wordenneapolitan768/llm-pricing` 1★) with near-identical "300+ models in a single JSON" descriptions — a cluster that looks **LLM-generated/SEO-spam**. `cheahjs/free-llm-api-resources` → **404** (repo gone/renamed). The only credible finds were `kingfs/go-llm-specs`, `modelscan/registry`, `bytebrujo/modelfax`, and `InterwebAlchemy/model-metadata-central`, all covered above. | observed live |

---

## 3. Ranked shortlist for auto-filling capabilities + context in a self-hostable Go proxy

### 🥇 1. models.dev — primary source
**Why:** the only source that is simultaneously **(a) MIT-licensed**, **(b) enormous** (226 providers / 8,389 models — 2× LiteLLM's provider count), **(c) purpose-built** for exactly this (`tool_call`, `structured_output`, `modalities.input`, `limit.context`, `limit.output`, `cost.*` as first-class booleans/ints), and **(d) genuinely live** (hourly CI; commit within minutes of my fetch). A single 5.3 MB GET with an `ETag` gives you the whole world.
**Tradeoffs:** `limit.input` is only 17% populated (use `limit.context`); `structured_output` 56% and `tool_call` 87% — **missing ≠ false**. No per-provider endpoint, so you must fetch and filter the 5.3 MB blob (trivial in Go, but not lazy). `model-schema.json` is a model-ID enum, not a field schema, so you must hand-write your Go structs. Consuming it also means you are downstream of an OpenRouter-derived catalog (see #3).

### 🥈 2. LiteLLM `model_prices_and_context_window.json` — primary complement
**Why:** MIT, 4,479 entries / 138 providers, and **by far the deepest capability flags** — `supports_function_calling`, `supports_vision`, `supports_response_schema`, `supports_tool_choice`, `supports_pdf_input`, `supports_audio_input`, `supports_prompt_caching`, plus a **documented 213-property JSON Schema**. It is the best source for the *provider-specific* question "does this provider's deployment of this model support tools?", which models.dev answers at a coarser grain. Extremely active (913 PyPI releases).
**Tradeoffs:** the **absence-vs-false trap** is severe — `supports_vision` is present on only 42% of chat entries and 460 entries are explicitly `false`; naive mapping will mislabel ~2,000 models. `supports_native_structured_output` is only 3% populated (use `supports_response_schema` instead). You must skip `sample_spec` and `fallback_generalizations`. Provider-id namespacing (`litellm_provider`) does not map 1:1 to models.dev ids — I measured only **32 overlapping provider ids** out of 138 vs 226, so **joining the two datasets requires a hand-maintained alias table**.

### 🥉 3. OpenRouter `/api/v1/models` — best freshness, **legal risk**
**Why:** 464 models, no auth, `context_length` on **100%**, `top_provider.max_completion_tokens` on 98%, and `supported_parameters[]` cleanly encodes `tools` / `structured_outputs` / `response_format`. It is also the *upstream* that models.dev, go-llm-specs, modelscan, and Helicone all derive from — so fetching it directly gives you the least-latency version of the same truth.
**Tradeoffs — and this is the key legal finding:** OpenRouter's ToS (**Aug 31, 2026**) explicitly forbids *"automated technology to scrape or copy any information on the Site"* and using the service to *"develop a competing service."* **A self-hosted multi-provider proxy is plausibly a competing service, and bundling a scraped snapshot is squarely "copying information."** Recommendation: **do not scrape-and-bundle OpenRouter directly.** If you want its data, take it transitively from an MIT/Apache-licensed aggregator (models.dev or go-llm-specs) so you are consuming *their* artifact, and accept the residual chain-of-title ambiguity. Also: no max-*input* field distinct from context, and 8 models lack a max-output value.

### 4. Vercel AI Gateway `/v1/models` — solid secondary, licensing murky
**Why:** 409 models, **no auth**, clean `context_window` + `max_tokens` + `modalities` + `tags` (`tool-use`, `structured-output`, `reasoning`) + `pricing`, plus a per-provider `/endpoints` call with uptime/throughput. Vercel's ToS contained **no anti-scraping clause** in the 52k chars I extracted — a materially better position than OpenRouter.
**Tradeoffs:** it is a **service endpoint, not a licensed dataset**; Apache-2.0 covers the `vercel/ai` code, not the catalog. Safest as a **runtime fetch** rather than a bundled snapshot. Capabilities are tag/parameter-derived rather than explicit booleans, so you need a mapping layer.

### 5. go-llm-specs — best *architectural* fit for a Go project, smallest coverage
**Why:** **Apache-2.0**, **Go-native** (`go get`), **compiled into the binary with zero runtime network calls**, and **581/581** models have both `ContextLength` and `MaxOutput` — the highest context/output completeness of any source here. Exposes `CapFunctionCall` (384), `CapJsonMode` (409), `ModalityImageIn` (254), plus aliases and bilingual descriptions. Daily sync.
**Tradeoffs:** only **581 models** (vs 8,389), **no pricing at all**, and **5 stars / single maintainer** — a genuine bus-factor risk. Also OpenRouter-derived upstream. Best used as a **vendored fallback/seed** or as a design reference for how to shape your own Go registry, not as the sole source.

### Honorable mentions (not top-5)
- **modelscan/registry** — the *best data design* (facts vs per-source `offers[]` with provenance, stable identity via `alias_id`, delisting instead of deletion), CC-BY-4.0 (bundleable with attribution), 1,520 models. **But ~1 month stale** (last commit `2026-09-07`) and only 73% context / 62% max-output. Worth a second look if it resumes daily syncing.
- **Helicone** — the **richest per-provider schema** (pricing tiers, cache multipliers, `rateLimits{rpm,tpm,tpd}`, `supportedParameters` including `tools`/`structured_outputs`, 25 providers). **But it is TypeScript source with no JSON artifact and no API** (all three API paths returned 502/404, npm 404), and `packages/cost` has been **untouched since 2026-04-04**. High effort, stale, and partly OpenRouter-derived.

### Explicitly **excluded**
- **Portkey** — 1,928 models but only `{id, object, provider.id, name}`; **cannot fill a single target field**; API requires auth (401); repo stale since `2026-05-25`.
- **llm-prices.com** — pricing only, **and no LICENSE file** → redistribution not permitted.
- **modelfax** — 25 models, one-session project, 1 star, ~1 month idle, DNS dead.
- **InterwebAlchemy/model-metadata-central** — **no LICENSE file**.
- **OpenRouter-derived mirrors** (e.g. `kj-9/openrouter-models-json`) — unlicensed, no releases, inherit the ToS problem.
- **Ollama registry** — manifests contain only blob digests; no capability/context remotely. Local `/api/show` only.
- **HuggingFace `config.json`** — viable only as a **last-resort enrichment** for open-weights, gated repos are 401, and `max_position_embeddings` must be corrected against `rope_scaling.original_max_position_embeddings`.
- **tokscale / aiprice / llm-model-info** — not metadata registries, or do not exist.

---

## 4. Recommended combination and the licensing bottom line

**Suggested strategy (c) inferred):**
1. **models.dev `api.json`** as the backbone — MIT, widest provider coverage, hourly freshness.
2. **LiteLLM** as the capability/precision layer, joined via a **hand-maintained provider-alias table** (only 32/138 ids overlap, so budget real effort here), with **tri-state logic** (`true` / `false` / unknown) so missing flags never become `false`.
3. **go-llm-specs** vendored as an offline seed/fallback if you want the registry compiled into the binary with no runtime network dependency.
4. **Vercel AI Gateway** as an optional runtime cross-check — no auth, and its ToS has no explicit anti-scraping clause.
5. **HuggingFace `config.json`** only for open-weights models that all of the above miss, applying the `rope_scaling` correction.

**Licensing bottom line:**
- ✅ **Clean to bundle:** models.dev (MIT), LiteLLM (MIT), go-llm-specs (Apache-2.0).
- ⚠️ **Bundleable with an attribution notice:** modelscan/registry, modelfax (both CC-BY-4.0).
- ⚠️ **Fetch-at-runtime, unclear for bundling:** Vercel AI Gateway, Helicone (Apache-2.0 code, but TS-source packaging and OpenRouter-derived content).
- ❌ **Do not bundle or scrape:** **OpenRouter** (ToS explicitly bans automated copying and competing services — the single most important restriction found), llm-prices.com and InterwebAlchemy (no license granted), Portkey (useless anyway).
