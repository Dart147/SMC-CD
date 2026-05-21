# SMC Deployment Service

A CD (Continuous Deployment) service built with Go and the Temporal SDK.

## POC scope

Out of scope
- **Cloudflare DNS** — to be stripped from `internal/adapter/cloudflare/`.
  `post.setup_domain` / `post.cleanup_domain` accepted but no-op.
- **Traefik** — not used. Inbound for GitHub Actions is a Cloudflare
  Tunnel terminating at `cd-service:7082` on the internal Docker
  network. No public host port. See `ref/deploy_pipeline.md §2.4`.
- **OpenTelemetry** — observability stack not wired up for the POC.

Kept:

- **Webhook API** for GitHub Actions deploy/cleanup requests.
- **Temporal workflow orchestration** for retries + durability.
- **SSH-based deployment execution** (no-op in practice until SMC's
  `.deploy/<env>/deploy.sh` lands; until then, callers set
  `post.notify_discord.notify_only=true` — see "Notify-only short-circuit").
- **Discord notifications** (via Discord Bot API, not webhook).
- **Cloudflare Tunnel** (`cloudflared` container) for GH-Actions
  inbound.

### Notify-only short-circuit (temporary)

Until SMC ships `.deploy/<env>/deploy.sh`, the SSH activity has nothing
real to do. To exercise the Discord notification path end-to-end
without faking a green SSH step, `CDWorkflow` honors
`post.notify_discord.notify_only` — when true, secrets/SSH/DNS are
skipped and only the Discord activity runs. The flag is deleted once
real SSH deploys land. Full rationale and retirement plan:
`ref/deploy_pipeline.md §2.5`.

## Architecture

- **Domain Layer**: core logic and interfaces
- **Workflow Layer**: Temporal workflow orchestration
- **Activity Layer**: individual deployment steps
- **Adapter Layer**: external service integrations (only `ssh/` + `discord/` for the POC)
- **API Layer**: HTTP handlers and middleware

## Project Structure

```
deploy/
├── cmd/
│   ├── api/          # API server entry point
│   └── worker/       # Temporal worker entry point
├── internal/
│   ├── domain/       # Domain models and interfaces
│   ├── workflow/     # Temporal workflows
│   ├── activity/     # Temporal activities
│   ├── adapter/      # External service adapters (POC: ssh, discord)
│   ├── config/       # Configuration management
│   ├── handler/      # HTTP handlers
│   ├── middleware/   # HTTP middleware
│   └── logger/       # Logger utilities
├── scripts/                       # send-webhook, setup-postgres-es, create-namespace
├── config.example.yaml
├── docker-compose.yaml            # API and Worker services (host :7082)
├── docker-compose.temporal.yaml   # Temporal infrastructure (UI :7080, gRPC :7233)
├── Dockerfile                     # EXPOSE 7082
├── Makefile
├── README.md   # this file
```

## Configuration

Copy `config.example.yaml` to `config.yaml` and configure:


> Upstream's `infisical:` and `cloudflare:` blocks are present in
> `config.example.yaml` but unused in the POC. Leave them blank or remove
> them once the adapters are stripped.

### SSH Private Key Configuration

SSH private key must be configured via `private_key` field in `config.yaml`
or `SSH_PRIVATE_KEY` environment variable. Multi-line private keys use
YAML literal block scalar (`|`):

```yaml
ssh:
  private_key: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    ...
    -----END OPENSSH PRIVATE KEY-----
```

Or via environment variable:

```bash
export SSH_PRIVATE_KEY="$(cat ~/.ssh/id_ed25519)"
```

Copy `.env.example` to `.env` (only used to pin Docker image versions for the
Temporal stack — `POSTGRESQL_VERSION`, `TEMPORAL_VERSION`, etc.):

```bash
cp .env.example .env
```

`.env` is gitignored at the SMC repo root — don't commit it.

## Running Locally

### Step 1: Start Temporal Infrastructure

```bash
docker compose -f docker-compose.temporal.yaml up -d
```

