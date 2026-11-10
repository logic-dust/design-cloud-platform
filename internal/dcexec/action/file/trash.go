//go:build !windows

package file

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	commonfs "design-cloud-engine/internal/platform/fs"
)

// ErrTrashFSNotSupported indicates trash filesystem is not supported on the host.
var ErrTrashFSNotSupported = errors.New("trash filesystem is not supported on this node")

// ErrTrashTooLarge indicates the files or directories exceed the maximum allowed trash size limit.
var ErrTrashTooLarge = errors.New("file or directory exceeds trash size limit")

// ErrTrashTooManyFiles indicates the total files/directories to be moved exceed the scan count limit.
var ErrTrashTooManyFiles = errors.New("items contain too many files for trash")

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
	Truncated bool            `json:"truncated,omitempty"`
}

func trashInfoName(name string) string {
	return name + ".trashinfo"
}

func Move(sourcePaths []string, maxBytes int64, maxCount int) error {
	if err := checkMoveLimits(sourcePaths, maxBytes, maxCount); err != nil {
		return err
	}

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

func checkMoveLimits(sourcePaths []string, maxBytes int64, maxCount int) error {
	if maxBytes <= 0 && maxCount <= 0 {
		return nil
	}

	var totalBytes int64
	var count int

	for _, srcPath := range sourcePaths {
		info, err := os.Lstat(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				return err
			}
			return fmt.Errorf("failed to inspect trash source: %w", err)
		}

		if !info.IsDir() {
			count++
			if maxCount > 0 && count > maxCount {
				return ErrTrashTooManyFiles
			}
			totalBytes += info.Size()
			if maxBytes > 0 && totalBytes > maxBytes {
				return ErrTrashTooLarge
			}
			continue
		}

		// Directory: walk tree with short-circuiting on count or size limit.
		walkErr := filepath.WalkDir(srcPath, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			count++
			if maxCount > 0 && count > maxCount {
				return ErrTrashTooManyFiles
			}
			if !d.IsDir() {
				fileInfo, err := d.Info()
				if err != nil {
					return err
				}
				totalBytes += fileInfo.Size()
				if maxBytes > 0 && totalBytes > maxBytes {
					return ErrTrashTooLarge
				}
			}
			return nil
		})
		if walkErr != nil {
			return walkErr
		}
	}
	return nil
}

func Restore(trashName string) error {
	filesDir, infoDir, err := dirs()
	if err != nil {
		return err
	}

	cleanName, err := commonfs.NormalizeTrashName(trashName)
	if err != nil {
		return err
	}

	infoPath := filepath.Join(infoDir, trashInfoName(cleanName))
	trashMeta, err := readInfoFile(infoPath)
	if err != nil {
		if os.IsNotExist(err) {
			return os.ErrNotExist
		}
		return fmt.Errorf("failed to read trash metadata: %w", err)
	}
	if trashMeta.Path == "" {
		return fmt.Errorf("%w: missing original path in trash metadata", fs.ErrInvalid)
	}

	dstPath := trashMeta.Path
	if _, err := os.Lstat(dstPath); err == nil {
		return fmt.Errorf("%w: restore destination already exists: %s", fs.ErrExist, dstPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect restore destination: %w", err)
	}

	srcPath := filepath.Join(filesDir, cleanName)
	if _, err := os.Lstat(srcPath); err != nil {
		if os.IsNotExist(err) {
			return os.ErrNotExist
		}
		return fmt.Errorf("failed to inspect trash item: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("failed to create restore parent dir: %w", err)
	}
	if err := movePath(srcPath, dstPath); err != nil {
		return fmt.Errorf("failed to restore %s: %w", cleanName, err)
	}
	if _, err := removePathIfExists(infoPath); err != nil {
		return fmt.Errorf("failed to remove trash metadata after restore: %w", err)
	}
	return nil
}

func RestoreMany(trashNames []string) error {
	failed := make([]string, 0)
	for _, trashName := range trashNames {
		if err := Restore(trashName); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", trashName, err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed to restore trash items: %s", strings.Join(failed, "; "))
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

func List(trashPath string, limit int) (DirInfo, error) {
	filesDir, infoDir, err := dirs()
	if err != nil {
		return DirInfo{}, err
	}

	cleanPath, err := commonfs.NormalizeTrashPath(trashPath)
	if err != nil {
		return DirInfo{}, err
	}

	if cleanPath == "" {
		return listRoot(filesDir, infoDir, limit)
	}
	return listSubDir(filesDir, infoDir, cleanPath, limit)
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
		return fmt.Errorf("failed to move %s to %s: %w", srcPath, dstPath, err)
	}

	if err := copyPath(srcPath, dstPath); err != nil {
		return fmt.Errorf("failed to copy %s to %s: %w", srcPath, dstPath, err)
	}
	if err := os.RemoveAll(srcPath); err != nil {
		_ = os.RemoveAll(dstPath)
		return fmt.Errorf("failed to remove original path %s after copy: %w", srcPath, err)
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
		return forEachDirBatch(srcPath, func(names []string) error {
			for _, name := range names {
				if err := copyPath(filepath.Join(srcPath, name), filepath.Join(dstPath, name)); err != nil {
					return err
				}
			}
			return nil
		})
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

func listRoot(filesDir, infoDir string, limit int) (DirInfo, error) {
	names, truncated, err := readDirNames(filesDir, listReadCap(limit))
	if os.IsNotExist(err) {
		return DirInfo{TrashPath: filesDir, Files: []TrashItemInfo{}}, nil
	}
	if err != nil {
		return DirInfo{}, fmt.Errorf("failed to read trash directory: %w", err)
	}
	slices.Sort(names)

	items := make([]TrashItemInfo, 0, len(names))
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(filesDir, name))
		if err != nil {
			continue
		}
		item := TrashItemInfo{
			TrashName: name,
			Mode:      modeLabel(info.Mode()),
			IsDir:     info.IsDir(),
			Name:      name,
		}

		infoPath := filepath.Join(infoDir, trashInfoName(name))
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
		Truncated: truncated,
	}, nil
}

func listSubDir(filesDir, infoDir, trashPath string, limit int) (DirInfo, error) {
	cleanPath := filepath.Clean(trashPath)
	names, truncated, err := readDirNames(filepath.Join(filesDir, cleanPath), listReadCap(limit))
	if err != nil {
		return DirInfo{}, fmt.Errorf("failed to read trash sub-directory: %w", err)
	}
	slices.Sort(names)

	parts := strings.Split(strings.Trim(cleanPath, "/"), "/")
	trashName := parts[0]

	var deletionDate string
	if trashMeta, err := readInfoFile(filepath.Join(infoDir, trashInfoName(trashName))); err == nil {
		deletionDate = trashMeta.DeletionDate
	}

	items := make([]TrashItemInfo, 0, len(names))
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(filesDir, cleanPath, name))
		if err != nil {
			continue
		}
		items = append(items, TrashItemInfo{
			Name:         name,
			Mode:         modeLabel(info.Mode()),
			IsDir:        info.IsDir(),
			TrashName:    trashName,
			DeletionDate: deletionDate,
		})
	}

	return DirInfo{
		TrashPath: cleanPath,
		Files:     items,
		Truncated: truncated,
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

func modeLabel(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "d"
	case mode&os.ModeSymlink != 0:
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

func removeDirContents(dir string) error {
	for {
		names, _, err := readDirNames(dir, dirReadBatchSize)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return nil
		}
		for _, name := range names {
			if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
	}
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
