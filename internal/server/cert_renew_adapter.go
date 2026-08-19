package server

import (
	"context"
	"fmt"
	"os"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/server/control"
	"design-cloud-engine/internal/server/service"
	nodeService "design-cloud-engine/internal/server/service/node"
)

// certRenewalAdapter implements control.CertRenewalHooks, bridging the control
// plane's certificate lifecycle engine to the settings policy, the node service
// (CSR signing + metadata persistence + audit) and this server's own cert paths.
type certRenewalAdapter struct {
	cfg      *config.Config
	services *service.Container
	self     *Server
}

func newCertRenewalAdapter(cfg *config.Config, services *service.Container, self *Server) *certRenewalAdapter {
	return &certRenewalAdapter{cfg: cfg, services: services, self: self}
}

func (a *certRenewalAdapter) Policy(ctx context.Context) (control.CertPolicy, error) {
	settings, err := a.services.Settings.GetCertSettings(ctx)
	if err != nil {
		return control.CertPolicy{}, err
	}
	return control.CertPolicy{
		AutoRenewMode:   settings.AutoRenewMode,
		RenewBeforeDays: settings.RenewBeforeDays,
		MaxAttempts:     settings.MaxAttempts,
		BackoffHours:    settings.BackoffScheduleHours,
	}, nil
}

func (a *certRenewalAdapter) SignCSR(csrPEM []byte, commonName string, hosts []string, validDays int) ([]byte, string, string, time.Time, error) {
	certPEM, result, err := nodeService.SignNodeCSR(nodeService.CSRSignOptions{
		CSRPEM:     csrPEM,
		CommonName: commonName,
		Hosts:      hosts,
		ValidDays:  validDays,
		CACertFile: a.cfg.ClusterCACertPath(),
		CAKeyFile:  a.cfg.ClusterCAKeyPath(),
	})
	if err != nil {
		return nil, "", "", time.Time{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339, result.ExpiresAt)
	if err != nil {
		return nil, "", "", time.Time{}, fmt.Errorf("parse certificate expiry: %w", err)
	}
	return certPEM, result.Fingerprint, result.Subject, expiresAt, nil
}

func (a *certRenewalAdapter) CAPEM() ([]byte, error) {
	return os.ReadFile(a.cfg.ClusterCACertPath())
}

func (a *certRenewalAdapter) CAKeyPEM() ([]byte, error) {
	return os.ReadFile(a.cfg.ClusterCAKeyPath())
}

func (a *certRenewalAdapter) UpdateCert(ctx context.Context, nodeID, fingerprint, subject string, expiresAt time.Time) error {
	_, err := a.services.Node.UpdateCert(ctx, nodeID, fingerprint, subject, expiresAt)
	return err
}

func (a *certRenewalAdapter) LogRenew(ctx context.Context, nodeID, phase, message string) {
	a.services.Node.LogCertRenew(ctx, nodeID, phase, message)
}

func (a *certRenewalAdapter) RenewControlNodeCert(ctx context.Context) error {
	return a.self.renewOwnControlCert(ctx)
}