This will start:

- Temporal Server (gRPC) on host port `7233`
- Temporal UI on host port `7080`
- PostgreSQL for Temporal — **internal Docker network only** (not published to host)
- Elasticsearch for Temporal visibility — **internal Docker network only** (not published to host)

### Step 2: Start API and Worker Services

Option A — using Docker Compose:

```bash
docker compose up -d
```

Option B — running locally:

```bash
# Start API (binds to PORT env / config.yaml's server.port; default 7082)
go run cmd/api/main.go

# Start Worker (in another terminal)
go run cmd/worker/main.go
```

## Service Ports

All temporal-family ports live in the `7xxx` range so they stay clear of
the SMC frontend on host `8080`. **Host port == container port** everywhere
so there is no host/container confusion. See the SMC root `README.md
§Ports` for the full table including the frontend.

| Service                  | Container (internal) | Host (external) | Compose file                   | Notes                                              |
|--------------------------|----------------------|-----------------|--------------------------------|----------------------------------------------------|
| **API Service**          | `7082`               | `7082`          | `docker-compose.yaml`          | `Dockerfile` `EXPOSE 7082`; container binds `0.0.0.0:7082`, published as `7082:7082` |
| **Worker**               | — (no port)          | — (no port)     | `docker-compose.yaml`          | Polls Temporal outbound only; no inbound port published |
| **Temporal Server**      | `7233`               | `7233`          | `docker-compose.temporal.yaml` | gRPC frontend for SDK clients                      |
| **Temporal UI**          | `7080`               | `7080`          | `docker-compose.temporal.yaml` | Web UI at `http://localhost:7080` — `TEMPORAL_UI_PORT=7080` set on the container |
| **PostgreSQL (Temporal)**| `5432`               | (unpublished)   | `docker-compose.temporal.yaml` | Internal Docker network only; reached by service name `postgresql`. Frees host `5432`. |
| **Elasticsearch**        | `9200`               | (unpublished)   | `docker-compose.temporal.yaml` | Internal Docker network only; reached by service name `elasticsearch`. Frees host `9200`. |
| **Temporal admin-tools** | — (no port)          | — (no port)     | `docker-compose.temporal.yaml` | One-shot setup container (schema + namespace)      |
| **temporal-create-namespace** | — (no port)     | — (no port)     | `docker-compose.temporal.yaml` | One-shot container that creates the `default` namespace |
| **cloudflared (tunnel)** | — (outbound)         | — (outbound)    | `docker-compose.yaml`          | Registers outbound to Cloudflare; terminates `https://cd.<yourdomain>.xyz` and forwards to `cd-service:7082` on the internal Docker network. Token via `CF_TUNNEL_TOKEN` in `.env`. |

### Quick reference (host ports)

- `7080` — Temporal UI
- `7082` — CD-service API
- `7233` — Temporal Server (gRPC)
- Host `5432` and `9200` intentionally **free** for SMC's own future Postgres / search.

> Changing the API port means editing **five aligned places**:
> `docker-compose.yaml` (`ports:` and `PORT=` env), `Dockerfile`'s `EXPOSE`,
> `config.example.yaml`'s `server.port`, and the fallback in
> `internal/config/config.go`. All five currently agree on `7082`.

## Exposing the webhook to GitHub Actions (Cloudflare Tunnel)

The CD-service binds `:7082` on the SMC host's localhost. GitHub-hosted
runners are not on that network, so they cannot POST the webhook
directly. The POC's inbound path is a **Cloudflare Tunnel**:

```
GitHub Actions runner
        │  POST https://cd.<yourdomain>.xyz/api/webhook/deploy
        ▼
Cloudflare edge ── tunnel ──► cloudflared (on SMC host) ──► cd-service:7082
                                                          (internal Docker network)
```

Why a tunnel: outbound-only registration means no public listener on
the host; aligns with the Phase-1 Cloudflare plan in the parent SMC
repo's CLAUDE.md. Full rationale in `ref/deploy_pipeline.md §2.4`.

