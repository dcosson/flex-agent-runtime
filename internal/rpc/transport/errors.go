package transport

import (
	"errors"

	"connectrpc.com/connect"
	"h2-agent-runtime/internal/rpc"
)

func toConnectError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		return connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewError(mapCode(rpcErr.Code), err)
}

func mapCode(code rpc.Code) connect.Code {
	switch code {
	case rpc.CodeInvalidArgument:
		return connect.CodeInvalidArgument
	case rpc.CodeNotFound:
		return connect.CodeNotFound
	case rpc.CodeAlreadyExists:
		return connect.CodeAlreadyExists
	case rpc.CodeFailedPrecondition:
		return connect.CodeFailedPrecondition
	case rpc.CodePermissionDenied:
		return connect.CodePermissionDenied
	case rpc.CodeResourceExhausted:
		return connect.CodeResourceExhausted
	case rpc.CodeUnavailable:
		return connect.CodeUnavailable
	case rpc.CodeDeadlineExceeded:
		return connect.CodeDeadlineExceeded
	case rpc.CodeCanceled:
		return connect.CodeCanceled
	default:
		return connect.CodeInternal
	}
}
