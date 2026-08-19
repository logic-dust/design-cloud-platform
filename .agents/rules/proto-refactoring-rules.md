<!-- context-optimizer: cohesive-specialist -->
# Proto 重构原则

## 核心原则

Proto 是集群内部通信协议， **不面向外部消费者**。唯一面向外部的接口是 HTTP API（`internal/server/portal/api/`）。

因此：

## 规则

1. **禁止 reserve 字段。** 字段废弃时直接删，不保留 `reserved` 声明。旧协议通过 git 历史可追溯。
2. **字段号可复用。** 删除旧字段后，新字段可以使用之前的字段号。不需要绕过已被废弃的编号。
3. **message 名和字段名随便改。** 只要字段号不变，wire format 兼容，内部重构不需要保留旧名作为别名或文档。
4. **oneof 字段同理。** oneof 成员改名、重排、增删都随意，不保留历史留痕。
5. **探针与遥测时间戳统一命名。** 周期采集与遥测上报统一使用 `int64 reported_at`（Unix 秒打点），禁止使用语义模糊的裸 `timestamp`。
6. **结构物理分区严格对齐通信方向。** Agent → Server 载荷（`AgentMessage` 成员）集中定义在 Agent 分区；Server → Agent 载荷（`ServerCommand` 成员）集中定义在 Server 分区，严禁跨区错位定义。

## 原因

- 程序通过 binary 整体部署（不单独分发 proto 文件给第三方）
- Server ↔ Agent 通信由项目自己生成、编译、部署，不存在跨版本兼容需求
- 保留历史包袱只会增加 proto 文件的视觉噪音和理解成本

## 自动化扫描工具

在修改 `internal/contract/proto/` 前后，可以执行自动化分析脚本：

```bash
python3 .agents/scripts/scan_proto_issues.py
```

- 检查是否存在命名不一致、reserved 字段残留、oneof 包装命名混乱等历史包袱。

