package api

// 本文件声明 Portal API 的显式依赖集合。
//
// 它只包含顶层路由与中间件装配需要的对象：15 个业务服务、3 个共享基础设施对象、
// 2 个应用读模型和 1 个应用用例入口。装配期对象（repos、dispatcher、invalidator、
// banCache）刻意不在此列——handler 不应该看见它们。
//
// 单个 handler 仍然只接收自己需要的那几个依赖（见 newXxxHandler），本结构体只
// 服务于 NewHandler 这一层组装；禁止把 Dependencies 下传进业务 handler。

import (
	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/server/application/identity"
	"design-cloud-engine/internal/server/application/overview"
	"design-cloud-engine/internal/server/application/system"
	"design-cloud-engine/internal/server/push"
	"design-cloud-engine/internal/server/service/alert"
	"design-cloud-engine/internal/server/service/app"
	"design-cloud-engine/internal/server/service/connection"
	"design-cloud-engine/internal/server/service/desktop"
	"design-cloud-engine/internal/server/service/file"
	"design-cloud-engine/internal/server/service/license"
	"design-cloud-engine/internal/server/service/node"
	"design-cloud-engine/internal/server/service/proxy"
	"design-cloud-engine/internal/server/service/role"
	"design-cloud-engine/internal/server/service/scheduler"
	"design-cloud-engine/internal/server/service/security"
	"design-cloud-engine/internal/server/service/session"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/storage"
	"design-cloud-engine/internal/server/service/ticket"
	"design-cloud-engine/internal/server/service/user"
)

// Dependencies is the explicit input of Portal route assembly.
//
// Field layout mirrors the layering: infrastructure objects first, then domain
// services in the same order as the assembly root, then application use cases.
type Dependencies struct {
	// 基础设施（生命周期由 Server 管理，handler 只读使用）
	Settings *settings.Service
	Audit    *audit.Service
	Push     *push.Hub

	// 单域业务服务
	User       *user.Service
	Role       *role.Service
	File       *file.Service
	Scheduler  *scheduler.Service
	Node       *node.Service
	Proxy      *proxy.Service
	Session    *session.Service
	Security   *security.Service
	App        *app.Service
	License    *license.Service
	Connection *connection.Service
	System     *system.Service
	Storage    *storage.Service
	Overview   *overview.Service
	Ticket     *ticket.Service
	Desktop    *desktop.Service
	Alert      *alert.Evaluator

	// 跨域聚合读模型与应用用例
	Identity *identity.Service
}
