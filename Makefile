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
# 官方发布构建可注入发布公钥；自编译版本留空，由管理员在 config.yaml 中配置。
PROGRAM_PUBLIC_KEY ?=
LDFLAGS := -s -w -X '$(MODULE)/internal/api.Version=$(VERSION)' -X '$(MODULE)/internal/api.BuildTime=$(BUILD_TIME)' -X '$(MODULE)/internal/api.BuildArch=$(BUILD_ARCH)' $(if $(PROGRAM_PUBLIC_KEY),-X '$(MODULE)/internal/config.OfficialProgramPublicKey=$(PROGRAM_PUBLIC_KEY)')
TAGS := embed
.PHONY: all build web backend dbconvert server-dev run dev dev-up dev-down dev-restart dev-status dev-logs dev-api dev-web test sql-check csr-client-check typecheck tidy clean ci release i18n-check i18n-check-all docs-build docs-serve

all: build

web:
	cd web && $(NPM) ci && $(NPM) run build
	rm -rf internal/webui/dist
	cp -a web/dist internal/webui/dist

backend:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/server

# 开发模式二进制：不嵌入前端，运行时读取 web/dist，供 Vite 热更新使用。
dbconvert:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP)-dbconvert ./cmd/dbconvert

server-dev:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP)-dev ./cmd/server

build: web backend

run:
	$(BUILD_ENV) $(GO) run ./cmd/server

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
	zensical build --strict --clean
	zensical build -f mkdocs.zh.yml --strict
	zensical build -f mkdocs.zh-hant.yml --strict
	zensical build -f mkdocs.ja.yml --strict
	zensical build -f mkdocs.ru.yml --strict

docs-serve:
	./scripts/docs-serve.sh $(PORT)

tidy:
	$(GO) mod tidy
	cd web && $(NPM) install

clean:
	rm -rf bin web/dist internal/webui/dist lactssl
