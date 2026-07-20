.DEFAULT_GOAL := build

SHELL := /bin/bash

APP_NAME := taiyiflow-cli
GO ?= go
NPM ?= npm
CGO_ENABLED ?= 0
DARWIN_CGO_ENABLED ?= 1
# linux/386 需要 CGO_ENABLED=1 以创建 extra M（cgoHasExtraM），
# 否则信号到达非 Go 线程时 sigFetchG 返回 nil，badsignal 直接 exit(2) 崩溃。
LINUX_386_CGO_ENABLED ?= 1
LINUX_386_CC ?= i686-linux-gnu-gcc
# linux/386 musl 变体：用于 Alpine Linux 等使用 musl libc 的目标系统。
# 使用 --platform linux/386 的 Alpine Go 容器进行原生编译，天然链接 musl libc。
LINUX_386_MUSL_CGO_ENABLED ?= 1
ALPINE_GO_IMAGE ?= docker.m.daocloud.io/golang:1.25-alpine
GO_RELEASE_FLAGS ?= -trimpath -buildvcs=false
# Overlay 补丁：针对 iSH（iPad 上的 x86 Linux 模拟器）环境，完全禁用
# linux/386 的 pidfd 支持。iSH 不实现 pidfd_open（syscall #434），且旧版
# （2023年10月前）对未实现系统调用发送 SIGSYS 信号，但 Go 的
# ignoreSIGSYS() 因 iSH 不设置 si_code=SYS_SECCOMP 而无法生效。
# 补丁使 checkPidfd() 直接返回 ENOSYS，让 Go 回退到 waitpid 方式。
# 仅在 linux/386 musl 构建中启用，通过 build-release-386-musl-local 传入。
# 注意：此处使用容器内路径，因为 build-release-386-musl-local 在 Docker 容器中执行。
PIDFD_OVERLAY_JSON ?= $(DOCKER_WORKDIR)/build/overlay/pidfd_overlay.json
BUILDINFO_PKG := snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo
BUILD_COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GO_BUILDINFO_LDFLAGS := -X $(BUILDINFO_PKG).Commit=$(BUILD_COMMIT) -X $(BUILDINFO_PKG).Date=$(BUILD_DATE)
GO_BUILDINFO_FLAGS := -ldflags "$(GO_BUILDINFO_LDFLAGS)"

export BUILD_COMMIT BUILD_DATE

BUILD_DIR := build
RELEASE_DIR := $(BUILD_DIR)/release
ROOT_DIR := $(CURDIR)
DOCKER_WORKDIR := /taiyiflow-cli
CTL_CMD := snz1dpctl

BROWSER_EXTENSION_DIR := browser-extension/chrome
BROWSER_EXTENSION_DIST_MANIFEST := $(BROWSER_EXTENSION_DIR)/dist/manifest.json
BROWSER_EXTENSION_NODE_MODULES_STAMP := $(BROWSER_EXTENSION_DIR)/node_modules/.package-lock.json
DOCKER_CMD := $(shell if command -v podman >/dev/null 2>&1; then echo podman; elif command -v docker >/dev/null 2>&1; then echo docker; fi)
GO_IMAGE ?= snz1.cn/dp/golang:1.25.0
NODE_IMAGE ?= snz1.cn/base/node:20-alpine
CONTAINER_GO_BIN_DIR ?= /usr/local/go/bin
TAIYIFLOW_COMPUTER_LIVE_BROWSER ?= chrome

RELEASE_CROSS_PLATFORMS ?= linux/amd64 linux/arm64 windows/amd64 windows/386 windows/arm64
RELEASE_DARWIN_PLATFORMS ?= darwin/amd64 darwin/arm64

.PHONY: help build build-all build-release build-release-cross build-release-386 build-release-386-local build-release-386-musl build-release-386-musl-local build-release-darwin-native verify-computer-live-darwin release browser-extension-dist browser-extension-dist-docker test clean check-go check-npm check-docker check-browser-extension-dist

