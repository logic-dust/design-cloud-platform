package fs

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows 侧的安全约束（POSIX 权限位在 Windows 无语义的替代物）：
// 只有 SYSTEM、Administrators 与当前进程账户允许持有读/写/控制权限，且不允许从父目录继承。
// HardenPath 同时把对象 Owner 转移给当前进程账户，避免低权限账户预先创建数据目录后保留所有权。
//
// x/sys/windows 只暴露 GetNamedSecurityInfo，且 SECURITY_DESCRIPTOR 字段全部私有，
// 既不能枚举 ACE 也不能写 DACL，因此这里直接绑定 advapi32 的必要入口（无第三方依赖）。

var (
	advapi32Dll = windows.NewLazySystemDLL("advapi32.dll")

	procGetNamedSecurityInfoW     = advapi32Dll.NewProc("GetNamedSecurityInfoW")
	procSetNamedSecurityInfoW     = advapi32Dll.NewProc("SetNamedSecurityInfoW")
	procGetSecurityDescriptorDacl = advapi32Dll.NewProc("GetSecurityDescriptorDacl")
	procGetAclInformation         = advapi32Dll.NewProc("GetAclInformation")
	procGetAce                    = advapi32Dll.NewProc("GetAce")
)

// aclSizeInformation 对应 Win32 ACL_INFORMATION_CLASS :: AclSizeInformation。
const aclSizeInformation = 2

// aclSizeInfo 对应 Win32 ACL_SIZE_INFORMATION。
type aclSizeInfo struct {
	AceCount      uint32
	AclBytesInUse uint32
	AclBytesFree  uint32
}

// fileDeleteChild 是 FILE_DELETE_CHILD（0x40），x/sys 未导出该常量。
const fileDeleteChild = 0x40

// privateAccessMask covers read, write and control rights that a private file must not
// grant to untrusted principals. Matching any bit is treated as an ACL violation.
const privateAccessMask = windows.GENERIC_ALL | windows.GENERIC_READ | windows.GENERIC_WRITE |
	windows.GENERIC_EXECUTE | windows.READ_CONTROL | windows.WRITE_DAC | windows.WRITE_OWNER |
	windows.DELETE | windows.FILE_READ_DATA | windows.FILE_READ_EA | windows.FILE_READ_ATTRIBUTES |
	windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA |
	windows.FILE_WRITE_ATTRIBUTES | windows.FILE_EXECUTE | fileDeleteChild

// isTrustedPrincipal reports whether sid may hold private-file access or ownership.
func isTrustedPrincipal(sid *windows.SID, selfSid *windows.SID) bool {
	if sid == nil {
		return false
	}
	if selfSid != nil && sid.Equals(selfSid) {
		return true
	}
	return sid.IsWellKnown(windows.WinLocalSystemSid) ||
		sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) ||
		sid.IsWellKnown(windows.WinCreatorOwnerSid)
}

// currentUserSID returns the SID of the account this process runs as, i.e. the Windows
// equivalent of the file owner that keeps access under the Linux 0600/0700 model.
func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	if user.User.Sid == nil {
		return nil, fmt.Errorf("current process token has no user SID")
	}
	return user.User.Sid, nil
}

