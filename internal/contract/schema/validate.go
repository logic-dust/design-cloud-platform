package schema

import (
	"design-cloud-engine/internal/contract"
	"fmt"
	"net/netip"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

var fileLocationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// ValidateStructByTags checks field bounds based on `validate` tags.
// min/max/enum are parsed once via parseValidateTag (same path as JSON Schema).
// Primitive slices apply the field tag to each element; struct slices recurse.
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

		if fieldVal.Kind() == reflect.Pointer {
			if fieldVal.IsNil() {
				continue
			}
			fieldVal = fieldVal.Elem()
		}

		if fieldVal.Kind() == reflect.Struct {
			if err := ValidateStructByTags(fieldVal.Interface()); err != nil {
				return err
			}
			continue
		}

		if fieldVal.Kind() == reflect.Slice {
			elemType := fieldVal.Type().Elem()
			if isStructOrStructPtr(elemType) {
				for j := range fieldVal.Len() {
					if err := ValidateStructByTags(fieldVal.Index(j).Interface()); err != nil {
						return err
					}
				}
				continue
			}
			tag := field.Tag.Get("validate")
			if tag == "" {
				continue
			}
			elemKind := unwrapType(elemType).Kind()
			c, err := parseValidateTag(tag, elemKind)
			if err != nil {
				return contract.NewValidationError(fmt.Sprintf("%s: %v", field.Name, err), err)
			}
			if !c.hasConstraints() && !c.OmitEmpty {
				continue
			}
			for j := range fieldVal.Len() {
				elemVal := fieldVal.Index(j)
				if elemVal.Kind() == reflect.Pointer {
					if elemVal.IsNil() {
						continue
					}
					elemVal = elemVal.Elem()
				}
				elemName := fmt.Sprintf("%s[%d]", field.Name, j)
				if err := validateValueAgainstConstraints(elemName, elemVal, c); err != nil {
					return err
				}
			}
			continue
		}

		tag := field.Tag.Get("validate")
		if tag == "" {
			continue
		}
		c, err := parseValidateTag(tag, fieldVal.Kind())
		if err != nil {
			return contract.NewValidationError(fmt.Sprintf("%s: %v", field.Name, err), err)
		}
		if err := validateValueAgainstConstraints(field.Name, fieldVal, c); err != nil {
			return err
		}
	}
	return nil
}

func validateValueAgainstConstraints(fieldName string, fieldVal reflect.Value, c fieldConstraints) error {
	if !c.hasConstraints() && !c.OmitEmpty {
		return nil
	}

	if len(c.Enum) > 0 {
		if fieldVal.Kind() == reflect.String && fieldVal.String() == "" {
			if c.OmitEmpty {
				return nil
			}
			return contract.NewValidationError(fmt.Sprintf("%s must be one of %v", fieldName, c.Enum))
		}
		asStr, ok := valueAsEnumString(fieldVal)
		if !ok {
			return nil
		}
		if slices.Contains(c.Enum, asStr) {
			return nil
		}
		return contract.NewValidationError(fmt.Sprintf("%s must be one of %v", fieldName, c.Enum))
	}

	switch fieldVal.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := fieldVal.Int()
		if c.AllowZero && n == 0 {
			return nil
		}
		if c.HasMin && n < c.MinInt {
			return contract.NewValidationError(fmt.Sprintf("%s must be >= %d", fieldName, c.MinInt))
		}
		if c.HasMax && n > c.MaxInt {
			return contract.NewValidationError(fmt.Sprintf("%s must be <= %d", fieldName, c.MaxInt))
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := fieldVal.Uint()
		if c.AllowZero && n == 0 {
			return nil
		}
		if c.HasMin && n < c.MinUint {
			return contract.NewValidationError(fmt.Sprintf("%s must be >= %d", fieldName, c.MinUint))
		}
		if c.HasMax && n > c.MaxUint {
			return contract.NewValidationError(fmt.Sprintf("%s must be <= %d", fieldName, c.MaxUint))
		}
	case reflect.Float32, reflect.Float64:
		n := fieldVal.Float()
		if c.AllowZero && n == 0 {
			return nil
		}
		if c.HasMin && n < c.MinFloat {
			return contract.NewValidationError(fmt.Sprintf("%s must be >= %v", fieldName, c.MinFloat))
		}
		if c.HasMax && n > c.MaxFloat {
			return contract.NewValidationError(fmt.Sprintf("%s must be <= %v", fieldName, c.MaxFloat))
		}
	case reflect.String:
		str := fieldVal.String()
		if str == "" && c.OmitEmpty {
			return nil
		}
		charCount := utf8.RuneCountInString(str)
		if c.HasMinLen && charCount < c.MinLen {
			return contract.NewValidationError(fmt.Sprintf("%s length cannot be less than %d characters", fieldName, c.MinLen))
		}
		if c.HasMaxLen && charCount > c.MaxLen {
			return contract.NewValidationError(fmt.Sprintf("%s length cannot exceed %d characters", fieldName, c.MaxLen))
		}
	}
	return nil
}