### One-time setup

1. Create a Cloudflare account, add a zone for your domain.
2. Cloudflare Zero Trust → Networks → Tunnels → Create tunnel. Note the
   tunnel token.
3. In the tunnel's "Public hostnames" tab, route
   `cd.<yourdomain>.xyz` → `http://cd-service:7082` (the service name on
   the Docker network this compose file uses).
4. Add the token to `.env` (gitignored):

   ```bash
   CF_TUNNEL_TOKEN=eyJh...
   ```

5. `make deploy` brings `cloudflared` up alongside `api` + `worker`.
6. Verify from a laptop: `curl https://cd.<yourdomain>.xyz/api/healthz`.

### GitHub repository secrets (set in SMC, not SMC-CD)

- `SMC_WEBHOOK_URL` = `https://cd.<yourdomain>.xyz/api/webhook/deploy`
- `SMC_DEPLOY_TOKEN` = (see "Generating a deploy token" below)

A `Notify` job in SMC's `frontend-dev.yaml` / `backend-dev.yaml` reads
these and POSTs the payload above.

### Authentication note

`cd.<yourdomain>.xyz` is **not** behind Cloudflare Access in the POC.
Access would block GH Actions (no OIDC identity at the edge). The
webhook is gated by `x-deploy-token` only — keep that token narrow and
rotate on leak. Cloudflare Access remains the right answer for
human-facing tools (Temporal UI, pgAdmin) per parent CLAUDE.md.

### Generating a deploy token

The deploy token is a shared secret you generate yourself — not issued
by any third party. The auth middleware in `internal/middleware/auth.go`
compares the incoming `x-deploy-token` header against
`cfg.Auth.DeployToken` (loaded from `auth.deploy_token` in
`config.yaml`, or the `DEPLOY_TOKEN` env var; env wins on collision).

**Generate one with any high-entropy source:**

```bash
openssl rand -hex 32                                       # 64-char hex (recommended)
openssl rand -base64 32                                    # ~44-char base64
python3 -c 'import secrets; print(secrets.token_hex(32))'  # if no openssl
```

**Put the same value in three places:**

| Location | What it is |
| --- | --- |
| `config.yaml` `auth.deploy_token` **or** `.env` `DEPLOY_TOKEN=…` (gitignored) | Server-side. What the auth middleware checks against. |
| Local shell when smoke-testing | `make send-deploy DEPLOY_TOKEN=<value>` — the Makefile passes this through to `scripts/send-webhook.sh` as the `x-deploy-token` header. |
| SMC repo GitHub Actions secret `SMC_DEPLOY_TOKEN` (later, for branch C) | What the `Notify` job in `frontend-dev.yaml` / `backend-dev.yaml` sends as `x-deploy-token`. |

If these three drift apart, every CI request gets a silent 401. Rotate
by generating a new value and updating all three at once.

**Local-only shortcut (POC).** For the row 0.6 localhost smoke test you
do not need a real secret — set `auth.deploy_token: "your-deploy-token-here"`
in your local `config.yaml` and pass the same literal string to
`make send-deploy DEPLOY_TOKEN=your-deploy-token-here`. Roll a real
`openssl rand -hex 32` value before row 0.8 (push to GitHub) and before
exposing `cd.<yourdomain>.xyz` publicly through Traefik.

Treat the token like a password: never commit it, never paste it into
chat logs or screenshots, rotate on any suspected leak.

## API Endpoints

### POST /api/webhook/deploy

Deploy or cleanup a service.

**Headers:**

- `x-deploy-token`: authentication token

**Request body (POC form — `inject_secret` and `setup_domain` are accepted but no-op):**

