package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/agent"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/transport"
)

type agentConfig struct {
	ListenAddr      string
	MaxSessions     int
	ShutdownTimeout time.Duration

	RPCMaxMessageBytes int
	APIVersion         string
	MinAPIVersion      string
	AuthToken          string
}

func parseAgentConfig(args []string) agentConfig {
	var cfg agentConfig
	fs := flag.NewFlagSet("flexagent serve agent", flag.ExitOnError)

	fs.StringVar(&cfg.ListenAddr, "listen", envOrDefault("FLEXAGENT_LISTEN", ":8081"), "listen address")
	fs.IntVar(&cfg.MaxSessions, "max-sessions", envIntOrDefault("FLEXAGENT_MAX_SESSIONS", 0), "maximum concurrent agent sessions (0 = unlimited)")
	fs.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", envDurationOrDefault("FLEXAGENT_SHUTDOWN_TIMEOUT", 30*time.Second), "graceful shutdown drain period")

	fs.IntVar(&cfg.RPCMaxMessageBytes, "rpc-max-message-bytes", envIntOrDefault("FLEXAGENT_RPC_MAX_MESSAGE_BYTES", 16<<20), "max rpc message size in bytes")
	fs.StringVar(&cfg.APIVersion, "api-version", envOrDefault("FLEXAGENT_API_VERSION", "v1"), "advertised api version")
	fs.StringVar(&cfg.MinAPIVersion, "min-api-version", envOrDefault("FLEXAGENT_MIN_API_VERSION", "v1"), "minimum supported api version")
	fs.StringVar(&cfg.AuthToken, "auth-token", envOrDefault("FLEXAGENT_AUTH_TOKEN", ""), "bearer auth token; empty disables auth")

	_ = fs.Parse(args)
	return cfg
}

func runServeAgent(args []string) {
	cfg := parseAgentConfig(args)
	logger := slog.Default()

	agentService := agent.NewAgentLoopService(
		nil, // publisher — wired externally when needed
		agent.WithMaxSessions(cfg.MaxSessions),
		agent.WithCloseDrainTimeout(cfg.ShutdownTimeout),
		agent.WithToolCatalogFactory(newRuntimeToolCatalogFactory(logger)),
	)

	authHook := makeAuthHook(cfg.AuthToken)

	rpcTransport := transport.NewServer(
		transport.ServerConfig{
			MaxMessageBytes: cfg.RPCMaxMessageBytes,
			APIVersion:      cfg.APIVersion,
			MinAPIVersion:   cfg.MinAPIVersion,
			AuthHook:        authHook,
		},
		transport.WithAgentService(agentService),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/", rpcTransport.Handler())

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("flexagent serve agent starting", "listen", cfg.ListenAddr, "api_version", cfg.APIVersion, "max_sessions", cfg.MaxSessions)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("agent server listen failed", "error", err)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()

	logger.Info("shutdown signal received, draining agent server", "timeout", cfg.ShutdownTimeout)

	// Close the agent service first — this drains active sessions and rejects
	// new CreateSession/ResumeSession calls with CodeUnavailable.
	if err := agentService.Close(); err != nil {
		logger.Error("agent service close error", "error", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "error", err)
	}

	logger.Info("flexagent serve agent shutdown complete")
}

func makeAuthHook(token string) transport.ServerAuthHook {
	if token == "" {
		return nil
	}
	return func(_ context.Context, _ string, headers http.Header) error {
		if got := headers.Get("authorization"); got == "Bearer "+token {
			return nil
		}
		return fmt.Errorf("missing or invalid authorization token")
	}
}
