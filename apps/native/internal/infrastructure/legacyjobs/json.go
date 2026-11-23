package legacyjobs

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
)

// Legacy files deserialize directly into Rust structs. Unlike remote jobs
// (which first pass through serde_json::Value), duplicate known fields must be
// rejected before an object map can collapse them. Unknown extensions remain
// ignored, including keys that differ only in case from a known field.
func decodeLegacy(raw []byte, destination any) bool {
	value := reflect.ValueOf(destination).Elem()
	next := reflect.New(value.Type()).Elem()
	// The callers seed scalar defaults and empty slices before decoding. A
	// present pointer or slice is allocated afresh, preserving failure atomicity.
	next.Set(value)
	if !decodeLegacyValue(raw, next) {
		return false
	}
	value.Set(next)
	return true
}

func decodeLegacyValue(raw []byte, value reflect.Value) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if value.Kind() != reflect.Pointer {
			return false
		}
		value.SetZero()
		return true
	}
	switch value.Kind() {
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		return decodeLegacyValue(raw, value.Elem())
	case reflect.Struct:
		decoder := json.NewDecoder(bytes.NewReader(raw))
		start, err := decoder.Token()
		if err != nil || start != json.Delim('{') {
			return false
		}
		fields := legacyFields(value.Type())
		seen := map[string]bool{}
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return false
			}
			name, ok := token.(string)
			var content json.RawMessage
			if !ok || decoder.Decode(&content) != nil {
				return false
			}
			if index, known := fields[name]; known {
				if seen[name] || !decodeLegacyValue(content, value.FieldByIndex(index)) {
					return false
				}
				seen[name] = true
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return false
		}
		_, err = decoder.Token()
		return err == io.EOF
	case reflect.Slice:
		var elements []json.RawMessage
		if json.Unmarshal(raw, &elements) != nil {
			return false
		}
		value.Set(reflect.MakeSlice(value.Type(), len(elements), len(elements)))
		for i, element := range elements {
			if !decodeLegacyValue(element, value.Index(i)) {
				return false
			}
		}
		return true
	default:
		return json.Unmarshal(raw, value.Addr().Interface()) == nil
	}
}

func legacyFields(kind reflect.Type) map[string][]int {
	fields := map[string][]int{}
	// Flatten embedded DTOs first; a direct field overrides a promoted field
	// with the same JSON name (legacySnapshot.Files retains private path data).
	for i := 0; i < kind.NumField(); i++ {
		field := kind.Field(i)
		if field.Anonymous && field.Type.Kind() == reflect.Struct && field.Tag.Get("json") == "" {
			for name, index := range legacyFields(field.Type) {
				fields[name] = append([]int{i}, index...)
			}
		}
	}
	for i := 0; i < kind.NumField(); i++ {
		field := kind.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.IsExported() && name != "" && name != "-" {
			fields[name] = []int{i}
		}
	}
	return fields
}
