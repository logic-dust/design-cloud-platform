package connection

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// UpsertFixedResult is the summary returned by UpsertFixed.
type UpsertFixedResult struct {
	Created int             `json:"created"`
	Updated int             `json:"updated"`
	Failed  []UpsertFailure `json:"failed,omitempty"`
}

// UpsertFailure describes a single record that could not be upserted.
type UpsertFailure struct {
	Index        int    `json:"index"`
	Username     string `json:"username,omitempty"`
	TargetNodeID string `json:"targetNodeId,omitempty"`
	Reason       string `json:"reason"`
}

// UpsertFixed bulk-creates or updates fixed connections.
// Each record is matched against existing fixed connections by the priority:
//  1. port > 0  → (username, targetNodeId, port)
//  2. port == 0, display != "" → (username, targetNodeId, display)
//  3. port == 0, display == "" → (username, targetNodeId) node-level slot
//
// Validation failures are skipped (skip-and-report); valid records are always processed.
// The caller receives a structured result with created/updated counts and failure details.
func (s *Service) UpsertFixed(ctx context.Context, forms []CreateFixedConnectionForm) (UpsertFixedResult, error) {
	result := UpsertFixedResult{}

	affectedUsers := make(map[string]struct{})
	hasShared := false

	for i, raw := range forms {
		form, err := raw.normalizeForUpsert()
		if err != nil {
			result.Failed = append(result.Failed, UpsertFailure{
				Index:        i,
				Username:     raw.Username,
				TargetNodeID: raw.TargetNodeID,
				Reason:       err.Error(),
			})
			continue
		}

		if s.nodeRepo != nil {
			node, err := s.nodeRepo.GetByNodeID(ctx, form.TargetNodeID)
			if err != nil {
				result.Failed = append(result.Failed, UpsertFailure{
					Index:        i,
					Username:     form.Username,
					TargetNodeID: form.TargetNodeID,
					Reason:       fmt.Sprintf("target node %q not found", form.TargetNodeID),
				})
				continue
			}
			if form.Host == "" {
				form.Host = cmp.Or(node.ClientHost, form.TargetNodeID)
			}
		} else if form.Host == "" {
			form.Host = form.TargetNodeID
		}

		existing, found, err := s.connectionRepo.FindFixedMatch(ctx, form.Username, form.TargetNodeID, form.Port, form.Display)
		if err != nil {
			result.Failed = append(result.Failed, UpsertFailure{
				Index:        i,
				Username:     form.Username,
				TargetNodeID: form.TargetNodeID,
				Reason:       fmt.Sprintf("lookup failed: %v", err),
			})
			continue
		}

		if found {
			if updateErr := s.updateFixedFields(ctx, existing.ID, form); updateErr != nil {
				result.Failed = append(result.Failed, UpsertFailure{
					Index:        i,
					Username:     form.Username,
					TargetNodeID: form.TargetNodeID,
					Reason:       fmt.Sprintf("update failed: %v", updateErr),
				})
				continue
			}
			result.Updated++
		} else {
			if _, createErr := s.createFixedFromForm(ctx, form); createErr != nil {
				result.Failed = append(result.Failed, UpsertFailure{
					Index:        i,
					Username:     form.Username,
					TargetNodeID: form.TargetNodeID,
					Reason:       fmt.Sprintf("create failed: %v", createErr),
				})
				continue
			}
			result.Created++
		}

		if form.Shared {
			hasShared = true
		} else if form.Username != "" {
			affectedUsers[form.Username] = struct{}{}
		}
	}

	// Invalidate caches and notify once per affected user after all records processed.
	if hasShared {
		s.invalidateConnListCache("")
		s.notifyOwnerRefresh(ctx, "", 0)
	} else {
		for u := range affectedUsers {
			s.invalidateConnListCache(u)
			s.notifyOwnerRefresh(ctx, u, 0)
		}
	}

	return result, nil
}

// updateFixedFields patches the mutable fields of an existing fixed connection.
// Name is always carried from the form (normalizeForUpsert auto-generates it if blank).
// Status and Source are never touched.
func (s *Service) updateFixedFields(ctx context.Context, id uint, form CreateFixedConnectionForm) error {
	extra := model.ConnectionExtra{
		StartOptions: form.StartOptions,
	}
	if form.OTPEnabled != nil {
		extra.OTPEnabled = form.OTPEnabled
	} else if form.Protocol == contract.VNCProtocol {
		t := true
		extra.OTPEnabled = &t
	}

	extraJSON, err := json.Marshal(extra)
	if err != nil {
		return fmt.Errorf("marshal extra: %w", err)
	}

	fields := map[string]any{
		"name":           form.Name,
		"alias":          form.Alias,
		"host":           form.Host,
		"port":           form.Port,
		"display":        form.Display,
		"target_node_id": form.TargetNodeID,
		"extra":          string(extraJSON),
	}

	return s.connectionRepo.UpdateFields(ctx, id, fields)
}

// createFixedFromForm inserts a new fixed connection from an already-normalised form.
// Shared by both CreateFixed and UpsertFixed to keep creation logic DRY.
func (s *Service) createFixedFromForm(ctx context.Context, form CreateFixedConnectionForm) (model.Connection, error) {
	otpEnabled := form.OTPEnabled
	if otpEnabled == nil && form.Protocol == contract.VNCProtocol {
		t := true
		otpEnabled = &t
	}

	conn := model.Connection{
		Name:         form.Name,
		Alias:        form.Alias,
		Username:     form.Username,
		Shared:       form.Shared,
		TargetNodeID: form.TargetNodeID,
		Host:         form.Host,
		Port:         form.Port,
		Display:      form.Display,
		Status:       model.ConnectionUnknown,
		Source:       model.ConnectionSourceFixed,
		Protocol:     form.Protocol,
		Extra: model.ConnectionExtra{
			StartOptions: form.StartOptions,
			OTPEnabled:   otpEnabled,
		},
	}

	quota, err := s.effectiveMaxConnections(ctx, form.Username)
	if err != nil {
		return model.Connection{}, fmt.Errorf("check connection quota: %w", err)
	}

	if err := s.connectionRepo.CreateWithQuota(ctx, &conn, quota); err != nil {
		return model.Connection{}, fmt.Errorf("create fixed connection: %w", err)
	}
	return conn, nil
}
