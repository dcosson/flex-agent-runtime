package rpc

import "context"

type ctxKey string

const apiVersionHeader = "x-api-version"

const (
	ctxKeyAPIVersion ctxKey = "rpc_api_version"
)

func WithAPIVersion(ctx context.Context, version string) context.Context {
	return context.WithValue(ctx, ctxKeyAPIVersion, version)
}

func APIVersionFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(ctxKeyAPIVersion).(string); ok {
		return v
	}
	return ""
}

func APIVersionHeaderName() string {
	return apiVersionHeader
}
