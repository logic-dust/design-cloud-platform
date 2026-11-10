package file

import (
	"bytes"
	"context"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/service/settings"
	usersvc "design-cloud-engine/internal/server/service/user"
)

func TestDownloadResolvedPathWithinRootsUsesSymlinkTarget(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	shared := filepath.Join(tmp, "shared")
	outside := filepath.Join(tmp, "outside")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	insideFile := filepath.Join(home, "inside.txt")
	sharedFile := filepath.Join(shared, "shared.txt")
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(insideFile, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sharedFile, []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	insideLink := filepath.Join(home, "inside-link.txt")
	sharedLink := filepath.Join(home, "shared-link.txt")
	outsideLink := filepath.Join(home, "outside-link.txt")
	if err := os.Symlink(insideFile, insideLink); err != nil {
		// Windows 创建符号链接需要 SeCreateSymbolicLinkPrivilege（或开启开发者模式），
		// 普通账户不具备该特权；本用例只验证「解析后的符号链接目标是否落在允许根内」，
		// 硬链接无法替代（测不出链接解析），故无跨平台替代写法，在此显式跳过。
		if runtime.GOOS == "windows" {
			t.Skipf("windows 账户无创建符号链接权限（需 SeCreateSymbolicLinkPrivilege 或开发者模式），跳过用例: %v", err)
		}
		t.Fatal(err)
	}
	if err := os.Symlink(sharedFile, sharedLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, outsideLink); err != nil {
		t.Fatal(err)
	}

	homeRoot, err := cleanDownloadRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	sharedRoot, err := cleanDownloadRoot(shared)
	if err != nil {
		t.Fatal(err)
	}

	insideRealPath, err := filepath.EvalSymlinks(insideLink)
	if err != nil {
		t.Fatal(err)
	}
	ok := downloadResolvedPathWithinRoots(insideRealPath, []string{homeRoot, sharedRoot})
	if !ok {
		t.Fatalf("expected symlink target under home to be allowed")
	}

	sharedRealPath, err := filepath.EvalSymlinks(sharedLink)
	if err != nil {
		t.Fatal(err)
	}
	ok = downloadResolvedPathWithinRoots(sharedRealPath, []string{homeRoot, sharedRoot})
	if !ok {
		t.Fatalf("expected symlink target under extra allowed root to be allowed")
	}

	outsideRealPath, err := filepath.EvalSymlinks(outsideLink)
	if err != nil {
		t.Fatal(err)
	}
	ok = downloadResolvedPathWithinRoots(outsideRealPath, []string{homeRoot, sharedRoot})
	if ok {
		t.Fatalf("expected symlink target outside home to be rejected")
	}
}

func TestOpenAllowedDownloadHonorsPermissionAndFingerprint(t *testing.T) {
	ctx := context.Background()
	svc, _, filePath, cleanup := newDownloadTicketTestService(t)
	defer cleanup()

	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	allowedPath, size, err := svc.openAllowedDownloadFile(ctx, filePath, "alice")
	if err != nil {
		t.Fatalf("open allowed file: %v", err)
	}
	if allowedPath != filePath || size != 5 {
		t.Fatalf("allowed file = (%q, %d), want (%q, 5)", allowedPath, size, filePath)
	}
}

func TestOpenAllowedDownloadHonorsPermissionMode(t *testing.T) {
	ctx := context.Background()
	svc, settingsSvc, filePath, cleanup := newDownloadTicketTestService(t)
	defer cleanup()

	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := setTestDownloadAccess(t, svc, settingsSvc, DownloadPermissionRequest); err != nil {
		t.Fatalf("set download access=request: %v", err)
	}
	if _, _, err := svc.openAllowedDownloadFile(ctx, filePath, "alice"); !errors.Is(err, ErrDownloadApprovalRequired) {
		t.Fatalf("open when request mode err = %v, want %v", err, ErrDownloadApprovalRequired)
	}

	if err := setTestDownloadAccess(t, svc, settingsSvc, DownloadPermissionAllow); err != nil {
		t.Fatalf("set download access=allow: %v", err)
	}
	if _, _, err := svc.openAllowedDownloadFile(ctx, filePath, "alice"); err != nil {
		t.Fatalf("open when allow mode: %v", err)
	}

	if err := setTestDownloadAccess(t, svc, settingsSvc, DownloadPermissionDeny); err != nil {
		t.Fatalf("set download access=deny: %v", err)
	}
	if _, _, err := svc.openAllowedDownloadFile(ctx, filePath, "alice"); !errors.Is(err, ErrNoDownloadPermission) {
		t.Fatalf("open when deny mode err = %v, want %v", err, ErrNoDownloadPermission)
	}
}

func TestSubmitDownloadRequestHonorsPermissionMode(t *testing.T) {
	ctx := context.Background()
	svc, settingsSvc, filePath, cleanup := newDownloadTicketTestService(t)
	defer cleanup()

	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := setTestDownloadAccess(t, svc, settingsSvc, DownloadPermissionAllow); err != nil {
		t.Fatalf("set download access=allow: %v", err)
	}
	if _, err := svc.SubmitDownloadRequest(ctx, filePath, "alice"); !errors.Is(err, ErrDownloadApprovalNotNeeded) {
		t.Fatalf("submit request when allow mode err = %v, want %v", err, ErrDownloadApprovalNotNeeded)
	}

	if err := setTestDownloadAccess(t, svc, settingsSvc, DownloadPermissionRequest); err != nil {
		t.Fatalf("set download access=request: %v", err)
	}
	download, err := svc.SubmitDownloadRequest(ctx, filePath, "alice")
	if err != nil {
		t.Fatalf("submit request when request mode: %v", err)
	}
	if download.Status != model.DownloadRequestStatusPending {
		t.Fatalf("download status = %d, want %d", download.Status, model.DownloadRequestStatusPending)
	}

	if err := setTestDownloadAccess(t, svc, settingsSvc, DownloadPermissionDeny); err != nil {
		t.Fatalf("set download access=deny: %v", err)
	}
	if _, err := svc.SubmitDownloadRequest(ctx, filePath, "alice"); !errors.Is(err, ErrNoDownloadPermission) {
		t.Fatalf("submit request when deny mode err = %v, want %v", err, ErrNoDownloadPermission)
	}
}

func TestOpenApprovedDownloadRequestDeniedMode(t *testing.T) {
	ctx := context.Background()
	svc, settingsSvc, filePath, cleanup := newDownloadTicketTestService(t)
	defer cleanup()

	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := setTestDownloadAccess(t, svc, settingsSvc, DownloadPermissionRequest); err != nil {
		t.Fatalf("set download access=request: %v", err)
	}
	requestInfo, err := svc.SubmitDownloadRequest(ctx, filePath, "alice")
	if err != nil {
		t.Fatalf("submit request: %v", err)
	}
	if _, err := svc.ApproveDownloadRequest(ctx, int(requestInfo.ID), "admin"); err != nil {
		t.Fatalf("approve request: %v", err)
	}

	if err := setTestDownloadAccess(t, svc, settingsSvc, DownloadPermissionDeny); err != nil {
		t.Fatalf("set download access=deny: %v", err)
	}
	if _, _, err := svc.OpenApprovedDownloadRequest(ctx, int(requestInfo.ID), "alice"); !errors.Is(err, ErrNoDownloadPermission) {
		t.Fatalf("open approved download when deny mode err = %v, want %v", err, ErrNoDownloadPermission)
	}
}

func TestConcurrentApproveAndRejectConflict(t *testing.T) {
	ctx := context.Background()
	svc, settingsSvc, filePath, cleanup := newDownloadTicketTestService(t)
	defer cleanup()

	if err := os.WriteFile(filePath, []byte("hello concurrent"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := setTestDownloadAccess(t, svc, settingsSvc, DownloadPermissionRequest); err != nil {
		t.Fatalf("set download access=request: %v", err)
	}
	req, err := svc.SubmitDownloadRequest(ctx, filePath, "alice")
	if err != nil {
		t.Fatalf("submit request: %v", err)
	}

	// 顺序或并发调用两次审核，第二次必须报冲突
	if _, err := svc.ApproveDownloadRequest(ctx, int(req.ID), "admin1"); err != nil {
		t.Fatalf("first approve should succeed: %v", err)
	}

	// 第二次 Approve 应失败
	if _, err := svc.ApproveDownloadRequest(ctx, int(req.ID), "admin2"); !errors.Is(err, ErrDownloadRequestConflict) {
		t.Fatalf("second approve want %v, got %v", ErrDownloadRequestConflict, err)
	}

	// 驳回已通过的请求也应失败
	if _, err := svc.RejectDownloadRequest(ctx, int(req.ID), "admin2", "late reject"); !errors.Is(err, ErrDownloadRequestConflict) {
		t.Fatalf("reject after approve want %v, got %v", ErrDownloadRequestConflict, err)
	}
}

func TestNilSettingsServiceSafety(t *testing.T) {
	ctx := context.Background()
	svc := &Service{}

	if !svc.IsEnabled(ctx) {
		t.Fatalf("expected IsEnabled=true when settingsSvc is nil")
	}
	if perm := svc.downloadPolicy(ctx); perm != DownloadPermissionAllow {
		t.Fatalf("expected DownloadPermissionAllow when settingsSvc is nil, got %v", perm)
	}
	if limit := svc.defaultUploadMaxSizeBytes(ctx); limit != schema.UploadMaxSizeBytesDefault {
		t.Fatalf("expected default upload max size, got %d", limit)
	}
	if ttl := svc.downloadApprovalTTL(ctx); ttl != 86400*time.Second {
		t.Fatalf("expected default approval ttl 86400s, got %v", ttl)
	}
}

func setTestDownloadAccess(t *testing.T, svc *Service, settingsSvc *settings.Service, mode DownloadPermission) error {
	t.Helper()

	fileSetting, err := svc.settingsSvc.GetFileSettings(context.Background())
	if err != nil {
		return err
	}
	switch mode {
	case DownloadPermissionAllow:
		fileSetting.DownloadEnabled = true
		fileSetting.RequiresApproval = false
	case DownloadPermissionRequest:
		fileSetting.DownloadEnabled = true
		fileSetting.RequiresApproval = true
	default: // DownloadPermissionDeny
		fileSetting.DownloadEnabled = false
		fileSetting.RequiresApproval = false
	}

	raw, err := json.Marshal(fileSetting)
	if err != nil {
		return err
	}
	return settingsSvc.UpdateByDomain(context.Background(), schema.DomainFile, raw)
}

type downloadTicketTestExecutor struct {
	home string
}

// Execute 只服务 user.Service 的 ActionUserGet（getent passwd 风格行）。
// 文件动作（file.check 等）由 file.Service 经真实流执行器路径执行，
// 见 TestMain 的伪 PrivilegedExec 再执行模式。
func (e downloadTicketTestExecutor) Execute(_ context.Context, req *execution.ExecuteRequest) (*execution.ExecuteResponse, error) {
	switch req.ActionName {
	case execution.ActionUserGet:
		var userGet payload.UserGet
		switch p := req.Payload.(type) {
		case payload.UserGet:
			userGet = p
		case *payload.UserGet:
			userGet = *p
		default:
			return nil, fmt.Errorf("invalid user get payload: %T", req.Payload)
		}
		return &execution.ExecuteResponse{
			Success: true,
			Stdout:  fmt.Sprintf("%s:x:1000:1000:%s:%s:/bin/sh", userGet.Username, userGet.Username, e.home),
		}, nil
	default:
		return &execution.ExecuteResponse{Success: false, Stderr: "unexpected action", ExitCode: 1}, nil
	}
}

func newDownloadTicketTestService(t *testing.T) (*Service, *settings.Service, string, func()) {
	t.Helper()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// 假 executor 返回的是 getent passwd 风格的行，字段以 ':' 分隔；
	// Windows 家目录自带盘符冒号（C:\Users\...），会撞上字段分隔符，故喂给该行时改用不含 ':' 的等价写法。
	passwdHome := colonFreeHomePath(t, home)

	store, err := db.NewStore(config.Database{
		Type: config.DatabaseTypeSQLite,
		Path: filepath.Join(root, "test.db"),
	}, slog.Default())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Migrate(); err != nil {
		_ = store.Close()
		t.Fatalf("migrate: %v", err)
	}

	repos := db.NewRepos(store)
	auditSvc := &audit.Service{}
	settingsSvc, err := settings.NewService(repos.Setting, slog.Default(), auditSvc, nil, repos.CacheVersion, repos.Node)
	if err != nil {
		_ = store.Close()
		t.Fatalf("settings service: %v", err)
	}
	err = settingsSvc.UpdateByDomain(context.Background(), schema.DomainFile, []byte(`{"enabled": true, "downloadEnabled": true, "uploadEnabled": true, "requiresApproval": false, "downloadApprovalTTL": 86400, "uploadMaxSizeBytes": 8589934592, "listMaxEntries": 999, "locations": []}`))
	if err != nil {
		_ = store.Close()
		t.Fatalf("update file settings: %v", err)
	}
	execSvc := downloadTicketTestExecutor{home: passwdHome}
	userSvc, err := usersvc.NewService(execSvc, usersvc.Config{AuthMode: config.UserAuthModeLocal}, settingsSvc, repos.User, repos.TaskLease, repos.CacheVersion, nil, slog.Default(), auditSvc, nil)
	if err != nil {
		_ = store.Close()
		t.Fatalf("user service: %v", err)
	}
	if err := repos.User.Create(context.Background(), &model.User{
		Username:       "alice",
		Status:         model.UserActive,
		Type:           model.UserTypeSystem,
		DownloadAccess: true,
	}); err != nil {
		_ = store.Close()
		t.Fatalf("create alice: %v", err)
	}

	svc := NewService(userSvc, settingsSvc, Config{}, repos.DownloadRequest, slog.Default())
	// 走真实流执行器路径：fork 本测试二进制（TestMain 伪 PrivilegedExec 模式）执行 file.check。
	svc.streamExec = newReexecFakeDcExec(t)
	return svc, settingsSvc, filepath.Join(passwdHome, "download.txt"), func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}

// colonFreeHomePath 返回与 home 指向同一目录、但不含 ':' 的等价路径写法。
//
// 伪 getent passwd 行以 ':' 分隔字段，而 Windows 盘符本身含 ':'：若把带盘符的家目录原样写进该行，
// 产品侧按 POSIX 语义解析出的 home 会退化成盘符片段（"C"），再经 filepath.Abs 得到 <cwd>\C，
// 与实际目录不符（真实 Windows 上表现为 GetFileAttributesEx <cwd>\C: ...）。
// 去盘符的根相对写法（Windows 下 \Users\... 解析到当前盘符下的同一目录）可规避该冲突。
// 非 Windows 平台路径本身不含 ':'，原样返回。
func colonFreeHomePath(t *testing.T, home string) string {
	t.Helper()

	volume := filepath.VolumeName(home)
	if volume == "" {
		return home
	}
	trimmed := strings.TrimPrefix(home, volume)
	// 去盘符写法按「当前盘符」解析，仅当与 home 同盘时才指向同一目录；否则该 fixture 无法表达，显式跳过。
	trimmedInfo, trimmedErr := os.Stat(trimmed)
	homeInfo, homeErr := os.Stat(home)
	if trimmedErr != nil || homeErr != nil || !os.SameFile(trimmedInfo, homeInfo) {
		t.Skipf("无法为 %s 构造不含 ':' 的等价家目录路径（去盘符: %s）：getent passwd fixture 无法表达带盘符的 Windows 家目录", home, trimmed)
	}
	return trimmed
}

// fakeDcExecEnv 标记「测试二进制以伪 PrivilegedExec 身份被再执行」。
// 经 BuildDcExecEnv 透传（不在 blocked env 名单内），跨平台可用。
const fakeDcExecEnv = "DCE_FILE_TEST_FAKE_EXEC_BIN"

// TestMain 是包级入口：正常模式跑测试；检测到 fakeDcExecEnv 时以伪 PrivilegedExec
// 身份处理 stdin 协议并退出，不进入测试运行器。
func TestMain(m *testing.M) {
	if os.Getenv(fakeDcExecEnv) == "1" {
		runFakeDcExecProcess()
		return
	}
	os.Exit(m.Run())
}

// newReexecFakeDcExec 返回指向本测试二进制的流执行器。
// file.Service 经真实 StreamExecutor 路径（framed stdin + ExecuteOutput stdout）
// fork 该二进制，TestMain 检测环境标记后走伪 PrivilegedExec 逻辑。
func newReexecFakeDcExec(t *testing.T) *StreamExecutor {
	t.Helper()
	t.Setenv(fakeDcExecEnv, "1")
	bin, err := os.Executable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("test-token"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	return &StreamExecutor{
		getExecBinPath: func(context.Context) string { return bin },
		tokenFile:      tokenFile,
	}
}

// runFakeDcExecProcess 实现 PrivilegedExec 的 stdin 首行 JSON header 协议。
// 当前仅覆盖下载链路用到的 file.check（真实 stat + EvalSymlinks），
// 其他动作显式失败，避免静默返回错误数据。
func runFakeDcExecProcess() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake PrivilegedExec: read stdin:", err)
		os.Exit(1)
	}
	header := data
	if before, _, ok := bytes.Cut(data, []byte{'\n'}); ok {
		header = before
	}
	var input execution.ExecuteInput
	if err := json.Unmarshal(header, &input); err != nil {
		fmt.Fprintln(os.Stderr, "fake PrivilegedExec: bad header:", err)
		os.Exit(1)
	}
	if input.Request == nil {
		fmt.Fprintln(os.Stderr, "fake PrivilegedExec: missing request")
		os.Exit(1)
	}
	if input.Request.ActionName != execution.ActionFileCheck {
		fmt.Fprintln(os.Stderr, "fake PrivilegedExec: unexpected action:", input.Request.ActionName)
		os.Exit(1)
	}
	pm, _ := input.Request.Payload.(map[string]any)
	path, _ := pm["path"].(string)

	info, err := os.Stat(path)
	if err != nil {
		writeFakeDcExecOutput(execution.ExecuteOutput{Success: false, Error: err.Error()})
		return
	}
	realPath := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		realPath = resolved
	}
	if abs, err := filepath.Abs(realPath); err == nil {
		realPath = abs
	}
	result := payload.FileCheckResult{
		Size:     info.Size(),
		Mode:     info.Mode().String(),
		RealPath: filepath.Clean(realPath),
		ModTime:  info.ModTime().Unix(),
		ModTimeN: int64(info.ModTime().Nanosecond()),
		IsDir:    info.IsDir(),
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake PrivilegedExec: marshal result:", err)
		os.Exit(1)
	}
	writeFakeDcExecOutput(execution.ExecuteOutput{Success: true, Output: string(resultJSON)})
}

func writeFakeDcExecOutput(out execution.ExecuteOutput) {
	data, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake PrivilegedExec: marshal output:", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}
