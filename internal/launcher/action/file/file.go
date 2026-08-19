//go:build !windows

package file

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/launcher/action"
)

const maxFileReadBytes int64 = 1 << 20

// HandleFileList lists files in a directory using native Go functions
func HandleFileList(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.FileList](req)
	if err != nil {
		return "", err
	}

	if err := action.ValidatePath("dir", p.Dir); err != nil {
		return "", err
	}

	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return "", fmt.Errorf("failed to list directory: %w", err)
	}

	files := make([]payload.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		f := payload.FileInfo{
			Name:    info.Name(),
			Mode:    info.Mode().String(),
			IsDir:   info.IsDir(),
			Hidden:  strings.HasPrefix(info.Name(), "."),
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
		}

		// Get owner and group (Unix specific)
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat != nil {
			f.OwnerID = stat.Uid
			f.GroupID = stat.Gid
		}

		// Handle symlinks
		if info.Mode()&fs.ModeSymlink != 0 {
			f.IsSymlink = true
			link, err := os.Readlink(filepath.Join(p.Dir, info.Name()))
			if err == nil {
				f.LinkTarget = link
			}
		}

		files = append(files, f)
	}

	bytes, err := json.Marshal(files)
	if err != nil {
		return "", fmt.Errorf("failed to marshal file list: %w", err)
	}
	return string(bytes), nil
}

// HandleFileRead reads a file and returns its base64-encoded content.
func HandleFileRead(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.FileRead](req)
	if err != nil {
		return "", err
	}
	if err := action.ValidatePath("path", p.Path); err != nil {
		return "", err
	}

	f, err := os.Open(p.Path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to stat file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("failed to read file: path is a directory")
	}

	data, err := io.ReadAll(io.LimitReader(f, maxFileReadBytes+1))
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	if int64(len(data)) > maxFileReadBytes {
		return "", fmt.Errorf("failed to read file: file too large")
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// HandleFileDelete deletes a directory or files
func HandleFileDelete(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.FileDelete](req)
	if err != nil {
		return "", err
	}

	// Delete specific files if provided
	if len(p.Files) > 0 {
		if err := action.ValidatePath("dir", p.Dir); err != nil {
			return "", err
		}
		for _, f := range p.Files {
			if err := validateEntryName(f); err != nil {
				return "", fmt.Errorf("invalid file name %q: %w", f, err)
			}
		}

		count := 0
		failed := 0
		for _, f := range p.Files {
			fullPath := filepath.Join(p.Dir, f)
			if err := os.RemoveAll(fullPath); err != nil {
				failed++
				slog.Warn("file action: failed to delete entry", "path", fullPath, "error", err)
				continue
			}
			count++
		}
		if failed > 0 {
			return fmt.Sprintf("Deleted %d files, %d failed", count, failed), fmt.Errorf("failed to delete %d of %d files", failed, len(p.Files))
		}
		return fmt.Sprintf("Deleted %d files", count), nil
	}

	// When no explicit files are provided, refuse to recursively delete the
	// directory to avoid accidental broad deletion.
	return "", fmt.Errorf("no files specified for deletion")
}

// HandleFileMove moves a file/dir
func HandleFileMove(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.FileMove](req)
	if err != nil {
		return "", err
	}

	if err := action.ValidatePath("src", p.Src); err != nil {
		return "", err
	}
	if err := action.ValidatePath("dst", p.Dst); err != nil {
		return "", err
	}

	if err := os.Rename(p.Src, p.Dst); err != nil {
		return "", fmt.Errorf("failed to move file: %w", err)
	}
	return fmt.Sprintf("Moved %s to %s", p.Src, p.Dst), nil
}

// HandleFileRename renames a file
func HandleFileRename(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.FileRename](req)
	if err != nil {
		return "", err
	}

	if err := action.ValidatePath("src", p.Src); err != nil {
		return "", err
	}
	if err := validateEntryName(p.Name); err != nil {
		return "", fmt.Errorf("invalid new name")
	}

	dir := filepath.Dir(p.Src)
	dst := filepath.Join(dir, p.Name)

	if err := os.Rename(p.Src, dst); err != nil {
		return "", fmt.Errorf("failed to rename: %w", err)
	}
	return fmt.Sprintf("Renamed to %s", p.Name), nil
}

// HandleFileMkdir creates a directory
func HandleFileMkdir(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.FileMkdir](req)
	if err != nil {
		return "", err
	}
	if err := action.ValidatePath("dir", p.Dir); err != nil {
		return "", err
	}

	if err := os.Mkdir(p.Dir, 0755); err != nil {
		return "", fmt.Errorf("failed to mkdir: %w", err)
	}
	return fmt.Sprintf("Created %s", p.Dir), nil
}

