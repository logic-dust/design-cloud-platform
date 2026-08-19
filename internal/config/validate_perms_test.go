//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 本文件收纳依赖 POSIX 权限位语义的断言：Windows 上 os.FileMode 的权限位是合成值
// （可写文件恒为 0666），无法表达 0640/0644/0200 这类区别，断言必然失真。产品侧在
// Windows 由 validate_perms_windows.go 走 ACL 校验（VerifyPathACL/HardenPath），
// 所以这里只在非 Windows 平台断言 Unix 权限位。

func TestDatabase_Validate_PasswordFileTooPermissive(t *testing.T) {
	permFile := filepath.Join(t.TempDir(), "loose-dbpwd")
	require.NoError(t, os.WriteFile(permFile, []byte("secret"), 0644))
	db := Database{
		Type:            DatabaseTypePostgres,
		PasswordFile:    permFile,
		MaxOpenConns:    10,
		ConnMaxLifetime: 0,
		MaxIdleConns:    5,
	}
	err := db.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not grant any permissions to group or others")
}

func TestCheckSecretFilePermissions_POSIXModes(t *testing.T) {
	t.Run("group-readable file (0640)", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "secret")
		require.NoError(t, os.WriteFile(file, []byte("secret"), 0640))
		err := checkSecretFilePermissions("test_secret", file)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not grant any permissions to group or others")
	})

	t.Run("world-readable file (0644)", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "secret")
		require.NoError(t, os.WriteFile(file, []byte("secret"), 0644))
		err := checkSecretFilePermissions("test_secret", file)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not grant any permissions to group or others")
	})

	t.Run("not owner-readable file (0200)", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "secret")
		require.NoError(t, os.WriteFile(file, []byte("secret"), 0200))
		err := checkSecretFilePermissions("test_secret", file)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be owner-readable")
	})
}

func TestValidateConfigFilePermissions_POSIXChmod(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(file, []byte("key=value"), 0644))
	err := validateConfigFilePermissions(file)
	assert.NoError(t, err)
	// Verify the file was chmod'd to 0600
	info, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
