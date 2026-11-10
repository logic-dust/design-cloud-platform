package direct

import (
	"strings"
	"testing"

	"design-cloud-engine/internal/platform/winsession"
)

func parseQueryUserSessionIDs(output, username string) []int {
	want := strings.ToLower(strings.TrimSpace(username))
	var ids []int
	for _, s := range winsession.ParseQueryUserSessions(output) {
		if strings.ToLower(s.Username) != want {
			continue
		}
		ids = append(ids, s.ID)
	}
	return ids
}

func TestParseQueryUserSessionIDs(t *testing.T) {
	t.Parallel()
	out := ` USERNAME              SESSIONNAME        ID  STATE   IDLE TIME  LOGON TIME
>alice                 rdp-tcp#1           2  Active          .  11/6/2026 10:00 AM
 bob                   console             1  Active          .  11/6/2026 9:00 AM
 alice                                     5  Disc            .  11/6/2026 8:00 AM
`
	ids := parseQueryUserSessionIDs(out, "alice")
	if len(ids) != 2 || ids[0] != 2 || ids[1] != 5 {
		t.Fatalf("ids=%v", ids)
	}
}
