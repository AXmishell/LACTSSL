APP := lactssl
GO := go
NPM := npm
MODULE := lactssl
VERSION := $(shell cat VERSION 2>/dev/null | tr -d '[:space:]')
ifeq ($(VERSION),)
VERSION := dev
endif
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_ARCH := $(shell $(GO) env GOOS)/$(shell $(GO) env GOARCH)
LDFLAGS := -s -w -X '$(MODULE)/internal/api.Version=$(VERSION)' -X '$(MODULE)/internal/api.BuildTime=$(BUILD_TIME)' -X '$(MODULE)/internal/api.BuildArch=$(BUILD_ARCH)'

.PHONY: all build web backend run dev dev-api dev-web test typecheck tidy clean ci i18n-check

all: build

web:
	cd web && $(NPM) install && $(NPM) run build
	rm -rf internal/webui/dist
	cp -a web/dist internal/webui/dist

backend:
	mkdir -p bin
	$(GO) build -trimpath -tags embed -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/server

build: web backend

run:
	$(GO) run ./cmd/server

dev:
	./scripts/dev.sh

dev-api:
	$(GO) run ./cmd/server

dev-web:
	cd web && $(NPM) run dev

test:
	$(GO) test ./...

typecheck:
	cd web && $(NPM) run typecheck
	cd web && ./node_modules/.bin/tsc -p ../scripts/tsconfig.json

ci:
	./scripts/ci.sh

i18n-check:
	node scripts/check-i18n.ts

tidy:
	$(GO) mod tidy
	cd web && $(NPM) install

clean:
	rm -rf bin web/dist internal/webui/dist lactssl
