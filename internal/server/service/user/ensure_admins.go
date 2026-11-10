package user

import (
	"errors"
	"fmt"

	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"

	"gorm.io/gorm"
)

// EnsureAdminUsers creates default admin accounts if they do not already exist.
// No initial password is stored; use `dc-engine user set-password` before first login.
// All four built-in names are ensured; role exclusivity is enforced on server start via InitAdminAccounts.
func EnsureAdminUsers(store *db.Store) error {
	for _, a := range model.AllBuiltinAdminAccounts() {
		var existing model.User
		// 按具体账号名（而非角色）判断存在性：我们必须保证文档约定的引导账号存在。
		// 若按角色查询，当该角色已被另一个账号（如迁移/手工创建的管理员）占用时，
		// 会错误跳过创建，导致约定账号始终未生成。按 username 查询则每个命名账号独立确认。
		result := store.DB().Where("username = ?", a.Username).First(&existing)
		if result.Error == nil {
			// Already exists, skip.
			continue
		}
		if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("check admin user %q: %w", a.Username, result.Error)
		}

		user := model.NewBuiltinAdminUser(a)
		if err := store.DB().Create(&user).Error; err != nil {
			return fmt.Errorf("create admin user %q: %w", a.Username, err)
		}

		fmt.Printf("[bootstrap] Created admin user %q (role=%s). Set password: dc-engine user set-password --username %s\n",
			a.Username, a.Role.String(), a.Username)
	}
	return nil
}
