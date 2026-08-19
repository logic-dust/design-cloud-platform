package connection

import (
	"context"
	"fmt"
	"io/fs"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// validateDeletePermission validates whether requestUser can delete the connection.
func validateDeletePermission(connection model.Connection, requestUser string) error {
	if !canAccessConnection(requestUser, connection) {
		return fmt.Errorf("%w: only admin or owner can delete connection %s (owner:%s)", fs.ErrPermission, connection.Name, connection.Username)
	}
	if connection.Source == model.ConnectionSourceFixed && requestUser != AllUsers {
		return fmt.Errorf("%w: fixed connection %s can only be deleted by admin", fs.ErrPermission, connection.Name)
	}
	return nil
}

// Delete deletes one or more connections by ID, stopping any active processes and updating caches.
func (s *Service) Delete(ctx context.Context, requestUser string, ids ...uint) error {
	if len(ids) == 0 {
		return nil
	}
	defer s.invalidateConnListCache(requestUser)

	conns, err := s.connectionRepo.GetByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("fetch connections for delete: %w", err)
	}

	// Pre-flight check: validate delete permissions for all connections before performing side-effects
	for _, c := range conns {
		if err := validateDeletePermission(c, requestUser); err != nil {
			return err
		}
	}

	var toDeleteIDs []uint
	affectedUsers := make(map[string]struct{})
	hasShared := false

	for _, c := range conns {
		if c.Protocol == contract.VNCProtocol && c.TargetNodeID != "" && c.Display != "" && (c.Status == model.ConnectionRunning || c.Status == model.ConnectionUnknown) {
			// Terminate physical VNC server process on the remote node to avoid orphan processes and zombie re-discovery
			if err := s.dispatchVNCControl(ctx, c, vncOpStop); err != nil {
				if s.logger != nil {
					s.logger.Warn("Failed to stop remote VNC process during connection delete", "connectionID", c.ID, "nodeID", c.TargetNodeID, "display", c.Display, "error", err)
				}
			}
		}

		if s.tunnelCloser != nil {
			_ = s.tunnelCloser.CloseByConnectionID(c.ID)
		}

		toDeleteIDs = append(toDeleteIDs, c.ID)
		if c.Shared {
			hasShared = true
		} else if c.Username != "" {
			affectedUsers[c.Username] = struct{}{}
		}
	}

	if len(toDeleteIDs) > 0 {
		if err := s.connectionRepo.DeleteByIDs(ctx, toDeleteIDs); err != nil {
			return fmt.Errorf("delete connections from db: %w", err)
		}
	}

	if hasShared {
		s.invalidateConnListCache("")
		s.notifyOwnerRefresh(ctx, "", 0)
	} else {
		for u := range affectedUsers {
			s.invalidateConnListCache(u)
			s.notifyOwnerRefresh(ctx, u, 0)
		}
	}

	return nil
}
