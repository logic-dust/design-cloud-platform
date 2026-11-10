package controlplane

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	pb "design-cloud-engine/internal/contract/proto"
	"design-cloud-engine/internal/model"
	agentregistry "design-cloud-engine/internal/server/controlplane/registry"
)

// ==================== Certificate lifecycle (inspection + renewal) ====================

// CertPolicy is the certificate lifecycle policy consumed by the renewal engine.
// It mirrors the settings `cert` domain but stays a plain struct so the control
// package does not depend on the settings schema.
type CertPolicy struct {
	AutoRenewMode   string // apply | observe | off
	RenewBeforeDays int
	MaxAttempts     int
	BackoffHours    []int
}

// DefaultCertPolicy mirrors the schema defaults and is used when no policy
// provider is wired (e.g. minimal test setups).
var DefaultCertPolicy = CertPolicy{
	AutoRenewMode:   "apply",
	RenewBeforeDays: 30,
	MaxAttempts:     5,
	BackoffHours:    []int{1, 6, 24},
}

// CertRenewalHooks groups the server-side capabilities the certificate renewal
// engine depends on. The assembly layer provides the adapter, keeping this
// package decoupled from config internals and the node service.
type CertRenewalHooks interface {
	// Policy returns the current certificate lifecycle policy.
	Policy(ctx context.Context) (CertPolicy, error)
	// SignCSR validates and signs an agent CSR with server-decided SANs.
	// validDays <= 0 selects the agent default.
	SignCSR(csrPEM []byte, commonName string, hosts []string, validDays int) (certPEM []byte, fingerprint, subject string, expiresAt time.Time, err error)
	// CAPEM returns the cluster CA certificate in PEM form.
	CAPEM() ([]byte, error)
	// CAKeyPEM returns the cluster CA private key in PEM form (used for the
	// file-asset key pairing check).
	CAKeyPEM() ([]byte, error)
	// UpdateCert persists fingerprint (rotating prev), subject and expiry.
	UpdateCert(ctx context.Context, nodeID, fingerprint, subject string, expiresAt time.Time) error
	// LogRenew records an audit entry for a renewal phase (requested/succeeded/failed/...).
	LogRenew(ctx context.Context, nodeID, phase, message string)
	// RenewControlNodeCert re-signs this control node's own certificate in place.
	RenewControlNodeCert(ctx context.Context) error
}

const (
	certInspectionWindow = 90 * 24 * time.Hour
	certRenewRoundLimit  = 20
	certRenewInFlightTTL = 30 * time.Minute
	certExpiredStage     = 1
)

// certRenewState tracks attempt bookkeeping for a node (leader-local, in-memory:
// a leadership change or restart simply resets the counters, which is safe because
// the agent side tolerates repeated renewal triggers).
type certRenewState struct {
	attempts        int
	lastAttempt     time.Time
	inFlight        bool
	inFlightSince   time.Time
	exhaustedLogged bool
}

// checkSelfCert is the "self section" of the certificate tick: every control node
// checks its own row regardless of leadership, because only the host itself can
// re-sign its own certificate (private key + paths are node-local). The leader
// lease gates only the Agent-row sweep below.
func (s *Server) checkSelfCert(ctx context.Context) {
	if s.deps.CertHooks == nil {
		return
	}
	node, err := s.nodes.GetByNodeID(ctx, s.nodeID)
	if err != nil {
		return
	}
	if !node.IsServer || !node.CertAutoRenew || node.CertExpiresAt.IsZero() {
		return
	}
	policy := s.certPolicy(ctx)
	if policy.AutoRenewMode == "off" {
		return
	}
	remaining := time.Until(node.CertExpiresAt)
	if remaining > time.Duration(policy.RenewBeforeDays)*24*time.Hour {
		return
	}
	if policy.AutoRenewMode == "observe" {
		s.logger.Info("cert.observe: control node certificate would be renewed",
			"node_id", s.nodeID, "expires_at", node.CertExpiresAt.Format(time.RFC3339))
		return
	}
	if err := s.deps.CertHooks.RenewControlNodeCert(ctx); err != nil {
		s.logger.Warn("Failed to renew control node certificate", "node_id", s.nodeID, "error", err)
		s.deps.CertHooks.LogRenew(ctx, s.nodeID, "failed", err.Error())
		return
	}
	s.logger.Info("Control node certificate renewed; hot reload applies on next handshake", "node_id", s.nodeID)
	s.deps.CertHooks.LogRenew(ctx, s.nodeID, "succeeded", "control node self-renewal")
}

