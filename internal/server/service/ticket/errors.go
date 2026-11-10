package ticket

import (
	"net/http"
	"slices"

	"google.golang.org/grpc/codes"

	"design-cloud-engine/internal/contract"
)

// 对外错误：消息为固定短语，handler 经 httputil.RespondError 映射状态码。
var (
	ErrTicketNotFound   = contract.NewNotFoundError("ticket not found")
	ErrProjectNotFound  = contract.NewNotFoundError("project not found")
	ErrCommentNotFound  = contract.NewNotFoundError("comment not found")
	ErrPermissionDenied = contract.NewForbiddenError("permission denied")

	ErrVersionConflict     = contract.NewConflictError("ticket was modified")
	ErrProjectArchived     = contract.NewConflictError("project is archived")
	ErrProjectDeleted      = contract.NewConflictError("project is deleted")
	ErrProjectNameConflict = contract.NewConflictError("project name already exists")
	ErrProjectHasTickets   = contract.NewConflictError("project has tickets")
	ErrTicketNotDeleted    = contract.NewConflictError("ticket is not deleted")

	// ErrKeysetSortUnsupported：keyset 续页仅支持 sort=sortOrder。
	ErrKeysetSortUnsupported = contract.NewValidationError("invalid afterSort")
)

func invalid(msg string) error { return contract.NewValidationError(msg) }

// BatchError 是批量操作整体失败时的错误，Key/IDs 写入响应 details（如 {"notFound": [3, 7]}）。
type BatchError struct {
	Status int
	Msg    string
	Key    string
	IDs    []uint
}

func (e *BatchError) Error() string   { return e.Msg }
func (e *BatchError) HTTPStatus() int { return e.Status }
func (e *BatchError) Message() string { return e.Msg }
func (e *BatchError) Unwrap() error   { return nil }
func (e *BatchError) GRPCCode() codes.Code {
	switch e.Status {
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusForbidden:
		return codes.PermissionDenied
	default:
		return codes.FailedPrecondition
	}
}

func batchNotFound(ids []uint) error {
	slices.Sort(ids)
	return &BatchError{Status: http.StatusNotFound, Msg: "tickets not found", Key: "notFound", IDs: ids}
}

func batchForbidden(ids []uint) error {
	slices.Sort(ids)
	return &BatchError{Status: http.StatusForbidden, Msg: "permission denied", Key: "forbidden", IDs: ids}
}
