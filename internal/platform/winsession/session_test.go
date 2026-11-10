package winsession

import "testing"

func TestParseQueryUserSessions(t *testing.T) {
	t.Parallel()
	out := ` USERNAME              SESSIONNAME        ID  STATE   IDLE TIME  LOGON TIME
>alice                 rdp-tcp#1           2  Active          .  11/6/2026 10:00 AM
 bob                   console             1  Active          .  11/6/2026 9:00 AM
 alice                                     5  Disc            .  11/6/2026 8:00 AM
`
	got := ParseQueryUserSessions(out)
	if len(got) != 3 {
		t.Fatalf("len=%d want 3: %+v", len(got), got)
	}
	if got[0].Username != "alice" || got[0].Name != "rdp-tcp#1" || got[0].ID != 2 {
		t.Fatalf("got[0]=%+v", got[0])
	}
	if got[2].Username != "alice" || got[2].Name != "" || got[2].ID != 5 {
		t.Fatalf("got[2]=%+v", got[2])
	}
	if !IsRemoteDesktopSessionName(got[0].Name) {
		t.Fatal("rdp-tcp#1 should be remote desktop")
	}
	if !IsRemoteDesktopSessionName(got[2].Name) {
		t.Fatal("blank sessionname (Disc state) should be treated as remote desktop candidate")
	}
	if IsRemoteDesktopSessionName("console") {
		t.Fatal("console must not be treated as remote desktop")
	}
}
