package jsoncodec

import (
	"bytes"
	"testing"
)

type marshalJSONString string

func (s marshalJSONString) MarshalJSON() ([]byte, error) {
	return []byte(`"` + string(s) + `"`), nil
}

func TestMarshal_nilSliceNull(t *testing.T) {
	var s struct {
		Items []string `json:"items"`
	}
	b, err := Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"items":null}` {
		t.Fatalf("got %s", b)
	}
}

func TestMarshal_enumCustomJSON(t *testing.T) {
	b, err := Marshal(marshalJSONString("active"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"active"` {
		t.Fatalf("got %s", b)
	}
}

func TestUnmarshalStrict_rejectsUnknownField(t *testing.T) {
	var s struct {
		Name string `json:"name"`
	}
	err := UnmarshalStrict([]byte(`{"name":"x","extra":1}`), &s)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUnmarshalStrict_rejectsTrailingValue(t *testing.T) {
	var s struct {
		Name string `json:"name"`
	}
	err := UnmarshalStrict([]byte(`{"name":"x"} {}`), &s)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCompact(t *testing.T) {
	var dst bytes.Buffer
	src := []byte(`{ "a" : 1 }`)
	if err := Compact(&dst, src); err != nil {
		t.Fatal(err)
	}
	if dst.String() != `{"a":1}` {
		t.Fatalf("got %q", dst.String())
	}
}
