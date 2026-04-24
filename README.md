# agent-service

A Go microservice that manages agent sessions and streaming runs.

## Prerequisites

- Go 1.22+
- Docker & Docker Compose
- PostgreSQL (or use docker-compose)

## Quick Start

### With Docker Compose

```bash
docker-compose up --build
```

### Local Development

1. Start Postgres:
   ```bash
   docker-compose up -d postgres
   ```

2. Run migrations:
   ```bash
   export DATABASE_URL=postgres://agent:agent@localhost:5432/agentdb?sslmode=disable
   make migrate
   ```

3. Start the service:
   ```bash
   make run
   ```

## Environment Variables

| Variable          | Default | Description                                                              |
|-------------------|---------|--------------------------------------------------------------------------|
| `DATABASE_URL`    | —       | Postgres connection string                                               |
| `PORT`            | `8080`  | HTTP listen port                                                         |
| `LOG_LEVEL`       | `info`  | Log level (`debug`, `info`, `warn`)                                      |
| `LLAMA_URL`       | —       | Base URL of a single llama.cpp / OpenAI-compatible model server          |
| `LLM_NODES`       | —       | Comma-separated list of llm-service node URLs (takes precedence over `LLAMA_URL` when set) |
| `AGENT_MAX_STEPS` | `10`    | Maximum number of agent reasoning steps per run                          |
| `API_KEY`         | —       | When set, enables `X-API-Key` authentication on all endpoints except `/health` and `/metrics` |
| `MCP_ENDPOINT`    | —       | When set, enables the MCP tool runner targeting the given server URL     |

## Makefile Targets

| Target    | Description                         |
|-----------|-------------------------------------|
| `build`   | Compile binary to `bin/agent-service` |
| `test`    | Run all tests                       |
| `run`     | Run service locally                 |
| `lint`    | Run `go vet`                        |
| `migrate` | Apply DB migrations                 |

## API

### Health check
```
GET /health
```
Returns `{"status":"ok"}` with HTTP 200. Always accessible even when API key
authentication is enabled. Suitable for use as a liveness probe.

### Metrics
```
GET /metrics
```
Returns a JSON object with service counters:
```json
{
  "total_requests":           42,
  "total_runs":                8,
  "failed_runs":               1,
  "active_requests":           2,
  "tool_calls_total":          5,
  "approval_requests_total":   1,
  "backend_selections_total":  3,
  "runs_completed":            7,
  "run_latency_total_ms":   1540
}
```
Always accessible even when API key authentication is enabled.

### Create Session
```
POST /sessions
Content-Type: application/json

{"name": "my session", "description": "optional"}
```

### Start a Run (SSE streaming)
```
POST /sessions/{sessionID}/runs
Content-Type: application/json

{"prompt": "your prompt here"}
```

### Stream Run Events
```
GET /sessions/{sessionID}/runs/{runID}/events
```

### Approval endpoints

| Method | Path                         | Description                         |
|--------|------------------------------|-------------------------------------|
| GET    | `/approvals/{id}`            | Get the current state of an approval |
| POST   | `/approvals/{id}/approve`    | Approve a pending tool call          |
| POST   | `/approvals/{id}/deny`       | Deny a pending tool call             |

### Run inspection endpoints

| Method | Path                  | Description                                               |
|--------|-----------------------|-----------------------------------------------------------|
| GET    | `/runs/{runID}`       | Full run record (status, model backend, tool calls, etc.) |
| GET    | `/runs/{runID}/steps` | Ordered list of agent reasoning steps for replay          |

## SSE Event Types

- `run.created` – run record created
- `run.in_progress` – processing started
- `run.model_selected` – model backend selected for the run
- `run.step` – intermediate step (one per agent reasoning step)
- `run.tool_call` – tool invoked by the agent
- `run.approval_requested` – tool call is awaiting human approval
- `run.paused` – run suspended pending an external decision
- `run.completed` – run finished successfully
- `run.failed` – run failed

## Authentication

Set the `API_KEY` environment variable to enable `X-API-Key` header authentication:

```bash
export API_KEY=my-secret-key
```

Clients must then supply the header on every protected request:

```
X-API-Key: my-secret-key
```

The `/health` and `/metrics` endpoints are always accessible without authentication.

## Model Routing

The `internal/model/router` package provides a `Router` that dispatches
`model.Request` calls to different back-end providers based on configurable
prefix rules.  Rules are evaluated in order; the first matching rule wins.
If no rule matches, the configured default provider is used.

```go
r := router.New(defaultProvider)
r.AddProvider("llama", llama.New(cfg.LlamaURL))
r.AddRule(router.Rule{Prefix: "llama", Provider: "llama"})
```

Set the `Model` field on a `model.Request` to select a provider:

```go
req := model.Request{Model: "llama-3", Messages: messages}
resp, err := r.Complete(ctx, req)
```

## Multi-node llm-service Pool

Set `LLM_NODES` to a comma-separated list of llm-service base URLs to treat
multiple backends as a shared inference pool:

