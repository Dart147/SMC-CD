GREEN = \033[0;32m
BLUE  = \033[0;34m
RED   = \033[0;31m
NC    = \033[0m

TEMPORAL_COMPOSE = docker-compose.temporal.yaml
CD_COMPOSE       = docker-compose.yaml
HEALTHZ_URL      = http://localhost:7082/api/healthz
TEMPORAL_PROJECT = smc-temporal
TRAEFIK_NETWORK  = smc-traefik
TEMPORAL_NETWORK = temporal-network
IMAGE_API        = smc-cd-api
IMAGE_WORKER     = smc-cd-worker

# ---------------------------------------------------------------------------
# Default target
# ---------------------------------------------------------------------------

all: build ## Build both binaries (default goal)

## Show this help message
help:
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' Makefile | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[0;32m%-18s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------------------
# Build / local-run (Go binaries — used for development outside Docker)
# ---------------------------------------------------------------------------

prepare: ## Download Go module dependencies
	@echo -e ":: $(GREEN)Preparing environment...$(NC)"
	@echo -e ":: $(GREEN)Downloading go dependencies...$(NC)"
	@go mod download \
		&& echo -e "==> $(BLUE)Successfully downloaded go dependencies$(NC)" \
		|| (echo -e "==> $(RED)Failed to download go dependencies$(NC)" && exit 1)

build: build-api build-worker ## Build both api + worker binaries

build-api: ## Build the api binary
	@echo -e ":: $(GREEN)Building API...$(NC)"
	@go build -o bin/api cmd/api/main.go && echo -e "==> $(BLUE)API build completed successfully$(NC)" || (echo -e "==> $(RED)API build failed$(NC)" && exit 1)

build-worker: ## Build the worker binary
	@echo -e ":: $(GREEN)Building Worker...$(NC)"
	@go build -o bin/worker cmd/worker/main.go && echo -e "==> $(BLUE)Worker build completed successfully$(NC)" || (echo -e "==> $(RED)Worker build failed$(NC)" && exit 1)

run-api: ## Build + run the api on the host (Go dev path; needs temporal-up)
	@echo -e ":: $(GREEN)Starting API...$(NC)"
	@go build -o bin/api cmd/api/main.go && ./bin/api

run-worker: ## Build + run the worker on the host (Go dev path; needs temporal-up)
	@echo -e ":: $(GREEN)Starting Worker...$(NC)"
	@go build -o bin/worker cmd/worker/main.go && ./bin/worker

clean: ## Remove built binaries
	@echo -e ":: $(GREEN)Cleaning binaries...$(NC)"
	@rm -f bin/api bin/worker
	@rmdir bin 2>/dev/null || true

# ---------------------------------------------------------------------------
# Operator targets — run on the SMC host to update / inspect the CD-service.
# Idempotent. `make deploy` is the one-word "make the deployer current."
# Rollback is a manual procedure (see README) — intentionally not automated.
# ---------------------------------------------------------------------------

deploy: deploy-temporal deploy-cd-service healthz ## (operator) Make the running deployer current with main: apply Temporal + roll api+worker + healthz
	@echo -e "==> $(BLUE)Deploy complete — CD-service is healthy on $(HEALTHZ_URL)$(NC)"

deploy-temporal: ## (operator) Apply the Temporal stack (project=smc-temporal)
	@echo -e ":: $(GREEN)Applying Temporal stack...$(NC)"
	@docker compose -f $(TEMPORAL_COMPOSE) -p $(TEMPORAL_PROJECT) up -d

deploy-cd-service: deploy-api deploy-worker ## (operator) Rebuild + roll only api + worker

# Build the CD-service image with plain `docker build`, NOT
# `docker compose --build`. api and worker are the same image, so build once
# and tag both. This skips compose's buildx metadata-file step, which fails
# under snap-confined Docker
build-image:
	@echo -e ":: $(GREEN)Building CD-service image...$(NC)"
	@docker build -t $(IMAGE_API) -t $(IMAGE_WORKER) -f Dockerfile .

deploy-api: build-image ## (operator) Rebuild + roll only the api
	@echo -e ":: $(GREEN)Deploying CD-service API...$(NC)"
	@docker compose -f $(CD_COMPOSE) up -d --no-deps api

deploy-worker: build-image ## (operator) Rebuild + roll only the worker
	@echo -e ":: $(GREEN)Deploying CD-service Worker...$(NC)"
	@docker compose -f $(CD_COMPOSE) up -d --no-deps worker

logs: ## Tail logs (use SERVICE=api|worker to scope)
	@docker compose -f $(CD_COMPOSE) logs -f $(SERVICE)

ps: ## Show container status (CD-service + Temporal)
	@echo -e ":: $(GREEN)CD-service:$(NC)"
	@docker compose -f $(CD_COMPOSE) ps
	@echo -e ":: $(GREEN)Temporal stack:$(NC)"
	@docker compose -f $(TEMPORAL_COMPOSE) -p $(TEMPORAL_PROJECT) ps

healthz: ## Probe api /api/healthz from inside the container
	@echo -e ":: $(GREEN)Probing api /api/healthz (from inside the container)...$(NC)"
	@for i in 1 2 3 4 5; do \
		docker exec deployment-api wget -qO- http://localhost:7082/api/healthz >/dev/null 2>&1 \
			&& echo -e "==> $(BLUE)healthz ok$(NC)" && exit 0; \
		echo "  attempt $$i/5 failed, retrying in 2s..."; sleep 2; \
	done; \
	echo -e "==> $(RED)healthz failed after 5 attempts$(NC)"; exit 1

# ---------------------------------------------------------------------------
# boots / tears down the full all-in-Docker stack in
# the right order (Temporal first, then api+worker)
# ---------------------------------------------------------------------------

cd-up: net-init temporal-up namespace-init cd-service-up healthz ## Bring up the full all-in-Docker stack (Temporal -> api+worker)
	@echo -e "==> $(BLUE)Stack up.$(NC)"
	@echo -e "    api      : reachable only inside the smc-traefik / temporal-network Docker networks"
	@echo -e "               (probe with: docker exec deployment-api wget -qO- http://localhost:7082/api/healthz)"
	@echo -e "    Temporal : http://localhost:7080  (UI publishes a host port)"

cd-down: ## Stop api+worker, then the Temporal stack (keeps volumes)
	@echo -e ":: $(GREEN)Stopping CD-service api + worker...$(NC)"
	@docker compose -f $(CD_COMPOSE) down
	@echo -e ":: $(GREEN)Stopping Temporal stack (project=$(TEMPORAL_PROJECT))...$(NC)"
	@docker compose -f $(TEMPORAL_COMPOSE) -p $(TEMPORAL_PROJECT) down

net-init:
	@docker network inspect $(TRAEFIK_NETWORK) >/dev/null 2>&1 \
		|| docker network create $(TRAEFIK_NETWORK)
	@docker network inspect $(TEMPORAL_NETWORK) >/dev/null 2>&1 \
		|| docker network create $(TEMPORAL_NETWORK)

temporal-up: ## Start only the Temporal stack (host-Go dev path)
	@echo -e ":: $(GREEN)Starting Temporal stack (project=$(TEMPORAL_PROJECT))...$(NC)"
	@docker compose -f $(TEMPORAL_COMPOSE) -p $(TEMPORAL_PROJECT) up -d --wait temporal temporal-ui
	@echo -e "==> $(BLUE)Temporal ready (server healthy per compose)$(NC)"

namespace-init: ## Ensure the Temporal 'default' namespace exists
	@echo -e ":: $(GREEN)Ensuring Temporal 'default' namespace exists...$(NC)"
	@docker compose -f $(TEMPORAL_COMPOSE) -p $(TEMPORAL_PROJECT) run --rm --no-deps \
		--entrypoint /usr/local/bin/temporal temporal-admin-tools \
		operator namespace describe --namespace default --address temporal:7233 \
		>/dev/null 2>&1 \
		&& echo -e "==> $(BLUE)namespace 'default' already registered$(NC)" \
		|| ( \
			echo -e "  registering namespace 'default'..."; \
			docker compose -f $(TEMPORAL_COMPOSE) -p $(TEMPORAL_PROJECT) run --rm --no-deps \
				--entrypoint /usr/local/bin/temporal temporal-admin-tools \
				operator namespace create --namespace default --retention 24h --address temporal:7233 \
		)

cd-service-up:
	@echo -e ":: $(GREEN)Starting CD-service api + worker...$(NC)"
	@docker compose -f $(CD_COMPOSE) up -d --build

# ---------------------------------------------------------------------------
# Webhook test targets — exercise a running CD-service from the operator
# host. Distinct from the `deploy*` targets above, which update the
# CD-service itself.
# ---------------------------------------------------------------------------

send-deploy: ## Send a deploy webhook to a running CD-service (DEPLOY_TOKEN=...)
	@echo -e ":: $(GREEN)Sending deploy webhook request...$(NC)"
	@PAYLOAD_FILE=webhook-payload.deploy.json; \
	API_URL_VAL=$${API_URL:-http://localhost:7082}; \
	./scripts/send-webhook.sh $$PAYLOAD_FILE $$API_URL_VAL $(DEPLOY_TOKEN)

send-cleanup: ## Send a cleanup webhook to a running CD-service (DEPLOY_TOKEN=...)
	@echo -e ":: $(GREEN)Sending cleanup webhook request...$(NC)"
	@PAYLOAD_FILE=webhook-payload.cleanup.json; \
	API_URL_VAL=$${API_URL:-http://localhost:7082}; \
	./scripts/send-webhook.sh $$PAYLOAD_FILE $$API_URL_VAL $(DEPLOY_TOKEN)

.PHONY: all help prepare build build-api build-worker run-api run-worker clean \
	deploy deploy-temporal deploy-cd-service deploy-api deploy-worker \
	logs ps healthz send-deploy send-cleanup \
	cd-up cd-down net-init temporal-up cd-service-up namespace-init
