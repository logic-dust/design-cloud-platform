package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"runtime"
	"strconv"
	"time"

	"design-cloud-engine/internal/config"
	pb "design-cloud-engine/internal/contract/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
)

const (
	// Keepalive configuration (matches server settings)
	clientKeepaliveTime    = 30 * time.Second // Send ping after 30s of no activity
	clientKeepaliveTimeout = 5 * time.Second  // Wait 5s for ping ack

	// Reconnection delays
	minReconnectDelay = 2 * time.Second  // Minimum delay before reconnection
	maxReconnectDelay = 60 * time.Second // Maximum delay before reconnection
)

// connectLoop implements Strict Ordered Failover connection strategy
func (a *Agent) connectLoop() {
	preferredAddr := ""
	currentDelay := minReconnectDelay

	for {
		select {
		case <-a.stopCh:
			return
		default:
		}

		addresses := a.serverAddresses()
		if preferredAddr != "" {
			addresses = prependUniqueAddress(addresses, preferredAddr)
			preferredAddr = ""
		}
		if len(addresses) == 0 {
			a.logger.Warn("No cluster servers configured in Nodes")
		}
		redirected := false
		skipDelay := false
		for _, addr := range addresses {
			select {
			case <-a.stopCh:
				return
			default:
			}

			a.logger.Debug("Attempting to connect to server", "address", addr)

			connStartTime := time.Now()
			err := a.connectToServer(addr)
			// Only reset the backoff after a connection was successfully established
			// and lived long enough to be considered stable.
			if err == nil && time.Since(connStartTime) > 30*time.Second {
				currentDelay = minReconnectDelay
			}

			if err != nil {
				// A successful renewal deliberately ends the session: reconnect
				// immediately with the freshly installed certificate.
				if errors.Is(err, errCertRenewed) {
					a.logger.Info("Reconnecting with renewed certificate")
					skipDelay = true
					break
				}

				// Count post-renewal failures; after the threshold the agent rolls
				// back to the .prev certificate (D12).
				a.noteRenewalFailure()

				// An explicit rejection means the certificate itself is unusable (not a
				// network problem): count it so the agent can roll back to .prev even
				// long after the renewal switch window has closed.
				if errors.Is(err, errRegistrationRejected) {
					a.noteAuthRejection()
				}

				if redirect, ok := errors.AsType[*redirectError](err); ok {
					if redirect.leaderAddr != "" {
						a.logger.Info("Redirecting to leader", "leader_id", redirect.leaderID, "leader_addr", redirect.leaderAddr, "reason", redirect.reason)
						preferredAddr = redirect.leaderAddr
						redirected = true
						break
					}
				}

				// Server requested shutdown: connectToServer already applied a random
				// reconnect delay (Thundering Herd protection). Break out of the server
				// list and skip the additional ReconnectDelay to reconnect promptly.
				if errors.Is(err, ErrServerShutdown) {
					skipDelay = true
					break
				}

				a.logger.Warn("Connection failed, trying next server", "error", err, "address", addr)
				continue
			}

			// Connection was successful but has now ended: disarm any pending
			// renewal rollback and restart from Master.
			a.noteRenewalRecovered()
			a.logger.Info("Disconnected from server", "address", addr)
			break
		}
		if redirected {
			continue
		}
		if skipDelay {
			continue
		}

		// All servers failed or connection ended, wait before retry
		// Exponential backoff with jitter
		jitter := time.Duration(rand.Int64N(int64(currentDelay) / 2))
		retryTimer := time.NewTimer(currentDelay + jitter)

		select {
		case <-a.stopCh:
			retryTimer.Stop()
			return
		case <-retryTimer.C:
			a.logger.Debug("Reconnection delay elapsed, retrying from Master", "delay", currentDelay+jitter)
		}

		currentDelay *= 2
		currentDelay = min(currentDelay, maxReconnectDelay)
	}
}

