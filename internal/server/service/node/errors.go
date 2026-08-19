package node

import "errors"

// Sentinel errors for the node service — callers map these to HTTP status codes.
var (
	ErrNodeNotFound      = errors.New("node not found")
	ErrInvalidNodeID     = errors.New("invalid node ID")
	ErrInvalidNodeStatus = errors.New("invalid node status")
	ErrInvalidClientHost = errors.New("invalid client host")
	ErrInvalidNodeCert   = errors.New("invalid node certificate")

	ErrInvalidNodeToken       = errors.New("invalid node token")
	ErrExpiredNodeToken       = errors.New("expired node token")
	ErrMissingNodeToken       = errors.New("missing node token")
	ErrNodeAlreadyRegistered  = errors.New("node already registered")
	ErrInvalidNodeCertOptions = errors.New("invalid node certificate options")
	ErrCAExists               = errors.New("cluster CA already exists")
	ErrNodeCertAlreadyExists  = errors.New("node already has an active certificate binding (use --force to overwrite)")
)
