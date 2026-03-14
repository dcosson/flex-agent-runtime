package main

import (
	"encoding/json"
	"flag"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ConfigFile      string
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
}

func LoadConfig() Config {
	var cfg Config
	flag.StringVar(&cfg.ConfigFile, "config", envOrDefault("SANDBOX_HOST_CONFIG", ""), "optional path to JSON config file")
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
	flag.Parse()

	if cfg.ConfigFile != "" {
		_ = applyConfigFile(&cfg, cfg.ConfigFile)
	}
	return cfg
}

type fileConfig struct {
	ListenAddr         *string `json:"listen"`
	PoolName           *string `json:"pool_name"`
	BasesDataset       *string `json:"bases_dataset"`
	SessionsDataset    *string `json:"sessions_dataset"`
	MaxSessions        *int    `json:"max_sessions"`
	DefaultQuota       *int64  `json:"default_quota"`
	ToolTimeout        *string `json:"tool_timeout"`
	RunscPath          *string `json:"runsc_path"`
	RunscRoot          *string `json:"runsc_root"`
	BundleBaseDir      *string `json:"bundle_base_dir"`
	ZFSPath            *string `json:"zfs_path"`
	ZPoolPath          *string `json:"zpool_path"`
	UseSudo            *bool   `json:"sudo"`
	RPCMaxMessageBytes *int    `json:"rpc_max_message_bytes"`
	APIVersion         *string `json:"api_version"`
	MinAPIVersion      *string `json:"min_api_version"`
}

func applyConfigFile(cfg *Config, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var fc fileConfig
	if err := json.Unmarshal(raw, &fc); err != nil {
		return err
	}
	if fc.ListenAddr != nil {
		cfg.ListenAddr = *fc.ListenAddr
	}
	if fc.PoolName != nil {
		cfg.PoolName = *fc.PoolName
	}
	if fc.BasesDataset != nil {
		cfg.BasesDataset = *fc.BasesDataset
	}
	if fc.SessionsDataset != nil {
		cfg.SessionsDataset = *fc.SessionsDataset
	}
	if fc.MaxSessions != nil {
		cfg.MaxSessions = *fc.MaxSessions
	}
	if fc.DefaultQuota != nil {
		cfg.DefaultQuota = *fc.DefaultQuota
	}
	if fc.ToolTimeout != nil {
		if d, err := time.ParseDuration(*fc.ToolTimeout); err == nil {
			cfg.ToolTimeout = d
		}
	}
	if fc.RunscPath != nil {
		cfg.RunscPath = *fc.RunscPath
	}
	if fc.RunscRoot != nil {
		cfg.RunscRoot = *fc.RunscRoot
	}
	if fc.BundleBaseDir != nil {
		cfg.BundleBaseDir = *fc.BundleBaseDir
	}
	if fc.ZFSPath != nil {
		cfg.ZFSPath = *fc.ZFSPath
	}
	if fc.ZPoolPath != nil {
		cfg.ZPoolPath = *fc.ZPoolPath
	}
	if fc.UseSudo != nil {
		cfg.UseSudo = *fc.UseSudo
	}
	if fc.RPCMaxMessageBytes != nil {
		cfg.RPCMaxMessageBytes = *fc.RPCMaxMessageBytes
	}
	if fc.APIVersion != nil {
		cfg.APIVersion = *fc.APIVersion
	}
	if fc.MinAPIVersion != nil {
		cfg.MinAPIVersion = *fc.MinAPIVersion
	}
	return nil
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
