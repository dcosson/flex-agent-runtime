package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/rpc"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

const (
	healthFailureThreshold = 3
	healthProbeTimeout     = 3 * time.Second
)

func (o *Orchestrator) runHealthLoop(ctx context.Context) {
	ticker := time.NewTicker(o.config.HealthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.checkAllSessions(ctx)
		}
	}
}

func (o *Orchestrator) checkAllSessions(ctx context.Context) {
	for _, entry := range o.listSessionEntries() {
		o.checkSessionHealth(ctx, entry)
	}
}

func (o *Orchestrator) checkSessionHealth(ctx context.Context, entry *sessionEntry) {
	entry.mu.Lock()
	state := entry.state
	if state == sessionCreating || state == sessionPaused || state == sessionDestroyed {
		entry.mu.Unlock()
		return
	}
	placement := entry.placement
	agentAddress := entry.agentAddress
	sandboxHostAddr := entry.sandboxHostAddr
	sandboxControl := entry.sandboxControl
	sandboxID := entry.sandboxID
	processID := entry.processID
	failuresBefore := entry.healthFailures
	entry.mu.Unlock()

	checkErr := o.checkPlacementHealth(ctx, placement, agentAddress, sandboxHostAddr)
	if checkErr != nil && (placement == PlacementAgentDirect || placement == PlacementAgentSandbox) &&
		sandboxControl != nil && sandboxID != "" && processID != "" && failuresBefore+1 >= healthFailureThreshold {
		checkErr = errors.Join(checkErr, o.checkProcessHealth(ctx, sandboxControl, sandboxID, processID))
	}

	now := time.Now()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	// Session state may have changed while health checks were running.
	if entry.state == sessionCreating || entry.state == sessionPaused || entry.state == sessionDestroyed {
		return
	}

	if checkErr != nil {
		entry.healthFailures++
		entry.healthErr = checkErr
		if entry.state != sessionUnhealthy {
			o.logger.WarnContext(ctx, "session became unhealthy",
				"session_id", entry.sessionID,
				"placement", entry.placement,
				"error", checkErr)
		}
		entry.state = sessionUnhealthy
		return
	}

	wasUnhealthy := entry.state == sessionUnhealthy
	entry.healthFailures = 0
	entry.healthErr = nil
	entry.lastHealth = now
	entry.state = sessionActive
	if wasUnhealthy {
		o.logger.InfoContext(ctx, "session recovered",
			"session_id", entry.sessionID,
			"placement", entry.placement)
	}
}

func (o *Orchestrator) checkPlacementHealth(ctx context.Context, placement PlacementMode, agentAddress, sandboxHostAddr string) error {
	switch placement {
	case PlacementAgentDirect, PlacementAgentSandbox:
		return o.checkHTTPHealth(ctx, agentAddress)
	case PlacementToolsSandbox:
		return o.checkHTTPHealth(ctx, sandboxHostAddr)
	default:
		return nil
	}
}

func (o *Orchestrator) checkProcessHealth(ctx context.Context, sc control.SandboxControl, sandboxID, processID string) error {
	checkCtx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
	statusResp, err := sc.GetProcessStatus(checkCtx, control.GetProcessStatusRequest{
		SandboxID: sandboxID,
		ProcessID: processID,
	})
	if err != nil {
		return fmt.Errorf("process status check failed: %w", err)
	}
	if statusResp == nil {
		return errors.New("process status check returned nil response")
	}
	if statusResp.Status != control.ProcessExited {
		return nil
	}
	if statusResp.ExitCode != nil {
		return fmt.Errorf("agent process exited with code %d", *statusResp.ExitCode)
	}
	return errors.New("agent process exited")
}

func (o *Orchestrator) checkHTTPHealth(ctx context.Context, addr string) error {
	healthURL, err := normalizeAddressToHealthURL(addr)
	if err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	resp, err := o.healthClient.Do(req)
	if err != nil {
		return fmt.Errorf("health request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint %s returned status %d", healthURL, resp.StatusCode)
	}
	return nil
}

func normalizeAddressToHealthURL(rawAddr string) (string, error) {
	addr := strings.TrimSpace(rawAddr)
	if addr == "" {
		return "", errors.New("empty health address")
	}
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	parsed, err := url.Parse(addr)
	if err != nil {
		return "", fmt.Errorf("invalid health address %q: %w", rawAddr, err)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid health address %q: missing host", rawAddr)
	}
	parsed.Path = path.Join(parsed.EscapedPath(), "/health")
	if parsed.Path == "" {
		parsed.Path = "/health"
	}
	return parsed.String(), nil
}

func sessionUnavailableError(sessionID string, healthErr error) error {
	message := fmt.Sprintf("session %q is unhealthy", sessionID)
	if healthErr != nil {
		message = fmt.Sprintf("%s: %v", message, healthErr)
	}
	return rpc.NewRPCError(rpc.CodeUnavailable, message, nil)
}
