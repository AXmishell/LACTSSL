APP := lactssl
GO := go
BUILD_ENV := CGO_ENABLED=0
LDFLAGS := -s -w
BIN := bin

.PHONY: all build bundle bundle-lego-v5 generated catalog i18n deployimports gen gen-v5 manifest plugins test fmt vet check clean index validate-index sign-index verify-index

all: build

build: bundle

# 合并插件包：DNS 只发布 all-lego-v5，部署与通知各一个包，
# 共享各自进程内的 Go 运行时。
bundle:
	mkdir -p $(BIN)
	$(BUILD_ENV) $(GO) build -trimpath -tags "nethttpomithttp2,http2legacy,urfave_cli_no_template,ozzo_validation_no_template,grpcnotrace,validator_novalidatefn" -ldflags "$(LDFLAGS)" -o $(BIN)/$(APP)-dns-all-lego-v5 ./cmd/dns-helper-v5
	$(BUILD_ENV) $(GO) build -trimpath -tags nethttpomithttp2,http2legacy -ldflags "$(LDFLAGS)" -o $(BIN)/$(APP)-deploy-all ./cmd/deploy-helper
	$(BUILD_ENV) $(GO) build -trimpath -tags nethttpomithttp2,http2legacy -ldflags "$(LDFLAGS)" -o $(BIN)/$(APP)-notify-all ./cmd/notify-helper

# all-lego-v5 单独构建：全部服务商使用 lego v5 实现。
# DCE 标签与上游 lego v5 release 保持一致，避免 validator/ozzo 模板代码进入二进制。
bundle-lego-v5:
	mkdir -p $(BIN)
	$(BUILD_ENV) $(GO) build -trimpath -tags "nethttpomithttp2,http2legacy,urfave_cli_no_template,ozzo_validation_no_template,grpcnotrace,validator_novalidatefn" -ldflags "$(LDFLAGS)" -o $(BIN)/$(APP)-dns-all-lego-v5 ./cmd/dns-helper-v5

# 生成所有聚合文件。各聚合文件都来自单 provider 源文件，禁止手改。
generated: catalog i18n deployimports gen-v5

# 把 catalog/dns、catalog/deploy 合并为 catalog/data.json。
catalog:
	$(GO) run scripts/cataloggen/main.go

# 把 i18n/core、i18n/providers 合并为 i18n/locales。
i18n:
	$(GO) run scripts/i18ngen/main.go

# 生成 cmd/deploy-helper/imports_gen.go。
deployimports:
	$(GO) run scripts/deployimports/main.go

# 生成 plugins.json（manifest + gen）。聚合文件由 generated 目标维护。
# 改动会被链接进产物的代码后运行本目标，并把变更一起提交；CI 会校验是否遗漏。
# gen 为历史名称，与 manifest 等价。
manifest gen: generated
	$(GO) run scripts/pluginctl/main.go manifest
	$(GO) run scripts/pluginctl/main.go gen

# 生成 lego v5 的包装包、分组与补充元数据。
# v5 生成器读取 catalog 元数据，因此显式依赖 catalog。
gen-v5: catalog
	$(GO) run scripts/gen_dns_v5_plugins/main.go

# 构建单个合并包：make plugin KIND=dns ID=all-lego-v5 GOOS=linux GOARCH=amd64
# DNS 发布的 ID 为 all-lego-v5；部署和通知的 ID 为 all。
ID ?= all-lego-v5
plugin:
	$(GO) run scripts/pluginctl/main.go build -kind "$(KIND)" -id "$(ID)" -goos "$(GOOS)" -goarch "$(GOARCH)" -out dist

# 从 dist/*.meta.json 生成索引（生成时会自动执行 JSON Schema 校验）。
BUCKET ?=
PREV ?=
VALID_FOR ?= 720h
index:
	$(GO) run scripts/pluginctl/main.go index -assets dist -base-url "$(BASE_URL)" $(if $(BUCKET),-bucket "$(BUCKET)") $(if $(PREV),-prev "$(PREV)") -valid-for "$(VALID_FOR)" -out index.json

validate-index:
	$(GO) run scripts/pluginctl/main.go validate -in index.json

# 索引签名请使用独立 signer，避免插件 init() 在签名进程中执行。
# 例：make sign-index KEY=/secure/key.b64
sign-index:
	$(GO) run ./cmd/signer sign -key "$(KEY)" -in index.json -out index.json.sig
	$(GO) run ./cmd/signer sign -envelope -key "$(KEY)" -in index.json -out index.json.sig.json

# 例：make verify-index PUB=t7Q4...
verify-index:
	$(GO) run ./cmd/signer verify -pub "$(PUB)" -in index.json -sig index.json.sig
	$(GO) run ./cmd/signer verify -pub "$(PUB)" -in index.json -sig index.json.sig.json

test:
	$(GO) test ./...

fmt:
	gofmt -w .

vet:
	$(GO) vet ./...

check:
	test -z "$$(gofmt -l .)" || (echo "gofmt required" >&2; exit 1)
	$(GO) vet ./...
	$(GO) test ./...

clean:
	rm -rf $(BIN) dist out index.json index.json.sig index.json.sig.json
