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

| Variable       | Default | Description                        |
|----------------|---------|------------------------------------|
| `DATABASE_URL` | —       | Postgres connection string         |
| `PORT`         | `8080`  | HTTP listen port                   |
| `LOG_LEVEL`    | `info`  | Log level (`debug`, `info`, `warn`)|

## Makefile Targets

| Target    | Description                         |
|-----------|-------------------------------------|
| `build`   | Compile binary to `bin/agent-service` |
| `test`    | Run all tests                       |
| `run`     | Run service locally                 |
| `lint`    | Run `go vet`                        |
| `migrate` | Apply DB migrations                 |

## API

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

## SSE Event Types

- `run.created` – run record created
- `run.in_progress` – processing started
- `run.step` – intermediate step (3 emitted per run)
- `run.completed` – run finished successfully
- `run.failed` – run failed