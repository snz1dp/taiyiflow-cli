.DEFAULT_GOAL := build

SHELL := /bin/bash

APP_NAME := taiyiflow-cli
GO ?= go
NPM ?= npm
CGO_ENABLED ?= 0

BUILD_DIR := build
RELEASE_DIR := $(BUILD_DIR)/release

BROWSER_EXTENSION_DIR := browser-extension/chrome
BROWSER_EXTENSION_DIST_MANIFEST := $(BROWSER_EXTENSION_DIR)/dist/manifest.json
BROWSER_EXTENSION_NODE_MODULES_STAMP := $(BROWSER_EXTENSION_DIR)/node_modules/.package-lock.json

PLATFORMS ?= darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: help build build-all release browser-extension-dist test clean check-go check-npm

help:
	@printf '%s\n' \
	  'make build                 Build current platform binary and refresh browser extension dist' \
	  'make build-all             Build multi-platform binaries into build/release/' \
	  'make browser-extension-dist Refresh browser extension dist only' \
	  'make test                  Run all Go tests' \
	  'make clean                 Remove build outputs'

check-go:
	@command -v $(GO) >/dev/null 2>&1 || { echo 'go not found in PATH'; exit 1; }

check-npm:
	@command -v $(NPM) >/dev/null 2>&1 || { echo 'npm not found in PATH'; exit 1; }

$(BROWSER_EXTENSION_NODE_MODULES_STAMP): $(BROWSER_EXTENSION_DIR)/package.json $(BROWSER_EXTENSION_DIR)/package-lock.json
	@cd $(BROWSER_EXTENSION_DIR) && $(NPM) ci

$(BROWSER_EXTENSION_DIST_MANIFEST): $(BROWSER_EXTENSION_DIR)/manifest.json $(BROWSER_EXTENSION_DIR)/scripts/build.mjs $(wildcard $(BROWSER_EXTENSION_DIR)/src/*.ts) $(BROWSER_EXTENSION_NODE_MODULES_STAMP)
	@cd $(BROWSER_EXTENSION_DIR) && $(NPM) run build

browser-extension-dist: check-npm $(BROWSER_EXTENSION_DIST_MANIFEST)

build: check-go browser-extension-dist
	@$(GO) build -o $(APP_NAME) .

build-all: check-go browser-extension-dist
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

release: build-all

test: check-go
	@$(GO) test ./...

clean:
	@rm -rf $(BUILD_DIR) $(APP_NAME)