```json
{
  "source": {
    "title": "SMC Frontend",
    "repo": "Dart147/SMC",
    "branch": "main",
    "commit": "a58327e5a861d8e4bb7ccc75a324ae97caf8c089",
    "pr_number": "123",
    "pr_title": "feat: workspace polish",
    "pr_type": "feat",
    "pr_purpose": "Editor UX"
  },
  "method": "deploy",
  "metadata": {
    "project_name": "smc",
    "component": "frontend",
    "environment": "dev"
  },
  "setup": {
    "inject_secret": { "enable": false }
  },
  "post": {
    "setup_domain":   { "enable": false },
    "cleanup_domain": { "enable": false },
    "notify_discord": {
      "enable": true,
      "channel": "smc-activity",
      "notify_only": true
    }
  }
}
```

`notify_only=true` (temporary) tells `CDWorkflow` to skip secrets/SSH/DNS
and run only the Discord activity. Use it from GH Actions until SMC's
`.deploy/<env>/deploy.sh` lands; then drop the field. See the
"Notify-only short-circuit" note at the top of this README.

**Response:**

```json
{
  "workflow_id": "deploy-...",
  "run_id": "...",
  "trace_id": "...",
  "status": "started"
}
```

See `webhook-payload.deploy.json` and `webhook-payload.cleanup.json` for
complete examples.

### GET /api/healthz

Health check endpoint.

## Operator workflow — updating the CD-service on the host

When the code or compose files under `deploy/` change, the operator
(whoever holds the host SSH key) updates the running CD-service by hand.
This pipeline does **not** auto-deploy itself, by design — the deployer
cannot orchestrate its own upgrade safely (see
`ref/deploy_pipeline.md §2`).

```bash
ssh <smc-host>
cd /opt/smc/deploy
git pull --ff-only
make deploy        # = deploy-temporal + deploy-cd-service + healthz
```

Targets are idempotent — re-running is safe. Component-scoped variants
exist when you only need to restart one binary:

```bash
make deploy-api       # rebuild + restart api only, --no-deps
make deploy-worker    # rebuild + restart worker only, --no-deps
make deploy-temporal  # apply the Temporal stack (rare — only when its compose changes)
```

Diagnostics:

```bash
make ps                  # list CD-service + Temporal containers
make logs                # tail api + worker
make logs SERVICE=api    # tail just one
make healthz             # probe http://localhost:7082/api/healthz with retries
```

### Rollback

Intentionally not a Makefile target — the operator picks the SHA:

```bash
cd /opt/smc/deploy
git checkout <previous-sha>
make deploy-cd-service
make healthz
```

## Testing Webhooks against a running CD-service

Distinct from the `deploy*` targets above (which update the CD-service
itself), these send a real webhook to a running CD-service to exercise
the workflow end-to-end:

```bash
# Send deploy webhook (defaults to http://localhost:7082)
make send-deploy

# Send cleanup webhook
make send-cleanup

# Custom API URL and token
make send-deploy   API_URL=http://your-api:7082 DEPLOY_TOKEN=your-token
make send-cleanup  API_URL=http://your-api:7082 DEPLOY_TOKEN=your-token
```

Webhook payload examples:

- `webhook-payload.deploy.json` — deploy workflow example
- `webhook-payload.cleanup.json` — cleanup workflow example

## Development

### Dependencies

- Go 1.25+
- Temporal Server
- Docker and Docker Compose

### Building

```bash
# Build both API and Worker
make build

# Build individually
make build-api
make build-worker

# Run locally
make run-api
make run-worker
```

### Makefile Targets

Build / local-run (Go binaries, no Docker):

- `make build`             — build API and Worker binaries
- `make build-api`         — build API binary only
- `make build-worker`      — build Worker binary only
- `make run-api`           — run API server locally
- `make run-worker`        — run Worker locally
- `make clean`             — remove built binaries

Operator targets (run on the SMC host):

- `make deploy`            — `deploy-temporal` + `deploy-cd-service` + `healthz`
- `make deploy-temporal`   — `docker compose up -d` for the Temporal stack
- `make deploy-cd-service` — rebuild + restart `api` and `worker` (`--no-deps`)
- `make deploy-api`        — same, scoped to `api`
- `make deploy-worker`     — same, scoped to `worker`
- `make logs [SERVICE=…]`  — tail container logs
- `make ps`                — list containers across both compose files
- `make healthz`           — probe `/api/healthz` with retries

