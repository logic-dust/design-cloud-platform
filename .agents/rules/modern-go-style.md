<!-- context-optimizer: cohesive-specialist -->
# Go 与数值类型约束

- `cmp.Or` 只用于 fallback 纯值/无副作用；不能用于昂贵调用或 `0`/`false` 是有效值的字段。标准库能力以 `go.mod` 版本和当前实现为准。
- 密码、Token 等安全随机值必须使用 `crypto/rand`；文件系统边界遵循项目安全实现并覆盖符号链接/路径逃逸用例。
- 内存、磁盘等资源容量使用 `int64`，避免 32 位溢出及无符号差值下溢；时间戳和时长遵循各自源契约。
- GORM 标识遵循当前模型/驱动契约；切片索引、循环计数保持 Go 原生 `int`。跨层仅在边界要求时转换。
- 不因风格统一单独改变协议或持久化字段类型；既有字段只有在缺陷、溢出或性能证据下做破坏性迁移。
- 业务与 GORM `serializer:json` 的 JSON 统一走 `internal/platform/jsoncodec`（`encoding/json/v2`）；不要直接 `import "encoding/json"` 编解码业务载荷。

按需扫描现代 Go 可用性：`python3 .agents/scripts/scan_modern_go.py`。
