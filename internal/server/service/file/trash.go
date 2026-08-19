package file

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"

	"design-cloud-engine/internal/execution/payload"

	commonfs "design-cloud-engine/internal/common/fs"
	"design-cloud-engine/internal/execution"
)

// trashFileEntry describes a single file/dir inside a user's trash directory.
type trashFileEntry struct {
	Name         string `json:"name"`
	Mode         string `json:"mode"`
	IsDir        bool   `json:"isDir"`
	TrashName    string `json:"trashName"`
	Path         string `json:"path"`
	DeletionDate string `json:"deletionDate"`
}

// ListTrash lists trash contents at the given relative path.
// Empty trashPath means the trash root.
func (s *Service) ListTrash(ctx context.Context, trashPath, username string) (TrashDirInfo, error) {
	cleanPath, err := commonfs.NormalizeTrashPath(trashPath)
	if err != nil {
		return TrashDirInfo{}, err
	}

	result, err := s.execute(ctx, execution.ActionTrashList,
		payload.TrashList{TrashPath: cleanPath}, username)
	if err != nil {
		return TrashDirInfo{}, execution.HandleError(s.logger, err, "trash.list")
	}

	var out TrashDirInfo
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		return TrashDirInfo{}, fmt.Errorf("invalid trash list response from launcher: %w", err)
	}
	return out, nil
}

// MoveToTrash moves files to trash by gio command.
func (s *Service) MoveToTrash(ctx context.Context, dirPath string, files []string, requestUser string) error {
	trashReq := payload.TrashMove{
		Dir:   dirPath,
		Files: files,
	}
	if _, err := s.execute(ctx, execution.ActionTrashMove, trashReq, requestUser); err != nil {
		return execution.HandleError(s.logger, err, "trash.move")
	}
	return nil
}

func (s *Service) MoveLocationPathsToTrash(ctx context.Context, locationName string, relPaths []string, requestUser string) error {
	if err := s.ensureWritableLocation(ctx, locationName, requestUser); err != nil {
		return err
	}
	filesByDir, err := s.locationPathsByDir(ctx, locationName, relPaths, requestUser)
	if err != nil {
		return err
	}
	for dir, names := range filesByDir {
		if err := s.MoveToTrash(ctx, dir, names, requestUser); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) DeleteTrashItem(ctx context.Context, trashName, requestUser string) error {
	return s.DeleteTrashItems(ctx, []string{trashName}, requestUser)
}

func (s *Service) DeleteTrashItems(ctx context.Context, trashNames []string, requestUser string) error {
	cleanNames, err := normalizeTrashNames(trashNames)
	if err != nil {
		return err
	}

	if len(cleanNames) == 1 {
		trashPayload := payload.TrashDelete{TrashName: cleanNames[0]}
		if _, err := s.execute(ctx, execution.ActionTrashDelete, trashPayload, requestUser); err != nil {
			return execution.HandleError(s.logger, err, "trash.delete")
		}
		return nil
	}

	trashPayload := payload.TrashDeleteMany{TrashNames: cleanNames}
	if _, err := s.execute(ctx, execution.ActionTrashDeleteMany, trashPayload, requestUser); err != nil {
		return execution.HandleError(s.logger, err, "trash.delete_many")
	}
	return nil
}

func (s *Service) EmptyTrash(ctx context.Context, requestUser string) error {
	if _, err := s.execute(ctx, execution.ActionTrashEmpty, payload.TrashEmpty{}, requestUser); err != nil {
		return execution.HandleError(s.logger, err, "trash.empty")
	}
	return nil
}

func normalizeTrashNames(trashNames []string) ([]string, error) {
	if len(trashNames) == 0 {
		return nil, fmt.Errorf("%w: missing trash names", fs.ErrInvalid)
	}

	out := make([]string, 0, len(trashNames))
	seen := make(map[string]struct{}, len(trashNames))
	for _, trashName := range trashNames {
		cleanName, err := commonfs.NormalizeTrashName(trashName)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[cleanName]; ok {
			continue
		}
		seen[cleanName] = struct{}{}
		out = append(out, cleanName)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: missing trash names", fs.ErrInvalid)
	}
	return out, nil
}
