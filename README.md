# SMC Deployment Service

A CD (Continuous Deployment) service built with Go and the Temporal SDK.
A webhook from GitHub Actions triggers a Temporal workflow to auto deploy, and posts a Discord notification.

## Architecture

Hexagonal (ports & adapters): `CDWorkflow` depends on domain interfaces;
adapters implement them.

- **Domain** (`internal/domain`) — models + ports (`SSHExecutor`, `Notifier`)
- **Workflow** (`internal/workflow`) — Temporal orchestration (`CDWorkflow`)
- **Activity** (`internal/activity`) — one retriable unit of work per step
- **Adapter** (`internal/adapter`) — `ssh/`, `discord/`
- **API** (`internal/handler`, `internal/middleware`) — HTTP + auth

```
deploy/
├── cmd/{api,worker}/              # two entry points
├── internal/
│   ├── domain/ workflow/ activity/ adapter/   # core
│   └── config/ handler/ middleware/ logger/   # plumbing
├── scripts/                       # send-webhook, setup-postgres-es, create-namespace
├── config.example.yaml            # template for config.yaml
├── docker-compose.yaml            # api + worker (no host port; Traefik routes via smc-traefik)
├── docker-compose.temporal.yaml   # Temporal infra (UI :7080, gRPC :7233)
└── Dockerfile                     # EXPOSE 7082
```

## Configuration

Copy `config.example.yaml` → `config.yaml` (gitignored). The SSH private
key goes in `ssh.private_key` as a YAML literal block (`|`), or the
`SSH_PRIVATE_KEY` env var:

```yaml
ssh:
  private_key: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    ...
    -----END OPENSSH PRIVATE KEY-----
```

Copy `.env.example` → `.env` (gitignored) — only pins Docker image
versions for the Temporal stack (`POSTGRESQL_VERSION`, `TEMPORAL_VERSION`, …)
and sets `DOMAIN`.

### Deploy token

The `x-deploy-token` header is checked by `internal/middleware/auth.go`
against `auth.deploy_token` (`config.yaml`) or the `DEPLOY_TOKEN` env var
(env wins). Generate one with `openssl rand -hex 32`. The **same value**
must live in three places or every request 401s:

- server — `config.yaml` `auth.deploy_token` (or `.env` `DEPLOY_TOKEN`)
- smoke tests — `make send-deploy DEPLOY_TOKEN=<value>`
- CI — SMC repo GitHub Actions secret `SMC_DEPLOY_TOKEN`

## Running locally

```bash
make cd-up      # net-init → Temporal (waits for healthcheck) → api+worker → healthz
make cd-down    # tear down api/worker, then Temporal
```

Order matters: the api crash-loops if `temporal:7233` isn't up yet, so
`cd-up` waits for Temporal's healthcheck first.

The api has **no host port** — Traefik routes to it over the `smc-traefik`
network. Probe it with `make healthz` (execs into the container). For
ad-hoc `curl localhost:7082`, either use the host-Go path below or drop a
gitignored `docker-compose.override.yaml` adding `ports: ["7082:7082"]`.

**Host-Go dev path** (Go on host, only Temporal in Docker — for debugger
attach / fast iteration; binds api to `localhost:7082` directly):

```bash
make temporal-up
make run-api      # terminal 1
make run-worker   # terminal 2
```

## Service ports

Temporal-family ports sit in `7xxx`, clear of the SMC frontend on `8080`.

| Service | Container | Host | Notes |
|---|---|---|---|
| API | `7082` | (unpublished) | Traefik routes `cd.${DOMAIN}` → `api:7082` over `smc-traefik`. `EXPOSE 7082`. |
| Worker | — | — | Polls Temporal outbound only. |
| Temporal Server | `7233` | `7233` | gRPC frontend for SDK clients. |
| Temporal UI | `7080` | `7080` | <http://localhost:7080> |
| PostgreSQL (Temporal) | `5432` | (unpublished) | Internal network only; frees host `5432`. |
| Elasticsearch | `9200` | (unpublished) | Internal network only; frees host `9200`. |
| Traefik edge | — | `:80`/`:443` | Owned by sibling **SMC-Infra**; dials `api:7082`. |

