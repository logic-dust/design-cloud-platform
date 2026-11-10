package connection

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
)

// launchDial creates or reuses an RDP/XRDP connection row for the given resource.
func (s *Service) launchDial(ctx context.Context, username string, resourceID uint, res model.ConnectionResource) (model.Connection, error) {
	if s.connectionRepo == nil {
		return model.Connection{}, fmt.Errorf("connection repo uninitialized")
	}
	existing, err := s.connectionRepo.ListByUsernameAndResource(ctx, username, resourceID)
	if err != nil {
		return model.Connection{}, fmt.Errorf("list connections: %w", err)
	}
	if len(existing) > 0 && (res.MaxPerUser <= 1 || len(existing) >= res.MaxPerUser) {
		return s.reuseDialConnection(ctx, existing[0], res, username)
	}

	dialPort := cmp.Or(res.Protocol.DialDefaultPort(), 22)
	if res.Port != nil && *res.Port > 0 {
		dialPort = *res.Port
	}

	var usedNodes map[string]struct{}
	if len(existing) > 0 {
		usedNodes = make(map[string]struct{}, len(existing))
		for _, c := range existing {
			if c.NodeID == "" {
				continue
			}
			if c.Status == model.ConnectionStopped || c.Status == model.ConnectionTerminating {
				continue
			}
			usedNodes[c.NodeID] = struct{}{}
		}
	}

	node, err := s.placer.SelectNode(ctx, res, usedNodes)
	if err != nil {
		if len(existing) > 0 {
			return s.reuseDialConnection(ctx, existing[0], res, username)
		}
		return model.Connection{}, fmt.Errorf("select node for %s failed: %w", res.Protocol.String(), err)
	}
	now := time.Now()
	connName := model.ConnectionName(res.Name, node.NodeID)
	row := &model.Connection{
		ResourceID:     &resourceID,
		Name:           connName,
		Username:       username,
		Protocol:       res.Protocol,
		NodeID:         node.NodeID,
		Port:           dialPort,
		Display:        res.Display, // XRDP fixed seat reservation; RDP usually nil
		Status:         model.ConnectionRunning,
		LastAccessedAt: &now,
	}
	if err := s.connectionRepo.CreateWithResourceQuota(ctx, row); err != nil {
		if errors.Is(err, db.ErrQuotaExceeded) {
			fallbackExisting, listErr := s.connectionRepo.ListByUsernameAndResource(ctx, username, resourceID)
			if listErr != nil {
				return model.Connection{}, fmt.Errorf("list connections after quota conflict: %w", listErr)
			}
			if len(fallbackExisting) > 0 {
				return s.reuseDialConnection(ctx, fallbackExisting[0], res, username)
			}
		}
		if errors.Is(err, db.ErrDisplayOccupied) {
			return model.Connection{}, ErrDisplayOccupied
		}
		return model.Connection{}, err
	}
	out := *row
	s.attachDialHost(ctx, &out)
	return out, nil
}

// reuseDialConnection keeps the same connection id (UI tombstone). Running rows
// only refresh access time; stopped/other rows re-run resource placement.
// Terminating dial rows (Stop kill failure; no seat probe for RDP) retry teardown
// before placement so Launch cannot resurrect a ghost session.
func (s *Service) reuseDialConnection(ctx context.Context, conn model.Connection, res model.ConnectionResource, requestUser string) (model.Connection, error) {
	if conn.Status == model.ConnectionTerminating {
		resolved, err := s.resolveDialTerminating(ctx, conn)
		if err != nil {
			return model.Connection{}, err
		}
		conn = resolved
	}
	if conn.Status == model.ConnectionRunning && conn.NodeID != "" {
		return s.refreshDialAccess(ctx, conn, requestUser)
	}
	return s.placeDialOnConnection(ctx, conn, res, requestUser)
}

// resolveDialTerminating retries remote session teardown for a dial terminating
// tombstone, then collapses it to stopped (resource kept) or deletes (resource cleared).
func (s *Service) resolveDialTerminating(ctx context.Context, conn model.Connection) (model.Connection, error) {
	if _, err := s.teardownRemoteSession(ctx, conn); err != nil {
		return model.Connection{}, fmt.Errorf("retry session teardown for terminating connection %d: %w", conn.ID, err)
	}
	if err := s.finishTerminatingAfterSeatGone(ctx, conn); err != nil {
		return model.Connection{}, err
	}
	if conn.ResourceID == nil {
		return model.Connection{}, fmt.Errorf("terminating connection %d was deleted after teardown", conn.ID)
	}
	got, err := s.connectionRepo.GetByID(ctx, conn.ID)
	if err != nil {
		return model.Connection{}, err
	}
	return got, nil
}
