package ai

import (
	"context"
	"fmt"
)

// Embed is the top-level embedding entry point.
func Embed(ctx context.Context, modelID string, req EmbeddingRequest) (*EmbeddingResponse, error) {
	if len(req.Texts) == 0 {
		return nil, fmt.Errorf("embedding request requires at least one text")
	}

	model, ok := GetEmbeddingModel(modelID)
	if !ok {
		return nil, fmt.Errorf("unknown embedding model: %s", modelID)
	}

	provider, err := GetEmbeddingProvider(model.API)
	if err != nil {
		return nil, err
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

	resp, err := provider.Embed(ctx, model, req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("embedding provider %s returned nil response", model.API)
	}
	if resp.Model == "" {
		resp.Model = model.ID
	}
	if resp.Usage.Cost == 0 && model.Cost.PerMTok > 0 && resp.Usage.Tokens > 0 {
		resp.Usage.Cost = float64(resp.Usage.Tokens) * model.Cost.PerMTok / 1_000_000
	}
	return resp, nil
}
