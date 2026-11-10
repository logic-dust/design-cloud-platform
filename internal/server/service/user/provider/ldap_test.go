package provider

import (
	"crypto/tls"
	"errors"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/platform/config"

	"github.com/go-ldap/ldap/v3"
)

// =============================================================================
// parseCNFromDN
// =============================================================================

func TestParseCNFromDN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dn   string
		want string
	}{
		{"simple", "cn=alice,ou=users,dc=example,dc=com", "alice"},
		{"uid attr", "uid=bob,ou=users,dc=example,dc=com", "bob"},
		{"no equals", "alice", ""},
		{"empty", "", ""},
		{"equals at start", "=alice", "alice"},
		{"no comma", "cn=charlie", "charlie"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCNFromDN(tc.dn)
			if got != tc.want {
				t.Errorf("parseCNFromDN(%q) = %q, want %q", tc.dn, got, tc.want)
			}
		})
	}
}

// =============================================================================
// parseMemberDN
// =============================================================================

func TestParseMemberDN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		dn     string
		idAttr string
		want   string
	}{
		{"uid match", "uid=alice,ou=users,dc=ex,dc=com", "uid", "alice"},
		{"uid match no comma", "uid=alice", "uid", "alice"},
		{"CN match", "CN=Developers,ou=groups,dc=ex,dc=com", "CN", "Developers"},
		{"case insensitive", "UID=bob,ou=users,dc=ex,dc=com", "uid", "bob"},
		{"wrong attr", "uid=alice,ou=users,dc=ex,dc=com", "cn", ""},
		{"no equals", "alice", "uid", ""},
		{"empty", "", "uid", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMemberDN(tc.dn, tc.idAttr)
			if got != tc.want {
				t.Errorf("parseMemberDN(%q, %q) = %q, want %q",
					tc.dn, tc.idAttr, got, tc.want)
			}
		})
	}
}

// =============================================================================
// mapLDAPBindError
// =============================================================================

func TestMapLDAPBindError_Nil(t *testing.T) {
	t.Parallel()
	if err := mapLDAPBindError(nil); err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestMapLDAPBindError_InvalidCredentials(t *testing.T) {
	t.Parallel()
	err := mapLDAPBindError(ldap.NewError(ldap.LDAPResultInvalidCredentials, errors.New("invalid credentials")))
	if !errors.Is(err, ErrUserPasswordIncorrect) {
		t.Errorf("expected ErrUserPasswordIncorrect, got: %v", err)
	}
}

func TestMapLDAPBindError_InvalidCredentialsExpired(t *testing.T) {
	t.Parallel()
	err := mapLDAPBindError(ldap.NewError(ldap.LDAPResultInvalidCredentials,
		errors.New("data 532 password expired")))
	if !errors.Is(err, ErrUserPasswordExpired) {
		t.Errorf("expected ErrUserPasswordExpired, got: %v", err)
	}
}

func TestMapLDAPBindError_NoSuchObject(t *testing.T) {
	t.Parallel()
	err := mapLDAPBindError(ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("no such object")))
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected ErrUserNotFound, got: %v", err)
	}
}

func TestMapLDAPBindError_UnwillingPasswordExpired(t *testing.T) {
	t.Parallel()
	err := mapLDAPBindError(ldap.NewError(ldap.LDAPResultUnwillingToPerform,
		errors.New("password expired")))
	if !errors.Is(err, ErrUserPasswordExpired) {
		t.Errorf("expected ErrUserPasswordExpired, got: %v", err)
	}
}

func TestMapLDAPBindError_PlainErrorMatching(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		wantIs error
	}{
		{"password expired data 532", errors.New("data 532"), ErrUserPasswordExpired},
		{"password expired text", errors.New("password Expired"), ErrUserPasswordExpired},
		{"invalid credentials text", errors.New("invalid credentials"), ErrUserPasswordIncorrect},
		{"no such object text", errors.New("no such object"), ErrUserNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapLDAPBindError(tc.err)
			if !errors.Is(got, tc.wantIs) {
				t.Errorf("expected %v, got: %v", tc.wantIs, got)
			}
		})
	}
}

