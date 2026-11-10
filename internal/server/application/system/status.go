package system

import (
	"cmp"
	"context"
	"fmt"
	"sync"
	"time"

	"design-cloud-engine/internal/platform/config"
)

// ==================== Types ====================

// ComponentStatus is a short health descriptor for a system component.
type ComponentStatus string

const (
	ComponentStatusHealthy       ComponentStatus = "healthy"
	ComponentStatusUnhealthy     ComponentStatus = "unhealthy"
	ComponentStatusNotConfigured ComponentStatus = "not_configured"
)

// StatusResponse is the full payload for GET /api/admin/system/status.
type StatusResponse struct {
	Platform PlatformStatus `json:"platform"`
	Auth     AuthStatus     `json:"auth"`
	Database DatabaseStatus `json:"database"`
	Portal   PortalStatus   `json:"portal"`
	Nodes    NodesStatus    `json:"nodes"`
	Log      LogStatus      `json:"log"`
}

// PlatformStatus is the platform identity / uptime / cluster server topology snapshot.
type PlatformStatus struct {
	Version       string   `json:"version"`
	UptimeSeconds int64    `json:"uptimeSeconds"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Servers       []string `json:"servers"`
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

// NodesStatus summarises enrolled cluster nodes (online/offline/maintenance only).
// Counts include both pure agents and composite server nodes; no derived splits.
type NodesStatus struct {
	Total       int `json:"total"`
	Online      int `json:"online"`
	Offline     int `json:"offline"`
	Maintenance int `json:"maintenance"`
}

// AuthStatus describes authentication configuration, three-role mode, and LDAP health.
type AuthStatus struct {
	AuthMode   string      `json:"authMode"`
	ThreeRoles bool        `json:"threeRoles"`
	LDAP       *LDAPStatus `json:"ldap,omitempty"`
}

// PortalStatus describes the portal HTTP server configuration.
type PortalStatus struct {
	Address string `json:"address"`
	TLS     bool   `json:"tls"`
	Cert    string `json:"cert,omitempty"`
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

	authStatus := AuthStatus{
		AuthMode:   string(s.cfg.User.AuthMode),
		ThreeRoles: s.cfg.Admin.EnableThreeRoles,
	}
	if s.cfg.User.AuthMode == config.UserAuthModeLDAP {
		authStatus.LDAP = &ldapStatus
	}

	return StatusResponse{
		Platform: s.platformStatus(ctx),
		Auth:     authStatus,
		Database: dbStatus,
		Portal:   s.portalStatus(),
		Nodes:    s.nodesStatus(ctx),
		Log:      s.logStatus(),
	}
}

func (s *Service) platformStatus(ctx context.Context) PlatformStatus {
	ps := PlatformStatus{
		Version:       config.VERSION,
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		Servers:       append([]string(nil), s.cfg.Runtime.Servers...),
	}
	if s.settingsSvc != nil {
		if plat, err := s.settingsSvc.GetPlatformSettings(ctx); err == nil {
			ps.Name = plat.Name
			ps.Description = plat.Description
		}
	}
	return ps
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

// nodesStatus 汇总已接入节点的运行状态（online/offline/maintenance）。
func (s *Service) nodesStatus(ctx context.Context) NodesStatus {
	online, offline, maintenance, err := s.nodes.CountRuntimeStatus(ctx)
	if err != nil {
		return NodesStatus{}
	}
	total := online + offline + maintenance
	return NodesStatus{
		Total:       int(total),
		Online:      int(online),
		Offline:     int(offline),
		Maintenance: int(maintenance),
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

func (s *Service) logStatus() LogStatus {
	return LogStatus{
		Dir:   s.cfg.Log.Dir,
		Level: s.cfg.Log.AppLevel.String(),
	}
}