// connectToServer establishes a connection to a specific server.
func (a *Agent) connectToServer(addr string) error {
	// Build dial options (pass addr for TLS ServerName)
	opts, err := a.buildDialOptions(addr)
	if err != nil {
		return fmt.Errorf("failed to build gRPC dial options for %s: %w", addr, err)
	}

	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	a.streamMutex.Lock()
	a.currentConn = conn
	a.streamMutex.Unlock()

	defer func() {
		a.streamMutex.Lock()
		if a.currentConn == conn {
			a.currentConn = nil
			a.currentStream = nil
		}
		a.streamMutex.Unlock()
		if err := conn.Close(); err != nil {
			a.logger.Debug("failed to close grpc connection", "address", addr, "error", err)
		}
	}()

	// Create gRPC client
	client := pb.NewControlServiceClient(conn)

	// Establish bidirectional stream
	// specific timeout is for handshake, but stream itself should be long-lived
	// stream context must be cancellable, not timed out
	streamCtx, streamCancel := context.WithCancel(a.ctx)
	defer streamCancel()

	stream, err := client.Sync(streamCtx)
	if err != nil {
		return fmt.Errorf("failed to create stream: %w", err)
	}

	a.streamMutex.Lock()
	a.currentStream = stream
	a.streamMutex.Unlock()

	// Send registration request
	if err := a.sendRegister(stream); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	// Wait for registration response
	resp, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("failed to receive register response: %w", err)
	}

	if ack := resp.GetRegisterResponse(); ack != nil {
		if !ack.Accepted {
			leaderAddr := leaderRedirectAddr(a.cfg.ClusterPort, ack.GetLeaderNodeId(), ack.GetLeaderAddr())
			if leaderAddr != "" || ack.GetLeaderNodeId() != "" {
				return &redirectError{
					leaderID:   ack.GetLeaderNodeId(),
					leaderAddr: leaderAddr,
					reason:     ack.Message,
				}
			}
			return fmt.Errorf("%w: %s", errRegistrationRejected, ack.Message)
		}
		a.logger.Info("Successfully registered with server", "address", addr, "message", ack.Message)
		a.noteAuthAccepted()
		if ack.AgentSettings != nil && a.settingsMgr != nil {
			a.settingsMgr.Update(ack.AgentSettings)
		}
	}

	a.replayPendingResults(stream)

	reportCfg := TelemetryConfig{
		HeartbeatInterval: a.cfg.HeartbeatInterval,
	}

	reporting := startTelemetry(streamCtx, a, reportCfg, a.logger)
	defer reporting.Stop()

	// Message handling loop (blocks until disconnection)
	err = a.commandStreamLoop(stream)

	// If server requested shutdown, apply random delay before returning
	// This prevents Thundering Herd
	// NOTE: 使用 math/rand/v2 而非 crypto/rand，因为此处不涉及安全性
	// 延迟范围 1-9 秒足以分散重连请求
	if err != nil && errors.Is(err, ErrServerShutdown) {
		delay := minReconnectDelay + time.Duration(rand.IntN(int(maxReconnectDelay-minReconnectDelay)))
		a.logger.Info("Server shutting down, delaying reconnection", "delay", delay)
		select {
		case <-time.After(delay):
		case <-a.stopCh:
			// Stop() called during Thundering Herd delay: abort the wait early
			return err
		}
	}

	return err
}

