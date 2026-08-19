package execution

type ActionName string

// PrivilegeMode 定义通过 Launcher 执行时的权限策略
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

// LauncherSpec 描述 Action 委托给 Launcher 时的行为要求
type LauncherSpec struct {
	// Privilege 指定权限切换策略（目标用户还是 root）
	Privilege PrivilegeMode
	// IOModel 指定交互模型（JSON 或 Streaming），默认 IOModelJSON
	IOModel IOModel
	// EnableSeccomp 指定是否启用 seccomp 系统调用沙箱，默认 true
	EnableSeccomp bool
}

// ActionSpec 定义单个受控 Action 的完整规格
type ActionSpec struct {
	Description string
	// NewPayload returns a zero-value pointer to the expected payload type for this action.
	// Used by GetPayload to validate ActionName ↔ Payload type consistency at runtime.
	NewPayload func() any
	// Launcher 为 nil 时表示 Agent 内部直接执行 (Direct)；非 nil 表示委托给 Launcher 执行
	Launcher *LauncherSpec
}

// IsDirect 返回该 Action 是否由 Agent 内部直接执行（不经过 Launcher）
func (s ActionSpec) IsDirect() bool {
	return s.Launcher == nil
}

// IsStream 返回该 Action 是否使用流式 I/O 协议
func (s ActionSpec) IsStream() bool {
	return s.Launcher != nil && s.Launcher.IOModel == IOModelStreaming
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

	ActionFileList        ActionName = "FILE_LIST"
	ActionFileRead        ActionName = "FILE_READ"
	ActionFileDelete      ActionName = "FILE_DELETE"
	ActionFileMove        ActionName = "FILE_MOVE"
	ActionFileRename      ActionName = "FILE_RENAME"
	ActionFileMkdir       ActionName = "FILE_MKDIR"
	ActionFileMkdirAll    ActionName = "FILE_MKDIR_ALL"
	ActionFileCheck       ActionName = "FILE_CHECK"
	ActionFileWriteCheck  ActionName = "FILE_WRITE_CHECK"
	ActionTrashMove       ActionName = "TRASH_MOVE"
	ActionTrashDelete     ActionName = "TRASH_DELETE"
	ActionTrashDeleteMany ActionName = "TRASH_DELETE_MANY"
	ActionTrashEmpty      ActionName = "TRASH_EMPTY"
	ActionTrashList       ActionName = "TRASH_LIST"

	// ActionFileUpload Large file I/O — bypass the 1 MB JSON-envelope limit via a newline-delimited
	// stdin protocol: [compact JSON ExecuteInput]\n followed by raw file bytes.
	// Download stdout carries raw file bytes; errors go to stderr only.
	ActionFileUpload   ActionName = "FILE_UPLOAD"
	ActionFileDownload ActionName = "FILE_DOWNLOAD"

	//--- License Management

	ActionLicenseStatus   ActionName = "LICENSE_GET_STATUS"    // Query license server status
	ActionLicenseFeatures ActionName = "LICENSE_LIST_FEATURES" // Query license features/usage

	// --- Connection Operations

	// ActionConnectionCheck 检查连接在目标节点上的存活状态。
	// 注意：当前底层实现仅针对 VNC 进程（Xvnc/Xtigervnc）通过 /proc 进行进程与 UID 校验；
	// TODO: 后续协议演进时，需扩展支持 RDP/SSH 等其他协议的探活机制。
	ActionConnectionCheck  ActionName = "CONNECTION_CHECK"
	ActionVNCGenerateOTP   ActionName = "VNC_GENERATE_OTP"   // Generate a TurboVNC one-time password for a running VNC session
	ActionVNCControl       ActionName = "VNC_CONTROL"        // Start or stop a VNC server via vncserver commands
	ActionVNCCgroupControl ActionName = "VNC_CGROUP_CONTROL" // Start or stop a VNC server with systemd-run Cgroup slice isolation
)

// Actions holds all registered action specifications for the current platform.
// Platform-specific actions are registered in init() within actions_<platform>.go.
var Actions = make(map[ActionName]ActionSpec)
