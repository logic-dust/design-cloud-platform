package model

import (
	"fmt"

	"design-cloud-engine/internal/platform/host"
)

const (
	// MaxNodeIDLen matches Node.NodeID varchar(64).
	MaxNodeIDLen = 64
	// MaxConnectionResourceNameLen matches ConnectionResource.Name varchar(128).
	MaxConnectionResourceNameLen = 128
	// MaxNodeGroupNameLen keeps historical flat group name limit (32 runes).
	MaxNodeGroupNameLen = 32
)

// ValidateNodeID checks cluster node identifiers (hostname / FQDN, optional underscore in labels).
func ValidateNodeID(nodeID string) error {
	if err := host.ValidateHostnameOrFQDN(nodeID, host.ValidateHostnameOptions{
		MaxTotalLen:     MaxNodeIDLen,
		AllowUnderscore: true,
	}); err != nil {
		return fmt.Errorf("invalid node id %q: %w", nodeID, err)
	}
	return nil
}

// ValidateConnectionResourceName checks connection resource display names.
func ValidateConnectionResourceName(name string) error {
	if err := host.ValidateHostnameOrFQDN(name, host.ValidateHostnameOptions{
		MaxTotalLen:     MaxConnectionResourceNameLen,
		AllowUnderscore: true,
	}); err != nil {
		return fmt.Errorf("invalid resource name %q: %w", name, err)
	}
	return nil
}

// ValidateNodeGroupName checks flat node group tokens (single lowercase hostname label).
func ValidateNodeGroupName(name string) error {
	if err := host.ValidateHostnameOrFQDN(name, host.ValidateHostnameOptions{
		MaxTotalLen:   MaxNodeGroupNameLen,
		SingleLabel:   true,
		LowercaseOnly: true,
	}); err != nil {
		return fmt.Errorf("invalid group name %q: %w", name, err)
	}
	return nil
}
