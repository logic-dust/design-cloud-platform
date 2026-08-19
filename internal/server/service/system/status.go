package system

import (
	"cmp"
	"context"
	"fmt"
	"sync"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/model"
)

// ==================== Types ====================

// ComponentStatus is a short health descriptor for a system component.
type ComponentStatus string

const (
	ComponentStatusHealthy       ComponentStatus = "healthy"
	ComponentStatusUnhealthy     ComponentStatus = "unhealthy"
	ComponentStatusNotConfigured ComponentStatus = "not_configured"
)

// StatusResponse is the full payload for GET /api/system/status.
type StatusResponse struct {
	Version        string         `json:"version"`
	UptimeSeconds  int64          `json:"uptimeSeconds"`
	ControlServers []string       `json:"controlServers"`
	Portal         PortalStatus   `json:"portal"`
	Database       DatabaseStatus `json:"database"`
	User           UserStatus     `json:"user"`
	Nodes          NodesStatus    `json:"nodes"`
	Admin          AdminStatus    `json:"admin"`
	Log            LogStatus      `json:"log"`
}

// DatabaseStatus includes Ping result and connection pool metrics.
type DatabaseStatus struct {
	Status        ComponentStatus `json:"status"`
	Type          string          `json:"type"`
	Host          string          `json:"host,omitempty"`
	LatencyMs     int64           `json:"latencyMs,omitempty"`
	Error         string          `json:"error,omitempty"`
	PoolOpen      int             `json:"poolOpen"`
	PoolInUse     int             `json:"poolInUse"`
	PoolIdle      int             `json:"poolIdle"`
	PoolWaitTotal int64           `json:"poolWaitTotal"`
}

// LDAPStatus describes LDAP server connectivity.
type LDAPStatus struct {
	Status    ComponentStatus `json:"status"`
	Server    string          `json:"server,omitempty"`
	LatencyMs int64           `json:"latencyMs,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// NodesStatus summarises the cluster node topology from the DB node table.
type NodesStatus struct {
	Total   int `json:"total"`
	Online  int `json:"online"`
	Offline int `json:"offline"`
}

// UserStatus describes the user authentication configuration and LDAP health.
type UserStatus struct {
	AuthMode string      `json:"authMode"`
	LDAP     *LDAPStatus `json:"ldap,omitempty"`
}

// PortalStatus describes the portal HTTP server configuration.
type PortalStatus struct {
	Address string `json:"address"`
	TLS     bool   `json:"tls"`
	Cert    string `json:"cert,omitempty"`
}

// AdminStatus describes the admin role configuration.
type AdminStatus struct {
	ThreeRoles bool `json:"threeRoles"`
}

// LogStatus describes the logging configuration.
type LogStatus struct {
	Dir   string `json:"dir"`
	Level string `json:"level"`
}

// GetStatus concurrently gathers health information from all components.
func (s *Service) GetStatus(ctx context.Context) StatusResponse {
	var (
		dbStatus   DatabaseStatus
		ldapStatus LDAPStatus
		wg         sync.WaitGroup
	)

	wg.Go(func() {
		dbStatus = s.checkDatabase(ctx)
	})

	wg.Go(func() {
		ldapStatus = s.checkLDAP(ctx)
	})

	wg.Wait()

	userStatus := UserStatus{AuthMode: string(s.cfg.User.AuthMode)}
	if s.cfg.User.AuthMode == config.UserAuthModeLDAP {
		userStatus.LDAP = &ldapStatus
	}

	return StatusResponse{
		Version:        config.VERSION,
		UptimeSeconds:  int64(time.Since(s.startedAt).Seconds()),
		ControlServers: s.cfg.Runtime.Servers,
		Portal:         s.portalStatus(),
		Database:       dbStatus,
		User:           userStatus,
		Nodes:          s.nodesStatus(ctx),
		Admin:          s.adminStatus(),
		Log:            s.logStatus(),
	}
}

func (s *Service) checkDatabase(ctx context.Context) DatabaseStatus {
	latency, err := s.healthRepo.Check(ctx)
	stats := s.healthRepo.PoolStats()

	ds := DatabaseStatus{
		Type:          string(s.cfg.Database.Type),
		PoolOpen:      stats.OpenConnections,
		PoolInUse:     stats.InUse,
		PoolIdle:      stats.Idle,
		PoolWaitTotal: stats.WaitCount,
	}

	if s.cfg.Database.Type == config.DatabaseTypePostgres {
		ds.Host = fmt.Sprintf("%s:%s", s.cfg.Database.Host, s.cfg.Database.Port)
	}

	if err != nil {
		ds.Status = ComponentStatusUnhealthy
		ds.Error = err.Error()
	} else {
		ds.Status = ComponentStatusHealthy
		ds.LatencyMs = latency
	}

	return ds
}

func (s *Service) checkLDAP(ctx context.Context) LDAPStatus {
	result := s.userSvc.LDAPHealthCheck(ctx)
	if !result.Configured {
		return LDAPStatus{Status: ComponentStatusNotConfigured}
	}

	ls := LDAPStatus{
		Server: s.cfg.User.Ldap.Server,
	}
	if result.Err != nil {
		ls.Status = ComponentStatusUnhealthy
		ls.Error = result.Err.Error()
	} else {
		ls.Status = ComponentStatusHealthy
		ls.LatencyMs = result.LatencyMs
	}
	return ls
}

// nodesStatus 汇总节点状态（真实状态口径）。
// 注意：Offline 只计显式 offline，pending/maintenance/disabled 不计入
// offline——与 dashboard.refreshNodeStatus 的 X/Y 口径（非 online 全计
// offline）不同，勿合并两处实现。
func (s *Service) nodesStatus(ctx context.Context) NodesStatus {
	statusCounts, err := s.nodes.CountByStatus(ctx)
	if err != nil {
		return NodesStatus{}
	}
	total := int64(0)
	for _, c := range statusCounts {
		total += c
	}

	return NodesStatus{
		Total:   int(total),
		Online:  int(statusCounts[model.NodeStatusOnline]),
		Offline: int(statusCounts[model.NodeStatusOffline]),
	}
}

func (s *Service) portalStatus() PortalStatus {
	host := cmp.Or(s.cfg.Portal.Host, "0.0.0.0")
	return PortalStatus{
		Address: fmt.Sprintf("%s:%s", host, s.cfg.Portal.Port),
		TLS:     s.cfg.Portal.TLS,
		Cert:    s.cfg.Portal.Cert,
	}
}

func (s *Service) adminStatus() AdminStatus {
	return AdminStatus{
		ThreeRoles: s.cfg.Admin.EnableThreeRoles,
	}
}

func (s *Service) logStatus() LogStatus {
	return LogStatus{
		Dir:   s.cfg.Log.Dir,
		Level: s.cfg.Log.AppLevel.String(),
	}
}