func TestMapLDAPBindError_Unknown(t *testing.T) {
	t.Parallel()
	got := mapLDAPBindError(errors.New("something went completely wrong"))
	if got == nil {
		t.Error("expected non-nil error for unknown error")
	}
	if errors.Is(got, ErrUserNotFound) || errors.Is(got, ErrUserPasswordIncorrect) || errors.Is(got, ErrUserPasswordExpired) {
		t.Errorf("unexpected sentinel match for unknown error: %v", got)
	}
}

// =============================================================================
// entryToSystemUser
// =============================================================================

func TestEntryToSystemUser(t *testing.T) {
	t.Parallel()

	p := &ldapProvider{cfg: config.LdapConfig{
		UserBaseDN:  "ou=users,dc=example,dc=com",
		GroupBaseDN: "ou=groups,dc=example,dc=com",
	}}

	entry := &ldap.Entry{
		Attributes: []*ldap.EntryAttribute{
			{Name: "uid", Values: []string{"alice"}},
			{Name: "uidNumber", Values: []string{"1001"}},
			{Name: "gidNumber", Values: []string{"1001"}},
			{Name: "gecos", Values: []string{"Alice Smith"}},
			{Name: "homeDirectory", Values: []string{"/home/alice"}},
			{Name: "loginShell", Values: []string{"/bin/bash"}},
			{Name: "memberOf", Values: []string{
				"cn=developers,ou=groups,dc=example,dc=com",
				"cn=admins,ou=groups,dc=example,dc=com",
				"cn=other,ou=elsewhere,dc=example,dc=com", // different base DN
			}},
			{Name: "krbPasswordExpiration", Values: []string{"20260718125409Z"}},
		},
	}

	u := p.entryToSystemUser(entry)

	if u.Username != "alice" {
		t.Errorf("Username = %q, want alice", u.Username)
	}
	if u.Uid != "1001" {
		t.Errorf("Uid = %q, want 1001", u.Uid)
	}
	if u.Gid != "1001" {
		t.Errorf("Gid = %q, want 1001", u.Gid)
	}
	if u.Comment != "Alice Smith" {
		t.Errorf("Comment = %q, want 'Alice Smith'", u.Comment)
	}
	if u.Home != "/home/alice" {
		t.Errorf("Home = %q", u.Home)
	}
	if u.Shell != "/bin/bash" {
		t.Errorf("Shell = %q", u.Shell)
	}
	if u.Status != "active" {
		t.Errorf("Status = %q, want active", u.Status)
	}
	if u.PasswordExpiresAt == nil {
		t.Fatal("PasswordExpiresAt = nil, want parsed time")
	}
	if want := "2026-07-18 12:54:09 +0000 UTC"; u.PasswordExpiresAt.UTC().String() != want {
		t.Errorf("PasswordExpiresAt = %v, want %s", u.PasswordExpiresAt.UTC(), want)
	}
	// Only groups with matching groupBaseDN should be included
	if len(u.Groups) != 2 {
		t.Errorf("Groups count = %d, want 2 (only matching base DN)", len(u.Groups))
	}
	if len(u.Groups) > 0 && u.Groups[0] != "developers" {
		t.Errorf("Groups[0] = %q, want developers", u.Groups[0])
	}
}

func TestEntryToSystemUser_NoMemberOf(t *testing.T) {
	t.Parallel()

	p := &ldapProvider{cfg: config.LdapConfig{}}
	entry := &ldap.Entry{
		Attributes: []*ldap.EntryAttribute{
			{Name: "uid", Values: []string{"bob"}},
		},
	}
	u := p.entryToSystemUser(entry)
	if u.Username != "bob" {
		t.Errorf("Username = %q, want bob", u.Username)
	}
	if len(u.Groups) != 0 {
		t.Errorf("Groups = %v, want empty", u.Groups)
	}
	if u.PasswordExpiresAt != nil {
		t.Errorf("PasswordExpiresAt = %v, want nil when attribute absent", u.PasswordExpiresAt)
	}
}

