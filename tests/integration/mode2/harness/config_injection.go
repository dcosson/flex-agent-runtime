package harness

import (
	"os"
	"path/filepath"
	"testing"
)

// ConfigInjector handles credential and config directory setup for Mode 2 E2E tests.
// It prepares the managed config directory with driver-specific config and credentials
// before the driver process launches.
type ConfigInjector struct {
	ConfigDir string
	t         *testing.T
}

// Credential represents a credential file to inject.
type Credential struct {
	RelPath string // path relative to config dir
	Content string // file content
	Mode    os.FileMode
}

// NewConfigInjector creates a ConfigInjector for the given config directory.
func NewConfigInjector(t *testing.T, configDir string) *ConfigInjector {
	t.Helper()
	return &ConfigInjector{ConfigDir: configDir, t: t}
}

// InjectCredentials writes credential files into the config directory.
func (ci *ConfigInjector) InjectCredentials(creds ...Credential) {
	ci.t.Helper()
	for _, cred := range creds {
		abs := filepath.Join(ci.ConfigDir, cred.RelPath)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			ci.t.Fatalf("mkdir for credential %s: %v", cred.RelPath, err)
		}
		mode := cred.Mode
		if mode == 0 {
			mode = 0o600
		}
		if err := os.WriteFile(abs, []byte(cred.Content), mode); err != nil {
			ci.t.Fatalf("write credential %s: %v", cred.RelPath, err)
		}
	}
}

// InjectAPIKey writes a typical API key credential file.
func (ci *ConfigInjector) InjectAPIKey(filename, key string) {
	ci.InjectCredentials(Credential{
		RelPath: filename,
		Content: key,
		Mode:    0o600,
	})
}

// InjectDriverConfig writes driver-specific configuration.
func (ci *ConfigInjector) InjectDriverConfig(filename, config string) {
	ci.InjectCredentials(Credential{
		RelPath: filename,
		Content: config,
		Mode:    0o644,
	})
}

// VerifyCredentialExists checks that a credential file exists at the expected path.
func (ci *ConfigInjector) VerifyCredentialExists(relPath string) bool {
	_, err := os.Stat(filepath.Join(ci.ConfigDir, relPath))
	return err == nil
}

// ReadCredential reads a credential file's content.
func (ci *ConfigInjector) ReadCredential(relPath string) string {
	ci.t.Helper()
	data, err := os.ReadFile(filepath.Join(ci.ConfigDir, relPath))
	if err != nil {
		ci.t.Fatalf("read credential %s: %v", relPath, err)
	}
	return string(data)
}

// BuildEnvVars returns environment variables for the driver process
// that point to the managed config directory.
func (ci *ConfigInjector) BuildEnvVars() map[string]string {
	return map[string]string{
		"CONFIG_DIR":        ci.ConfigDir,
		"CLAUDE_CONFIG_DIR": ci.ConfigDir,
		"XDG_CONFIG_HOME":   ci.ConfigDir,
	}
}
