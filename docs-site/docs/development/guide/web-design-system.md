---
title: Web 界面与数据大屏设计系统指南
linkTitle: Web 设计系统指南
weight: 60
description: Portal 门户与感知数据大屏的设计语言、色彩契约与布局规范
---

> **源码目录**：`internal/server/portal/web/` (`index.html`, `dashboard.html`, `css/theme.css`, `js/theme.js`)

本文档定义了设计云平台（Design Cloud Platform）内置 Web 前端与 NOC 感知数据大屏的设计语言、色彩契约与前端组件化规范，供后续维护与扩展时保持风格和谐统一。

---

## 1. 核心设计原则

设计云平台内置 Web 界面面向两类典型场景：
1. **Portal 门户主页 (`index.html`)**：面向普通用户与管理员的轻量级控制面板入口；
2. **感知数据大屏 (`dashboard.html`)**：面向展厅大屏与 NOC 运维监控室的全屏玻璃拟态（Glassmorphism）高感知监控大屏。

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
| `var(--accent-amber)` | `#fbbf24` / `#d97706` | 琥珀金 | GPU 节点/GPU 卡异构高算子域 |
| `var(--primary-violet)` | `#a855f7` / `#7c3aed` | 高频紫 | 内存/显存吞吐容量子域 |
| `var(--primary-blue)` | `#6366f1` / `#4f46e5` | 品牌蓝 | 主品牌色、分母 $/Y$ 统一降级色 |
| `var(--accent-emerald)`| `#34d399` / `#10b981` | 翡翠绿 | 物理正常状态（$\le 70\%$ 负载/存储） |
| `var(--accent-rose)` | `#f43f5e` / `#e11d48` | 玫瑰红 | 物理告警状态（$> 100\%$ 负载/ $> 90\%$ 存储） |

---

### 2.2 资源子域色彩契约 (Domain Theme Color System)

在数据大屏的 9 大基础指标卡片中，**分子数字 $X$（在线/可用数）** 与 **SVG 圆环 Donut 描边 (`stroke`)** 严格保持 **1:1 绝对同色绑定**：

```
[通用 CPU 算力域] ─── 科技青 (Cyan #38bdf8)   ─── Card 1 (节点), Card 2 (CPU核心), Card 3 (CPU利用率)
[异构 GPU 算力域] ─── 琥珀金 (Amber #fbbf24)  ─── Card 6 (GPU节点), Card 7 (GPU卡数), Card 8 (GPU利用率)
[容量与吞吐域]    ─── 高频紫 (Violet #a855f7) ─── Card 5 (系统内存), Card 9 (GPU显存)
```

#### 四条色彩纪律：
1. **分子 $X$ 与 Donut 同色**：节点/CPU 统一使用科技青，GPU 节点/卡数统一使用琥珀金，内存/显存统一使用高频紫。
2. **分母 $/Y$ 统一降级**：所有 $X/Y$ 比例卡片中，分母 $/Y$ 统一使用主品牌蓝 (`var(--primary-blue)`)，避免抢夺分子第一视觉焦点。
3. **安全状态红黄绿隔离**：绿/黄/红严格只留给动态安全状态（总负载 1m/5m/15m 进度条、共享存储容量占用率），**绝不可用于常规硬件概览卡片**。
4. **字体与排版**：所有数值与时间统一使用 `JetBrains Mono` / `Roboto Mono` 等宽字体，提升科技感与对齐稳定性。

---

## 3. 布局与 CSS Grid 网格规范 (Layout Standards)

### 3.1 3 大列响应式网格 (3-Column Layout Standards)
- **三大列对齐架构**：`grid-template-columns: 1.1fr 1fr 1.3fr`
  - **左列 (集群配置基准)**：单张贯通上下的整体大卡片（`.node-overview-card`）。顶部展示`配置节点`与科技青 SVG Donut 圆圈（`95%`）；底部为无冗余大标题的极简规格列表（`CPU 核心数 190/200 核`；`系统内存 6,144/10,240 GB`），一行一个指标。
  - **中列 (在线运行动态)**：2 行标准卡片结构（Row 1：`CPU 平均利用率` + `内存使用量` 2 张独立小卡横向平排；Row 2：`总负载` 1m/5m/15m 3排 CSS Grid 进度条全宽卡片）。
  - **右列 (GPU 异构高算专区)**：2x2 网格小卡片（GPU节点、GPU卡数、GPU利用率、GPU显存）。
- **全列高度平齐**：三大列借助 CSS Grid `align-items: stretch` 实现垂直方向高度 100% 自动平齐。

### 3.2 零硬编码像素 CSS Grid 对齐
对于包含多排进度条与数值的卡片（如卡片 4 `总负载`），采用 **CSS Grid 4 列网格** 替换脆弱的固定 `px` 宽度：

```css
.metric-card .load-bars-wrap {
  display: grid;
  grid-template-columns: max-content max-content 1fr max-content;
  align-items: center;
  gap: 6px 10px;
}

.load-bar-row {
  display: contents; /* 将每行的子元素拉入父级 4 列网格 */
}
```

- **Col 1 (Label)**：`max-content`（自适应最长标签）
- **Col 2 (数值)**：`max-content` + `min-width: 4ch`（等宽对齐）
- **Col 3 (进度条)**：**`1fr`**（填充所有剩余空间，确保 3 排进度条轨道在像素上 100% 相同长度）
- **Col 4 (百分比)**：`max-content` + `min-width: 4ch`（末尾 N% 数字动态着色）

---

## 4. 模块化与脚本分层

1. **`js/theme.js`**：独立防 FOUC（闪烁）主题脚本，放在 `<head>` 中优先执行，支持深/浅色模式无缝切换与 `localStorage` 记忆。
2. **`internal/server/portal/web.go`**：通过 Go `//go:embed web` 实现单二进制文件内置挂载，受 `PlatformSettings.Website` 系统设置精确动态调控。