help:
	@printf '%s\n' \
	  'make build                 Build current platform binary and refresh browser extension dist' \
	  'make build-all             Build full release set into build/release/ with local toolchains (darwin native requires macOS host)' \
	  'make build-release         Build full release set into build/release/ (linux/windows cross + darwin native on macOS host)' \
	  'make build-release-cross   Build linux/windows release binaries into build/release/' \
	  'make build-release-386     Build linux/386 release binary in container (with CGO enabled, glibc)' \
	  'make build-release-386-musl Build linux/386 musl release binary in container (for Alpine Linux)' \
	  'make build-release-darwin-native Build darwin release binaries on the current macOS host with CGO enabled' \
	  'make verify-computer-live-darwin Run macOS live pointer verification against a visible Chromium window' \
	  'make release               Build browser extension in container, then linux/windows cross binaries in container and darwin native binaries on the host' \
	  'make browser-extension-dist Refresh browser extension dist only' \
	  'make browser-extension-dist-docker Refresh browser extension dist via Docker/Podman' \
	  'make test                  Run all Go tests' \
	  'make clean                 Remove build outputs'

check-go:
	@command -v "$(GO)" >/dev/null 2>&1 || { echo 'go not found in PATH'; exit 1; }

check-npm:
	@command -v "$(NPM)" >/dev/null 2>&1 || { echo 'npm not found in PATH'; exit 1; }

check-docker:
	@test -n "$(DOCKER_CMD)" || { echo 'docker or podman not found in PATH'; exit 1; }
	@command -v "$(DOCKER_CMD)" >/dev/null 2>&1 || { echo 'docker or podman not found in PATH'; exit 1; }

check-browser-extension-dist:
	@test -f $(BROWSER_EXTENSION_DIST_MANIFEST) || { echo 'browser extension dist missing; run make browser-extension-dist or make release'; exit 1; }

$(BROWSER_EXTENSION_NODE_MODULES_STAMP): $(BROWSER_EXTENSION_DIR)/package.json $(BROWSER_EXTENSION_DIR)/package-lock.json
	@cd $(BROWSER_EXTENSION_DIR) && "$(NPM)" ci

$(BROWSER_EXTENSION_DIST_MANIFEST): $(BROWSER_EXTENSION_DIR)/manifest.json $(BROWSER_EXTENSION_DIR)/scripts/build.mjs $(wildcard $(BROWSER_EXTENSION_DIR)/src/*.ts) $(BROWSER_EXTENSION_NODE_MODULES_STAMP)
	@cd $(BROWSER_EXTENSION_DIR) && "$(NPM)" run build

browser-extension-dist: check-npm $(BROWSER_EXTENSION_DIST_MANIFEST)

browser-extension-dist-docker: check-docker
	@$(DOCKER_CMD) run --rm -v "$(ROOT_DIR):$(DOCKER_WORKDIR)" \
		$(NODE_IMAGE) \
		sh -lc "cd $(DOCKER_WORKDIR)/$(BROWSER_EXTENSION_DIR) && npm ci && npm run build"

build: check-go browser-extension-dist
	@"$(GO)" build $(GO_BUILDINFO_FLAGS) -o $(APP_NAME) .

build-all: browser-extension-dist build-release

build-release: check-go check-browser-extension-dist
	@if [[ "$$(uname -s)" != 'Darwin' ]]; then echo 'build-release 需要在 macOS 构建机上执行，以生成原生 darwin 发布产物；如只需 linux/windows，请改用 make build-release-cross'; exit 1; fi
	@$(MAKE) build-release-cross
	@$(MAKE) build-release-darwin-native

build-release-cross: check-go check-browser-extension-dist
	@mkdir -p $(RELEASE_DIR)
	@set -e; \
	for platform in $(RELEASE_CROSS_PLATFORMS); do \
		goos=$${platform%/*}; \
		goarch=$${platform#*/}; \
		ext=''; \
		if [[ "$$goos" == 'windows' ]]; then ext='.exe'; fi; \
		output='$(RELEASE_DIR)/$(APP_NAME)-'"$$goos"'-'"$$goarch"'"$$ext"; \
		echo "==> $$output"; \
		CGO_ENABLED=$(CGO_ENABLED) GOOS=$$goos GOARCH=$$goarch "$(GO)" build $(GO_RELEASE_FLAGS) $(GO_BUILDINFO_FLAGS) -o "$$output" .; \
	done

build-release-386: check-docker check-browser-extension-dist
	@mkdir -p $(RELEASE_DIR)
	@$(DOCKER_CMD) run --rm -v "$(ROOT_DIR):$(DOCKER_WORKDIR)" \
		-e LINUX_386_CGO_ENABLED=$(LINUX_386_CGO_ENABLED) \
		-e LINUX_386_CC=$(LINUX_386_CC) \
		-e BUILD_COMMIT="$(BUILD_COMMIT)" \
		-e BUILD_DATE="$(BUILD_DATE)" \
		-e GODEBUG="invalidcpu=ignore" \
		$(GO_IMAGE) \
		bash -lc 'export PATH="$(CONTAINER_GO_BIN_DIR):$$PATH"; \
			apt-get update -qq && apt-get install -y -qq gcc-i686-linux-gnu libc6-dev-i386-cross >/dev/null; \
			cd $(DOCKER_WORKDIR) && make build-release-386-local'

