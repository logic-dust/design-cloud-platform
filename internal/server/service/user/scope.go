package user

import (
	"fmt"
	"slices"

	"design-cloud-engine/internal/model"
)

// ScopeMeta contains metadata for an AdminScope to be rendered by clients.
type ScopeMeta struct {
	Key   string `json:"key"`
	Value uint64 `json:"value"`
	Name  string `json:"name"`
	Desc  string `json:"desc"`
}

// Admin scope business-logic helpers.
//
// These functions were originally defined in the model package, but they
// contain business-validation logic that does not belong in the GORM model
// layer (model must remain free of business rules per arch-rules.yaml).
// They are kept here, in the user service, because the only caller of the
// validation/parse functions is user.Service.SetScopes/SetScopesByNames, and
// the schema/name functions are surfaced to the API via the user service.

// scopeNameSysAdmin is the catalog name that maps to the ScopeAll sentinel.
const scopeNameSysAdmin = "systemadmin"

// scopeDef is one row of the canonical scope catalog.
type scopeDef struct {
	name  string
	scope model.AdminScope
	label string
	desc  string
}

// scopeCatalog is the single source of truth for delegatable admin scopes:
// AllowedScopes, name parsing and the API schema are all derived from it.
// Scope keys use singular nouns so they align with settings domain ids
// (schema.Domain) and admin console module ids.
// Adding a scope = add one row here, then wire the route middleware, the
// parent-chain scope list, BuildAdminConsoleModules and the docs.
var scopeCatalog = []scopeDef{
	{"node", model.ScopeNodes, "节点管理", "节点注册/证书/token"},
	{"connection", model.ScopeConnections, "连接管理", "桌面/VNC 会话管理"},
	{"scheduler", model.ScopeSchedulers, "作业调度", "作业提交/控制/修改"},
	{"app", model.ScopeApps, "应用管理", "应用目录管理"},
	{"license", model.ScopeLicenses, "许可证管理", "许可证的创建与维护"},
	{"file", model.ScopeFiles, "文件审核", "文件下载申请审核"},
	{"settings", model.ScopeSettings, "系统配置", "系统配置管理"},
	{"ticket", model.ScopeTickets, "工单管理", "工单/项目管理"},
	{"user", model.ScopeUsers, "用户管理", "门户账号、目录组与 ID 映射缓存（不含角色与 Scope 授权）"},
	{"session", model.ScopeSessions, "会话管理", "强制登出/会话清理"},
	{"ban", model.ScopeBans, "封禁管理", "封禁记录查看/解除"},
	{"storage", model.ScopeStorages, "存储管理", "共享存储状态/快照查看"},
	{"status", model.ScopeStatuses, "系统状态", "系统运行状态查看"},
	{"desktop", model.ScopeDesktop, "桌面发布", "桌面客户端与插件发布管理"},
}

var (
	// AllowedScopes is the whitelist of granular bits, derived from scopeCatalog.
	AllowedScopes = buildAllowedScopes()
	scopeName     = buildScopeName()
	scFromName    = buildScopeFromName()
)

func buildAllowedScopes() model.AdminScopes {
	var allowed model.AdminScopes
	for _, def := range scopeCatalog {
		allowed.Set(def.scope)
	}
	return allowed
}

func buildScopeName() map[model.AdminScope]string {
	m := make(map[model.AdminScope]string, len(scopeCatalog)+1)
	m[model.ScopeAll] = scopeNameSysAdmin
	for _, def := range scopeCatalog {
		m[def.scope] = def.name
	}
	return m
}

func buildScopeFromName() map[string]model.AdminScope {
	m := make(map[string]model.AdminScope, len(scopeCatalog)+1)
	m[scopeNameSysAdmin] = model.ScopeAll
	for _, def := range scopeCatalog {
		m[def.name] = def.scope
	}
	return m
}

func ParseScopes(names []string) (model.AdminScopes, error) {
	var scopes model.AdminScopes
	for _, name := range names {
		scope, ok := scFromName[name]
		if !ok {
			return 0, fmt.Errorf("%w: %s", model.ErrInvalidScope, name)
		}
		scopes.Set(scope)
	}
	return scopes, nil
}

func ScopeNames(scopes model.AdminScopes) []string {
	if scopes == 0 {
		return nil
	}
	// 全量子管理员：直接返回 "systemadmin"，避免因 Has 短接而展开所有粒化 scope
	if scopes == model.AdminScopes(model.ScopeAll) {
		return []string{scopeNameSysAdmin}
	}
	names := make([]string, 0, len(scopeName)-1)
	for scope, name := range scopeName {
		if scope == model.ScopeAll {
			continue
		}
		if scopes.Has(scope) {
			names = append(names, name)
		}
	}
	// Deterministic output: map iteration order would otherwise reshuffle API responses.
	slices.Sort(names)
	return names
}

func validateScopes(scopes model.AdminScopes) error {
	if scopes == model.AdminScopes(model.ScopeAll) {
		return nil
	}
	if int64(scopes)&^int64(AllowedScopes) != 0 {
		return fmt.Errorf("%w: %d", model.ErrInvalidScope, scopes)
	}
	return nil
}

func GetScopeSchema() []ScopeMeta {
	schema := make([]ScopeMeta, 0, len(scopeCatalog)+1)
	// The full-delegation sentinel is grantable by name, so it must be discoverable here too.
	schema = append(schema, ScopeMeta{
		Key:   scopeNameSysAdmin,
		Value: ^uint64(0),
		Name:  "全量管理",
		Desc:  "一次性授予全部管理域（不展开为粒化 scope）",
	})
	for _, def := range scopeCatalog {
		schema = append(schema, ScopeMeta{
			Key:   def.name,
			Value: uint64(def.scope),
			Name:  def.label,
			Desc:  def.desc,
		})
	}
	return schema
}