func valueAsEnumString(fieldVal reflect.Value) (string, bool) {
	switch fieldVal.Kind() {
	case reflect.String:
		return fieldVal.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(fieldVal.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(fieldVal.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(fieldVal.Float(), 'g', -1, 64), true
	default:
		return "", false
	}
}

// validatePlatformSettings checks all platform domain fields.
func validatePlatformSettings(v PlatformSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	if strings.TrimSpace(v.Name) == "" {
		return contract.NewValidationError("name cannot be empty")
	}
	if v.SupportUrl != "" &&
		!strings.HasPrefix(v.SupportUrl, "http://") &&
		!strings.HasPrefix(v.SupportUrl, "https://") {
		return contract.NewValidationError("supportUrl must start with http:// or https://")
	}
	for module := range v.DisabledModules {
		if !slices.Contains(userPortalModuleIDs, module) {
			return contract.NewValidationError("disabledModules contains unknown module: " + module)
		}
	}
	return nil
}

// validateConnectionSettings checks all connection domain fields.
func validateConnectionSettings(v ConnectionSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	return validateProxySettings(v.Proxy)
}

// validateFileSettings checks all file domain fields.
func validateFileSettings(v FileSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	return validateFileLocations(v.Locations)
}

func validateFileLocations(locations []FileLocationSettings) error {
	seen := make(map[string]struct{}, len(locations))
	for _, location := range locations {
		name := location.Name
		if name == "" {
			return contract.NewValidationError("file location name cannot be empty")
		}
		if !fileLocationNamePattern.MatchString(name) {
			return contract.NewValidationError(fmt.Sprintf("file location name must match %s", fileLocationNamePattern.String()))
		}
		if name == "home" || name == "releases" || name == "trash" {
			return contract.NewValidationError(fmt.Sprintf("file location name is reserved: %s", name))
		}
		if _, ok := seen[name]; ok {
			return contract.NewValidationError(fmt.Sprintf("duplicate file location name: %s", name))
		}
		seen[name] = struct{}{}

		root := location.Root
		if root == "" {
			return contract.NewValidationError("file location root cannot be empty")
		}
		if !filepath.IsAbs(root) {
			return contract.NewValidationError(fmt.Sprintf("file location root must be absolute: %s", name))
		}
		if strings.ContainsAny(root, "\x00\r\n\t") {
			return contract.NewValidationError(fmt.Sprintf("invalid file location root: %s", name))
		}
	}
	return nil
}

// validateProxySettings checks all proxy domain fields.
func validateProxySettings(v ProxySettings) error {
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
		return contract.NewValidationError("ldapWebAdminUrl must start with http:// or https://")
	}
	// Cross-field constraint: a confirmed-absent result must not outlive a
	// confirmed-existing result. Both fields are required and zero is invalid.
	if v.IDMappingNotFoundCacheTTL > v.IDMappingFoundCacheTTL {
		return contract.NewValidationError("idMappingNotFoundCacheTTL must not exceed idMappingFoundCacheTTL")
	}
	for _, entry := range v.BanAllowlist {
		if _, err := netip.ParseAddr(entry); err == nil {
			continue
		}
		if _, err := netip.ParsePrefix(entry); err == nil {
			continue
		}
		return contract.NewValidationError(fmt.Sprintf("banAllowlist entry %q must be an IP address or CIDR prefix", entry))
	}
	return nil
}

// validateAgentSettings checks agent domain fields (execBinPath, vnc, and monitor).
func validateAgentSettings(v AgentSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	if v.ExecBinPath != "" && !filepath.IsAbs(v.ExecBinPath) {
		return contract.NewValidationError("execBinPath must be an absolute path")
	}
	if v.VNC.BinDir != "" && !filepath.IsAbs(v.VNC.BinDir) {
		return contract.NewValidationError("vnc.binDir must be an absolute path")
	}
	if err := validateVNCServerArgsContent(v.VNC.ServerArgs); err != nil {
		return err
	}
	if v.Monitor.NvidiaSmiPath != "" && !filepath.IsAbs(v.Monitor.NvidiaSmiPath) {
		return contract.NewValidationError("monitor.nvidiaSmiPath must be an absolute path")
	}
	for _, dm := range v.Monitor.DiskMounts {
		if dm.Name == "" {
			return contract.NewValidationError("monitor.diskMounts: name cannot be empty")
		}
		if dm.Path == "" {
			return contract.NewValidationError("monitor.diskMounts: path cannot be empty")
		}
	}
	return nil
}

// validateOverviewSettings checks all overview domain fields.
func validateOverviewSettings(v OverviewSettings) error {
	return ValidateStructByTags(v)
}

// validateSchedulerSettings checks all scheduler domain fields.
func validateSchedulerSettings(v SchedulerSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	return validateEnvScript("scheduler.envScript", v.EnvScript)
}

// validateEnvScript checks an optional scheduler environment script path.
func validateEnvScript(field, p string) error {
	if p == "" {
		return nil
	}
	if !filepath.IsAbs(p) {
		return contract.NewValidationError(fmt.Sprintf("%s must be an absolute path", field))
	}
	if strings.ContainsAny(p, "\x00\r\n\t") {
		return contract.NewValidationError(fmt.Sprintf("%s contains invalid characters", field))
	}
	return nil
}

// validateAlertSettings checks all alert domain fields.
func validateAlertSettings(v AlertSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	// 交叉约束：启用时至少保留一个可触发维度（0 表示该维度不参与）
	if v.Memory.Enabled && v.Memory.MaxUsedPercent == 0 && v.Memory.MinFreeSpace == 0 {
		return contract.NewValidationError("alert.memory: enabled=true but both maxUsedPercent and minFreeSpace are 0")
	}
	if v.SharedStorage.Enabled && v.SharedStorage.MaxUsedPercent == 0 && v.SharedStorage.MinFreeSpace == 0 {
		return contract.NewValidationError("alert.sharedStorage: enabled=true but both maxUsedPercent and minFreeSpace are 0")
	}
	if v.Disk.Enabled && v.Disk.MaxUsedPercent == 0 && v.Disk.MinFreeSpace == 0 {
		return contract.NewValidationError("alert.disk: enabled=true but both maxUsedPercent and minFreeSpace are 0")
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
			return contract.NewValidationError("id cannot be empty")
		}
		if _, ok := seen[storage.ID]; ok {
			return contract.NewValidationError(fmt.Sprintf("duplicate id %q", storage.ID))
		}
		seen[storage.ID] = struct{}{}

		if storage.Name == "" {
			return contract.NewValidationError(fmt.Sprintf("name cannot be empty for storage %q", storage.ID))
		}
		if storage.MountPoint == "" {
			return contract.NewValidationError(fmt.Sprintf("mountPoint cannot be empty for storage %q", storage.ID))
		}
		// probeNodes 只在存储**启用**时才有意义：消费方（settings/overview）都先判
		// `!Enabled → continue` 再读它。禁用存储允许 probeNodes 为空（默认值即如此：
		// share 存储声明但未启用）。
		if storage.Enabled && len(storage.ProbeNodes) == 0 {
			return contract.NewValidationError(fmt.Sprintf("probeNodes cannot be empty for enabled storage %q", storage.ID))
		}
	}
	return nil
}

