package execution

import (
	"design-cloud-engine/internal/execution/payload"
)

func init() {
	registerActions(map[ActionName]ActionSpec{
		// --- Direct System Queries (Linux)
		ActionUserList: {
			Description: "List users directly",
			NewPayload:  func() any { return &payload.UserList{} },
		},
		ActionUserGet: {
			Description: "Get user directly",
			NewPayload:  func() any { return &payload.UserGet{} },
		},
		ActionGroupGet: {
			Description: "Get group directly",
			NewPayload:  func() any { return &payload.GroupGet{} },
		},
		ActionGroupList: {
			Description: "List groups directly",
			NewPayload:  func() any { return &payload.GroupList{} },
		},
		ActionLicenseStatus: {
			Description: "Query license status directly",
			NewPayload:  func() any { return &payload.LicenseStatus{} },
		},
		ActionLicenseFeatures: {
			Description: "Query license features directly",
			NewPayload:  func() any { return &payload.LicenseFeatures{} },
		},

		// --- File Operations (Linux via PrivilegedExec)
		ActionFileList: {
			Description:    "List files as target user via dc-exec",
			NewPayload:     func() any { return &payload.FileList{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionFileRead: {
			Description:    "Read file content as target user via dc-exec",
			NewPayload:     func() any { return &payload.FileRead{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionFileDelete: {
			Description:    "Delete files as target user via dc-exec",
			NewPayload:     func() any { return &payload.FileDelete{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionFileMove: {
			Description:    "Move files as target user via dc-exec",
			NewPayload:     func() any { return &payload.FileMove{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionFileRename: {
			Description:    "Rename files as target user via dc-exec",
			NewPayload:     func() any { return &payload.FileRename{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionFileMkdir: {
			Description:    "Create directories as target user via dc-exec",
			NewPayload:     func() any { return &payload.FileMkdir{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionFileMkdirAll: {
			Description:    "Create directories recursively as target user via dc-exec",
			NewPayload:     func() any { return &payload.FileMkdirAll{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionFileCheck: {
			Description:    "Check files as target user via dc-exec",
			NewPayload:     func() any { return &payload.FileCheck{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionFileWriteCheck: {
			Description:    "Check directory writeability as target user via dc-exec",
			NewPayload:     func() any { return &payload.FileWriteCheck{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionTrashMove: {
			Description:    "Move to trash as target user via dc-exec",
			NewPayload:     func() any { return &payload.TrashMove{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionTrashDelete: {
			Description:    "Delete one trash item as target user via dc-exec",
			NewPayload:     func() any { return &payload.TrashDelete{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionTrashDeleteMany: {
			Description:    "Delete multiple trash items as target user via dc-exec",
			NewPayload:     func() any { return &payload.TrashDeleteMany{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionTrashRestoreMany: {
			Description:    "Restore trash items to original paths as target user via dc-exec",
			NewPayload:     func() any { return &payload.TrashRestoreMany{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionTrashEmpty: {
			Description:    "Empty trash as target user via dc-exec",
			NewPayload:     func() any { return &payload.TrashEmpty{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionTrashList: {
			Description:    "List trash files as target user via dc-exec",
			NewPayload:     func() any { return &payload.TrashList{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionFileUpload: {
			Description:    "Upload a large file as target user via PrivilegedExec (framed stdin protocol)",
			NewPayload:     func() any { return &payload.FileUpload{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelStreaming},
		},
		ActionFileDownload: {
			Description:    "Download a large file as target user via PrivilegedExec (raw stdout)",
			NewPayload:     func() any { return &payload.FileDownload{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelStreaming},
		},

		// --- User Password Management (Linux PAM/shadow via PrivilegedExec)
		ActionUserPasswordChange: {
			Description:    "User changes own password",
			NewPayload:     func() any { return &payload.UserPasswordChange{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeRoot, IOModel: IOModelJSON, DisableSeccomp: true, DisableNoNewPrivs: true},
		},
		ActionUserPasswordReset: {
			Description:    "Admin resets user password",
			NewPayload:     func() any { return &payload.UserPasswordReset{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeRoot, IOModel: IOModelJSON, DisableSeccomp: true, DisableNoNewPrivs: true},
		},
		ActionUserPasswordVerify: {
			Description:    "Verify local user password against shadow",
			NewPayload:     func() any { return &payload.UserPasswordVerify{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeRoot, IOModel: IOModelJSON, DisableSeccomp: true, DisableNoNewPrivs: true},
		},

		// --- Scheduler Management (Linux Slurm/PBS)
		ActionJobQuery: {
			Description: "Query job information directly on the agent",
			NewPayload:  func() any { return &payload.JobQuery{} },
		},
		ActionJobSubmit: {
			Description: "Submit job via PrivilegedExec as target user",
			NewPayload:  func() any { return &payload.JobSubmit{} },
			// LSF/Slurm/PBS CLIs need sockets and vendor-specific syscalls; seccomp hangs until timeout.
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, DisableSeccomp: true},
		},
		ActionJobControl: {
			Description:    "Control job (kill, suspend, etc.) via PrivilegedExec as target user",
			NewPayload:     func() any { return &payload.JobControl{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, DisableSeccomp: true},
		},
		ActionJobModify: {
			Description:    "Modify job attributes (queue, name) via PrivilegedExec as target user",
			NewPayload:     func() any { return &payload.JobModify{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, DisableSeccomp: true},
		},

		// --- Connection & VNC Operations (Linux)
		ActionConnectionCheck: {
			Description:    "Check VNC connection liveness on the target node as the connection owner",
			NewPayload:     func() any { return &payload.ConnectionCheck{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON},
		},
		ActionVNCGenerateOTP: {
			Description:    "Generate a TurboVNC one-time password for a running VNC session",
			NewPayload:     func() any { return &payload.VNCGenerateOTP{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, DisableSeccomp: true},
		},
		ActionVNCControl: {
			Description:    "Start or stop a VNC server via vncserver commands as the target user",
			NewPayload:     func() any { return &payload.VNCControl{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, DisableSeccomp: true, DisableNoNewPrivs: true},
		},
		ActionVNCCgroupControl: {
			Description:    "Start or stop a VNC server under systemd-run Cgroup slice isolation",
			NewPayload:     func() any { return &payload.VNCControl{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeRoot, IOModel: IOModelJSON, DisableSeccomp: true, DisableNoNewPrivs: true},
		},
		ActionXRDPControl: {
			Description:    "Stop an XRDP user X seat via loginctl terminate-session then Xorg SIGTERM/SIGKILL as the target user",
			NewPayload:     func() any { return &payload.XRDPControl{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, DisableSeccomp: true},
		},
		ActionSessionControl: {
			Description:    "Stop the target user's own SSH login sessions via loginctl (never terminate-user; never other users)",
			NewPayload:     func() any { return &payload.SessionControl{} },
			PrivilegedExec: &PrivilegedExecSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, DisableSeccomp: true},
		},
	})
}
