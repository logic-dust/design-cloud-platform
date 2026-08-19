package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"design-cloud-engine/internal/config"
)

func flagsWith(names ...string) *Flags {
	f := &Flags{explicit: make(map[string]bool), Port: config.DefaultClusterPort}
	for _, n := range names {
		f.explicit[n] = true
	}
	return f
}

func TestResolveStartMode(t *testing.T) {
	t.Run("conflict fails", func(t *testing.T) {
		if _, err := ResolveStartMode(flagsWith("config", "servers")); err == nil {
			t.Fatal("expected conflict error for --config + --servers")
		}
	})

	t.Run("only config wins", func(t *testing.T) {
		mode, err := ResolveStartMode(flagsWith("config"))
		if err != nil || mode != StartModeConfig {
			t.Fatalf("mode=%v err=%v", mode, err)
		}
	})

	t.Run("only servers selects configless", func(t *testing.T) {
		mode, err := ResolveStartMode(flagsWith("servers"))
		if err != nil || mode != StartModeConfigless {
			t.Fatalf("mode=%v err=%v", mode, err)
		}
	})

	t.Run("port without servers fails", func(t *testing.T) {
		if _, err := ResolveStartMode(flagsWith("port")); err == nil {
			t.Fatal("expected error for --port without --servers")
		} else if !strings.Contains(err.Error(), "--servers") {
			t.Fatalf("error must mention --servers, got: %v", err)
		}
	})

	t.Run("data-dir without servers fails", func(t *testing.T) {
		if _, err := ResolveStartMode(flagsWith("data-dir")); err == nil {
			t.Fatal("expected error for --data-dir without --servers")
		}
	})

	t.Run("config with port fails", func(t *testing.T) {
		if _, err := ResolveStartMode(flagsWith("config", "port")); err == nil {
			t.Fatal("expected error for --config with --port")
		}
	})

	t.Run("explicit empty config counts as provided", func(t *testing.T) {
		f := &Flags{explicit: map[string]bool{"config": true}, Port: config.DefaultClusterPort}
		mode, err := ResolveStartMode(f)
		if err != nil || mode != StartModeConfig {
			t.Fatalf("mode=%v err=%v", mode, err)
		}
	})

	t.Run("no declarations without config.toml fails", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if _, err := ResolveStartMode(flagsWith()); err == nil {
			t.Fatal("expected error when no mode declared and config.toml missing")
		} else if !strings.Contains(err.Error(), "--servers") || !strings.Contains(err.Error(), "--config") {
			t.Fatalf("error must show both usage examples, got: %v", err)
		}
	})

	t.Run("no declarations with config.toml selects config mode", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(""), 0600); err != nil {
			t.Fatalf("write config.toml: %v", err)
		}
		mode, err := ResolveStartMode(flagsWith())
		if err != nil || mode != StartModeConfig {
			t.Fatalf("mode=%v err=%v", mode, err)
		}
	})
}

func TestParseFlagsExplicit(t *testing.T) {
	flags, rest, err := ParseFlags(
		[]string{"--config", "", "--servers", "mgt01,mgt02", "--log-dir", "/tmp/logs", "--port", "55051"},
		os.Stderr, "test",
	)
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("rest = %v, want empty", rest)
	}
	for _, name := range []string{"config", "servers", "log-dir", "port"} {
		if !flags.Explicit(name) {
			t.Fatalf("--%s must be recorded as explicit", name)
		}
	}
	if flags.Explicit("data-dir") {
		t.Fatal("--data-dir must not be explicit")
	}
	if flags.ConfigFile != "" || flags.Servers != "mgt01,mgt02" || flags.Port != 55051 || flags.LogDir != "/tmp/logs" {
		t.Fatalf("unexpected flags: %+v", flags)
	}
}

func TestParseFlagsPortDefaultsToClusterDefault(t *testing.T) {
	flags, _, err := ParseFlags([]string{"--servers", "mgt01"}, os.Stderr, "test")
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if flags.Port != config.DefaultClusterPort {
		t.Fatalf("Port = %d, want flag default %d", flags.Port, config.DefaultClusterPort)
	}
	if flags.Explicit("port") {
		t.Fatal("--port must not be explicit when it was not passed")
	}
}

