package execution

import (
	"maps"

	"design-cloud-engine/internal/execution/payload"
)

func init() {
	maps.Copy(Actions, map[ActionName]ActionSpec{
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

		// --- File Operations (Linux via Launcher)
		ActionFileList: {
			Description: "List files as target user via launcher",
			NewPayload:  func() any { return &payload.FileList{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionFileRead: {
			Description: "Read file content as target user via launcher",
			NewPayload:  func() any { return &payload.FileRead{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionFileDelete: {
			Description: "Delete files as target user via launcher",
			NewPayload:  func() any { return &payload.FileDelete{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionFileMove: {
			Description: "Move files as target user via launcher",
			NewPayload:  func() any { return &payload.FileMove{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionFileRename: {
			Description: "Rename files as target user via launcher",
			NewPayload:  func() any { return &payload.FileRename{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionFileMkdir: {
			Description: "Create directories as target user via launcher",
			NewPayload:  func() any { return &payload.FileMkdir{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionFileMkdirAll: {
			Description: "Create directories recursively as target user via launcher",
			NewPayload:  func() any { return &payload.FileMkdirAll{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionFileCheck: {
			Description: "Check files as target user via launcher",
			NewPayload:  func() any { return &payload.FileCheck{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionFileWriteCheck: {
			Description: "Check directory writeability as target user via launcher",
			NewPayload:  func() any { return &payload.FileWriteCheck{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionTrashMove: {
			Description: "Move to trash as target user via launcher",
			NewPayload:  func() any { return &payload.TrashMove{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionTrashDelete: {
			Description: "Delete one trash item as target user via launcher",
			NewPayload:  func() any { return &payload.TrashDelete{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionTrashDeleteMany: {
			Description: "Delete multiple trash items as target user via launcher",
			NewPayload:  func() any { return &payload.TrashDeleteMany{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionTrashEmpty: {
			Description: "Empty trash as target user via launcher",
			NewPayload:  func() any { return &payload.TrashEmpty{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionTrashList: {
			Description: "List trash files as target user via launcher",
			NewPayload:  func() any { return &payload.TrashList{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionFileUpload: {
			Description: "Upload a large file as target user via launcher (framed stdin protocol)",
			NewPayload:  func() any { return &payload.FileUpload{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelStreaming, EnableSeccomp: true},
		},
		ActionFileDownload: {
			Description: "Download a large file as target user via launcher (raw stdout)",
			NewPayload:  func() any { return &payload.FileDownload{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelStreaming, EnableSeccomp: true},
		},

		// --- User Password Management (Linux PAM/shadow via launcher)
		ActionUserPasswordChange: {
			Description: "User changes own password",
			NewPayload:  func() any { return &payload.UserPasswordChange{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeRoot, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionUserPasswordReset: {
			Description: "Admin resets user password",
			NewPayload:  func() any { return &payload.UserPasswordReset{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeRoot, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionUserPasswordVerify: {
			Description: "Verify local user password against shadow",
			NewPayload:  func() any { return &payload.UserPasswordVerify{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeRoot, IOModel: IOModelJSON, EnableSeccomp: true},
		},

		// --- Scheduler Management (Linux Slurm/PBS)
		ActionJobQuery: {
			Description: "Query job information directly on the agent",
			NewPayload:  func() any { return &payload.JobQuery{} },
		},
		ActionJobSubmit: {
			Description: "Submit job via launcher as target user",
			NewPayload:  func() any { return &payload.JobSubmit{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionJobControl: {
			Description: "Control job (kill, suspend, etc.) via launcher as target user",
			NewPayload:  func() any { return &payload.JobControl{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionJobModify: {
			Description: "Modify job attributes (queue, name) via launcher as target user",
			NewPayload:  func() any { return &payload.JobModify{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},

		// --- Connection & VNC Operations (Linux)
		ActionConnectionCheck: {
			Description: "Check VNC connection liveness on the target node as the connection owner",
			NewPayload:  func() any { return &payload.ConnectionCheck{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: true},
		},
		ActionVNCGenerateOTP: {
			Description: "Generate a TurboVNC one-time password for a running VNC session",
			NewPayload:  func() any { return &payload.VNCGenerateOTP{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: false},
		},
		ActionVNCControl: {
			Description: "Start or stop a VNC server via vncserver commands as the target user",
			NewPayload:  func() any { return &payload.VNCControl{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeTargetUser, IOModel: IOModelJSON, EnableSeccomp: false},
		},
		ActionVNCCgroupControl: {
			Description: "Start or stop a VNC server under systemd-run Cgroup slice isolation",
			NewPayload:  func() any { return &payload.VNCControl{} },
			Launcher:    &LauncherSpec{Privilege: PrivilegeRoot, IOModel: IOModelJSON, EnableSeccomp: false},
		},
	})
}