// =============================================================================
// parsePasswordExpiration
// =============================================================================

func TestParsePasswordExpiration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		attrs    []*ldap.EntryAttribute
		wantTime string // expected UTC string, empty means nil
	}{
		{
			name:  "absent attribute → nil",
			attrs: []*ldap.EntryAttribute{{Name: "uid", Values: []string{"alice"}}},
		},
		{
			name:     "krbPasswordExpiration valid",
			attrs:    []*ldap.EntryAttribute{{Name: "krbPasswordExpiration", Values: []string{"20260718125409Z"}}},
			wantTime: "2026-07-18 12:54:09 +0000 UTC",
		},
		{
			name:     "pwdExpirationTime fallback",
			attrs:    []*ldap.EntryAttribute{{Name: "pwdExpirationTime", Values: []string{"20300101120000Z"}}},
			wantTime: "2030-01-01 12:00:00 +0000 UTC",
		},
		{
			name:     "fractional seconds stripped",
			attrs:    []*ldap.EntryAttribute{{Name: "krbPasswordExpiration", Values: []string{"20260718125409.123Z"}}},
			wantTime: "2026-07-18 12:54:09 +0000 UTC",
		},
		{
			name:  "invalid format → nil",
			attrs: []*ldap.EntryAttribute{{Name: "krbPasswordExpiration", Values: []string{"not-a-time"}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parsePasswordExpiration(&ldap.Entry{Attributes: tc.attrs})
			if tc.wantTime == "" {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected parsed time, got nil")
			}
			if got.UTC().String() != tc.wantTime {
				t.Errorf("got %v, want %s", got.UTC(), tc.wantTime)
			}
		})
	}
}

func TestPasswordExpired(t *testing.T) {
	t.Parallel()

	past := time.Now().Add(-24 * time.Hour).UTC().Format("20060102150405Z")
	future := time.Now().Add(24 * time.Hour).UTC().Format("20060102150405Z")

	tests := []struct {
		name  string
		attrs []*ldap.EntryAttribute
		want  bool
	}{
		{"absent attribute → false", []*ldap.EntryAttribute{{Name: "uid", Values: []string{"alice"}}}, false},
		{"expiration in past → true", []*ldap.EntryAttribute{{Name: "krbPasswordExpiration", Values: []string{past}}}, true},
		{"expiration in future → false", []*ldap.EntryAttribute{{Name: "krbPasswordExpiration", Values: []string{future}}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := passwordExpired(&ldap.Entry{Attributes: tc.attrs}); got != tc.want {
				t.Errorf("passwordExpired = %v, want %v", got, tc.want)
			}
		})
	}
}

// =============================================================================
// ldapDial & DI
// =============================================================================

func TestLdapDial_NotConfigured(t *testing.T) {
	t.Parallel()

	p := &ldapProvider{cfg: config.LdapConfig{Server: ""}}
	_, err := p.ldapDial()
	if err == nil {
		t.Error("expected error for unconfigured ldap server")
	}
}

func TestLdapDial_CustomDialFn(t *testing.T) {
	t.Parallel()

	called := false
	p := &ldapProvider{
		cfg: config.LdapConfig{Server: "ldap://localhost:389"},
		dialFn: func(url string, _ *tls.Config) (*ldap.Conn, error) {
			called = true
			if url != "ldap://localhost:389" {
				t.Errorf("url = %q, want ldap://localhost:389", url)
			}
			return nil, errors.New("simulated dial failure")
		},
	}
	_, err := p.ldapDial()
	if !called {
		t.Error("expected custom dialFn to be called")
	}
	if err == nil || !strings.Contains(err.Error(), "simulated dial failure") {
		t.Errorf("unexpected error: %v", err)
	}
}

// =============================================================================
// buildIDNumberFilter & idNameMap
// =============================================================================

