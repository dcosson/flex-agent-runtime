package anthropic

import (
	"net/http"
	"strings"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

const (
	apiName        = "anthropic-messages"
	defaultBaseURL = "https://api.anthropic.com"
	defaultVersion = "2023-06-01"
	defaultTimeout = 60 * time.Second
)

// ClientConfig controls Anthropic client construction.
type ClientConfig struct {
	HTTPClient *http.Client
}

// Client implements ai.APIClient for the Anthropic Messages API.
// It is stateless — base URL, API key, version, and beta headers come from
// ProviderEndpoint per-call.
type Client struct {
	httpClient *http.Client
}

// NewClient constructs an Anthropic protocol client.
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
	HTTPClient  *http.Client
	BaseURL     string
	APIKey      string
	Version     string
	BetaHeaders []string
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

	ep := ai.ProviderEndpoint{
		ProviderName:     "anthropic",
		BaseURL:          baseURL,
		APIKey:           cfg.APIKey,
		ProviderSpecific: map[string]string{"apiVersion": version},
	}

	// Convert BetaHeaders to multi-valued header
	if len(cfg.BetaHeaders) > 0 {
		var betas []string
		for _, b := range cfg.BetaHeaders {
			if strings.TrimSpace(b) != "" {
				betas = append(betas, b)
			}
		}
		if len(betas) > 0 {
			ep.Headers = map[string][]string{"anthropic-beta": betas}
		}
	}

	return ep
}
