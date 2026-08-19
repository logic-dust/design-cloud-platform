//go:build !windows

package file

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	commonfs "design-cloud-engine/internal/common/fs"
)

// ErrTrashFSNotSupported indicates trash filesystem is not supported on the host.
var ErrTrashFSNotSupported = errors.New("trash filesystem is not supported on this node")

// https://specifications.freedesktop.org/trash/latest/

type TrashItemInfo struct {
	Name         string `json:"name"`
	Mode         string `json:"mode"`
	IsDir        bool   `json:"isDir"`
	TrashName    string `json:"trashName"`
	Path         string `json:"path"`
	DeletionDate string `json:"deletionDate"`
}

type DirInfo struct {
	TrashPath string          `json:"trashPath"`
	Files     []TrashItemInfo `json:"files"`
}

func trashInfoName(name string) string {
	return name + ".trashinfo"
}

func Move(sourcePaths []string) error {
	filesDir, infoDir, err := ensureDirs()
	if err != nil {
		return err
	}

	failed := make([]string, 0)
	for _, srcPath := range sourcePaths {
		if err := moveOne(srcPath, filesDir, infoDir); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", srcPath, err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed to move files to trash: %s", strings.Join(failed, "; "))
	}
	return nil
}

func Delete(trashName string) error {
	filesDir, infoDir, err := dirs()
	if err != nil {
		return err
	}

	cleanName, err := commonfs.NormalizeTrashName(trashName)
	if err != nil {
		return err
	}

	removedFile, err := removePathIfExists(filepath.Join(filesDir, cleanName))
	if err != nil {
		return fmt.Errorf("failed to delete trash item data: %w", err)
	}
	removedInfo, err := removePathIfExists(filepath.Join(infoDir, trashInfoName(cleanName)))
	if err != nil {
		return fmt.Errorf("failed to delete trash item metadata: %w", err)
	}
	if !removedFile && !removedInfo {
		return os.ErrNotExist
	}
	return nil
}

func DeleteMany(trashNames []string) error {
	failed := make([]string, 0)
	for _, trashName := range trashNames {
		if err := Delete(trashName); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", trashName, err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed to delete trash items: %s", strings.Join(failed, "; "))
	}
	return nil
}

func Empty() error {
	filesDir, infoDir, err := dirs()
	if err != nil {
		return err
	}
	if err := removeDirContents(filesDir); err != nil {
		return fmt.Errorf("failed to empty trash files dir: %w", err)
	}
	if err := removeDirContents(infoDir); err != nil {
		return fmt.Errorf("failed to empty trash info dir: %w", err)
	}
	return nil
}

func List(trashPath string) (DirInfo, error) {
	filesDir, infoDir, err := dirs()
	if err != nil {
		return DirInfo{}, err
	}

	cleanPath, err := commonfs.NormalizeTrashPath(trashPath)
	if err != nil {
		return DirInfo{}, err
	}

	if cleanPath == "" {
		return listRoot(filesDir, infoDir)
	}
	return listSubDir(filesDir, infoDir, cleanPath)
}

func moveOne(srcPath, filesDir, infoDir string) error {
	absPath, err := filepath.Abs(srcPath)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	trashName, dstFile, dstInfo, err := allocatePaths(filesDir, infoDir, filepath.Base(absPath))
	if err != nil {
		return err
	}

	infoContent := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		url.PathEscape(absPath), time.Now().Format("2006-01-02T15:04:05"))
	if err := os.WriteFile(dstInfo, []byte(infoContent), 0o600); err != nil {
		return fmt.Errorf("failed to write trash metadata for %s: %w", trashName, err)
	}

	if err := movePath(absPath, dstFile); err != nil {
		_ = os.Remove(dstInfo)
		return err
	}
	return nil
}

func allocatePaths(filesDir, infoDir, baseName string) (trashName, dstFile, dstInfo string, err error) {
	trashName = baseName
	dstFile = filepath.Join(filesDir, trashName)
	dstInfo = filepath.Join(infoDir, trashInfoName(trashName))

	for counter := 2; ; counter++ {
		_, errFile := os.Stat(dstFile)
		_, errInfo := os.Stat(dstInfo)
		if os.IsNotExist(errFile) && os.IsNotExist(errInfo) {
			return trashName, dstFile, dstInfo, nil
		}
		if errFile != nil && !os.IsNotExist(errFile) {
			return "", "", "", fmt.Errorf("failed to inspect trash destination: %w", errFile)
		}
		if errInfo != nil && !os.IsNotExist(errInfo) {
			return "", "", "", fmt.Errorf("failed to inspect trash metadata destination: %w", errInfo)
		}

		trashName = fmt.Sprintf("%s.%d", baseName, counter)
		dstFile = filepath.Join(filesDir, trashName)
		dstInfo = filepath.Join(infoDir, trashInfoName(trashName))
	}
}

