package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/agent"
	rpcserver "github.com/dcosson/flex-agent-runtime/internal/rpc/server"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/transport"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/gvisor"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/zfs"
	"github.com/dcosson/flex-agent-runtime/internal/termmux"
)

type serveAllConfig struct {
	ListenAddr      string
	ShutdownTimeout time.Duration

	// Agent settings
	AgentMaxSessions int

	// Sandbox-host settings
	StorageBackend   sandbox.StorageBackend
	ContainerRuntime sandbox.ContainerRuntime
	PoolName         string
	BasesDataset     string
	SessionsDataset  string
	SessionsRootDir  string
	MaxSessions      int
	DefaultQuota     int64
	ToolTimeout      time.Duration

	RunscPath     string
	RunscRoot     string
	BundleBaseDir string

	ZFSPath   string
	ZPoolPath string
	UseSudo   bool

	RPCMaxMessageBytes int
	APIVersion         string
	MinAPIVersion      string
	AuthToken          string
	EnableTerminal     bool
}

func parseServeAllConfig(args []string) serveAllConfig {
	var cfg serveAllConfig
	fs := flag.NewFlagSet("flexagent serve all", flag.ExitOnError)

	fs.StringVar(&cfg.ListenAddr, "listen", envOrDefault("FLEXAGENT_LISTEN", ":8080"), "listen address")
	fs.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", envDurationOrDefault("FLEXAGENT_SHUTDOWN_TIMEOUT", 30*time.Second), "graceful shutdown drain period")

	// Agent
	fs.IntVar(&cfg.AgentMaxSessions, "agent-max-sessions", envIntOrDefault("FLEXAGENT_MAX_SESSIONS", 0), "max concurrent agent sessions (0 = unlimited)")

	// Sandbox-host
	fs.StringVar((*string)(&cfg.StorageBackend), "storage-backend", envOrDefault("SANDBOX_STORAGE_BACKEND", string(sandbox.StorageBackendZFS)), "storage backend: zfs|local-disk")
	fs.StringVar((*string)(&cfg.ContainerRuntime), "container-runtime", envOrDefault("SANDBOX_CONTAINER_RUNTIME", string(sandbox.ContainerRuntimeGVisor)), "container runtime: gvisor|none")
	fs.StringVar(&cfg.PoolName, "pool", envOrDefault("SANDBOX_POOL_NAME", ""), "zfs pool name")
	fs.StringVar(&cfg.BasesDataset, "bases-dataset", envOrDefault("SANDBOX_BASES_DATASET", ""), "base snapshots dataset")
	fs.StringVar(&cfg.SessionsDataset, "sessions-dataset", envOrDefault("SANDBOX_SESSIONS_DATASET", ""), "sessions dataset")
	fs.StringVar(&cfg.SessionsRootDir, "sessions-root-dir", envOrDefault("SANDBOX_SESSIONS_ROOT_DIR", ""), "root directory for local-disk sessions")
	fs.IntVar(&cfg.MaxSessions, "max-sessions", envIntOrDefault("SANDBOX_MAX_SESSIONS", 0), "max live sandbox sessions (0 = unlimited)")
	fs.Int64Var(&cfg.DefaultQuota, "default-quota", envInt64OrDefault("SANDBOX_DEFAULT_QUOTA", 0), "default per-session quota bytes (0 = unlimited)")
	fs.DurationVar(&cfg.ToolTimeout, "tool-timeout", envDurationOrDefault("SANDBOX_TOOL_TIMEOUT", 5*time.Minute), "tool execution timeout")

	fs.StringVar(&cfg.RunscPath, "runsc-path", envOrDefault("SANDBOX_RUNSC_PATH", "runsc"), "runsc binary path")
	fs.StringVar(&cfg.RunscRoot, "runsc-root", envOrDefault("SANDBOX_RUNSC_ROOT", ""), "runsc root directory")
	fs.StringVar(&cfg.BundleBaseDir, "bundle-base-dir", envOrDefault("SANDBOX_BUNDLE_BASE_DIR", ""), "gvisor bundle base directory")

	fs.StringVar(&cfg.ZFSPath, "zfs-path", envOrDefault("SANDBOX_ZFS_PATH", "zfs"), "zfs binary path")
	fs.StringVar(&cfg.ZPoolPath, "zpool-path", envOrDefault("SANDBOX_ZPOOL_PATH", "zpool"), "zpool binary path")
	fs.BoolVar(&cfg.UseSudo, "sudo", envBoolOrDefault("SANDBOX_SUDO", false), "use sudo for zfs/zpool commands")

	fs.IntVar(&cfg.RPCMaxMessageBytes, "rpc-max-message-bytes", envIntOrDefault("FLEXAGENT_RPC_MAX_MESSAGE_BYTES", 16<<20), "max rpc message size in bytes")
	fs.StringVar(&cfg.APIVersion, "api-version", envOrDefault("FLEXAGENT_API_VERSION", "v1"), "advertised api version")
	fs.StringVar(&cfg.MinAPIVersion, "min-api-version", envOrDefault("FLEXAGENT_MIN_API_VERSION", "v1"), "minimum supported api version")
	fs.StringVar(&cfg.AuthToken, "auth-token", envOrDefault("FLEXAGENT_AUTH_TOKEN", ""), "bearer auth token; empty disables auth")
	fs.BoolVar(&cfg.EnableTerminal, "enable-terminal", envBoolOrDefault("SANDBOX_ENABLE_TERMINAL", false), "enable terminal streaming service")

	_ = fs.Parse(args)
	return cfg
}

