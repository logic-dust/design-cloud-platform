//go:build !windows

package file

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestTrash(t *testing.T) (dataHome string, filesDir string, infoDir string) {
	t.Helper()
	tempDir := t.TempDir()
	dataHome = filepath.Join(tempDir, "share")
	t.Setenv("XDG_DATA_HOME", dataHome)

	filesDir = filepath.Join(dataHome, "Trash", "files")
	infoDir = filepath.Join(dataHome, "Trash", "info")
	require.NoError(t, os.MkdirAll(filesDir, 0o700))
	require.NoError(t, os.MkdirAll(infoDir, 0o700))
	return dataHome, filesDir, infoDir
}

func createTrashItem(t *testing.T, filesDir, infoDir, trashName, originalPath, content string) {
	t.Helper()
	// 1. write file into filesDir
	srcPath := filepath.Join(filesDir, trashName)
	require.NoError(t, os.WriteFile(srcPath, []byte(content), 0o600))

	// 2. write metadata into infoDir
	infoPath := filepath.Join(infoDir, trashInfoName(trashName))
	infoContent := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		url.PathEscape(originalPath), time.Now().Format("2006-01-02T15:04:05"))
	require.NoError(t, os.WriteFile(infoPath, []byte(infoContent), 0o600))
}

func TestRestore_Success(t *testing.T) {
	_, filesDir, infoDir := setupTestTrash(t)
	restoreTargetDir := t.TempDir()
	origFile := filepath.Join(restoreTargetDir, "doc.txt")

	createTrashItem(t, filesDir, infoDir, "doc.txt", origFile, "hello trash")

	// Verify pre-condition
	_, err := os.Stat(origFile)
	require.True(t, os.IsNotExist(err))

	// Execute Restore
	err = Restore("doc.txt")
	require.NoError(t, err)

	// Verify post-condition
	content, err := os.ReadFile(origFile)
	require.NoError(t, err)
	assert.Equal(t, "hello trash", string(content))

	// Verify trash files cleaned
	_, err = os.Stat(filepath.Join(filesDir, "doc.txt"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(infoDir, trashInfoName("doc.txt")))
	assert.True(t, os.IsNotExist(err))
}

func TestRestore_ParentDirAutoCreated(t *testing.T) {
	_, filesDir, infoDir := setupTestTrash(t)
	baseDir := t.TempDir()
	nestedTarget := filepath.Join(baseDir, "nested", "sub", "file.log")

	createTrashItem(t, filesDir, infoDir, "file.log", nestedTarget, "nested log content")

	err := Restore("file.log")
	require.NoError(t, err)

	content, err := os.ReadFile(nestedTarget)
	require.NoError(t, err)
	assert.Equal(t, "nested log content", string(content))
}

func TestRestore_DestinationAlreadyExists(t *testing.T) {
	_, filesDir, infoDir := setupTestTrash(t)
	targetDir := t.TempDir()
	origFile := filepath.Join(targetDir, "existing.txt")
	require.NoError(t, os.WriteFile(origFile, []byte("original alive"), 0o600))

	createTrashItem(t, filesDir, infoDir, "existing.txt", origFile, "from trash")

	err := Restore("existing.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrExist)

	// Verify existing file was not overwritten
	content, err := os.ReadFile(origFile)
	require.NoError(t, err)
	assert.Equal(t, "original alive", string(content))

	// Verify trash item still exists
	_, err = os.Stat(filepath.Join(filesDir, "existing.txt"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(infoDir, trashInfoName("existing.txt")))
	require.NoError(t, err)
}

func TestRestore_TrashItemNotFound(t *testing.T) {
	setupTestTrash(t)

	err := Restore("non_existent.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestRestore_InvalidTrashName(t *testing.T) {
	setupTestTrash(t)

	for _, badName := range []string{"", ".", "..", "foo/bar", "foo/../bar"} {
		err := Restore(badName)
		require.Error(t, err, "name %q should fail", badName)
	}
}

func TestRestoreMany_SuccessAndPartialFailure(t *testing.T) {
	_, filesDir, infoDir := setupTestTrash(t)
	targetDir := t.TempDir()

	file1 := filepath.Join(targetDir, "f1.txt")
	file2 := filepath.Join(targetDir, "f2.txt")
	require.NoError(t, os.WriteFile(file2, []byte("already here"), 0o600))

	createTrashItem(t, filesDir, infoDir, "f1.txt", file1, "content1")
	createTrashItem(t, filesDir, infoDir, "f2.txt", file2, "content2")

	// Restore f1 and f2: f1 should succeed, f2 should fail with conflict
	err := RestoreMany([]string{"f1.txt", "f2.txt"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "f2.txt")

	// f1 is restored
	c1, err := os.ReadFile(file1)
	require.NoError(t, err)
	assert.Equal(t, "content1", string(c1))

	// f2 remains existing
	c2, err := os.ReadFile(file2)
	require.NoError(t, err)
	assert.Equal(t, "already here", string(c2))
}

func TestMoveAndRestore_RoundTrip(t *testing.T) {
	_, filesDir, infoDir := setupTestTrash(t)
	targetDir := t.TempDir()
	originalFile := filepath.Join(targetDir, "roundtrip.txt")
	require.NoError(t, os.WriteFile(originalFile, []byte("roundtrip payload"), 0o600))

	// 1. Move to trash
	err := Move([]string{originalFile}, 0, 0)
	require.NoError(t, err)

	_, err = os.Stat(originalFile)
	require.True(t, os.IsNotExist(err))

	// Check filesDir has roundtrip.txt
	trashFile := filepath.Join(filesDir, "roundtrip.txt")
	_, err = os.Stat(trashFile)
	require.NoError(t, err)

	// Check infoDir has metadata
	infoFile := filepath.Join(infoDir, trashInfoName("roundtrip.txt"))
	_, err = os.Stat(infoFile)
	require.NoError(t, err)

	// 2. Restore from trash
	err = Restore("roundtrip.txt")
	require.NoError(t, err)

	// File is back
	content, err := os.ReadFile(originalFile)
	require.NoError(t, err)
	assert.Equal(t, "roundtrip payload", string(content))

	// Trash is clean
	_, err = os.Stat(trashFile)
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(infoFile)
	assert.True(t, os.IsNotExist(err))
}

func TestMove_SizeLimit(t *testing.T) {
	_, filesDir, _ := setupTestTrash(t)
	targetDir := t.TempDir()

	file1 := filepath.Join(targetDir, "large.bin")
	require.NoError(t, os.WriteFile(file1, make([]byte, 1024), 0o600)) // 1024 bytes

	// Limit is 500 bytes -> should fail
	err := Move([]string{file1}, 500, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTrashTooLarge)

	// Original file still intact
	_, err = os.Stat(file1)
	require.NoError(t, err)

	// Limit is 2000 bytes -> should succeed
	err = Move([]string{file1}, 2000, 0)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(filesDir, "large.bin"))
	require.NoError(t, err)
}

func TestMove_CountLimit(t *testing.T) {
	setupTestTrash(t)
	targetDir := t.TempDir()

	// Create directory with 5 files
	subDir := filepath.Join(targetDir, "lotsoffiles")
	require.NoError(t, os.MkdirAll(subDir, 0o700))
	for i := range 5 {
		require.NoError(t, os.WriteFile(filepath.Join(subDir, fmt.Sprintf("f%d.txt", i)), []byte("a"), 0o600))
	}

	// Limit is 3 files -> subDir has 1 dir + 5 files = 6 entries -> should fail
	err := Move([]string{subDir}, 0, 3)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTrashTooManyFiles)

	// Limit is 10 -> should succeed
	err = Move([]string{subDir}, 0, 10)
	require.NoError(t, err)
}
