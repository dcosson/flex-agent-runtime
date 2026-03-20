package openai

import (
	"net/http"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

const (
	apiName        = "openai-completions"
	defaultTimeout = 60 * time.Second
)

// ClientConfig controls OpenAI client construction.
type ClientConfig struct {
	HTTPClient *http.Client
}

// Client implements ai.APIClient for the OpenAI Chat Completions protocol.
// It is stateless — base URL and API key come from ProviderEndpoint per-call.
type Client struct {
	httpClient *http.Client
}

// NewClient constructs an OpenAI protocol client.
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
}

// EndpointFromConfig creates a ProviderEndpoint from Config for testing.
func EndpointFromConfig(cfg Config) ai.ProviderEndpoint {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return ai.ProviderEndpoint{
		ProviderName: "openai",
		BaseURL:      baseURL,
		APIKey:       cfg.APIKey,
	}
}
