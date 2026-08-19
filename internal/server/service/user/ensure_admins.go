package user

import (
	"errors"
	"fmt"
	"os"

	"design-cloud-engine/internal/common/crypto"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// EnsureAdminUsers creates default admin accounts if they do not already exist.
// Passwords are randomly generated per instance and printed to the startup log.
// Initial password is only valid for the first login (status: UserPasswordExpired).
//
// enableThreeRoles=true: three-admin mode (securityadmin/systemadmin/auditadmin).
// enableThreeRoles=false: single-admin mode (superadmin).
func EnsureAdminUsers(store *db.Store, enableThreeRoles bool) error {
	type adminAccount struct {
		name string
		role model.Role
	}

	var accounts []adminAccount
	if enableThreeRoles {
		accounts = []adminAccount{
			{name: "securityadmin", role: model.RoleSecurityAdmin},
			{name: "systemadmin", role: model.RoleSystemAdmin},
			{name: "auditadmin", role: model.RoleAuditAdmin},
		}
	} else {
		accounts = []adminAccount{
			{name: "dceadmin", role: model.RoleSuperAdmin},
		}
	}

	for _, a := range accounts {
		var existing model.User
		// 按具体账号名（而非角色）判断存在性：我们必须保证文档约定的引导账号存在。
		// 若按角色查询，当该角色已被另一个账号（如迁移/手工创建的管理员）占用时，
		// 会错误跳过创建，导致约定账号始终未生成、却仍打印（从未创建的）口令，
		// 形成可用性/误导缺口。按 username 查询则每个命名账号独立确认。
		result := store.DB().Where("username = ?", a.name).First(&existing)
		if result.Error == nil {
			// Already exists, skip.
			continue
		}
		if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("check admin user %q: %w", a.name, result.Error)
		}

		rawPwd := crypto.GenSecret(16)
		hashed, err := bcrypt.GenerateFromPassword([]byte(rawPwd), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash password for %q: %w", a.name, err)
		}

		user := model.User{
			Username: a.name,
			Password: string(hashed),
			Role:     a.role,
			Status:   model.UserPasswordExpired, // force password change on first login
			Type:     model.UserTypeApp,
		}
		if err := store.DB().Create(&user).Error; err != nil {
			return fmt.Errorf("create admin user %q: %w", a.name, err)
		}

		// 临时口令为随机值，仅以 bcrypt 哈希存储。生产环境绝不回显明文口令，
		// 仅在 dev 主机（DCE_DEV=true）打印以方便本地启动；无论何种环境，
		// 运维均须在首次登录时修改该口令。
		if os.Getenv("DCE_DEV") == "true" {
			fmt.Printf("[bootstrap] Created admin user %q (role=%s) with temporary password: %s\n", a.name, a.role.String(), rawPwd)
		} else {
			fmt.Printf("[bootstrap] Created admin user %q (role=%s). Temporary password is random and NOT echoed; reset it via admin tooling before first login.\n", a.name, a.role.String())
		}
	}
	return nil
}
