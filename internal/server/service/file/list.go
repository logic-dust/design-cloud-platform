package file

import (
	"context"
	"design-cloud-engine/internal/contract"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/model"
)

// ListDir lists files in a directory via the PrivilegedExec.
func (s *Service) ListDir(ctx context.Context, dirPath, runUser string) (DirInfo, error) {
	listResult, err := s.execute(ctx, execution.ActionFileList, payload.FileList{Dir: dirPath, Limit: s.listMaxEntries(ctx)}, runUser)
	if err != nil {
		if isMissingDirectory(err) {
			return DirInfo{}, contract.NewNotFoundError("directory not found")
		}
		return DirInfo{}, execution.HandleError(s.logger, err, "file.list_files")
	}

	dirInfo := DirInfo{Name: path.Base(dirPath), Path: dirPath, Files: []payload.FileInfo{}}
	if listResult == "" {
		return dirInfo, nil
	}

	var listed struct {
		Files     []payload.FileInfo `json:"files"`
		Truncated bool               `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(listResult), &listed); err != nil {
		s.logger.Error("failed to unmarshal PrivilegedExec file list", "error", err, "result", listResult)
		return DirInfo{}, fmt.Errorf("invalid file list response from PrivilegedExec: %w", err)
	}
	if listed.Files != nil {
		dirInfo.Files = listed.Files
	}
	dirInfo.Truncated = listed.Truncated
	return dirInfo, nil
}

func (s *Service) listMaxEntries(ctx context.Context) int {
	if s.settingsSvc == nil {
		return schema.ListMaxEntriesDefault
	}
	fileSetting, err := s.settingsSvc.GetFileSettings(ctx)
	if err != nil {
		return schema.ListMaxEntriesDefault
	}
	return fileSetting.ListMaxEntries
}

func (s *Service) ListLocations(ctx context.Context, runUser string) ([]LocationInfo, error) {
	if !s.IsEnabled(ctx) {
		return nil, ErrFileDisabled
	}
	if _, err := s.userSvc.Get(ctx, runUser); err != nil {
		return nil, execution.HandleError(s.logger, err, "file.list_locations")
	}

	locations := []LocationInfo{{Name: string(PredefinedDirHome), BuiltIn: true, ReadOnly: false}}
	if dbUser, err := s.userSvc.GetDBUser(ctx, runUser); err == nil {
		if dbUser.Role == model.RoleSuperAdmin {
			locations = append(locations, LocationInfo{Name: string(PredefinedDirReleases), BuiltIn: true, ReadOnly: false})
		}
	}

	for _, location := range s.extraFileLocations(ctx) {
		if !location.Enabled {
			continue
		}
		locations = append(locations, LocationInfo{Name: location.Name, BuiltIn: false, ReadOnly: false})
	}
	return locations, nil
}

// ResolveLocationPath resolves a location-relative API path into a real path.
func (s *Service) ResolveLocationPath(ctx context.Context, locationName, relPath, runUser string) (string, error) {
	if !s.IsEnabled(ctx) {
		return "", ErrFileDisabled
	}
	userInfo, err := s.userSvc.Get(ctx, runUser)
	if err != nil {
		return "", execution.HandleError(s.logger, err, "file.resolve_location")
	}

	var root string
	switch PredefinedDir(locationName) {
	case PredefinedDirHome:
		root = userInfo.Home
	case PredefinedDirReleases:
		dbUser, err := s.userSvc.GetDBUser(ctx, runUser)
		if err != nil || dbUser.Role != model.RoleSuperAdmin {
			return "", contract.NewNotFoundError(fmt.Sprintf("file location not found: %s", locationName))
		}
		root = s.desktopReleaseDir(ctx)
	default:
		location, ok := s.extraFileLocation(ctx, locationName)
		if !ok || !location.Enabled {
			return "", contract.NewNotFoundError(fmt.Sprintf("file location not found: %s", locationName))
		}
		root = location.Root
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve location root: %w", err)
	}
	cleanRel, err := normalizeLocationPath(relPath)
	if err != nil {
		return "", err
	}
	if cleanRel == "" {
		return rootAbs, nil
	}
	target := filepath.Join(rootAbs, cleanRel)
	relativeToRoot, err := filepath.Rel(rootAbs, target)
	if err != nil {
		return "", fmt.Errorf("resolve location path: %w", err)
	}
	if relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(relativeToRoot) {
		return "", contract.NewValidationError("invalid location path")
	}
	return target, nil
}

// ListLocationDirPath lists files under a named file location.
func (s *Service) ListLocationDirPath(ctx context.Context, locationName, relPath, runUser string) (DirInfo, error) {
	dirPath, err := s.ResolveLocationPath(ctx, locationName, relPath, runUser)
	if err != nil {
		return DirInfo{}, err
	}
	return s.ListDir(ctx, dirPath, runUser)
}

func (s *Service) GetLocationPolicy(ctx context.Context, locationName, runUser string) (LocationAccessPolicy, error) {
	switch PredefinedDir(locationName) {
	case PredefinedDirHome:
		return LocationAccessPolicy{Exists: true, ReadOnly: false}, nil
	case PredefinedDirReleases:
		dbUser, err := s.userSvc.GetDBUser(ctx, runUser)
		if err != nil || dbUser.Role != model.RoleSuperAdmin {
			return LocationAccessPolicy{}, nil
		}
		return LocationAccessPolicy{Exists: true, ReadOnly: false}, nil
	default:
		location, ok := s.extraFileLocation(ctx, locationName)
		if !ok || !location.Enabled {
			return LocationAccessPolicy{}, nil
		}
		return LocationAccessPolicy{Exists: true, ReadOnly: false}, nil
	}
}

func (s *Service) ensureWritableLocation(ctx context.Context, locationName, runUser string) error {
	policy, err := s.GetLocationPolicy(ctx, locationName, runUser)
	if err != nil {
		return err
	}
	if !policy.Exists {
		return contract.NewNotFoundError("file location not found")
	}
	if policy.ReadOnly {
		return contract.NewForbiddenError("file location is read-only")
	}
	return nil
}

func (s *Service) extraFileLocations(ctx context.Context) []schema.FileLocationSettings {
	fileSetting, err := s.settingsSvc.GetFileSettings(ctx)
	if err != nil || len(fileSetting.Locations) == 0 {
		return nil
	}
	return fileSetting.Locations
}

func (s *Service) extraFileLocation(ctx context.Context, name string) (schema.FileLocationSettings, bool) {
	fileSetting, err := s.settingsSvc.GetFileSettings(ctx)
	if err != nil {
		return schema.FileLocationSettings{}, false
	}
	return fileSetting.FindLocation(name)
}

func validateEntryName(name string) error {
	if name == "" || name == "." || name == ".." {
		return contract.NewValidationError("invalid file name")
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00\r\n\t") {
		return contract.NewValidationError("invalid file name")
	}
	return nil
}

func isMissingDirectory(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if execErr, ok := errors.AsType[*execution.ActionError](err); ok {
		msg := execErr.Message + " " + execErr.Stderr
		return strings.Contains(msg, "no such file or directory") ||
			strings.Contains(msg, "cannot access") && strings.Contains(msg, "No such file")
	}
	return false
}

func normalizeLocationPath(relPath string) (string, error) {
	if relPath == "" {
		return "", nil
	}
	if filepath.IsAbs(relPath) || strings.ContainsAny(relPath, "\x00\r\n\t") {
		return "", contract.NewValidationError("invalid location path")
	}
	cleanRel := filepath.Clean(relPath)
	if cleanRel == "." {
		return "", nil
	}
	if cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", contract.NewValidationError("invalid location path")
	}
	return cleanRel, nil
}
