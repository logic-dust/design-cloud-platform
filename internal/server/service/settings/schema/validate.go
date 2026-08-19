package schema

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var fileLocationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// ValidateStructByTags checks field bounds based on `validate` tags.
func ValidateStructByTags(v any) error {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return nil
	}

	typ := val.Type()
	for i := range typ.NumField() {
		field := typ.Field(i)
		fieldVal := val.Field(i)

		// 1. Pointer dereference & nil guard (nil pointer is treated as omitted)
		if fieldVal.Kind() == reflect.Pointer {
			if fieldVal.IsNil() {
				continue
			}
			fieldVal = fieldVal.Elem()
		}

		// 2. Recursive validation for embedded or pointed structs
		if fieldVal.Kind() == reflect.Struct {
			if err := ValidateStructByTags(fieldVal.Interface()); err != nil {
				return err
			}
			continue
		}

		tag := field.Tag.Get("validate")
		if tag == "" {
			continue
		}

		var hasMin, hasMax, allowZero bool
		var minVal, maxVal float64
		var enumVals []string

		tagParts := strings.SplitSeq(tag, ",")
		for part := range tagParts {
			kv := strings.SplitN(part, "=", 2)
			key := strings.TrimSpace(kv[0])
			if key == "omitempty" {
				continue
			}
			if key == "allowzero" {
				allowZero = true
				continue
			}
			if len(kv) == 2 {
				valStr := strings.TrimSpace(kv[1])
				switch key {
				case "min":
					hasMin = true
					minVal, _ = strconv.ParseFloat(valStr, 64)
				case "max":
					hasMax = true
					maxVal, _ = strconv.ParseFloat(valStr, 64)
				case "enum":
					enumVals = strings.Split(valStr, "|")
				}
			}
		}

		if !hasMin && !hasMax && len(enumVals) == 0 {
			continue
		}

		// Handle enum validation for any comparable type (numeric or string).
		if len(enumVals) > 0 {
			var asStr string
			switch fieldVal.Kind() {
			case reflect.String:
				asStr = fieldVal.String()
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				asStr = strconv.FormatInt(fieldVal.Int(), 10)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				asStr = strconv.FormatUint(fieldVal.Uint(), 10)
			case reflect.Float32, reflect.Float64:
				asStr = strconv.FormatFloat(fieldVal.Float(), 'g', -1, 64)
			default:
				// Non-comparable kind: skip enum validation silently.
				asStr = ""
			}
			if asStr != "" {
				matched := slices.Contains(enumVals, asStr)
				if matched {
					continue
				}
				if !hasMin && !hasMax {
					return fmt.Errorf("%w: %s must be one of %v", fs.ErrInvalid, field.Name, enumVals)
				}
			}
		}

		// Numeric min/max validation.
		var floatVal float64
		isNumeric := false
		switch fieldVal.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			floatVal = float64(fieldVal.Int())
			isNumeric = true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			floatVal = float64(fieldVal.Uint())
			isNumeric = true
		case reflect.Float32, reflect.Float64:
			floatVal = fieldVal.Float()
			isNumeric = true
		default:
			// Non-numeric kind: min/max don't apply, skip.
		}

		if !isNumeric {
			continue
		}

		if allowZero && floatVal == 0 {
			continue
		}

		if hasMin && floatVal < minVal {
			return fmt.Errorf("%w: %s must be >= %v", fs.ErrInvalid, field.Name, minVal)
		}
		if hasMax && floatVal > maxVal {
			return fmt.Errorf("%w: %s must be <= %v", fs.ErrInvalid, field.Name, maxVal)
		}
	}
	return nil
}

// validatePlatformSettings checks all platform domain fields.
func validatePlatformSettings(v PlatformSettings) error {
	if v.Name == "" {
		return fmt.Errorf("%w: name cannot be empty", fs.ErrInvalid)
	}
	if v.SupportUrl != "" &&
		!strings.HasPrefix(v.SupportUrl, "http://") &&
		!strings.HasPrefix(v.SupportUrl, "https://") {
		return fmt.Errorf("%w: supportUrl must start with http:// or https://", fs.ErrInvalid)
	}
	for _, m := range v.DisabledModules {
		if strings.TrimSpace(m) == "" {
			return fmt.Errorf("%w: disabledModules contains empty entry", fs.ErrInvalid)
		}
	}
	return nil
}

