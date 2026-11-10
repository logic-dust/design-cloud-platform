package contract

import (
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"
)

// ErrNotFound is returned when a queried record does not exist.
// All layers (db, service, server) can use errors.Is(err, contract.ErrNotFound)
// to distinguish "not found" from other errors (e.g. for HTTP 404 responses).
var ErrNotFound = errors.New("not found")

// DomainError defines the standardized interface for domain-level business errors
// that map cleanly to both HTTP status codes and gRPC status codes.
type DomainError interface {
	error
	HTTPStatus() int
	GRPCCode() codes.Code
	Message() string
	Unwrap() error
}

type appError struct {
	httpStatus int
	grpcCode   codes.Code
	message    string
	err        error
}

func (e *appError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%s: %v", e.message, e.err)
	}
	return e.message
}

func (e *appError) HTTPStatus() int      { return e.httpStatus }
func (e *appError) GRPCCode() codes.Code { return e.grpcCode }
func (e *appError) Message() string      { return e.message }
func (e *appError) Unwrap() error        { return e.err }

func (e *appError) Is(target error) bool {
	if target == nil {
		return false
	}
	if target == ErrNotFound && e.httpStatus == http.StatusNotFound {
		return true
	}
	return errors.Is(e.err, target)
}

// 400 Bad Request / Invalid Argument
func NewInvalidParamError(msg string, cause ...error) error {
	return wrap(http.StatusBadRequest, codes.InvalidArgument, msg, cause...)
}

// 401 Unauthorized / Unauthenticated
func NewUnauthorizedError(msg string, cause ...error) error {
	return wrap(http.StatusUnauthorized, codes.Unauthenticated, msg, cause...)
}

// 403 Forbidden / Permission Denied
func NewForbiddenError(msg string, cause ...error) error {
	return wrap(http.StatusForbidden, codes.PermissionDenied, msg, cause...)
}

// 404 Not Found
func NewNotFoundError(msg string, cause ...error) error {
	return wrap(http.StatusNotFound, codes.NotFound, msg, cause...)
}

// 409 Conflict / Already Exists
func NewConflictError(msg string, cause ...error) error {
	return wrap(http.StatusConflict, codes.AlreadyExists, msg, cause...)
}

// 422 Unprocessable Entity / Validation Failed
func NewValidationError(msg string, cause ...error) error {
	return wrap(http.StatusUnprocessableEntity, codes.InvalidArgument, msg, cause...)
}

// 503 Service Unavailable
func NewUnavailableError(msg string, cause ...error) error {
	return wrap(http.StatusServiceUnavailable, codes.Unavailable, msg, cause...)
}

func wrap(httpStatus int, grpcCode codes.Code, msg string, cause ...error) *appError {
	var underlying error
	if len(cause) > 0 {
		underlying = cause[0]
	}
	return &appError{
		httpStatus: httpStatus,
		grpcCode:   grpcCode,
		message:    msg,
		err:        underlying,
	}
}
