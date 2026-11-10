# Settings Schema 字段约束纪律

## 核心原则：每个字段都必须有边界

settings 是运行时配置，经 API 写入后会持久化到 DB 并下发/读取。
**字段无约束 = 任意值进入系统**。新增/修改 settings schema 字段时，
必须为每个字段显式声明约束，禁止裸字段。

## 字段类型 → 必须的约束

| 字段类型 | 必须的 validate 标签 | 示例 |
|:--|:--|:--|
| 数字（间隔/配额/TTL） | `min=…,max=…`（+ 仅当 0 是区间外哨兵时 `allowzero`） | `validate:"min=30,max=3600,allowzero"` |
| 字符串枚举 | `enum=值1\|值2`（支持字符串与数字，`|` 分隔） | `validate:"enum=lsf\|slurm\|openlava"` |
| 布尔开关 | 无需 validate（真值域本身受限） | — |
| 路径字符串 | 走业务校验（validate.go 内 `filepath.IsAbs` 等） | `execBinPath` 必须绝对路径 |
| 原始类型切片 | 字段上的 `min`/`max`/`enum` 作用在**每个元素** | `[]int` `validate:"min=1,max=168"` |

## 规则

1. **数字字段必须给范围**：`min`/`max` 是硬性要求。标签是唯一事实源；消费端不得再抄一份上下界常量。
2. **整数按整数比较**：`ValidateStructByTags` 与 `GenerateJSONSchema` 共用 `parseValidateTag`。`int`/`int64` 用 `ParseInt`，`uint` 用 `ParseUint`，浮点才用 `ParseFloat`。解析失败直接报错，禁止静默成 `0`。
3. **`allowzero` 只留给区间外的哨兵 0**（`min > 0` 且 0 仍合法）：
   - **回退默认**（常配 `json:",omitempty"`）：`jobCacheTTL`、各类刷新/探测间隔；
   - **关闭功能**（禁止 `json:",omitempty"`）：`userSyncInterval`、`vnc.scanInterval`。
   - **`min` 已是 0 的合法零值**（配额、告警阈值）：不要写 `allowzero`，`>= 0` 已包含 0。
4. **禁止 `enum` 与 `min`/`max` 写在同一字段**：解析直接失败。哨兵 0 只用 `allowzero`。
5. **`validate:"omitempty"` 只用于空字符串枚举**（如磁盘 `os`）。数字 0 不靠它跳过；`json:",omitempty"` 只管序列化省键。
6. **字符串不用 `min`/`max`**：空值/空白在域校验里判断（如 `platform.name`）；路径、URL、正则另写。
7. **新增字段三件套**（缺一不可）：
   - `types.go`：字段 + `validate` 标签（含 `json` tag）
   - `defaults.go`：`DefaultSettings(domain)` 补默认值（行不存在时落库）
   - `validate.go`：如标签不足（路径、交叉约束），补业务校验
8. **默认值**：`defaults.go` 仅用于新行创建和运行时默认回退。settings 是全新契约，不提供旧数据库行迁移、旧字段剥离或兼容读取。
   **Agent 下发字段**：凡进入 `pb.AgentSettings` 的默认（含 `DomainAgent` 与跨域组装的 `probe_interval`），字面量优先放在 `internal/contract/defaults.go`，并同步 `internal/agent` 的 `NewSettingsManager` 与 `GetAgentSettingsForNode`；对照测试见 `settings/agent_bootstrap_test.go` 与 `agent_test.go`。
9. **测试**：每个新字段加校验用例（合法值通过、越界值/非法枚举被拒）。

## 校验机制（了解如何被强制执行）

```
API 写 → UpdateByDomain → schema.DecodeForStore(domain, raw)
   → validateXxxSettings
       → ValidateStructByTags（parseValidateTag；递归 struct；原始切片套用字段标签）
GET schema → GenerateJSONSchema → 同一 parseValidateTag（元素界写在 items 上）
```

- 校验是**反射驱动的**：只要字段写了 `validate` 标签，自动纳入，无需注册。
- 支持指针字段解引用：nil 指针跳过；非 nil 时解引用目标值执行数值范围/枚举校验。
- 字段**没有**标签 = 校验层直接跳过 = 任意值通过。这就是"裸字段"的代价。
- 交叉字段（告警空条件、身份缓存 TTL 大小）与路径/URL 留在 `validate.go`，不进通用标签。

## 已知补漏（示例）

- 扫描漏网字段：`grep -n 'json:"' types.go | grep -v 'validate:'`（布尔与纯业务字符串除外）。

## 新增 Domain 的检查清单

新增设置域时，以 `internal/contract/schema/` 中的 schema 实现为事实源，沿现有 domain 实现逐项追踪：domain 注册与解析、字段类型和约束、默认值、业务校验、严格解码、service getter/按域分发、测试与需要的 API 暴露。不要按旧模板预设固定文件数或固定接线位置；确认每个新增行为是否被调用并由测试覆盖。

`DecodeForStore` 必须拒绝未知字段；业务校验错误使用 422，语法/绑定错误使用 400。新字段应覆盖合法值、越界/非法值及默认值序列化行为。

## 节点覆盖语义

节点覆盖是稀疏补丁：可覆盖域与字段以当前 schema 白名单为准。PUT 中缺失字段保留已有值，显式 `null` 删除覆盖并恢复继承，数组整体替换，空数组表示覆盖为空；未知字段拒绝。改动时核对 merge/validation/cache invalidation 实现及测试，不在本规则复制易变白名单或 TTL。

## 兼容与验证

Settings schema 是新契约；调整字段时按全仓引用一次性更新，不添加旧字段兼容读取或别名。对域迁移核对持久化、默认值、验证、API 与测试链路。运行相关 settings 测试及项目增量门禁 `./.agents/verifiers/verify-changed.sh`。
