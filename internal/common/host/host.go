package host

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// bracketRegex matches patterns like "cn[01-11]" or "node-[1-5].local"
var bracketRegex = regexp.MustCompile(`^(.*?)\[(\d+)-(\d+)](.*)$`)

// errInvalidHost is returned by ValidateHost when the host string is malformed.
var errInvalidHost = errors.New("invalid host")

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
