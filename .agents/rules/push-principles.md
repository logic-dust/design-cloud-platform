# Push 推送原则

## 核心原则：推送只服务"不知情方"

操作者走 **请求→响应** 链路，response 即反馈，前端据此更新 UI——**操作者不推送**。

需要推送的只有两类：

1. **被动方**：不在请求链路上、无法从 response 得知变化的人（被踢者、被分配者、连接归属者）
2. **异步链路**：请求已返回但业务结果未定（agent 上报驱动的数据变化、VNC 发现）

### 判定表（写推送前对照）

| 场景 | 链路 | 是否推送 | 推给谁 |
|:--|:--|:--|:--|
| 同步 CRUD（创建/删除/控制），操作者=知情者 | 同步 | ❌ 不推 | — |
| 跨操作者（admin 删他人连接） | 同步 | ✅ 推 | 归属者（非操作者） |
| 被踢/被分配等被动方 | 同步 | ✅ 推 | 被动方 |
| 异步采集/发现（agent 触发） | 异步 | ✅ 推 | 归属者 |

### 实施要点

- **作业操作（submit/control）不推送**：sbatch/scancel 同步秒回，response 含 jobID/失败信息
- **作业状态演进**（排队→运行→完成）若需实时通知，属异步链路，另行推送（不在操作请求内）
- **VNC 异步发现**（sync_vnc 链路）：agent 上报创建连接，无请求方，必须推给归属用户

## 定向投递

- per-user 数据必须 `PublishToUser`（服务端按 userID 路由，泄露面为零）
- **禁止** `PublishToRole` / 广播 per-user 数据（其他用户收到无意义且泄露他人信息）
- 同一用户多标签页 = 同一 userID 下多个 Client，`PublishToUser` 全部送达

## 通知与数据分离（EventPayload.Notify）

- **纯数据刷新**：`notify` 为 nil（JSON 不输出字段）——前端不弹通知
- **弹通知**：`notify = {title, message, level}`——前端据此展示 toast
- 前端只判断 `payload.notify` 是否为 null 即可，无通知时是纯刷新
- Go 实现：`Notify *Notification \`json:"notify,omitempty"\``——**必须用指针**
  （`omitempty` 对 struct 值类型无效，结构体永远非空；只有 nil 指针才省略字段）

## 命名

- 请求发起者一律用 `RequestUser`（或 `requestUser`），禁止歧义的 `User`
  ——后者与"资源属主"概念撞车
