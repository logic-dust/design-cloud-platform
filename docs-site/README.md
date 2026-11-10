# 文档站

本目录是 `Hugo + Hextra` 站点工程，业务文档位于 `docs-site/docs/`（站内挂载为 `content/docs`）。

## 设计原则

- `docs-site/docs/` 作为文档事实源
- 文档站配置与业务代码分离
- 主题按 Hextra 官方推荐的 Hugo module 方式接入
- 站点负责导航、搜索与发布承载

## 内容来源

站点通过 Hugo module mounts 将：

- `docs/`（本目录下的 `docs/` 子目录）

挂载为：

- `content/docs`

因此站点内容直接来自 `docs-site/docs/**/*.md`。

## 预期工具链

- Hugo Extended
- Go

## 本地运行

在本目录执行：

```bash
hugo mod tidy
hugo server #--buildDrafts --disableFastRender
```

如果要构建静态站点：

```bash
hugo
```

## 站点内容

站点包含：

- Hextra 主题配置
- 首页跳转
- `docs/` 内容挂载

站点暂未包含：

- 自定义导航树
- 完整主题样式定制
- CI 构建
- 文档版本化

## 构建状态

已确认：

- `docs/` 挂载配置语法有效
- Hextra 主题可以通过 Hugo module 加载

站点构建命令：

```bash
hugo
```
