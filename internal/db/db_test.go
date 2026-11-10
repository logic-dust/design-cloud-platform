package db

import (
	"net/url"
	"testing"

	"design-cloud-engine/internal/platform/config"
)

func TestPostgresDSNEscapesConnectionValues(t *testing.T) {
	cfg := config.Database{
		Host:      "2001:db8::1",
		Port:      "5432",
		User:      "service user",
		Password:  `p@ss:/?#% 'word`,
		Name:      "engine db",
		SSLMode:   "verify-full",
		SSLCAFile: "/etc/ssl/my ca.pem",
	}

	dsn, err := url.Parse(postgresDSN(cfg))
	if err != nil {
		t.Fatalf("parse generated DSN: %v", err)
	}
	password, ok := dsn.User.Password()
	if !ok || dsn.User.Username() != cfg.User || password != cfg.Password {
		t.Fatalf("generated DSN lost credentials: user=%q password=%q", dsn.User.Username(), password)
	}
	if dsn.Host != "[2001:db8::1]:5432" {
		t.Fatalf("host = %q, want bracketed IPv6 host and port", dsn.Host)
	}
	if dsn.Path != "/"+cfg.Name {
		t.Fatalf("database path = %q, want %q", dsn.Path, "/"+cfg.Name)
	}
	if got := dsn.Query().Get("sslmode"); got != cfg.SSLMode {
		t.Fatalf("sslmode = %q, want %q", got, cfg.SSLMode)
	}
	if got := dsn.Query().Get("sslrootcert"); got != cfg.SSLCAFile {
		t.Fatalf("sslrootcert = %q, want %q", got, cfg.SSLCAFile)
	}
}

func TestResolveListLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want int
	}{
		{0, DefaultListLimit},
		{-1, DefaultListLimit},
		{1, 1},
		{50, 50},
		{2000, MaxListLimit},
		{5000, MaxListLimit},
	}
	for _, tt := range tests {
		if got := ResolveListLimit(tt.in); got != tt.want {
			t.Errorf("ResolveListLimit(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestCapListLimit(t *testing.T) {
	t.Parallel()
	if CapListLimit(500) != 500 || CapListLimit(2000) != MaxListLimit {
		t.Fatalf("CapListLimit unexpected")
	}
}

func TestPageSlice(t *testing.T) {
	t.Parallel()
	items := []int{0, 1, 2, 3, 4}
	page, total := PageSlice(items, 2, 0)
	if total != 5 || len(page) != 2 || page[0] != 0 || page[1] != 1 {
		t.Fatalf("first page: page=%v total=%d", page, total)
	}
	page, total = PageSlice(items, 2, 4)
	if total != 5 || len(page) != 1 || page[0] != 4 {
		t.Fatalf("last page: page=%v total=%d", page, total)
	}
	page, total = PageSlice(items, 2, 10)
	if total != 5 || len(page) != 0 {
		t.Fatalf("past end: page=%v total=%d", page, total)
	}
}
