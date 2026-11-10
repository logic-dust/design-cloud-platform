package execution

import "fmt"

type ActionName string

// PrivilegeMode 定义通过 PrivilegedExec 执行时的权限策略
type PrivilegeMode string

const (
	PrivilegeTargetUser PrivilegeMode = "target_user" // 切换/降权到请求中指定的 targetUser
	PrivilegeRoot       PrivilegeMode = "root"        // 提权/保持 root 身份
)

// IOModel 定义 Action 的数据传输与进程交互模型
type IOModel string

const (
	IOModelJSON      IOModel = "json"      // 默认：短生命周期，标准 JSON 请求/响应
	IOModelStreaming IOModel = "streaming" // 大文件流式传输 (Framed Header + Raw Bytes)
)

// PrivilegedExecSpec 描述 Action 委托给 PrivilegedExec 时的行为要求
type PrivilegedExecSpec struct {
	// Privilege 指定权限切换策略（目标用户还是 root）
	Privilege PrivilegeMode
	// IOModel 指定交互模型（JSON 或 Streaming），默认 IOModelJSON
	IOModel IOModel
	// DisableSeccomp 关闭 seccomp 系统调用白名单；零值表示加载白名单。
	DisableSeccomp bool
	// DisableNoNewPrivs 关闭 PR_SET_NO_NEW_PRIVS；零值表示设置 NNP。
	// seccomp 依赖 NNP 才能在无 CAP_SYS_ADMIN 时加载，因此关闭 NNP 必须同时关闭 seccomp。
	DisableNoNewPrivs bool
}

// Validate 检查 PrivilegedExecSpec 的保护开关组合是否自洽。
func (s *PrivilegedExecSpec) Validate() error {
	if s == nil {
		return nil
	}
	if s.DisableNoNewPrivs && !s.DisableSeccomp {
		return fmt.Errorf("PrivilegedExec spec: DisableNoNewPrivs requires DisableSeccomp")
	}
	return nil
}

// ActionSpec 定义单个受控 Action 的完整规格
type ActionSpec struct {
	Description string
	// NewPayload returns a zero-value pointer to the expected payload type for this action.
	// Used by GetPayload to validate ActionName ↔ Payload type consistency at runtime.
	NewPayload func() any
	// PrivilegedExec 为 nil 时表示 Agent 内部直接执行 (Direct)；非 nil 表示委托给 PrivilegedExec 执行
	PrivilegedExec *PrivilegedExecSpec
}

// IsDirect 返回该 Action 是否由 Agent 内部直接执行（不经过 DcExec）
func (s ActionSpec) IsDirect() bool {
	return s.PrivilegedExec == nil
}

// IsStream 返回该 Action 是否使用流式 I/O 协议
func (s ActionSpec) IsStream() bool {
	return s.PrivilegedExec != nil && s.PrivilegedExec.IOModel == IOModelStreaming
}

// --- Action Definitions (Constants) ---

const (
	// --- User Management

	ActionUserPasswordChange ActionName = "USER_PASSWORD_CHANGE" // User changes own password
	ActionUserPasswordReset  ActionName = "USER_PASSWORD_RESET"  // Admin resets user password
	ActionUserPasswordVerify ActionName = "USER_PASSWORD_VERIFY"

	ActionUserList  ActionName = "USER_LIST"
	ActionUserGet   ActionName = "USER_GET"
	ActionGroupGet  ActionName = "GROUP_GET"
	ActionGroupList ActionName = "GROUP_LIST"

	// --- Scheduler Management

	ActionJobQuery   ActionName = "JOB_QUERY"
	ActionJobSubmit  ActionName = "JOB_SUBMIT"
	ActionJobControl ActionName = "JOB_CONTROL"
	ActionJobModify  ActionName = "JOB_MODIFY"

	// --- File Operations

	ActionFileList         ActionName = "FILE_LIST"
	ActionFileRead         ActionName = "FILE_READ"
	ActionFileDelete       ActionName = "FILE_DELETE"
	ActionFileMove         ActionName = "FILE_MOVE"
	ActionFileRename       ActionName = "FILE_RENAME"
	ActionFileMkdir        ActionName = "FILE_MKDIR"
	ActionFileMkdirAll     ActionName = "FILE_MKDIR_ALL"
	ActionFileCheck        ActionName = "FILE_CHECK"
	ActionFileWriteCheck   ActionName = "FILE_WRITE_CHECK"
	ActionTrashMove        ActionName = "TRASH_MOVE"
	ActionTrashDelete      ActionName = "TRASH_DELETE"
	ActionTrashDeleteMany  ActionName = "TRASH_DELETE_MANY"
	ActionTrashRestoreMany ActionName = "TRASH_RESTORE_MANY"
	ActionTrashEmpty       ActionName = "TRASH_EMPTY"
	ActionTrashList        ActionName = "TRASH_LIST"

	// ActionFileUpload Large file I/O — bypass the 1 MB JSON-envelope limit via a newline-delimited
	// stdin protocol: [compact JSON ExecuteInput]\n followed by raw file bytes.
	// Download stdout carries raw file bytes; errors go to stderr only.
	ActionFileUpload   ActionName = "FILE_UPLOAD"
	ActionFileDownload ActionName = "FILE_DOWNLOAD"

	//--- License Management

	ActionLicenseStatus   ActionName = "LICENSE_GET_STATUS"    // Query license server status
	ActionLicenseFeatures ActionName = "LICENSE_LIST_FEATURES" // Query license features/usage

	// --- Connection Operations

	// ActionConnectionCheck 检查受管 VNC/XRDP 座位在目标节点上的存活状态。
	// 底层通过 /proc 校验 Xvnc/Xtigervnc/Xorg(xrdp) 进程与 UID；
	// RDP/SSH 拨号实例不经由本 Action 探活（会话收尾走 SESSION_CONTROL / Stop）。
	ActionConnectionCheck  ActionName = "CONNECTION_CHECK"
	ActionVNCGenerateOTP   ActionName = "VNC_GENERATE_OTP"   // Generate a TurboVNC one-time password for a running VNC session
	ActionVNCControl       ActionName = "VNC_CONTROL"        // Start or stop a VNC server via vncserver commands
	ActionVNCCgroupControl ActionName = "VNC_CGROUP_CONTROL" // Start or stop a VNC server with systemd-run Cgroup slice isolation
	ActionXRDPControl      ActionName = "XRDP_CONTROL"       // Stop an XRDP user X seat (loginctl / Xorg kill); never touches xrdp daemon
	ActionSessionControl   ActionName = "SESSION_CONTROL"    // Stop a user login session (SSH on Linux / RDP on Windows); never stops system daemons
)

// Actions holds all registered action specifications for the current platform.
// Platform-specific actions are registered in init() within actions_<platform>.go.
var Actions = make(map[ActionName]ActionSpec)

// registerActions 校验并登记平台 action；PrivilegedExecSpec 不自洽时 panic，使配置错误在启动时暴露。
func registerActions(specs map[ActionName]ActionSpec) {
	for name, spec := range specs {
		if err := spec.PrivilegedExec.Validate(); err != nil {
			panic(fmt.Sprintf("execution: action %s: %v", name, err))
		}
		Actions[name] = spec
	}
}
