package file

import (
	"context"
	"design-cloud-engine/internal/contract"
	"path/filepath"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

// DelFiles deletes files from a directory.
func (s *Service) DelFiles(ctx context.Context, filesDirPath string, files []string, runUser string) error {
	delReq := payload.FileDelete{
		Dir:   filesDirPath,
		Files: files,
	}
	if _, err := s.execute(ctx, execution.ActionFileDelete, delReq, runUser); err != nil {
		return execution.HandleError(s.logger, err, "file.delete")
	}
	return nil
}

func (s *Service) DeleteLocationPaths(ctx context.Context, locationName string, relPaths []string, runUser string) error {
	if err := s.ensureWritableLocation(ctx, locationName, runUser); err != nil {
		return err
	}
	filesByDir, err := s.locationPathsByDir(ctx, locationName, relPaths, runUser)
	if err != nil {
		return err
	}
	for dir, names := range filesByDir {
		if err := s.DelFiles(ctx, dir, names, runUser); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) locationPathsByDir(ctx context.Context, locationName string, relPaths []string, runUser string) (map[string][]string, error) {
	if len(relPaths) == 0 {
		return nil, contract.NewInvalidParamError("missing paths")
	}

	filesByDir := make(map[string][]string)
	for _, relPath := range relPaths {
		cleanPath, err := normalizeLocationPath(relPath)
		if err != nil {
			return nil, err
		}
		if cleanPath == "" {
			return nil, contract.NewInvalidParamError("path is required")
		}
		targetPath, err := s.ResolveLocationPath(ctx, locationName, cleanPath, runUser)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(targetPath)
		if err := validateEntryName(name); err != nil {
			return nil, err
		}
		filesByDir[filepath.Dir(targetPath)] = append(filesByDir[filepath.Dir(targetPath)], name)
	}
	return filesByDir, nil
}
