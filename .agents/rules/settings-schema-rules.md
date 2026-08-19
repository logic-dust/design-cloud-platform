# Settings Schema 字段约束纪律

## 核心原则：每个字段都必须有边界

settings 是运行时配置，经 API 写入后会持久化到 DB 并下发/读取。
**字段无约束 = 任意值进入系统**。新增/修改 settings schema 字段时，
必须为每个字段显式声明约束，禁止裸字段。

## 字段类型 → 必须的约束

| 字段类型 | 必须的 validate 标签 | 示例 |
|:--|:--|:--|
| 数字（间隔/配额/TTL） | `min=…,max=…`（+ 需要时 `allowzero`） | `validate:"min=30,max=3600,allowzero"` |
| 字符串枚举 | `enum=值1\|值2`（支持字符串与数字，`|` 分隔） | `validate:"enum=turbovnc\|tigervnc"` |
| 布尔开关 | 无需 validate（真值域本身受限） | — |
| 路径字符串 | 走业务校验（validate.go 内 `filepath.IsAbs` 等） | `launcherPath` 必须绝对路径 |

## 规则

1. **数字字段必须给范围**：`min`/`max` 是硬性要求。即使允许 0（未配置/关闭），
   也要显式写 `allowzero`——**不允许"无标签靠代码兜底"**，标签是唯一事实源。
2. **枚举字符串必须给 enum**：语义上是固定集合的字符串（flavor、mode、type 等）
   必须 `enum=…`，禁止裸 string。校验器对字符串/数字均支持 `enum`。
3. **`allowzero` 语义**：标签只表示「0 能通过校验」；**0 的业务含义由字段自己决定**，消费端必须按该字段约定处理，不能一律当成「关掉」或「用默认」：
   - **回退默认**（常见于带 `omitempty` 的 TTL/间隔）：0 / 缺省 → 用 `defaults.go` 或代码常量（如 `connectionListCacheTTL`、`jobCacheTTL`）；
   - **关闭功能**（常见于无 `omitempty` 的间隔）：0 持久化且表示不跑（如 `userSyncInterval`、`scanInterval`、`maxLoginSessionsPerUser`）；
   - **合法零值**（配额/阈值）：0 就是业务取值（如 `maxConnectionsPerUser`、告警 `usedPercent`），不得偷偷换成默认正数。
   **与 `omitempty` 叠加时（持久化轴，与上一含义正交）**：写库 `json.Marshal` 会省略该键，端到端「显式 0 ≡ 未携带」。因此「回退默认」类字段可以（也通常应当）带 `omitempty`；「关闭功能 / 合法零值」类字段**禁止**加 `omitempty`，否则 0 存不住。
4. **新增字段三件套**（缺一不可）：
   - `types.go`：字段 + `validate` 标签（含 `json` tag）
   - `defaults.go`：`DefaultSettings(domain)` 补默认值（行不存在时落库）
   - `validate.go`：如标签不足（路径、交叉约束），补业务校验
5. **默认值**：`defaults.go` 仅用于新行创建和运行时默认回退。settings 是全新契约，不提供旧数据库行迁移、旧字段剥离或兼容读取。
6. **测试**：每个新字段加校验用例（合法值通过、越界值/非法枚举被拒）。

## 校验机制（了解如何被强制执行）

```
API 写 → UpdateByDomain → schema.DecodeForStore(domain, raw)
   → validateAgentSettings
       → ValidateStructByTags（反射扫 validate 标签，递归进嵌套 struct 与指针解引用）
```

- 校验是**反射驱动的**：只要字段写了 `validate` 标签，自动纳入，无需注册。
- 支持指针字段解引用：nil 指针视为 `omitempty` 跳过；非 nil 时解引用目标值执行数值范围/枚举校验，并自动递归进嵌套结构体指针。
- 字段**没有**标签 = 校验层直接跳过 = 任意值通过。这就是"裸字段"的代价。
- 枚举校验器支持：字符串、int、uint、float（`asStr` 统一转换后比对）。

## 已知补漏（示例）

- `Flavor`（turbovnc/tigervnc）曾为裸 string，应按 `enum=turbovnc\|tigervnc` 补齐。
- 扫描漏网字段：`grep -n 'json:"' types.go | grep -v 'validate:'`。

## 新增 Domain 的检查清单

新增设置域时，以 `internal/server/service/settings/schema/` 中的 schema 实现为事实源，沿现有 domain 实现逐项追踪：domain 注册与解析、字段类型和约束、默认值、业务校验、严格解码、service getter/按域分发、测试与需要的 API 暴露。不要按旧模板预设固定文件数或固定接线位置；确认每个新增行为是否被调用并由测试覆盖。

`DecodeForStore` 必须拒绝未知字段；业务校验错误使用 422，语法/绑定错误使用 400。新字段应覆盖合法值、越界/非法值及默认值序列化行为。

## 节点覆盖语义

节点覆盖是稀疏补丁：可覆盖域与字段以当前 schema 白名单为准。PUT 中缺失字段保留已有值，显式 `null` 删除覆盖并恢复继承，数组整体替换，空数组表示覆盖为空；未知字段拒绝。改动时核对 merge/validation/cache invalidation 实现及测试，不在本规则复制易变白名单或 TTL。

## 兼容与验证

Settings schema 是新契约；调整字段时按全仓引用一次性更新，不添加旧字段兼容读取或别名。对域迁移核对持久化、默认值、验证、API 与测试链路。运行相关 settings 测试及项目增量门禁 `./.agents/verifiers/verify-changed.sh`。
