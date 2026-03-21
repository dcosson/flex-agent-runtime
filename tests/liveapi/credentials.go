//go:build liveapi

package liveapi

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// loadCredentials loads credentials from ~/.flexagent/test-credentials.env,
// falling back to environment variables. Returns a map of key→value.
func loadCredentials() map[string]string {
	creds := make(map[string]string)

	// Load from secrets file first.
	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".flexagent", "test-credentials.env")
		if entries, err := parseEnvFile(path); err == nil {
			for k, v := range entries {
				creds[k] = v
			}
		}
	}

	// Environment variables override file-based credentials.
	envKeys := []string{
		"ANTHROPIC_API_KEY",
		"OPENAI_API_KEY",
		"GOOGLE_API_KEY",
		"OPENROUTER_API_KEY",
		"COHERE_API_KEY",
	}
	for _, k := range envKeys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			creds[k] = v
		}
	}

	return creds
}

// setCredentialEnvVars loads credentials and sets them as environment variables
// so the ai/ library can pick them up.
func setCredentialEnvVars() {
	for k, v := range loadCredentials() {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		result[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return result, scanner.Err()
}
