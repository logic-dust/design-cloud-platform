package schema

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// fieldConstraints is the parsed form of a `validate` struct tag for one field
// (or one primitive-slice element). ValidateStructByTags and GenerateJSONSchema
// both consume this type so min/max/enum cannot drift between the two paths.
type fieldConstraints struct {
	OmitEmpty bool
	AllowZero bool
	HasMin    bool
	HasMax    bool
	MinInt    int64
	MaxInt    int64
	MinUint   uint64
	MaxUint   uint64
	MinFloat  float64
	MaxFloat  float64
	Enum      []string
	HasMinLen bool
	HasMaxLen bool
	MinLen    int
	MaxLen    int
	// numberClass selects which min/max pair applies: "int", "uint", "float", or "".
	numberClass string
}

func unwrapType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func numberClassOf(kind reflect.Kind) string {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "uint"
	case reflect.Float32, reflect.Float64:
		return "float"
	default:
		return ""
	}
}

func isStructOrStructPtr(t reflect.Type) bool {
	t = unwrapType(t)
	return t != nil && t.Kind() == reflect.Struct
}

// parseValidateTag parses a validate tag against the Go kind of the value it constrains.
// Combining enum with min/max or minLen/maxLen is rejected. min/max on non-numeric kinds
// and minLen/maxLen on non-string kinds are rejected. Malformed bounds return an error.
func parseValidateTag(tag string, kind reflect.Kind) (fieldConstraints, error) {
	var c fieldConstraints
	if tag == "" {
		return c, nil
	}
	c.numberClass = numberClassOf(kind)

	var hasEnum bool
	for part := range strings.SplitSeq(tag, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		key := strings.TrimSpace(kv[0])
		if key == "" {
			continue
		}
		switch key {
		case "omitempty":
			c.OmitEmpty = true
			continue
		case "allowzero":
			c.AllowZero = true
			continue
		}
		if len(kv) != 2 {
			return fieldConstraints{}, fmt.Errorf("unknown validate token %q", key)
		}
		valStr := strings.TrimSpace(kv[1])
		switch key {
		case "min":
			if c.numberClass == "" {
				return fieldConstraints{}, fmt.Errorf("min is not valid for %s", kind)
			}
			c.HasMin = true
			if err := parseBound(&c, true, valStr); err != nil {
				return fieldConstraints{}, err
			}
		case "max":
			if c.numberClass == "" {
				return fieldConstraints{}, fmt.Errorf("max is not valid for %s", kind)
			}
			c.HasMax = true
			if err := parseBound(&c, false, valStr); err != nil {
				return fieldConstraints{}, err
			}
		case "minLen":
			if kind != reflect.String {
				return fieldConstraints{}, fmt.Errorf("minLen is not valid for %s", kind)
			}
			c.HasMinLen = true
			if err := parseLenBound(&c, true, valStr); err != nil {
				return fieldConstraints{}, err
			}
		case "maxLen":
			if kind != reflect.String {
				return fieldConstraints{}, fmt.Errorf("maxLen is not valid for %s", kind)
			}
			c.HasMaxLen = true
			if err := parseLenBound(&c, false, valStr); err != nil {
				return fieldConstraints{}, err
			}
		case "enum":
			hasEnum = true
			if valStr == "" {
				return fieldConstraints{}, fmt.Errorf("enum must list at least one value")
			}
			c.Enum = strings.Split(valStr, "|")
		default:
			return fieldConstraints{}, fmt.Errorf("unknown validate key %q", key)
		}
	}

	if hasEnum && (c.HasMin || c.HasMax || c.HasMinLen || c.HasMaxLen) {
		return fieldConstraints{}, fmt.Errorf("enum cannot be combined with min/max or minLen/maxLen")
	}
	if c.AllowZero && c.numberClass == "" {
		return fieldConstraints{}, fmt.Errorf("allowzero is not valid for %s", kind)
	}
	if c.HasMin && c.HasMax {
		switch c.numberClass {
		case "int":
			if c.MinInt > c.MaxInt {
				return fieldConstraints{}, fmt.Errorf("min %d exceeds max %d", c.MinInt, c.MaxInt)
			}
		case "uint":
			if c.MinUint > c.MaxUint {
				return fieldConstraints{}, fmt.Errorf("min %d exceeds max %d", c.MinUint, c.MaxUint)
			}
		case "float":
			if c.MinFloat > c.MaxFloat {
				return fieldConstraints{}, fmt.Errorf("min %v exceeds max %v", c.MinFloat, c.MaxFloat)
			}
		}
	}
	if c.HasMinLen && c.HasMaxLen && c.MinLen > c.MaxLen {
		return fieldConstraints{}, fmt.Errorf("minLen %d exceeds maxLen %d", c.MinLen, c.MaxLen)
	}
	return c, nil
}

