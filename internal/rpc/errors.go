package rpc

import (
	"context"
	"errors"
	"fmt"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox"
	"github.com/anthropics/flex-agent-runtime/internal/sandbox/zfs"
)

type Code string

const (
	CodeOK                 Code = "ok"
	CodeInvalidArgument    Code = "invalid_argument"
	CodeNotFound           Code = "not_found"
	CodeAlreadyExists      Code = "already_exists"
	CodeFailedPrecondition Code = "failed_precondition"
	CodePermissionDenied   Code = "permission_denied"
	CodeResourceExhausted  Code = "resource_exhausted"
	CodeUnavailable        Code = "unavailable"
	CodeDeadlineExceeded   Code = "deadline_exceeded"
	CodeCanceled           Code = "canceled"
	CodeInternal           Code = "internal"
)

type RPCError struct {
	Code    Code
	Message string
	Details map[string]string
	Cause   error
}

func (e *RPCError) Error() string {
	if e == nil {
		return "rpc: <nil>"
	}
	if e.Message == "" {
		return fmt.Sprintf("rpc error: %s", e.Code)
	}
	return fmt.Sprintf("rpc error: %s: %s", e.Code, e.Message)
}

func (e *RPCError) Unwrap() error { return e.Cause }

func NewRPCError(code Code, msg string, cause error) *RPCError {
	return &RPCError{Code: code, Message: msg, Cause: cause}
}

func MapError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	var serviceErr *agent.ServiceError
	if errors.As(err, &serviceErr) {
		msg := serviceErr.Message
		if msg == "" {
			msg = err.Error()
		}
		return NewRPCError(mapServiceCode(serviceErr.Code), msg, err)
	}
	switch {
	case errors.Is(err, agent.ErrQueueFull):
		return NewRPCError(CodeResourceExhausted, err.Error(), err)
	case errors.Is(err, agent.ErrBusy),
		errors.Is(err, agent.ErrStopped),
		errors.Is(err, agent.ErrInvalidState):
		return NewRPCError(CodeFailedPrecondition, err.Error(), err)
	case errors.Is(err, sandbox.ErrSessionNotFound):
		return NewRPCError(CodeNotFound, err.Error(), err)
	case errors.Is(err, sandbox.ErrProcessNotFound):
		return NewRPCError(CodeNotFound, err.Error(), err)
	case errors.Is(err, sandbox.ErrSessionExists):
		return NewRPCError(CodeAlreadyExists, err.Error(), err)
	case errors.Is(err, sandbox.ErrSessionPaused),
		errors.Is(err, sandbox.ErrSessionDestroying),
		errors.Is(err, sandbox.ErrInvalidState),
		errors.Is(err, sandbox.ErrRollbackInProgress),
		errors.Is(err, sandbox.ErrToolsInFlight),
		errors.Is(err, sandbox.ErrMaxSessionsReached):
		return NewRPCError(CodeFailedPrecondition, err.Error(), err)
	case errors.Is(err, zfs.ErrPoolFull):
		return NewRPCError(CodeResourceExhausted, err.Error(), err)
	case errors.Is(err, context.Canceled):
		return NewRPCError(CodeCanceled, err.Error(), err)
	case errors.Is(err, context.DeadlineExceeded):
		return NewRPCError(CodeDeadlineExceeded, err.Error(), err)
	default:
		return NewRPCError(CodeInternal, err.Error(), err)
	}
}

func mapServiceCode(code agent.ServiceCode) Code {
	switch code {
	case agent.CodeInvalidArgument:
		return CodeInvalidArgument
	case agent.CodeNotFound:
		return CodeNotFound
	case agent.CodeAlreadyExists:
		return CodeAlreadyExists
	case agent.CodeFailedPrecondition:
		return CodeFailedPrecondition
	case agent.CodeResourceExhausted:
		return CodeResourceExhausted
	case agent.CodeUnavailable:
		return CodeUnavailable
	case agent.CodeDeadlineExceeded:
		return CodeDeadlineExceeded
	case agent.CodeCanceled:
		return CodeCanceled
	default:
		return CodeInternal
	}
}

func WrapRPCError(err error, sessionID, toolName string) error {
	if err == nil {
		return nil
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		if rpcErr.Details == nil {
			rpcErr.Details = map[string]string{}
		}
		if sessionID != "" {
			rpcErr.Details["session_id"] = sessionID
		}
		if toolName != "" {
			rpcErr.Details["tool_name"] = toolName
		}
		return rpcErr
	}
	return &RPCError{
		Code:    CodeInternal,
		Message: err.Error(),
		Details: map[string]string{"session_id": sessionID, "tool_name": toolName},
		Cause:   err,
	}
}
