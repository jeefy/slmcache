# Makefile for development: start the slmcache container and run e2e tests

.PHONY: serve serve-host stop wait-ready e2e e2e-test

UNAME_S := $(shell uname -s)

ifeq ($(UNAME_S),Linux)
DEFAULT_OLLAMA_URL := http://localhost:11434
DEFAULT_NETWORK_MODE := host
else
DEFAULT_OLLAMA_URL := http://host.docker.internal:11434
DEFAULT_NETWORK_MODE := bridge
endif

# Start container (default). Automatically picks a network mode/URL suitable for the host OS.
serve:
	@echo "Starting slmcache container (network mode defaults to $(DEFAULT_NETWORK_MODE), SLM_OLLAMA_URL defaults to $(DEFAULT_OLLAMA_URL))"
	DOCKER_NETWORK_MODE=$${DOCKER_NETWORK_MODE:-$(DEFAULT_NETWORK_MODE)} SLM_OLLAMA_URL=$${SLM_OLLAMA_URL:-$(DEFAULT_OLLAMA_URL)} docker compose up --build -d

# Start container using host networking so container can reach localhost:11434 directly
serve-host:
	@echo "Starting slmcache container with host networking (container sees host localhost)"
	DOCKER_NETWORK_MODE=host SLM_OLLAMA_URL=$${SLM_OLLAMA_URL:-http://localhost:11434} docker compose up --build -d

# Stop and remove containers
stop:
	docker compose down

# Wait until service responds on /search (simple readiness check)
wait-ready:
	@echo "Waiting for slmcache to become ready on http://localhost:8080..."
	@i=0; \
	while [ $$i -lt 30 ]; do \
		if curl -sSf "http://localhost:8080/search?q=health" >/dev/null 2>&1; then \
			echo "slmcache is ready"; exit 0; \
		fi; \
		sleep 1; i=$$((i+1)); \
	done; \
	echo "timeout waiting for slmcache"; exit 1

# Run e2e tests against the codebase (these tests start an in-process server and
# will use the configured SLM backend — ensure Ollama is reachable if you want
# to exercise the Ollama backend). This target does not target the running
# container; it runs the repo's e2e tests locally.
e2e: serve
	$(MAKE) wait-ready
	@echo "Running e2e tests..."
	go test ./internal/e2e -v
	@echo "e2e tests finished"

# External e2e tests against a running instance at localhost:8080.
# Starts the container (host networking on Linux by default so Ollama is reachable),
# waits for readiness, then runs only the external test(s) whose name includes 'External'.
e2e-test:
	@echo "Starting slmcache container for external e2e (Ollama required)..."
	SLM_REQUIRE_OLLAMA=1 DOCKER_NETWORK_MODE=$${DOCKER_NETWORK_MODE:-$(DEFAULT_NETWORK_MODE)} SLM_OLLAMA_URL=$${SLM_OLLAMA_URL:-$(DEFAULT_OLLAMA_URL)} docker compose up --build -d
	$(MAKE) wait-ready
	@echo "Running external e2e tests against running instance (localhost:8080)..."
	set -e; \
	ENFORCE_OLLAMA=1 go test -count=1 -v ./internal/e2e -run External; \
	rc=$$?; \
	if [ -z "${KEEP_CONTAINER:-}" ]; then \
		echo "Stopping containers..."; docker compose down; \
	else \
		echo "KEEP_CONTAINER set; leaving containers running"; \
	fi; \
	exit $$rc
