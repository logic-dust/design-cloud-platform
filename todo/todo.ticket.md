
# 工单系统（Tickets）

## 附件上传

当前 `Attachments` 仅存 JSON 元信息（`fileName` / `filePath`），无实际文件上传/下载逻辑。

- [ ] 附件上传端点 — 接收文件、落盘、回写元信息到 ticket
- [ ] 附件下载/预览端点
- [ ] 与现有 `internal/server/service/file/` 对接或独立实现
