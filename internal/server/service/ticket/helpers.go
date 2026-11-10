package ticket

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"strconv"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

func isNotFound(err error) bool { return errors.Is(err, contract.ErrNotFound) }

//go:fix inline
func strPtr(s string) *string { return new(s) }

func timeStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	return new(t.Format(time.RFC3339))
}

func uintStr(v uint) string { return strconv.FormatUint(uint64(v), 10) }

func usernameOf(u *model.User) *string {
	if u == nil {
		return nil
	}
	return new(u.Username)
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func dedupe(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok || id == 0 {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
