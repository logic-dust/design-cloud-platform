package provider

import (
	"slices"
	"strconv"
	"strings"
)

func parseOptionalGid(raw string) *uint32 {
	id, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil {
		return nil
	}
	gid := uint32(id)
	return &gid
}

func normalizeMembers(members []string) []string {
	if len(members) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(members))
	out := make([]string, 0, len(members))
	for _, m := range members {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	slices.Sort(out)
	return out
}

func sortGroupsByName(groups []GroupInfo) {
	slices.SortFunc(groups, func(a, b GroupInfo) int {
		return strings.Compare(a.Name, b.Name)
	})
}

// memberIndex builds username → group names from a group directory.
func memberIndex(groups []GroupInfo) map[string][]string {
	out := make(map[string][]string)
	for _, g := range groups {
		for _, m := range g.Members {
			out[m] = append(out[m], g.Name)
		}
	}
	for user, names := range out {
		slices.Sort(names)
		out[user] = names
	}
	return out
}
