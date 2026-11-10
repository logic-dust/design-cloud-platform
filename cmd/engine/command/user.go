package command

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"design-cloud-engine/cmd/engine/console"
	"design-cloud-engine/internal/audit"
	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/model"
	usersvc "design-cloud-engine/internal/server/service/user"
)

type userPasswordStore interface {
	GetByName(ctx context.Context, username string) (model.User, error)
	Update(ctx context.Context, u *model.User) error
}

type userSessionRevoker interface {
	DeleteByUsername(ctx context.Context, username string) error
}

type dbSessionRevoker struct {
	repo *db.SessionRepo
}

func (r dbSessionRevoker) DeleteByUsername(ctx context.Context, username string) error {
	return r.repo.DeleteByUsername(ctx, username)
}

func runUserCommand(flags *Flags, args []string) error {
	if len(args) == 0 {
		printUserUsage()
		return fmt.Errorf("user sub-command required: set-password")
	}
	switch args[0] {
	case "set-password":
		return runUserSetPasswordCommand(flags, args[1:])
	default:
		printUserUsage()
		return fmt.Errorf("unknown user command: %s", args[0])
	}
}

func printUserUsage() {
	console.PrintStderr("Usage: %s user <set-password> [options]\n\n", config.AppName)
	console.PrintStderr("Subcommands:\n")
	console.PrintStderr("  set-password             Set password for an application user (type=app)\n")
}

func runUserSetPasswordCommand(flags *Flags, args []string) error {
	fs := newCommandFlagSet("set-password", os.Stderr)
	var username, configFile string
	var passwordStdin, expire bool
	fs.StringVar(&username, "username", "", "Target application username (e.g. dceadmin, systemadmin)")
	fs.StringVar(&configFile, "config", "", "Path to config file")
	fs.BoolVar(&passwordStdin, "password-stdin", false, "Read the new password from stdin (single line)")
	fs.BoolVar(&expire, "expire", false, "Mark password expired so the user must change it on next login")
	fs.Usage = func() {
		console.PrintStderr("Usage: %s user set-password --username <name> [--config <path>] [--password-stdin] [--expire]\n\n", config.AppName)
		console.PrintStderr("Set the password for an application user (UserTypeApp only; system LDAP/PAM users are rejected).\n")
		console.PrintStderr("Requires root or the program/config file owner.\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("set-password parse error: %w", err)
	}

	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("--username is required")
	}

	configPath := cmp.Or(configFile, flags.ConfigFile, config.DefaultConfigFile)
	if err := ensureSetPasswordPrivilege(configPath); err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	newPassword, err := readNewPassword(passwordStdin)
	if err != nil {
		return err
	}

	repos, auditSvc, cleanup, err := openOfflineReposAndAudit(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := setAppUserPassword(context.Background(), repos.User, dbSessionRevoker{repo: repos.Session}, auditSvc, username, newPassword, expire); err != nil {
		return err
	}

	console.PrintStdout("Password updated for application user %q (sessions revoked).\n", username)
	return nil
}

func setAppUserPassword(ctx context.Context, users userPasswordStore, sessions userSessionRevoker, auditSvc *audit.Service, username, newPassword string, expire bool) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("--username is required")
	}

	dbUser, err := users.GetByName(ctx, username)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return fmt.Errorf("user %q not found", username)
		}
		return fmt.Errorf("lookup user %q: %w", username, err)
	}
	if dbUser.Type != model.UserTypeApp {
		return fmt.Errorf("user %q is not an application user (type=%d); only type=app users can be updated via CLI", username, dbUser.Type)
	}

	if err := usersvc.ValidatePasswordPolicy(newPassword); err != nil {
		return err
	}
	hashed, err := usersvc.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	dbUser.Password = hashed
	if expire {
		dbUser.Status = model.UserPasswordExpired
	} else {
		dbUser.Status = model.UserActive
	}
	if err := users.Update(ctx, &dbUser); err != nil {
		return fmt.Errorf("update user %q: %w", username, err)
	}

	if sessions != nil {
		if err := sessions.DeleteByUsername(ctx, username); err != nil {
			return fmt.Errorf("revoke sessions for %q: %w", username, err)
		}
	}

	if auditSvc != nil {
		auditSvc.LogUser(ctx, "USER_PASSWORD_SET_CLI", "success", username, slog.LevelInfo, map[string]any{
			"user_type": "app",
			"expire":    expire,
		})
	}
	return nil
}

func readNewPassword(fromStdin bool) (string, error) {
	if fromStdin {
		line, err := readSingleLine(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		if line == "" {
			return "", fmt.Errorf("password must not be empty")
		}
		return line, nil
	}

	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("interactive hidden prompt is unsupported on Windows; pass --password-stdin")
	}
	info, err := os.Stdin.Stat()
	if err != nil || (info.Mode()&os.ModeCharDevice) == 0 {
		return "", fmt.Errorf("stdin is not a terminal; pass --password-stdin")
	}

	pwd1, err := promptHiddenLine("New password: ")
	if err != nil {
		return "", err
	}
	pwd2, err := promptHiddenLine("Confirm new password: ")
	if err != nil {
		return "", err
	}
	if pwd1 != pwd2 {
		return "", fmt.Errorf("passwords do not match")
	}
	if pwd1 == "" {
		return "", fmt.Errorf("password must not be empty")
	}
	return pwd1, nil
}

func promptHiddenLine(prompt string) (string, error) {
	console.PrintStderr("%s", prompt)
	_ = setStdinEcho(false)
	defer func() {
		_ = setStdinEcho(true)
		console.PrintStderr("\n")
	}()
	return readSingleLine(os.Stdin)
}

func setStdinEcho(enabled bool) error {
	arg := "-echo"
	if enabled {
		arg = "echo"
	}
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func readSingleLine(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
