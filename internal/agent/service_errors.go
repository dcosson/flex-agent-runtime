package agent

import "fmt"

type ServiceCode string

const (
	CodeInvalidArgument    ServiceCode = "invalid_argument"
	CodeNotFound           ServiceCode = "not_found"
	CodeAlreadyExists      ServiceCode = "already_exists"
	CodeFailedPrecondition ServiceCode = "failed_precondition"
	CodeResourceExhausted  ServiceCode = "resource_exhausted"
	CodeUnavailable        ServiceCode = "unavailable"
	CodeDeadlineExceeded   ServiceCode = "deadline_exceeded"
	CodeCanceled           ServiceCode = "canceled"
	CodeInternal           ServiceCode = "internal"
)

type ServiceError struct {
	Code    ServiceCode
	Message string
	Cause   error
}

func (e *ServiceError) Error() string {
	if e == nil {
		return "service: <nil>"
	}
	if e.Message == "" {
		return fmt.Sprintf("service error: %s", e.Code)
	}
	return fmt.Sprintf("service error: %s: %s", e.Code, e.Message)
}

func (e *ServiceError) Unwrap() error { return e.Cause }

func newServiceError(code ServiceCode, msg string, cause error) *ServiceError {
	return &ServiceError{Code: code, Message: msg, Cause: cause}
}
