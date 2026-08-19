<!-- context-optimizer: cohesive-specialist -->
# 后端分层与缓存边界

## DB / Service 职责

- `internal/db/` 只处理持久化操作、事务原子性和领域中性的查询组装；业务策略、状态机、鉴权、配置解释及跨领域编排放在 Service。
- 新增 DB 方法时确认它描述的是数据读写而不是业务决策；import 依赖边界以 `.agents/rules/arch-rules.yaml` 和 `verify-arch.py` 为准。

## Service 辅助抽取

仅在逻辑阶段独立、校验/转换明显成组或嵌套逻辑难以理解时提取私有函数。避免机械按行数、参数数量或调用次数拆分；小而直观的逻辑保留内联。

## 派生缓存失效

由配置或持久化状态派生的缓存必须与数据变更显式联动失效；失效机制应与现有 `Invalidator` / 服务装配方式保持一致，不能以无关的定时轮询替代变更通知。
