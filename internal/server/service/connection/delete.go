package connection

import (
	"context"
	"fmt"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// validateDeletePermission validates whether requestUser can delete the connection.
func validateDeletePermission(connection model.Connection, requestUser string) error {
	if requestUser != AllUsers && connection.Username != requestUser {
		return contract.NewForbiddenError(fmt.Sprintf("only admin or owner can delete connection %d (owner:%s)", connection.ID, connection.Username))
	}
	return nil
}

// DeleteResult summarizes whether connections were deleted or entered terminating tombstones.
type DeleteResult struct {
	DeletedCount     int `json:"deletedCount"`
	TerminatingCount int `json:"terminatingCount"`
}

// Delete removes connections after the same remote teardown as Stop.
// On successful teardown (or nothing to kill): physical delete (quota released).
// On kill failure when a teardown was attempted: terminating tombstone (resource cleared).
func (s *Service) Delete(ctx context.Context, requestUser string, ids ...uint) (DeleteResult, error) {
	var result DeleteResult
	if len(ids) == 0 {
		return result, nil
	}

	conns, err := s.connectionRepo.GetByIDs(ctx, ids)
	if err != nil {
		return result, fmt.Errorf("fetch connections for delete: %w", err)
	}

	for _, c := range conns {
		if err := validateDeletePermission(c, requestUser); err != nil {
			return result, err
		}
	}

	affectedUsers := make(map[string]struct{})
	for _, c := range conns {
		if s.proxyConnectionCloser != nil {
			_ = s.proxyConnectionCloser.CloseAllByConnectionID(c.ID)
		}

		terminating, err := s.deleteOne(ctx, c)
		if err != nil {
			return result, err
		}
		if terminating {
			result.TerminatingCount++
		} else {
			result.DeletedCount++
		}
		if c.Username != "" {
			affectedUsers[c.Username] = struct{}{}
		}
	}

	for u := range affectedUsers {
		s.notifyOwnerRefresh(ctx, requestUser, u, 0)
	}
	return result, nil
}

func (s *Service) deleteOne(ctx context.Context, c model.Connection) (bool, error) {
	attempted, killErr := s.teardownRemoteSession(ctx, c)
	if killErr != nil {
		if s.logger != nil {
			s.logger.Warn("seat/session kill failed on delete; marking terminating",
				"connectionID", c.ID, "protocol", c.Protocol.String(),
				"nodeID", c.NodeID, "error", killErr)
		}
		if attempted {
			if markErr := s.connectionRepo.MarkTerminatingClearResource(ctx, c.ID); markErr != nil {
				return false, fmt.Errorf("mark terminating after kill failure: %w", markErr)
			}
			return true, nil
		}
		return false, fmt.Errorf("delete teardown: %w", killErr)
	}

	if err := s.connectionRepo.DeleteByID(ctx, c.ID); err != nil {
		return false, fmt.Errorf("delete connection %d: %w", c.ID, err)
	}
	return false, nil
}

// finishTerminatingKill reuses delete/stop kill semantics for probe-driven tombstone cleanup.
func (s *Service) finishTerminatingKill(ctx context.Context, c model.Connection) error {
	if _, err := s.teardownRemoteSession(ctx, c); err != nil {
		return err
	}
	return s.finishTerminatingAfterSeatGone(ctx, c)
}

func (s *Service) finishTerminatingAfterSeatGone(ctx context.Context, c model.Connection) error {
	if c.ResourceID == nil {
		return s.connectionRepo.DeleteByID(ctx, c.ID)
	}
	return s.markStoppedTombstone(ctx, c)
}
