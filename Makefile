GO     := $(shell command -v go)
MODULE := github.com/OneDayX/gophkeeper

SERVER_BINARY := bin/gophkeeper-server
CLIENT_BINARY := bin/gophkeeper
DIST_DIR      := dist

# Build info printed by `gophkeeper version` and logged by the server at
# start, override as `make build BUILD_VERSION=v1.0.0`.
BUILD_VERSION ?= $(or $(shell git describe --tags --always 2>/dev/null),N/A)
BUILD_DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_COMMIT  ?= $(or $(shell git rev-parse --short HEAD 2>/dev/null),N/A)
LDFLAGS := -s -w \
	-X main.buildVersion=$(BUILD_VERSION) \
	-X main.buildDate=$(BUILD_DATE) \
	-X main.buildCommit=$(BUILD_COMMIT)

# Platforms the client is released for, as GOOS/GOARCH.
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

GRPC_ADDRESS ?= localhost:3200
HTTP_ADDRESS ?= localhost:3201
DEV_AUTH_KEY ?= dev-auth-key

PG_CONTAINER ?= gophkeeper-pg
PG_IMAGE     ?= postgres:16-alpine
PG_PORT      ?= 5435
DATABASE_DSN ?= postgres://postgres:postgres@localhost:$(PG_PORT)/gophkeeper?sslmode=disable

COVER_FILE ?= coverage.out
# Generated code is not ours to test, it stays out of the coverage.
COVER_EXCLUDE := \.pb(\.gw)?\.go:

CERTS_DIR ?= certs

# protoc itself is not a Go program: install it from your package manager or
# https://github.com/protocolbuffers/protobuf/releases. The plugins come
# from `make tools`.
PROTOC    ?= protoc
TOOLS_BIN := $(CURDIR)/bin/tools
PROTO_DIR := api/proto

.DEFAULT_GOAL := help
.PHONY: help build build-server build-client dist run-server run-server-db run-server-tls certs \
	tools proto test test-short test-db cover cover-nodb vet fmt db-up db-down docker-up docker-down clean

help: ## Show this help
	@grep -E '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: build-server build-client ## Build the server and the client for this machine into bin/

build-server: ## Build the server
	$(GO) build -ldflags '$(LDFLAGS)' -o $(SERVER_BINARY) ./cmd/gophkeeper-server

build-client: ## Build the client
	$(GO) build -ldflags '$(LDFLAGS)' -o $(CLIENT_BINARY) ./cmd/gophkeeper

dist: ## Build the client for Linux, macOS and Windows into dist/
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; ext=; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		out=$(DIST_DIR)/gophkeeper-$$os-$$arch$$ext; \
		echo "$$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -ldflags '$(LDFLAGS)' -o $$out ./cmd/gophkeeper || exit 1; \
	done

run-server: build-server ## Run the server with the data in memory, without TLS
	$(SERVER_BINARY) -a $(GRPC_ADDRESS) -http $(HTTP_ADDRESS) -k $(DEV_AUTH_KEY)

run-server-db: build-server db-up ## Run the server with the data in the local postgres
	$(SERVER_BINARY) -a $(GRPC_ADDRESS) -http $(HTTP_ADDRESS) -k $(DEV_AUTH_KEY) -d '$(DATABASE_DSN)'

run-server-tls: build-server ## Run the server over TLS with the certificate from `make certs`
	$(SERVER_BINARY) -a $(GRPC_ADDRESS) -http $(HTTP_ADDRESS) -k $(DEV_AUTH_KEY) \
		-tls-cert $(CERTS_DIR)/server.pem -tls-key $(CERTS_DIR)/server.key