build-release-386-local: check-go check-browser-extension-dist
	@output='$(RELEASE_DIR)/$(APP_NAME)-linux-386'; \
	echo "==> $$output"; \
	CGO_ENABLED=$(LINUX_386_CGO_ENABLED) CC=$(LINUX_386_CC) GOOS=linux GOARCH=386 \
		"$(GO)" build $(GO_RELEASE_FLAGS) $(GO_BUILDINFO_FLAGS) -o "$$output" .

build-release-386-musl: check-docker check-browser-extension-dist
	@mkdir -p $(RELEASE_DIR)
	@$(DOCKER_CMD) run --rm --platform linux/386 -v "$(ROOT_DIR):$(DOCKER_WORKDIR)" \
		-e BUILD_COMMIT="$(BUILD_COMMIT)" \
		-e BUILD_DATE="$(BUILD_DATE)" \
		-e GODEBUG="invalidcpu=ignore" \
		-e GOPROXY="https://goproxy.cn,direct" \
		$(ALPINE_GO_IMAGE) \
		sh -lc 'apk add --no-cache make gcc musl-dev >/dev/null 2>&1; \
			export PATH=$(CONTAINER_GO_BIN_DIR):$$PATH; \
			cd $(DOCKER_WORKDIR) && make SHELL=/bin/sh build-release-386-musl-local'

build-release-386-musl-local: check-go check-browser-extension-dist
	@output='$(RELEASE_DIR)/$(APP_NAME)-linux-386-musl'; \
	echo "==> $$output"; \
	CGO_ENABLED=$(LINUX_386_MUSL_CGO_ENABLED) GOOS=linux GOARCH=386 \
		"$(GO)" build $(GO_RELEASE_FLAGS) $(GO_BUILDINFO_FLAGS) \
		-overlay "$(PIDFD_OVERLAY_JSON)" \
		-o "$$output" .

build-release-darwin-native: check-go check-browser-extension-dist
	@if [[ "$$(uname -s)" != 'Darwin' ]]; then echo 'build-release-darwin-native 仅支持在 macOS 构建机上执行'; exit 1; fi
	@mkdir -p $(RELEASE_DIR)
	@set -e; \
	for platform in $(RELEASE_DARWIN_PLATFORMS); do \
		goarch=$${platform#*/}; \
		output='$(RELEASE_DIR)/$(APP_NAME)-darwin-'"$$goarch"; \
		echo "==> $$output"; \
		CGO_ENABLED=$(DARWIN_CGO_ENABLED) GOOS=darwin GOARCH=$$goarch "$(GO)" build $(GO_RELEASE_FLAGS) $(GO_BUILDINFO_FLAGS) -o "$$output" .; \
	done

verify-computer-live-darwin: check-go
	@if [[ "$$(uname -s)" != 'Darwin' ]]; then echo 'verify-computer-live-darwin 仅支持在 macOS 上执行'; exit 1; fi
	@TAIYIFLOW_COMPUTER_LIVE_TEST=1 TAIYIFLOW_COMPUTER_LIVE_BROWSER=$(TAIYIFLOW_COMPUTER_LIVE_BROWSER) "$(GO)" test ./internal/computerbridge -run TestDarwinLivePointerVerification -count=1 -v

release: check-docker browser-extension-dist-docker
	@if [[ "$$(uname -s)" != 'Darwin' ]]; then echo 'make release 需要在 macOS 构建机上执行，以生成原生 darwin 发布产物'; exit 1; fi
	@$(DOCKER_CMD) run --rm -v "$(ROOT_DIR):$(DOCKER_WORKDIR)" \
		-e CGO_ENABLED=$(CGO_ENABLED) \
		-e BUILD_COMMIT="$(BUILD_COMMIT)" \
		-e BUILD_DATE="$(BUILD_DATE)" \
		$(GO_IMAGE) \
		bash -lc 'export PATH="$(CONTAINER_GO_BIN_DIR):$$PATH"; cd $(DOCKER_WORKDIR) && touch -d "@$$(date +%s)" Makefile && make build-release-cross'
	@$(MAKE) build-release-darwin-native

test: check-go
	@"$(GO)" test ./...

clean:
	@rm -rf $(BUILD_DIR) $(APP_NAME)
