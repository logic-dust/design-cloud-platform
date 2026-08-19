//go:build !windows

// Package action provides handlers for elevated operations executed within the launcher (running as root).
//
// This file (local_user.go) implements write or secure operations for local Unix users and groups
// (e.g. updating passwords via chpasswd, or verifying passwords against /etc/shadow).
// Because these require root privileges, they must be executed via the launcher.
// These actions are only used by the Local user provider.
package action

import (
	"bufio"
	"cmp"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"
	"unicode"

	"design-cloud-engine/internal/common/crypto"
	"design-cloud-engine/internal/execution"
	"design-cloud-engine/internal/execution/payload"

	"github.com/openwall/yescrypt-go"
)

const minUID = 1000

// shadowFilePath is the path to the shadow file, overridable in tests.
var shadowFilePath = "/etc/shadow"

// =============================================================================
// User/Group Handlers
// =============================================================================

// HandleUserChangePassword user change the own password by verifying the old one.
func HandleUserChangePassword(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.UserPasswordChange](req)
	if err != nil {
		return "", err
	}

	// Security & Validation
	if err := validateTargetUser(p.Username); err != nil {
		return "", err
	}

	// Validate both passwords
	if p.Password == "" {
		return "", fmt.Errorf("current password is required")
	}
	if err := validatePassword(p.NewPassword); err != nil { // Validate complexity of new password
		return "", fmt.Errorf("invalid new password: %w", err)
	}

	// 1. Verify Old Password
	success, reason := authenticateLocalUser(p.Username, p.Password)
	if !success {
		return "", fmt.Errorf("authentication failed: %s", reason)
	}

	// 2. Set New Password
	return setUserPassword(ctx, p.Username, p.NewPassword)
}

// HandleUserResetPassword allows an admin to reset a user's password (needed root)
func HandleUserResetPassword(ctx context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.UserPasswordReset](req)
	if err != nil {
		return "", err
	}

	// Security & Validation
	if err := validateTargetUser(p.Username); err != nil {
		return "", err
	}

	if err := validatePassword(p.Password); err != nil {
		return "", fmt.Errorf("invalid password: %w", err)
	}

	return setUserPassword(ctx, p.Username, p.Password)
}

func setUserPassword(ctx context.Context, username, newPassword string) (string, error) {
	input := fmt.Sprintf("%s:%s", username, newPassword)
	output, err := runCommand(ctx, nil, "chpasswd", input)
	if err != nil {
		return "", fmt.Errorf("failed to update password: %w", err)
	}
	// Output is usually empty for success, or errors are caught by runCommand's err check
	_ = output
	return "Password updated successfully", nil
}

// =============================================================================
// Validation Functions (merged from utils.go)
// =============================================================================

func validateIdentifier(label, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", label)
		}
		if unicode.IsSpace(r) {
			return fmt.Errorf("%s cannot contain whitespace", label)
		}
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s cannot start with '-'", label)
	}
	return nil
}

func validatePassword(value string) error {
	if value == "" {
		return fmt.Errorf("password is required")
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return fmt.Errorf("password contains control characters")
		}
	}
	if len(value) > 256 {
		return fmt.Errorf("password too long")
	}
	return nil
}

// validateTargetUser enforces security policies for target users.
// It blocks operations on 'root' and system users (UID < 1000).
func validateTargetUser(username string) error {
	if err := validateIdentifier("username", username); err != nil {
		return err
	}
	// Security: Prevent modifying root user
	if username == "root" {
		return fmt.Errorf("operation not permitted on root user")
	}

	// Security: Enforce Minimum UID
	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("invalid uid: %w", err)
	}
	if uid < minUID {
		return fmt.Errorf("operation not permitted on system user (uid < %d)", minUID)
	}
	return nil
}

// =============================================================================
// Auth Handlers (merged from auth.go)
// =============================================================================

