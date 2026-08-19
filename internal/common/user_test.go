package common

import (
	"reflect"
	"testing"
)

func TestParseNumericPrefix(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		want    int
		wantErr bool
	}{
		{name: "with name", field: "uid=1000(alice)", want: 1000},
		{name: "without name", field: "gid=998", want: 998},
		{name: "different prefix", field: "用户=1000(alice)", want: 1000},
		{name: "no prefix", field: "1000(alice)", want: 1000},
		{name: "no name, no prefix", field: "1000", want: 1000},
		{name: "empty value", field: "uid=", wantErr: true},
		{name: "missing value", field: "uid=(alice)", wantErr: true},
		{name: "not numeric", field: "uid=abc(alice)", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseNumericPrefix(tt.field)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseGroups(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		want    []int
		wantErr bool
	}{
		{name: "multiple groups", field: "groups=998(wheel),9000(cad),150", want: []int{998, 9000, 150}},
		{name: "Chinese prefix", field: "组=998(wheel),9000(cad),150(wireshark)", want: []int{998, 9000, 150}},
		{name: "no prefix", field: "998(wheel),9000(cad),150", want: []int{998, 9000, 150}},
		{name: "empty groups", field: "groups=", want: nil},
		{name: "empty entry", field: "groups=998(wheel),,150", want: []int{998, 150}},
		{name: "bad group id", field: "groups=abc(wheel)", wantErr: true},
		{name: "missing value", field: "groups=(wheel)", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGroups(tt.field)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}
