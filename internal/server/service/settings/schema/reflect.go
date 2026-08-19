package schema

import (
	"reflect"
	"strconv"
	"strings"
)

// GenerateJSONSchema generates a basic JSON Schema-like structure from a given Go struct.
// It also extracts the current values of the struct and sets them as "default" in the schema.
func GenerateJSONSchema(obj any) map[string]any {
	return generateSchemaFromValue(reflect.ValueOf(obj))
}

func generateSchemaFromValue(v reflect.Value) map[string]any {
	if !v.IsValid() {
		return map[string]any{"type": "null"}
	}

	t := v.Type()

	// Resolve pointer
	if t.Kind() == reflect.Pointer {
		if v.IsNil() {
			t = t.Elem()
			// If pointer is nil, we can only infer type, no default value
			return generateSchemaFromType(t)
		}
		v = v.Elem()
		t = v.Type()
	}

	switch t.Kind() {
	case reflect.Struct:
		schema := map[string]any{
			"type":       "object",
			"properties": make(map[string]any),
		}
		props := schema["properties"].(map[string]any)
		for i := range t.NumField() {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}

			// Parse json tag
			jsonTag := field.Tag.Get("json")
			name := field.Name
			if jsonTag != "" {
				parts := strings.Split(jsonTag, ",")
				if parts[0] == "-" {
					continue
				}
				if parts[0] != "" {
					name = parts[0]
				}
			}

			// Recursively parse the field value
			fieldValue := v.Field(i)
			fieldSchema := generateSchemaFromValue(fieldValue)
			applyValidateTagToSchema(fieldSchema, field.Tag.Get("validate"))
			props[name] = fieldSchema
		}
		return schema

	case reflect.Slice, reflect.Array:
		schema := map[string]any{
			"type": "array",
		}
		if v.Len() > 0 {
			// For arrays, if there's an element, we can infer items type from the first element
			schema["items"] = generateSchemaFromValue(v.Index(0))
		} else {
			// If array is empty, fallback to type inspection
			schema["items"] = generateSchemaFromType(t.Elem())
		}
		return schema

	case reflect.String:
		return map[string]any{
			"type":    "string",
			"default": v.String(),
		}

	case reflect.Bool:
		return map[string]any{
			"type":    "boolean",
			"default": v.Bool(),
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{
			"type":    "number",
			"default": v.Int(),
		}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{
			"type":    "number",
			"default": v.Uint(),
		}

	case reflect.Float32, reflect.Float64:
		return map[string]any{
			"type":    "number",
			"default": v.Float(),
		}

	case reflect.Map:
		schema := map[string]any{
			"type": "object",
		}
		// Fallback to type inspection for map values
		schema["additionalProperties"] = generateSchemaFromType(t.Elem())
		return schema

	default:
		return map[string]any{"type": "any"}
	}
}

// generateSchemaFromType is a fallback for when we don't have a concrete value (e.g. empty slice)
func generateSchemaFromType(t reflect.Type) map[string]any {
	if t == nil {
		return map[string]any{"type": "null"}
	}

	// Resolve pointer
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Struct:
		schema := map[string]any{
			"type":       "object",
			"properties": make(map[string]any),
		}
		props := schema["properties"].(map[string]any)
		for field := range t.Fields() {
			if !field.IsExported() {
				continue
			}

			// Parse json tag
			jsonTag := field.Tag.Get("json")
			name := field.Name
			if jsonTag != "" {
				parts := strings.Split(jsonTag, ",")
				if parts[0] == "-" {
					continue
				}
				if parts[0] != "" {
					name = parts[0]
				}
			}
			fieldSchema := generateSchemaFromType(field.Type)
			applyValidateTagToSchema(fieldSchema, field.Tag.Get("validate"))
			props[name] = fieldSchema
		}
		return schema

	case reflect.Slice, reflect.Array:
		schema := map[string]any{
			"type":  "array",
			"items": generateSchemaFromType(t.Elem()),
		}
		return schema

	case reflect.String:
		return map[string]any{"type": "string"}

	case reflect.Bool:
		return map[string]any{"type": "boolean"}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}

	case reflect.Map:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": generateSchemaFromType(t.Elem()),
		}

	default:
		// Fallback for interfaces, channels, etc.
		return map[string]any{"type": "any"}
	}
}

func applyValidateTagToSchema(schema map[string]any, tag string) {
	if tag == "" || schema == nil {
		return
	}
	parts := strings.SplitSeq(tag, ",")
	for part := range parts {
		kv := strings.SplitN(part, "=", 2)
		key := strings.TrimSpace(kv[0])
		if key == "allowzero" {
			schema["allowZero"] = true
			continue
		}
		if len(kv) == 2 {
			val := strings.TrimSpace(kv[1])
			switch key {
			case "min":
				if num, err := strconv.ParseFloat(val, 64); err == nil {
					schema["minimum"] = num
				}
			case "max":
				if num, err := strconv.ParseFloat(val, 64); err == nil {
					schema["maximum"] = num
				}
			case "enum":
				// Simple enum parser, e.g., enum=-1|0|1
				choices := strings.Split(val, "|")
				var enumVals []any
				for _, c := range choices {
					if num, err := strconv.ParseFloat(c, 64); err == nil {
						enumVals = append(enumVals, num)
					} else {
						enumVals = append(enumVals, c)
					}
				}
				if len(enumVals) > 0 {
					schema["enum"] = enumVals
				}
			}
		}
	}
}
