---
title: 嵌入式前端开发规范
linkTitle: 前端规范
weight: 50
description: Portal 门户与感知数据大屏的设计语言、色彩契约与布局规范
aliases:
  - /docs/development/guide/web-design-system
  - /docs/development/web-design-system
---

> **源码目录**：`internal/server/portal/web/` (`index.html`, `overview.html`, `static/css/theme.css`, `static/js/theme.js`)

本文档定义了设计云平台（Design Cloud Platform）内置 Web 前端与 NOC 感知数据大屏的设计语言、色彩契约与前端组件化规范，供后续维护与扩展时保持风格和谐统一。

---

## 1. 核心设计原则

设计云平台内置 Web 界面面向两类典型场景：
1. **Portal 门户主页 (`index.html`)**：面向普通用户与管理员的轻量级控制面板入口；
2. **感知数据大屏 (`overview.html`)**：面向展厅大屏与 NOC 运维监控室的全屏玻璃拟态（Glassmorphism）高感知监控大屏。

### 四项底层原则：
1. **暗黑科技感 (Dark Glassmorphism)**：使用深色背景（`#0b0f19`）搭配高感半透明玻璃毛玻璃卡片（`backdrop-filter: blur(12px)`）。
2. **严密色彩契约 (Color Hierarchy)**：硬件子域、品牌色、物理安全三态色界限分明，绝不出错配。
3. **零依赖原生标准 (Zero-Framework Standard)**：坚决不引入 Tailwind、Vue、React 等重型依赖，使用 Vanilla HTML5/CSS3/JS，编译产物极小且秒开。
4. **零硬编码像素弹性布局 (Fluid Sizing)**：采用基于 CSS Grid (`max-content 1fr max-content`) 与 `ch` 字符宽度的自适应网格，拒绝脆弱的硬编码 `px`。

---

## 2. 色彩体系与资源域色彩契约 (Domain Theme Matching)

所有的视觉色彩均统一收聚在 `css/theme.css` 的 CSS 自定义属性（Variables）中，**严禁在 HTML / JS 中内联 Hex 颜色**。

### 2.1 色彩 Tokens (CSS Variables)

| CSS 变量名 | 颜色 Hex / RGBA | 语义定位 | 应用场景 |
|---|---|---|---|
| `var(--bg-main)` | `#0b0f19` | 深色底色 | 全屏大屏与门户底色 |
| `var(--bg-panel)` | `rgba(15, 23, 42, 0.65)` | 玻璃卡片 | 半透明毛玻璃卡片背景 |
| `var(--primary-cyan)` | `#38bdf8` / `#0284c7` | 科技青 | 节点、CPU 核心通用计算子域 |
| `var(--accent-amber)` | `#fbbf24` / `#d97706` | 琥珀金 | GPU 利用率等异构高算子域；节点「维护」态 |
| `var(--primary-violet)` | `#a855f7` / `#7c3aed` | 高频紫 | 内存/显存吞吐容量子域 |
| `var(--primary-blue)` | `#6366f1` / `#4f46e5` | 品牌蓝 | 主品牌色、分母 $/Y$ 统一降级色 |
| `var(--accent-emerald)`| `#34d399` / `#10b981` | 翡翠绿 | 物理正常状态（$\le 70\%$ 负载/存储） |
| `var(--accent-rose)` | `#f43f5e` / `#e11d48` | 玫瑰红 | 物理告警状态（$> 100\%$ 负载/ $> 90\%$ 存储） |

---

### 2.2 资源子域色彩契约 (Domain Theme Color System)

上排节点区内容卡片与 Desktop 集群概览对齐（不含作业/存储）：**指标名作标题，统计口径写在卡片底部 `* …` 脚注**；派生百分比一位小数。

```
[集群节点卡]     ─── 状态色（在线绿 / 维护琥珀 / 离线玫红）+ 在线占比环（翡翠绿）
[CPU 利用率]     ─── 科技青 (Cyan) 大百分比 + 指标环
[内存 / 显存]    ─── 高频紫 (Violet)：总计 + 使用两行，环心为在线容量占比
[GPU 利用率]     ─── 琥珀金 (Amber) 大百分比 + 指标环（有 GPU 时；不单独放 GPU 节点卡）
[集群总负载]     ─── 仅展示原始负载；进度条按「负载 ÷ 在线核数」归一化并按告警阈值着色
```

#### 四条色彩纪律：
1. **指标环与主数字同色**：CPU/GPU 利用率与内存/显存使用量各自绑定子域色；节点在线占比环使用翡翠绿（对齐 Desktop `App.Status.Success`）。
2. **分母 $/Y$ 统一降级**：节点卡「配置资源」的 `在线/配置` 分母使用主品牌蓝 (`var(--primary-blue)`)，避免抢夺分子第一视觉焦点。
3. **状态色边界**：绿/黄/红用于节点运行态（在线/维护/离线）、负载条与存储占用率；常规 CPU/GPU 利用率卡不用红黄绿作主色。
4. **字体与排版**：所有数值与时间统一使用 `JetBrains Mono` / `Roboto Mono` 等宽字体，提升科技感与对齐稳定性。

---

## 3. 布局与 CSS Grid 网格规范 (Layout Standards)

### 3.1 3 大列响应式网格 (3-Column Layout Standards)
- **三大列对齐架构**（对齐 Desktop `*,2.1*,Auto`）：`grid-template-columns: 1fr 2.1fr minmax(11rem, 0.85fr)`；无 GPU 时隐藏右列并收成两列。
  - **左列 (集群节点)**：单张贯通大卡（`.node-overview-card`）。上半为总计 / 在线 / 维护 / 离线 + 在线占比环；下半「配置资源」为处理器、内存（GB）的 `在线/配置`，脚注 `* 在线/配置`。
  - **中列 (CPU · 内存 · 负载)**：Row 1 为 `CPU 平均利用率` + `内存使用量`（总计/使用）两卡；Row 2 为 `集群总负载`（1m/5m/15m 三列，只显示原始负载与归一化条，悬停给出「负载 / N 核 · 百分比」），脚注 `* 满负载为 N 核（当前在线核数）`。
  - **右列 (GPU)**：仅 `GPU 平均利用率` + `显存使用量` 两张竖排卡（有 `gpu.nodes.configured > 0` 时显示）；**不**单独放 GPU 节点卡。
- **全列高度平齐**：三大列借助 CSS Grid `align-items: stretch` 实现垂直方向高度 100% 自动平齐。

### 3.2 负载三列网格
`集群总负载` 采用三列等分（`.load-cols`），每列标签+数值在上、进度条在下；条长按「负载 ÷ 在线核数」封顶 100%，与 engine `alert.loadRatio` 口径一致。

---

## 4. 模块化与脚本分层

1. **`js/theme.js`**：独立防 FOUC（闪烁）主题脚本，放在 `<head>` 中优先执行，支持深/浅色模式无缝切换与 `localStorage` 记忆。
2. **`internal/server/portal/web.go`**：通过 Go `//go:embed web` 实现单二进制文件内置挂载，受 `PlatformSettings.Website` 系统设置精确动态调控。
