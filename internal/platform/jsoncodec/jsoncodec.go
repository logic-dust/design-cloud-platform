// Package jsoncodec is the single JSON boundary for design-cloud-engine.
// It uses encoding/json/v2 with a small set of marshal options that preserve
// historical wire shapes for nil slices and maps.
package jsoncodec

import (
	"bytes"
	"fmt"
	"io"

	stdjson "encoding/json"

	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// RawMessage re-exports encoding/json.RawMessage so importers can use
// jsoncodec as their sole JSON import (commonly aliased as `json`).
type RawMessage = stdjson.RawMessage

var (
	defaultMarshalOpts = jsonv2.JoinOptions(
		jsonv2.FormatNilSliceAsNull(true),
		jsonv2.FormatNilMapAsNull(true),
		jsonv2.Deterministic(true),
	)
)

// Marshal encodes v as JSON using project-default marshal semantics.
func Marshal(v any) ([]byte, error) {
	return jsonv2.Marshal(v, defaultMarshalOpts)
}

// Unmarshal decodes JSON into v using v2 default unmarshal semantics.
func Unmarshal(data []byte, v any, opts ...jsonv2.Options) error {
	all := jsonv2.JoinOptions(opts...)
	return jsonv2.Unmarshal(data, v, all)
}

// UnmarshalStrict decodes a single JSON value and rejects unknown struct fields and trailing values.
func UnmarshalStrict(data []byte, v any) error {
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	opts := jsonv2.JoinOptions(jsonv2.RejectUnknownMembers(true))
	if err := jsonv2.UnmarshalDecode(dec, v, opts); err != nil {
		return err
	}
	if _, err := dec.ReadToken(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON after first value")
		}
		return err
	}
	return nil
}

// UnmarshalStrictRead reads all of r and applies UnmarshalStrict.
func UnmarshalStrictRead(r io.Reader, v any) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return UnmarshalStrict(data, v)
}

// UnmarshalRead decodes a single JSON value from r until EOF.
func UnmarshalRead(r io.Reader, v any) error {
	return jsonv2.UnmarshalRead(r, v)
}

// MarshalIndent is like Marshal but with indented output.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return jsonv2.Marshal(v, jsonv2.JoinOptions(
		defaultMarshalOpts,
		jsontext.Multiline(true),
		jsontext.WithIndentPrefix(prefix),
		jsontext.WithIndent(indent),
	))
}

// Valid reports whether data is syntactically valid JSON.
func Valid(data []byte) bool {
	return jsontext.Value(data).IsValid()
}

// Compact appends compacted JSON to dst.
func Compact(dst *bytes.Buffer, src []byte) error {
	dst.Grow(len(src))
	b := dst.AvailableBuffer()
	b, err := jsontext.AppendFormat(b, src,
		jsontext.AllowDuplicateNames(true),
		jsontext.AllowInvalidUTF8(true),
		jsontext.PreserveRawStrings(true),
	)
	if err != nil {
		return err
	}
	dst.Write(b)
	return nil
}
