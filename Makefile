# veilon-app (Veil Connect: veilond, desktop, Android; GPL-3.0-or-later).
# Needs the veil-shared checkout next to this repository (ADR-0074).
SHELL := /bin/bash
.ONESHELL:
.DEFAULT_GOAL := help

GO         ?= go
# Go >= 1.26 for desktop/ (Wails v3 requirement)
GO126      ?= go
export GOWORK := off
SHARED_DIR ?= ../veil-shared
BIN_DIR    := $(CURDIR)/bin
VERSION    ?= 0.7.0

.PHONY: help
help: ## Show targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-16s %s\n",$$1,$$2}'

.PHONY: build
build: ## veilond, veilon-updater, veilonctl into ./bin
	mkdir -p $(BIN_DIR)
	cd veilond && $(GO) build -trimpath -ldflags "-X main.Version=$(VERSION)" -o $(BIN_DIR)/ ./cmd/...

.PHONY: test
test: ## Unit tests
	cd veilond && $(GO) test -race -count=1 ./...

.PHONY: vet
vet: ## go vet
	cd veilond && $(GO) vet ./...

.PHONY: lint
lint: ## golangci-lint on Linux, macOS and Windows builds (privileged code differs per OS)
	cd veilond && golangci-lint run ./... && GOOS=darwin golangci-lint run ./... && GOOS=windows golangci-lint run ./...

.PHONY: nolog
nolog: ## Zero-log tests of veilond against the pinned sing-box (make -C ../veil-shared singbox)
	$(MAKE) -C $(SHARED_DIR) singbox
	cd veilond && SINGBOX_BIN=$$(realpath $(SHARED_DIR))/.cache/sing-box/sing-box-*-linux-amd64/sing-box \
	  $(GO) test -count=1 -run 'NoLog|Golden|Redact|Connections|DNS' ./...
	cd $(SHARED_DIR) && $(GO) run ./cmd/trackerscan -root $(CURDIR)

.PHONY: repro
repro: ## Two clean builds must be byte-identical
	for i in 1 2; do rm -rf .cache/repro$$i && mkdir -p .cache/repro$$i; \
	  (cd veilond && CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w -buildid=" -o ../.cache/repro$$i/ ./cmd/...) || exit 1; done
	diff <(cd .cache/repro1 && sha256sum *) <(cd .cache/repro2 && sha256sum *) && echo "reproducible: OK"

.PHONY: desktop
desktop: ## Desktop app (Wails v3; Go >= 1.26 and WebKitGTK/WebView2 headers)
	cd desktop/frontend && npm ci && npm run build
	cd desktop && $(GO126) build -o $(BIN_DIR)/veilon .

.PHONY: android
android: ## AAR (libbox + veil-shared/mobile) and debug APK in the pinned toolchain image
	android/scripts/build-aar.sh --docker
	android/scripts/fetch-xray.sh
	android/scripts/docker.sh gradle --no-daemon :app:assembleDebug

.PHONY: packages
packages: ## deb and rpm (SINGBOX_BIN, XRAY_BIN required), see tools/package
	tools/package/build-linux.sh

.PHONY: update-test
update-test: ## Silent update round trip (root; needs the veil-api checkout for veilon-keygen)
	tools/package/test-linux-update.sh

.PHONY: brand
brand: ## Copy the brand assets from veil-shared/brand/dist into the apps
	cp $(SHARED_DIR)/brand/dist/icon-256.png desktop/build/appicon.png
	cp $(SHARED_DIR)/brand/dist/veilon.ico desktop/build/windows/icon.ico
	cp $(SHARED_DIR)/brand/dist/veilon.icns desktop/build/darwin/icons.icns
	cp $(SHARED_DIR)/brand/dist/icon-512.png desktop/build/linux/veilon.png
	mkdir -p desktop/frontend/public && cp $(SHARED_DIR)/brand/dist/favicon.ico desktop/frontend/public/

.PHONY: nodash
nodash: ## Typography: no em dashes (ADR-0058)
	python3 $(SHARED_DIR)/tools/text/nodash.py --check --root .

.PHONY: ci
ci: vet lint test nolog nodash repro ## Everything CI runs here
