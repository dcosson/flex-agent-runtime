package google

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

const (
	apiName        = "google-genai"
	defaultBaseURL = "https://generativelanguage.googleapis.com"
	defaultVersion = "v1beta"
	defaultTimeout = 60 * time.Second
)

// ClientConfig controls Google client construction.
type ClientConfig struct {
	HTTPClient *http.Client
}

// Client implements ai.APIClient for the Google Gemini REST API.
// It is stateless — base URL, API key, and version come from
// ProviderEndpoint per-call.
type Client struct {
	httpClient *http.Client
}

// NewClient constructs a Google Gemini protocol client.
func NewClient(cfg ClientConfig) *Client {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{httpClient: client}
}

// ClientType returns the API client type identifier.
func (c *Client) ClientType() string {
	return apiName
}

// Register creates a Client and registers it as an API client.
func Register(cfg ClientConfig) *Client {
	c := NewClient(cfg)
	ai.RegisterAPIClient(c)
	return c
}

// Config is the legacy configuration struct. Retained for backward
// compatibility during migration. New code should use ClientConfig.
type Config struct {
	HTTPClient *http.Client
	BaseURL    string
	APIKey     string
	Version    string // API version, default "v1beta"
}

// Provider wraps Client with stored endpoint config for legacy callers that
// use the old ai.Provider interface. Will be removed when callers migrate.
type Provider struct {
	*Client
	endpoint ai.ProviderEndpoint
}

// API returns the ai.Provider API identifier.
func (p *Provider) API() string { return apiName }

// Stream implements ai.Provider for legacy callers.
func (p *Provider) Stream(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.StreamOptions) *ai.EventStream {
	return p.Client.Stream(ctx, p.endpoint, model, llmCtx, opts)
}

// StreamSimple implements ai.Provider for legacy callers.
func (p *Provider) StreamSimple(ctx context.Context, model ai.Model, llmCtx ai.Context, opts ai.SimpleStreamOptions) *ai.EventStream {
	return p.Client.StreamSimple(ctx, p.endpoint, model, llmCtx, opts)
}

// New constructs a legacy Provider with stored endpoint config.
// Retained for backward compatibility during migration.
func New(cfg Config) *Provider {
	client := NewClient(ClientConfig{HTTPClient: cfg.HTTPClient})
	ep := EndpointFromConfig(cfg)
	return &Provider{Client: client, endpoint: ep}
}

// EndpointFromConfig creates a ProviderEndpoint from legacy Config for testing.
func EndpointFromConfig(cfg Config) ai.ProviderEndpoint {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	version := strings.TrimSpace(cfg.Version)
	if version == "" {
		version = defaultVersion
	}

	return ai.ProviderEndpoint{
		ProviderName:     "google",
		BaseURL:          baseURL,
		APIKey:           cfg.APIKey,
		ProviderSpecific: map[string]string{"apiVersion": version},
	}
}
