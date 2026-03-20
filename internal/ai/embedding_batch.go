package ai

import (
	"context"
	"fmt"
)

// BatchEmbed splits large embedding requests across provider max batch size.
func BatchEmbed(ctx context.Context, embedFn EmbedFunc, endpoint ProviderEndpoint, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
	if embedFn == nil {
		return nil, fmt.Errorf("embed function is required")
	}
	batchSize := model.MaxBatchSize
	if batchSize <= 0 {
		batchSize = len(req.Texts)
		if batchSize == 0 {
			batchSize = 1
		}
	}
	if len(req.Texts) <= batchSize {
		return embedFn(ctx, endpoint, model, req)
	}

	allEmbeddings := make([]Embedding, 0, len(req.Texts))
	usage := EmbeddingUsage{}
	total := len(req.Texts)

	for start := 0; start < total; start += batchSize {
		if err := ctx.Err(); err != nil {
			return &EmbeddingResponse{Embeddings: allEmbeddings, Model: model.ID, Usage: usage}, err
		}
		end := start + batchSize
		if end > total {
			end = total
		}
		batchReq := req
		batchReq.Texts = req.Texts[start:end]
		batchReq.OnProgress = nil

		resp, err := embedFn(ctx, endpoint, model, batchReq)
		if err != nil {
			return &EmbeddingResponse{Embeddings: allEmbeddings, Model: model.ID, Usage: usage}, fmt.Errorf("embed batch [%d:%d] failed: %w", start, end, err)
		}
		if resp != nil {
			for _, emb := range resp.Embeddings {
				emb.Index += start
				allEmbeddings = append(allEmbeddings, emb)
			}
			usage.Tokens += resp.Usage.Tokens
			usage.Cost += resp.Usage.Cost
		}
		if req.OnProgress != nil {
			req.OnProgress(end, total)
		}
	}

	return &EmbeddingResponse{
		Embeddings: allEmbeddings,
		Model:      model.ID,
		Usage:      usage,
	}, nil
}
