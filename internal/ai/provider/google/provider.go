package google

import (
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

// Config holds test-friendly configuration for constructing a ProviderEndpoint.
type Config struct {
	HTTPClient *http.Client
	BaseURL    string
	APIKey     string
	Version    string // API version, default "v1beta"
}

// EndpointFromConfig creates a ProviderEndpoint from Config for testing.
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
