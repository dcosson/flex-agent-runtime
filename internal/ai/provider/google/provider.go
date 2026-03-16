package google

import (
	"net/http"
	"strings"
	"time"

	"flex-agent-runtime/internal/ai"
)

const (
	apiName        = "google-genai"
	defaultBaseURL = "https://generativelanguage.googleapis.com"
	defaultVersion = "v1beta"
	defaultTimeout = 60 * time.Second
)

// Config controls Google provider construction.
type Config struct {
	HTTPClient *http.Client
	BaseURL    string
	APIKey     string
	Version    string // API version, default "v1beta"
}

// Provider implements ai.Provider for Google Gemini REST API.
type Provider struct {
	client  *http.Client
	baseURL string
	apiKey  string
	version string
}

// New constructs a Google Gemini provider with sane defaults.
func New(cfg Config) *Provider {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	version := strings.TrimSpace(cfg.Version)
	if version == "" {
		version = defaultVersion
	}
	return &Provider{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  cfg.APIKey,
		version: version,
	}
}

// API returns the provider API identifier.
func (p *Provider) API() string {
	return apiName
}

// Register constructs and registers the Google provider in ai registry.
func Register(cfg Config, sourceID string) *Provider {
	p := New(cfg)
	ai.RegisterProvider(p, sourceID)
	return p
}
