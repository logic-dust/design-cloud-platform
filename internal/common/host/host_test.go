package host

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidateHost_Valid(t *testing.T) {
	t.Parallel()

	valid := []string{
		"localhost",
		"myhost",
		"example.com",
		"sub.example.com",
		"192.168.1.1",
		"::1",
		"fe80::1",
		"node-01",
		"mgt01",
		"a", // single char
	}
	for _, h := range valid {
		if err := ValidateHost(h); err != nil {
			t.Errorf("ValidateHost(%q) = %v, want nil", h, err)
		}
	}
}

func TestValidateHost_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host    string
		contain string // expected error substring
	}{
		{"http://example.com", "scheme"},
		{"https://example.com", "scheme"},
		{"host/path", "path"},
		{"host\\path", "path"},
		{"host?query=1", "query"},
		{"host#frag", "fragment"},
		{"user@host", "userinfo"},
		{"[host]", "brackets"},
		{"host:8080", "port"},
		{"host\tname", "whitespace"},
		{"host\nname", "whitespace"},
		{"host name", "whitespace"},
	}
	for _, tc := range tests {
		err := ValidateHost(tc.host)
		if err == nil {
			t.Errorf("ValidateHost(%q) = nil, want error containing %q", tc.host, tc.contain)
		} else if !errors.Is(err, errInvalidHost) {
			t.Errorf("ValidateHost(%q) error does not wrap errInvalidHost: %v", tc.host, err)
		}
	}
}

func TestValidateHost_IPv6(t *testing.T) {
	t.Parallel()

	// IPv6 with brackets should be rejected (bare IP only, no brackets)
	if err := ValidateHost("[::1]"); err == nil {
		t.Error("ValidateHost([::1]) should fail")
	}
	// Bare IPv6 should be accepted
	if err := ValidateHost("::1"); err != nil {
		t.Errorf("ValidateHost(::1) = %v", err)
	}
}

func TestExpandHostPattern_Literal(t *testing.T) {
	t.Parallel()

	result, err := ExpandHostPattern("mgt01,mgt02,cn01")
	if err != nil {
		t.Fatalf("ExpandHostPattern: %v", err)
	}
	want := []string{"mgt01", "mgt02", "cn01"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("got %v, want %v", result, want)
	}
}

func TestExpandHostPattern_Range(t *testing.T) {
	t.Parallel()

	result, err := ExpandHostPattern("cn[01-03]")
	if err != nil {
		t.Fatalf("ExpandHostPattern: %v", err)
	}
	want := []string{"cn01", "cn02", "cn03"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("got %v, want %v", result, want)
	}
}

func TestExpandHostPattern_PreservesLeadingZeros(t *testing.T) {
	t.Parallel()

	result, err := ExpandHostPattern("cn[01-11]")
	if err != nil {
		t.Fatalf("ExpandHostPattern: %v", err)
	}
	want := []string{"cn01", "cn02", "cn03", "cn04", "cn05", "cn06", "cn07", "cn08", "cn09", "cn10", "cn11"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("got %v, want %v", result, want)
	}
}

func TestExpandHostPattern_NoLeadingZeros(t *testing.T) {
	t.Parallel()

	result, err := ExpandHostPattern("cn[1-3]")
	if err != nil {
		t.Fatalf("ExpandHostPattern: %v", err)
	}
	want := []string{"cn1", "cn2", "cn3"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("got %v, want %v", result, want)
	}
}

func TestExpandHostPattern_WithPrefixSuffix(t *testing.T) {
	t.Parallel()

	result, err := ExpandHostPattern("node-[1-2].local")
	if err != nil {
		t.Fatalf("ExpandHostPattern: %v", err)
	}
	want := []string{"node-1.local", "node-2.local"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("got %v, want %v", result, want)
	}
}

func TestExpandHostPattern_Mixed(t *testing.T) {
	t.Parallel()

	result, err := ExpandHostPattern("mgt01,cn[01-03],gw01")
	if err != nil {
		t.Fatalf("ExpandHostPattern: %v", err)
	}
	want := []string{"mgt01", "cn01", "cn02", "cn03", "gw01"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("got %v, want %v", result, want)
	}
}

func TestExpandHostPattern_SingleElement(t *testing.T) {
	t.Parallel()

	result, err := ExpandHostPattern("mgt01")
	if err != nil {
		t.Fatalf("ExpandHostPattern: %v", err)
	}
	want := []string{"mgt01"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("got %v, want %v", result, want)
	}
}

func TestExpandHostPattern_SingleRangeElement(t *testing.T) {
	t.Parallel()

	result, err := ExpandHostPattern("cn[05-05]")
	if err != nil {
		t.Fatalf("ExpandHostPattern: %v", err)
	}
	want := []string{"cn05"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("got %v, want %v", result, want)
	}
}

func TestExpandHostPattern_Errors(t *testing.T) {
	t.Parallel()

	tests := []string{
		"cn[10-01]", // start > end
	}
	for _, pattern := range tests {
		_, err := ExpandHostPattern(pattern)
		if err == nil {
			t.Errorf("ExpandHostPattern(%q) = nil, want error", pattern)
		}
	}
}

func TestExpandHostPattern_Empty(t *testing.T) {
	t.Parallel()

	result, err := ExpandHostPattern("")
	if err != nil {
		t.Fatalf("ExpandHostPattern: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("got %v, want empty", result)
	}
}

func TestExpandHostPattern_TrailingComma(t *testing.T) {
	t.Parallel()

	result, err := ExpandHostPattern("mgt01,")
	if err != nil {
		t.Fatalf("ExpandHostPattern: %v", err)
	}
	want := []string{"mgt01"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("got %v, want %v", result, want)
	}
}
