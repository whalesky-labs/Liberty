package remote

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
)

// decodeWire preserves Serde's case-sensitive field names and nullable pointer
// semantics. encoding/json's struct decoder also accepts case-folded names,
// including Unicode aliases, which must remain unknown protocol extensions.
// Decode into a fresh value so a rejected response never publishes partial data.
func decodeWire(raw []byte, destination any) bool {
	value := reflect.ValueOf(destination)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return false
	}
	next := reflect.New(value.Elem().Type()).Elem()
	if !decodeValue(raw, next) {
		return false
	}
	value.Elem().Set(next)
	return true
}

func decodeValue(raw []byte, value reflect.Value) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return value.Kind() == reflect.Pointer
	}
	switch value.Kind() {
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		return decodeValue(raw, value.Elem())
	case reflect.Struct:
		fields, ok := object(raw)
		return ok && decodeFields(fields, value)
	case reflect.Slice:
		var elements []json.RawMessage
		if json.Unmarshal(raw, &elements) != nil {
			return false
		}
		value.Set(reflect.MakeSlice(value.Type(), len(elements), len(elements)))
		for i, raw := range elements {
			if !decodeValue(raw, value.Index(i)) {
				return false
			}
		}
		return true
	default:
		// Decode directly into the declared scalar type, retaining u64/i64
		// precision and rejecting fractional or out-of-range integers.
		return json.Unmarshal(raw, value.Addr().Interface()) == nil
	}
}

func decodeFields(fields map[string]json.RawMessage, value reflect.Value) bool {
	kind := value.Type()
	for i := 0; i < kind.NumField(); i++ {
		field := kind.Field(i)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.Anonymous && name == "" {
			if !decodeFields(fields, value.Field(i)) {
				return false
			}
			continue
		}
		if name == "" || name == "-" {
			continue
		}
		if raw, exists := fields[name]; exists && !decodeValue(raw, value.Field(i)) {
			return false
		}
	}
	return true
}
