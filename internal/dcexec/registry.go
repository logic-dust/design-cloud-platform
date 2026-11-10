//go:build !windows

package dcexec

import (
	"context"

	"design-cloud-engine/internal/dcexec/action"
	"design-cloud-engine/internal/dcexec/action/file"
	"design-cloud-engine/internal/execution"
)

// Handler is the function signature for action handlers.
type Handler func(ctx context.Context, req *execution.ExecuteRequest) (string, error)

// Handlers defines the static PrivilegedExec handler registry.
// Route / identity metadata lives in execution.Actions.
var Handlers = map[execution.ActionName]Handler{
	// User Operations
	execution.ActionUserPasswordChange: action.HandleUserChangePassword,
	execution.ActionUserPasswordReset:  action.HandleUserResetPassword,
	execution.ActionUserPasswordVerify: action.HandleVerifyUser,

	// File Operations
	execution.ActionFileList:         file.HandleFileList,
	execution.ActionFileRead:         file.HandleFileRead,
	execution.ActionFileDelete:       file.HandleFileDelete,
	execution.ActionFileMove:         file.HandleFileMove,
	execution.ActionFileRename:       file.HandleFileRename,
	execution.ActionFileMkdir:        file.HandleFileMkdir,
	execution.ActionFileMkdirAll:     file.HandleFileMkdirAll,
	execution.ActionFileCheck:        file.HandleFileCheck,
	execution.ActionFileWriteCheck:   file.HandleFileWriteCheck,
	execution.ActionTrashMove:        file.HandleTrashMove,
	execution.ActionTrashDelete:      file.HandleTrashDelete,
	execution.ActionTrashDeleteMany:  file.HandleTrashDeleteMany,
	execution.ActionTrashRestoreMany: file.HandleTrashRestoreMany,
	execution.ActionTrashEmpty:       file.HandleTrashEmpty,
	execution.ActionTrashList:        file.HandleTrashList,

	// Job Operations (JobQuery is direct — executed on agent, not via PrivilegedExec)
	execution.ActionJobSubmit:  action.HandleJobSubmit,
	execution.ActionJobControl: action.HandleJobControl,
	execution.ActionJobModify:  action.HandleJobModify,

	// Connection Operations
	execution.ActionConnectionCheck:  action.HandleConnectionCheck,
	execution.ActionVNCGenerateOTP:   action.HandleVNCGenerateOTP,
	execution.ActionVNCControl:       action.HandleVNCControl,
	execution.ActionVNCCgroupControl: action.HandleVNCCgroupControl,
	execution.ActionXRDPControl:      action.HandleXRDPControl,
	execution.ActionSessionControl:   action.HandleSessionControl,
}