// validateConnectionSettings checks all connection domain fields.
func validateConnectionSettings(v ConnectionSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	return nil
}

// validateFileSettings checks all file domain fields.
func validateFileSettings(v FileSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	if err := validateFileLocations(v.Locations); err != nil {
		return err
	}
	return nil
}

func validateFileLocations(locations []FileLocationSettings) error {
	seen := make(map[string]struct{}, len(locations))
	for _, location := range locations {
		name := location.Name
		if name == "" {
			return fmt.Errorf("%w: file location name cannot be empty", fs.ErrInvalid)
		}
		if !fileLocationNamePattern.MatchString(name) {
			return fmt.Errorf("%w: file location name must match %s", fs.ErrInvalid, fileLocationNamePattern.String())
		}
		if name == "home" || name == "releases" || name == "trash" {
			return fmt.Errorf("%w: file location name is reserved: %s", fs.ErrInvalid, name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("%w: duplicate file location name: %s", fs.ErrInvalid, name)
		}
		seen[name] = struct{}{}

		root := location.Root
		if root == "" {
			return fmt.Errorf("%w: file location root cannot be empty", fs.ErrInvalid)
		}
		if !filepath.IsAbs(root) {
			return fmt.Errorf("%w: file location root must be absolute: %s", fs.ErrInvalid, name)
		}
		if strings.ContainsAny(root, "\x00\r\n\t") {
			return fmt.Errorf("%w: invalid file location root: %s", fs.ErrInvalid, name)
		}
	}
	return nil
}

// validateTunnelSettings checks all tunnel domain fields.
func validateTunnelSettings(v TunnelSettings) error {
	return ValidateStructByTags(v)
}

// validateAuthSettings checks all auth domain fields.
func validateAuthSettings(v AuthSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	if v.LdapWebAdminUrl != "" &&
		!strings.HasPrefix(v.LdapWebAdminUrl, "http://") &&
		!strings.HasPrefix(v.LdapWebAdminUrl, "https://") {
		return fmt.Errorf("%w: ldapWebAdminUrl must start with http:// or https://", fs.ErrInvalid)
	}
	// Cross-field constraint: a confirmed-absent result must not outlive a
	// confirmed-existing result. Both fields are required and zero is invalid.
	if v.IDMappingNotFoundCacheTTL > v.IDMappingFoundCacheTTL {
		return fmt.Errorf("%w: idMappingNotFoundCacheTTL must not exceed idMappingFoundCacheTTL", fs.ErrInvalid)
	}
	return nil
}

// validateAgentSettings checks agent domain fields (launcherPath, vnc, and monitor).
func validateAgentSettings(v AgentSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	if v.LauncherPath != "" && !filepath.IsAbs(v.LauncherPath) {
		return fmt.Errorf("%w: launcherPath must be an absolute path", fs.ErrInvalid)
	}
	if v.VNC.BinDir != "" && !filepath.IsAbs(v.VNC.BinDir) {
		return fmt.Errorf("%w: vnc.binDir must be an absolute path", fs.ErrInvalid)
	}
	for _, arg := range v.VNC.ServerArgs {
		if len(arg) > 255 {
			return fmt.Errorf("%w: vnc.serverArgs element is too long", fs.ErrInvalid)
		}
		if strings.ContainsAny(arg, "\r\n\x00") {
			return fmt.Errorf("%w: vnc.serverArgs element contains invalid control characters", fs.ErrInvalid)
		}
	}
	if v.Monitor.NvidiaSmiPath != "" && !filepath.IsAbs(v.Monitor.NvidiaSmiPath) {
		return fmt.Errorf("%w: monitor.nvidiaSmiPath must be an absolute path", fs.ErrInvalid)
	}
	for _, dm := range v.Monitor.DiskMounts {
		if dm.Name == "" {
			return fmt.Errorf("%w: monitor.diskMounts: name cannot be empty", fs.ErrInvalid)
		}
		if dm.Path == "" {
			return fmt.Errorf("%w: monitor.diskMounts: path cannot be empty", fs.ErrInvalid)
		}
		if dm.OS != "" && dm.OS != "linux" && dm.OS != "windows" {
			return fmt.Errorf("%w: monitor.diskMounts: os must be linux or windows", fs.ErrInvalid)
		}
	}
	return nil
}

// validateDashboardSettings checks all dashboard domain fields.
func validateDashboardSettings(v DashboardSettings) error {
	return ValidateStructByTags(v)
}

// validateSchedulerSettings checks all scheduler domain fields.
func validateSchedulerSettings(v SchedulerSettings) error {
	return ValidateStructByTags(v)
}

// validateAlertSettings checks all alert domain fields.
func validateAlertSettings(v AlertSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	// 交叉约束：memory/sharedStorage/disk 启用时至少有一个阈值维度不为 0
	if v.Memory.Enabled && v.Memory.MaxUsedPercent == 0 && v.Memory.MinFreeSpace == 0 {
		return fmt.Errorf("%w: alert.memory: enabled=true but both maxUsedPercent and minFreeSpace are 0", fs.ErrInvalid)
	}
	if v.SharedStorage.Enabled && v.SharedStorage.MaxUsedPercent == 0 && v.SharedStorage.MinFreeSpace == 0 {
		return fmt.Errorf("%w: alert.sharedStorage: enabled=true but both maxUsedPercent and minFreeSpace are 0", fs.ErrInvalid)
	}
	if v.Disk.Enabled && v.Disk.MaxUsedPercent == 0 && v.Disk.MinFreeSpace == 0 {
		return fmt.Errorf("%w: alert.disk: enabled=true but both maxUsedPercent and minFreeSpace are 0", fs.ErrInvalid)
	}
	return nil
}

// validateStorageSettings checks all storage domain fields.
func validateStorageSettings(v StorageSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(v.Storages))
	for _, storage := range v.Storages {
		if storage.ID == "" {
			return fmt.Errorf("%w: id cannot be empty", fs.ErrInvalid)
		}
		if _, ok := seen[storage.ID]; ok {
			return fmt.Errorf("%w: duplicate id %q", fs.ErrInvalid, storage.ID)
		}
		seen[storage.ID] = struct{}{}

		if storage.Name == "" {
			return fmt.Errorf("%w: name cannot be empty for storage %q", fs.ErrInvalid, storage.ID)
		}
		if storage.MountPoint == "" {
			return fmt.Errorf("%w: mountPoint cannot be empty for storage %q", fs.ErrInvalid, storage.ID)
		}
		// probeNodes 只在存储**启用**时才有意义：消费方（settings/dashboard）都先判
		// `!Enabled → continue` 再读它。禁用存储允许 probeNodes 为空（默认值即如此：
		// share 存储声明但未启用）。
		if storage.Enabled && len(storage.ProbeNodes) == 0 {
			return fmt.Errorf("%w: probeNodes cannot be empty for enabled storage %q", fs.ErrInvalid, storage.ID)
		}
		validTypes := map[string]struct{}{"nfs": {}, "lustre": {}, "ceph": {}, "gpfs": {}, "other": {}}
		if _, ok := validTypes[storage.Type]; !ok {
			return fmt.Errorf("%w: invalid type %q for storage %q", fs.ErrInvalid, storage.Type, storage.ID)
		}
	}
	return nil
}

