.DEFAULT_GOAL := build

SHELL := /bin/bash

APP_NAME := taiyiflow-cli
GO ?= go
NPM ?= npm
CGO_ENABLED ?= 0

BUILD_DIR := build
RELEASE_DIR := $(BUILD_DIR)/release
ROOT_DIR := $(CURDIR)
DOCKER_WORKDIR := /tayiflow-cli

BROWSER_EXTENSION_DIR := browser-extension/chrome
BROWSER_EXTENSION_DIST_MANIFEST := $(BROWSER_EXTENSION_DIR)/dist/manifest.json
BROWSER_EXTENSION_NODE_MODULES_STAMP := $(BROWSER_EXTENSION_DIR)/node_modules/.package-lock.json
DOCKER_CMD:=$(shell if [ -x "$(command -v podman)" ]; then echo podman; else echo docker; fi)
GO_IMAGE ?= snz1.cn/dp/golang:1.25.0
NODE_IMAGE ?= snz1.cn/base/node:20-alpine

PLATFORMS ?= darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: help build build-all build-release release browser-extension-dist browser-extension-dist-docker test clean check-go check-npm check-docker check-browser-extension-dist

help:
	@printf '%s\n' \
	  'make build                 Build current platform binary and refresh browser extension dist' \
	  'make build-all             Build multi-platform binaries into build/release/ with local toolchains' \
	  'make build-release         Build multi-platform binaries into build/release/ (requires browser extension dist)' \
	  'make release               Build browser extension and release binaries via Docker/Podman' \
	  'make browser-extension-dist Refresh browser extension dist only' \
	  'make browser-extension-dist-docker Refresh browser extension dist via Docker/Podman' \
	  'make test                  Run all Go tests' \
	  'make clean                 Remove build outputs'

check-go:
	@command -v $(GO) >/dev/null 2>&1 || { echo 'go not found in PATH'; exit 1; }

check-npm:
	@command -v $(NPM) >/dev/null 2>&1 || { echo 'npm not found in PATH'; exit 1; }

check-docker:
	@command -v $(DOCKER_CMD) >/dev/null 2>&1 || { echo 'docker or podman not found in PATH'; exit 1; }

check-browser-extension-dist:
	@test -f $(BROWSER_EXTENSION_DIST_MANIFEST) || { echo 'browser extension dist missing; run make browser-extension-dist or make release'; exit 1; }

$(BROWSER_EXTENSION_NODE_MODULES_STAMP): $(BROWSER_EXTENSION_DIR)/package.json $(BROWSER_EXTENSION_DIR)/package-lock.json
	@cd $(BROWSER_EXTENSION_DIR) && $(NPM) ci

$(BROWSER_EXTENSION_DIST_MANIFEST): $(BROWSER_EXTENSION_DIR)/manifest.json $(BROWSER_EXTENSION_DIR)/scripts/build.mjs $(wildcard $(BROWSER_EXTENSION_DIR)/src/*.ts) $(BROWSER_EXTENSION_NODE_MODULES_STAMP)
	@cd $(BROWSER_EXTENSION_DIR) && $(NPM) run build

browser-extension-dist: check-npm $(BROWSER_EXTENSION_DIST_MANIFEST)

browser-extension-dist-docker: check-docker
	@$(DOCKER_CMD) run --rm -v "$(ROOT_DIR):$(DOCKER_WORKDIR)" \
		$(NODE_IMAGE) \
		sh -lc "cd $(DOCKER_WORKDIR)/$(BROWSER_EXTENSION_DIR) && $(NPM) ci && $(NPM) run build"

build: check-go browser-extension-dist
	@$(GO) build -o $(APP_NAME) .

build-all: browser-extension-dist build-release

build-release: check-go check-browser-extension-dist
	@mkdir -p $(RELEASE_DIR)
	@set -e; \
	for platform in $(PLATFORMS); do \
		goos=$${platform%/*}; \
		goarch=$${platform#*/}; \
		ext=''; \
		if [[ "$$goos" == 'windows' ]]; then ext='.exe'; fi; \
		output='$(RELEASE_DIR)/$(APP_NAME)-'"$$goos"'-'"$$goarch""$$ext"; \
		echo "==> $$output"; \
		CGO_ENABLED=$(CGO_ENABLED) GOOS=$$goos GOARCH=$$goarch $(GO) build -trimpath -o "$$output" .; \
	done

release: check-docker browser-extension-dist-docker
	@$(DOCKER_CMD) run --rm -v "$(ROOT_DIR):$(DOCKER_WORKDIR)" \
		-e CGO_ENABLED=$(CGO_ENABLED) \
		-e GODEBUG="invalidcpu=ignore" \
		$(GO_IMAGE) \
		bash -lc "cd $(DOCKER_WORKDIR) && make build-release"

test: check-go
	@$(GO) test ./...

clean:
	@rm -rf $(BUILD_DIR) $(APP_NAME)