// HandleVerifyUser handles the USER_VERIFY_PASSWORD action.
// It uses strict payload parsing.
func HandleVerifyUser(_ context.Context, req *execution.ExecuteRequest) (string, error) {
	p, err := execution.GetPayload[payload.UserPasswordVerify](req)
	if err != nil {
		return "", err
	}

	if p.Username == "" || p.Password == "" {
		return "", fmt.Errorf("missing required parameters: username and password")
	}

	success, reason := authenticateLocalUser(p.Username, p.Password)
	if success {
		return "authentication_successful", nil
	}

	reason = cmp.Or(reason, "authentication failed")
	return "", errors.New(reason)
}

// =============================================================================
// Password Authentication
// =============================================================================

// shadowEntry represents a user entry from /etc/shadow
type shadowEntry struct {
	Username       string
	HashedPassword string
	ExpireDate     string // Days since epoch when account expires
}

// errUserNotFoundInShadow is returned by readShadowEntry when the username is absent.
var errUserNotFoundInShadow = errors.New("user not found in shadow file")

// authenticateLocalUser verifying the password against /etc/shadow (needed root)
func authenticateLocalUser(username, password string) (bool, string) {
	startTime := time.Now()

	// Read shadow file entry
	entry, err := readShadowEntry(username)
	if err != nil {
		if errors.Is(err, errUserNotFoundInShadow) {
			return false, "user not found"
		}
		return false, "user lookup failed"
	}

	// Check account status
	accountStatus, isActive := getAccountStatus(entry)
	if !isActive {
		return false, accountStatus
	}

	// Verify password
	if !verifyPassword(password, entry.HashedPassword) {
		// Add artificial delay to prevent timing attacks
		time.Sleep(time.Duration(100+startTime.UnixNano()%100) * time.Millisecond)
		return false, "password incorrect"
	}

	return true, ""
}

func readShadowEntry(username string) (*shadowEntry, error) {
	file, err := os.Open(shadowFilePath)
	if err != nil {
		return nil, fmt.Errorf("cannot open shadow file: %w", err)
	}
	defer func(file *os.File) { _ = file.Close() }(file)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, username+":") {
			return parseShadowLine(line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading shadow file: %w", err)
	}

	return nil, errUserNotFoundInShadow
}

func parseShadowLine(line string) (*shadowEntry, error) {
	parts := strings.Split(line, ":")
	if len(parts) < 8 {
		return nil, fmt.Errorf("invalid shadow file format")
	}

	return &shadowEntry{
		Username:       parts[0],
		HashedPassword: parts[1],
		ExpireDate:     parts[7],
	}, nil
}

func getAccountStatus(entry *shadowEntry) (string, bool) {
	if entry.HashedPassword == "" {
		return "no_password_set", false
	}

	if len(entry.HashedPassword) > 0 {
		firstChar := entry.HashedPassword[0]
		switch firstChar {
		case '!':
			if len(entry.HashedPassword) >= 2 && entry.HashedPassword[1] == '*' {
				return "account_not_allowed_to_login", false
			}
			return "account_locked", false
		case '*':
			return "account_disabled", false
		default:
			// valid hash prefix — fall through to expiry check below
		}
	}

	if entry.ExpireDate != "" && entry.ExpireDate != "0" {
		if expireDate, err := strconv.ParseInt(entry.ExpireDate, 10, 64); err == nil && expireDate > 0 {
			currentDays := time.Now().Unix() / 86400
			if currentDays > expireDate {
				return "account_expired", false
			}
		}
	}

	return "active", true
}

func verifyPassword(password, hashedPassword string) bool {
	if len(hashedPassword) < 3 {
		return false
	}

	// Yescrypt ($y$) is handled via official Openwall implementation
	if strings.HasPrefix(hashedPassword, "$y$") {
		hash, err := yescrypt.Hash([]byte(password), []byte(hashedPassword))
		return err == nil && subtle.ConstantTimeCompare(hash, []byte(hashedPassword)) == 1
	}

	// SHA-512 ($6$) is verified via pure Go crypto
	return crypto.VerifyShadowHash(password, hashedPassword)
}
