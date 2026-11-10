package schema

import (
	"cmp"
	"design-cloud-engine/internal/contract"
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// ErrMalformed 表示请求体 JSON 语法/结构错误（语法层，HTTP 400）。
// 与 contract.NewValidationError（业务规则校验失败，HTTP 422）区分：
// 400 只留给语法/绑定错误，业务校验一律 422。
var ErrMalformed = errors.New("malformed request body")

// Domain represents a runtime settings category.
type Domain string

const (
	DomainPlatform   Domain = "platform"
	DomainAuth       Domain = "auth"
	DomainConnection Domain = "connection"
	DomainFile       Domain = "file"
	DomainOverview   Domain = "overview"
	DomainStorage    Domain = "storage"
	DomainScheduler  Domain = "scheduler"
	DomainLicense    Domain = "license"
	DomainAgent      Domain = "agent"
	DomainAlert      Domain = "alert"
	DomainDesktop    Domain = "desktop"
	DomainCert       Domain = "cert"
)

var allDomains = []Domain{
	DomainPlatform,
	DomainAuth,
	DomainConnection,
	DomainFile,
	DomainOverview,
	DomainStorage,
	DomainScheduler,
	DomainLicense,
	DomainAgent,
	DomainAlert,
	DomainDesktop,
	DomainCert,
}

var domainByName = map[string]Domain{
	string(DomainPlatform):   DomainPlatform,
	string(DomainAuth):       DomainAuth,
	string(DomainConnection): DomainConnection,
	string(DomainFile):       DomainFile,
	string(DomainOverview):   DomainOverview,
	string(DomainStorage):    DomainStorage,
	string(DomainScheduler):  DomainScheduler,
	string(DomainLicense):    DomainLicense,
	string(DomainAgent):      DomainAgent,
	string(DomainAlert):      DomainAlert,
	string(DomainDesktop):    DomainDesktop,
	string(DomainCert):       DomainCert,
}

// GetAllDomains returns a list of all available domains.
func GetAllDomains() []string {
	infos := make([]string, 0, len(allDomains))
	for _, d := range allDomains {
		infos = append(infos, string(d))
	}
	return infos
}

// String returns the string representation of Domain.
func (d Domain) String() string { return string(d) }

// ParseDomain converts a route or storage domain string into a known settings domain.
func ParseDomain(value string) (Domain, bool) {
	domain, ok := domainByName[value]
	return domain, ok
}

// AllDomains returns the supported runtime settings domains.
func AllDomains() []Domain {
	return append([]Domain(nil), allDomains...)
}

// decodeDomainPayload decodes a domain JSON object, rejecting unknown fields.
//
// 不校验"缺字段"：设置更新是叠加语义——未携带的字段保持库中现值，
// 因此 payload 只需携带真正要修改的字段（见 MergeDomainPayload / UpdateByDomain）。
func decodeDomainPayload[T any](raw json.RawMessage) (T, error) {
	var zero T

	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		// JSON 语法/结构错误 → ErrMalformed（HTTP 400），与业务校验失败（422）区分。
		return zero, fmt.Errorf("%w: %s", ErrMalformed, err)
	}

	expected := jsonFieldNames[T]()
	var unknown []string
	for field := range body {
		if slices.Contains(expected, field) {
			continue
		}
		unknown = append(unknown, field)
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return zero, contract.NewValidationError(fmt.Sprintf("unknown fields: %s", strings.Join(unknown, ", ")))
	}

	var value T
	if err := json.UnmarshalStrict(raw, &value); err != nil {
		return zero, contract.NewValidationError(err.Error(), err)
	}

	return value, nil
}

func jsonFieldNames[T any]() []string {
	return slices.Collect(maps.Keys(knownFieldTypes(reflect.TypeFor[T]())))
}

// knownFieldTypes 返回结构体各字段的 JSON 名 → 字段类型。JSON 名规则与
// jsonFieldNames 一致（`json:"-"` 跳过；空名回退字段名）。
// 非结构体返回 nil。
func knownFieldTypes(typ reflect.Type) map[string]reflect.Type {
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == nil || typ.Kind() != reflect.Struct {
		return nil
	}
	out := make(map[string]reflect.Type, typ.NumField())
	for field := range typ.Fields() {
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		out[cmp.Or(name, field.Name)] = field.Type
	}
	return out
}

// DecodeForStore strictly decodes and validates a domain payload for persistence.
func DecodeForStore(domain Domain, raw json.RawMessage) (any, error) {
	switch domain {
	case DomainPlatform:
		v, err := decodeDomainPayload[PlatformSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validatePlatformSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	case DomainAuth:
		v, err := decodeDomainPayload[AuthSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validateAuthSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	case DomainConnection:
		v, err := decodeDomainPayload[ConnectionSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validateConnectionSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	case DomainFile:
		v, err := decodeDomainPayload[FileSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validateFileSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	case DomainOverview:
		v, err := decodeDomainPayload[OverviewSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validateOverviewSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	case DomainStorage:
		v, err := decodeDomainPayload[StorageSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validateStorageSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	case DomainScheduler:
		v, err := decodeDomainPayload[SchedulerSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validateSchedulerSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	case DomainLicense:
		v, err := decodeDomainPayload[LicenseSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validateLicenseSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	case DomainAgent:
		v, err := decodeDomainPayload[AgentSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validateAgentSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	case DomainAlert:
		v, err := decodeDomainPayload[AlertSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validateAlertSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	case DomainDesktop:
		v, err := decodeDomainPayload[DesktopSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validateDesktopSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	case DomainCert:
		v, err := decodeDomainPayload[CertSettings](raw)
		if err != nil {
			return nil, err
		}
		if err := validateCertSettings(v); err != nil {
			return nil, err
		}
		return v, nil
	default:
		return nil, nil
	}
}
