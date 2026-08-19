.PHONY: help clean build all all-dev engine engine-dev win-agent launcher launcher-dev \
	proto test vet print-version package package-dev package-from-binary run sync-assets

# Application names
ENGINE_APP_NAME=design-cloud-engine
LAUNCHER_APP_NAME=dc-launcher

# --- 版本信息（统一计算，供 -ldflags 注入与脚本查询） ---
# 版本号统一为纯净 BASE_VERSION（semver 风格，如 0.5.0）；
# git 追溯信息不拼进版本串，由 COMMIT（短 hash）/DATE（构建日期）单独注入
BASE_VERSION := 0.1.21
VERSION      ?= $(BASE_VERSION)
# 用 ?= 允许外部覆盖：远程构建主机可能未安装 git（rsync 也不同步 .git 元数据），
# 由调用方（如 .vscode/remote-build-debug.sh）在本地计算后传入，否则 COMMIT 注入 unknown
GIT_COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null)
GIT_DIRTY    ?= $(shell git status --porcelain 2>/dev/null)
DATE         := $(shell date -u +"%Y.%m.%d")

# --- 编译环境（export 后自动注入所有编译命令） ---
export CGO_ENABLED=0
export GOOS ?= linux
# 目标包/二进制架构；交叉编译时 gen-config 仍用本机 go build（见 build-packages.sh）
export GOARCH ?= amd64
export PKG_TYPE ?= rpm
export PKG_USER ?= design-cloud
export PKG_GROUP ?= design-cloud

# --- -ldflags ---
# 公共版本元信息（DATE/COMMIT，所有目标共享）
META_LD_FLAGS = -X 'design-cloud-engine/internal/config.DATE=$(DATE)' -X 'design-cloud-engine/internal/config.COMMIT=$(or $(GIT_COMMIT),unknown)'
# 版本注入片段（不含 -ldflags 前缀）：所有目标统一注入纯净 VERSION
VERSION_LD_FLAGS = -X 'design-cloud-engine/internal/config.VERSION=$(VERSION)' $(META_LD_FLAGS)
# 组装 -ldflags（-s -w 剥离符号；launcher 追加 main.Version 供协议校验）
LD_FLAGS = -ldflags "-s -w $(VERSION_LD_FLAGS)"
LAUNCHER_LD_FLAGS = -ldflags "-s -w $(VERSION_LD_FLAGS) -X 'main.Version=$(VERSION)'"

# --- 帮助 ---
help:
	@echo "╔══════════════════════════════════════════╗"
	@echo "║   Design Cloud Engine - 构建命令        ║"
	@echo "╚══════════════════════════════════════════╝"
	@echo ""
	@echo "📦 编译任务:"
	@echo "  make all            全量编译正式版并打包（clean → proto → engine + launcher → package）"
	@echo "  make all-dev        全量编译开发版（clean → proto → engine-dev + launcher-dev）"
	@echo ""
	@echo "  make engine         编译正式版 engine（交叉编译）"
	@echo "  make win-agent      编译正式版 Windows Agent（amd64，输出 build/$(ENGINE_APP_NAME).exe）"
	@echo "  make launcher       编译正式版 launcher（交叉编译）"
	@echo "  make engine-dev     编译开发版 engine（保留调试符号，支持 ENGINE_* 覆盖）"
	@echo "  make launcher-dev   编译开发版 launcher（本地编译，tags=dev）"
	@echo ""
	@echo "📦 打包部署:"
	@echo "  make package        正式包：engine+launcher 锁步 → design-cloud-engine（版本=$(VERSION)）"
	@echo "  make package-dev    开发快照包：同上，RPM/DEB 版本带 git 短 hash"
	@echo ""
	@echo "🛠  工具:"
	@echo "  make proto         编译 Protobuf（internal/contract/proto/cluster.proto）"
	@echo "  make clean         清理 build/ 二进制与 build/packages/"
	@echo "  make test          运行全部测试（-count=1 -timeout=60s）"
	@echo "  make vet           执行 go vet"
	@echo "  make run           本地运行 engine-dev（编译走 make，-config config.dev.toml）"
	@echo "  make sync-assets   品牌 logo 权威源 → web/assets 与 docs-site/static/images"
	@echo ""
	@echo "⚙️  环境变量:"
	@echo "  GOOS / GOARCH      交叉编译目标（默认 linux/amd64；打 arm64 包: GOARCH=arm64）"
	@echo "  VERSION            正式版本号 X.Y.Z（注入二进制 + package）"
	@echo "  PKG_TYPE           包类型: rpm / deb / all（默认 rpm）"
	@echo "  PKG_USER           运行用户（默认 design-cloud；写入 unit/tmpfiles/%pre）"
	@echo "  PKG_GROUP          运行组（默认 design-cloud）"

# --- 组合任务 ---
all: clean proto engine launcher package

all-dev: clean proto engine-dev launcher-dev

# --- 核心编译任务 ---
# 统一编译输出目录（.PHONY 保证每次先建目录，幂等）
build:
	@mkdir -p build

engine: sync-assets | build
	go build -trimpath $(LD_FLAGS) -o build/$(ENGINE_APP_NAME) ./cmd/engine

win-agent: sync-assets | build
	GOOS=windows GOARCH=amd64 go build -trimpath $(LD_FLAGS) -o build/$(ENGINE_APP_NAME).exe ./cmd/engine