func TestBuildIDNumberFilter(t *testing.T) {
	t.Parallel()

	base := "(objectClass=posixAccount)"

	t.Run("empty", func(t *testing.T) {
		if got := buildIDNumberFilter("uidNumber", nil, base); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})

	t.Run("single_no_or", func(t *testing.T) {
		got := buildIDNumberFilter("uidNumber", []uint32{1000}, base)
		want := "(&(uidNumber=1000)(objectClass=posixAccount))"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if strings.Contains(got, "|") {
			t.Fatalf("single-id filter must not contain |: %q", got)
		}
	})

	t.Run("multi_or", func(t *testing.T) {
		got := buildIDNumberFilter("uidNumber", []uint32{10001, 10002}, base)
		want := "(&(|(uidNumber=10001)(uidNumber=10002))(objectClass=posixAccount))"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("chunk_boundary_sizes", func(t *testing.T) {
		for _, n := range []int{1, 49, 50, 51, 200} {
			ids := make([]uint32, n)
			for i := range ids {
				ids[i] = uint32(1000 + i)
			}
			chunk := ids
			if n > idLookupChunk {
				chunk = ids[:idLookupChunk]
			}
			got := buildIDNumberFilter("uidNumber", chunk, base)
			if len(chunk) == 1 && strings.Contains(got, "|") {
				t.Fatalf("chunk size 1 must not contain |: %q", got)
			}
			if len(chunk) > 1 && !strings.Contains(got, "|") {
				t.Fatalf("chunk size %d must contain |: %q", len(chunk), got)
			}
			if !strings.Contains(got, "objectClass=posixAccount") {
				t.Fatalf("filter must preserve base filter: %q", got)
			}
		}
	})
}

func makeLDAPEntry(attrs map[string][]string) *ldap.Entry {
	e := &ldap.Entry{}
	for name, values := range attrs {
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: name, Values: values})
	}
	return e
}

func TestIDNameMap_Normal(t *testing.T) {
	t.Parallel()
	got := idNameMap([]*ldap.Entry{
		makeLDAPEntry(map[string][]string{"uidNumber": {"1000"}, "uid": {"alice"}}),
		makeLDAPEntry(map[string][]string{"uidNumber": {"1001"}, "uid": {"bob"}}),
	}, "uidNumber", "uid")
	if len(got) != 2 || got[1000] != "alice" || got[1001] != "bob" {
		t.Fatalf("unexpected map: %v", got)
	}
}

func TestIDNameMap_DuplicateIDOverwrites(t *testing.T) {
	t.Parallel()
	got := idNameMap([]*ldap.Entry{
		makeLDAPEntry(map[string][]string{"uidNumber": {"904300001"}, "uid": {"dup1"}}),
		makeLDAPEntry(map[string][]string{"uidNumber": {"904300001"}, "uid": {"dup2"}}),
	}, "uidNumber", "uid")
	if len(got) != 1 {
		t.Fatalf("expected 1 map entry, got %v", got)
	}
	if _, ok := got[904300001]; !ok {
		t.Fatalf("expected id 904300001 present, got %v", got)
	}
}

func TestIDNameMap_SkipsIncompleteEntries(t *testing.T) {
	t.Parallel()
	got := idNameMap([]*ldap.Entry{
		makeLDAPEntry(map[string][]string{"uid": {"no-uidnumber"}}),
		makeLDAPEntry(map[string][]string{"uidNumber": {"1000"}}),
		makeLDAPEntry(map[string][]string{"uidNumber": {"abc"}, "uid": {"bad-number"}}),
		makeLDAPEntry(map[string][]string{"uidNumber": {"1002"}, "uid": {""}}),
		makeLDAPEntry(map[string][]string{"uidNumber": {"1003"}, "uid": {"valid"}}),
	}, "uidNumber", "uid")
	if len(got) != 1 || got[1003] != "valid" {
		t.Fatalf("skips incomplete entries: got %v", got)
	}
}

func TestIDNameMap_GroupsUseCN(t *testing.T) {
	t.Parallel()
	got := idNameMap([]*ldap.Entry{
		makeLDAPEntry(map[string][]string{"gidNumber": {"2000"}, "cn": {"developers"}}),
	}, "gidNumber", "cn")
	if len(got) != 1 || got[2000] != "developers" {
		t.Fatalf("groups by cn: got %v", got)
	}
}