```bash
export LLM_NODES=http://node1:8080,http://node2:8080,http://node3:8080
```

The `internal/model/registry` package manages the node registry and pool:

- **`Registry`** tracks the health of each node.  Nodes begin healthy; any
  failed request marks the offending node as unhealthy so it is skipped by
  subsequent requests.  Calling `MarkHealthy` restores a node.
- **`Pool`** implements `model.Provider` and picks the first healthy node that
  supports the requested model name on every call.  When a node returns an
  error it is automatically marked as unhealthy.

Each `NodeConfig` may declare a `Models` slice restricting which model names
the node accepts.  An empty slice means the node accepts any model.

The `Model` field on a `model.Request` is forwarded unchanged to the chosen
backend.  When the field is empty the llama adapter defaults to `"local"`.

## Kulrs Automation Caller

The `POST /internal/kulrs/palette` endpoint provides a first-class integration
path for the Kulrs automation system.  It accepts domain-specific payloads
without gateway-chat semantics and returns a single JSON result:

```
POST /internal/kulrs/palette
Content-Type: application/json

{
  "product_id":  "prod-123",
  "image_urls":  ["https://cdn.example.com/a.jpg"],
  "workflow_id": "wf-palette-1",
  "model_preferences": { "preferred": "llama3" }
}
```

Response:

```json
{
  "run_id":        "...",
  "status":        "completed",
  "output":        "...",
  "model_backend": "llama3"
}
```

The underlying run is stored as an automation run with `source="kulrs"` and
`job_type="palette_analysis"`, making it queryable alongside other automation
runs.

## MCP Tool Runner