## Exposing the webhook

The api binds `:7082` inside its container only. The public path runs
through Traefik

```
GitHub Actions ──POST https://cd.<domain>/api/webhook/deploy──▶ Cloudflare DNS
  ──▶ Traefik──smc-traefik──▶ api:7082
```

## API

### `POST /api/webhook/deploy`

Header `x-deploy-token`. Body:

```json
{
  "source": {
    "title": "SMC Frontend", "repo": "Dart147/SMC", "branch": "main",
    "commit": "a58327e…", "pr_number": "123", "pr_title": "feat: …",
    "pr_type": "feat", "pr_purpose": "Editor UX"
  },
  "method": "deploy",
  "metadata": { "project_name": "smc", "component": "frontend", "environment": "dev" },
  "setup": { "inject_secret": { "enable": false } },
  "post": {
    "setup_domain":   { "enable": false },
    "cleanup_domain": { "enable": false },
    "notify_discord": { "enable": true, "channel": "smc-activity", "notify_only": false }
  }
}
```

Returns `202 Accepted` with `{workflow_id, run_id, trace_id, status}`.
Full examples: `webhook-payload.deploy.json`, `webhook-payload.cleanup.json`.


## Makefile targets

```
build, build-api, build-worker      # compile binaries
run-api, run-worker, clean          # host-Go run / clean

cd-up, cd-down                      # full all-in-Docker lifecycle
net-init, temporal-up, cd-service-up

deploy                              # deploy-temporal + deploy-cd-service + healthz (on host)
deploy-temporal                     # apply Temporal stack
deploy-cd-service                   # rebuild + restart api + worker (--no-deps)
deploy-api, deploy-worker           # same, scoped to one binary
ps, logs [SERVICE=…], healthz       # diagnostics

send-deploy, send-cleanup           # POST a webhook to a running api
```

## Testing

Send a real webhook to a running stack (exercises the full workflow):

```bash
make send-deploy  DEPLOY_TOKEN=<token>     # defaults to http://localhost:7082
make send-cleanup DEPLOY_TOKEN=<token>
# override target: make send-deploy API_URL=http://your-api:7082 DEPLOY_TOKEN=<token>
```

These read `webhook-payload.{deploy,cleanup}.json`. Watch the run in the
Temporal UI at <http://localhost:7080> — inputs, outputs, retries, errors.
Discord testing needs `DISCORD_BOT_TOKEN` + `DISCORD_DEFAULT_CHANNEL_ID`
(env or `config.yaml`).

## Temporal

Each activity retries up to 3× with exponential backoff and times out
after 10 min; runs are inspectable in the UI.

| Concept | In this codebase |
|---|---|
| Workflow — durable orchestration, replays from checkpoint | `CDWorkflow` |
| Activity — one retriable unit of work | `internal/activity/` |
| Task queue | `cd-task-queue` |
| Worker — polls the queue, runs work | `cmd/worker/main.go` |
| Namespace | `default` |

### Workflow steps

1. **Fetch secrets** — no-op (`setup.inject_secret` accepted, ignored).
2. **SSH deploy / cleanup** — `internal/activity/ssh.go` →
   `adapter/ssh/client.go`. Deploy: temp dir at
   `/{base_path}/{environment}/{repo_name}/` → (private repo: write
   `REPO_PRIVATE_KEY` to a temp SSH config) → `git clone --depth=1
   --branch <branch>` (fallback: full clone + checkout commit) → run
   `repo/.deploy/{environment}/deploy.sh` with env vars → clean up.
   Cleanup runs `cleanup.sh` instead.
3. **DNS** — no-op (`post.setup_domain` / `cleanup_domain` accepted, ignored).
4. **Discord notify** — `discordgo` bot embed (green ok / red fail); a
   failure in step 2 sends a red embed before returning.
