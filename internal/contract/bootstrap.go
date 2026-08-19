package contract

import "time"

// BootstrapMaterials is the OTP bootstrap response payload for a node.
type BootstrapMaterials struct {
	NodeID        string    `json:"nodeId"`
	ServerAddr    string    `json:"serverAddr"`
	CertPEM       string    `json:"certPem"`
	KeyPEM        string    `json:"keyPem"`
	CAPEM         string    `json:"caPem"`
	CertExpiresAt time.Time `json:"certExpiresAt"`
}
