package db

import (
	"context"
	"reflect"

	"design-cloud-engine/internal/platform/jsoncodec"

	"gorm.io/gorm/schema"
)

func init() {
	schema.RegisterSerializer("json", codecJSONSerializer{})
}

// codecJSONSerializer mirrors GORM's JSONSerializer but uses jsoncodec (json/v2).
type codecJSONSerializer struct{}

func (codecJSONSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue any) (err error) {
	fieldValue := reflect.New(field.FieldType)

	if dbValue != nil {
		var bytes []byte
		switch v := dbValue.(type) {
		case []byte:
			bytes = v
		case string:
			bytes = []byte(v)
		default:
			bytes, err = jsoncodec.Marshal(v)
			if err != nil {
				return err
			}
		}

		if len(bytes) > 0 {
			err = jsoncodec.Unmarshal(bytes, fieldValue.Interface())
		}
	}

	field.ReflectValueOf(ctx, dst).Set(fieldValue.Elem())
	return
}

func (codecJSONSerializer) Value(ctx context.Context, field *schema.Field, dst reflect.Value, fieldValue any) (any, error) {
	result, err := jsoncodec.Marshal(fieldValue)
	if string(result) == "null" {
		if field.TagSettings["NOT NULL"] != "" {
			return "", nil
		}
		return nil, err
	}
	return string(result), err
}
