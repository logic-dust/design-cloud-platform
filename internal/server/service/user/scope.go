package user

import (
	"fmt"
	"strings"

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

const (
	scNameSysAdmin    = "systemadmin"
	scNameNode        = "node"
	scNameConnections = "connections"
	scNameScheduler   = "scheduler"
	scNameApps        = "apps"
	scNameLicenses    = "licenses"
	scNameFiles       = "files"
	scNameSetting     = "setting"
	scNameTickets     = "tickets"
	scNameUsers       = "users"
	scNameSessions    = "sessions"
	scNameBans        = "bans"
	scNameStorage     = "storage"
	scNameStatus      = "status"
)

var scopeName = map[model.AdminScope]string{
	model.ScopeAll:         scNameSysAdmin,
	model.ScopeNodes:       scNameNode,
	model.ScopeConnections: scNameConnections,
	model.ScopeSchedulers:  scNameScheduler,
	model.ScopeApps:        scNameApps,
	model.ScopeLicenses:    scNameLicenses,
	model.ScopeFiles:       scNameFiles,
	model.ScopeSettings:    scNameSetting,
	model.ScopeTickets:     scNameTickets,
	model.ScopeUsers:       scNameUsers,
	model.ScopeSessions:    scNameSessions,
	model.ScopeBans:        scNameBans,
	model.ScopeStorages:    scNameStorage,
	model.ScopeStatuses:    scNameStatus,
}

var scFromName = map[string]model.AdminScope{
	scNameSysAdmin:    model.ScopeAll,
	scNameNode:        model.ScopeNodes,
	scNameConnections: model.ScopeConnections,
	scNameScheduler:   model.ScopeSchedulers,
	scNameApps:        model.ScopeApps,
	scNameLicenses:    model.ScopeLicenses,
	scNameFiles:       model.ScopeFiles,
	scNameSetting:     model.ScopeSettings,
	scNameTickets:     model.ScopeTickets,
	scNameUsers:       model.ScopeUsers,
	scNameSessions:    model.ScopeSessions,
	scNameBans:        model.ScopeBans,
	scNameStorage:     model.ScopeStorages,
	scNameStatus:      model.ScopeStatuses,
}

const AllowedScopes = model.AdminScopes(
	model.ScopeNodes |
		model.ScopeConnections |
		model.ScopeSchedulers |
		model.ScopeApps |
		model.ScopeLicenses |
		model.ScopeFiles |
		model.ScopeSettings |
		model.ScopeTickets |
		model.ScopeUsers |
		model.ScopeSessions |
		model.ScopeBans |
		model.ScopeStorages |
		model.ScopeStatuses,
)

func ParseScopes(names []string) (model.AdminScopes, error) {
	var scopes model.AdminScopes
	for _, name := range names {
		scope, ok := scFromName[strings.ToLower(name)]
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
		return []string{scNameSysAdmin}
	}
	var names []string
	for scope, name := range scopeName {
		if scope == model.ScopeAll {
			continue
		}
		if scopes.Has(scope) {
			names = append(names, name)
		}
	}
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
	return []ScopeMeta{
		{
			Key:   scNameNode,
			Value: uint64(model.ScopeNodes),
			Name:  "节点管理",
			Desc:  "节点注册/证书/token",
		},
		{
			Key:   scNameConnections,
			Value: uint64(model.ScopeConnections),
			Name:  "连接管理",
			Desc:  "桌面/VNC 会话管理",
		},
		{
			Key:   scNameScheduler,
			Value: uint64(model.ScopeSchedulers),
			Name:  "作业调度",
			Desc:  "作业提交/控制/修改",
		},
		{
			Key:   scNameApps,
			Value: uint64(model.ScopeApps),
			Name:  "应用管理",
			Desc:  "应用目录管理",
		},
		{
			Key:   scNameLicenses,
			Value: uint64(model.ScopeLicenses),
			Name:  "许可证管理",
			Desc:  "许可证的创建与维护",
		},
		{
			Key:   scNameFiles,
			Value: uint64(model.ScopeFiles),
			Name:  "文件审核",
			Desc:  "文件下载申请审核",
		},
		{
			Key:   scNameSetting,
			Value: uint64(model.ScopeSettings),
			Name:  "系统配置",
			Desc:  "系统配置管理",
		},
		{
			Key:   scNameTickets,
			Value: uint64(model.ScopeTickets),
			Name:  "工单管理",
			Desc:  "工单/项目管理",
		},
		{
			Key:   scNameUsers,
			Value: uint64(model.ScopeUsers),
			Name:  "用户管理",
			Desc:  "用户/角色/同步管理",
		},
		{
			Key:   scNameSessions,
			Value: uint64(model.ScopeSessions),
			Name:  "会话管理",
			Desc:  "强制登出/会话清理",
		},
		{
			Key:   scNameBans,
			Value: uint64(model.ScopeBans),
			Name:  "封禁管理",
			Desc:  "封禁记录查看/解除",
		},
		{
			Key:   scNameStorage,
			Value: uint64(model.ScopeStorages),
			Name:  "存储管理",
			Desc:  "共享存储状态/快照查看",
		},
		{
			Key:   scNameStatus,
			Value: uint64(model.ScopeStatuses),
			Name:  "系统状态",
			Desc:  "系统运行状态查看",
		},
	}
}
