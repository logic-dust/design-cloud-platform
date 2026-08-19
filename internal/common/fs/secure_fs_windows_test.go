package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// grantEveryoneAccess applies an Everyone ACE with the given mask (test helper only).
func grantEveryoneAccess(t *testing.T, path string, mask windows.ACCESS_MASK) {
	t.Helper()

	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatalf("build Everyone SID: %v", err)
	}

	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: mask,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValue(unsafe.Pointer(everyone)),
			},
		},
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatalf("build Everyone ACL: %v", err)
	}
	defer runtime.KeepAlive(acl)

	namePtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("encode path: %v", err)
	}
	if r1, _, _ := procSetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(namePtr)),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
		0, 0,
		uintptr(unsafe.Pointer(acl)),
		0,
	); r1 != 0 {
		t.Fatalf("apply Everyone ACL: %v", windows.Errno(r1))
	}
}

// Windows 侧的私有性由 Owner + DACL 保证：硬化后的目录/文件必须通过校验，
// 而被授予 Everyone 写/只读权限或错误 Owner 的路径必须被拒绝。
func TestPrivateACL_Windows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := MkdirPrivate(dir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	if err := VerifyPrivateDir(dir); err != nil {
		t.Fatalf("硬化后的目录必须通过 ACL 校验: %v", err)
	}

	path := filepath.Join(dir, "secret")
	if err := WritePrivateFile(path, []byte("payload")); err != nil {
		t.Fatalf("WritePrivateFile: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open private file: %v", err)
	}
	defer func() { _ = f.Close() }()
	if err := VerifyPrivateFileDescriptor(f); err != nil {
		t.Fatalf("硬化后的文件必须通过 ACL 校验: %v", err)
	}

	// 反向用例：Everyone 可写必须被判定为违规。
	grantEveryoneAccess(t, path, windows.GENERIC_READ|windows.GENERIC_WRITE)
	if err := VerifyPathACL(path); err == nil {
		t.Error("expected a path with an Everyone-write ACE to be rejected")
	} else {
		t.Logf("拒绝原因 (write): %v", err)
	}

	// 重新硬化后，Everyone 只读也必须被拒绝（私密文件不允许非受信读）。
	if err := HardenPath(path, false); err != nil {
		t.Fatalf("re-harden: %v", err)
	}
	grantEveryoneAccess(t, path, windows.GENERIC_READ)
	if err := VerifyPathACL(path); err == nil {
		t.Error("expected a path with an Everyone-read ACE to be rejected")
	} else {
		t.Logf("拒绝原因 (read): %v", err)
	}
}

func TestVerifyPathACL_RejectsUntrustedOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "owned")
	if err := MkdirPrivate(dir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	path := filepath.Join(dir, "secret")
	if err := WritePrivateFile(path, []byte("payload")); err != nil {
		t.Fatalf("WritePrivateFile: %v", err)
	}

	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatalf("build Everyone SID: %v", err)
	}
	// Taking ownership as Everyone may require SeTakeOwnershipPrivilege; if the
	// platform rejects the transfer, skip rather than false-fail on restricted CI.
	namePtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("encode path: %v", err)
	}
	if r1, _, _ := procSetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(namePtr)),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION,
		uintptr(unsafe.Pointer(everyone)),
		0, 0, 0,
	); r1 != 0 {
		t.Skipf("cannot reassign owner to Everyone in this environment: %v", windows.Errno(r1))
	}

	if err := VerifyPathACL(path); err == nil {
		t.Error("expected a path with an untrusted Owner to be rejected")
	} else {
		t.Logf("拒绝原因 (owner): %v", err)
	}
}

func TestHardenPath_TakesOwnership(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "take-owner")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	self, err := currentUserSID()
	if err != nil {
		t.Fatalf("currentUserSID: %v", err)
	}
	if err := HardenPath(path, false); err != nil {
		t.Fatalf("HardenPath: %v", err)
	}
	if err := VerifyPathACL(path); err != nil {
		t.Fatalf("hardened path must pass VerifyPathACL: %v", err)
	}

	// Owner must be the current account after HardenPath.
	namePtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var ownerSID *windows.SID
	var sd uintptr
	if r1, _, _ := procGetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(namePtr)),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION,
		uintptr(unsafe.Pointer(&ownerSID)),
		0, 0, 0,
		uintptr(unsafe.Pointer(&sd)),
	); r1 != 0 {
		t.Fatalf("GetNamedSecurityInfo: %v", windows.Errno(r1))
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(sd)) }()
	if ownerSID == nil || !ownerSID.Equals(self) {
		t.Fatalf("owner = %v, want current process SID %v", ownerSID, self)
	}
}

// 确保测试辅助路径不会因为路径拼接方式而误判（回归保护）。
func TestVerifyPathACL_MissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if err := VerifyPathACL(missing); err == nil {
		t.Errorf("expected an error for a missing path, got nil")
	} else {
		t.Logf("缺失路径报错: %v", fmt.Sprint(err))
	}
}

// 公共资产在 Windows 上只做创建与写入，不做 ACL 收紧：目录与文件均应成功创建。
func TestSharedAssets_Windows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cluster")
	if err := MkdirShared(dir); err != nil {
		t.Fatalf("MkdirShared: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("shared dir must exist: %v", err)
	}

	path := filepath.Join(dir, "ca.crt")
	if err := WriteSharedFile(path, []byte("cert")); err != nil {
		t.Fatalf("WriteSharedFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared file: %v", err)
	}
	if string(data) != "cert" {
		t.Fatalf("shared file content = %q, want %q", data, "cert")
	}
}
