# Deployment: Dev vs Production

agent-service belongs to the "internal stateful Compose service" class: an
internal-only backend with persistent state (Postgres-backed sessions/runs)
that runs as a long-lived Docker Compose service rather than a blue/green
managed app. This document describes the two supported Compose layouts and
how production deploys and rollbacks work.

## Compose layouts

| File | Purpose | Postgres |
|---|---|---|
| `docker-compose.yml` | Local development | Bundles a disposable `postgres` container with fixed dev credentials |
| `docker-compose.prod.yml` | Production | None — connects to the shared Postgres instance via `DATABASE_URL` |

### Development

```bash
docker compose up --build
```

This starts both `postgres` and `agent-service` with the fixed dev
credentials baked into `docker-compose.yml` (`agent` / `agent` /
`agentdb`). Nothing here is suitable for production use.

### Production

```bash
docker compose -p agent-service -f docker-compose.prod.yml --env-file .env up -d --build
```

`docker-compose.prod.yml`:

- runs only the `agent-service` container (no bundled Postgres)
- loads all runtime configuration from a host-managed `.env` file
  (see `.env.example` for the full list of variables)
- bind-mounts `./config` read-only into `/etc/agent-service` for optional
  host-managed files such as an `AGENT_CATALOG_PATH` JSON document
- binds the HTTP port to `127.0.0.1` by default (`AGENT_SERVICE_HOST` /
  `AGENT_SERVICE_PORT`), since this is an internal-only service consumed by
  other services on the same host (e.g. `gateway-chat-platform`)

## Required runtime inputs

All production configuration is env/config-driven — nothing is hardcoded in
the repo:

- `DATABASE_URL` — shared Postgres instance (e.g. on the data-services node),
  **not** the bundled dev Postgres
- `LLM_NODES` (or `LLAMA_URL` as a single-node fallback) — the multi-node
  llm-service pool (tiny/small/medium tiers across nodes)
- `API_KEY` — optional service-to-service shared secret
- `AGENT_CATALOG_PATH` + `./config/<file>` — optional gateway-control-plane
  agent catalog mount
- `APNS_*` — optional push notification settings. `APNS_AUTH_KEY` accepts
  either raw PEM contents or a path to a `.p8` file; in production, use a
  path to a file under `./config/` (see below) rather than PEM contents in
  `.env`

See `.env.example` for the complete, documented list.

## Host layout

Production deploys assume a checked-out copy of this repo on the deploy host
(by convention `/srv/apps/agent-service`, matching the layout used for other
internal services):

```text
/srv/apps/agent-service/
  (git checkout of this repo, at the deployed revision)
  .env               <- host-managed, copied from .env.example, never committed
  config/            <- host-managed optional mounts (e.g. agent-catalog.json,
                          apns-auth-key.p8)
```

`.env` and `config/` are gitignored and must be created/maintained directly
on the host. They are part of the host's internal-service config and should
be covered by host-config backups, not by this repo.

### `.env` format and parsing

`.env` is a plain `KEY=value` file (one per line, `#` comments allowed) — the
same format `docker compose --env-file` consumes. It is **never** sourced as
shell by any tooling in this repo:

- `docker compose` reads it directly via `--env-file`
- `deploy/bin/deploy.sh` reads individual required values (currently
  `DATABASE_URL`, `AGENT_SERVICE_HOST`, `AGENT_SERVICE_PORT`) with a small
  `awk`-based parser that treats values as plain text

This means values are never evaluated as shell — no `$()`, backticks, quoting
rules, or multi-line PEM blocks are interpreted. It also means multi-line
values (such as APNs PEM keys) do not work in `.env`; use a file path instead
(see `APNS_AUTH_KEY` above and in `.env.example`).

#### Required format (enforced)

To guarantee `deploy/bin/deploy.sh` and `docker compose --env-file` agree on
every value, `.env` is restricted to a strict subset of the Compose env-file
format:

- every non-blank, non-comment line is `KEY=VALUE`, where `KEY` matches
  `[A-Za-z_][A-Za-z0-9_]*`
- bare `KEY` lines (which Compose would resolve from the runner's own
  environment rather than from `.env`) are **not** allowed — every variable
  the service needs must have an explicit `=value`, even if empty
- values are taken literally by both parsers: no quote stripping, no `$VAR`
  expansion, and no multi-line values. Avoid trailing `#` comments on a
  `KEY=VALUE` line — they become part of the value rather than being
  stripped

`deploy/bin/deploy.sh` validates the `KEY=VALUE` and no-bare-`KEY` rules
against `.env` before doing anything else, and fails with the offending line
number if a line doesn't conform. `.env.example` follows this format and can
be copied as a starting point.

