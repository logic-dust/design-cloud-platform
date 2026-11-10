package host

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxHostnameLabelLen is the RFC 1123 per-label limit.
	MaxHostnameLabelLen = 63
)

// bracketRegex matches patterns like "cn[01-11]" or "node-[1-5].local"
var bracketRegex = regexp.MustCompile(`^(.*?)\[(\d+)-(\d+)](.*)$`)

// Hostname / FQDN identity names (node id, connection resource name): RFC 1123 labels, optional underscore.
var fqdnIdentityNamePattern = regexp.MustCompile(
	`^[A-Za-z0-9](?:[A-Za-z0-9_-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9_-]{0,61}[A-Za-z0-9])?)*$`,
)

// Node group: single lowercase label, max 32 runes, letter first, no trailing hyphen.
var nodeGroupLabelPattern = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

// errInvalidHost is returned by ValidateHost when the host string is malformed.
var errInvalidHost = errors.New("invalid host")

// ValidateHostnameOptions configures hostname / FQDN validation for identity names (not dialHost).
type ValidateHostnameOptions struct {
	MaxTotalLen     int  // UTF-8 runes; zero = no cap
	AllowUnderscore bool // only applies to multi-label FQDN mode
	SingleLabel     bool // node groups: no dots
	LowercaseOnly   bool // node groups
}

// ValidateHostnameOrFQDN checks Linux/RFC 1123-style hostnames and FQDNs (ASCII labels).
func ValidateHostnameOrFQDN(name string, opts ValidateHostnameOptions) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: name is required", errInvalidHost)
	}
	if strings.Contains(name, "..") ||
		strings.Contains(name, "/") ||
		strings.Contains(name, "\\") {
		return fmt.Errorf("%w: invalid hostname %q", errInvalidHost, name)
	}
	for _, r := range name {
		if unicode.IsSpace(r) {
			return fmt.Errorf("%w: hostname must not contain whitespace", errInvalidHost)
		}
	}
	if opts.MaxTotalLen > 0 && utf8.RuneCountInString(name) > opts.MaxTotalLen {
		return fmt.Errorf("%w: hostname cannot exceed %d characters", errInvalidHost, opts.MaxTotalLen)
	}

	if opts.SingleLabel && opts.LowercaseOnly {
		if !nodeGroupLabelPattern.MatchString(name) {
			return fmt.Errorf("%w: invalid group name %q", errInvalidHost, name)
		}
		return nil
	}

	if opts.SingleLabel && strings.Contains(name, ".") {
		return fmt.Errorf("%w: must be a single hostname label", errInvalidHost)
	}

	if !opts.AllowUnderscore {
		return fmt.Errorf("%w: identity names require AllowUnderscore option", errInvalidHost)
	}
	if !fqdnIdentityNamePattern.MatchString(name) {
		return fmt.Errorf("%w: invalid hostname %q", errInvalidHost, name)
	}
	return nil
}

// ValidateHost checks that a host string contains only a bare hostname,
// domain name, FQDN, or IP address — no scheme, path, port, userinfo, or whitespace.
func ValidateHost(host string) error {
	if strings.Contains(host, "://") ||
		strings.ContainsAny(host, `/\\?#`) ||
		strings.Contains(host, "@") ||
		strings.HasPrefix(host, "[") ||
		strings.HasSuffix(host, "]") {
		return fmt.Errorf("%w: host must not include scheme, path, query, fragment, userinfo, or brackets", errInvalidHost)
	}
	for _, r := range host {
		if unicode.IsSpace(r) {
			return fmt.Errorf("%w: host must not contain whitespace", errInvalidHost)
		}
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return fmt.Errorf("%w: host must not include a port", errInvalidHost)
	}
	return nil
}

// ExpandHostPattern parses a topology pattern string (e.g., "mgt01,cn[01-11]") and returns a list of individual hostnames.
func ExpandHostPattern(pattern string) ([]string, error) {
	var result []string

	// Split by comma
	parts := strings.SplitSeq(pattern, ",")
	for part := range parts {
		if part == "" {
			continue
		}

		// Try to match the bracket range pattern
		matches := bracketRegex.FindStringSubmatch(part)
		if len(matches) == 5 {
			prefix := matches[1]
			startStr := matches[2]
			endStr := matches[3]
			suffix := matches[4]

			start, err := strconv.Atoi(startStr)
			if err != nil {
				return nil, fmt.Errorf("invalid start number '%s' in pattern: %s", startStr, part)
			}
			end, err := strconv.Atoi(endStr)
			if err != nil {
				return nil, fmt.Errorf("invalid end number '%s' in pattern: %s", endStr, part)
			}

			if start > end {
				return nil, fmt.Errorf("start number is greater than end number in pattern: %s", part)
			}

			// Preserve leading zeros based on the width of the start/end string.
			// e.g., "01" has width 2, "1" has width 1.
			// We take the length of startStr to dictate formatting.
			width := len(startStr)
			format := fmt.Sprintf("%%s%%0%dd%%s", width)

			for i := start; i <= end; i++ {
				result = append(result, fmt.Sprintf(format, prefix, i, suffix))
			}
		} else {
			// No bracket range found, treat as a literal hostname
			result = append(result, part)
		}
	}

	return result, nil
}