// validateDesktopSettings checks all desktop domain fields.
func validateDesktopSettings(v DesktopSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	if v.InstallerFileName != "" {
		name := strings.TrimSpace(v.InstallerFileName)
		if name != v.InstallerFileName || name == "" ||
			filepath.Base(name) != name || strings.Contains(name, "..") ||
			strings.ContainsAny(name, "/\\") || !strings.HasSuffix(strings.ToLower(name), ".exe") {
			return contract.NewValidationError("desktop.installerFileName must be a plain .exe file name without path separators")
		}
	}
	if err := ValidateMinSupportedVersion(v.MinSupportedVersion); err != nil {
		return err
	}
	return nil
}

// validateCertSettings checks the certificate lifecycle policy domain.
func validateCertSettings(v CertSettings) error {
	if err := ValidateStructByTags(v); err != nil {
		return err
	}
	// Element bounds come from validate:"min=1,max=168" on BackoffScheduleHours.
	if len(v.BackoffScheduleHours) == 0 {
		return contract.NewValidationError("cert.backoffScheduleHours must not be empty")
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
			return contract.NewValidationError("lmutilPath must be an absolute path")
		}
		if strings.ContainsAny(v.LmutilPath, "\x00\r\n\t") {
			return contract.NewValidationError("lmutilPath contains invalid characters")
		}
	}
	return nil
}

// validateVNCServerArgsContent rejects control characters in serverArgs elements.
// Element length bounds come solely from the validate:"maxLen=…" tag.
func validateVNCServerArgsContent(args []string) error {
	for _, arg := range args {
		if strings.ContainsAny(arg, "\r\n\x00") {
			return contract.NewValidationError("vnc.serverArgs element contains invalid control characters")
		}
	}
	return nil
}