// buildDialOptions constructs gRPC dial options.
// addr is used to extract the server nodeID for TLS certificate verification.
func (a *Agent) buildDialOptions(addr string) ([]grpc.DialOption, error) {
	var opts []grpc.DialOption

	// Keepalive configuration
	kaParams := keepalive.ClientParameters{
		Time:                clientKeepaliveTime,
		Timeout:             clientKeepaliveTimeout,
		PermitWithoutStream: true,
	}
	opts = append(opts, grpc.WithKeepaliveParams(kaParams))

	creds, err := a.buildTLSCredentials(addr)
	if err != nil {
		return nil, fmt.Errorf("build TLS credentials: %w", err)
	}
	opts = append(opts, grpc.WithTransportCredentials(creds))

	if a.cfg.ConnectTimeout > 0 {
		opts = append(opts, grpc.WithConnectParams(grpc.ConnectParams{
			MinConnectTimeout: a.cfg.ConnectTimeout,
		}))
	}

	if a.cfg.DialContext != nil {
		opts = append(opts, grpc.WithContextDialer(a.cfg.DialContext))
	}

	return opts, nil
}

// buildTLSCredentials creates TLS credentials for the client.
// addr is used to derive the TLS server name when not explicitly configured.
func (a *Agent) buildTLSCredentials(addr string) (credentials.TransportCredentials, error) {
	serverName := a.cfg.TLS.ServerName
	if serverName == "" && addr != "" {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		serverName = host
	}
	if serverName == "" {
		return nil, fmt.Errorf("TLS server name is required")
	}

	cert, err := tls.LoadX509KeyPair(a.cfg.TLS.CertPath, a.cfg.TLS.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load key pair: %w", err)
	}

	pool := x509.NewCertPool()
	ca, err := os.ReadFile(a.cfg.TLS.CAPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate from %s: %w", a.cfg.TLS.CAPath, err)
	}
	if ok := pool.AppendCertsFromPEM(ca); !ok {
		return nil, fmt.Errorf("failed to append CA certificate")
	}

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

func prependUniqueAddress(addresses []string, preferred string) []string {
	if preferred == "" {
		return addresses
	}
	filtered := make([]string, 0, len(addresses)+1)
	filtered = append(filtered, preferred)
	for _, addr := range addresses {
		if addr != preferred {
			filtered = append(filtered, addr)
		}
	}
	return filtered
}

func leaderRedirectAddr(clusterPort int, leaderID, leaderAddr string) string {
	if leaderAddr != "" {
		return leaderAddr
	}
	if leaderID != "" {
		return net.JoinHostPort(leaderID, strconv.Itoa(clusterPort))
	}
	return ""
}

// sendRegister sends the registration request to the server
func (a *Agent) sendRegister(stream pb.ControlService_SyncClient) error {
	return a.sendMessage(stream, &pb.AgentMessage{
		Payload: &pb.AgentMessage_RegisterRequest{
			RegisterRequest: &pb.RegisterRequest{
				NodeId:       a.nodeIdentity,
				Version:      config.VERSION,
				RegisteredAt: time.Now().Unix(),
				SystemInfo: &pb.SystemInfo{
					Os:       runtime.GOOS,
					Arch:     runtime.GOARCH,
					CpuCores: int64(runtime.NumCPU()),
				},
			},
		},
	})
}

// sendMessage safely sends a message to the server stream with proper locking
func (a *Agent) sendMessage(stream pb.ControlService_SyncClient, msg *pb.AgentMessage) error {
	a.sendMutex.Lock()
	defer a.sendMutex.Unlock()
	return stream.Send(msg)
}

type redirectError struct {
	leaderID   string
	leaderAddr string
	reason     string
}

func (e *redirectError) Error() string {
	if e.reason != "" {
		return e.reason
	}
	if e.leaderAddr != "" {
		return fmt.Sprintf("redirect to leader %s", e.leaderAddr)
	}
	if e.leaderID != "" {
		return fmt.Sprintf("redirect to leader %s", e.leaderID)
	}
	return "redirect to leader"
}

// sendOnActiveStream acquires the current stream and sends a message on it.
func (a *Agent) sendOnActiveStream(msg *pb.AgentMessage) error {
	a.streamMutex.Lock()
	stream := a.currentStream
	a.streamMutex.Unlock()
	if stream == nil {
		return fmt.Errorf("no active stream")
	}
	return a.sendMessage(stream, msg)
}
