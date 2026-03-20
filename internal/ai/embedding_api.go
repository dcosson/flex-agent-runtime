package ai

import (
	"context"
	"fmt"
)

// Embed is the top-level embedding entry point.
// Resolution: model.Provider → ProviderConfig → EmbeddingAPIClient + ResolveEndpoint
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

	cfg, err := GetProviderConfig(model.Provider)
	if err != nil {
		return nil, fmt.Errorf("no provider config for embedding model %q provider %q: %w", modelID, model.Provider, err)
	}

	// Determine embedding client type: provider config first, then model.API fallback.
	clientType := cfg.EmbeddingAPIClientType
	if clientType == "" {
		clientType = model.API
	}
	client, err := GetEmbeddingAPIClient(clientType)
	if err != nil {
		return nil, fmt.Errorf("no embedding API client for type %q (provider %q): %w", clientType, model.Provider, err)
	}

	endpoint := ResolveEndpoint(cfg, StreamOptions{})
	resp, err := client.Embed(ctx, endpoint, model, req)
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