// HandleFileMkdirAll creates a directory recursively and is idempotent when it already exists.
func HandleFileMkdirAll(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.FileMkdir](req)
	if err != nil {
		return "", err
	}
	if err := action.ValidatePath("dir", p.Dir); err != nil {
		return "", err
	}

	if err := os.MkdirAll(p.Dir, 0755); err != nil {
		return "", fmt.Errorf("failed to mkdirall: %w", err)
	}
	return fmt.Sprintf("Created %s", p.Dir), nil
}

// HandleFileCheck checks existence
func HandleFileCheck(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.FileCheck](req)
	if err != nil {
		return "", err
	}
	if err := action.ValidatePath("path", p.Path); err != nil {
		return "", err
	}

	info, err := os.Stat(p.Path)
	if err != nil {
		return "", fmt.Errorf("check failed: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(p.Path)
	if err != nil {
		return "", fmt.Errorf("check failed: %w", err)
	}
	realPath, err = filepath.Abs(realPath)
	if err != nil {
		return "", fmt.Errorf("check failed: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("check failed: unsupported file stat")
	}
	result := payload.FileCheckResult{
		Size:     info.Size(),
		Mode:     info.Mode().String(),
		RealPath: filepath.Clean(realPath),
		ModTime:  info.ModTime().Unix(),
		ModTimeN: int64(info.ModTime().Nanosecond()),
		CTime:    stat.Ctim.Sec,
		CTimeN:   stat.Ctim.Nsec,
		Device:   stat.Dev,
		Inode:    stat.Ino,
		IsDir:    info.IsDir(),
	}
	bytes, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal file check: %w", err)
	}
	return string(bytes), nil
}

// HandleFileWriteCheck checks write permission for a directory
func HandleFileWriteCheck(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.FileWriteCheck](req)
	if err != nil {
		return "", err
	}
	if err := action.ValidatePath("dir", p.Dir); err != nil {
		return "", err
	}

	// Check write permission by attempting to create a temp file
	// This is more reliable than checking mode bits (handles ACLs, RO mounts, etc.)
	f, err := os.CreateTemp(p.Dir, ".write_check_*")
	if err != nil {
		return "", fmt.Errorf("directory is not writable: %w", err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return "ok", nil
}

// HandleTrashMove moves files to trash directory using XDG Trash Spec
func HandleTrashMove(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.TrashMove](req)
	if err != nil {
		return "", err
	}
	if err := action.ValidatePath("dir", p.Dir); err != nil {
		return "", err
	}

	sourcePaths := make([]string, 0, len(p.Files))
	for _, name := range p.Files {
		if err := validateEntryName(name); err != nil {
			return "", err
		}
		sourcePaths = append(sourcePaths, filepath.Join(p.Dir, name))
	}

	if len(sourcePaths) == 0 {
		return "Moved 0 files to trash", nil
	}

	if err := Move(sourcePaths); err != nil {
		return "", err
	}
	return fmt.Sprintf("Moved %d files to trash", len(sourcePaths)), nil
}

// HandleTrashDelete deletes one item from the trash.
func HandleTrashDelete(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.TrashDelete](req)
	if err != nil {
		return "", err
	}
	if err := validateEntryName(p.TrashName); err != nil {
		return "", err
	}

	if err := Delete(p.TrashName); err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted trash item %s", p.TrashName), nil
}

// HandleTrashDeleteMany deletes multiple items from the trash.
func HandleTrashDeleteMany(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.TrashDeleteMany](req)
	if err != nil {
		return "", err
	}
	for _, name := range p.TrashNames {
		if err := validateEntryName(name); err != nil {
			return "", err
		}
	}

	if len(p.TrashNames) == 0 {
		return "Deleted 0 trash items", nil
	}
	if err := DeleteMany(p.TrashNames); err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted %d trash items", len(p.TrashNames)), nil
}

// HandleTrashEmpty empties the trash
func HandleTrashEmpty(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.TrashEmpty](req)
	if err != nil {
		return "", err
	}
	_ = p

	if err := Empty(); err != nil {
		return "", err
	}
	return "Trash emptied", nil
}

// HandleTrashList lists the trash directory contents, running as the target user.
// For the root trash dir, TrashPath should be empty.
// For a sub-directory, TrashPath is relative to the trash files root.
func HandleTrashList(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.TrashList](req)
	if err != nil {
		return "", err
	}

	if p.TrashPath == "" {
		result, err := List("")
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("failed to marshal trash list: %w", err)
		}
		return string(b), nil
	}

	result, err := List(p.TrashPath)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal trash sub-list: %w", err)
	}
	return string(b), nil
}

func validateEntryName(name string) error {
	if name == "" {
		return fmt.Errorf("entry name is required")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("entry name must be relative")
	}
	cleanName := filepath.Clean(name)
	if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
		return fmt.Errorf("invalid entry name: %s", name)
	}
	if cleanName != name || strings.ContainsAny(name, "/\\\x00\r\n\t") {
		return fmt.Errorf("entry name must be a single clean path segment: %s", name)
	}
	return nil
}