### Secret files under `./config/`

For secrets that are awkward or unsafe to embed as a single `.env` line
(e.g. APNs `.p8` private keys), place the file under `./config/` on the host
and point the relevant `.env` variable at its in-container path:

```text
.env:    APNS_AUTH_KEY=/etc/agent-service/apns-auth-key.p8
host:    /srv/apps/agent-service/config/apns-auth-key.p8
```

`docker-compose.prod.yml` bind-mounts `./config` read-only to
`/etc/agent-service`, so any file placed there is available to the container
at `/etc/agent-service/<filename>`.

## Deploying

`deploy/bin/deploy.sh` is the single entry point for a production deploy. Run
it from the host checkout:

```bash
deploy/bin/deploy.sh
```

It will:

1. parse `.env` (without sourcing it) and confirm `DATABASE_URL` is set
2. apply any new database migrations (`make migrate`) against the shared
   Postgres instance, using the parsed `DATABASE_URL`
3. build and (re)start the `agent-service` container via
   `docker compose -p agent-service -f docker-compose.prod.yml --env-file .env up -d --build`
4. poll `/health` (using `AGENT_SERVICE_HOST`/`AGENT_SERVICE_PORT` from `.env`,
   defaulting to `127.0.0.1:8080`) until the service responds, or fail after
   ~60s, printing recent container logs on failure

The Compose project name is `agent-service` and the compose file is
`docker-compose.prod.yml` unless overridden with `--project` / `--compose-file`
/ `--env-file`.

### CI/CD

`.github/workflows/deploy-on-merge.yml` runs on push to `main` and on manual
`workflow_dispatch`, using the same `[self-hosted, gateway]` runner class as
the other gateway-managed apps. It:

1. checks out the target revision
2. runs `go vet`, `go test`, and a build as a basic regression check
3. bootstraps the host checkout at `/srv/apps/agent-service` (default,
   overridable via the `AGENT_SERVICE_DEPLOY_DIR` repository variable) by
   cloning this repo there if `.git` is not already present
4. fast-forwards the host checkout to the target revision
5. runs `deploy/bin/deploy.sh` on the host

#### Bootstrap and credentials

The bootstrap clone uses the workflow's job-scoped `github.token` over HTTPS,
then immediately runs `git remote set-url origin https://github.com/<owner>/agent-service.git`
to strip the embedded token from the persisted `.git/config`. No
credential-bearing remote URL is left on disk.

Because `origin` is then a plain HTTPS URL, subsequent `git fetch origin`
calls (on every deploy, not just bootstrap) depend on the host already having
its own GitHub authentication configured — e.g. a credential helper, or the
repository being public. This is the same requirement any other git-based
checkout on this host already has; bootstrap does not introduce a new
credential mechanism, it just avoids relying on the workflow's token past the
initial clone.

This intentionally does **not** use blue/green slots — agent-service is a
single stateful instance, not a managed app with parallel slots.

#### Host prerequisites for bootstrap

The bootstrap step only creates the git checkout. Before the first successful
deploy, an operator must still:

- ensure the parent directory (e.g. `/srv/apps`) exists and is writable by the
  `[self-hosted, gateway]` runner user
- create `/srv/apps/agent-service/.env` (copied from `.env.example` and
  filled in with real values)
- create `/srv/apps/agent-service/config/` with any required files (e.g.
  `apns-auth-key.p8`, an agent catalog JSON)
- confirm `docker`, the `docker compose` plugin, and `psql` are available to
  that runner user
- ensure the runner user's git configuration can authenticate `git fetch
  origin` against `https://github.com/<owner>/agent-service.git` without a
  token embedded in the remote URL (credential helper, or a public repo)

Until `.env` exists, `deploy/bin/deploy.sh` will fail fast with a clear error
rather than running migrations or starting the container.

## Rollback

There are no blue/green slots to flip. To roll back:

```bash
cd /srv/apps/agent-service
git checkout <previous-revision>
deploy/bin/deploy.sh
```

This rebuilds and restarts the container from the previous revision's source.
Database migrations are additive/forward-only by convention (numbered files
under `migrations/`); rolling back application code does not roll back schema
changes. If a specific migration must be reverted, that is a manual,
case-by-case operator step against the shared Postgres instance.

## What stays host-managed

- `.env` (secrets, DB connection string, LLM node list, etc.)
- `./config/` (optional mounted files such as an agent catalog JSON)
- the `/srv/apps/agent-service` checkout itself and its `.git` state
- the shared Postgres instance and its data (owned by the data-services node,
  not by this repo)