// validateDesktopSettings checks all desktop domain fields.
func validateDesktopSettings(v DesktopSettings) error {
	return ValidateStructByTags(v)
}

// validateCertSettings checks the certificate lifecycle policy domain.
func validateCertSettings(v CertSettings) error {
	switch v.AutoRenewMode {
	case CertAutoRenewApply, CertAutoRenewObserve, CertAutoRenewOff:
	default:
		return fmt.Errorf("%w: cert.autoRenewMode must be one of apply/observe/off; got %q", fs.ErrInvalid, v.AutoRenewMode)
	}
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	if len(v.BackoffScheduleHours) == 0 {
		return fmt.Errorf("%w: cert.backoffScheduleHours must not be empty", fs.ErrInvalid)
	}
	for _, hours := range v.BackoffScheduleHours {
		if hours < 1 || hours > 168 {
			return fmt.Errorf("%w: cert.backoffScheduleHours entries must be in 1..168; got %d", fs.ErrInvalid, hours)
		}
	}
	return nil
}

// validateLicenseSettings checks all license domain fields.
func validateLicenseSettings(v LicenseSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	if v.LmutilPath != "" {
		if !filepath.IsAbs(v.LmutilPath) {
			return fmt.Errorf("%w: lmutilPath must be an absolute path", fs.ErrInvalid)
		}
		if strings.ContainsAny(v.LmutilPath, "\x00\r\n\t") {
			return fmt.Errorf("%w: lmutilPath contains invalid characters", fs.ErrInvalid)
		}
	}
	return nil
}