Webhook test targets (exercise a running CD-service):

- `make send-deploy`       — POST `webhook-payload.deploy.json`
- `make send-cleanup`      — POST `webhook-payload.cleanup.json`

---

## Temporal Architecture

Temporal is a **durable execution engine** — a workflow orchestrator that
guarantees a multi-step process runs to completion, even if machines crash.

```
                    ┌──────────────────────────────────┐
                    │        Temporal Server            │
                    │  (PostgreSQL + Elasticsearch)     │
                    │                                   │
                    │  Stores workflow state, handles   │
                    │  retries, timeouts, scheduling    │
                    └──────────┬───────────┬────────────┘
                               │           │
                    ┌──────────▼──┐  ┌─────▼──────────┐
                    │   API Server │  │   Worker        │
                    │  (cmd/api)   │  │  (cmd/worker)   │
                    │              │  │                  │
                    │  Receives    │  │  Polls Temporal  │
                    │  webhooks,   │  │  for tasks,      │
                    │  starts      │  │  executes        │
                    │  workflows   │  │  activities      │
                    └──────────────┘  └─────────────────┘
```

### Key Temporal Concepts

| Concept | What It Is | In This Codebase |
|---------|-----------|------------------|
| **Workflow** | A durable function that orchestrates steps. Survives crashes — Temporal replays from last checkpoint. | `CDWorkflow` in `internal/workflow/cd_workflow.go` |
| **Activity** | A single unit of work (e.g., "SSH deploy"). Retried independently on failure. | Files in `internal/activity/` |
| **Task Queue** | Named queue connecting workflows/activities to workers. | `cd-task-queue` |
| **Worker** | Process that polls a task queue and executes workflows/activities. | `cmd/worker/main.go` |
| **Namespace** | Logical isolation for workflows (like a DB schema). | `default` |

### Why Temporal Instead of Just Running Scripts?

1. **Durability** — if the worker crashes mid-SSH, Temporal restarts from the last completed step
2. **Retries** — each activity retries up to 3 times with exponential backoff (1s, 2s, 4s...)
3. **Visibility** — Temporal UI (`http://localhost:7080`) shows every execution with inputs, outputs, and errors
4. **Timeouts** — activities timeout after 10 minutes; no hung deployments

---

### Request Flow (End to End — POC variant)

```
CI / Manual Trigger          API Server                 Temporal Server           Worker
       │                         │                           │                      │
       │  POST /api/webhook/deploy                           │                      │
       │  Header: x-deploy-token │                           │                      │
       │  Body: DeployRequest    │                           │                      │
       │ ───────────────────────>│                           │                      │
       │                         │                           │                      │
       │                         │  1. Validate token (auth middleware)             │
       │                         │  2. Validate JSON body    │                      │
       │                         │  3. Generate trace_id     │                      │
       │                         │                           │                      │
       │                         │  StartWorkflow(CDWorkflow)│                      │
       │                         │ ─────────────────────────>│                      │
       │                         │                           │                      │
       │  202 Accepted           │                           │                      │
       │  {workflow_id, run_id}  │                           │                      │
       │ <───────────────────────│                           │                      │
       │                         │                           │  Poll task queue     │
       │                         │                           │ <─────────────────── │
       │                         │                           │                      │
       │                         │                           │  Execute CDWorkflow  │
       │                         │                           │ ───────────────────> │
       │                         │                           │                      │
       │                         │                           │  Step 1: Secrets     │
       │                         │                           │  ──> NO-OP (Phase 1) │
       │                         │                           │                      │
       │                         │                           │  Step 2: SSH Deploy  │
       │                         │                           │  ──> Remote Server   │
       │                         │                           │                      │
       │                         │                           │  Step 3: DNS Setup   │
       │                         │                           │  ──> NO-OP (Phase 1) │
       │                         │                           │                      │
       │                         │                           │  Step 4: Notify      │
       │                         │                           │  ──> Discord Bot     │
```

