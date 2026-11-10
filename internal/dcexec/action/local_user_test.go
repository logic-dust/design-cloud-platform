//go:build !windows

package action

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"design-cloud-engine/internal/execution/payload"

	"design-cloud-engine/internal/execution"
)

func TestValidateIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid simple", "testuser", false},
		{"valid with dash", "test-user", false},
		{"valid with underscore", "test_user", false},
		{"valid with dot", "test.user", false},
		{"empty", "", true},
		{"starts with dash", "-test", true},
		{"contains space", "test user", true},
		{"contains tab", "test\tuser", true},
		{"contains newline", "test\nuser", true},
		{"contains null", "test\x00user", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateIdentifier("test_label", tc.value)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateIdentifier(%q) error = %v, wantErr = %v", tc.value, err, tc.wantErr)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid", "secureP@ss123", false},
		{"empty", "", true},
		{"too long", string(make([]byte, 257)), true},
		{"contains null", "pass\x00word", true},
		{"contains newline", "pass\nword", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePassword(tc.value)
			if (err != nil) != tc.wantErr {
				t.Errorf("validatePassword(%q) error = %v, wantErr = %v", tc.value, err, tc.wantErr)
			}
		})
	}
}

func TestValidatePassword_Exactly256(t *testing.T) {
	t.Parallel()

	// Create a 256-byte string of printable chars (not null bytes).
	b := make([]byte, 256)
	for i := range b {
		b[i] = 'a'
	}
	err := validatePassword(string(b))
	if err != nil {
		t.Errorf("password of exactly 256 bytes should be valid: %v", err)
	}
}

func TestParseShadowLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		line    string
		want    *shadowEntry
		wantErr bool
	}{
		{
			name: "valid line",
			line: "user:$6$salt$hash:19000:0:99999:7:::",
			want: &shadowEntry{
				Username:       "user",
				HashedPassword: "$6$salt$hash",
				ExpireDate:     "",
			},
		},
		{
			name: "locked account (!)",
			line: "user:!:19000:0:99999:7:::",
			want: &shadowEntry{
				Username:       "user",
				HashedPassword: "!",
				ExpireDate:     "",
			},
		},
		{
			name: "disabled account (*)",
			line: "user:*:19000:0:99999:7:::",
			want: &shadowEntry{
				Username:       "user",
				HashedPassword: "*",
				ExpireDate:     "",
			},
		},
		{
			name: "expired account with date",
			line: "user:!:19000:0:99999:7::20000:",
			want: &shadowEntry{
				Username:       "user",
				HashedPassword: "!",
				ExpireDate:     "20000",
			},
		},
		{
			name:    "too few fields",
			line:    "user:x:19000",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseShadowLine(tc.line)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseShadowLine error = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got.Username != tc.want.Username {
				t.Errorf("Username = %q, want %q", got.Username, tc.want.Username)
			}
			if got.HashedPassword != tc.want.HashedPassword {
				t.Errorf("HashedPassword = %q, want %q", got.HashedPassword, tc.want.HashedPassword)
			}
			if got.ExpireDate != tc.want.ExpireDate {
				t.Errorf("ExpireDate = %q, want %q", got.ExpireDate, tc.want.ExpireDate)
			}
		})
	}
}

func TestGetAccountStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		entry  *shadowEntry
		wantOk bool
		reason string
	}{
		{
			name: "active with hash",
			entry: &shadowEntry{
				HashedPassword: "$6$salt$hash",
				ExpireDate:     "",
			},
			wantOk: true,
			reason: "active",
		},
		{
			name: "no password",
			entry: &shadowEntry{
				HashedPassword: "",
				ExpireDate:     "",
			},
			wantOk: false,
			reason: "no_password_set",
		},
		{
			name: "locked with !",
			entry: &shadowEntry{
				HashedPassword: "!",
				ExpireDate:     "",
			},
			wantOk: false,
			reason: "account_locked",
		},
		{
			name: "disabled with *",
			entry: &shadowEntry{
				HashedPassword: "*",
				ExpireDate:     "",
			},
			wantOk: false,
			reason: "account_disabled",
		},
		{
			name: "not allowed to login with !*",
			entry: &shadowEntry{
				HashedPassword: "!*",
				ExpireDate:     "",
			},
			wantOk: false,
			reason: "account_not_allowed_to_login",
		},
		{
			name: "expired account",
			entry: &shadowEntry{
				HashedPassword: "$6$salt$hash",
				ExpireDate:     "1", // epoch day 1, definitely in the past
			},
			wantOk: false,
			reason: "account_expired",
		},
		{
			name: "expire date 0 (no expiry)",
			entry: &shadowEntry{
				HashedPassword: "$6$salt$hash",
				ExpireDate:     "0",
			},
			wantOk: true,
			reason: "active",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, isActive := getAccountStatus(tc.entry)
			if isActive != tc.wantOk {
				t.Errorf("isActive = %v, want %v", isActive, tc.wantOk)
			}
			if status != tc.reason {
				t.Errorf("status = %q, want %q", status, tc.reason)
			}
		})
	}
}

func TestVerifyPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		password       string
		hashedPassword string
		want           bool
	}{
		{
			name:           "too short hash",
			password:       "test",
			hashedPassword: "$",
			want:           false,
		},
		{
			name:           "unknown algorithm",
			password:       "test",
			hashedPassword: "$2$unknown",
			want:           false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := verifyPassword(tc.password, tc.hashedPassword)
			if got != tc.want {
				t.Errorf("verifyPassword = %v, want %v", got, tc.want)
			}
		})
	}
}

// Not adding a timing test, but verifyPassword for known algorithms (SHA-256/SHA-512/yescrypt)
// requires crypt/hash libraries that are computationally verified in integration tests.
// Here we test only the guard clauses.
func TestVerifyPassword_ShortHash(t *testing.T) {
	t.Parallel()

	if verifyPassword("anything", "") {
		t.Error("empty hash should return false")
	}
	if verifyPassword("anything", "ab") {
		t.Error("2-char hash should return false")
	}
	if verifyPassword("anything", "$") {
		t.Error("1-char hash should return false")
	}
}

func TestParseShadowLine_EmptyString(t *testing.T) {
	t.Parallel()

	_, err := parseShadowLine("")
	if err == nil {
		t.Error("expected error for empty shadow line")
	}
}

func TestGetAccountStatus_NotExpiredFuture(t *testing.T) {
	t.Parallel()

	farFutureDays := time.Now().Unix()/86400 + 36500 // ~100 years
	entry := &shadowEntry{
		HashedPassword: "$6$salt$hash",
		ExpireDate:     strconv.FormatInt(farFutureDays, 10),
	}
	_, isActive := getAccountStatus(entry)
	if !isActive {
		t.Error("account with future expire date should be active")
	}
}

// =============================================================================
// validateTargetUser
// =============================================================================

func TestValidateTargetUser_Root(t *testing.T) {
	t.Parallel()
	err := validateTargetUser("root")
	if err == nil {
		t.Error("expected error for root user")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("expected root-related error, got: %v", err)
	}
}

func TestValidateTargetUser_BadIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
	}{
		{"empty", ""},
		{"contains space", "bad user"},
		{"starts with dash", "-test"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTargetUser(tc.username)
			if err == nil {
				t.Errorf("expected error for username %q", tc.username)
			}
		})
	}
}

// =============================================================================
// HandleVerifyUser
// =============================================================================

