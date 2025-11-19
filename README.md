# slmcache

Small, sidecar-friendly cache for LLM prompt/response pairs with semantic retrieval.

## What it does
- Stores prompts, responses, and optional metadata using the in-memory vector store (swap in your own adapter if needed) and lets you query/update that metadata directly via the API.
- Generates embeddings via a co-located Small Language Model (SLM) to support vector similarity search, with a token-based fallback for safety.
- Exposes a minimal HTTP API that higher-level orchestration can use to read, mutate, and search cached entries.
- Never calls a remote LLM – it only returns candidates for a co-located SLM/LLM orchestrator to evaluate.

## Why SLM-backed caching improves latency
- **Short-circuit generation**: When a request paraphrases an already-solved question, the embedded prompt will surface the cached answer, allowing your orchestrator to skip a full LLM invocation.
- **Sidecar locality**: Run slmcache and the SLM in the same pod/VM so embedding calls stay on-host. Cached hits become sub-10 ms lookups compared to multi-hundred-ms round trips to a remote model.
- **Prompt warm-up**: Pre-populate recurring intents (FAQs, onboarding flows, canned responses). The orchestrator can check slmcache first and only escalate to the heavyweight LLM on a miss, dramatically reducing median latency and cost.
- **Observability hook**: Attach metadata that records which upstream model produced a response, temperature, or guard-rail signals. On a miss, the orchestrator can decide whether to regenerate, fine-tune, or add new prompts back into the cache.

## Quick start (binary)
1. Build the service:
	 ```bash
	 go build -o bin/slmcache ./cmd/slmcache
	 ```
