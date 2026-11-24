APP := lactssl
GO := go
# 统一使用静态链接构建，不依赖目标机器的 glibc 版本。
BUILD_ENV := CGO_ENABLED=0
NPM := npm
MODULE := lactssl
# 允许通过环境变量覆盖（例如 VERSION=1.2.3 make docker-context），
# 未指定时从 git describe 推导。
VERSION ?= $(shell ./scripts/version.sh 2>/dev/null)
ifeq ($(VERSION),)
VERSION := dev
endif
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_ARCH := $(shell $(GO) env GOOS)/$(shell $(GO) env GOARCH)
# 官方发布构建可注入发布公钥；自编译版本留空，由管理员在 config.yaml 中配置。
PROGRAM_PUBLIC_KEY ?=
LDFLAGS := -s -w -X '$(MODULE)/internal/api.Version=$(VERSION)' -X '$(MODULE)/internal/api.BuildTime=$(BUILD_TIME)' -X '$(MODULE)/internal/api.BuildArch=$(BUILD_ARCH)' $(if $(PROGRAM_PUBLIC_KEY),-X '$(MODULE)/internal/config.OfficialProgramPublicKey=$(PROGRAM_PUBLIC_KEY)')
# validator_novalidatefn：剔除 go-playground/validator 的 validateFn 反射校验器。
# 该反射调用会让链接器保留所有导出方法，实测使二进制增大约 5.9 MB。
TAGS := embed nomsgpack validator_novalidatefn
TAGS_NO_DNS := $(TAGS) nobuiltin_dns
TAGS_DNS_COMMON := $(TAGS) dns_providers_common
TAGS_DNS_MINIMAL := $(TAGS) dns_providers_minimal
DOCKER_ARCH ?= $(shell $(GO) env GOARCH)
DOCKER_CONTEXT := dist/docker/linux-$(DOCKER_ARCH)
.PHONY: all build web backend backend-no-dns backend-dns-common backend-dns-minimal plugins-test test-host test-providers test-sdk metadata-check dbconvert server-dev run dev dev-up dev-down dev-restart dev-status dev-logs dev-api dev-web size test sql-check csr-client-check typecheck tidy clean ci release i18n-check i18n-check-all docs-build docs-serve docker-context docker

all: build

web:
	cd web && $(NPM) ci && $(NPM) run build
	rm -rf internal/webui/dist
	cp -a web/dist internal/webui/dist
	$(GO) run ./scripts/compress-assets.go internal/webui/dist
	$(GO) run ./scripts/bundle-i18n.go

backend:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/server

# 极简构建：不编译 DNS 内置实现，仅保留上传插件能力。
backend-no-dns:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -tags "$(TAGS_NO_DNS)" -ldflags "$(LDFLAGS)" -o bin/$(APP)-no-dns ./cmd/server

# 常用 DNS provider 精简构建：保留 lego v5 代码路径，但只链接白名单。
backend-dns-common:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -tags "$(TAGS_DNS_COMMON)" -ldflags "$(LDFLAGS)" -o bin/$(APP)-dns-common ./cmd/server

# 最小 DNS 构建：仅保留 cloudflare，用于低资源环境。
backend-dns-minimal:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -tags "$(TAGS_DNS_MINIMAL)" -ldflags "$(LDFLAGS)" -o bin/$(APP)-dns-minimal ./cmd/server

# 开发模式二进制：不嵌入前端，运行时读取 web/dist，供 Vite 热更新使用。
dbconvert:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP)-dbconvert ./cmd/dbconvert

server-dev:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -tags "nomsgpack validator_novalidatefn" -ldflags "$(LDFLAGS)" -o bin/$(APP)-dev ./cmd/server

build: web backend

# Dockerfile 只复制外部构建好的 Linux 二进制，不安装前端/Go 工具链。
# 用法: VERSION=1.2.3 make docker-context
docker-context: web
	mkdir -p $(DOCKER_CONTEXT)
	$(BUILD_ENV) GOOS=linux GOARCH=$(DOCKER_ARCH) $(GO) build -trimpath -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(DOCKER_CONTEXT)/lactssl ./cmd/server

# 本地单架构镜像；多架构发布请用 ./scripts/release.sh <tag> --multiarch
docker: docker-context
	docker build --build-arg VERSION=$(VERSION) -t lactssl:local .

run:
	$(BUILD_ENV) $(GO) run -tags "nomsgpack validator_novalidatefn" ./cmd/server

dev:
	./scripts/dev.sh start

dev-up:
	./scripts/dev.sh start

dev-down:
	./scripts/dev.sh stop

dev-restart:
	./scripts/dev.sh restart

dev-status:
	./scripts/dev.sh status

dev-logs:
	./scripts/dev.sh logs

dev-api:
	LACTSSL_DEV_ORIGIN=1 $(BUILD_ENV) $(GO) run ./cmd/server

dev-web:
	cd web && $(NPM) run dev

test:
	$(GO) test ./...
	(cd internal/plugin/providers && $(GO) test ./...)

# ./plugins 是外部插件开发 SDK；官方实现位于 internal/plugin，随主模块编译与测试。
plugins-test:
	(cd plugins && $(GO) test ./...)

# 宿主运行期测试：不包含 228 个 DNS provider 包装。
test-host:
	$(GO) test ./internal/plugin/host/... ./internal/plugin/runtime/... ./internal/plugin/builtin/... ./internal/plugin/proto

# 官方 provider 实现测试（独立 module）。
test-providers:
	(cd internal/plugin/providers && $(GO) test ./...)

# 外部插件 SDK 测试。
test-sdk:
	(cd plugins && $(GO) test ./...)

# 校验 provider 元数据、i18n、图标与 plugins.json 是否一致。
metadata-check:
	cd internal/plugin && $(GO) run ./scripts/metadatacheck/main.go

# 分析生产二进制的体积构成（段大小 + 按包聚合的符号大小）。
size:
	./scripts/size-report.sh

sql-check:
	./scripts/check-sql-dialect.sh

csr-client-check:
	python3 -m py_compile scripts/csr_client.py scripts/check-csr-client-i18n.py scripts/tests/test_csr_client.py
	python3 scripts/check-csr-client-i18n.py
	python3 -m unittest discover -s scripts/tests -v

typecheck:
	cd web && $(NPM) run typecheck
	cd web && ./node_modules/.bin/tsc -p ../scripts/tsconfig.json

ci:
	./scripts/ci.sh

release:
	./scripts/release.sh $(TAG)

i18n-check:
	node scripts/check-i18n.ts

i18n-check-all:
	node scripts/check-i18n.ts --all

docs-build:
	zensical build -f docsite/mkdocs.yml --strict --clean
	zensical build -f docsite/mkdocs.zh.yml --strict
	zensical build -f docsite/mkdocs.zh-hant.yml --strict
	zensical build -f docsite/mkdocs.ja.yml --strict
	zensical build -f docsite/mkdocs.ru.yml --strict

docs-serve:
	./scripts/docs-serve.sh $(PORT)

tidy:
	$(GO) mod tidy
	(cd internal/plugin/providers && $(GO) mod tidy)
	(cd plugins && $(GO) mod tidy)
	cd web && $(NPM) install

clean:
	rm -rf bin web/dist internal/webui/dist internal/i18n/dist lactssl