func movePath(srcPath, dstPath string) error {
	if err := os.Rename(srcPath, dstPath); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("failed to move %s into trash: %w", srcPath, err)
	}

	if err := copyPath(srcPath, dstPath); err != nil {
		return fmt.Errorf("failed to copy %s into trash: %w", srcPath, err)
	}
	if err := os.RemoveAll(srcPath); err != nil {
		_ = os.RemoveAll(dstPath)
		return fmt.Errorf("failed to remove original path %s after trash copy: %w", srcPath, err)
	}
	return nil
}

func copyPath(srcPath, dstPath string) error {
	info, err := os.Lstat(srcPath)
	if err != nil {
		return err
	}

	switch mode := info.Mode(); {
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(srcPath)
		if err != nil {
			return err
		}
		return os.Symlink(target, dstPath)
	case mode.IsDir():
		if err := os.Mkdir(dstPath, mode.Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(srcPath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyPath(filepath.Join(srcPath, entry.Name()), filepath.Join(dstPath, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		srcFile, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer func() { _ = srcFile.Close() }()

		dstFile, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
		if err != nil {
			return err
		}

		_, copyErr := io.Copy(dstFile, srcFile)
		closeErr := dstFile.Close()

		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return nil
	}
}

func listRoot(filesDir, infoDir string) (DirInfo, error) {
	entries, err := readDirIfExists(filesDir)
	if err != nil {
		return DirInfo{}, fmt.Errorf("failed to read trash directory: %w", err)
	}

	items := make([]TrashItemInfo, 0, len(entries))
	for _, entry := range entries {
		item := TrashItemInfo{
			TrashName: entry.Name(),
			Mode:      modeLabel(entry),
			IsDir:     entry.IsDir(),
			Name:      entry.Name(),
		}

		infoPath := filepath.Join(infoDir, trashInfoName(entry.Name()))
		if trashMeta, err := readInfoFile(infoPath); err == nil {
			item.Path = trashMeta.Path
			item.DeletionDate = trashMeta.DeletionDate
			if item.Path != "" {
				item.Name = filepath.Base(item.Path)
			}
		}

		items = append(items, item)
	}

	return DirInfo{
		TrashPath: filesDir,
		Files:     items,
	}, nil
}

func listSubDir(filesDir, infoDir, trashPath string) (DirInfo, error) {
	cleanPath := filepath.Clean(trashPath)
	entries, err := os.ReadDir(filepath.Join(filesDir, cleanPath))
	if err != nil {
		return DirInfo{}, fmt.Errorf("failed to read trash sub-directory: %w", err)
	}

	parts := strings.Split(strings.Trim(cleanPath, "/"), "/")
	trashName := parts[0]

	var deletionDate string
	if trashMeta, err := readInfoFile(filepath.Join(infoDir, trashInfoName(trashName))); err == nil {
		deletionDate = trashMeta.DeletionDate
	}

	items := make([]TrashItemInfo, 0, len(entries))
	for _, entry := range entries {
		items = append(items, TrashItemInfo{
			Name:         entry.Name(),
			Mode:         modeLabel(entry),
			IsDir:        entry.IsDir(),
			TrashName:    trashName,
			DeletionDate: deletionDate,
		})
	}

	return DirInfo{
		TrashPath: cleanPath,
		Files:     items,
	}, nil
}

type infoFile struct {
	Path         string
	DeletionDate string
}

func readInfoFile(infoPath string) (infoFile, error) {
	data, err := os.ReadFile(infoPath)
	if err != nil {
		return infoFile{}, err
	}

	var out infoFile
	for line := range strings.SplitSeq(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "Path="):
			decoded, err := url.PathUnescape(line[5:])
			if err == nil {
				out.Path = decoded
			} else {
				out.Path = line[5:]
			}
		case strings.HasPrefix(line, "DeletionDate="):
			out.DeletionDate = line[13:]
		}
	}
	return out, nil
}

func modeLabel(entry os.DirEntry) string {
	switch {
	case entry.Type().IsDir():
		return "d"
	case entry.Type()&os.ModeSymlink != 0:
		return "l"
	default:
		return "-"
	}
}

func ensureDirs() (filesDir, infoDir string, err error) {
	filesDir, infoDir, err = dirs()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return "", "", fmt.Errorf("failed to create trash files dir: %w", err)
	}
	if err := os.MkdirAll(infoDir, 0o700); err != nil {
		return "", "", fmt.Errorf("failed to create trash info dir: %w", err)
	}
	return filesDir, infoDir, nil
}

func dirs() (filesDir, infoDir string, err error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", ErrTrashFSNotSupported
	}

	dataHome := cmp.Or(os.Getenv("XDG_DATA_HOME"), filepath.Join(home, ".local", "share"))
	trashRoot := filepath.Join(dataHome, "Trash")
	return filepath.Join(trashRoot, "files"), filepath.Join(trashRoot, "info"), nil
}

func readDirIfExists(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []os.DirEntry{}, nil
	}
	return entries, err
}

func removeDirContents(dir string) error {
	entries, err := readDirIfExists(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func removePathIfExists(target string) (bool, error) {
	if _, err := os.Lstat(target); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := os.RemoveAll(target); err != nil {
		return false, err
	}
	return true, nil
}