2. Ensure an embedding-capable SLM is reachable. By default we expect a local [Ollama](https://ollama.com) instance (slmcache will automatically pull the embedding model if it isn't present, but pre-pulling avoids the first-start delay). Make sure you're running Ollama `0.1.25` or newer so the embeddings API is available:
	 ```bash
	 # Install model once
	 ollama pull nomic-embed-text
	 # (Optional) run Ollama if not already running
	 ollama serve
	 ```
3. Run slmcache (persists `cache.db` in the current directory and connects to Ollama at `http://localhost:11434`):
	 ```bash
	 ./bin/slmcache
	 ```

## Quick start (Docker / Compose)
- Multi-stage Docker build produces a compact image:
	```bash
	docker build -t slmcache:local .
	```
- Run with the default Ollama URL:
	```bash
	docker run --rm \
		-p 8080:8080 \
		-v "$PWD/cache.db":/data/cache.db \
		-e SLC_DB_PATH=/data/cache.db \
		-e SLM_OLLAMA_URL=http://host.docker.internal:11434 \
		slmcache:local
	```
- Or use docker compose (host networking on Linux is handled automatically via the Makefile helpers):
	```bash
	docker compose up --build
	```

## Makefile helpers
The repo ships with a lightweight workflow:

| Target | Description |
| --- | --- |
| `make serve` | Build & run the container, auto-detecting network mode to reach Ollama. |
| `make serve-host` | Force host networking (useful if you manually run Ollama on localhost). |
| `make stop` | Tear down compose services. |
| `make wait-ready` | Poll `/search` until the service is up (used by other targets). |
| `make e2e` | Run the in-process Go e2e tests against an embedded server. |
| `make e2e-test` | Spin up the container and run the external e2e test that requires a real Ollama backend (the target now fails fast if `SLM_OLLAMA_URL` is unreachable by probing `GET $SLM_OLLAMA_URL/api/version`). |

> ℹ️ `make e2e-test` requires `ollama pull nomic-embed-text` to be completed on the host so the embeddings endpoint is available.

## HTTP API Surface
- `POST /entries` — create `{prompt, response, metadata?}` entry. Returns the stored object with ID.
- `GET /entries?metadata.tag=value` — list entries filtered by metadata. Use `metadata.<key>=value` or repeated `metadata=key:value` query params to AND multiple filters. Omitting filters returns every entry.
- `PUT /entries/{id}` — update an entry (re-embeds the prompt).
- `GET /entries/{id}` — fetch a single entry.
- `PATCH /entries/{id}/metadata` — merge or replace metadata in-place without re-embedding. Send `{ "metadata": {...}, "replace": false }` to merge, or `replace: true` to fully overwrite.
- `DELETE /entries/{id}/metadata/{key?}` — clear all metadata (no key) or delete a specific key.
- `DELETE /entries/{id}` — remove an entry and its vector.
- `GET /search?q=...&limit=10` — semantic search. Apply the same metadata filters as `/entries` by appending `metadata.<key>=value` query params. Results are filtered by similarity threshold (defaults to `0.8` when Ollama is active, configurable via `SLM_MIN_SCORE`).
- `GET /slm-backend` — returns `{"backend": "ollama"|"mock"}` so automation can verify which SLM is in use.

Metadata filters always use AND semantics. Values are matched against the string form of the stored metadata, so numbers can be filtered with `metadata.score=42` and booleans with `metadata.active=true`.

> ℹ️ Entries automatically expire after `SLC_ENTRY_TTL` (24 hours by default). Expired entries are never returned from the API and are removed by a background janitor.

## Configuration
Environment variables control embedding behavior:

| Variable | Default | Purpose |
| --- | --- | --- |
| `SLM_BACKEND` | `ollama` | Choose between `ollama` and `mock`. |
| `SLM_OLLAMA_URL` | `http://localhost:11434` | Endpoint for the Ollama embeddings API. Adjust when running in containers (Makefile manages host networking automatically). |
| `SLM_OLLAMA_MODEL` | `nomic-embed-text` | Ollama model used for embeddings. The server checks and pulls this model automatically when `SLM_BACKEND=ollama`. Requires Ollama version ≥ `0.1.25`. |
| `SLM_REQUIRE_OLLAMA` | `0` | When set to `1`, startup panics if Ollama is unreachable (used by CI/e2e). |
| `SLM_MIN_SCORE` | auto | Override similarity threshold (set explicitly to change hit sensitivity). |
| `SLC_DB_PATH` | `./cache.db` | Persistence file path (used by the default on-disk store). |
| `SLC_ENTRY_TTL` | `24h` | Time-to-live for cached entries. Older results are treated as misses and purged automatically. Set to `0` to disable expiration. |
| `SLC_PURGE_INTERVAL` | `1m` | How often the background janitor scans for expired entries. Increase for quieter deployments. |

## Running tests
- Standard Go unit tests:
	```bash
	go test ./...
	```
- Embedded e2e tests (use mock fallback when Ollama is unavailable):
	```bash
	make e2e
	```
- External e2e tests (hits the running container and verifies real semantic retrieval). Requires the Ollama model:
	```bash
	make e2e-test
	```

## Developer tips for latency-sensitive pipelines
- Deploy slmcache as a sidecar with your inference service. Route every inbound user prompt through slmcache first; reuse cached responses when similarity ≥ configured threshold.
- Periodically sync telemetry back into the cache (e.g., successful completions) to keep popular prompts warm.
- Use `metadata` to store runtime hints: the generating model, temperature, grounding sources, or cache invalidation timestamps. Your orchestrator can inspect this metadata to decide whether a cached answer is still fresh enough to return instantly.
- Combine the `/slm-backend` endpoint with health checks to ensure the embedding subsystem is operational before routing production traffic.
- See `docs/EXAMPLES.md` for a Go chatbot sample that demonstrates the cache-hit workflow.

## Roadmap & contributions
- The in-memory vector store keeps dependencies minimal. To integrate with an external vector DB, implement the `store.Store` interface in `internal/store` and wire it into `cmd/slmcache`.
- Contributions that keep the HTTP API stable and preserve the “co-located SLM” design principle are welcome.

