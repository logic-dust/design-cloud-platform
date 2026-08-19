package common

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const userLookupTimeout = 5 * time.Second

// UserIdentity holds the resolved identity of a user.
type UserIdentity struct {
	UID   int
	GID   int
	GIDs  []int
	Home  string
	Shell string
}

// ResolveUserIdentity calls `id <username>` and `getent passwd <username>` to parse user details.
// Each external command is bounded by a 5-second timeout to prevent hanging on slow NSS/LDAP lookups.
func ResolveUserIdentity(username string) (UserIdentity, error) {
	if username == "" {
		return UserIdentity{}, fmt.Errorf("username is required")
	}
	if strings.HasPrefix(username, "-") || strings.ContainsRune(username, 0) {
		return UserIdentity{}, fmt.Errorf("invalid username %q", username)
	}

	out, err := runLookupCommand("id", username)
	if err != nil {
		return UserIdentity{}, fmt.Errorf("id command failed for user %s: %w", username, err)
	}

	// Split by whitespace: ["uid=1000(levin)", "gid=998(wheel)", "groups=998(wheel),9000(cad),..."]
	fields := strings.Fields(string(out))
	if len(fields) < 3 {
		return UserIdentity{}, fmt.Errorf("unexpected id output format: %s", string(out))
	}

	// Parse uid from fields[0] (e.g., "uid=1000(levin)")
	uid, err := parseNumericPrefix(fields[0])
	if err != nil {
		return UserIdentity{}, err
	}

	// Parse gid from fields[1] (e.g., "gid=998(wheel)")
	gid, err := parseNumericPrefix(fields[1])
	if err != nil {
		return UserIdentity{}, err
	}

	// Parse groups from fields[2] (e.g., "groups=998(wheel),9000(cad),150(wireshark)" or "组=...")
	gids, err := parseGroups(fields[2])
	if err != nil {
		return UserIdentity{}, err
	}

	// Get additional info (home, shell) via getent passwd
	getentOut, err := runLookupCommand("getent", "passwd", username)
	var home, shell string
	if err == nil {
		// Output format: username:password:uid:gid:gecos:home:shell
		parts := strings.Split(strings.TrimSpace(string(getentOut)), ":")
		if len(parts) >= 7 {
			home = parts[5]
			shell = parts[6]
		}
	}

	return UserIdentity{UID: uid, GID: gid, GIDs: gids, Home: home, Shell: shell}, nil
}

func runLookupCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), userLookupTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// parseNumericPrefix extracts the numeric value after the '=' character (if present) and before '('.
// Example: "uid=1000(levin)" returns 1000.
// Example: "用户=1000(levin)" returns 1000.
// Example: "1000(levin)" returns 1000.
func parseNumericPrefix(field string) (int, error) {
	valuePart := field
	if _, after, ok := strings.Cut(field, "="); ok {
		valuePart = after
	}
	if before, _, ok := strings.Cut(valuePart, "("); ok {
		valuePart = before
	}
	if valuePart == "" {
		return 0, fmt.Errorf("field %q contains no numeric value", field)
	}
	value, err := strconv.Atoi(valuePart)
	if err != nil {
		return 0, fmt.Errorf("parse numeric value from %q: %w", field, err)
	}
	return value, nil
}

// parseGroups extracts all numeric group IDs from a field like "groups=998(wheel),9000(cad),150(wireshark)"
// or "组=998(wheel),9000(cad),150(wireshark)".
func parseGroups(field string) ([]int, error) {
	valuePart := field
	if _, after, ok := strings.Cut(field, "="); ok {
		valuePart = after
	}
	if valuePart == "" {
		return nil, nil
	}
	entries := strings.Split(valuePart, ",")
	gids := make([]int, 0, len(entries))
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		gidPart := entry
		if before, _, ok := strings.Cut(entry, "("); ok {
			gidPart = before
		}
		if gidPart == "" {
			return nil, fmt.Errorf("group entry %q contains no numeric value", entry)
		}
		gid, err := strconv.Atoi(gidPart)
		if err != nil {
			return nil, fmt.Errorf("parse group id from %q: %w", entry, err)
		}
		gids = append(gids, gid)
	}
	return gids, nil
}
