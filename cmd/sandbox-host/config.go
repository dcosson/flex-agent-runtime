package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr      string
	PoolName        string
	BasesDataset    string
	SessionsDataset string
	MaxSessions     int
	DefaultQuota    int64
	ToolTimeout     time.Duration

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
	flag.StringVar(&cfg.PoolName, "pool", envOrDefault("SANDBOX_HOST_POOL", ""), "zfs pool name")
	flag.StringVar(&cfg.BasesDataset, "bases-dataset", envOrDefault("SANDBOX_HOST_BASES_DATASET", ""), "base snapshots dataset")
	flag.StringVar(&cfg.SessionsDataset, "sessions-dataset", envOrDefault("SANDBOX_HOST_SESSIONS_DATASET", ""), "sessions dataset")
	flag.IntVar(&cfg.MaxSessions, "max-sessions", envIntOrDefault("SANDBOX_HOST_MAX_SESSIONS", 0), "maximum live sessions (0 = unlimited)")
	flag.Int64Var(&cfg.DefaultQuota, "default-quota", envInt64OrDefault("SANDBOX_HOST_DEFAULT_QUOTA", 0), "default per-session quota bytes (0 = unlimited)")
	flag.DurationVar(&cfg.ToolTimeout, "tool-timeout", envDurationOrDefault("SANDBOX_HOST_TOOL_TIMEOUT", 5*time.Minute), "tool execution timeout")

	flag.StringVar(&cfg.RunscPath, "runsc-path", envOrDefault("SANDBOX_HOST_RUNSC_PATH", "runsc"), "runsc binary path")
	flag.StringVar(&cfg.RunscRoot, "runsc-root", envOrDefault("SANDBOX_HOST_RUNSC_ROOT", ""), "runsc root directory")
	flag.StringVar(&cfg.BundleBaseDir, "bundle-base-dir", envOrDefault("SANDBOX_HOST_BUNDLE_BASE_DIR", ""), "gvisor bundle base directory")

	flag.StringVar(&cfg.ZFSPath, "zfs-path", envOrDefault("SANDBOX_HOST_ZFS_PATH", "zfs"), "zfs binary path")
	flag.StringVar(&cfg.ZPoolPath, "zpool-path", envOrDefault("SANDBOX_HOST_ZPOOL_PATH", "zpool"), "zpool binary path")
	flag.BoolVar(&cfg.UseSudo, "sudo", envBoolOrDefault("SANDBOX_HOST_SUDO", false), "use sudo for zfs/zpool commands")

	flag.IntVar(&cfg.RPCMaxMessageBytes, "rpc-max-message-bytes", envIntOrDefault("SANDBOX_HOST_RPC_MAX_MESSAGE_BYTES", 16<<20), "max rpc message size in bytes")
	flag.StringVar(&cfg.APIVersion, "api-version", envOrDefault("SANDBOX_HOST_API_VERSION", "v1"), "advertised api version")
	flag.StringVar(&cfg.MinAPIVersion, "min-api-version", envOrDefault("SANDBOX_HOST_MIN_API_VERSION", "v1"), "minimum supported api version")
	flag.StringVar(&cfg.AuthToken, "auth-token", envOrDefault("SANDBOX_HOST_AUTH_TOKEN", ""), "bearer auth token; empty disables auth")
	flag.BoolVar(&cfg.EnableTerminal, "enable-terminal", envBoolOrDefault("SANDBOX_HOST_ENABLE_TERMINAL", false), "enable terminal streaming service")
	flag.Parse()

	return cfg
}

func (cfg Config) Validate() error {
	var errs []error
	if strings.TrimSpace(cfg.PoolName) == "" {
		errs = append(errs, fmt.Errorf("pool is required"))
	}
	if strings.TrimSpace(cfg.BasesDataset) == "" {
		errs = append(errs, fmt.Errorf("bases-dataset is required"))
	}
	if strings.TrimSpace(cfg.SessionsDataset) == "" {
		errs = append(errs, fmt.Errorf("sessions-dataset is required"))
	}
	if strings.TrimSpace(cfg.BundleBaseDir) == "" {
		errs = append(errs, fmt.Errorf("bundle-base-dir is required"))
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
