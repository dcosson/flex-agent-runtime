package openai

import (
	"net/http"
	"strings"
	"time"

	"h2-agent-runtime/internal/ai"
)

const (
	apiName        = "openai-completions"
	defaultBaseURL = "https://api.openai.com/v1"
	defaultTimeout = 60 * time.Second
)

// Config controls OpenAI provider construction.
type Config struct {
	HTTPClient *http.Client
	BaseURL    string
	APIKey     string
}

// Provider implements ai.Provider for OpenAI Chat Completions API.
type Provider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// New constructs an OpenAI provider with sane defaults.
func New(cfg Config) *Provider {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Provider{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  cfg.APIKey,
	}
}

// API returns the provider API identifier.
func (p *Provider) API() string {
	return apiName
}

// Register constructs and registers the OpenAI provider in ai registry.
func Register(cfg Config, sourceID string) *Provider {
	p := New(cfg)
	ai.RegisterProvider(p, sourceID)
	return p
}