func TestParseFlagsRejectsCertOverrides(t *testing.T) {
	if _, _, err := ParseFlags([]string{"--servers", "mgt01", "--cert", "x.crt"}, os.Stderr, "test"); err == nil {
		t.Fatal("expected unknown flag error for --cert")
	}
}

func TestStartArgv(t *testing.T) {
	f := &Flags{Servers: "mgt01,mgt02", Port: 52255, DataDir: "/var/lib/design-cloud", LogLevel: "INFO", explicit: map[string]bool{"servers": true}}
	got, err := StartArgv(f, StartModeConfigless)
	if err != nil {
		t.Fatalf("StartArgv: %v", err)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--servers mgt01,mgt02") || !strings.Contains(joined, "--port 52255") {
		t.Fatalf("join argv incomplete: %v", got)
	}
}

func TestValidatePortalURL(t *testing.T) {
	// HTTPS should always be valid
	validHTTPS := []string{"https://mgt01:8443", "https://[::1]:8443", "https://portal.example.com"}
	for _, raw := range validHTTPS {
		if err := validatePortalURL(raw, false); err != nil {
			t.Fatalf("validatePortalURL(%q, insecureHTTP=false) unexpected error: %v", raw, err)
		}
		if err := validatePortalURL(raw, true); err != nil {
			t.Fatalf("validatePortalURL(%q, insecureHTTP=true) unexpected error: %v", raw, err)
		}
	}

	// Plain HTTP must be rejected when insecureHTTP=false
	plainHTTP := []string{"http://mgt01", "http://mgt01:8443", "http://127.0.0.1:8080"}
	for _, raw := range plainHTTP {
		err := validatePortalURL(raw, false)
		if err == nil {
			t.Fatalf("validatePortalURL(%q, insecureHTTP=false) expected error for plain HTTP", raw)
		}
		if !strings.Contains(err.Error(), "insecure http portal URL rejected") {
			t.Fatalf("validatePortalURL(%q, insecureHTTP=false) error must mention rejection, got: %v", raw, err)
		}
		// Plain HTTP must be accepted when insecureHTTP=true
		if err := validatePortalURL(raw, true); err != nil {
			t.Fatalf("validatePortalURL(%q, insecureHTTP=true) unexpected error: %v", raw, err)
		}
	}

	invalid := []string{"mgt01", "mgt01:8443", "ftp://mgt01", "https://"}
	for _, raw := range invalid {
		if err := validatePortalURL(raw, false); err == nil {
			t.Fatalf("validatePortalURL(%q, insecureHTTP=false) expected error", raw)
		}
		if err := validatePortalURL(raw, true); err == nil {
			t.Fatalf("validatePortalURL(%q, insecureHTTP=true) expected error", raw)
		}
	}
}

func TestRegisterRequiresPortalURL(t *testing.T) {
	err := runRegisterCommand([]string{"--token", "dummy"})
	if err == nil {
		t.Fatal("register without --portal-url must fail")
	}
	if !strings.Contains(err.Error(), "--portal-url") {
		t.Fatalf("error must mention --portal-url, got: %v", err)
	}
}

func TestRegisterInsecureHTTPRejection(t *testing.T) {
	// Rejects plain http without --insecure-http
	err := runRegisterCommand([]string{"--token", "dummy", "--portal-url", "http://mgt01:8443"})
	if err == nil {
		t.Fatal("register with plain http without --insecure-http must fail")
	}
	if !strings.Contains(err.Error(), "insecure http portal URL rejected") {
		t.Fatalf("error must mention insecure http rejection, got: %v", err)
	}

	// With --insecure-http, url validation passes (fails on directory or connection, not URL validation)
	tempDataDir := t.TempDir()
	errWithOverride := runRegisterCommand([]string{
		"--token", "dummy",
		"--portal-url", "http://127.0.0.1:59999",
		"--insecure-http",
		"--data-dir", tempDataDir,
	})
	if errWithOverride == nil {
		t.Fatal("expected failure due to unreachable server, but got nil")
	}
	if strings.Contains(errWithOverride.Error(), "insecure http portal URL rejected") {
		t.Fatalf("--insecure-http flag should have bypassed insecure rejection, got: %v", errWithOverride)
	}
}
