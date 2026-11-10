.PHONY: help clean build all all-dev engine engine-dev win-agent dc-exec dcexec-dev desktop-installer inject-installer fmt-check \
	proto test test-api test-e2e vet tools lint vuln print-version print-pkg-version package package-dev package-from-binary run sync-assets

# Application names
ENGINE_APP_NAME=dc-engine
EXEC_BIN_APP_NAME=dc-exec

# --- 版本信息（统一计算，供 -ldflags 注入与脚本查询） ---
# 版本号统一为纯净 BASE_VERSION（semver 风格，如 0.5.0）；
# git 追溯信息不拼进版本串，由 COMMIT（短 hash）/DATE（构建日期）单独注入
BASE_VERSION := 0.2.5
VERSION      ?= $(BASE_VERSION)

# 智能拆解：支持 VERSION=0.2.5 或 VERSION=0.2.5-1 形式
# APP_VERSION：纯净软件业务版本（注入 Go 二进制 dc-engine --version）
APP_VERSION  := $(firstword $(subst -, ,$(VERSION)))
# PKG_RELEASE：包发布/修订迭代号（注入 RPM/DEB 的 Release 字段，默认 1）
PKG_RELEASE  ?= $(if $(findstring -,$(VERSION)),$(patsubst $(APP_VERSION)-%,%,$(VERSION)),1)

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
export PKG_USER ?= dce
export PKG_GROUP ?= dce

# --- -ldflags ---
# 公共版本元信息（DATE/COMMIT，所有目标共享）
META_LD_FLAGS = -X 'design-cloud-engine/internal/platform/config.DATE=$(DATE)' -X 'design-cloud-engine/internal/platform/config.COMMIT=$(or $(GIT_COMMIT),unknown)'
# 版本注入片段（不含 -ldflags 前缀）：所有目标统一注入纯净 APP_VERSION
VERSION_LD_FLAGS = -X 'design-cloud-engine/internal/platform/config.VERSION=$(APP_VERSION)' $(META_LD_FLAGS)
# 组装 -ldflags（-s -w 剥离符号；dc-exec 追加 main.Version 供协议校验）
LD_FLAGS = -ldflags "-s -w $(VERSION_LD_FLAGS)"
EXEC_BIN_LD_FLAGS = -ldflags "-s -w $(VERSION_LD_FLAGS) -X 'main.Version=$(APP_VERSION)'"
INSTALLER_LD_FLAGS = -ldflags "-s -w -X 'main.version=$(APP_VERSION)'"

# 证书注入默认参数（支持命令行覆盖：make inject-installer SERVER_URL=https://...）
CA_CERT ?= conf/certs/portal/portal.ca.crt
SERVER_URL ?= https://127.0.0.1:8443

