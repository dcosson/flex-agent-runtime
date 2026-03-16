package harness

import (
	"os"

	"gopkg.in/yaml.v3"
)

type HostsConfig struct {
	Environments map[string][]Host `yaml:"environments"`
}

type Host struct {
	Name      string `yaml:"name"`
	Address   string `yaml:"address"`
	Cores     int    `yaml:"cores"`
	RAMGB     int    `yaml:"ram_gb"`
	ZFSPoolGB int    `yaml:"zfs_pool_gb"`
}

func LoadHostsConfig(path string) (*HostsConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg HostsConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
