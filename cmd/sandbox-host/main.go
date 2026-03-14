package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	rpcserver "h2-agent-runtime/internal/rpc/server"
	"h2-agent-runtime/internal/rpc/transport"
	"h2-agent-runtime/internal/sandbox"
	"h2-agent-runtime/internal/sandbox/gvisor"
	"h2-agent-runtime/internal/sandbox/zfs"
	"h2-agent-runtime/internal/termmux"
)

func main() {
	cfg := LoadConfig()
	logger := slog.Default()
	if err := cfg.Validate(); err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	zm, err := zfs.NewCLIManager(
		zfs.WithZFSPath(cfg.ZFSPath),
		zfs.WithZPoolPath(cfg.ZPoolPath),
		zfs.WithSudo(cfg.UseSudo),
		zfs.WithPool(cfg.PoolName),
		zfs.WithLogger(logger),
	)
	if err != nil {
		logger.Error("failed to initialize zfs manager", "error", err)
		os.Exit(1)
	}
	gmCfg := gvisor.ManagerConfig{
		RunscPath:     cfg.RunscPath,
		RunscRoot:     cfg.RunscRoot,
		BundleBaseDir: cfg.BundleBaseDir,
		Logger:        logger,
	}
	gm, err := gvisor.NewManager(gmCfg)
	if err != nil {
		logger.Error("failed to initialize gvisor manager", "error", err)
		os.Exit(1)
	}

	svcCfg := sandbox.DefaultServiceConfig()
	if cfg.PoolName != "" {
		svcCfg.PoolName = cfg.PoolName
	}
	if cfg.BasesDataset != "" {
		svcCfg.BasesDataset = cfg.BasesDataset
	}
	if cfg.SessionsDataset != "" {
		svcCfg.SessionsDataset = cfg.SessionsDataset
	}
	if cfg.MaxSessions > 0 {
		svcCfg.MaxSessions = cfg.MaxSessions
	}
	if cfg.DefaultQuota > 0 {
		svcCfg.DefaultSessionQuota = cfg.DefaultQuota
	}
	if cfg.ToolTimeout > 0 {
		svcCfg.ToolTimeout = cfg.ToolTimeout
	}

	host := sandbox.NewSandboxHostService(svcCfg, zm, gm, logger)
	sandboxRPC := rpcserver.NewSandboxServer(host)
	defer sandboxRPC.Close()
	eventRPC := rpcserver.NewAgentEventServer()

	authToken := cfg.AuthToken
	authHook := func(_ context.Context, _ string, headers http.Header) error {
		if authToken == "" {
			return nil
		}
		if got := headers.Get("authorization"); got == "Bearer "+authToken {
			return nil
		}
		return fmt.Errorf("missing or invalid authorization token")
	}

	var termSessions *termmux.SessionManager
	if cfg.EnableTerminal {
		termSessions = termmux.NewSessionManager()
	}

	rpcTransport := transport.NewServer(sandboxRPC, eventRPC, termSessions, transport.ServerConfig{
		MaxMessageBytes: cfg.RPCMaxMessageBytes,
		APIVersion:      cfg.APIVersion,
		MinAPIVersion:   cfg.MinAPIVersion,
		AuthHook:        authHook,
	})

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           rpcTransport.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("sandbox-host starting", "listen", cfg.ListenAddr, "api_version", cfg.APIVersion, "terminal_enabled", cfg.EnableTerminal)
	go func() {
		if serveErr := httpServer.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("sandbox-host listen failed", "error", serveErr)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()

	logger.Info("shutdown signal received, draining sandbox-host")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "error", err)
	}
	if err := host.Shutdown(shutdownCtx); err != nil {
		logger.Error("sandbox service shutdown error", "error", err)
	}
	logger.Info("sandbox-host shutdown complete")
}
