package schema

import (
	"fmt"
	"reflect"
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
			applyValidateTagToSchema(fieldSchema, field.Tag.Get("validate"), field.Type)
			applyUnitTagToSchema(fieldSchema, field.Tag.Get("unit"))
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
			"type":       "object",
			"properties": make(map[string]any),
		}
		props := schema["properties"].(map[string]any)
		if t.Key().Kind() == reflect.String {
			for _, key := range v.MapKeys() {
				props[key.String()] = generateSchemaFromValue(v.MapIndex(key))
			}
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
			applyValidateTagToSchema(fieldSchema, field.Tag.Get("validate"), field.Type)
			applyUnitTagToSchema(fieldSchema, field.Tag.Get("unit"))
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

// applyValidateTagToSchema projects parseValidateTag onto a JSON-Schema node.
// For primitive slices/arrays, min/max/enum land on items; unit stays on the array.
// Invalid tags panic: schema generation runs on compile-time struct definitions.
func applyValidateTagToSchema(schema map[string]any, tag string, typ reflect.Type) {
	if tag == "" || schema == nil || typ == nil {
		return
	}
	typ = unwrapType(typ)
	kind := typ.Kind()
	target := schema

	if kind == reflect.Slice || kind == reflect.Array {
		elem := unwrapType(typ.Elem())
		if elem == nil {
			return
		}
		if isStructOrStructPtr(elem) {
			// Element constraints come from the element struct fields.
			return
		}
		items, ok := schema["items"].(map[string]any)
		if !ok {
			items = map[string]any{}
			schema["items"] = items
		}
		target = items
		kind = elem.Kind()
	}

	c, err := parseValidateTag(tag, kind)
	if err != nil {
		panic(fmt.Sprintf("invalid validate tag %q on %s: %v", tag, typ, err))
	}
	if c.AllowZero {
		// allowZero stays on the field schema (including arrays), not on items.
		schema["allowZero"] = true
	}
	if min, ok := c.schemaMinimum(); ok {
		target["minimum"] = min
	}
	if max, ok := c.schemaMaximum(); ok {
		target["maximum"] = max
	}
	if minLen, ok := c.schemaMinLength(); ok {
		target["minLength"] = minLen
	}
	if maxLen, ok := c.schemaMaxLength(); ok {
		target["maxLength"] = maxLen
	}
	if enums := c.schemaEnumValues(); len(enums) > 0 {
		target["enum"] = enums
	}
}

func applyUnitTagToSchema(schema map[string]any, tag string) {
	if tag = strings.TrimSpace(tag); tag != "" && schema != nil {
		schema["unit"] = tag
	}
}
