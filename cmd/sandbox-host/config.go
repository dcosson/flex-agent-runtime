package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"h2-agent-runtime/internal/sandbox"
)

type Config struct {
	ListenAddr       string
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

func LoadConfig() Config {
	var cfg Config
	flag.StringVar(&cfg.ListenAddr, "listen", envOrDefault("SANDBOX_HOST_LISTEN", ":8080"), "listen address")
	flag.StringVar((*string)(&cfg.StorageBackend), "storage-backend", envOrDefault("SANDBOX_STORAGE_BACKEND", string(sandbox.StorageBackendZFS)), "storage backend: zfs|local-disk")
	flag.StringVar((*string)(&cfg.ContainerRuntime), "container-runtime", envOrDefault("SANDBOX_CONTAINER_RUNTIME", string(sandbox.ContainerRuntimeGVisor)), "container runtime: gvisor|none")
	flag.StringVar(&cfg.PoolName, "pool", envOrDefault("SANDBOX_POOL_NAME", ""), "zfs pool name")
	flag.StringVar(&cfg.BasesDataset, "bases-dataset", envOrDefault("SANDBOX_BASES_DATASET", ""), "base snapshots dataset")
	flag.StringVar(&cfg.SessionsDataset, "sessions-dataset", envOrDefault("SANDBOX_SESSIONS_DATASET", ""), "sessions dataset")
	flag.StringVar(&cfg.SessionsRootDir, "sessions-root-dir", envOrDefault("SANDBOX_SESSIONS_ROOT_DIR", ""), "root directory for local-disk sessions")
	flag.IntVar(&cfg.MaxSessions, "max-sessions", envIntOrDefault("SANDBOX_MAX_SESSIONS", 0), "maximum live sessions (0 = unlimited)")
	flag.Int64Var(&cfg.DefaultQuota, "default-quota", envInt64OrDefault("SANDBOX_DEFAULT_QUOTA", 0), "default per-session quota bytes (0 = unlimited)")
	flag.DurationVar(&cfg.ToolTimeout, "tool-timeout", envDurationOrDefault("SANDBOX_TOOL_TIMEOUT", 5*time.Minute), "tool execution timeout")

	flag.StringVar(&cfg.RunscPath, "runsc-path", envOrDefault("SANDBOX_RUNSC_PATH", "runsc"), "runsc binary path")
	flag.StringVar(&cfg.RunscRoot, "runsc-root", envOrDefault("SANDBOX_RUNSC_ROOT", ""), "runsc root directory")
	flag.StringVar(&cfg.BundleBaseDir, "bundle-base-dir", envOrDefault("SANDBOX_BUNDLE_BASE_DIR", ""), "gvisor bundle base directory")

	flag.StringVar(&cfg.ZFSPath, "zfs-path", envOrDefault("SANDBOX_ZFS_PATH", "zfs"), "zfs binary path")
	flag.StringVar(&cfg.ZPoolPath, "zpool-path", envOrDefault("SANDBOX_ZPOOL_PATH", "zpool"), "zpool binary path")
	flag.BoolVar(&cfg.UseSudo, "sudo", envBoolOrDefault("SANDBOX_SUDO", false), "use sudo for zfs/zpool commands")

	flag.IntVar(&cfg.RPCMaxMessageBytes, "rpc-max-message-bytes", envIntOrDefault("SANDBOX_RPC_MAX_MESSAGE_BYTES", 16<<20), "max rpc message size in bytes")
	flag.StringVar(&cfg.APIVersion, "api-version", envOrDefault("SANDBOX_API_VERSION", "v1"), "advertised api version")
	flag.StringVar(&cfg.MinAPIVersion, "min-api-version", envOrDefault("SANDBOX_MIN_API_VERSION", "v1"), "minimum supported api version")
	flag.StringVar(&cfg.AuthToken, "auth-token", envOrDefault("SANDBOX_AUTH_TOKEN", ""), "bearer auth token; empty disables auth")
	flag.BoolVar(&cfg.EnableTerminal, "enable-terminal", envBoolOrDefault("SANDBOX_ENABLE_TERMINAL", false), "enable terminal streaming service")
	flag.Parse()

	return cfg
}

func (cfg Config) Validate() error {
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

func envOrDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envInt64OrDefault(name string, def int64) int64 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func envDurationOrDefault(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func envBoolOrDefault(name string, def bool) bool {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