The `internal/runner` package includes `MCPRunner`, which calls an external
[Model Context Protocol](https://spec.modelcontextprotocol.io/) server to
execute tools using the JSON-RPC 2.0 `tools/call` method.

Set the `MCP_ENDPOINT` environment variable to enable MCP tool routing at
runtime:

```bash
export MCP_ENDPOINT=http://localhost:3000
```

The runner can also be used programmatically:

```go
r := runner.NewMCPRunner("http://localhost:3000", nil)
result, err := r.Execute(ctx, "my_tool", map[string]any{"key": "value"})
```

---

## Deployment Guide

This section documents the expected production deployment model, authentication
assumptions, database requirements, and the contracts that callers must follow.

### Internal Deployment Model

agent-service is an **internal-only** backend.  It is not intended to be
exposed directly to end-users or the public internet.  All callers must be
other internal services running within the same trusted network or a
private VPC.

**Recommended topology:**

```
 Browser / external client
        │
        ▼
  gateway-chat-platform   (public-facing; handles user auth, sessions)
        │  POST /internal/chat
        ▼
   agent-service          (internal; no user auth; service-to-service only)
        │  SELECT / INSERT
        ▼
     PostgreSQL
```

Automation callers (schedulers, workers, workflow engines) communicate via:

```
 Scheduler / Kulrs / automation worker
        │  POST /internal/automation   or   POST /internal/kulrs/palette
        ▼
   agent-service
```

### Authentication

Two mutually exclusive authentication modes are supported:

#### Service-token (`X-API-Key`)

Set the `API_KEY` environment variable on the agent-service pod/container.
All callers must include the header on every protected request:

```
X-API-Key: <shared-secret>
```

The `/health` and `/metrics` endpoints are **always exempt** from key checks
so they can be reached by load-balancer health probes and Prometheus scrapers.

> **Key rotation**: rotate `API_KEY` as an env-var update; no code change is
> needed.  Use a secrets manager (Vault, AWS Secrets Manager, etc.) to avoid
> storing the key in plain text.

#### mTLS (recommended for production)

When the cluster terminates TLS at the sidecar or ingress level you may omit
`API_KEY` entirely and rely on mutual TLS for service identity.  The service
trusts any connection that passes the mTLS handshake; the `X-API-Key` check
is skipped when `API_KEY` is empty.

Configure your service mesh (Istio, Linkerd, Envoy) to:

1. Require client certificates from the allowed service accounts
   (`gateway-chat-platform`, `scheduler`, `kulrs-worker`).
2. Reject any connection whose client certificate does not match the
   expected SPIFFE/SVID or organisational unit.

### Database Requirements

| Requirement | Value |
|---|---|
| Engine | PostgreSQL 14+ |
| Connection string env-var | `DATABASE_URL` |
| Schema migration | `make migrate` (runs `migrations/001_init.sql`) |
| Minimum privileges | `SELECT`, `INSERT`, `UPDATE` on `sessions`, `runs`, `run_steps` |
| Recommended pool size | 10–25 connections depending on concurrency |
| SSL mode | `sslmode=require` in production |

Apply the initial schema migration before the first deployment:

```bash
export DATABASE_URL=postgres://user:pass@host:5432/agentdb?sslmode=require
make migrate
```

Subsequent schema changes must be applied as additional numbered migration
files following the same pattern.

### gateway-chat-platform Contract

The gateway-chat-platform sends chat turns to agent-service using the
`POST /internal/chat` endpoint.  The service does **not** contact the gateway
during execution; all required context must be included in the request body.

#### Request (`ChatRunRequest`)

```jsonc
{
  "request_id": "req-abc123",      // correlation ID assigned by the gateway
  "thread_id":  "thread-xyz",      // maps to a logical chat session
  "user_id":    "user-42",         // authenticated end-user identifier
  "agent_id":   "agent-default",   // selects the agent definition to run
  "messages": [                    // full conversation history (required)
    {"role": "user",      "content": "..."},
    {"role": "assistant", "content": "..."},
    {"role": "user",      "content": "latest turn"}
  ],
  "system_prompt": "...",          // optional system instruction override
  "tool_policy": {                 // optional per-request tool access rules
    "allowed_tools":   ["search"],
    "denied_tools":    ["shell"],
    "require_approval": ["file_write"]
  },
  "model_preferences": {           // optional model-selection hints
    "preferred":  "llama3",
    "fallbacks":  ["gpt-4"],
    "max_tokens": 1024
  },
  "metadata": {}                   // free-form forwarded fields
}
```

#### Response (SSE stream)

The response is a `text/event-stream` of JSON-encoded events.  The gateway
should consume the stream and forward relevant events to the client.

| Event | Meaning |
|---|---|
| `run.created` | Run record persisted; contains `run_id` for correlation |
| `run.in_progress` | Agent loop has started |
| `run.model_selected` | Model backend was explicitly chosen |
| `run.step` | One reasoning step completed (content chunk) |
| `run.tool_call` | A tool was invoked; includes tool name, params, and result |
| `run.approval_requested` | A tool call is paused pending human approval; includes `approval_id` |
| `run.paused` | Run suspended (e.g. awaiting approval) |
| `run.completed` | Run finished successfully |
| `run.failed` | Run terminated with an error |

The gateway must handle `run.approval_requested` by presenting the approval
request to a human and then calling `POST /approvals/{id}/approve` or
`POST /approvals/{id}/deny` to resume the run.

### Automation Caller Contract

Non-chat callers (schedulers, background workers, Kulrs) send requests to
`POST /internal/automation`.  Responses can be streamed (SSE) or synchronous
(single JSON object).

#### Request (`AutomationRunRequest`)

```jsonc
{
  "source":        "scheduler",     // caller identifier (required)
  "job_type":      "report",        // work classification (required)
  "workflow_id":   "wf-abc",        // parent workflow ID (optional)
  "prompt":        "run the report", // agent instruction (required)
  "response_mode": "stream",        // "stream" | "sync" (default: "sync")
  "model_preferences": { "preferred": "llama3" },
  "context":    {},                 // additional free-form data
  "metadata":   {}
}
```

#### Response

**Sync mode** (`response_mode` omitted or `"sync"`):

```json
{
  "run_id":        "...",
  "status":        "completed",
  "output":        "...",
  "model_backend": "llama3",
  "tool_calls":    []
}
```

**Stream mode** (`response_mode: "stream"`): same SSE event vocabulary as the
chat endpoint above.

#### Kulrs-specific endpoint

Kulrs callers should prefer the dedicated `POST /internal/kulrs/palette`
endpoint which accepts a domain-typed payload and always responds synchronously:

```jsonc
{
  "product_id":        "prod-123",
  "image_urls":        ["https://cdn.example.com/img.jpg"],
  "workflow_id":       "wf-pal-1",
  "model_preferences": { "preferred": "llama3" }
}
```

### Run Inspection and Replay

Operators can inspect any stored run without relying on streaming-time logs:

| Method | Path | Description |
|---|---|---|
| `GET` | `/runs/{runID}` | Full run record: status, model backend, tool calls, approval records |
| `GET` | `/runs/{runID}/steps` | Ordered list of agent reasoning steps for replay |

These endpoints require the same `X-API-Key` authentication as other protected
routes when key-based auth is enabled.

### Metrics and Observability

`GET /metrics` returns a JSON snapshot of in-process counters:

```json
{
  "total_requests":           42,
  "total_runs":                8,
  "failed_runs":               1,
  "active_requests":           2,
  "tool_calls_total":          5,
  "approval_requests_total":   1,
  "backend_selections_total":  3,
  "runs_completed":            7,
  "run_latency_total_ms":   1540
}
```

Average run latency can be computed as `run_latency_total_ms / runs_completed`.

Every log line is emitted as structured JSON (via `log/slog`) and carries the
following fields where applicable:

| Field | Source |
|---|---|
| `run_id` | Every run lifecycle log |
| `request_id` | Chat runs (from gateway) |
| `thread_id` | Chat runs |
| `workflow_id` | Automation runs |
| `job_type` | Automation runs |
| `model_backend` | Runs with an explicit backend selection |
| `latency_ms` | Run completion and failure events |
| `tool_calls` | Run completion events |