certs: ## Make a development CA and a certificate for localhost in certs/
	mkdir -p $(CERTS_DIR)
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 365 \
		-subj "/CN=GophKeeper development CA" -keyout $(CERTS_DIR)/ca.key -out $(CERTS_DIR)/ca.pem
	openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
		-subj "/CN=localhost" -keyout $(CERTS_DIR)/server.key -out $(CERTS_DIR)/server.csr
	printf 'subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' > $(CERTS_DIR)/server.ext
	openssl x509 -req -in $(CERTS_DIR)/server.csr -CA $(CERTS_DIR)/ca.pem -CAkey $(CERTS_DIR)/ca.key \
		-CAcreateserial -days 365 -extfile $(CERTS_DIR)/server.ext -out $(CERTS_DIR)/server.pem
	rm -f $(CERTS_DIR)/server.csr $(CERTS_DIR)/server.ext $(CERTS_DIR)/ca.srl
	@echo "server: -tls-cert $(CERTS_DIR)/server.pem -tls-key $(CERTS_DIR)/server.key; client: --ca-cert $(CERTS_DIR)/ca.pem"

tools: ## Install the protoc plugins of the pinned versions into bin/tools
	GOBIN=$(TOOLS_BIN) $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
	GOBIN=$(TOOLS_BIN) $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
	GOBIN=$(TOOLS_BIN) $(GO) install \
		github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.32.0 \
		github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v2.32.0

proto: ## Generate the gRPC, gateway and Swagger code from api/proto (needs protoc and `make tools`)
	PATH="$(TOOLS_BIN):$$PATH" $(PROTOC) -I $(PROTO_DIR) \
		--go_out=. --go_opt=module=$(MODULE) \
		--go-grpc_out=. --go-grpc_opt=module=$(MODULE) \
		--grpc-gateway_out=. --grpc-gateway_opt=module=$(MODULE) \
		--openapiv2_out=api/openapi --openapiv2_opt=allow_merge=true,merge_file_name=gophkeeper \
		$(PROTO_DIR)/gophkeeper/v1/gophkeeper.proto

test: ## Run the tests under the race detector (database tests are skipped)
	$(GO) test -race ./...

test-short: ## Run the tests but the functional ones, which build the binaries
	$(GO) test -short ./...

# Packages with database tests empty the same tables, so they run one by one.
test-db: db-up ## Run all tests under the race detector, including the ones that need postgres
	TEST_DATABASE_DSN='$(DATABASE_DSN)' $(GO) test -race -p 1 ./...

cover: db-up ## Print the coverage of every function and the total, database tests included
	TEST_DATABASE_DSN='$(DATABASE_DSN)' $(GO) test -p 1 -coverprofile=$(COVER_FILE).all ./... >/dev/null
	@grep -v -E '$(COVER_EXCLUDE)' $(COVER_FILE).all > $(COVER_FILE) && rm $(COVER_FILE).all
	$(GO) tool cover -func=$(COVER_FILE) | tail -1

cover-nodb: ## Print the total coverage without postgres
	$(GO) test -coverprofile=$(COVER_FILE).all ./... >/dev/null
	@grep -v -E '$(COVER_EXCLUDE)' $(COVER_FILE).all > $(COVER_FILE) && rm $(COVER_FILE).all
	$(GO) tool cover -func=$(COVER_FILE) | tail -1

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format the source tree
	$(GO) fmt ./...

# pg_isready goes over TCP: while the image initialises the database, it runs
# a temporary server that listens on the unix socket only.
db-up: ## Start the local postgres
	@docker inspect -f '{{.State.Running}}' $(PG_CONTAINER) 2>/dev/null | grep -q true \
		|| docker run -d --rm --name $(PG_CONTAINER) \
			-e POSTGRES_USER=postgres \
			-e POSTGRES_PASSWORD=postgres \
			-e POSTGRES_DB=gophkeeper \
			-p $(PG_PORT):5432 $(PG_IMAGE)
	@until docker exec $(PG_CONTAINER) pg_isready -h localhost -U postgres >/dev/null 2>&1; do sleep 1; done
	@echo "postgres ready on localhost:$(PG_PORT)"

db-down: ## Stop the local postgres
	-docker rm -f $(PG_CONTAINER)

docker-up: ## Run the server with postgres in Docker Compose
	docker compose up --build -d
	@echo "gRPC on localhost:3200, REST and Swagger on http://localhost:3201/docs"

docker-down: ## Stop Docker Compose
	docker compose down

clean: ## Remove the binaries and the coverage profile
	rm -rf bin/gophkeeper bin/gophkeeper-server $(DIST_DIR) $(COVER_FILE)