// VerifyPathACL rejects a path whose DACL grants read/write/control access to any principal
// other than SYSTEM / Administrators / CREATOR OWNER / the current process account, or whose
// Owner is not one of those trusted principals. This is the Windows analogue of the Linux
// 0600/0700 owner+mode check: a missing DACL means "everyone has full control" and is
// therefore rejected as well.
func VerifyPathACL(path string) error {
	selfSid, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("resolve current account SID: %w", err)
	}

	namePtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode path %s: %w", path, err)
	}

	var ownerSID *windows.SID
	var sd uintptr
	if r1, _, _ := procGetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(namePtr)),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
		uintptr(unsafe.Pointer(&ownerSID)),
		0, 0, 0,
		uintptr(unsafe.Pointer(&sd)),
	); r1 != 0 {
		return fmt.Errorf("read security descriptor of %s: %w", path, windows.Errno(r1))
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(sd)) }()

	if ownerSID == nil {
		return fmt.Errorf("security violation: %s has no owner SID", path)
	}
	if !isTrustedPrincipal(ownerSID, selfSid) {
		return fmt.Errorf("security violation: %s owner %s is not a trusted principal", path, ownerSID.String())
	}

	var present, defaulted int32
	var acl uintptr
	if r1, _, _ := procGetSecurityDescriptorDacl.Call(
		sd,
		uintptr(unsafe.Pointer(&present)),
		uintptr(unsafe.Pointer(&acl)),
		uintptr(unsafe.Pointer(&defaulted)),
	); r1 == 0 {
		return fmt.Errorf("read DACL of %s: %w", path, windows.Errno(r1))
	}
	if present == 0 || acl == 0 {
		return fmt.Errorf("security violation: %s has no DACL (everyone keeps full control)", path)
	}

	var size aclSizeInfo
	if r1, _, _ := procGetAclInformation.Call(
		acl,
		uintptr(unsafe.Pointer(&size)),
		unsafe.Sizeof(size),
		aclSizeInformation,
	); r1 == 0 {
		return fmt.Errorf("read ACL information of %s: %w", path, windows.Errno(r1))
	}

	for i := uint32(0); i < size.AceCount; i++ {
		// ACE 内存由 GetNamedSecurityInfoW 分配的堆块持有（defer 中 LocalFree），
		// 全程以 unsafe.Pointer 传递，避免 uintptr 往返触发 vet 的 unsafe 警告。
		var acePtr unsafe.Pointer
		if r1, _, _ := procGetAce.Call(acl, uintptr(i), uintptr(unsafe.Pointer(&acePtr))); r1 == 0 {
			return fmt.Errorf("read ACE #%d of %s: %w", i, path, windows.Errno(r1))
		}

		header := (*windows.ACE_HEADER)(acePtr)
		if header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue // deny ACE 只会更严格；其他类型不授予访问
		}

		ace := (*windows.ACCESS_ALLOWED_ACE)(acePtr)
		if ace.Mask&privateAccessMask == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if isTrustedPrincipal(sid, selfSid) {
			continue
		}
		return fmt.Errorf("security violation: %s grants private access to %s", path, sid.String())
	}

	return nil
}

// HardenPath rewrites the Owner and DACL of path so that only SYSTEM, Administrators and
// the account this process runs as keep full control, cuts off inheritance from the parent
// directory (PROTECTED_DACL), and transfers ownership to the current process account.
// The running account must stay in the DACL: otherwise a service running under a dedicated
// non-admin account would lose access to its own runtime files — the exact analogue of
// Linux owner-0600, where the owner keeps access.
// Directories pass the ACEs down to their children; files do not inherit further.
func HardenPath(path string, isDir bool) error {
	selfSid, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("resolve current account SID: %w", err)
	}
	systemSid, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("build SYSTEM SID: %w", err)
	}
	adminSid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("build Administrators SID: %w", err)
	}

	inheritance := uint32(windows.NO_INHERITANCE)
	if isDir {
		inheritance = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}

	trustees := []struct {
		sid  *windows.SID
		kind windows.TRUSTEE_TYPE
	}{
		{systemSid, windows.TRUSTEE_IS_USER},
		{adminSid, windows.TRUSTEE_IS_GROUP},
		{selfSid, windows.TRUSTEE_IS_USER},
	}

	entries := make([]windows.EXPLICIT_ACCESS, 0, len(trustees))
	seen := make(map[string]struct{}, len(trustees))
	for _, trustee := range trustees {
		key := trustee.sid.String()
		if _, dup := seen[key]; dup {
			continue // SYSTEM / Administrators 运行服务时与本进程账户可能是同一个 SID
		}
		seen[key] = struct{}{}

		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  trustee.kind,
				TrusteeValue: windows.TrusteeValue(unsafe.Pointer(trustee.sid)),
			},
		})
	}

	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build DACL for %s: %w", path, err)
	}
	defer runtime.KeepAlive(acl)

	namePtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode path %s: %w", path, err)
	}

	if r1, _, _ := procSetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(namePtr)),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		uintptr(unsafe.Pointer(selfSid)),
		0,
		uintptr(unsafe.Pointer(acl)),
		0,
	); r1 != 0 {
		return fmt.Errorf("apply owner and DACL to %s: %w", path, windows.Errno(r1))
	}

	return nil
}
