package ai

import (
	"context"
	"fmt"
)

// Embed is the top-level embedding entry point. Resolution order:
//  1. New path: model.Provider → ProviderConfig → EmbeddingAPIClient + ResolveEndpoint
//  2. Legacy fallback: model.API → old EmbeddingProvider registry (until callers migrate)
func Embed(ctx context.Context, modelID string, req EmbeddingRequest) (*EmbeddingResponse, error) {
	if len(req.Texts) == 0 {
		return nil, fmt.Errorf("embedding request requires at least one text")
	}

	model, ok := GetEmbeddingModel(modelID)
	if !ok {
		return nil, fmt.Errorf("unknown embedding model: %s", modelID)
	}

	if req.Dimensions > 0 && !model.SupportsDimCtrl {
		return nil, fmt.Errorf("model %s does not support dimension control", modelID)
	}
	if req.Dimensions > 0 && model.MinDims > 0 && req.Dimensions < model.MinDims {
		return nil, fmt.Errorf("requested dimensions %d below min %d for model %s", req.Dimensions, model.MinDims, modelID)
	}
	if req.Dimensions > 0 && model.MaxDims > 0 && req.Dimensions > model.MaxDims {
		return nil, fmt.Errorf("requested dimensions %d exceeds max %d for model %s", req.Dimensions, model.MaxDims, modelID)
	}

	// Try new two-step resolution path first.
	if cfg, err := GetProviderConfig(model.Provider); err == nil {
		// Determine embedding client type: provider config first, then model.API fallback.
		clientType := cfg.EmbeddingAPIClientType
		if clientType == "" {
			clientType = model.API
		}
		if client, err := GetEmbeddingAPIClient(clientType); err == nil {
			endpoint := ResolveEndpoint(cfg, StreamOptions{})
			resp, err := client.Embed(ctx, endpoint, model, req)
			if err != nil {
				return nil, err
			}
			return finalizeEmbeddingResponse(resp, model)
		}
	}

	// Legacy fallback: use old EmbeddingProvider registry.
	provider, err := GetEmbeddingProvider(model.API)
	if err != nil {
		return nil, fmt.Errorf("no provider config for %q and no legacy embedding provider for API %q", model.Provider, model.API)
	}

	resp, err := provider.Embed(ctx, model, req)
	if err != nil {
		return nil, err
	}
	return finalizeEmbeddingResponse(resp, model)
}

func finalizeEmbeddingResponse(resp *EmbeddingResponse, model EmbeddingModel) (*EmbeddingResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("embedding provider returned nil response for model %s", model.ID)
	}
	if resp.Model == "" {
		resp.Model = model.ID
	}
	if resp.Usage.Cost == 0 && model.Cost.PerMTok > 0 && resp.Usage.Tokens > 0 {
		resp.Usage.Cost = float64(resp.Usage.Tokens) * model.Cost.PerMTok / 1_000_000
	}
	return resp, nil
}
