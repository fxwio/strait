SHELL := /bin/bash

APP_NAME := strait
BUILD_DIR := build
BINARY := $(BUILD_DIR)/$(APP_NAME)
GO ?= go
IMAGE_REPO ?= strait
IMAGE_TAG ?= local

GOFLAGS := -trimpath
LDFLAGS := -s -w

.PHONY: clean build test benchmark run deploy down smoke image deploy-image release-selftest release-real-smoke

clean:
	rm -rf $(BUILD_DIR)

build:
	mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/gateway/main.go

image:
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

test:
	$(GO) test ./...

benchmark:
	$(GO) test -run '^$$' -bench . -benchmem ./benchmark/...

## run — 一键启动本地开发环境 (使用 .env.example 和 config.example.yaml 如果本地文件不存在)
run: build
	@test -f .env || cp .env.example .env
	@test -f config.yaml || cp config.example.yaml config.yaml
	@echo "Starting $(APP_NAME)..."
	@export $$(grep -v '^#' .env | xargs) && ./$(BINARY)

## deploy — 一键容器化部署
deploy:
	@test -f .env || cp .env.example .env
	@test -f config.yaml || cp config.example.yaml config.yaml
	docker compose up --build -d
	@echo "Gateway deployed at http://localhost:8080"

deploy-image:
	@test -f .env || cp .env.example .env
	@test -f config.yaml || cp config.example.yaml config.yaml
	@if [ -z "$(IMAGE_TAG)" ] || [ "$(IMAGE_TAG)" = "local" ]; then \
		echo "IMAGE_TAG must be an explicit non-local release tag for deploy-image"; \
		exit 1; \
	fi
	GATEWAY_IMAGE_REPO=$(IMAGE_REPO) GATEWAY_IMAGE_TAG=$(IMAGE_TAG) docker compose up -d
	@echo "Gateway deployed from image $(IMAGE_REPO):$(IMAGE_TAG)"

down:
	docker compose down

smoke:
	@bash scripts/smoke.sh

release-selftest:
	@bash scripts/release_selftest.sh

release-real-smoke:
	@bash scripts/release_real_provider_smoke.sh
