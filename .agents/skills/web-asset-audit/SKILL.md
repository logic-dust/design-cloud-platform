---
name: web-asset-audit
description: 审查与优化 internal/server/portal/web 中的嵌入式前端资产（HTML, CSS, JS），排查冗余死样式、全局污染、内联样式残留、职责跨界与逻辑缺陷，保障纯内网离线自包含与高性能渲染。
---

# Web 静态资产与前端代码审查优化 (Web Asset Audit & Optimization)

> **定位**：本 SKILL 专门用于审查、清理与优化项目中内嵌的 Portal Web 静态资产（如 `internal/server/portal/web/` 下的 HTML、CSS、JS、SVG 等）。
> 确保前端代码零外部 CDN 依赖、零冗余死代码、CSS 架构清晰解耦、JS 逻辑现代高内聚、HTML 结构语义化，并严格与服务端的内嵌与版本化缓存机制对齐。

---

## 一、四大核心审查维度

```text
┌────────────────────────────────────────────────────────────────────────┐
│                     Web 资产审查与优化模型 (WEB-AUDIT)                 │
├──────────────────────────────────┬─────────────────────────────────────┤
│ 1. CSS 架构解耦与死样式审查      │ 2. JavaScript 逻辑精简与现代重构    │
│ • 全局 Token/Reset 与专属样式分离│ • 消除全局变量与 window 污染        │
│ • 杜绝跨文件重复定义与权重打架   │ • 消除冗余状态缓存与硬编码假数据    │
│ • 扫描并剔除未引用的死样式类     │ • DOM 操作去重与 SSE/WS 健壮重连    │
├──────────────────────────────────┼─────────────────────────────────────┤
│ 3. HTML 结构语义化与内联清理     │ 4. 离线安全与服务端内嵌生命周期     │
│ • 彻底清理与 CSS 类冲突的内联样式│ • 100% 纯内网离线自包含 (严禁外链)  │
│ • 移除多余内联脚本与侵入式属性   │ • 版本占位符 (__WEB_VERSION__) 闭环 │
│ • 语义化标签与响应式容器断点     │ • 嵌入式 Go 单测与路由开关同步      │
└──────────────────────────────────┴─────────────────────────────────────┘
```

---

## 二、详细审查准则与判定标准

### 1. CSS 架构解耦与死样式治理 (CSS Layer)

- **职责边界清晰 (Separation of Concerns)**：
  - **`theme.css`（全局共享）**：仅包含全局设计 Token（色彩、渐变、字体栈、间距、圆角、阴影）、Reset 重置、全局滚动条、毛玻璃面板 (`.glass-panel`)、面板头部 (`.panel-head`) 以及通用组件（`.brand`、`.theme-toggle-btn`、`.btn-action` 等）。
  - **页面专属 CSS（如 `dashboard.css` 或 `index.html` 内联样式）**：收归专属布局网格（如 `.screen`、`.top-cards-section`）、专属图表（`.card-ring`、`.donut-svg-wrap`）、专属表格与状态指示灯。
  - ❌ **严禁**将仅某单页面使用的表格、容量条、图表类塞入全局 `theme.css`。
- **杜绝选择器权重冲突与重复覆盖**：
  - 检查是否存在同一类名在 `theme.css` 和页面 CSS 中被重复定义且字号、间距互相覆盖（例如 `.metric-card .val-num`）。
- **死样式类 (Dead Classes) 扫描**：
  - 检查 CSS 中声明的所有 Class 是否在 HTML 模板或 JS 模板字符串中实际被使用；
  - 剔除历史遗留、改版后未消费的废弃颜色变体（如未使用的 `.val-num.green`、重复的 `.text-muted-custom`）。

### 2. JavaScript 逻辑精简与现代重构 (JS Layer)

- **避免全局变量污染**：
  - ❌ 严禁在 HTML `<head>` 中通过 `<script>window.FOO = 'bar'</script>` 向全局注入配置常量；
  - ✅ JS 文件内直接定义 `const VERSION_KEY = 'dashVersion';` 等模块私有常量。