# --- 帮助 ---
help:
	@echo "╔══════════════════════════════════════════╗"
	@echo "║   Design Cloud Engine - 构建命令        ║"
	@echo "╚══════════════════════════════════════════╝"
	@echo ""
	@echo "📦 编译任务:"
	@echo "  make all            全量编译正式版并打包（clean → proto → engine + dcexec → package）"
	@echo "  make all-dev        全量编译开发版（clean → proto → engine-dev + dcexec-dev）"
	@echo ""
	@echo "  make engine         编译正式版 engine（交叉编译）"
	@echo "  make dcexec       编译正式版 dc-exec（交叉编译）"
	@echo "  make engine-dev     编译开发版 engine（保留调试符号，支持 ENGINE_* 覆盖）"
	@echo "  make dcexec-dev   编译开发版 dcexec（本地编译，tags=dev）"
	@echo "  make win-agent      编译正式版 Windows Agent（amd64，输出 build/$(ENGINE_APP_NAME).exe）"
	@echo "  make desktop-installer 编译 Windows 桌面端轻量启动安装器"
	@echo "  make inject-installer 注入 CA 证书与服务端地址，生成单文件就绪安装包"
	@echo ""
	@echo "📦 打包部署:"
	@echo "  make package        正式包：engine+dc-exec 锁步 → design-cloud-engine（版本=$(APP_VERSION)-$(PKG_RELEASE)）"
	@echo "  make package-dev    开发快照包：同上，RPM/DEB 版本带 git 短 hash"
	@echo ""
	@echo "🧪 测试与门禁:"
	@echo "  make test           Go 单元测试"
	@echo "  make test-api       Portal HTTP 集成测试（test/portal/run.sh --skip-deploy）"
	@echo "  make test-e2e       本地节点 / dc-exec 集成测试（test/node/run_all.sh）"
	@echo "  make lint           全量静态门禁（gofmt + go vet + staticcheck + govulncheck）"
	@echo ""
	@echo "🛠  实用工具:"
	@echo "  make run            本地运行 engine-dev（-config config.dev.toml）"
	@echo "  make proto          编译 Protobuf（internal/contract/proto/cluster.proto）"
	@echo "  make clean          清理 build/ 产物"
	@echo ""
	@echo "⚙️  环境变量:"
	@echo "  GOOS / GOARCH      交叉编译目标（默认 linux/amd64；打 arm64 包: GOARCH=arm64）"
	@echo "  VERSION            正式版本号 X.Y.Z 或 X.Y.Z-R（注入二进制 + package）"
	@echo "  PKG_RELEASE        包修订/发布号（默认 1，如 1 / 2 / 1.el7）"
	@echo "  PKG_TYPE           包类型: rpm / deb / all（默认 rpm）"
	@echo "  PKG_USER           运行用户（默认 dce；写入 unit/tmpfiles/%pre）"
	@echo "  PKG_GROUP          运行组（默认 dce）"

# --- 组合任务 ---
all: clean proto engine dc-exec package

all-dev: clean proto engine-dev dcexec-dev

# --- 核心编译任务 ---
# 统一编译输出目录（.PHONY 保证每次先建目录，幂等）
build:
	@mkdir -p build

engine: sync-assets | build
	go build -trimpath $(LD_FLAGS) -o build/$(ENGINE_APP_NAME) ./cmd/engine

win-agent: sync-assets | build
	GOOS=windows GOARCH=amd64 go build -trimpath $(LD_FLAGS) -o build/$(ENGINE_APP_NAME).exe ./cmd/engine

desktop-installer: | build
	GOOS=windows GOARCH=amd64 go build -trimpath $(INSTALLER_LD_FLAGS) -o build/dc-desktop-installer.exe ./cmd/dc-desktop-installer

inject-installer: desktop-installer engine
	@if [ ! -f "$(CA_CERT)" ]; then \
		echo "❌ CA 证书未找到: $(CA_CERT)，请指定 CA_CERT=... 或先生成证书"; exit 1; \
	fi
	./build/$(ENGINE_APP_NAME) cert inject-installer \
		--ca "$(CA_CERT)" \
		--server-url "$(SERVER_URL)" \
		--input build/dc-desktop-installer.exe \
		--output build/DesignCloudDesktop-Installer.exe
	@echo "✅ 已生成就绪安装器: build/DesignCloudDesktop-Installer.exe (Server: $(SERVER_URL))"

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

dc-exec: | build
	go build -trimpath $(EXEC_BIN_LD_FLAGS) -o build/$(EXEC_BIN_APP_NAME) ./cmd/exec

#   EXEC_BIN_OUT        输出路径（脚本传 $LOCAL_EXEC_BIN）
EXEC_BIN_OUT       ?= build/$(EXEC_BIN_APP_NAME)-dev

dcexec-dev: | build
	go build -trimpath -tags=dev $(EXEC_BIN_LD_FLAGS) -o $(EXEC_BIN_OUT) ./cmd/exec

