package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/settings"
	"design-cloud-engine/internal/server/service/settings/schema"
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

func TestDownloadTicketValidatesRequesterExpiryAndFingerprint(t *testing.T) {
	ctx := context.Background()
	svc, filePath, cleanup := newDownloadTicketTestService(t)
	defer cleanup()

	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	token, err := svc.CreateAllowedDownloadToken(ctx, filePath, "alice")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if !strings.HasPrefix(token.Token, "dl_") || strings.Contains(token.Token, "/") || strings.Contains(token.Token, filePath) {
		t.Fatalf("token exposures unexpected shape: %q", token.Token)
	}

	if _, _, err := svc.GetAllowedDownloadFile(ctx, token.Token, "bob"); !errors.Is(err, ErrInvalidDownloadToken) {
		t.Fatalf("requester mismatch err = %v, want %v", err, ErrInvalidDownloadToken)
	}

	allowedPath, size, err := svc.GetAllowedDownloadFile(ctx, token.Token, "alice")
	if err != nil {
		t.Fatalf("get allowed file: %v", err)
	}
	if allowedPath != filePath || size != 5 {
		t.Fatalf("allowed file = (%q, %d), want (%q, 5)", allowedPath, size, filePath)
	}
	if _, _, err := svc.GetAllowedDownloadFile(ctx, token.Token, "alice"); !errors.Is(err, ErrInvalidDownloadToken) {
		t.Fatalf("reused token err = %v, want %v", err, ErrInvalidDownloadToken)
	}

	changedToken, err := svc.CreateAllowedDownloadToken(ctx, filePath, "alice")
	if err != nil {
		t.Fatalf("create changed file token: %v", err)
	}

	if err := os.WriteFile(filePath, []byte("hello changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GetAllowedDownloadFile(ctx, changedToken.Token, "alice"); !errors.Is(err, ErrDownloadFileChanged) {
		t.Fatalf("changed file err = %v, want %v", err, ErrDownloadFileChanged)
	}
	if _, _, err := svc.GetAllowedDownloadFile(ctx, changedToken.Token, "alice"); !errors.Is(err, ErrInvalidDownloadToken) {
		t.Fatalf("changed file token reuse err = %v, want %v", err, ErrInvalidDownloadToken)
	}

	expiredToken, err := svc.CreateAllowedDownloadToken(ctx, filePath, "alice")
	if err != nil {
		t.Fatalf("create expired token: %v", err)
	}
	svc.downloadTokensMu.Lock()
	entry := svc.downloadTokens[expiredToken.Token]
	entry.ExpiresAt = time.Now().Add(-time.Second)
	svc.downloadTokens[expiredToken.Token] = entry
	svc.downloadTokensMu.Unlock()

	if _, _, err := svc.GetAllowedDownloadFile(ctx, expiredToken.Token, "alice"); !errors.Is(err, ErrInvalidDownloadToken) {
		t.Fatalf("expired token err = %v, want %v", err, ErrInvalidDownloadToken)
	}
}

func TestCreateAllowedDownloadTicketHonorsPermissionMode(t *testing.T) {
	ctx := context.Background()
	svc, filePath, cleanup := newDownloadTicketTestService(t)
	defer cleanup()

	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := setTestDownloadAccess(t, svc, DownloadPermissionRequest); err != nil {
		t.Fatalf("set download access=request: %v", err)
	}
	if _, err := svc.CreateAllowedDownloadToken(ctx, filePath, "alice"); !errors.Is(err, ErrDownloadApprovalRequired) {
		t.Fatalf("create token when request mode err = %v, want %v", err, ErrDownloadApprovalRequired)
	}

	if err := setTestDownloadAccess(t, svc, DownloadPermissionAllow); err != nil {
		t.Fatalf("set download access=allow: %v", err)
	}
	if _, err := svc.CreateAllowedDownloadToken(ctx, filePath, "alice"); err != nil {
		t.Fatalf("create token when allow mode: %v", err)
	}

	if err := setTestDownloadAccess(t, svc, DownloadPermissionDeny); err != nil {
		t.Fatalf("set download access=deny: %v", err)
	}
	if _, err := svc.CreateAllowedDownloadToken(ctx, filePath, "alice"); !errors.Is(err, ErrNoDownloadPermission) {
		t.Fatalf("create token when deny mode err = %v, want %v", err, ErrNoDownloadPermission)
	}
}

func TestSubmitDownloadRequestHonorsPermissionMode(t *testing.T) {
	ctx := context.Background()
	svc, filePath, cleanup := newDownloadTicketTestService(t)
	defer cleanup()

	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := setTestDownloadAccess(t, svc, DownloadPermissionAllow); err != nil {
		t.Fatalf("set download access=allow: %v", err)
	}
	if _, err := svc.SubmitDownloadRequest(ctx, filePath, "alice"); !errors.Is(err, ErrDownloadApprovalNotNeeded) {
		t.Fatalf("submit request when allow mode err = %v, want %v", err, ErrDownloadApprovalNotNeeded)
	}

	if err := setTestDownloadAccess(t, svc, DownloadPermissionRequest); err != nil {
		t.Fatalf("set download access=request: %v", err)
	}
	download, err := svc.SubmitDownloadRequest(ctx, filePath, "alice")
	if err != nil {
		t.Fatalf("submit request when request mode: %v", err)
	}
	if download.Status != model.DownloadStatusPending {
		t.Fatalf("download status = %d, want %d", download.Status, model.DownloadStatusPending)
	}

	if err := setTestDownloadAccess(t, svc, DownloadPermissionDeny); err != nil {
		t.Fatalf("set download access=deny: %v", err)
	}
	if _, err := svc.SubmitDownloadRequest(ctx, filePath, "alice"); !errors.Is(err, ErrNoDownloadPermission) {
		t.Fatalf("submit request when deny mode err = %v, want %v", err, ErrNoDownloadPermission)
	}
}

func TestGetAllowedDownloadFileDeniedAfterTicketIssue(t *testing.T) {
	ctx := context.Background()
	svc, filePath, cleanup := newDownloadTicketTestService(t)
	defer cleanup()

	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := setTestDownloadAccess(t, svc, DownloadPermissionAllow); err != nil {
		t.Fatalf("set download access=allow: %v", err)
	}
	token, err := svc.CreateAllowedDownloadToken(ctx, filePath, "alice")
	if err != nil {
		t.Fatalf("create allowed token: %v", err)
	}

	if err := setTestDownloadAccess(t, svc, DownloadPermissionDeny); err != nil {
		t.Fatalf("set download access=deny: %v", err)
	}
	if _, _, err := svc.GetAllowedDownloadFile(ctx, token.Token, "alice"); !errors.Is(err, ErrNoDownloadPermission) {
		t.Fatalf("get allowed file when deny mode err = %v, want %v", err, ErrNoDownloadPermission)
	}
}

func TestCreateDownloadTokenCapsEntriesByDroppingOldest(t *testing.T) {
	ctx := context.Background()
	svc, _, cleanup := newDownloadTicketTestService(t)
	defer cleanup()

	now := time.Now()
	svc.downloadTokensMu.Lock()
	for i := range maxDownloadTokens {
		tStr := fmt.Sprintf("dl_existing_%d", i)
		svc.downloadTokens[tStr] = downloadTokenEntry{
			Requester: "alice",
			Path:      "/tmp/file",
			CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
			ExpiresAt: now.Add(time.Hour),
		}
	}
	svc.lastTokenCleanup = now
	svc.downloadTokensMu.Unlock()

	token, err := svc.createDownloadToken(ctx, downloadTokenEntry{Requester: "alice", Path: "/tmp/new"})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	svc.downloadTokensMu.Lock()
	defer svc.downloadTokensMu.Unlock()
	if got := len(svc.downloadTokens); got != maxDownloadTokens {
		t.Fatalf("download tokens = %d, want %d", got, maxDownloadTokens)
	}
	if _, ok := svc.downloadTokens[token.Token]; !ok {
		t.Fatalf("new token was not stored")
	}
	if _, ok := svc.downloadTokens["dl_existing_0"]; ok {
		t.Fatalf("oldest token remained after cap pruning")
	}
}

func TestCreateApprovedDownloadTokenDeniedMode(t *testing.T) {
	ctx := context.Background()
	svc, filePath, cleanup := newDownloadTicketTestService(t)
	defer cleanup()

	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := setTestDownloadAccess(t, svc, DownloadPermissionRequest); err != nil {
		t.Fatalf("set download access=request: %v", err)
	}
	requestInfo, err := svc.SubmitDownloadRequest(ctx, filePath, "alice")
	if err != nil {
		t.Fatalf("submit request: %v", err)
	}
	if _, err := svc.ApproveDownloadRequest(ctx, int(requestInfo.ID), "admin"); err != nil {
		t.Fatalf("approve request: %v", err)
	}

	if err := setTestDownloadAccess(t, svc, DownloadPermissionDeny); err != nil {
		t.Fatalf("set download access=deny: %v", err)
	}
	if _, err := svc.CreateApprovedDownloadToken(ctx, int(requestInfo.ID), "alice"); !errors.Is(err, ErrNoDownloadPermission) {
		t.Fatalf("create approved token when deny mode err = %v, want %v", err, ErrNoDownloadPermission)
	}
}

func setTestDownloadAccess(t *testing.T, svc *Service, mode DownloadPermission) error {
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
	return svc.settingsSvc.UpdateByDomain(context.Background(), schema.DomainFile, raw)
}

type downloadTicketTestExecutor struct {
	home string
}

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
	case execution.ActionFileCheck:
		var fileCheck payload.FileCheck
		switch p := req.Payload.(type) {
		case payload.FileCheck:
			fileCheck = p
		case *payload.FileCheck:
			fileCheck = *p
		default:
			return nil, fmt.Errorf("invalid file check payload: %T", req.Payload)
		}
		info, err := os.Stat(fileCheck.Path)
		if err != nil {
			return &execution.ExecuteResponse{Success: false, Stderr: err.Error(), ExitCode: 1}, nil
		}
		realPath, err := filepath.EvalSymlinks(fileCheck.Path)
		if err != nil {
			return &execution.ExecuteResponse{Success: false, Stderr: err.Error(), ExitCode: 1}, nil
		}
		realPath, err = filepath.Abs(realPath)
		if err != nil {
			return nil, err
		}
		result := payload.FileCheckResult{
			Size:     info.Size(),
			RealPath: filepath.Clean(realPath),
			ModTime:  info.ModTime().Unix(),
			ModTimeN: int64(info.ModTime().Nanosecond()),
			IsDir:    info.IsDir(),
		}
		data, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return &execution.ExecuteResponse{Success: true, Stdout: string(data)}, nil
	default:
		return &execution.ExecuteResponse{Success: false, Stderr: "unexpected action", ExitCode: 1}, nil
	}
}

func newDownloadTicketTestService(t *testing.T) (*Service, string, func()) {
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
	settingsSvc, err := settings.NewService(repos.Setting, auditSvc, nil)
	if err != nil {
		_ = store.Close()
		t.Fatalf("settings service: %v", err)
	}
	err = settingsSvc.UpdateByDomain(context.Background(), schema.DomainFile, []byte(`{"enabled": true, "downloadEnabled": true, "uploadEnabled": true, "requiresApproval": false, "downloadApprovalTTL": 86400, "uploadMaxSizeBytes": 8589934592, "locations": []}`))
	if err != nil {
		_ = store.Close()
		t.Fatalf("update file settings: %v", err)
	}
	execSvc := downloadTicketTestExecutor{home: passwdHome}
	userSvc, err := usersvc.NewService(execSvc, usersvc.Config{AuthMode: config.UserAuthModeLocal}, settingsSvc, repos.User, repos.TaskLease, repos.CacheVersion, nil, slog.Default(), auditSvc)
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

	svc := NewService(execSvc, userSvc, settingsSvc, Config{}, repos.File, slog.Default())
	return svc, filepath.Join(passwdHome, "download.txt"), func() {
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