- **消除历史冗余状态**：
  - 检查 `localStorage` 缓存读取与兜底逻辑，避免定义如 `queueSeed` 这样仅使用一次的多余顶层变量；
  - 统一采用现代 ES6+ 链式解构与空值合并：`queuesData || state.scheduler?.queues || []`。
- **杜绝硬编码假数据**：
  - 检查数值渲染与基准计算，杜绝无理由的硬编码默认值（如硬编码默认 `4` 核）；使用优雅的动态回退：`d.cpu?.online || 1`。
- **DOM 操作与事件去重**：
  - 避免在全局脚本执行时和 `DOMContentLoaded` 回调内重复查询相同 DOM 元素并设置相同属性。
- **长连接健壮性 (SSE / WebSocket)**：
  - 确保断线重连具备**指数退避与 Jitter 抖动**，避免集群波动时的大屏并发重连风暴。

### 3. HTML 结构与内联清理 (HTML Layer)

- **清理冗余内联样式 (Inline Styles)**：
  - 若元素已有 CSS Class 声明（例如 `.btn-dl` 已有 `text-decoration: none;`），必须移除 HTML/JS 模板中的内联 `style="text-decoration: none;"`；
  - 将多次重复的内联布局（如 `style="margin:0 10px"`）提取为语义化 Class（如 `.meta-sep`）或由父容器 flex gap 统一控制。
- **清理 SVG 侵入式样式**：
  - 移除 `<svg style="width:100%; height:100%;">` 等内联样式，统一由外部容器与 CSS 规则控制。

### 4. 离线安全与服务端协同 (Security & Server Parity)

- **100% 离线自包含约束 (Air-Gapped Hard Rule)**：
  - 平台部署于专网/HPC 离线集群，**严禁引入任何外部 CDN**（如 Google Fonts、cdnjs、unpkg、FontAwesome 等）；
  - 字体统一使用系统字体栈（macOS/Windows/Linux/Noto Sans CJK/WenQuanYi Micro Hei）。
- **版本化缓存控制 (Cache Invalidation)**：
  - 内嵌资源引用必须带有服务端替换的版本占位符：`src="static/js/dashboard.js?v=__WEB_VERSION__"`；
  - 大屏 HTML 服务端响应头必须保持 `Cache-Control: no-cache`，确保大屏长期驻留时通过版本轮询与 SSE 事件感知自动 reload。

---

## 三、标准审查与优化工作流

### 步骤 1：资产清单与引用扫描

```bash
# 1. 查找所有内嵌前端文件
find internal/server/portal/web -type f

# 2. 检查是否有非法外链/外部 CDN (必须输出为空)
grep -rnIE "http(s)?://" internal/server/portal/web/ | grep -v "api/" | grep -v "xmlns"
```

### 步骤 2：CSS 与 JS 依赖拓扑核查

1. 提取 `theme.css` 中的全部类选择器；
2. 交叉比对 `index.html`、`dashboard.html`、`dashboard.js`；
3. 将专属组件从 `theme.css` 迁移至对应页面 CSS；
4. 消除所有重叠类定义。

### 步骤 3：HTML 与 JS 模板字符串清理

1. 扫描内联 `style="..."` 并剔除重复声明；
2. 扫描 `window.*` 注入并收敛为 JS 局部常量；
3. 检查数值格式化与时间格式化函数边界。

### 步骤 4：Go 单元测试与门禁验证

```bash
# 1. 运行 Portal 页面与版本替换专项单测
go test -v ./internal/server/portal -run '^TestLoadVersionedHTML_(PlaceholderReplaced|CommitNotAppended)$|^TestRegisterWebRoutes_DynamicPageSwitches$'

# 2. 运行全量代码验证门禁
./.agents/verifiers/verify-changed.sh
```