# 本地开发运行（先编译 engine-dev 再执行，复用增量缓存）
run: engine-dev
	./$(ENGINE_OUT) -config config.dev.toml

# 输出当前计算出的版本号（供脚本查询，如 .vscode/deploy-dcexec.sh）
print-version:
	@echo $(APP_VERSION)

# 输出当前安装包完整版本号（含 Release，如 0.2.5-1）
print-pkg-version:
	@echo $(APP_VERSION)-$(PKG_RELEASE)

# --- 打包任务 ---
# package / package-dev：对外入口；开发变体统一用 -dev 后缀（与 engine-dev / dcexec-dev 一致）
# package-from-binary：内部入口，对已有二进制打 nfpm 包
PKG_ENGINE   ?= build/$(ENGINE_APP_NAME)
PKG_EXEC_BIN ?= build/$(EXEC_BIN_APP_NAME)
PKG_VERSION  ?= $(APP_VERSION)
DEV_PKG_RELEASE = $(PKG_RELEASE).git.$(or $(GIT_COMMIT),unknown)$(if $(GIT_DIRTY),.dirty,)

package-from-binary:
	@bash deployment/packaging/build-packages.sh $(PKG_ENGINE) $(PKG_EXEC_BIN) $(PKG_VERSION) $(PKG_RELEASE) $(PKG_TYPE) $(PKG_USER) $(PKG_GROUP) $(GOARCH)

package: engine dc-exec
	@$(MAKE) --no-print-directory package-from-binary PKG_VERSION=$(APP_VERSION) PKG_RELEASE=$(PKG_RELEASE)

package-dev: engine dc-exec
	@$(MAKE) --no-print-directory package-from-binary PKG_VERSION=$(APP_VERSION) PKG_RELEASE='$(DEV_PKG_RELEASE)'

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
	rm -f build/$(ENGINE_APP_NAME) build/$(EXEC_BIN_APP_NAME) \
		build/$(ENGINE_APP_NAME)-dev build/$(EXEC_BIN_APP_NAME)-dev \
		build/engine-debug build/$(ENGINE_APP_NAME).exe \
		build/design-cloud-engine* \
		build/dc-desktop-installer.exe build/DesignCloudDesktop-*.exe \
		build/*.rpm build/*.deb
	rm -rf build/packages

test:
	go test ./... -count=1 -timeout 60s

test-api:
	./test/portal/run.sh --skip-deploy --env dev-ldap-hpcadmin

# 节点 / dc-exec 手工集成测试（需 build/ 产物；部分用例需 root 与 setcap）
test-e2e:
	./test/node/run_all.sh

# --- 静态分析工具：必须用与 go.mod 一致的工具链构建，否则无法加载 go 1.27 的包 ---
GO_TOOLCHAIN ?= go1.27.2
TOOLS_BIN    ?= $(HOME)/go/bin

tools:
	@GOOS= GOARCH= GOTOOLCHAIN=$(GO_TOOLCHAIN) GOBIN=$(TOOLS_BIN) go install golang.org/x/vuln/cmd/govulncheck@latest
	@GOOS= GOARCH= GOTOOLCHAIN=$(GO_TOOLCHAIN) GOBIN=$(TOOLS_BIN) go install honnef.co/go/tools/cmd/staticcheck@latest

# --- 代码质量门禁（格式 + vet + 静态分析 + 漏洞扫描） ---
# make lint 一键执行全量静态门禁；任一失败即返回非 0
lint: fmt-check vet tools
	@echo "🔍 [1/2] 运行 staticcheck 静态分析..."
	@$(TOOLS_BIN)/staticcheck ./...
	@echo "🛡️  [2/2] 运行 govulncheck 漏洞扫描..."
	@$(TOOLS_BIN)/govulncheck ./...

fmt-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: 以下文件未格式化（运行 gofmt -w 修复）:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet:
	go vet ./...

vuln: tools
	$(TOOLS_BIN)/govulncheck ./...