func TestHandleVerifyUser_EmptyParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		payload  payload.UserPasswordVerify
		wantErr  bool
		errMatch string
	}{
		{
			name:    "empty username",
			payload: payload.UserPasswordVerify{Username: "", Password: "secret"},
			wantErr: true,
		},
		{
			name:    "empty password",
			payload: payload.UserPasswordVerify{Username: "alice", Password: ""},
			wantErr: true,
		},
		{
			name:    "both empty",
			payload: payload.UserPasswordVerify{Username: "", Password: ""},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := execution.NewExecuteRequest(execution.ActionUserPasswordVerify, "test-verify",
				execution.WithPayload(tc.payload))
			_, err := HandleVerifyUser(context.Background(), req)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// =============================================================================
// readShadowEntry (using temp file)
// =============================================================================

func writeTempShadow(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write temp shadow: %v", err)
	}
	return path
}

func TestReadShadowEntry_Found(t *testing.T) {
	path := writeTempShadow(t, "alice:$6$salt$hash:19000:0:99999:7:::\n"+
		"bob:!:19000:0:99999:7:::\n")
	orig := shadowFilePath
	shadowFilePath = path
	t.Cleanup(func() { shadowFilePath = orig })

	entry, err := readShadowEntry("alice")
	if err != nil {
		t.Fatalf("expected to find alice: %v", err)
	}
	if entry.Username != "alice" {
		t.Errorf("Username = %q, want alice", entry.Username)
	}
	if entry.HashedPassword != "$6$salt$hash" {
		t.Errorf("HashedPassword = %q", entry.HashedPassword)
	}
}

func TestReadShadowEntry_NotFound(t *testing.T) {
	path := writeTempShadow(t, "alice:x:19000:0:99999:7:::\n")
	orig := shadowFilePath
	shadowFilePath = path
	t.Cleanup(func() { shadowFilePath = orig })

	_, err := readShadowEntry("bob")
	if !errors.Is(err, errUserNotFoundInShadow) {
		t.Errorf("expected errUserNotFoundInShadow, got: %v", err)
	}
}

func TestReadShadowEntry_EmptyFile(t *testing.T) {
	path := writeTempShadow(t, "")
	orig := shadowFilePath
	shadowFilePath = path
	t.Cleanup(func() { shadowFilePath = orig })

	_, err := readShadowEntry("anyone")
	if !errors.Is(err, errUserNotFoundInShadow) {
		t.Errorf("expected errUserNotFoundInShadow, got: %v", err)
	}
}

func TestReadShadowEntry_FileNotFound(t *testing.T) {
	orig := shadowFilePath
	shadowFilePath = "/nonexistent/shadow/file"
	t.Cleanup(func() { shadowFilePath = orig })

	_, err := readShadowEntry("anyone")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

// =============================================================================
// authenticateLocalUser (using temp shadow file)
// =============================================================================

func TestAuthenticateLocalUser_NotFound(t *testing.T) {
	path := writeTempShadow(t, "alice:$6$salt$hash:19000:0:99999:7:::\n")
	orig := shadowFilePath
	shadowFilePath = path
	t.Cleanup(func() { shadowFilePath = orig })

	ok, reason := authenticateLocalUser("bob", "any")
	if ok {
		t.Error("expected false for unknown user")
	}
	if reason != "user not found" {
		t.Errorf("reason = %q, want 'user not found'", reason)
	}
}

func TestAuthenticateLocalUser_NoPassword(t *testing.T) {
	path := writeTempShadow(t, "alice::19000:0:99999:7:::\n")
	orig := shadowFilePath
	shadowFilePath = path
	t.Cleanup(func() { shadowFilePath = orig })

	ok, reason := authenticateLocalUser("alice", "any")
	if ok {
		t.Error("expected false for no-password account")
	}
	if reason != "no_password_set" {
		t.Errorf("reason = %q, want 'no_password_set'", reason)
	}
}

func TestAuthenticateLocalUser_Locked(t *testing.T) {
	path := writeTempShadow(t, "alice:!:19000:0:99999:7:::\n")
	orig := shadowFilePath
	shadowFilePath = path
	t.Cleanup(func() { shadowFilePath = orig })

	ok, reason := authenticateLocalUser("alice", "any")
	if ok {
		t.Error("expected false for locked account")
	}
	if reason != "account_locked" {
		t.Errorf("reason = %q, want 'account_locked'", reason)
	}
}

func TestAuthenticateLocalUser_Disabled(t *testing.T) {
	path := writeTempShadow(t, "alice:*:19000:0:99999:7:::\n")
	orig := shadowFilePath
	shadowFilePath = path
	t.Cleanup(func() { shadowFilePath = orig })

	ok, reason := authenticateLocalUser("alice", "any")
	if ok {
		t.Error("expected false for disabled account")
	}
	if reason != "account_disabled" {
		t.Errorf("reason = %q, want 'account_disabled'", reason)
	}
}

func TestAuthenticateLocalUser_NotAllowedToLogin(t *testing.T) {
	path := writeTempShadow(t, "alice:!*:19000:0:99999:7:::\n")
	orig := shadowFilePath
	shadowFilePath = path
	t.Cleanup(func() { shadowFilePath = orig })

	ok, reason := authenticateLocalUser("alice", "any")
	if ok {
		t.Error("expected false for !* account")
	}
	if reason != "account_not_allowed_to_login" {
		t.Errorf("reason = %q, want 'account_not_allowed_to_login'", reason)
	}
}

func TestAuthenticateLocalUser_Expired(t *testing.T) {
	path := writeTempShadow(t, "alice:$6$salt$hash:19000:0:99999:7::1:\n")
	orig := shadowFilePath
	shadowFilePath = path
	t.Cleanup(func() { shadowFilePath = orig })

	ok, reason := authenticateLocalUser("alice", "any")
	if ok {
		t.Error("expected false for expired account")
	}
	if reason != "account_expired" {
		t.Errorf("reason = %q, want 'account_expired'", reason)
	}
}

func TestAuthenticateLocalUser_WrongPassword(t *testing.T) {
	path := writeTempShadow(t, "alice:$6$salt$hash:19000:0:99999:7:::\n")
	orig := shadowFilePath
	shadowFilePath = path
	t.Cleanup(func() { shadowFilePath = orig })

	ok, reason := authenticateLocalUser("alice", "wrongpassword")
	if ok {
		t.Error("expected false for wrong password")
	}
	if reason != "password incorrect" {
		t.Errorf("reason = %q, want 'password incorrect'", reason)
	}
}