---

### Steps

#### Step 1: Fetch Secrets — **NO-OP in POC**

Upstream calls Infisical; in the POC the activity returns an empty map
without making any network call. `setup.inject_secret.enable=true` payloads
are accepted but produce no secrets. Phase 1 plug-in point: re-add
`internal/adapter/infisical/` and the activity body.

#### Step 2: SSH Deploy or Cleanup — **Always runs**

- **Code**: `internal/activity/ssh.go` → `internal/adapter/ssh/client.go`
- **Deploy flow on remote server**:
  1. Clean and create temp dir at `/{base_path}/{environment}/{repo_name}/`
  2. If private repo: write `REPO_PRIVATE_KEY` to temp SSH config
  3. `git clone --depth=1 --branch <branch>` (fallback: full clone + checkout specific commit)
  4. `cd repo/.deploy/{environment}/` and execute `deploy.sh` with injected env vars
  5. Clean up temp dir
- **Cleanup flow**: if deploy dir exists, run `cleanup.sh`, then remove temp dir
- **Secrets are passed as env vars** to the deploy/cleanup scripts (empty map in the POC)

#### Step 3: DNS — **NO-OP in POC**

Upstream upserts/removes Cloudflare DNS records; in the POC the activity
is a no-op regardless of `post.setup_domain.enable`. Phase 1 plug-in
point: re-add `internal/adapter/cloudflare/`.

#### Step 4: Discord Notification (Optional)

- **Uses**: Discord Bot API via `discordgo` library (not webhook)
- **Sends**: rich embed with project name, environment, component, commit, trace ID
- **Colors**: green on success, red on failure
- **Also fires on failure**: Step 2 sends a failure notification before returning errors

---

### Hexagonal Architecture (Ports & Adapters — POC)

```
                  ┌─────────────────────────────────┐
                  │         Domain Layer             │
                  │  internal/domain/ports.go        │
                  │                                  │
                  │  Interfaces:                     │
                  │  - SecretManager  (no-op stub)   │
                  │  - SSHExecutor                   │
                  │  - DNSProvider    (no-op stub)   │
                  │  - Notifier                      │
                  └──────────┬──────────────────────┘
                             │ implements
                ┌────────────┴────────────┐
                │                         │
        ┌───────▼──────┐         ┌────────▼──────┐
        │     SSH      │         │    Discord    │
        │   Adapter    │         │    Adapter    │
        │              │         │               │
        │ SSHExecutor  │         │   Notifier    │
        └──────────────┘         └───────────────┘
```

Infisical and Cloudflare adapter directories are scheduled for removal —
Phase 1 work re-introduces them behind the same `SecretManager` /
`DNSProvider` interfaces with zero workflow changes.

---

### External Service Dependencies (POC)

| Service | Purpose | Config |
|---------|---------|--------|
| **Temporal Server** | Workflow orchestration | `temporal.address` (default: `localhost:7233`) |
| **PostgreSQL** | Temporal persistence | Inside `docker-compose.temporal.yaml` (internal only) |
| **Elasticsearch** | Temporal visibility/search | Inside `docker-compose.temporal.yaml` (internal only) |
| **Discord** | Notifications | `DISCORD_BOT_TOKEN`, `DISCORD_DEFAULT_CHANNEL_ID` env vars |
| **Target SSH Server** | Where deployments run | `ssh.host`, `ssh.user`, `ssh.private_key` |

---

### Domain Model

