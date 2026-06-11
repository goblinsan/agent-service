#!/usr/bin/env bash
# Production deploy for agent-service (internal stateful Compose service).
#
# Expected host layout (example):
#   /srv/apps/agent-service/              <- this repo checkout, deploy CWD
#     docker-compose.prod.yml             <- checked in
#     .env                                <- host-managed, see .env.example
#     config/                             <- host-managed optional mounts
#                                             (e.g. agent-catalog.json)
#
# Requirements on the host:
#   - docker + docker compose plugin
#   - psql (for `make migrate`)
#   - curl (for the health check)
#
# Usage:
#   deploy/bin/deploy.sh [--project NAME] [--compose-file FILE] [--env-file FILE]
#
# Run from the repo checkout, or it will cd there automatically based on this
# script's own location.
set -euo pipefail

PROJECT_NAME="${AGENT_SERVICE_PROJECT:-agent-service}"
COMPOSE_FILE="docker-compose.prod.yml"
ENV_FILE=".env"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --project)
      PROJECT_NAME="$2"
      shift 2
      ;;
    --compose-file)
      COMPOSE_FILE="$2"
      shift 2
      ;;
    --env-file)
      ENV_FILE="$2"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

if [[ ! -f "$COMPOSE_FILE" ]]; then
  echo "Missing $COMPOSE_FILE in $REPO_ROOT" >&2
  exit 1
fi

if [[ ! -f "$ENV_FILE" ]]; then
  echo "Missing $ENV_FILE in $REPO_ROOT (copy .env.example and fill in real values)" >&2
  exit 1
fi

# Enforce a constrained .env format so this script's reads and
# `docker compose --env-file` cannot disagree about any value (see
# docs/deployment.md, "`.env` format and parsing"). Every non-comment,
# non-blank line must be KEY=VALUE with no inline comments and no bare
# "KEY" (env-passthrough) lines, since those are the cases where Compose's
# env-file parser and a naive parser could diverge.
validate_env_file() {
  local file="$1"
  local lineno=0 bad=0
  while IFS= read -r line || [[ -n "$line" ]]; do
    lineno=$((lineno + 1))
    [[ "$line" =~ ^[[:space:]]*#.*$ ]] && continue
    [[ "$line" =~ ^[[:space:]]*$ ]] && continue
    if [[ ! "$line" =~ ^[A-Za-z_][A-Za-z0-9_]*= ]]; then
      echo "  $file:$lineno: not in KEY=VALUE form: $line" >&2
      bad=1
    fi
  done < "$file"
  if [[ "$bad" -ne 0 ]]; then
    echo "$file does not match the required KEY=VALUE format (see docs/deployment.md)" >&2
    exit 1
  fi
}

validate_env_file "$ENV_FILE"

# Read a KEY=value pair from $ENV_FILE without sourcing it, so values are
# treated as plain text (matching `docker compose --env-file`'s parsing) and
# never evaluated as shell. Comments and blank lines are skipped; the last
# matching key wins. Safe given validate_env_file above.
env_file_get() {
  local key="$1" file="$2"
  awk -v key="$key" '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*$/ { next }
    {
      line = $0
      sub(/^[[:space:]]+/, "", line)
      eq = index(line, "=")
      if (eq == 0) next
      k = substr(line, 1, eq - 1)
      gsub(/[[:space:]]+$/, "", k)
      if (k == key) found = substr(line, eq + 1)
    }
    END { print found }
  ' "$file"
}

# Required variables, validated up front so a missing/blank value fails fast
# before migrations or compose run.
DATABASE_URL="$(env_file_get DATABASE_URL "$ENV_FILE")"
if [[ -z "$DATABASE_URL" ]]; then
  echo "DATABASE_URL is not set in $ENV_FILE" >&2
  exit 1
fi

echo "==> Applying database migrations against the shared Postgres instance"
DATABASE_URL="$DATABASE_URL" make migrate

echo "==> Building and starting agent-service (project: $PROJECT_NAME)"
docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d --build --remove-orphans

echo "==> Waiting for /health"
HOST="$(env_file_get AGENT_SERVICE_HOST "$ENV_FILE")"
HOST="${HOST:-127.0.0.1}"
PORT="$(env_file_get AGENT_SERVICE_PORT "$ENV_FILE")"
PORT="${PORT:-8080}"
for _ in $(seq 1 30); do
  if curl -fsS "http://${HOST}:${PORT}/health" >/dev/null 2>&1; then
    echo "agent-service is healthy"
    exit 0
  fi
  sleep 2
done

echo "agent-service did not become healthy in time" >&2
docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" logs --tail=100 agent-service >&2
exit 1
