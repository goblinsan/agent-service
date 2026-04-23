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
| `LLAMA_URL`       | —       | Base URL of a llama.cpp / OpenAI-compatible model server                 |
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
  "total_requests":  42,
  "total_runs":       8,
  "failed_runs":      1,
  "active_requests":  2
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

## SSE Event Types

- `run.created` – run record created
- `run.in_progress` – processing started
- `run.step` – intermediate step (one per agent reasoning step)
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