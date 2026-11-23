// Package contracts validates wire data using the repository's shared JSON Schema.
package contracts

import (
	"bytes"
	"errors"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var ErrInvalid = errors.New("contract_invalid")

type Validator struct{ schemas map[string]*jsonschema.Schema }

var Shared = sync.OnceValues(compile)

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) { return nil, errors.New("external_schema_disabled") }

func compile() (*Validator, error) {
	c := jsonschema.NewCompiler()
	c.UseLoader(offlineLoader{})
	for id, text := range schemas {
		document, err := jsonschema.UnmarshalJSON(strings.NewReader(text))
		if err != nil {
			return nil, ErrInvalid
		}
		if c.AddResource(id, document) != nil {
			return nil, ErrInvalid
		}
	}
	v := &Validator{schemas: make(map[string]*jsonschema.Schema)}
	for id := range schemas {
		schema, err := c.Compile(id)
		if err != nil {
			return nil, ErrInvalid
		}
		v.schemas[id] = schema
	}
	return v, nil
}

func (v *Validator) Validate(id string, data []byte) error {
	schema, ok := v.schemas[id]
	if !ok {
		return ErrInvalid
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil || schema.Validate(value) != nil {
		return ErrInvalid
	}
	return nil
}
