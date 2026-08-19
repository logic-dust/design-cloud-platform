package system

import (
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/server/service/node"
	"design-cloud-engine/internal/server/service/user"
)

// Service aggregates system-level health and operational information.
//
// This is the ONLY leaf service permitted to hold *config.Config directly.
// It requires full read-only configuration to produce system status
// snapshots; other services must extract narrow Config structs instead
// (see docs/development/guide/code-conventions.md).
type Service struct {
	healthRepo *db.HealthRepo
	nodes      *node.Service
	userSvc    *user.Service
	cfg        *config.Config
	startedAt  time.Time
}

// NewService creates a new system service.
func NewService(healthRepo *db.HealthRepo, nodes *node.Service, userSvc *user.Service, cfg *config.Config) *Service {
	return &Service{
		healthRepo: healthRepo,
		nodes:      nodes,
		userSvc:    userSvc,
		cfg:        cfg,
		startedAt:  time.Now(),
	}
}