// dropRevokedSessions closes the live stream of any node whose certificate was revoked
// (fingerprint cleared). Without it a revoked node keeps its established session until it
// happens to reconnect, so revocation would lag indefinitely; dropping the session makes the
// agent reconnect immediately and fail validateAgentIdentity (fail-closed, no self-healing).
//
// Unlike the leader-gated agent sweep, this runs on every control node: it only touches the
// sessions that node actually holds.
func (s *Server) dropRevokedSessions(ctx context.Context) {
	if s.registry == nil {
		return
	}
	for _, nodeID := range s.registry.ConnectedNodeIDs() {
		node, err := s.nodes.GetByNodeID(ctx, nodeID)
		if err != nil || node.CertFingerprint != "" {
			continue
		}
		if s.registry.Disconnect(nodeID) {
			s.logger.Info("Dropped live session of a node without a registered certificate",
				"node_id", nodeID)
		}
	}
}

// runCertInspection is the leader section of the certificate lifecycle: DB-first
// expiry alerts for every registered node plus auto-renewal triggers for agents.
func (s *Server) runCertInspection(ctx context.Context) {
	if s.deps.CertHooks == nil {
		return
	}
	policy := s.certPolicy(ctx)

	expiring, err := s.nodes.ListCertExpiring(ctx, time.Now().Add(certInspectionWindow))
	if err != nil {
		s.logger.Warn("Failed to list nodes with expiring certificates", "error", err)
		return
	}
	triggered := 0
	for i := range expiring {
		node := expiring[i]
		s.checkCertExpiry(ctx, node)
		if triggered < certRenewRoundLimit && s.maybeTriggerRenewal(ctx, node, policy) {
			triggered++
		}
	}
	if triggered > 0 {
		s.logger.Info("Certificate renewal round complete", "requested", triggered)
	}
	s.checkCACertExpiry()
}

func (s *Server) certPolicy(ctx context.Context) CertPolicy {
	if s.deps.CertHooks == nil {
		return DefaultCertPolicy
	}
	policy, err := s.deps.CertHooks.Policy(ctx)
	if err != nil || policy.AutoRenewMode == "" {
		return DefaultCertPolicy
	}
	if policy.RenewBeforeDays <= 0 {
		policy.RenewBeforeDays = DefaultCertPolicy.RenewBeforeDays
	}
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = DefaultCertPolicy.MaxAttempts
	}
	if len(policy.BackoffHours) == 0 {
		policy.BackoffHours = DefaultCertPolicy.BackoffHours
	}
	return policy
}

// certAlertStage maps remaining lifetime to an alert stage (0 = no alert).
// Severity increases as the number decreases: 1 > 7 > 30 > 90.
func certAlertStage(remaining time.Duration) int16 {
	switch {
	case remaining <= 0:
		return certExpiredStage
	case remaining < 7*24*time.Hour:
		return 7
	case remaining < 30*24*time.Hour:
		return 30
	case remaining < certInspectionWindow:
		return 90
	default:
		return 0
	}
}

// checkCertExpiry emits a stage-stepped alert (at most once per stage per node,
// tracked in nodes.cert_alert_stage) and pushes an event for the severe stages.
func (s *Server) checkCertExpiry(ctx context.Context, node model.Node) {
	if node.CertExpiresAt.IsZero() {
		return
	}
	remaining := time.Until(node.CertExpiresAt)
	stage := certAlertStage(remaining)
	if stage == 0 {
		return
	}
	// Stage numbers decrease with severity; alert only when crossing into a more
	// severe stage than the one already recorded.
	if node.CertAlertStage != 0 && stage >= node.CertAlertStage {
		return
	}

	expiresAt := node.CertExpiresAt.Format(time.RFC3339)
	switch {
	case remaining <= 0:
		s.logger.Error("Node certificate has expired", "node_id", node.NodeID, "expires_at", expiresAt)
		s.pushCertAlert(ctx, node.NodeID, "节点证书已过期", "error")
	case stage == 7:
		s.logger.Error("Node certificate expires within 7 days", "node_id", node.NodeID, "expires_at", expiresAt)
		s.pushCertAlert(ctx, node.NodeID, "节点证书将在 7 天内过期", "error")
	case stage == 30:
		s.logger.Warn("Node certificate expires within 30 days", "node_id", node.NodeID, "expires_at", expiresAt)
	case stage == 90:
		s.logger.Warn("Node certificate expires within 90 days", "node_id", node.NodeID, "expires_at", expiresAt)
	}

	if err := s.nodes.UpdateCertAlertStage(ctx, node.NodeID, stage); err != nil {
		s.logger.Warn("Failed to persist certificate alert stage", "node_id", node.NodeID, "error", err)
	}
}

