package transport

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/anthropics/flex-agent-runtime/internal/rpc"
)

type ServerAuthHook func(ctx context.Context, procedure string, headers http.Header) error

type InterceptorConfig struct {
	AuthHook       ServerAuthHook
	APIVersion     string
	MinAPIVersion  string
	APIVersionName string
}

func NewPolicyInterceptor(cfg InterceptorConfig) connect.Interceptor {
	name := cfg.APIVersionName
	if name == "" {
		name = rpc.APIVersionHeaderName()
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = "v1"
	}
	if cfg.MinAPIVersion == "" {
		cfg.MinAPIVersion = rpc.MinSupportedAPIVersion()
	}
	cfg.APIVersionName = name
	return &policyInterceptor{cfg: cfg}
}

type policyInterceptor struct {
	cfg InterceptorConfig
}

func (i *policyInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := i.validate(ctx, req.Spec().Procedure, req.Header()); err != nil {
			return nil, err
		}
		resp, err := next(ctx, req)
		if err != nil {
			return nil, err
		}
		resp.Header().Set(i.cfg.APIVersionName, i.cfg.APIVersion)
		return resp, nil
	}
}

func (i *policyInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *policyInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := i.validate(ctx, conn.Spec().Procedure, conn.RequestHeader()); err != nil {
			return err
		}
		conn.ResponseHeader().Set(i.cfg.APIVersionName, i.cfg.APIVersion)
		return next(ctx, conn)
	}
}

func (i *policyInterceptor) validate(ctx context.Context, procedure string, headers http.Header) error {
	if i.cfg.AuthHook != nil {
		if err := i.cfg.AuthHook(ctx, procedure, headers); err != nil {
			return connect.NewError(connect.CodeUnauthenticated, err)
		}
	}
	got := strings.TrimSpace(headers.Get(i.cfg.APIVersionName))
	if got == "" {
		got = "v1"
	}
	if versionLessThan(got, i.cfg.MinAPIVersion) {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("unsupported API version: got %s, minimum %s", got, i.cfg.MinAPIVersion))
	}
	return nil
}

func versionLessThan(a, b string) bool {
	parse := func(v string) int {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		n, _ := strconv.Atoi(v)
		return n
	}
	return parse(a) < parse(b)
}
