package connection

import (
	"cmp"
	"context"
	"fmt"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

// ResolveSSHDialTarget picks a node for an accessible SSH resource and returns
// the TCP dial target. No connections row is created or reused.
func (s *Service) ResolveSSHDialTarget(ctx context.Context, username string, resourceID uint) (host string, port uint, err error) {
	if username == "" {
		return "", 0, contract.NewInvalidParamError("username is required")
	}
	if resourceID == 0 {
		return "", 0, contract.NewInvalidParamError("resourceId is required")
	}
	if s.connectionResourceRepo == nil {
		return "", 0, fmt.Errorf("connection resource repo uninitialized")
	}
	if s.placer == nil {
		return "", 0, fmt.Errorf("placer uninitialized")
	}

	user, err := s.userSvc.GetDBUser(ctx, username)
	if err != nil {
		return "", 0, fmt.Errorf("get user %s: %w", username, err)
	}
	res, err := s.connectionResourceRepo.GetAccessibleByID(ctx, resourceID, user.ID)
	if err != nil {
		return "", 0, err
	}
	if res.Protocol != contract.SSHProtocol {
		return "", 0, contract.NewValidationError("resource protocol is not SSH")
	}

	node, err := s.placer.SelectNode(ctx, res, nil)
	if err != nil {
		return "", 0, fmt.Errorf("select node for SSH resource %d: %w", resourceID, err)
	}
	host = model.DialHostFor(node.DialHost, node.NodeID)
	if host == "" {
		return "", 0, contract.NewInvalidParamError("proxy target host not defined")
	}
	port = cmp.Or(res.Protocol.DialDefaultPort(), 22)
	if res.Port != nil && *res.Port > 0 {
		port = *res.Port
	}
	if port == 0 || port > 65535 {
		return "", 0, contract.NewValidationError(fmt.Sprintf("proxy target port is invalid: %d", port))
	}
	return host, port, nil
}
