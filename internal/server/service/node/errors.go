package node

import "errors"

// Sentinel errors for the node service — callers map these to HTTP status codes.
var (
	ErrNodeNotFound      = errors.New("node not found")
	ErrInvalidNodeID     = errors.New("invalid node ID")
	ErrInvalidNodeStatus = errors.New("invalid node status")
	ErrInvalidDialHost   = errors.New("invalid dial host")
	ErrInvalidNodeCert   = errors.New("invalid node certificate")

	ErrInvalidNodeToken            = errors.New("invalid node token")
	ErrExpiredNodeToken            = errors.New("expired node token")
	ErrMissingNodeToken            = errors.New("missing node token")
	ErrEnrollmentIssueAfterConsume = errors.New("enrollment token consumed but certificate issue failed; generate a new token")
	ErrNodeAlreadyRegistered       = errors.New("node already registered")
	ErrInvalidNodeCertOptions      = errors.New("invalid node certificate options")
	ErrCAExists                    = errors.New("cluster CA already exists")
	ErrNodeCertAlreadyExists       = errors.New("node already has an active certificate binding (use --force to overwrite)")
)

// MissingNodeIDsError lists node IDs referenced by a selector that do not exist.
type MissingNodeIDsError struct {
	MissingNodeIDs []string
}

func (e MissingNodeIDsError) Error() string {
	return "one or more nodes do not exist"
}