func parseLenBound(c *fieldConstraints, isMin bool, valStr string) error {
	n, err := strconv.Atoi(valStr)
	if err != nil || n < 0 {
		return fmt.Errorf("invalid length bound %q", valStr)
	}
	if isMin {
		c.MinLen = n
	} else {
		c.MaxLen = n
	}
	return nil
}

func parseBound(c *fieldConstraints, isMin bool, valStr string) error {
	switch c.numberClass {
	case "int":
		n, err := strconv.ParseInt(valStr, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid integer bound %q: %w", valStr, err)
		}
		if isMin {
			c.MinInt = n
		} else {
			c.MaxInt = n
		}
	case "uint":
		n, err := strconv.ParseUint(valStr, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid unsigned bound %q: %w", valStr, err)
		}
		if isMin {
			c.MinUint = n
		} else {
			c.MaxUint = n
		}
	case "float":
		n, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			return fmt.Errorf("invalid float bound %q: %w", valStr, err)
		}
		if isMin {
			c.MinFloat = n
		} else {
			c.MaxFloat = n
		}
	default:
		return fmt.Errorf("bounds require a numeric kind")
	}
	return nil
}

func (c fieldConstraints) hasConstraints() bool {
	return c.HasMin || c.HasMax || c.HasMinLen || c.HasMaxLen || len(c.Enum) > 0 || c.AllowZero
}

func (c fieldConstraints) schemaMinLength() (int, bool) {
	if !c.HasMinLen {
		return 0, false
	}
	return c.MinLen, true
}

func (c fieldConstraints) schemaMaxLength() (int, bool) {
	if !c.HasMaxLen {
		return 0, false
	}
	return c.MaxLen, true
}

// schemaMinimum returns the JSON-Schema minimum value typed for the field kind.
func (c fieldConstraints) schemaMinimum() (any, bool) {
	if !c.HasMin {
		return nil, false
	}
	switch c.numberClass {
	case "int":
		return c.MinInt, true
	case "uint":
		return c.MinUint, true
	case "float":
		return c.MinFloat, true
	default:
		return nil, false
	}
}

func (c fieldConstraints) schemaMaximum() (any, bool) {
	if !c.HasMax {
		return nil, false
	}
	switch c.numberClass {
	case "int":
		return c.MaxInt, true
	case "uint":
		return c.MaxUint, true
	case "float":
		return c.MaxFloat, true
	default:
		return nil, false
	}
}

// schemaEnumValues converts enum tag tokens into JSON-Schema enum entries,
// preferring the field's numeric type when the token parses cleanly.
func (c fieldConstraints) schemaEnumValues() []any {
	if len(c.Enum) == 0 {
		return nil
	}
	out := make([]any, 0, len(c.Enum))
	for _, token := range c.Enum {
		switch c.numberClass {
		case "int":
			if n, err := strconv.ParseInt(token, 10, 64); err == nil {
				out = append(out, n)
				continue
			}
		case "uint":
			if n, err := strconv.ParseUint(token, 10, 64); err == nil {
				out = append(out, n)
				continue
			}
		case "float":
			if n, err := strconv.ParseFloat(token, 64); err == nil {
				out = append(out, n)
				continue
			}
		}
		out = append(out, token)
	}
	return out
}
