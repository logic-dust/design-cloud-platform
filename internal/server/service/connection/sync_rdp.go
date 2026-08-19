package connection

import (
	"context"
	"errors"
	"fmt"
	"net"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/contract"
	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/model"
)

const (
	DefaultRDPPort = 3389
	DefaultRDPName = "Windows 3D Workstation"
)

// SyncStaticRDPConnection ensures a static, shared RDP connection exists for a Windows node
// and reconciles its host, listener port and readiness state.
//
// rdp carries the latest agent probe result (availability + actual listener port). A nil rdp
// means "no observation" — node registration, a Linux agent, or an indeterminate Windows
// probe: a missing record is then created with the unknown state, while an existing record
// keeps its state and port untouched.
func (s *Service) SyncStaticRDPConnection(ctx context.Context, nodeID, peerAddr string, rdp *pb.RDPStatus) error {
	if nodeID == "" {
		return fmt.Errorf("node ID is required")
	}

	host := ""
	if peerAddr != "" {
		if h, _, err := net.SplitHostPort(peerAddr); err == nil {
			host = h
		} else {
			host = peerAddr
		}
	}
	if host == "" || host == "127.0.0.1" || host == "::1" {
		if node, err := s.nodeRepo.GetByNodeID(ctx, nodeID); err == nil && node.ClientHost != "" {
			host = node.ClientHost
		}
	}

	// An absent probe result is "no observation", never a state of its own: it must not
	// overwrite what the agent reported earlier.
	port, status, observed := uint(DefaultRDPPort), model.ConnectionUnknown, false
	if rdp != nil {
		observed = true
		status = model.ConnectionStopped
		if rdp.Available {
			status = model.ConnectionRunning
		}
		if rdp.Port != 0 {
			port = uint(rdp.Port)
		}
	}

	existing, found, err := s.connectionRepo.FindSharedStatic(ctx, nodeID, contract.RDPProtocol)
	if err != nil {
		return fmt.Errorf("lookup shared RDP connection for node %s: %w", nodeID, err)
	}

	if !found {
		newConn := &model.Connection{
			Name:         DefaultRDPName,
			TargetNodeID: nodeID,
			Host:         host,
			Port:         port,
			Protocol:     contract.RDPProtocol,
			Shared:       true,
			Source:       model.ConnectionSourceFixed,
			Status:       status,
		}
		if err := s.connectionRepo.CreateWithQuota(ctx, newConn, 0); err != nil {
			if dErr, ok := errors.AsType[common.DomainError](err); ok && dErr.HTTPStatus() == 422 {
				s.logger.Debug("Skip registering shared RDP connection on non-admitting node", "node_id", nodeID, "reason", dErr.Error())
				return nil
			}
			// Concurrent registration/heartbeat may race on the shared node+protocol unique index.
			if common.IsUniqueConstraintError(err) {
				existing, found, findErr := s.connectionRepo.FindSharedStatic(ctx, nodeID, contract.RDPProtocol)
				if findErr != nil {
					return fmt.Errorf("lookup shared RDP after unique conflict for node %s: %w", nodeID, findErr)
				}
				if !found {
					return fmt.Errorf("create static RDP connection conflict but no match found for node %s: %w", nodeID, err)
				}
				return s.patchSharedRDP(ctx, &existing, host, port, status, observed)
			}
			return fmt.Errorf("create static RDP connection for node %s: %w", nodeID, err)
		}
		s.invalidateConnListCache("")
		s.notifyOwnerRefresh(ctx, "", newConn.ID)
		return nil
	}

	return s.patchSharedRDP(ctx, &existing, host, port, status, observed)
}

// patchSharedRDP updates only host/status/port on an existing shared RDP row so concurrent
// alias / lastAccessedAt edits are not overwritten by heartbeat reconciliation.
func (s *Service) patchSharedRDP(ctx context.Context, existing *model.Connection, host string, port uint, status model.ConnectionStatus, observed bool) error {
	fields := map[string]any{}

	if !observed {
		if host != "" && existing.Host != host {
			fields["host"] = host
		}
	} else {
		if existing.Status != status {
			fields["status"] = status
		}
		if existing.Port != port {
			fields["port"] = port
		}
		if host != "" && existing.Host != host {
			fields["host"] = host
		}
	}

	if len(fields) == 0 {
		return nil
	}

	if err := s.connectionRepo.UpdateFields(ctx, existing.ID, fields); err != nil {
		return fmt.Errorf("update static RDP connection %d for node %s: %w", existing.ID, existing.TargetNodeID, err)
	}

	s.invalidateConnListCache("")
	s.notifyOwnerRefresh(ctx, "", existing.ID)
	return nil
}