func (s *Server) pushCertAlert(ctx context.Context, nodeID, message, level string) {
	if s.deps.Publisher == nil {
		return
	}
	s.deps.Publisher.PublishNodeEvent(ctx, int(model.RoleSystemAdmin), nodeID, "notify", "证书到期告警", message, level)
}

// maybeTriggerRenewal sends a renewal trigger to an online agent whose certificate
// is close to expiry. Returns true when a trigger was actually sent.
func (s *Server) maybeTriggerRenewal(ctx context.Context, node model.Node, policy CertPolicy) bool {
	if !node.CertAutoRenew || node.Status != model.NodeStatusOnline || node.CertExpiresAt.IsZero() {
		return false
	}
	remaining := time.Until(node.CertExpiresAt)
	if remaining <= 0 || remaining > time.Duration(policy.RenewBeforeDays)*24*time.Hour {
		// An expired certificate cannot authenticate a renewal session: manual path.
		return false
	}
	if policy.AutoRenewMode == "observe" {
		s.logger.Info("cert.observe: agent certificate would be renewed",
			"node_id", node.NodeID, "expires_at", node.CertExpiresAt.Format(time.RFC3339))
		return false
	}
	if policy.AutoRenewMode != "apply" {
		return false
	}

	now := time.Now()
	s.renewMu.Lock()
	st := s.renewStates[node.NodeID]
	if st == nil {
		st = &certRenewState{}
		s.renewStates[node.NodeID] = st
	}
	if st.inFlight {
		if now.Sub(st.inFlightSince) < certRenewInFlightTTL {
			s.renewMu.Unlock()
			return false
		}
		// Timed out: count it as a failed attempt and allow a retry.
		st.inFlight = false
		st.attempts++
	}
	if st.attempts >= policy.MaxAttempts {
		exhausted := !st.exhaustedLogged
		st.exhaustedLogged = true
		s.renewMu.Unlock()
		if exhausted {
			s.logger.Warn("Certificate renewal exceeded attempt budget, manual handling required",
				"node_id", node.NodeID, "attempts", policy.MaxAttempts)
			s.deps.CertHooks.LogRenew(ctx, node.NodeID, "exhausted", "manual handling required")
		}
		return false
	}
	if st.attempts > 0 {
		idx := min(st.attempts-1, len(policy.BackoffHours)-1)
		wait := time.Duration(policy.BackoffHours[idx]) * time.Hour
		if now.Sub(st.lastAttempt) < wait {
			s.renewMu.Unlock()
			return false
		}
	}
	st.lastAttempt = now
	st.inFlight = true
	st.inFlightSince = now
	s.renewMu.Unlock()

	cmd := &pb.ServerCommand{
		Payload: &pb.ServerCommand_RenewCertCommand{
			RenewCertCommand: &pb.RenewCertCommand{Reason: "expiring"},
		},
	}
	if err := s.registry.SendCommand(node.NodeID, cmd); err != nil {
		s.logger.Warn("Failed to send certificate renewal trigger", "node_id", node.NodeID, "error", err)
		s.noteRenewFailure(node.NodeID)
		return false
	}
	s.logger.Info("Requested certificate renewal", "node_id", node.NodeID,
		"expires_at", node.CertExpiresAt.Format(time.RFC3339))
	s.deps.CertHooks.LogRenew(ctx, node.NodeID, "requested", "certificate nearing expiry")
	return true
}

