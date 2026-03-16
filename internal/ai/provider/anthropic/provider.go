package anthropic

import (
	"net/http"
	"strings"
	"time"

	"flex-agent-runtime/internal/ai"
)

const (
	apiName        = "anthropic-messages"
	defaultBaseURL = "https://api.anthropic.com"
	defaultVersion = "2023-06-01"
	defaultTimeout = 60 * time.Second
)

// Config controls Anthropic provider construction.
type Config struct {
	HTTPClient  *http.Client
	BaseURL     string
	APIKey      string
	Version     string
	BetaHeaders []string
}

// Provider implements ai.Provider for Anthropic Messages API.
type Provider struct {
	client      *http.Client
	baseURL     string
	apiKey      string
	version     string
	betaHeaders []string
}

// New constructs an Anthropic provider with sane defaults.
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
		client:      client,
		baseURL:     strings.TrimRight(baseURL, "/"),
		apiKey:      cfg.APIKey,
		version:     version,
		betaHeaders: append([]string(nil), cfg.BetaHeaders...),
	}
}

// API returns the ai.Provider API key.
func (p *Provider) API() string {
	return apiName
}

// Register constructs and registers the Anthropic provider in ai registry.
func Register(cfg Config, sourceID string) *Provider {
	p := New(cfg)
	ai.RegisterProvider(p, sourceID)
	return p
}
