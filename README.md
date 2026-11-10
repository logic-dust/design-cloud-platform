# Design Cloud Engine

[![Go Version](https://img.shields.io/badge/Go-1.27.2%2B-blue.svg)](https://golang.org)

Design Cloud Platform 核心引擎，基于 Server/Agent 架构，双组件安全隔离（Engine + DcExec），支持分布式部署。

## 🏗️ 编译

需要 **Go 1.27.2+**。`go.mod` 的 `go` 指令声明语言版本要求；未配置 `toolchain` 指令时，需自行安装满足最低版本要求的 Go。

```bash
make all
```

静态检查与漏洞扫描（`lint` 先执行 `gofmt -l` 格式门禁再跑 staticcheck；工具以 go1.27.2 构建，安装到 `$HOME/go/bin`）：

```bash
make lint vuln
```

或者单独编译某个组件，参看 [Makefile](Makefile) 中的目标。

## 📦 部署与运行

详细部署说明请参考 [配置文件](docs-site/docs/deployment/config.md) 和 [安装与首次部署](docs-site/docs/deployment/install.md) 文档。

## 🔨 开发

详细开发说明请参考 [开发指南](docs-site/docs/development/_index.md) 。

使用 AI 辅助开发时，按根目录 `AGENTS.md` 和 `.agents/README.md` 的任务路由按需加载上下文。

`.agents/` 包含项目规则、专项技能及自动化验证工具；具体加载方式见 `.agents/README.md`。涉及 API、架构、配置或安全边界的改动，按需核对并更新 `docs-site/docs/` 中相关文档。

## 📚 文档

查看 `docs-site/docs/` 目录包含所有设计文档、接口文档和操作指南等。

也可以在`docs-site/`目录使用`hugo`构建静态文档站点，提供更友好的浏览体验：

```bash
cd docs-site
hugo server #-D
```

访问 `http://localhost:1313` 即可浏览文档站点。
