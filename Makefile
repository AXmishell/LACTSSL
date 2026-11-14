APP := lactssl
GO := go
# 统一使用静态链接构建，不依赖目标机器的 glibc 版本。
BUILD_ENV := CGO_ENABLED=0
NPM := npm
MODULE := lactssl
VERSION := $(shell ./scripts/version.sh 2>/dev/null)
ifeq ($(VERSION),)
VERSION := dev
endif
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_ARCH := $(shell $(GO) env GOOS)/$(shell $(GO) env GOARCH)
LDFLAGS := -s -w -X '$(MODULE)/internal/api.Version=$(VERSION)' -X '$(MODULE)/internal/api.BuildTime=$(BUILD_TIME)' -X '$(MODULE)/internal/api.BuildArch=$(BUILD_ARCH)'
TAGS := embed
FULL_TAGS := embed dns_all
# 部署插件安装目录；可通过 make deploy-helper-install DEPLOY_PLUGIN_DIR=... 覆盖。
DEPLOY_PLUGIN_DIR ?= data/plugins/deploy

.PHONY: all build build-full web backend backend-full helper helper-extra helper-full deploy-helper deploy-helper-install gen-plugins run dev dev-api dev-web rebuild-run test typecheck tidy clean ci release i18n-check docs-build docs-serve

all: build

web:
	cd web && $(NPM) install && $(NPM) run build
	rm -rf internal/webui/dist
	cp -a web/dist internal/webui/dist

backend:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/server

# 全量版包含全部 lego DNS 服务商（dns_all），体积更大，适合需要冷门服务商的部署。
backend-full:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -tags "$(FULL_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(APP)-full ./cmd/server

build: web backend

build-full: web backend-full

# 外部插件参考实现：默认构建不内置服务商，helper 必须带 dns_extra 才能
# 提供全部 lego 服务商；helper-extra 与 helper 等价，保留旧名称兼容脚本。
helper:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -tags dns_extra -ldflags "$(LDFLAGS)" -o bin/$(APP)-dns-helper ./cmd/dns-helper

# DNS 插件集合：覆盖 lego 全部服务商，与默认主程序互补。
helper-extra:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -tags dns_extra -ldflags "$(LDFLAGS)" -o bin/$(APP)-dns-helper-extra ./cmd/dns-helper

helper-full:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -tags dns_all -ldflags "$(LDFLAGS)" -o bin/$(APP)-dns-helper-full ./cmd/dns-helper

# 部署插件：提供本地文件与 SSH 之外的部署目标（FTP、Webhook、CDN、面板等）。
deploy-helper:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP)-deploy-helper ./cmd/deploy-helper

# 构建并安装到部署插件目录，安装后重扫插件即可使用。
deploy-helper-install: deploy-helper
	mkdir -p "$(DEPLOY_PLUGIN_DIR)"
	install -m 0755 bin/$(APP)-deploy-helper "$(DEPLOY_PLUGIN_DIR)/deploy-helper"

# 重新生成 DNS 插件包装包与构建 profile。
gen-plugins:
	$(GO) run scripts/gen_dns_plugins/main.go

run:
	$(BUILD_ENV) $(GO) run ./cmd/server

dev:
	./scripts/dev.sh

rebuild-run:
	./scripts/rebuild-run.sh

dev-api:
	$(BUILD_ENV) $(GO) run ./cmd/server

dev-web:
	cd web && $(NPM) run dev

test:
	$(GO) test ./...

typecheck:
	cd web && $(NPM) run typecheck
	cd web && ./node_modules/.bin/tsc -p ../scripts/tsconfig.json

ci:
	./scripts/ci.sh

release:
	./scripts/release.sh $(TAG)

i18n-check:
	node scripts/check-i18n.ts

docs-build:
	zensical build --strict --clean
	zensical build -f mkdocs.zh.yml --strict

docs-serve:
	./scripts/docs-serve.sh $(PORT)

tidy:
	$(GO) mod tidy
	cd web && $(NPM) install

clean:
	rm -rf bin web/dist internal/webui/dist lactssl