# --- 开发版编译（保留调试符号，无 -s -w；也可供 .vscode 远程脚本复用） ---
# 可覆盖变量：
#   ENGINE_OUT          输出路径（脚本传 $LOCAL_BINARY）
#   ENGINE_GC_FLAGS     -gcflags，传 all=-N -l 禁优化（远程 dlv 调试精准）
#   ENGINE_EXTRA_FLAGS  追加任意 go build 标志
ENGINE_OUT         ?= build/$(ENGINE_APP_NAME)-dev
ENGINE_GC_FLAGS    ?=
ENGINE_EXTRA_FLAGS ?=

engine-dev: sync-assets | build
	go build -trimpath -tags=dev $(if $(ENGINE_GC_FLAGS),-gcflags="$(ENGINE_GC_FLAGS)",) $(ENGINE_EXTRA_FLAGS) -ldflags "$(VERSION_LD_FLAGS)" -o $(ENGINE_OUT) ./cmd/engine

launcher: | build
	go build -trimpath $(LAUNCHER_LD_FLAGS) -o build/$(LAUNCHER_APP_NAME) ./cmd/launcher

launcher-dev: | build
	go build -trimpath -tags=dev $(LAUNCHER_LD_FLAGS) -o build/$(LAUNCHER_APP_NAME)-dev ./cmd/launcher

# 本地开发运行（先编译 engine-dev 再执行，复用增量缓存）
run: engine-dev
	./$(ENGINE_OUT) -config config.dev.toml

# 输出当前计算出的版本号（供脚本查询，如 .vscode/deploy-launcher.sh）
print-version:
	@echo $(VERSION)

# --- 打包任务 ---
# package / package-dev：对外入口；开发变体统一用 -dev 后缀（与 engine-dev / launcher-dev 一致）
# package-from-binary：内部入口，对已有二进制打 nfpm 包
PKG_ENGINE   ?= build/$(ENGINE_APP_NAME)
PKG_LAUNCHER ?= build/$(LAUNCHER_APP_NAME)
PKG_VERSION  ?= $(VERSION)
DEV_PKG_VERSION = $(VERSION)+git.$(or $(GIT_COMMIT),unknown)$(if $(GIT_DIRTY),.dirty,)

package-from-binary:
	@bash deployment/packaging/build-packages.sh $(PKG_ENGINE) $(PKG_LAUNCHER) $(PKG_VERSION) $(PKG_TYPE) $(PKG_USER) $(PKG_GROUP) $(GOARCH)

package: engine launcher
	@$(MAKE) --no-print-directory package-from-binary PKG_VERSION=$(VERSION)

package-dev: engine launcher
	@$(MAKE) --no-print-directory package-from-binary PKG_VERSION='$(DEV_PKG_VERSION)'

# --- 工具任务 ---
PROTOC_GEN_GO     := $(shell command -v protoc-gen-go 2>/dev/null)
PROTOC_GEN_GO_GRPC := $(shell command -v protoc-gen-go-grpc 2>/dev/null)

proto:
	@if [ -z "$(PROTOC_GEN_GO)" ]; then echo -e >&2 "protoc-gen-go is required but it's not installed. Please install it with\n\n  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest"; exit 1; fi
	@if [ -z "$(PROTOC_GEN_GO_GRPC)" ]; then echo -e >&2 "protoc-gen-go-grpc is required but it's not installed. Please install it with\n\n  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest"; exit 1; fi
	@protoc --go_out=. --go-grpc_out=. --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative internal/contract/proto/cluster.proto

WEB_SRC_DIR      := internal/server/portal/web

# ---------- 品牌资产同步 ----------
# docs-site/static/ 为事实源（docs 品牌门面，改动最自然）：
#   - images/logo.svg → web/static/assets/design-cloud-logo.svg（logo）
#   - favicon.ico     → web/static/assets/design-cloud.ico（favicon）
# web（embed 编译期）为消费方，由 sync-assets 单向同步。
# 需要品牌资产的配方（engine/engine-dev）必须声明依赖本目标。
ASSETS_DOCS_SRC     := docs-site/static/images/logo.svg
ASSETS_WEB_DST      := $(WEB_SRC_DIR)/static/assets/design-cloud-logo.svg
ASSETS_DOCS_FAVICON := docs-site/static/favicon.ico
ASSETS_WEB_FAVICON  := $(WEB_SRC_DIR)/static/assets/design-cloud.ico

sync-assets: $(ASSETS_DOCS_SRC) $(ASSETS_DOCS_FAVICON)
	@mkdir -p $(dir $(ASSETS_WEB_DST))
	@cp $(ASSETS_DOCS_SRC) $(ASSETS_WEB_DST)
	@cp $(ASSETS_DOCS_FAVICON) $(ASSETS_WEB_FAVICON)
	@echo "✅ 品牌资产已同步 → logo: $(ASSETS_WEB_DST) / favicon: $(ASSETS_WEB_FAVICON)（源：docs-site/static/）"

clean:
	rm -f build/$(ENGINE_APP_NAME) build/$(LAUNCHER_APP_NAME) \
		build/$(ENGINE_APP_NAME)-dev build/$(LAUNCHER_APP_NAME)-dev \
		build/engine-debug build/$(ENGINE_APP_NAME).exe
	rm -rf build/packages

test:
	go test ./... -count=1 -timeout 60s

vet:
	go vet ./...