// handleRenewCertRequest signs an agent-uploaded CSR and returns the certificate.
// The DB fingerprint is rotated BEFORE the response is handed out, otherwise the
// agent's reconnect with the new certificate would be rejected.
func (s *Server) handleRenewCertRequest(conn *agentregistry.Connection, nodeID string, msg *pb.RenewCertRequest) {
	if s.deps.CertHooks == nil || msg == nil || len(msg.GetCsr()) == 0 {
		s.noteRenewFailure(nodeID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), nodeStatusUpdateTimeout)
	defer cancel()

	// CSR mode: the private key never reaches the server; SANs are decided here
	// (agent certificates are client identities, CN is authoritative).
	certPEM, fingerprint, subject, expiresAt, err := s.deps.CertHooks.SignCSR(msg.GetCsr(), nodeID, []string{nodeID}, 0)
	if err != nil {
		s.logger.Warn("Certificate renewal CSR rejected", "node_id", nodeID, "error", err)
		s.noteRenewFailure(nodeID)
		s.deps.CertHooks.LogRenew(ctx, nodeID, "failed", "CSR rejected: "+err.Error())
		return
	}
	if err := s.deps.CertHooks.UpdateCert(ctx, nodeID, fingerprint, subject, expiresAt); err != nil {
		s.logger.Warn("Failed to persist renewed certificate metadata", "node_id", nodeID, "error", err)
		s.noteRenewFailure(nodeID)
		s.deps.CertHooks.LogRenew(ctx, nodeID, "failed", "persist metadata: "+err.Error())
		return
	}
	caPEM, err := s.deps.CertHooks.CAPEM()
	if err != nil {
		s.logger.Warn("Failed to read cluster CA for renewal response", "node_id", nodeID, "error", err)
		s.noteRenewFailure(nodeID)
		return
	}
	if err := conn.Send(&pb.ServerCommand{
		Payload: &pb.ServerCommand_RenewCertResponse{
			RenewCertResponse: &pb.RenewCertResponse{Cert: certPEM, Ca: caPEM},
		},
	}); err != nil {
		s.logger.Warn("Failed to deliver renewed certificate", "node_id", nodeID, "error", err)
		s.noteRenewFailure(nodeID)
		return
	}
	s.logger.Info("Renewed certificate issued", "node_id", nodeID, "expires_at", expiresAt.Format(time.RFC3339))
	s.deps.CertHooks.LogRenew(ctx, nodeID, "issued", "certificate signed and delivered")
}

// handleRenewCertResult processes the agent's local installation outcome.
func (s *Server) handleRenewCertResult(nodeID string, res *pb.RenewCertResult) {
	if s.deps.CertHooks == nil || res == nil {
		return
	}
	if res.GetOk() {
		s.resetRenewState(nodeID)
		s.logger.Info("Certificate renewal installed on agent", "node_id", nodeID)
		s.deps.CertHooks.LogRenew(context.Background(), nodeID, "succeeded", res.GetMessage())
		return
	}
	s.noteRenewFailure(nodeID)
	s.logger.Warn("Certificate renewal failed on agent", "node_id", nodeID, "message", res.GetMessage())
	s.deps.CertHooks.LogRenew(context.Background(), nodeID, "failed", res.GetMessage())
}

func (s *Server) noteRenewFailure(nodeID string) {
	s.renewMu.Lock()
	defer s.renewMu.Unlock()
	st := s.renewStates[nodeID]
	if st == nil {
		st = &certRenewState{}
		s.renewStates[nodeID] = st
	}
	st.inFlight = false
	st.attempts++
	st.lastAttempt = time.Now()
}

func (s *Server) resetRenewState(nodeID string) {
	s.renewMu.Lock()
	defer s.renewMu.Unlock()
	delete(s.renewStates, nodeID)
}

// checkCACertExpiry warns (at most once per 24h per process) when the cluster CA
// expires within a year, covering the §3.9.6 rotation lead time. It also performs
// the file-asset pairing check (crt/key must belong together): a mismatched pair
// starts the process fine but silently breaks every rotation and revocation.
func (s *Server) checkCACertExpiry() {
	caPEM, err := s.deps.CertHooks.CAPEM()
	if err != nil {
		return
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		return
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return
	}

	if keyPEM, err := s.deps.CertHooks.CAKeyPEM(); err == nil {
		if err := verifyKeyPairing(cert, keyPEM); err != nil {
			s.logger.Error("Cluster CA certificate/key pairing check failed", "error", err)
		}
	}

	remaining := time.Until(cert.NotAfter)
	if remaining > 365*24*time.Hour {
		return
	}
	now := time.Now().Unix()
	last := s.caWarnedAt.Load()
	if last != 0 && now-last < int64(24*time.Hour/time.Second) {
		return
	}
	s.caWarnedAt.Store(now)
	s.logger.Warn("Cluster CA certificate expires within a year; plan cross-signing rotation",
		"expires_at", cert.NotAfter.Format(time.RFC3339), "remaining_days", int(remaining.Hours()/24))
}

// verifyKeyPairing checks that the private key actually belongs to the certificate.
func verifyKeyPairing(cert *x509.Certificate, keyPEM []byte) error {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return fmt.Errorf("private key is not PEM encoded")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}
	certPub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("certificate public key is not ECDSA")
	}
	if !certPub.Equal(&key.PublicKey) {
		return fmt.Errorf("certificate and private key do not match (pairing error)")
	}
	return nil
}