func (cfg serveAllConfig) validate() error {
	var errs []error
	switch cfg.StorageBackend {
	case sandbox.StorageBackendZFS:
		if strings.TrimSpace(cfg.PoolName) == "" {
			errs = append(errs, fmt.Errorf("pool is required for zfs backend"))
		}
		if strings.TrimSpace(cfg.BasesDataset) == "" {
			errs = append(errs, fmt.Errorf("bases-dataset is required for zfs backend"))
		}
		if strings.TrimSpace(cfg.SessionsDataset) == "" {
			errs = append(errs, fmt.Errorf("sessions-dataset is required for zfs backend"))
		}
	case sandbox.StorageBackendLocalDisk:
		if strings.TrimSpace(cfg.SessionsRootDir) == "" {
			errs = append(errs, fmt.Errorf("sessions-root-dir is required for local-disk backend"))
		}
	default:
		errs = append(errs, fmt.Errorf("invalid storage-backend %q", cfg.StorageBackend))
	}
	switch cfg.ContainerRuntime {
	case sandbox.ContainerRuntimeGVisor:
		if strings.TrimSpace(cfg.BundleBaseDir) == "" {
			errs = append(errs, fmt.Errorf("bundle-base-dir is required for gvisor runtime"))
		}
	case sandbox.ContainerRuntimeNone:
	default:
		errs = append(errs, fmt.Errorf("invalid container-runtime %q", cfg.ContainerRuntime))
	}
	if cfg.RPCMaxMessageBytes <= 0 {
		errs = append(errs, fmt.Errorf("rpc-max-message-bytes must be > 0"))
	}
	if strings.TrimSpace(cfg.APIVersion) == "" {
		errs = append(errs, fmt.Errorf("api-version is required"))
	}
	if strings.TrimSpace(cfg.MinAPIVersion) == "" {
		errs = append(errs, fmt.Errorf("min-api-version is required"))
	}
	return errors.Join(errs...)
}

func runServeAll(args []string) {
	cfg := parseServeAllConfig(args)
	logger := slog.Default()
	if err := cfg.validate(); err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	// --- Agent service ---
	agentService := agent.NewAgentLoopService(
		nil,
		agent.WithMaxSessions(cfg.AgentMaxSessions),
		agent.WithCloseDrainTimeout(cfg.ShutdownTimeout),
		agent.WithToolCatalogFactory(newRuntimeToolCatalogFactory(logger)),
	)

	// --- Sandbox-host service ---
	var err error
	var zm zfs.ZFSManager
	if cfg.StorageBackend == sandbox.StorageBackendZFS {
		zm, err = zfs.NewCLIManager(
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
	}

	var gm gvisor.GVisorManager
	if cfg.ContainerRuntime == sandbox.ContainerRuntimeGVisor {
		gmCfg := gvisor.ManagerConfig{
			RunscPath:     cfg.RunscPath,
			RunscRoot:     cfg.RunscRoot,
			BundleBaseDir: cfg.BundleBaseDir,
			Logger:        logger,
		}
		gm, err = gvisor.NewManager(gmCfg)
		if err != nil {
			logger.Error("failed to initialize gvisor manager", "error", err)
			os.Exit(1)
		}
	}

	svcCfg := sandbox.DefaultServiceConfig()
	svcCfg.StorageBackend = cfg.StorageBackend
	svcCfg.ContainerRuntime = cfg.ContainerRuntime
	if cfg.PoolName != "" {
		svcCfg.PoolName = cfg.PoolName
	}
	if cfg.BasesDataset != "" {
		svcCfg.BasesDataset = cfg.BasesDataset
	}
	if cfg.SessionsDataset != "" {
		svcCfg.SessionsDataset = cfg.SessionsDataset
	}
	if cfg.SessionsRootDir != "" {
		svcCfg.SessionsRootDir = cfg.SessionsRootDir
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

	host, err := sandbox.NewSandboxHostService(svcCfg, zm, gm, logger)
	if err != nil {
		logger.Error("failed to initialize sandbox host service", "error", err)
		os.Exit(1)
	}
	sandboxRPC := rpcserver.NewSandboxServer(host)
	defer sandboxRPC.Close()
	eventRPC := rpcserver.NewAgentEventServer()

	authHook := makeAuthHook(cfg.AuthToken)

	var termSessions *termmux.SessionManager
	if cfg.EnableTerminal {
		termSessions = termmux.NewSessionManager()
	}

	// --- Single transport server with all services ---
	rpcTransport := transport.NewServer(
		transport.ServerConfig{
			MaxMessageBytes: cfg.RPCMaxMessageBytes,
			APIVersion:      cfg.APIVersion,
			MinAPIVersion:   cfg.MinAPIVersion,
			AuthHook:        authHook,
		},
		transport.WithAgentService(agentService),
		transport.WithSandboxService(sandboxRPC),
		transport.WithAgentEventService(eventRPC),
		transport.WithSessionManager(termSessions),
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

	logger.Info("flexagent serve all starting",
		"listen", cfg.ListenAddr,
		"api_version", cfg.APIVersion,
		"agent_max_sessions", cfg.AgentMaxSessions,
		"terminal_enabled", cfg.EnableTerminal,
	)
	go func() {
		if serveErr := httpServer.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("serve all listen failed", "error", serveErr)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()

	logger.Info("shutdown signal received, draining all services", "timeout", cfg.ShutdownTimeout)

	if err := agentService.Close(); err != nil {
		logger.Error("agent service close error", "error", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "error", err)
	}
	if err := host.Shutdown(shutdownCtx); err != nil {
		logger.Error("sandbox service shutdown error", "error", err)
	}
	logger.Info("flexagent serve all shutdown complete")
}