```
DeployRequest
├── source
│   ├── title           # Display name (e.g., "SMC Frontend")
│   ├── repo            # GitHub org/repo (e.g., "Dart147/SMC")
│   ├── branch          # Branch name
│   ├── commit          # Full commit SHA
│   ├── pr_number       # Optional PR number
│   ├── pr_title        # Optional PR title
│   ├── pr_type         # Optional PR type
│   └── pr_purpose      # Optional PR purpose
├── method              # "deploy" or "cleanup"
├── metadata
│   ├── project_name    # Project identifier (e.g., "smc")
│   ├── component       # Component (e.g., "frontend", "backend")
│   └── environment     # "snapshot" | "dev" | "stage" | "production"
├── setup
│   └── inject_secret   # Accepted, no-op in POC
├── post
│   ├── setup_domain    # Accepted, no-op in POC
│   ├── cleanup_domain  # Accepted, no-op in POC
│   └── notify_discord  # Discord notification toggle
└── trace_id            # Auto-generated UUID (server-side)
```

---

### Testing Locally: Step-by-Step

#### Prerequisites

- Docker and Docker Compose installed
- Go 1.25+ installed
- `config.yaml` created from `config.example.yaml`
- Discord bot token and channel ID (for notification testing)

#### 1. Start Temporal Infrastructure

```bash
# Copy env file for Docker image versions
cp .env.example .env

# Start Temporal Server + PostgreSQL + Elasticsearch + UI
docker compose -f docker-compose.temporal.yaml up -d

# Wait for it to be ready (check Temporal UI at http://localhost:7080)
```

#### 2. Start the API Server and Worker

```bash
# Terminal 1: Start API
make run-api

# Terminal 2: Start Worker
make run-worker
```

#### 3. Send a Test Deploy Request

```bash
# Using the Makefile (reads from webhook-payload.deploy.json)
make deploy DEPLOY_TOKEN=your-deploy-token-here

# Or using curl directly
curl -X POST http://localhost:7082/api/webhook/deploy \
  -H "Content-Type: application/json" \
  -H "x-deploy-token: your-deploy-token-here" \
  -d '{
    "source": {
      "title": "Test Deploy",
      "repo": "Dart147/SMC",
      "branch": "main",
      "commit": "abc123",
      "pr_number": "1"
    },
    "method": "deploy",
    "metadata": {
      "project_name": "smc",
      "component": "frontend",
      "environment": "dev"
    },
    "setup": {
      "inject_secret": { "enable": false }
    },
    "post": {
      "setup_domain":   { "enable": false },
      "cleanup_domain": { "enable": false },
      "notify_discord": { "enable": false }
    }
  }'
```

You'll get back a `202 Accepted` with the workflow ID. Check the Temporal
UI at <http://localhost:7080> to see the workflow execution and its steps.

#### 4. Send a Test Discord Notification

Send a deploy request with `notify_discord.enable = true`. The notification
fires at the end of the workflow (Step 4).

Required env vars (or set them in `config.yaml`):

```bash
export DISCORD_BOT_TOKEN="your-bot-token"
export DISCORD_DEFAULT_CHANNEL_ID="your-channel-id"
```

Then send a request with Discord enabled:

```bash
curl -X POST http://localhost:7082/api/webhook/deploy \
  -H "Content-Type: application/json" \
  -H "x-deploy-token: your-deploy-token-here" \
  -d '{
    "source": {
      "title": "Discord Test",
      "repo": "Dart147/SMC",
      "branch": "main",
      "commit": "abc123"
    },
    "method": "deploy",
    "metadata": {
      "project_name": "smc",
      "component": "frontend",
      "environment": "dev"
    },
    "setup": {
      "inject_secret": { "enable": false }
    },
    "post": {
      "setup_domain":   { "enable": false },
      "cleanup_domain": { "enable": false },
      "notify_discord": {
        "enable": true,
        "channel": "smc-activity"
      }
    }
  }'
```

**Note**: the Discord notification fires after SSH deployment (Step 2)
completes. If the SSH step fails, a failure notification is sent instead.
To test Discord in isolation (without needing a real SSH target), mock
the SSH activity or create a test workflow.

#### 5. Monitor with Temporal UI

Open <http://localhost:7080> in your browser to:

- See all workflow executions and their status
- Click into a workflow to see each activity's input/output
- View error details for failed activities
- Check retry attempts and timing

---

## Where decisions live

- Root port table (frontend + temporal family): sibling `../SMC/README.md` §Ports.